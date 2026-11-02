package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSatisfiesByRemoteHTTPResponse(t *testing.T) {
	results := []Result{
		{Name: "bilibili", Status: Pass, HTTPCode: 503},
		{Name: "github", Status: Pass, HTTPCode: 401},
		{Name: "google", Status: Fail},
		{Name: "youtube", Status: Fail},
		{Name: "chatgpt", Status: Pass, HTTPCode: 403},
	}
	if !Satisfies(results) {
		t.Fatal("remote responses should satisfy the network condition")
	}
	results[4].HTTPCode = 0
	if Satisfies(results) {
		t.Fatal("no response from any third-term site")
	}
	results[2].HTTPCode = 429
	if !Satisfies(results) {
		t.Fatal("Google responding HTTP 429 still means reachable")
	}
	results[1].HTTPCode = 0
	if Satisfies(results) {
		t.Fatal("GitHub must be reachable")
	}
}

func TestClassifyHTTPStatusSeparatesServiceFromTransport(t *testing.T) {
	cases := []struct {
		code int
		want Status
	}{
		{200, Pass}, {204, Pass}, {301, Pass},
		{401, Fail}, {403, Fail}, {404, Fail},
		{429, Limited}, {500, Fail}, {503, Fail},
		{0, Fail}, {99, Fail}, {600, Fail},
	}
	for _, tc := range cases {
		r := Classify(Result{Name: "chatgpt", HTTPCode: tc.code}, nil)
		if r.Status != tc.want {
			t.Fatalf("HTTP %d: got %s, want %s", tc.code, r.Status, tc.want)
		}
		if Accepted(r) != (tc.code >= 100 && tc.code <= 599) {
			t.Fatalf("transport mismatch for HTTP %d: %+v", tc.code, r)
		}
	}
}

func TestProbeDetectsCloudflareChallenge(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	r := Probe(context.Background(), srv.Client(), Target{Name: "chatgpt", URL: srv.URL})
	if r.Status != Challenge || r.HTTPCode != 403 || !Accepted(r) {
		t.Fatalf("challenge must count as transport-only reachability: %+v", r)
	}
}

func TestServiceConditionRejectsChallengeAndServerErrors(t *testing.T) {
	rows := []Result{
		{Name: "bilibili", Status: Pass, HTTPCode: 200},
		{Name: "github", Status: Pass, HTTPCode: 200},
		{Name: "chatgpt", Status: Challenge, HTTPCode: 403},
	}
	if !Satisfies(rows) || SatisfiesService(rows) {
		t.Fatal("transport passed, service access must remain unverified")
	}
	rows = append(rows, Result{Name: "google", Status: Pass, HTTPCode: 204})
	if !SatisfiesService(rows) {
		t.Fatal("working Google should satisfy the third service")
	}
	rows[0].Status = Fail
	if SatisfiesService(rows) {
		t.Fatal("failed Bilibili must not satisfy the service condition")
	}
}

func TestProbeAcceptsRedirectWithoutFollowing(t *testing.T) {
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "https://example.invalid/redirect", http.StatusFound)
	}))
	defer srv.Close()
	client := srv.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	r := Probe(context.Background(), client, Target{Name: "github", URL: srv.URL})
	if r.Status != Pass || r.HTTPCode != 302 || calls != 1 {
		t.Fatalf("expected original HTTPS response, not redirect follow: %+v; calls=%d", r, calls)
	}
}

func TestProxyCONNECTFailureIsNotTargetReachability(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	client, err := NewClient(proxy.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	r := Probe(context.Background(), client, Target{Name: "github", URL: "https://example.invalid/"})
	if r.Status != Fail || r.HTTPCode != 0 {
		t.Fatalf("proxy-generated 502 must not count as remote HTTP response: %+v", r)
	}
}

func TestNewClientRequiresExplicitProxy(t *testing.T) {
	for _, bad := range []string{"", "socks5://localhost:1080", "http://localhost", "file:///tmp/proxy"} {
		if _, err := NewClient(bad, 10*time.Second); err == nil {
			t.Fatalf("accepted invalid proxy %q", bad)
		}
	}
	client, err := NewClient("http://localhost:7890", 10*time.Second)
	if err != nil || client.Timeout != 10*time.Second {
		t.Fatalf("unexpected client: %v", err)
	}
	if client.CheckRedirect == nil {
		t.Fatal("redirect behavior not explicit")
	}
}

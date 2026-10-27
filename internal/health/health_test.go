package health

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

func TestSatisfies(t *testing.T) {
	data := []Result{
		{Name: "bilibili", Status: Pass},
		{Name: "github", Status: Pass},
		{Name: "google", Status: Fail},
		{Name: "youtube", Status: Pass},
		{Name: "chatgpt", Status: Challenge},
	}
	if !Satisfies(data) {
		t.Fatal("expected successful boolean criterion")
	}
	data[3].Status = Fail
	if Satisfies(data) {
		t.Fatal("all third-term alternatives failed")
	}
	data[2].Status = Pass
	data[0].Status = Challenge
	if Satisfies(data) {
		t.Fatal("challenged Bilibili must not count as success")
	}
}

func TestClassifyBilibiliUnauthenticated(t *testing.T) {
	result := Classify(Result{Name: "bilibili", HTTPCode: 200}, []byte(`{"code":-101,"message":"Account is not logged in"}`))
	if result.Status != Pass {
		t.Fatalf("unauthenticated but reachable API: %+v", result)
	}
}

func TestClassifyCloudflareAndRateLimit(t *testing.T) {
	for _, tcase := range []struct {
		name string
		code int
		body []byte
		want Status
	}{
		{"chatgpt", 403, []byte("Just a moment"), Challenge},
		{"github", 429, nil, Limited},
		{"bilibili", 200, []byte(`{"code":-352}`), Challenge},
		{"google", 204, nil, Pass},
		{"github", 200, []byte("GitHub home"), Pass},
		{"youtube", 200, []byte("YouTube home"), Pass},
		{"chatgpt", 200, []byte("Cloudflare challenge-platform"), Challenge},
	} {
		actual := Classify(Result{Name: tcase.name, HTTPCode: tcase.code}, tcase.body)
		if actual.Status != tcase.want {
			t.Fatalf("%s HTTP %d: got %+v, want %s", tcase.name, tcase.code, actual, tcase.want)
		}
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
}

func TestClassifyUnexpectedBody(t *testing.T) {
	for _, label := range []string{"github", "youtube"} {
		if r := Classify(Result{Name: label, HTTPCode: http.StatusOK}, bytes.Repeat([]byte("x"), 64)); r.Status == Pass {
			t.Fatalf("accepted invalid %s response", label)
		}
	}
}

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadOnlySnapshotAndRedaction(t *testing.T) {
	const secret = "private-controller-token"
	calls := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Errorf("auth header %q", got)
		}
		calls[r.URL.Path] = true
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"version":"1.19.31","meta":true}`)
		case "/configs":
			fmt.Fprint(w, `{"mode":"rule","log-level":"warning","mixed-port":7890,"allow-lan":false,"tun":{"enable":true},"authentication":["do-not-output"],"secret":"do-not-output"}`)
		case "/proxies":
			fmt.Fprint(w, `{"proxies":{"DIRECT":{"type":"Direct"},"B":{"type":"Selector","now":"NodeA","all":["NodeA"]},"A":{"type":"Fallback","now":"NodeB","all":["NodeA","NodeB"]},"NodeA":{"type":"Vless","password":"do-not-output"}}}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, secret, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != "1.19.31" || !snapshot.Meta || !snapshot.Config.TUNEnabled || snapshot.Config.Mode != "rule" || snapshot.ProxyEntries != 4 {
		t.Fatalf("snapshot %#v", snapshot)
	}
	if len(snapshot.Groups) != 2 || snapshot.Groups[0].Name != "A" || snapshot.Groups[1].Name != "B" {
		t.Fatalf("groups %#v", snapshot.Groups)
	}
	if len(calls) != 3 {
		t.Fatalf("expected exactly three read-only requests, got %v", calls)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "do-not-output") || strings.Contains(string(data), secret) {
		t.Fatal("snapshot leaked sensitive config")
	}
}

func TestControllerAddressRestrictions(t *testing.T) {
	for _, address := range []string{
		"https://127.0.0.1:9090", "http://100.64.0.3:17891", "http://localhost:9090", "http://127.0.0.1", "http://127.0.0.1:0", "http://127.0.0.1:99999", "http://admin@127.0.0.1:9090", "http://127.0.0.1:9090/proxies", "http://127.0.0.1:9090/?token=x", "http://127.0.0.1:9090/#x",
	} {
		if _, err := NewClient(address, "", time.Second); err == nil {
			t.Errorf("accepted unsafe address %q", address)
		}
	}
	if _, err := NewClient("http://[::1]:9090", "", time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient("http://127.0.0.1:9090", "", 31*time.Second); err == nil {
		t.Fatal("accepted excessive timeout")
	}
}

func TestRejectRedirectAndErrorBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://example.org/secret")
		w.WriteHeader(http.StatusFound)
		fmt.Fprint(w, "hidden credentials")
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "", time.Second)
	_, err := c.Snapshot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") || strings.Contains(err.Error(), "hidden credentials") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestBoundedResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "", 3*time.Second)
	_, err := c.Snapshot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected size error, got %v", err)
	}
}

func TestRejectInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"version":`) }))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "", time.Second)
	_, err := c.Snapshot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

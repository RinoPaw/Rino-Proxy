package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCoreStatusCLI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"version":"v1.19.31","meta":true}`)
		case "/configs":
			fmt.Fprint(w, `{"mode":"rule","mixed-port":7890}`)
		case "/proxies":
			fmt.Fprint(w, `{"proxies":{"GLOBAL":{"type":"Selector","now":"DIRECT","all":["DIRECT"]}}}`)
		default:
			t.Fatalf("unknown path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("RINO_MIHOMO_SECRET", "")
	for _, format := range []string{"--json", ""} {
		var out bytes.Buffer
		args := []string{"status", "--controller", srv.URL}
		if format != "" {
			args = append(args, format)
		}
		if err := runCore(args, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "v1.19.31") {
			t.Fatalf("missing version: %s", out.String())
		}
	}
}

func TestCoreStatusRejectRemoteController(t *testing.T) {
	var out bytes.Buffer
	err := runCore([]string{"status", "--controller", "http://100.64.0.3:17891"}, &out)
	if err == nil || out.Len() > 0 {
		t.Fatalf("expected no output and safety error, got %v %q", err, out.String())
	}
}

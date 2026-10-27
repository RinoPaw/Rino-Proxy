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

func TestReloadPayloadUsesLocalControllerAndBearer(t *testing.T) {
	const secret = "test-secret"
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/configs" || r.Method != http.MethodPut || r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		var payload struct {
			Path    string `json:"path"`
			Payload string `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Path != "" {
			t.Error("unexpected path")
		}
		got = payload.Payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, secret, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ReloadPayload(context.Background(), []byte("mode: rule\n")); err != nil {
		t.Fatal(err)
	}
	if got != "mode: rule\n" {
		t.Fatalf("wrong payload: %s", got)
	}
}

func TestReloadPayloadRejectsUnsafeResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://other.example")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "SECRET CONFIG CREDENTIAL")
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "", time.Second)
	err := client.ReloadPayload(context.Background(), []byte("mode: rule\n"))
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || strings.Contains(err.Error(), "CREDENTIAL") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := client.ReloadPayload(context.Background(), nil); err == nil {
		t.Fatal("accepted empty payload")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.ReloadPayload(ctx, []byte("mode: direct\n")); err == nil {
		t.Fatal("expected cancellation")
	}
}

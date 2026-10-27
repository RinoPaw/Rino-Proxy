package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDeployCLIRequiresExplicitConfirmation(t *testing.T) {
	tests := [][]string{
		{"deploy"},
		{"deploy", "--state", "state"},
		{"deploy", "--state", "state", "--proxy", "http://127.0.0.1:29793"},
		{"deploy", "--state", "state", "--proxy", "http://100.64.0.3:7890", "--confirm"},
		{"deploy", "--state", "state", "--proxy", "http://127.0.0.1:29793", "--controller", "http://100.64.0.3:17891", "--confirm"},
		{"deploy", "--state", "state", "--proxy", "http://127.0.0.1:29793", "--hold", "5m", "--confirm"},
		{"deploy", "--state", "state", "--proxy", "http://127.0.0.1:29793", "--target", "http://example.org", "--confirm"},
		{"watchdog"},
		{"recover"},
		{"deployment-status"},
	}
	for _, args := range tests {
		var output bytes.Buffer
		if err := runCore(args, &output); err == nil || output.Len() > 0 {
			t.Fatalf("invalid arguments %v accepted: %q", args, output.String())
		}
	}
}

func TestDeployRejectsRemoteListenerBeforeReadingArchive(t *testing.T) {
	var output bytes.Buffer
	err := runCore([]string{"deploy", "--state", "missing", "--controller", "http://8.8.8.8:9090", "--proxy", "http://127.0.0.1:29793", "--confirm"}, &output)
	if err == nil || !strings.Contains(err.Error(), "loopback") || output.Len() != 0 {
		t.Fatalf("expected local-only controller rejection: %v", err)
	}
}

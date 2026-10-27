package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCoreTrialRequiresExplicitArchiveAndProxy(t *testing.T) {
	tests := [][]string{
		{"trial"},
		{"trial", "--state", "some-archive"},
		{"trial", "--state", "some-archive", "--proxy", "http://100.64.0.3:17890"},
		{"trial", "--state", "some-archive", "--proxy", "http://127.0.0.1:29793", "--hold", "5m"},
		{"trial", "--state", "some-archive", "--proxy", "http://127.0.0.1:29793", "--target", "http://example.com/"},
		{"trial", "--state", "some-archive", "--proxy", "http://127.0.0.1:29793", "--apply"},
	}
	for _, args := range tests {
		var output bytes.Buffer
		err := runCore(args, &output)
		if err == nil || output.Len() > 0 {
			t.Fatalf("invalid input %v: err=%v output=%q", args, err, output.String())
		}
	}
}

func TestCoreTrialDoesNotPermitRemoteController(t *testing.T) {
	var output bytes.Buffer
	err := runCore([]string{
		"trial", "--state", "some-archive",
		"--proxy", "http://127.0.0.1:29793",
		"--controller", "http://100.64.0.3:17891",
	}, &output)
	if err == nil || !strings.Contains(err.Error(), "loopback") || output.Len() != 0 {
		t.Fatalf("expected local-only restriction: %v", err)
	}
}

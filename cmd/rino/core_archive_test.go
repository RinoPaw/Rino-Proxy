package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RinoPaw/Rino-Proxy/internal/deploy"
)

type archiveValidator func(context.Context, string) error

func (f archiveValidator) Validate(ctx context.Context, path string) error {
	return f(ctx, path)
}

func TestCoreArchiveCommand(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, "old.yaml")
	next := filepath.Join(base, "new.yaml")
	state := filepath.Join(base, "archive")
	if err := os.WriteFile(old, []byte("mode: rule\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(next, []byte("mode: direct\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := deploy.PrepareArchive(context.Background(), old, next, state,
		archiveValidator(func(context.Context, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runCore([]string{"archive", "verify", "--state", state, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var status deploy.ArchiveStatus
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.TargetMatchesBaseline || status.ApplyAllowed || status.Phase != "prepared" {
		t.Fatalf("unexpected state: %#v", status)
	}
	if strings.Contains(out.String(), base) || strings.Contains(out.String(), "mode:") {
		t.Fatal("JSON leaked configuration or local paths")
	}
	out.Reset()
	recovered := filepath.Join(base, "restored.yaml")
	if err := runCore([]string{"archive", "export", "--state", state, "--output", recovered}, &out); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(recovered); err != nil || string(data) != "mode: rule\n" {
		t.Fatalf("recovered data mismatch: %q %v", data, err)
	}
	if err := runCore([]string{"archive", "export", "--state", state, "--output", old}, &out); err == nil {
		t.Fatal("attempted to overwrite running config")
	}
}

func TestCoreArchiveRequiresExplicitArguments(t *testing.T) {
	for _, args := range [][]string{
		{"archive"}, {"archive", "unknown"},
		{"archive", "prepare"}, {"archive", "verify"},
		{"archive", "export"}, {"archive", "verify", "--state", "x", "--apply"},
		{"archive", "prepare", "--current", "a", "--candidate", "b", "--state", "c", "--timeout", "3m"},
	} {
		var out bytes.Buffer
		err := runCore(args, &out)
		if err == nil || out.Len() > 0 {
			t.Fatalf("invalid args %v: err=%v output=%q", args, err, out.String())
		}
	}
}

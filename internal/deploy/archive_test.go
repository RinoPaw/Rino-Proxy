package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchivePrepareVerifyExport(t *testing.T) {
	d := t.TempDir()
	old := fixture(t, d, "active.yaml", oldConfig)
	next := fixture(t, d, "next.yaml", newConfig)
	state := filepath.Join(d, "state")
	n := 0
	checker := validatorFunc(func(ctx context.Context, path string) error {
		n++
		if path != next && path != filepath.Join(state, candidateFile) {
			t.Errorf("unexpected validation path: %s", path)
		}
		return nil
	})
	status, err := PrepareArchive(context.Background(), old, next, state, checker)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || status.Phase != "prepared" || !status.TargetMatchesBaseline || status.ApplyAllowed {
		t.Fatalf("invalid status %#v, validations=%d", status, n)
	}
	if _, err := os.Stat(filepath.Join(state, backupFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, candidateFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, journalFile)); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != oldConfig {
		t.Fatalf("active config modified: %s", original)
	}
	check, err := ReadArchive(state)
	if err != nil || check != status {
		t.Fatalf("failed to resume journal: %#v, %v", check, err)
	}
	out := filepath.Join(d, "rescued.yaml")
	if err := ExportBaseline(state, out); err != nil {
		t.Fatal(err)
	}
	recovered, err := os.ReadFile(out)
	if err != nil || string(recovered) != oldConfig {
		t.Fatalf("unexpected recovery content: %q, %v", recovered, err)
	}
	if err := ExportBaseline(state, out); err == nil {
		t.Fatal("overwrote existing recovery file")
	}
	if err := ExportBaseline(state, old); err == nil {
		t.Fatal("tried to overwrite active configuration")
	}
	if _, err := PrepareArchive(context.Background(), old, next, state, checker); err == nil {
		t.Fatal("reused existing state directory")
	}
	// Only an explicit manual edit changes the target; verification must report it.
	if err := os.WriteFile(old, []byte(newConfig), 0600); err != nil {
		t.Fatal(err)
	}
	check, err = ReadArchive(state)
	if err != nil || check.TargetMatchesBaseline {
		t.Fatalf("target modification not detected: %#v, %v", check, err)
	}
}

func TestArchiveRejectedValidatorAndPartialState(t *testing.T) {
	d := t.TempDir()
	old := fixture(t, d, "old.yaml", oldConfig)
	next := fixture(t, d, "new.yaml", newConfig)
	state := filepath.Join(d, "state")
	_, err := PrepareArchive(context.Background(), old, next, state, validatorFunc(func(_ context.Context, path string) error {
		if path == next {
			return nil
		}
		return errors.New("simulated staged validation failure")
	}))
	if err == nil || !strings.Contains(err.Error(), "staged candidate validation failed") {
		t.Fatalf("missing failure: %v", err)
	}
	if _, err := ReadArchive(state); err == nil {
		t.Fatal("incomplete transaction reported ready")
	}
	if _, err := os.Stat(filepath.Join(state, journalFile)); !os.IsNotExist(err) {
		t.Fatalf("journal should be absent: %v", err)
	}
	if b, err := os.ReadFile(old); err != nil || string(b) != oldConfig {
		t.Fatal("failed preflight changed current configuration")
	}
}

func TestArchiveDetectsCorruption(t *testing.T) {
	for _, name := range []string{backupFile, candidateFile, journalFile} {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			old := fixture(t, d, "old.yaml", oldConfig)
			next := fixture(t, d, "next.yaml", newConfig)
			state := filepath.Join(d, "private")
			_, err := PrepareArchive(context.Background(), old, next, state, validatorFunc(func(context.Context, string) error { return nil }))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(state, name), []byte("bad: yes\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadArchive(state); err == nil {
				t.Fatal("tampered archive accepted")
			}
			if err := ExportBaseline(state, filepath.Join(d, "recovered.yaml")); err == nil && name != candidateFile {
				t.Fatal("invalid archive exported")
			}
		})
	}
}

func TestArchiveRejectsModifiedInputs(t *testing.T) {
	d := t.TempDir()
	old := fixture(t, d, "old.yaml", oldConfig)
	next := fixture(t, d, "next.yaml", newConfig)
	for label, modify := range map[string]string{"current": old, "candidate": next} {
		t.Run(label, func(t *testing.T) {
			state := filepath.Join(d, "state-"+label)
			_, err := PrepareArchive(context.Background(), old, next, state, validatorFunc(func(_ context.Context, path string) error {
				if path == next {
					return os.WriteFile(modify, []byte("mode: direct\n"), 0600)
				}
				return nil
			}))
			if err == nil {
				t.Fatal("accepted mutated input")
			}
		})
		// Restore for the next subtest.
		if err := os.WriteFile(old, []byte(oldConfig), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(next, []byte(newConfig), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArchiveExportRejectsSymlink(t *testing.T) {
	d := t.TempDir()
	old := fixture(t, d, "old.yaml", oldConfig)
	next := fixture(t, d, "next.yaml", newConfig)
	state := filepath.Join(d, "state")
	if _, err := PrepareArchive(context.Background(), old, next, state, validatorFunc(func(context.Context, string) error { return nil })); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(d, "target.yaml")
	fixture(t, d, "target.yaml", "do-not-overwrite")
	symlink := filepath.Join(d, "link.yaml")
	if err := os.Symlink(target, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := ExportBaseline(state, symlink); err == nil {
		t.Fatal("export followed existing symlink")
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "do-not-overwrite" {
		t.Fatal("existing target modified")
	}
}

[executed on device: rino-pc (6c706f50-a21c-4a1f-9bbf-39635d313ad0)]

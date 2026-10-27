package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RinoPaw/Rino-Proxy/internal/core"
)

type fakeRuntime struct {
	mu          sync.Mutex
	mode        string
	changes     []string
	reloadError string
}

func (r *fakeRuntime) Snapshot(context.Context) (core.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return core.Snapshot{
		Version: "test-core", ProxyEntries: 2,
		Config: core.Config{Mode: r.mode, MixedPort: 18993},
	}, nil
}

func (r *fakeRuntime) ReloadPayload(_ context.Context, input []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := string(input)
	if strings.Contains(s, "mode: direct") {
		r.mode = "direct"
		r.changes = append(r.changes, "candidate")
	} else {
		r.mode = "rule"
		r.changes = append(r.changes, "baseline")
	}
	if r.reloadError != "" && strings.Contains(s, r.reloadError) {
		return errors.New("injected reload failure")
	}
	return nil
}

func trialArchive(t *testing.T, baseline, candidate string) string {
	t.Helper()
	dir := t.TempDir()
	a := fixture(t, dir, "active.yaml", baseline)
	b := fixture(t, dir, "next.yaml", candidate)
	state := filepath.Join(dir, "archive")
	_, err := PrepareArchive(context.Background(), a, b, state, validatorFunc(func(context.Context, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestTrialAppliesProbesAndRestores(t *testing.T) {
	const base = "mode: rule\nmixed-port: 18993\nrules:\n  - MATCH,DIRECT\n"
	const candidate = "mode: direct\nmixed-port: 18993\nrules:\n  - MATCH,DIRECT\n"
	state := trialArchive(t, base, candidate)
	runtime := &fakeRuntime{mode: "rule"}
	probes := 0
	result, err := Trial(context.Background(), state, runtime, 2*time.Millisecond, func(context.Context) error {
		probes++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || !result.Healthy || !result.Restored || runtime.mode != "rule" || probes != 3 {
		t.Fatalf("result=%#v mode=%s probes=%d", result, runtime.mode, probes)
	}
	if len(runtime.changes) != 2 || runtime.changes[0] != "candidate" || runtime.changes[1] != "baseline" {
		t.Fatalf("wrong reload sequence: %v", runtime.changes)
	}
}

func TestTrialRollsBackOnProbeFailure(t *testing.T) {
	state := trialArchive(t, "mode: rule\nmixed-port: 18993\n", "mode: direct\nmixed-port: 18993\n")
	runtime := &fakeRuntime{mode: "rule"}
	calls := 0
	r, err := Trial(context.Background(), state, runtime, 0, func(context.Context) error {
		calls++
		if calls == 2 {
			return errors.New("candidate unreachable")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "candidate unreachable") || !r.Applied || r.Healthy || !r.Restored || calls != 3 {
		t.Fatalf("result=%#v err=%v calls=%d", r, err, calls)
	}
}

func TestTrialRollsBackAmbiguousReloadFailure(t *testing.T) {
	state := trialArchive(t, "mode: rule\nmixed-port: 18993\n", "mode: direct\nmixed-port: 18993\n")
	runtime := &fakeRuntime{mode: "rule", reloadError: "mode: direct"}
	r, err := Trial(context.Background(), state, runtime, 0, func(context.Context) error { return nil })
	if err == nil || !r.Restored || r.Applied || runtime.mode != "rule" {
		t.Fatalf("result=%#v err=%v", r, err)
	}
}

func TestTrialRejectsProtectedChanges(t *testing.T) {
	state := trialArchive(t, "mode: rule\nmixed-port: 18993\n", "mode: rule\nmixed-port: 18994\n")
	runtime := &fakeRuntime{mode: "rule"}
	_, err := Trial(context.Background(), state, runtime, 0, func(context.Context) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "mixed-port") || len(runtime.changes) != 0 {
		t.Fatalf("should reject dangerous change, %v", err)
	}
}

func TestTrialRejectsAlreadyChangedCurrentFile(t *testing.T) {
	const base = "mode: rule\nmixed-port: 18993\n"
	dir := t.TempDir()
	old := fixture(t, dir, "active.yaml", base)
	next := fixture(t, dir, "candidate.yaml", "mode: direct\nmixed-port: 18993\n")
	state := filepath.Join(dir, "state")
	_, err := PrepareArchive(context.Background(), old, next, state, validatorFunc(func(context.Context, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("mode: direct\nmixed-port: 18993\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{mode: "rule"}
	_, err = Trial(context.Background(), state, runtime, 0, func(context.Context) error { return nil })
	if err == nil || len(runtime.changes) != 0 {
		t.Fatalf("should reject stale baseline, %v", err)
	}
}

func TestTrialRejectsBaselineRuntimeMismatch(t *testing.T) {
	state := trialArchive(t, "mode: rule\nmixed-port: 18993\n", "mode: direct\nmixed-port: 18993\n")
	runtime := &fakeRuntime{mode: "direct"}
	_, err := Trial(context.Background(), state, runtime, 0, func(context.Context) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "baseline runtime check") || len(runtime.changes) != 0 {
		t.Fatalf("baseline check missing: %v", err)
	}
}

func TestTrialRollbackFailureIsReported(t *testing.T) {
	state := trialArchive(t, "mode: rule\nmixed-port: 18993\n", "mode: direct\nmixed-port: 18993\n")
	runtime := &fakeRuntime{mode: "rule", reloadError: "mode: rule"}
	r, err := Trial(context.Background(), state, runtime, 0, func(context.Context) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "restoration unverified") || r.Restored {
		t.Fatalf("rollback failure hidden: %#v %v", r, err)
	}
}

func TestTrialCancelledDuringHoldStillRestores(t *testing.T) {
	state := trialArchive(t, "mode: rule\nmixed-port: 18993\n", "mode: direct\nmixed-port: 18993\n")
	runtime := &fakeRuntime{mode: "rule"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	result, err := Trial(ctx, state, runtime, time.Minute, func(context.Context) error {
		count++
		if count == 2 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || !result.Applied || !result.Restored || runtime.mode != "rule" {
		t.Fatalf("cancellation did not restore baseline: %#v %v", result, err)
	}
}

func TestTrialNeverReloadsWhenBaselineProbeFails(t *testing.T) {
	state := trialArchive(t, "mode: rule\nmixed-port: 18993\n", "mode: direct\nmixed-port: 18993\n")
	runtime := &fakeRuntime{mode: "rule"}
	result, err := Trial(context.Background(), state, runtime, 0, func(context.Context) error {
		return errors.New("baseline unavailable")
	})
	if err == nil || result.Applied || result.Restored || len(runtime.changes) > 0 {
		t.Fatalf("baseline outage still caused a reload: %#v %v", result, err)
	}
}

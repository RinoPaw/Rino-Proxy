package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeployDiskCommitsAfterIndependentReady(t *testing.T) {
	const initial = "mode: rule\nmixed-port: 18993\nrules:\n - MATCH,DIRECT\n"
	const changed = "mode: direct\nmixed-port: 18993\nrules:\n - MATCH,DIRECT\n"
	dir := t.TempDir()
	active := fixture(t, dir, "active.yaml", initial)
	proposed := fixture(t, dir, "next.yaml", changed)
	state := filepath.Join(dir, "archive")
	if _, err := PrepareArchive(context.Background(), active, proposed, state, validatorFunc(func(context.Context, string) error { return nil })); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{mode: "rule"}
	watchCtx, stop := context.WithCancel(context.Background())
	defer stop()
	var watchdogDone = make(chan error, 1)
	start := func(state, _ string) error {
		go func() { watchdogDone <- Watchdog(watchCtx, state, runtime) }()
		return nil
	}
	report, err := DeployDisk(context.Background(), state, "http://127.0.0.1:18994", runtime, func(context.Context) error { return nil }, 30*time.Millisecond, start)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied || !report.Healthy || !report.Committed || report.Recovered {
		t.Fatalf("unexpected commit %#v", report)
	}
	got, err := os.ReadFile(active)
	if err != nil || string(got) != changed {
		t.Fatalf("expected committed disk candidate: %q %v", got, err)
	}
	status, err := Status(state)
	if err != nil || status.Phase != "committed" || !status.TargetMatchesCandidate {
		t.Fatalf("unexpected status %#v %v", status, err)
	}
	select {
	case err := <-watchdogDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not terminate after commit")
	}
}

func TestDeployDiskRollsBackOnHealthFailure(t *testing.T) {
	dir := t.TempDir()
	old := fixture(t, dir, "old.yaml", "mode: rule\nmixed-port: 18993\n")
	next := fixture(t, dir, "new.yaml", "mode: direct\nmixed-port: 18993\n")
	state := filepath.Join(dir, "archive")
	if _, err := PrepareArchive(context.Background(), old, next, state, validatorFunc(func(context.Context, string) error { return nil })); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{mode: "rule"}
	watchCtx, stop := context.WithCancel(context.Background())
	defer stop()
	start := func(state, _ string) error {
		go func() { _ = Watchdog(watchCtx, state, runtime) }()
		return nil
	}
	calls := 0
	result, err := DeployDisk(context.Background(), state, "http://127.0.0.1:18994", runtime, func(context.Context) error {
		calls++
		if calls == 2 {
			return errors.New("injected outbound failure")
		}
		return nil
	}, 0, start)
	if err == nil || !strings.Contains(err.Error(), "injected outbound failure") || result.Committed || !result.Recovered {
		t.Fatalf("rollback failed: %+v %v", result, err)
	}
	contents, err := os.ReadFile(old)
	if err != nil || string(contents) != "mode: rule\nmixed-port: 18993\n" {
		t.Fatalf("baseline disk not restored: %q %v", contents, err)
	}
	if runtime.mode != "rule" || calls != 3 {
		t.Fatalf("baseline runtime not restored: %+v, probe=%d", runtime, calls)
	}
	status, err := Status(state)
	if err != nil || status.Phase != "recovered" {
		t.Fatalf("expected recovered marker: %+v %v", status, err)
	}
}

func TestWatchdogRecoversAfterSimulatedManagerDeath(t *testing.T) {
	dir := t.TempDir()
	base := "mode: rule\nmixed-port: 18993\n"
	next := "mode: direct\nmixed-port: 18993\n"
	target := fixture(t, dir, "active.yaml", base)
	candidate := fixture(t, dir, "next.yaml", next)
	state := filepath.Join(dir, "archive")
	if _, err := PrepareArchive(context.Background(), target, candidate, state, validatorFunc(func(context.Context, string) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if err := Arm(state, "http://127.0.0.1:18994", time.Now().Add(3500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{mode: "rule"}
	done := make(chan error, 1)
	go func() { done <- Watchdog(context.Background(), state, runtime) }()
	for i := 0; !markerExists(state, readyFile) && i < 100; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !markerExists(state, readyFile) {
		t.Fatal("no watchdog readiness")
	}
	j, _, err := openJournal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceVerified(target, []byte(next), j.BaselineSHA256, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReloadPayload(context.Background(), []byte(next)); err != nil {
		t.Fatal(err)
	}
	// The manager is now presumed gone; only the independent guardian remains.
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(9 * time.Second):
		t.Fatal("guardian did not restore before deadline + grace")
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != base || runtime.mode != "rule" {
		t.Fatalf("crash rollback failed: disk=%q mode=%s err=%v", content, runtime.mode, err)
	}
	status, err := Status(state)
	if err != nil || status.Phase != "recovered" || !status.TargetMatchesBaseline {
		t.Fatalf("unexpected recovery status: %#v %v", status, err)
	}
}

func TestDeployRefusesMutationAndUnsafeController(t *testing.T) {
	dir := t.TempDir()
	old := fixture(t, dir, "old.yaml", "mode: rule\nmixed-port: 18993\n")
	next := fixture(t, dir, "new.yaml", "mode: direct\nmixed-port: 18993\n")
	state := filepath.Join(dir, "archive")
	if _, err := PrepareArchive(context.Background(), old, next, state, validatorFunc(func(context.Context, string) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if err := Arm(state, "http://100.64.0.3:17891", time.Now().Add(15*time.Second)); err == nil {
		t.Fatal("accepted nonlocal controller")
	}
	if err := ReplaceVerified(old, []byte("mode: direct\n"), "invalidhash", 0600); err == nil {
		t.Fatal("accepted mismatched source")
	}
	if err := os.WriteFile(old, []byte("mode: global\nmixed-port: 18993\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := Recover(context.Background(), state, &fakeRuntime{mode: "rule"}, nil)
	if err == nil {
		t.Fatal("recovery without pending marker succeeded")
	}
	if _, err := DeployDisk(context.Background(), state, "http://127.0.0.1:18994", &fakeRuntime{mode: "rule"}, func(context.Context) error { return nil }, 0, func(string, string) error { return nil }); err == nil {
		t.Fatal("deploy accepted mutated live target")
	}
}

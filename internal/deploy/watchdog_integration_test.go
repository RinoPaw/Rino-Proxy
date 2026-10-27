package deploy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RinoPaw/Rino-Proxy/internal/core"
)

// This integration test requires a disposable real Mihomo on ports
// 29793/29794. It intentionally kills a DIFFERENT manager process, then
// observes an independently spawned Rino watchdog restore disk + core.
func TestWatchdogSurvivesActualManagerKill(t *testing.T) {
	if os.Getenv("RINO_CRASH_ISOLATED") != "1" {
		t.Skip("requires disposable real Mihomo and explicit opt-in")
	}
	state := os.Getenv("RINO_CRASH_STATE")
	rino := os.Getenv("RINO_CRASH_BINARY")
	if state == "" || rino == "" {
		t.Fatal("missing disposable state/binary")
	}
	p, j, err := pendingReadForIntegration(state)
	if err != nil {
		t.Fatal(err)
	}
	_ = p
	client, err := core.NewClient("http://127.0.0.1:29794", "", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := client.Snapshot(context.Background())
	if err != nil || snap.Config.MixedPort != 29793 || snap.Config.Mode != "rule" || snap.Config.AllowLAN || snap.Config.TUNEnabled {
		t.Fatalf("refusing to touch nonisolated Mihomo %+v %v", snap.Config, err)
	}
	// The separate helper process owns application; this parent only watches.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(self, "-test.run=^TestWatchdogManagerKillHelper$")
	child.Env = append(os.Environ(), "RINO_CRASH_HELPER=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	var candidateSeen bool
	for i := 0; i < 160; i++ {
		time.Sleep(100 * time.Millisecond)
		if checkDigest(j.TargetPath, j.CandidateSHA256) != nil {
			continue
		}
		snap, err = client.Snapshot(context.Background())
		if err == nil && snap.Config.Mode == "direct" {
			candidateSeen = true
			break
		}
	}
	if !candidateSeen {
		t.Fatal("helper never applied candidate to real core")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	// Watchdog has its own OS PID and is not killed with the manager.
	var restored bool
	for i := 0; i < 560; i++ {
		time.Sleep(100 * time.Millisecond)
		if checkDigest(j.TargetPath, j.BaselineSHA256) != nil {
			continue
		}
		snap, err = client.Snapshot(context.Background())
		if err == nil && snap.Config.Mode == "rule" && markerExists(state, recoveredFile) {
			restored = true
			break
		}
	}
	if !restored {
		s, _ := Status(state)
		t.Fatalf("watchdog did not restore within deadline+grace: %#v", s)
	}
}

func pendingReadForIntegration(state string) (Pending, Journal, error) {
	// A fresh archive intentionally has no pending journal before the helper
	// starts. The test still needs the target and hashes from the archive.
	j, _, err := openJournal(state)
	if err != nil {
		return Pending{}, Journal{}, err
	}
	return Pending{}, j, nil
}

func TestWatchdogManagerKillHelper(t *testing.T) {
	if os.Getenv("RINO_CRASH_HELPER") != "1" {
		t.Skip("helper only")
	}
	state := os.Getenv("RINO_CRASH_STATE")
	rino := os.Getenv("RINO_CRASH_BINARY")
	if state == "" || rino == "" {
		t.Fatal("missing helper environment")
	}
	client, err := core.NewClient("http://127.0.0.1:29794", "", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	startWatchdog := func(state, _ string) error {
		logFile, err := os.OpenFile(filepath.Join(state, "test-watchdog.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer logFile.Close()
		cmd := exec.Command(rino, "core", "watchdog", "--state", state)
		cmd.Stdout, cmd.Stderr = logFile, logFile
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	// Health is intentionally injected to verify only disk and runtime in
	// this test; real outbound HTTPS is tested separately by CLI.
	_, err = DeployDisk(context.Background(), state, "http://127.0.0.1:29794", client, func(context.Context) error { return nil }, 16*time.Second, startWatchdog)
	if err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "watchdog") {
		t.Fatal(err)
	}
}

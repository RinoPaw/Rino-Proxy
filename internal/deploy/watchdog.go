package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/RinoPaw/Rino-Proxy/internal/core"
)

const (
	pendingFile     = "pending.json"
	readyFile       = "watchdog.ready"
	committedFile   = "committed"
	recoveredFile   = "recovered"
	failedFile      = "recovery.failed"
	recoverLockFile = "recovery.lock"
)

type Pending struct {
	Version    int       `json:"version"`
	Deadline   time.Time `json:"deadline"`
	Controller string    `json:"controller"`
}

type DeployStatus struct {
	Phase                  string `json:"phase"`
	TargetMatchesBaseline  bool   `json:"target_matches_baseline"`
	TargetMatchesCandidate bool   `json:"target_matches_candidate"`
}

// Arm creates a private, durable pending journal BEFORE any modification.
// Only one transaction can run per archive. Deadline survives controller crash.
func Arm(stateDir, controller string, deadline time.Time) error {
	status, err := ReadArchive(stateDir)
	if err != nil {
		return err
	}
	if !status.TargetMatchesBaseline {
		return errors.New("cannot arm: target diverged from archived baseline")
	}
	if deadline.Before(time.Now().Add(3*time.Second)) || deadline.After(time.Now().Add(3*time.Minute)) {
		return errors.New("watchdog deadline must be within 3 seconds to 3 minutes")
	}
	if _, err := core.NewClient(controller, "", 5*time.Second); err != nil {
		return fmt.Errorf("unsafe controller address: %w", err)
	}
	p := Pending{Version: 1, Deadline: deadline.UTC(), Controller: controller}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeExclusive(filepath.Join(stateDir, pendingFile), append(body, '\n'), 0600)
}

func pendingRead(stateDir string) (Pending, Journal, error) {
	j, base, err := openJournal(stateDir)
	if err != nil {
		return Pending{}, Journal{}, err
	}
	file := filepath.Join(base, pendingFile)
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 4096 {
		return Pending{}, Journal{}, errors.New("missing or invalid pending journal")
	}
	f, err := os.Open(file)
	if err != nil {
		return Pending{}, Journal{}, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(body) > 4096 {
		return Pending{}, Journal{}, errors.New("cannot read pending journal")
	}
	var p Pending
	if err := json.Unmarshal(body, &p); err != nil || p.Version != 1 || p.Deadline.IsZero() {
		return Pending{}, Journal{}, errors.New("invalid pending journal")
	}
	if _, err := core.NewClient(p.Controller, "", 5*time.Second); err != nil {
		return Pending{}, Journal{}, errors.New("pending controller must be local")
	}
	if err := checkDigest(filepath.Join(base, backupFile), j.BaselineSHA256); err != nil {
		return Pending{}, Journal{}, err
	}
	if err := checkDigest(filepath.Join(base, candidateFile), j.CandidateSHA256); err != nil {
		return Pending{}, Journal{}, err
	}
	return p, j, nil
}

func markerExists(stateDir, marker string) bool {
	info, err := os.Lstat(filepath.Join(stateDir, marker))
	return err == nil && info.Mode().IsRegular()
}

func mark(stateDir, marker string) error {
	return writeExclusive(filepath.Join(stateDir, marker), []byte("1\n"), 0600)
}

func Status(stateDir string) (DeployStatus, error) {
	_, j, err := pendingRead(stateDir)
	if err != nil {
		return DeployStatus{}, err
	}
	status := DeployStatus{
		Phase:                  "pending",
		TargetMatchesBaseline:  checkDigest(j.TargetPath, j.BaselineSHA256) == nil,
		TargetMatchesCandidate: checkDigest(j.TargetPath, j.CandidateSHA256) == nil,
	}
	switch {
	case markerExists(stateDir, committedFile):
		status.Phase = "committed"
	case markerExists(stateDir, recoveredFile):
		status.Phase = "recovered"
	case markerExists(stateDir, failedFile):
		status.Phase = "recovery_failed"
	}
	return status, nil
}

// ReplaceVerified only modifies the exact archived target, and only if it
// still has the expected checksum. It writes a same-directory temporary file
// and renames it, keeping the previous mode bits where supported.
func ReplaceVerified(path string, data []byte, expected string, mode os.FileMode) error {
	if err := checkDigest(path, expected); err != nil {
		return errors.New("target changed; refusing overwrite")
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".rino-deploy-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(mode.Perm()); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := checkDigest(path, expected); err != nil {
		return errors.New("target changed during staging")
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("atomic rename failed: %w", err)
	}
	return nil
}

// Recover attempts a single takeover of an unresolved transaction.
// Other changes made to the target after the transaction are preserved.
func Recover(ctx context.Context, stateDir string, runtime Runtime, probe func(context.Context) error) error {
	_, j, err := pendingRead(stateDir)
	if err != nil {
		return err
	}
	if markerExists(stateDir, committedFile) || markerExists(stateDir, recoveredFile) {
		return nil
	}
	lock := filepath.Join(stateDir, recoverLockFile)
	if err := writeExclusive(lock, []byte("recovering\n"), 0600); err != nil {
		return fmt.Errorf("another recovery is running: %w", err)
	}
	defer os.Remove(lock)
	if markerExists(stateDir, committedFile) {
		return nil
	}
	base, err := readDocument(filepath.Join(stateDir, backupFile))
	if err != nil || base.sha256 != j.BaselineSHA256 {
		return errors.New("verified baseline unavailable")
	}
	current, err := readDocument(j.TargetPath)
	if err != nil {
		return errors.New("active target is missing or invalid")
	}
	if current.sha256 != j.BaselineSHA256 && current.sha256 != j.CandidateSHA256 {
		return errors.New("target externally modified; preserving it")
	}
	// If restoration fails, report it. Do not mark recovered.
	if current.sha256 == j.CandidateSHA256 {
		if err := ReplaceVerified(j.TargetPath, base.data, j.CandidateSHA256, current.info.Mode()); err != nil {
			return err
		}
	}
	if err := runtime.ReloadPayload(ctx, base.data); err != nil {
		return fmt.Errorf("restore reload failed: %w", err)
	}
	restored, err := runtime.Snapshot(ctx)
	if err != nil {
		return err
	}
	if err := verifyMode(base.fields, restored); err != nil {
		return err
	}
	if probe != nil {
		if err := probe(ctx); err != nil {
			return err
		}
	}
	return mark(stateDir, recoveredFile)
}

// Watchdog remains alive in a distinct OS process. A successfully completed
// deployment writes committed; any uncommitted transaction is rolled back
// after its fixed deadline.
func Watchdog(ctx context.Context, stateDir string, runtime Runtime) error {
	p, _, err := pendingRead(stateDir)
	if err != nil {
		return err
	}
	if runtime == nil {
		return errors.New("watchdog requires runtime")
	}
	if err := mark(stateDir, readyFile); err != nil {
		return err
	}
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	for {
		if markerExists(stateDir, committedFile) || markerExists(stateDir, recoveredFile) {
			return nil
		}
		if !time.Now().Before(p.Deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
		}
	}
	// Keep trying briefly when a core is restarting. Do not silently give up.
	var last error
	for i := 0; i < 5; i++ {
		if markerExists(stateDir, committedFile) || markerExists(stateDir, recoveredFile) {
			return nil
		}
		attemptCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		last = Recover(attemptCtx, stateDir, runtime, nil)
		cancel()
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	_ = mark(stateDir, failedFile)
	return fmt.Errorf("watchdog rollback could not be verified: %w", last)
}

func WatchdogLocal(ctx context.Context, stateDir string) error {
	p, _, err := pendingRead(stateDir)
	if err != nil {
		return err
	}
	c, err := core.NewClient(p.Controller, os.Getenv("RINO_MIHOMO_SECRET"), 5*time.Second)
	if err != nil {
		return err
	}
	return Watchdog(ctx, stateDir, c)
}

func RecoverLocal(ctx context.Context, stateDir string) error {
	p, _, err := pendingRead(stateDir)
	if err != nil {
		return err
	}
	c, err := core.NewClient(p.Controller, os.Getenv("RINO_MIHOMO_SECRET"), 5*time.Second)
	if err != nil {
		return err
	}
	return Recover(ctx, stateDir, c, nil)
}

package deploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

type DeployResult struct {
	Applied   bool `json:"applied"`
	Healthy   bool `json:"healthy"`
	Committed bool `json:"committed"`
	Recovered bool `json:"recovered"`
}

// Launcher starts a watchdog INDEPENDENT from the current manager process.
// It must return only when the independent OS process has been started.
type Launcher func(stateDir, controller string) error

// DeployDisk atomically replaces the active YAML, reloads Mihomo, verifies,
// then commits. A separate watchdog must have started and acknowledged
// readiness BEFORE ReplaceVerified can modify the target.
//
// No reboot recovery is implied: the journal remains for explicit recovery
// after a host reboot until a system-level watchdog supervisor exists.
func DeployDisk(ctx context.Context, stateDir, controller string, runtime Runtime, probe func(context.Context) error, hold time.Duration, launch Launcher) (result DeployResult, err error) {
	if runtime == nil || probe == nil || launch == nil {
		return result, errors.New("runtime, probe and watchdog launcher required")
	}
	if hold < 0 || hold > time.Minute {
		return result, errors.New("hold must be within one minute")
	}
	status, err := ReadArchive(stateDir)
	if err != nil {
		return result, err
	}
	if !status.TargetMatchesBaseline {
		return result, errors.New("baseline file changed since preparing archive")
	}
	j, _, err := openJournal(stateDir)
	if err != nil {
		return result, err
	}
	current, err := readDocument(j.TargetPath)
	if err != nil {
		return result, err
	}
	candidate, err := readDocument(filepath.Join(stateDir, candidateFile))
	if err != nil || candidate.sha256 != j.CandidateSHA256 || current.sha256 != j.BaselineSHA256 {
		return result, errors.New("archive snapshot or active config changed")
	}
	if err := verifyProtectedSettings(current.fields, candidate.fields); err != nil {
		return result, err
	}
	before, err := runtime.Snapshot(ctx)
	if err != nil {
		return result, err
	}
	if err := verifyMode(current.fields, before); err != nil {
		return result, fmt.Errorf("active runtime mismatch: %w", err)
	}
	if err := probe(ctx); err != nil {
		return result, fmt.Errorf("baseline health failed: %w", err)
	}
	// Hard upper bound: watchdog deadline is deliberately several seconds
	// beyond the intended hold, to allow healthy validation and commit.
	deadline := time.Now().Add(hold + 25*time.Second)
	if err := Arm(stateDir, controller, deadline); err != nil {
		return result, err
	}
	if err := launch(stateDir, controller); err != nil {
		return result, fmt.Errorf("watchdog not started; target unchanged: %w", err)
	}
	readyDeadline := time.Now().Add(6 * time.Second)
	for !markerExists(stateDir, readyFile) {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if time.Now().After(readyDeadline) {
			return result, errors.New("watchdog did not acknowledge readiness; target unchanged")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// From here, every failure needs immediate rollback and the watchdog
	// remains available after a manager crash.
	defer func() {
		if result.Committed {
			return
		}
		restoreCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if recoverErr := Recover(restoreCtx, stateDir, runtime, probe); recoverErr != nil {
			err = errors.Join(err, fmt.Errorf("recovery pending with independent watchdog: %w", recoverErr))
			return
		}
		result.Recovered = true
	}()
	if err := ReplaceVerified(j.TargetPath, candidate.data, j.BaselineSHA256, current.info.Mode()); err != nil {
		return result, err
	}
	if err := runtime.ReloadPayload(ctx, candidate.data); err != nil {
		return result, fmt.Errorf("candidate reload failed: %w", err)
	}
	result.Applied = true
	after, err := runtime.Snapshot(ctx)
	if err != nil {
		return result, err
	}
	if err := verifyMode(candidate.fields, after); err != nil {
		return result, err
	}
	if err := probe(ctx); err != nil {
		return result, fmt.Errorf("candidate health failed: %w", err)
	}
	result.Healthy = true
	if hold > 0 {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(hold):
		}
	}
	// A frozen or slow CLI must never override the watchdog deadline.
	if time.Until(deadline) < 8*time.Second {
		return result, errors.New("too close to watchdog deadline to commit")
	}
	if err := checkDigest(j.TargetPath, j.CandidateSHA256); err != nil {
		return result, errors.New("candidate file changed during trial")
	}
	if markerExists(stateDir, recoveredFile) || markerExists(stateDir, failedFile) {
		return result, errors.New("watchdog recovery already started")
	}
	if err := mark(stateDir, committedFile); err != nil {
		return result, err
	}
	result.Committed = true
	return result, nil
}

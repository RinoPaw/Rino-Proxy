package deploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"time"

	"github.com/RinoPaw/Rino-Proxy/internal/core"
)

// Runtime is small enough to substitute with a fake controller in tests.
type Runtime interface {
	Snapshot(context.Context) (core.Snapshot, error)
	ReloadPayload(context.Context, []byte) error
}

type TrialResult struct {
	Applied  bool `json:"applied"`
	Healthy  bool `json:"healthy"`
	Restored bool `json:"restored"`
}

// Protected settings must remain stable during this first live trial. The
// controller must remain reachable through the same loopback address.
var protectedSettings = []string{
	"external-controller", "external-controller-tls", "external-controller-unix",
	"external-controller-pipe", "secret", "allow-lan", "bind-address",
	"port", "socks-port", "mixed-port", "redir-port", "tproxy-port",
	"tun", "dns", "interface-name", "routing-mark", "listeners",
}

func verifyProtectedSettings(base, candidate map[string]any) error {
	for _, name := range protectedSettings {
		if !reflect.DeepEqual(base[name], candidate[name]) {
			return fmt.Errorf("trial cannot change management, listener, DNS or route field %s", name)
		}
	}
	return nil
}

func verifyMode(config map[string]any, snapshot core.Snapshot) error {
	if expected, ok := config["mode"].(string); ok && expected != "" && snapshot.Config.Mode != expected {
		return errors.New("runtime mode does not match validated configuration")
	}
	if expected, ok := config["mixed-port"].(int); ok && snapshot.Config.MixedPort != expected {
		return errors.New("runtime listener does not match validated configuration")
	}
	if snapshot.Version == "" || snapshot.ProxyEntries <= 0 {
		return errors.New("runtime controller or proxy inventory is unavailable")
	}
	return nil
}

// Trial briefly reloads the prepared candidate in Mihomo's memory, checks
// the resulting runtime, then ALWAYS attempts to reload the baseline.
// This does not write the active configuration file and is NOT crash-safe:
// an external watchdog is still needed before unattended operation.
func Trial(ctx context.Context, stateDir string, runtime Runtime, hold time.Duration, probe func(context.Context) error) (result TrialResult, err error) {
	if runtime == nil || probe == nil {
		return result, errors.New("runtime controller and health probe are required")
	}
	if hold < 0 || hold > 2*time.Minute {
		return result, errors.New("trial duration must be between zero and two minutes")
	}
	status, err := ReadArchive(stateDir)
	if err != nil {
		return result, err
	}
	if !status.TargetMatchesBaseline {
		return result, errors.New("original configuration changed since archival")
	}
	baseline, err := readDocument(filepath.Join(stateDir, backupFile))
	if err != nil {
		return result, err
	}
	candidate, err := readDocument(filepath.Join(stateDir, candidateFile))
	if err != nil {
		return result, err
	}
	if baseline.sha256 != status.BaselineSHA256 || candidate.sha256 != status.CandidateSHA256 {
		return result, errors.New("archive changed while reading")
	}
	if err := verifyProtectedSettings(baseline.fields, candidate.fields); err != nil {
		return result, err
	}
	before, err := runtime.Snapshot(ctx)
	if err != nil {
		return result, err
	}
	if err := verifyMode(baseline.fields, before); err != nil {
		return result, fmt.Errorf("baseline runtime check failed: %w", err)
	}
	if err := probe(ctx); err != nil {
		return result, fmt.Errorf("baseline proxy health failed: %w", err)
	}
	// A PUT may modify the runtime before returning an error. Attempt to
	// restore even after an ambiguous failed reload.
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		restoreErr := runtime.ReloadPayload(restoreCtx, baseline.data)
		if restoreErr == nil {
			var restored core.Snapshot
			restored, restoreErr = runtime.Snapshot(restoreCtx)
			if restoreErr == nil {
				restoreErr = verifyMode(baseline.fields, restored)
			}
		}
		if restoreErr == nil {
			restoreErr = probe(restoreCtx)
		}
		if restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("baseline restoration unverified; intervention required: %w", restoreErr))
			return
		}
		result.Restored = true
	}()
	if err := runtime.ReloadPayload(ctx, candidate.data); err != nil {
		return result, fmt.Errorf("candidate reload failed: %w", err)
	}
	result.Applied = true
	after, err := runtime.Snapshot(ctx)
	if err != nil {
		return result, fmt.Errorf("candidate runtime inspection failed: %w", err)
	}
	if err := verifyMode(candidate.fields, after); err != nil {
		return result, fmt.Errorf("candidate verification failed: %w", err)
	}
	if err := probe(ctx); err != nil {
		return result, fmt.Errorf("candidate proxy health failed: %w", err)
	}
	result.Healthy = true
	if hold > 0 {
		timer := time.NewTimer(hold)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-timer.C:
		}
	}
	return result, nil
}

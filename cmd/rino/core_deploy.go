package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/RinoPaw/Rino-Proxy/internal/core"
	"github.com/RinoPaw/Rino-Proxy/internal/deploy"
	"github.com/RinoPaw/Rino-Proxy/internal/health"
)

func localProxyProbe(address, target string, timeout time.Duration) (func(context.Context) error, error) {
	if err := core.ValidateLocalProxy(address); err != nil {
		return nil, fmt.Errorf("proxy must be assigned to this host: %w", err)
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("target must be an HTTPS URL")
	}
	return func(ctx context.Context) error {
		client, err := health.NewClient(address, timeout)
		if err != nil {
			return err
		}
		defer client.CloseIdleConnections()
		result := health.Probe(ctx, client, health.Target{Name: "deploy", URL: target})
		if !health.Accepted(result) {
			return fmt.Errorf("HTTPS transport probe failed: %s", result.Detail)
		}
		return nil
	}, nil
}

// spawnGuard launches a separate process, and never passes controller secrets
// on the command line. The private archive holds the controller address.
func spawnGuard(stateDir, _ string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(stateDir, "watchdog.log")
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "core", "watchdog", "--state", stateDir)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func runCoreDeploy(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: rino core deploy|watchdog|recover|deployment-status")
	}
	switch args[0] {
	case "watchdog":
		f := flag.NewFlagSet("watchdog", flag.ContinueOnError)
		f.SetOutput(io.Discard)
		state := f.String("state", "", "private archive directory")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *state == "" || f.NArg() != 0 {
			return errors.New("watchdog requires --state")
		}
		return deploy.WatchdogLocal(context.Background(), *state)
	case "recover":
		f := flag.NewFlagSet("recover", flag.ContinueOnError)
		f.SetOutput(io.Discard)
		state := f.String("state", "", "private archive directory")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *state == "" || f.NArg() != 0 {
			return errors.New("recover requires --state")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := deploy.RecoverLocal(ctx, *state)
		if err == nil {
			_, err = fmt.Fprintln(output, "Baseline restored and verified.")
		}
		return err
	case "deployment-status":
		f := flag.NewFlagSet("deployment-status", flag.ContinueOnError)
		f.SetOutput(io.Discard)
		state := f.String("state", "", "private archive directory")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *state == "" || f.NArg() != 0 {
			return errors.New("deployment-status requires --state")
		}
		result, err := deploy.Status(*state)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(result)
	case "deploy":
		f := flag.NewFlagSet("deploy", flag.ContinueOnError)
		f.SetOutput(io.Discard)
		state := f.String("state", "", "prepared private archive")
		controller := f.String("controller", "http://127.0.0.1:9090", "local controller")
		proxy := f.String("proxy", "", "local Mihomo proxy")
		target := f.String("target", "https://api.bilibili.com/x/web-interface/nav", "HTTPS reachability target")
		hold := f.Duration("hold", 5*time.Second, "health observation period")
		timeout := f.Duration("timeout", 8*time.Second, "HTTP probe timeout")
		confirm := f.Bool("confirm", false, "explicitly authorize replacing active config")
		jsonOut := f.Bool("json", false, "JSON outcome")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 || *state == "" || *proxy == "" || !*confirm {
			return errors.New("deploy requires --state, local --proxy and --confirm")
		}
		if *hold < 0 || *hold > time.Minute || *timeout <= 0 || *timeout > 30*time.Second {
			return errors.New("invalid hold/timeout")
		}
		controllerClient, err := core.NewClient(*controller, os.Getenv("RINO_MIHOMO_SECRET"), 8*time.Second)
		if err != nil {
			return err
		}
		probe, err := localProxyProbe(*proxy, *target, *timeout)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), *hold+20*time.Second)
		defer cancel()
		outcome, deployErr := deploy.DeployDisk(ctx, *state, *controller, controllerClient, probe, *hold, spawnGuard)
		if *jsonOut {
			if err := json.NewEncoder(output).Encode(outcome); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(output, "applied=%t healthy=%t committed=%t recovered=%t\n", outcome.Applied, outcome.Healthy, outcome.Committed, outcome.Recovered); err != nil {
				return err
			}
		}
		return deployErr
	default:
		return errors.New("usage: rino core deploy|watchdog|recover|deployment-status")
	}
}

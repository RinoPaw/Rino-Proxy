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
	"time"

	"github.com/RinoPaw/Rino-Proxy/internal/core"
	"github.com/RinoPaw/Rino-Proxy/internal/deploy"
	"github.com/RinoPaw/Rino-Proxy/internal/health"
)

// runCoreTrial applies a memory-only candidate for a bounded interval.
// The current on-disk configuration remains untouched.
func runCoreTrial(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("core trial", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state", "", "existing verified offline archive")
	controller := flags.String("controller", "http://127.0.0.1:9090", "local Mihomo controller")
	proxy := flags.String("proxy", "", "local HTTP/mixed-port proxy for HTTPS probe")
	target := flags.String("target", "https://github.com/", "HTTPS target to probe through the proxy")
	hold := flags.Duration("hold", 10*time.Second, "duration before baseline rollback")
	timeout := flags.Duration("timeout", 8*time.Second, "per-controller/probe timeout")
	jsonOutput := flags.Bool("json", false, "print redacted trial result")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *state == "" || *proxy == "" {
		return errors.New("usage: rino core trial --state DIR --proxy http://127.0.0.1:PORT [--controller URL] [--hold 10s] [--target https://...]")
	}
	if *hold < 0 || *hold > 2*time.Minute || *timeout <= 0 || *timeout > 30*time.Second {
		return errors.New("invalid hold or timeout")
	}
	u, err := url.Parse(*target)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return errors.New("probe target must be an HTTPS URL")
	}
	// OpenWrt may bind its proxy to br-lan; require an address assigned
	// to THIS host. The controller remains strictly loopback-only.
	if err := core.ValidateLocalProxy(*proxy); err != nil {
		return fmt.Errorf("proxy must be a local HTTP listener: %w", err)
	}
	control, err := core.NewClient(*controller, os.Getenv("RINO_MIHOMO_SECRET"), *timeout)
	if err != nil {
		return err
	}
	probe := func(ctx context.Context) error {
		client, err := health.NewClient(*proxy, *timeout)
		if err != nil {
			return err
		}
		defer client.CloseIdleConnections()
		result := health.Probe(ctx, client, health.Target{Name: "trial", URL: *target})
		if !health.Accepted(result) {
			return fmt.Errorf("HTTPS target returned no valid response through proxy: %s", result.Detail)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), *hold+3*(*timeout)+15*time.Second)
	defer cancel()
	report, err := deploy.Trial(ctx, *state, control, *hold, probe)
	if *jsonOutput {
		if encodeErr := json.NewEncoder(output).Encode(report); encodeErr != nil {
			return encodeErr
		}
	} else {
		if _, printErr := fmt.Fprintf(output, "Trial: applied=%t healthy=%t restored=%t\n", report.Applied, report.Healthy, report.Restored); printErr != nil {
			return printErr
		}
	}
	return err
}

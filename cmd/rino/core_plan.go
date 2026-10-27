package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/RinoPaw/Rino-Proxy/internal/deploy"
)

// runCorePlan is deliberately read-only. No --apply/--force command exists.
func runCorePlan(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("core plan", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	current := flags.String("current", "", "existing local Mihomo config")
	candidate := flags.String("candidate", "", "new local candidate config")
	binary := flags.String("mihomo", "mihomo", "Mihomo executable for native validation")
	home := flags.String("home", "", "optional Mihomo data directory")
	timeout := flags.Duration("timeout", 30*time.Second, "validation deadline")
	jsonOutput := flags.Bool("json", false, "emit sanitized JSON report")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *current == "" || *candidate == "" {
		return errors.New("usage: rino core plan --current old.yaml --candidate new.yaml [--mihomo BINARY] [--home DIR] [--json]")
	}
	if *timeout <= 0 || *timeout > 2*time.Minute {
		return errors.New("timeout must be between 1ns and 2 minutes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report, err := deploy.BuildPlan(ctx, *current, *candidate, deploy.MihomoValidator{Binary: *binary, Home: *home})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(output).Encode(report)
	}
	_, err = fmt.Fprintf(output, "Validation: passed; file identical: %t; YAML equivalent: %t\nChanged fields: %v\nHigh-risk fields: %v\nApply permitted: %t (offline plan only)\n", report.ContentIdentical, report.SemanticallyIdentical, report.ChangedFields, report.RiskFields, report.ApplyAllowed)
	return err
}

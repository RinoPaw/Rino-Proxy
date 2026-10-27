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

func runCoreArchive(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: rino core archive prepare|verify|export")
	}
	switch args[0] {
	case "prepare":
		flags := flag.NewFlagSet("core archive prepare", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		current := flags.String("current", "", "existing local Mihomo config")
		candidate := flags.String("candidate", "", "candidate config")
		state := flags.String("state", "", "new private archive directory")
		binary := flags.String("mihomo", "mihomo", "Mihomo executable")
		home := flags.String("home", "", "optional Mihomo data directory")
		timeout := flags.Duration("timeout", 30*time.Second, "validation deadline")
		jsonOutput := flags.Bool("json", false, "JSON summary without paths or config content")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *current == "" || *candidate == "" || *state == "" {
			return errors.New("provide --current, --candidate, and --state")
		}
		if *timeout <= 0 || *timeout > 2*time.Minute {
			return errors.New("timeout must be between 1ns and 2 minutes")
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		status, err := deploy.PrepareArchive(ctx, *current, *candidate, *state, deploy.MihomoValidator{Binary: *binary, Home: *home})
		if err != nil {
			return err
		}
		return showArchive(output, status, *jsonOutput)
	case "verify":
		flags := flag.NewFlagSet("core archive verify", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		state := flags.String("state", "", "archive directory")
		jsonOutput := flags.Bool("json", false, "JSON summary without paths or config content")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *state == "" {
			return errors.New("provide --state")
		}
		status, err := deploy.ReadArchive(*state)
		if err != nil {
			return err
		}
		return showArchive(output, status, *jsonOutput)
	case "export":
		flags := flag.NewFlagSet("core archive export", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		state := flags.String("state", "", "archive directory")
		target := flags.String("output", "", "new file path for recovered baseline")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *state == "" || *target == "" {
			return errors.New("provide --state and --output")
		}
		if err := deploy.ExportBaseline(*state, *target); err != nil {
			return err
		}
		_, err := fmt.Fprintln(output, "Verified baseline exported to new file. No active configuration modified.")
		return err
	default:
		return errors.New("usage: rino core archive prepare|verify|export")
	}
}

func showArchive(output io.Writer, status deploy.ArchiveStatus, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(status)
	}
	_, err := fmt.Fprintf(output, "Archive: %s; hashes: %s / %s; target unchanged: %t; apply enabled: %t\n",
		status.Phase, status.BaselineSHA256, status.CandidateSHA256,
		status.TargetMatchesBaseline, status.ApplyAllowed)
	return err
}

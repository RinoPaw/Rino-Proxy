package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/RinoPaw/Rino-Proxy/internal/health"
)

func runHealth(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "check" {
		return errors.New("usage: rino health check --proxy http://HOST:PORT [--timeout 15s] [--json]")
	}
	flags := flag.NewFlagSet("health check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	proxy := flags.String("proxy", "", "explicit HTTP proxy URL")
	timeout := flags.Duration("timeout", 15*time.Second, "per-site timeout")
	jsonOutput := flags.Bool("json", false, "print JSON results")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	client, err := health.NewClient(*proxy, *timeout)
	if err != nil {
		return err
	}
	results := health.ProbeAll(context.Background(), client, health.DefaultTargets)
	passed := health.Satisfies(results)
	if *jsonOutput {
		if err := json.NewEncoder(output).Encode(struct {
			Results []health.Result `json:"results"`
			Passed  bool            `json:"passed"`
		}{Results: results, Passed: passed}); err != nil {
			return err
		}
		if !passed {
			return errors.New("website availability condition not met")
		}
		return nil
	}
	for _, result := range results {
		if _, err := fmt.Fprintf(output, "%-9s %-10s HTTP=%-3d %5dms %s\n",
			result.Name, result.Status, result.HTTPCode, result.LatencyMS, result.Detail); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(output, "bilibili && github && (google || youtube || chatgpt): %t\n", passed)
	if err != nil {
		return err
	}
	if !passed {
		return errors.New("website availability condition not met")
	}
	return nil
}

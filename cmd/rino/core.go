package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/RinoPaw/Rino-Proxy/internal/core"
)

func runCore(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "status" {
		return errors.New("usage: rino core status [--controller http://127.0.0.1:9090] [--timeout 5s] [--json]")
	}
	flags := flag.NewFlagSet("core status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	address := flags.String("controller", "http://127.0.0.1:9090", "local Mihomo controller")
	timeout := flags.Duration("timeout", 5*time.Second, "overall query timeout")
	jsonOutput := flags.Bool("json", false, "emit sanitized JSON")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional argument")
	}
	client, err := core.NewClient(*address, os.Getenv("RINO_MIHOMO_SECRET"), *timeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(output).Encode(snapshot)
	}
	if _, err := fmt.Fprintf(output, "Mihomo %s (meta=%t)\nMode: %s; LAN: %t; TUN: %t; Mixed port: %d\nProxy entries: %d; groups: %d\n", snapshot.Version, snapshot.Meta, snapshot.Config.Mode, snapshot.Config.AllowLAN, snapshot.Config.TUNEnabled, snapshot.Config.MixedPort, snapshot.ProxyEntries, len(snapshot.Groups)); err != nil {
		return err
	}
	for _, group := range snapshot.Groups {
		if _, err := fmt.Fprintf(output, "%s (%s): %s [%d choices]\n", group.Name, group.Type, group.Selected, group.Candidates); err != nil {
			return err
		}
	}
	return nil
}

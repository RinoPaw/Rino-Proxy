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

func runMatrix(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("health matrix", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	controllerURL := flags.String("controller", "http://127.0.0.1:18994", "isolated Mihomo controller")
	proxyURL := flags.String("proxy", "http://127.0.0.1:18993", "isolated Mihomo mixed port")
	pattern := flags.String("match", "", "node name regex")
	limit := flags.Int("limit", 12, "max nodes; 0 means all")
	timeout := flags.Duration("timeout", 8*time.Second, "per-site timeout")
	jsonOutput := flags.Bool("json", false, "emit JSON results")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unknown positional argument")
	}
	if *timeout <= 0 || *timeout > 2*time.Minute {
		return errors.New("timeout must be between 1 ns and 2 minutes")
	}
	controller, err := health.NewController(*controllerURL)
	if err != nil {
		return err
	}
	result, err := health.RunMatrix(context.Background(), controller, *proxyURL, *pattern, *limit, *timeout)
	if *jsonOutput {
		if e := json.NewEncoder(output).Encode(result); e != nil {
			return e
		}
	} else {
		if _, e := fmt.Fprint(output, health.MatrixSummary(result)); e != nil {
			return e
		}
	}
	if err != nil {
		return err
	}
	if !result.CoveragePassed {
		return errors.New("no combination of tested nodes satisfies site coverage condition")
	}
	return nil
}

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/RinoPaw/Rino-Proxy/internal/policy"
)

func runPolicy(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "render" {
		return errors.New("usage: rino policy render --base backup.yaml --matrix matrix.json --output candidate.yaml")
	}
	fs := flag.NewFlagSet("policy render", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	base := fs.String("base", "", "existing private Mihomo configuration")
	matrix := fs.String("matrix", "", "node-by-site measurement snapshot")
	out := fs.String("output", "", "generated candidate configuration")
	max := fs.Int("max-per-site", 8, "maximum preferred nodes per website")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || *base == "" || *matrix == "" || *out == "" {
		return errors.New("provide --base, --matrix and --output")
	}
	for _, path := range []string{*base, *matrix} {
		if err := differentPaths(path, *out); err != nil {
			return err
		}
	}
	baseData, err := os.ReadFile(*base)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	matrixData, err := os.ReadFile(*matrix)
	if err != nil {
		return fmt.Errorf("read matrix: %w", err)
	}
	generated, summary, err := policy.Render(baseData, matrixData, *max)
	if err != nil {
		return err
	}
	if err := atomicWrite(*out, generated); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Generated %s. Groups: %v. Validate with mihomo -t before deploying. %s\n", *out, summary.Groups, time.Now().Format(time.DateTime))
	return err
}

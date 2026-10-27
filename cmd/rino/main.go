package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/RinoPaw/Rino-Proxy/internal/mihomo"
	"github.com/RinoPaw/Rino-Proxy/internal/profile"
)

const usage = `Rino Proxy v0.1 — Mihomo Profile Flatten

Usage:
  rino profile merge PROFILE... --output merged.yaml [--manifest nodes.json]
  rino core status [--controller http://127.0.0.1:9090] [--json]
  rino health check --proxy http://HOST:PORT [--timeout 15s] [--json]
  rino health matrix --limit 12 [--match REGEX] [--json]
  rino router render --output config.yaml [--upstream http://100.64.0.3:17890]
  rino policy render --base backup.yaml --matrix matrix.json --output candidate.yaml

Generated configs default to DIRECT and never change your active proxy.
Provider-only YAML, remote URLs and automatic subscription updates are not yet supported.
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "rino:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "-h" || args[0] == "--help")) {
		_, _ = io.WriteString(stdout, usage)
		return nil
	}
	if len(args) > 0 && args[0] == "core" {
		return runCore(args[1:], stdout)
	}
	if len(args) > 0 && args[0] == "policy" {
		return runPolicy(args[1:], stdout)
	}
	if len(args) > 0 && args[0] == "router" {
		return runRouter(args[1:], stdout)
	}
	if len(args) > 1 && args[0] == "health" && args[1] == "matrix" {
		return runMatrix(args[2:], stdout)
	}
	if len(args) > 0 && args[0] == "health" {
		return runHealth(args[1:], stdout)
	}
	if len(args) < 2 || args[0] != "profile" || args[1] != "merge" {
		return errors.New("unknown command; use --help")
	}
	var inputs []string
	var output, manifest string
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--output", "-o", "--manifest":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return fmt.Errorf("missing value for %q", args[i])
			}
			if args[i] == "--manifest" {
				manifest = args[i+1]
			} else {
				output = args[i+1]
			}
			i++
		case "--help", "-h":
			_, _ = io.WriteString(stdout, usage)
			return nil
		case "--":
			inputs = append(inputs, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown option %q", args[i])
			}
			inputs = append(inputs, args[i])
		}
	}
	if len(inputs) == 0 || output == "" {
		return errors.New("supply profiles and --output")
	}
	if manifest != "" {
		if err := differentPaths(manifest, output); err != nil {
			return err
		}
	}
	for _, input := range inputs {
		if err := differentPaths(input, output); err != nil {
			return err
		}
		if manifest != "" {
			if err := differentPaths(input, manifest); err != nil {
				return err
			}
		}
	}
	list := make([]profile.Profile, 0, len(inputs))
	for _, input := range inputs {
		p, err := profile.Read(input)
		if err != nil {
			return err
		}
		list = append(list, p)
	}
	result, err := profile.Merge(list)
	if err != nil {
		return err
	}
	data, err := mihomo.Export(result.Nodes)
	if err != nil {
		return err
	}
	if manifest != "" {
		b, err := json.MarshalIndent(result.Provenance, "", "  ")
		if err != nil {
			return err
		}
		if err := atomicWrite(manifest, append(b, byte(10))); err != nil {
			return err
		}
	}
	if err := atomicWrite(output, data); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Merged %d profiles: %d nodes, %d exact duplicates removed. Output: %s (DIRECT first)", len(list), len(result.Nodes), result.DuplicateCount, output)
	return nil
}

func differentPaths(a, b string) error {
	pa, err := filepath.Abs(a)
	if err != nil {
		return err
	}
	pb, err := filepath.Abs(b)
	if err != nil {
		return err
	}
	if pa == pb {
		return fmt.Errorf("refusing to overwrite input/output %q", a)
	}
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA == nil && errB == nil && os.SameFile(infoA, infoB) {
		return fmt.Errorf("paths %q and %q point to the same file", a, b)
	}
	return nil
}

// Output can contain credentials: use 0600 and write via same-directory rename.
func atomicWrite(path string, data []byte) error {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("refusing to replace non-regular output %q", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".rino-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return nil
}

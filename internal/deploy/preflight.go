// Package deploy builds offline configuration preflight reports.
// It never changes a live configuration or contacts a controller.
package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

const maxConfigBytes = 16 << 20

// A Report contains only fingerprints and allowlisted field names.
// It never includes proxy credentials, subscription URLs, or YAML values.
type Report struct {
	CurrentSHA256         string   `json:"current_sha256"`
	CandidateSHA256       string   `json:"candidate_sha256"`
	ContentIdentical      bool     `json:"content_identical"`
	SemanticallyIdentical bool     `json:"semantically_identical"`
	ChangedFields         []string `json:"changed_fields"`
	RiskFields            []string `json:"risk_fields"`
	Validated             bool     `json:"validated"`
	ApplyAllowed          bool     `json:"apply_allowed"`
}

// Validator validates a candidate using the actual target Mihomo core.
// Fake validators are used by unit tests; production always uses MihomoValidator.
type Validator interface {
	Validate(ctx context.Context, candidatePath string) error
}

// MihomoValidator invokes the Mihomo binary with argv, never a shell.
// Output is suppressed to prevent accidental exposure of credentials.
type MihomoValidator struct {
	Binary string
	Home   string
}

func (v MihomoValidator) Validate(ctx context.Context, candidatePath string) error {
	if v.Binary == "" {
		return errors.New("mihomo executable is required")
	}
	args := []string{"-t", "-f", candidatePath}
	if v.Home != "" {
		info, err := os.Stat(v.Home)
		if err != nil || !info.IsDir() {
			return errors.New("mihomo home must be an existing directory")
		}
		args = append(args, "-d", v.Home)
	}
	cmd := exec.CommandContext(ctx, v.Binary, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("mihomo validation timed out or was canceled")
		}
		return errors.New("mihomo validation failed (diagnostic output withheld)")
	}
	return nil
}

var sensitiveFields = map[string]bool{
	"allow-lan": true, "authentication": true, "bind-address": true,
	"external-controller": true, "external-controller-tls": true,
	"external-controller-unix": true, "external-controller-pipe": true,
	"external-controller-cors": true, "secret": true, "tls": true,
	"mixed-port": true, "port": true, "socks-port": true, "redir-port": true,
	"tproxy-port": true, "tun": true, "dns": true, "ipv6": true,
	"interface-name": true, "routing-mark": true, "listeners": true,
	"rules": true, "rule-providers": true, "proxy-providers": true,
	"proxy-groups": true, "proxies": true, "mode": true,
	"sniffer": true, "hosts": true, "profile": true,
	"geodata-mode": true, "geox-url": true,
}

// Unknown top-level keys are grouped as "other-top-level" to avoid leaking
// arbitrary, potentially sensitive key names in reports.
var knownFields = func() map[string]bool {
	m := map[string]bool{
		"log-level": true, "find-process-mode": true, "unified-delay": true,
		"tcp-concurrent": true, "global-client-fingerprint": true,
		"keep-alive-interval": true, "keep-alive-idle": true,
		"geo-auto-update": true, "geo-update-interval": true,
		"external-ui": true, "external-ui-url": true,
		"external-ui-name": true, "allow-lan": true,
		"lan-allowed-ips": true, "lan-disallowed-ips": true,
		"proxy-server-nameserver": true, "skip-auth-prefixes": true,
		"ntp": true,
	}
	for key := range sensitiveFields {
		m[key] = true
	}
	return m
}()

type document struct {
	data   []byte
	sha256 string
	fields map[string]any
	info   os.FileInfo
}

func readDocument(path string) (document, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return document{}, fmt.Errorf("cannot stat configuration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxConfigBytes {
		return document{}, errors.New("configuration must be a regular, non-symlink file of at most 16 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return document{}, fmt.Errorf("cannot open configuration: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return document{}, errors.New("cannot read configuration")
	}
	if len(data) == 0 || len(data) > maxConfigBytes {
		return document{}, errors.New("configuration must contain 1 to 16 MiB of data")
	}
	var fields map[string]any
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&fields); err != nil || len(fields) == 0 {
		return document{}, errors.New("configuration is not a nonempty YAML mapping")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return document{}, errors.New("configuration contains multiple YAML documents")
	} else if !errors.Is(err, io.EOF) {
		return document{}, errors.New("invalid trailing YAML")
	}
	// Final lstat detects a file swapped during reading, without following links.
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(info, after) || after.Size() != int64(len(data)) {
		return document{}, errors.New("configuration changed while reading")
	}
	digest := sha256.Sum256(data)
	return document{data: data, sha256: hex.EncodeToString(digest[:]), fields: fields, info: info}, nil
}

func safeFieldName(name string) string {
	if knownFields[name] {
		return name
	}
	return "other-top-level"
}

// BuildPlan validates a candidate and compares it with the current file.
// The report's ApplyAllowed flag is deliberately always false: this package
// has no config-writing API, journal, or external recovery watchdog yet.
func BuildPlan(ctx context.Context, currentPath, candidatePath string, validator Validator) (Report, error) {
	report := Report{
		ChangedFields: []string{}, RiskFields: []string{},
		ApplyAllowed: false,
	}
	if validator == nil {
		return report, errors.New("validator is required")
	}
	a, err := filepath.Abs(currentPath)
	if err != nil {
		return report, errors.New("invalid current path")
	}
	b, err := filepath.Abs(candidatePath)
	if err != nil {
		return report, errors.New("invalid candidate path")
	}
	if a == b {
		return report, errors.New("current and candidate must be different files")
	}
	current, err := readDocument(a)
	if err != nil {
		return report, fmt.Errorf("current config: %w", err)
	}
	candidate, err := readDocument(b)
	if err != nil {
		return report, fmt.Errorf("candidate config: %w", err)
	}
	if os.SameFile(current.info, candidate.info) {
		return report, errors.New("current and candidate refer to the same file")
	}
	report.CurrentSHA256 = current.sha256
	report.CandidateSHA256 = candidate.sha256
	report.ContentIdentical = current.sha256 == candidate.sha256
	diffs := map[string]bool{}
	for key, before := range current.fields {
		if after, ok := candidate.fields[key]; !ok || !reflect.DeepEqual(before, after) {
			diffs[key] = true
		}
	}
	for key, after := range candidate.fields {
		if before, ok := current.fields[key]; !ok || !reflect.DeepEqual(before, after) {
			diffs[key] = true
		}
	}
	report.SemanticallyIdentical = len(diffs) == 0
	names := map[string]bool{}
	risks := map[string]bool{}
	for key := range diffs {
		label := safeFieldName(key)
		names[label] = true
		// Unknown keys may have routing/network effects; treat them as risky.
		if sensitiveFields[key] || !knownFields[key] {
			risks[label] = true
		}
	}
	for key := range names {
		report.ChangedFields = append(report.ChangedFields, key)
	}
	for key := range risks {
		report.RiskFields = append(report.RiskFields, key)
	}
	sort.Strings(report.ChangedFields)
	sort.Strings(report.RiskFields)
	if err := validator.Validate(ctx, b); err != nil {
		return report, err
	}
	// Validation could race with an editor or subscription updater. Refuse
	// to report success if the candidate changed after validation.
	after, err := readDocument(b)
	if err != nil || after.sha256 != candidate.sha256 || !os.SameFile(after.info, candidate.info) {
		return report, errors.New("candidate changed during validation")
	}
	report.Validated = true
	return report, nil
}

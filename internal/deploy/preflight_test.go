package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type validatorFunc func(context.Context, string) error

func (f validatorFunc) Validate(ctx context.Context, path string) error {
	return f(ctx, path)
}

func fixture(t *testing.T, dir, name, data string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

const oldConfig = "mixed-port: 7890\nsecret: hidden-token\nrules:\n  - MATCH,DIRECT\n"
const newConfig = "mixed-port: 7891\nsecret: hidden-token\nrules:\n  - MATCH,Proxy\n"

func TestBuildPlanReportsNamesOnly(t *testing.T) {
	dir := t.TempDir()
	current := fixture(t, dir, "old.yaml", oldConfig)
	candidate := fixture(t, dir, "new.yaml", newConfig)
	called := 0
	p, err := BuildPlan(context.Background(), current, candidate, validatorFunc(func(_ context.Context, path string) error {
		called++
		if path != candidate {
			t.Errorf("wrong validated path %s", path)
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || !p.Validated || p.ApplyAllowed || p.SemanticallyIdentical || p.ContentIdentical {
		t.Fatalf("unexpected report %#v", p)
	}
	expected := []string{"mixed-port", "rules"}
	if !reflect.DeepEqual(p.ChangedFields, expected) || !reflect.DeepEqual(p.RiskFields, expected) {
		t.Fatalf("wrong fields %#v", p)
	}
	if len(p.CurrentSHA256) != 64 || len(p.CandidateSHA256) != 64 {
		t.Fatal("missing hashes")
	}
	output, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hidden-token", "MATCH,Proxy", "7891"} {
		if strings.Contains(string(output), secret) {
			t.Fatalf("secret leaked in plan: %s", output)
		}
	}
}

func TestEquivalentYAML(t *testing.T) {
	dir := t.TempDir()
	a := fixture(t, dir, "a.yaml", oldConfig)
	b := fixture(t, dir, "b.yaml", "# only a comment differs\n"+oldConfig)
	p, err := BuildPlan(context.Background(), a, b, validatorFunc(func(context.Context, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if !p.SemanticallyIdentical || p.ContentIdentical || len(p.ChangedFields) != 0 || !p.Validated {
		t.Fatalf("expected equivalent documents %#v", p)
	}
}

func TestUnknownTopLevelFieldRedacted(t *testing.T) {
	dir := t.TempDir()
	a := fixture(t, dir, "a.yaml", oldConfig)
	b := fixture(t, dir, "b.yaml", oldConfig+"private-metadata-token: secretvalue\n")
	p, err := BuildPlan(context.Background(), a, b, validatorFunc(func(context.Context, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.ChangedFields, []string{"other-top-level"}) || !reflect.DeepEqual(p.RiskFields, []string{"other-top-level"}) {
		t.Fatalf("unknown key leaked: %#v", p)
	}
}

func TestReadGuards(t *testing.T) {
	dir := t.TempDir()
	current := fixture(t, dir, "old.yaml", oldConfig)
	baseValidator := validatorFunc(func(context.Context, string) error { return nil })
	t.Run("same path", func(t *testing.T) {
		if _, err := BuildPlan(context.Background(), current, current, baseValidator); err == nil {
			t.Fatal("accepted same path")
		}
	})
	t.Run("hardlink", func(t *testing.T) {
		alias := filepath.Join(dir, "hardlink.yaml")
		if err := os.Link(current, alias); err != nil {
			t.Skipf("no hard link support: %v", err)
		}
		if _, err := BuildPlan(context.Background(), current, alias, baseValidator); err == nil {
			t.Fatal("accepted hard link")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		alias := filepath.Join(dir, "symlink.yaml")
		if err := os.Symlink(current, alias); err != nil {
			t.Skipf("no symlink support: %v", err)
		}
		if _, err := BuildPlan(context.Background(), current, alias, baseValidator); err == nil {
			t.Fatal("accepted symlink")
		}
	})
	for label, yamlData := range map[string]string{
		"empty": "", "sequence": "- x\n", "malformed": "ports: [\n",
		"multidoc":  "mode: rule\n---\nmode: direct\n",
		"duplicate": "mode: rule\nmode: direct\n",
	} {
		t.Run(label, func(t *testing.T) {
			candidate := fixture(t, dir, label+".yaml", yamlData)
			if _, err := BuildPlan(context.Background(), current, candidate, baseValidator); err == nil {
				t.Fatal("accepted invalid candidate")
			}
		})
	}
	t.Run("too large", func(t *testing.T) {
		candidate := fixture(t, dir, "large.yaml", strings.Repeat("x", maxConfigBytes+1))
		if _, err := BuildPlan(context.Background(), current, candidate, baseValidator); err == nil {
			t.Fatal("accepted oversized candidate")
		}
	})
	t.Run("nil validator", func(t *testing.T) {
		if _, err := BuildPlan(context.Background(), current, current+"-new", nil); err == nil {
			t.Fatal("accepted absent validator")
		}
	})
}

func TestChangedDuringValidation(t *testing.T) {
	dir := t.TempDir()
	a := fixture(t, dir, "a.yaml", oldConfig)
	b := fixture(t, dir, "b.yaml", newConfig)
	report, err := BuildPlan(context.Background(), a, b, validatorFunc(func(_ context.Context, path string) error {
		return os.WriteFile(path, []byte(newConfig+"log-level: debug\n"), 0600)
	}))
	if err == nil || !strings.Contains(err.Error(), "changed during validation") || report.Validated {
		t.Fatalf("expected change error, report=%#v err=%v", report, err)
	}
}

func TestValidatorFailure(t *testing.T) {
	dir := t.TempDir()
	a := fixture(t, dir, "a.yaml", oldConfig)
	b := fixture(t, dir, "b.yaml", newConfig)
	_, err := BuildPlan(context.Background(), a, b, validatorFunc(func(context.Context, string) error {
		return errors.New("simulated validator rejection")
	}))
	if err == nil || err.Error() != "simulated validator rejection" {
		t.Fatalf("expected validator error: %v", err)
	}
}

func TestMihomoValidationSuppressesOutput(t *testing.T) {
	v := MihomoValidator{Binary: "rino-nonexistent-mihomo-XYZ"}
	err := v.Validate(context.Background(), "something.yaml")
	if err == nil || strings.Contains(err.Error(), "rino-nonexistent-mihomo-XYZ") {
		t.Fatalf("leaked executor internals: %v", err)
	}
	v.Home = filepath.Join(t.TempDir(), "not-a-home")
	if err := v.Validate(context.Background(), "something.yaml"); err == nil {
		t.Fatal("accepted missing home")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (MihomoValidator{Binary: "rino-nonexistent-mihomo-XYZ"}).Validate(ctx, "something.yaml"); err == nil {
		t.Fatal("expected cancellation error")
	}
}

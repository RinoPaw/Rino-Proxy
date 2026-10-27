package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"gopkg.in/yaml.v3"
)

func TestEndToEnd(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "merged.yaml")
	manifest := filepath.Join(root, "nodes.json")
	star := filepath.Join("..", "..", "testdata", "star.yaml")
	fubuki := filepath.Join("..", "..", "testdata", "fubuki.yaml")
	var log bytes.Buffer
	if err := run([]string{"profile","merge",star,"--output",out,fubuki,"--manifest",manifest}, &log); err != nil { t.Fatal(err) }
	if !strings.Contains(log.String(), "3 nodes, 1 exact duplicates") { t.Fatal(log.String()) }
	b, err := os.ReadFile(out)
	if err != nil { t.Fatal(err) }
	var doc struct { Proxies []map[string]any `yaml:"proxies"` }
	if err := yaml.Unmarshal(b, &doc); err != nil { t.Fatal(err) }
	if len(doc.Proxies) != 3 { t.Fatalf("got %d nodes", len(doc.Proxies)) }
	if stat, err := os.Stat(out); err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("insecure output permissions: %v", err)
	}
	data, err := os.ReadFile(manifest)
	if err != nil || !bytes.Contains(data, []byte(`"sources"`)) { t.Fatalf("manifest missing: %v", err) }
}

func TestDoNotOverwriteSource(t *testing.T) {
	path := filepath.Join("..","..","testdata","star.yaml")
	before, _ := os.ReadFile(path)
	if err := run([]string{"profile","merge",path,"--output",path}, &bytes.Buffer{}); err == nil {
		t.Fatal("overwrote the source")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) { t.Fatal("input changed") }
}

func TestRequireOutputAndRejectUnknownOptions(t *testing.T) {
	for _, args := range [][]string{
		{"profile","merge","x.yaml"},
		{"profile","merge","x.yaml","-unknown"},
	} {
		if err := run(args, &bytes.Buffer{}); err == nil { t.Fatalf("accepted invalid args: %v", args) }
	}
}

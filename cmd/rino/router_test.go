package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRouterRenderDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.yaml")
	var output bytes.Buffer
	if err := runRouter([]string{"render", "--output", path}, &output); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Port       int              `yaml:"mixed-port"`
		AllowLAN   bool             `yaml:"allow-lan"`
		Bind       string           `yaml:"bind-address"`
		Controller string           `yaml:"external-controller"`
		Rules      []string         `yaml:"rules"`
		Proxies    []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 7890 || !cfg.AllowLAN || cfg.Bind != "172.27.207.1" ||
		cfg.Controller != "127.0.0.1:9091" || len(cfg.Proxies) != 1 ||
		cfg.Proxies[0]["server"] != "100.64.0.3" || cfg.Rules[len(cfg.Rules)-1] != "MATCH,DIRECT" {
		t.Fatalf("invalid gateway config: %+v", cfg)
	}
	if !strings.Contains(string(content), "DOMAIN-SUFFIX,bilibili.com,DIRECT") ||
		!strings.Contains(string(content), "DOMAIN-SUFFIX,github.com,Tencent") ||
		strings.Contains(string(content), "tun:") {
		t.Fatalf("unsafe or incomplete routing config: %s", content)
	}
}

func TestRouterRenderRejectsPublicUpstream(t *testing.T) {
	for _, upstream := range []string{"https://100.64.0.3:17890", "http://8.8.8.8:17890",
		"http://x.invalid:17890", "http://user:password@100.64.0.3:17890"} {
		err := runRouter([]string{"render", "--upstream", upstream, "--output", filepath.Join(t.TempDir(), "out.yaml")}, &bytes.Buffer{})
		if err == nil {
			t.Fatalf("accepted unsafe upstream %s", upstream)
		}
	}
}

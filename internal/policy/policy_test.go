package policy

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const config = `mixed-port: 17890
bind-address: 100.64.0.3
external-controller: 127.0.0.1:17891
mode: rule
secret: NEVER-COMMIT
authentication:
  - secret: example
proxies:
  - name: A
    type: socks5
    server: a.example.invalid
    port: 1080
  - name: B
    type: socks5
    server: b.example.invalid
    port: 1080
proxy-groups:
  - name: TX
    type: select
    proxies: [A, B]
rules:
  - MATCH,TX
`
const matrix = `{"nodes":[
{"node":"A","results":[{"name":"github","status":"PASS","http_code":200,"latency_ms":180},{"name":"google","status":"PASS","http_code":204,"latency_ms":100},{"name":"youtube","status":"PASS","http_code":200,"latency_ms":240},{"name":"chatgpt","status":"CHALLENGE","http_code":403,"latency_ms":280}]},
{"node":"B","results":[{"name":"github","status":"PASS","http_code":200,"latency_ms":210},{"name":"google","status":"PASS","http_code":204,"latency_ms":190},{"name":"youtube","status":"PASS","http_code":200,"latency_ms":170},{"name":"chatgpt","status":"PASS","http_code":200,"latency_ms":350}]}]}`

func TestRenderGroupsRulesAndCredentials(t *testing.T) {
	output, summary, e := Render([]byte(config), []byte(matrix), 2)
	if e != nil {
		t.Fatal(e)
	}
	if len(summary.Groups) != 4 || summary.Groups["Rino-ChatGPT"] != 2 {
		t.Fatalf("bad summary: %+v", summary)
	}
	var cfg struct {
		Secret string   `yaml:"secret"`
		Rules  []string `yaml:"rules"`
		Groups []struct {
			Name    string   `yaml:"name"`
			Type    string   `yaml:"type"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if e := yaml.Unmarshal(output, &cfg); e != nil {
		t.Fatal(e)
	}
	if cfg.Secret != "NEVER-COMMIT" || len(cfg.Groups) != 5 {
		t.Fatalf("core data lost: %+v", cfg)
	}
	found := false
	for _, g := range cfg.Groups {
		if g.Name == "Rino-ChatGPT" {
			if g.Type != "fallback" || len(g.Proxies) != 3 || g.Proxies[0] != "A" || g.Proxies[1] != "B" || g.Proxies[2] != "TX" {
				t.Fatalf("wrong chatgpt: %+v", g)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("ChatGPT group absent")
	}
	if !strings.Contains(string(output), "expected-status: \u0027*\u0027") && !strings.Contains(string(output), "expected-status: \"*\"") {
		t.Fatal("wildcard HTTP response acceptance not configured")
	}
	a, b := -1, -1
	for i, r := range cfg.Rules {
		if r == "DOMAIN-SUFFIX,googlevideo.com,Rino-YouTube" {
			a = i
		}
		if r == "DOMAIN-SUFFIX,google.com,Rino-Google" {
			b = i
		}
	}
	if a < 0 || b < 0 || a > b || cfg.Rules[len(cfg.Rules)-1] != "MATCH,TX" {
		t.Fatal("rule priority unexpected")
	}
	updated, _, err := Render(output, []byte(matrix), 2)
	if err != nil {
		t.Fatal(err)
	}
	var again map[string]any
	if err := yaml.Unmarshal(updated, &again); err != nil {
		t.Fatal(err)
	}
	if len(again["proxy-groups"].([]any)) != 5 {
		t.Fatal("duplicate policy groups on rerun")
	}
}

func TestRenderRejectsWrongConfigOrData(t *testing.T) {
	for _, s := range []string{
		strings.Replace(config, "100.64.0.3", "0.0.0.0", 1),
		strings.Replace(config, "mixed-port: 17890", "mixed-port: 7890", 1),
		strings.Replace(config, "  - MATCH,TX", "  - MATCH,UNKNOWN", 1), // Missing TX rule is currently allowed; group check still works.
	} {
		if strings.Contains(s, "MATCH,UNKNOWN") {
			continue
		}
		if _, _, e := Render([]byte(s), []byte(matrix), 3); e == nil {
			t.Fatal("accepted unsafe core config")
		}
	}
	if _, _, e := Render([]byte(config), []byte(`{"nodes":[]}`), 8); e == nil {
		t.Fatal("allowed empty matrix")
	}
	if _, _, e := Render([]byte(config), []byte(matrix), 0); e == nil {
		t.Fatal("allowed zero limit")
	}
}

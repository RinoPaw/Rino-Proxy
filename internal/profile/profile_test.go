package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newProfile(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.yaml")
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPreserveVLESSOptions(t *testing.T) {
	p, err := Read(filepath.Join("..", "..", "testdata", "fubuki.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != "fubuki" || len(p.Nodes) != 2 {
		t.Fatalf("unexpected profile: %+v", p)
	}
	if p.Nodes[1]["type"] != "vless" {
		t.Fatal("lost protocol")
	}
	ws, ok := p.Nodes[1]["ws-opts"].(map[string]any)
	if !ok || ws["path"] != "/ws" {
		t.Fatalf("lost nested options: %v", p.Nodes[1])
	}
}

func TestDedupAndSameNameCoexist(t *testing.T) {
	a := Profile{Source: "star", Nodes: []Node{
		{"name": "JP", "type": "socks5", "server": "x.invalid", "port": 1080, "password": "A"},
		{"name": "JP", "type": "socks5", "server": "x.invalid", "port": 1080, "password": "B"},
	}}
	b := Profile{Source: "fubuki", Nodes: []Node{
		{"name": "Mirror", "type": "socks5", "port": 1080, "password": "A", "server": "x.invalid"},
		{"name": "JP", "type": "socks5", "server": "y.invalid", "port": 1080, "password": "C"},
	}}
	result, err := Merge([]Profile{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if result.InputCount != 4 || result.DuplicateCount != 1 || len(result.Nodes) != 3 {
		t.Fatalf("wrong dedup: %+v", result)
	}
	for i, want := range []string{"[star] JP", "[star] JP #2", "[fubuki] JP"} {
		if result.Nodes[i]["name"] != want {
			t.Fatalf("got %v, want %s", result.Nodes[i]["name"], want)
		}
	}
	if len(result.Provenance[0].Sources) != 2 || len(result.Provenance[0].Aliases) != 2 {
		t.Fatalf("lost provenance: %+v", result.Provenance)
	}
	if a.Nodes[0]["name"] != "JP" {
		t.Fatal("source mutated")
	}
}

func TestRejectMalformedAndProviderOnly(t *testing.T) {
	for _, body := range []string{
		"proxy-providers:\n  a:\n    type: http\n",
		"proxies:\n  - name: JP\n",
		"proxies:\n  - type: socks5\n",
		"proxies:\n  - null\n",
		"proxies: invalid\n",
	} {
		if _, err := Read(newProfile(t, body)); err == nil {
			t.Fatalf("accepted invalid profile: %s", body)
		}
	}
}

func TestRejectMultipleDocuments(t *testing.T) {
	input := newProfile(t, "proxies:\n  - name: x\n    type: socks5\n---\nproxies:\n  - name: y\n    type: socks5\n")
	if _, err := Read(input); err == nil || !strings.Contains(err.Error(), "multiple YAML") {
		t.Fatalf("expected multiple document error, got: %v", err)
	}
}

func TestRejectDuplicateProfileBasenames(t *testing.T) {
	profile := Profile{Source: "same", Nodes: []Node{{"name": "node", "type": "socks5"}}}
	if _, err := Merge([]Profile{profile, profile}); err == nil {
		t.Fatal("allowed source collision")
	}
}

package policy

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestChatGPT403IsAcceptableFallback(t *testing.T) {
	onlyChallenge := strings.Replace(matrix, "\"status\":\"PASS\",\"http_code\":200,\"latency_ms\":350", "\"status\":\"CHALLENGE\",\"http_code\":403,\"latency_ms\":350", 1)
	output, _, err := Render([]byte(config), []byte(onlyChallenge), 8)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(output, &doc); err != nil {
		t.Fatal(err)
	}
	for _, group := range doc["proxy-groups"].([]any) {
		g := group.(map[string]any)
		if g["name"] != "Rino-ChatGPT" {
			continue
		}
		if g["expected-status"] != "200/403" {
			t.Fatalf("incorrect health status: %v", g["expected-status"])
		}
		opts := g["proxies"].([]any)
		if len(opts) != 3 || opts[2] != "TX" {
			t.Fatalf("Cloudflare challenge candidates lost: %v", opts)
		}
		return
	}
	t.Fatal("ChatGPT fallback group is missing")
}

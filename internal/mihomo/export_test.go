package mihomo

import (
	"github.com/RinoPaw/Rino-Proxy/internal/profile"
	"gopkg.in/yaml.v3"
	"testing"
)

func TestExportSafeDefaults(t *testing.T) {
	data, err := Export([]profile.Node{{"name": "[star] JP", "type": "socks5", "server": "x.invalid", "port": 1080}})
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MixedPort int  `yaml:"mixed-port"`
		AllowLAN  bool `yaml:"allow-lan"`
		Groups    []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.MixedPort != 7890 || config.AllowLAN || len(config.Groups) != 1 ||
		config.Groups[0].Proxies[0] != "DIRECT" || config.Rules[0] != "MATCH,Rino" {
		t.Fatalf("unsafe output: %+v", config)
	}
}

func TestRejectEmpty(t *testing.T) {
	if _, err := Export(nil); err == nil {
		t.Fatal("expected error")
	}
}

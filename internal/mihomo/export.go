package mihomo

import (
	"errors"

	"github.com/RinoPaw/Rino-Proxy/internal/profile"
	"gopkg.in/yaml.v3"
)

type proxyGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
}

type config struct {
	MixedPort   int            `yaml:"mixed-port"`
	AllowLAN    bool           `yaml:"allow-lan"`
	Mode        string         `yaml:"mode"`
	LogLevel    string         `yaml:"log-level"`
	Proxies     []profile.Node `yaml:"proxies"`
	ProxyGroups []proxyGroup   `yaml:"proxy-groups"`
	Rules       []string       `yaml:"rules"`
}

// Export creates a standalone Mihomo config with DIRECT as the default.
// It never enables TUN, LAN exposure, or an unprotected external controller.
func Export(nodes []profile.Node) ([]byte, error) {
	if len(nodes) == 0 { return nil, errors.New("cannot export an empty node list") }
	choices := []string{"DIRECT"}
	for _, node := range nodes {
		name, ok := node["name"].(string)
		if !ok || name == "" { return nil, errors.New("proxy is missing a name") }
		choices = append(choices, name)
	}
	return yaml.Marshal(config{
		MixedPort: 7890, AllowLAN: false, Mode: "rule", LogLevel: "warning",
		Proxies: nodes,
		ProxyGroups: []proxyGroup{{Name: "Rino", Type: "select", Proxies: choices}},
		Rules: []string{"MATCH,Rino"},
	})
}

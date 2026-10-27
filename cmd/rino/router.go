package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// renderRouter generates a LAN-only proxy profile using the existing private
// Tailnet HTTP proxy as its upstream. It does not change the system route.
func renderRouter(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("router render", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	upstream := fs.String("upstream", "http://100.64.0.3:17890", "private HTTP upstream")
	bind := fs.String("bind", "172.27.207.1", "router LAN address")
	port := fs.Int("port", 7890, "LAN mixed proxy port")
	output := fs.String("output", "", "output config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *output == "" {
		return errors.New("usage: rino router render --output config.yaml [--upstream http://100.64.0.3:17890]")
	}
	u, err := url.Parse(*upstream)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("upstream must be an uncredentialed private HTTP proxy")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsPrivate() && !isTailnetIP(ip) {
		return errors.New("upstream must be a private/Tailscale IP")
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil || p < 1 || p > 65535 {
		return errors.New("invalid upstream port")
	}
	bindIP := net.ParseIP(*bind)
	if bindIP == nil || !bindIP.IsPrivate() || bindIP.To4() == nil {
		return errors.New("bind must be a private IPv4 LAN address")
	}
	if *port < 1024 || *port > 65535 {
		return errors.New("invalid LAN proxy port")
	}
	// Explicitly route sensitive domestic traffic direct. Cloud upstream is the
	// default only for international destinations covered by domain rules.
	rules := []string{
		"DOMAIN-SUFFIX,bilibili.com,DIRECT",
		"DOMAIN-SUFFIX,bilivideo.com,DIRECT",
		"DOMAIN-SUFFIX,hdslb.com,DIRECT",
		"DOMAIN-SUFFIX,github.com,Tencent",
		"DOMAIN-SUFFIX,githubusercontent.com,Tencent",
		"DOMAIN-SUFFIX,githubassets.com,Tencent",
		"DOMAIN-SUFFIX,google.com,Tencent",
		"DOMAIN-SUFFIX,googleapis.com,Tencent",
		"DOMAIN-SUFFIX,gstatic.com,Tencent",
		"DOMAIN-SUFFIX,youtube.com,Tencent",
		"DOMAIN-SUFFIX,googlevideo.com,Tencent",
		"DOMAIN-SUFFIX,ytimg.com,Tencent",
		"DOMAIN-SUFFIX,chatgpt.com,Tencent",
		"DOMAIN-SUFFIX,openai.com,Tencent",
		"MATCH,DIRECT",
	}
	config := struct {
		MixedPort          int      `yaml:"mixed-port"`
		AllowLAN           bool     `yaml:"allow-lan"`
		BindAddress        string   `yaml:"bind-address"`
		LanAllowed         []string `yaml:"lan-allowed-ips"`
		Mode               string   `yaml:"mode"`
		LogLevel           string   `yaml:"log-level"`
		IPv6               bool     `yaml:"ipv6"`
		ExternalController string   `yaml:"external-controller"`
		DNS                struct {
			Enable bool `yaml:"enable"`
		} `yaml:"dns"`
		Proxies []map[string]any `yaml:"proxies"`
		Groups  []map[string]any `yaml:"proxy-groups"`
		Rules   []string         `yaml:"rules"`
	}{
		MixedPort: *port, AllowLAN: true, BindAddress: *bind,
		LanAllowed: []string{"172.27.207.0/24"}, Mode: "rule", LogLevel: "warning",
		IPv6: false, ExternalController: "127.0.0.1:9091", Rules: rules,
		Proxies: []map[string]any{{"name": "Tencent-Tailnet", "type": "http", "server": ip.String(), "port": p}},
		Groups:  []map[string]any{{"name": "Tencent", "type": "select", "proxies": []string{"Tencent-Tailnet"}}},
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	if err := atomicWrite(*output, data); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Generated LAN-only Mihomo profile: %s (bind %s:%d, upstream %s, default DIRECT)\n", *output, *bind, *port, *upstream)
	return err
}

func isTailnetIP(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

func runRouter(args []string, output io.Writer) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) != "render" {
		return errors.New("usage: rino router render --output config.yaml")
	}
	return renderRouter(args[1:], output)
}

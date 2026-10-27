// Package core provides read-only access to a local Mihomo REST controller.
// It intentionally does not expose configuration mutation operations.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 8 << 20

type Client struct {
	baseURL string
	secret  string
	http    *http.Client
}

// NewClient only accepts loopback HTTP controllers. It never uses proxy
// environment variables, follows redirects, or sends the secret elsewhere.
func NewClient(address, secret string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 || timeout > 30*time.Second {
		return nil, errors.New("controller timeout must be between 1ns and 30s")
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return nil, errors.New("controller must be a loopback HTTP URL without credentials, path, or query")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("controller must use a loopback IP address")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("controller must include a valid TCP port")
	}
	transport := &http.Transport{
		Proxy:             nil, // Avoid HTTP_PROXY redirecting the controller connection.
		DialContext:       (&net.Dialer{Timeout: timeout}).DialContext,
		DisableKeepAlives: true,
	}
	return &Client{
		baseURL: strings.TrimSuffix(address, "/"),
		secret:  secret,
		http: &http.Client{
			Transport:     transport,
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Config holds an allowlisted summary; the full runtime configuration may
// include credentials and must never be serialized by this client.
type Config struct {
	Mode        string `json:"mode"`
	LogLevel    string `json:"log_level"`
	MixedPort   int    `json:"mixed_port"`
	BindAddress string `json:"bind_address"`
	AllowLAN    bool   `json:"allow_lan"`
	IPv6        bool   `json:"ipv6"`
	TUNEnabled  bool   `json:"tun_enabled"`
}

type Group struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Selected   string `json:"selected"`
	Candidates int    `json:"candidates"`
}

type Snapshot struct {
	Version      string  `json:"version"`
	Meta         bool    `json:"meta"`
	Config       Config  `json:"config"`
	ProxyEntries int     `json:"proxy_entries"`
	Groups       []Group `json:"groups"`
}

func (c *Client) get(ctx context.Context, path string, dst any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create controller request: %w", err)
	}
	if c.secret != "" {
		request.Header.Set("Authorization", "Bearer "+c.secret)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("controller GET %s: connection failed: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// Never include arbitrary response bodies in errors: they can leak secrets.
		return fmt.Errorf("controller GET %s: HTTP %d", path, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("controller GET %s: read failed: %w", path, err)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("controller GET %s: response too large", path)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("controller GET %s: invalid JSON", path)
	}
	return nil
}

// Snapshot returns three bounded, allowlisted views of the current runtime.
// It does not modify the Mihomo instance or return raw proxy configurations.
func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	var version struct {
		Version string `json:"version"`
		Meta    bool   `json:"meta"`
	}
	if err := c.get(ctx, "/version", &version); err != nil {
		return Snapshot{}, err
	}
	if version.Version == "" {
		return Snapshot{}, errors.New("controller returned an empty version")
	}
	var config struct {
		Mode        string `json:"mode"`
		LogLevel    string `json:"log-level"`
		MixedPort   int    `json:"mixed-port"`
		BindAddress string `json:"bind-address"`
		AllowLAN    bool   `json:"allow-lan"`
		IPv6        bool   `json:"ipv6"`
		TUN         struct {
			Enable bool `json:"enable"`
		} `json:"tun"`
	}
	if err := c.get(ctx, "/configs", &config); err != nil {
		return Snapshot{}, err
	}
	var proxies struct {
		Proxies map[string]struct {
			Type string   `json:"type"`
			Now  string   `json:"now"`
			All  []string `json:"all"`
		} `json:"proxies"`
	}
	if err := c.get(ctx, "/proxies", &proxies); err != nil {
		return Snapshot{}, err
	}
	if proxies.Proxies == nil {
		return Snapshot{}, errors.New("controller returned no proxy inventory")
	}
	result := Snapshot{
		Version: version.Version, Meta: version.Meta,
		Config: Config{
			Mode: config.Mode, LogLevel: config.LogLevel,
			MixedPort: config.MixedPort, BindAddress: config.BindAddress,
			AllowLAN: config.AllowLAN, IPv6: config.IPv6, TUNEnabled: config.TUN.Enable,
		},
		ProxyEntries: len(proxies.Proxies),
		Groups:       make([]Group, 0),
	}
	for name, item := range proxies.Proxies {
		if len(item.All) == 0 {
			continue
		}
		result.Groups = append(result.Groups, Group{
			Name: name, Type: item.Type, Selected: item.Now, Candidates: len(item.All),
		})
	}
	sort.Slice(result.Groups, func(i, j int) bool { return result.Groups[i].Name < result.Groups[j].Name })
	return result, nil
}

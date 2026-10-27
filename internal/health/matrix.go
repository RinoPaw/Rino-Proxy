package health

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type NodeResult struct {
	Node    string   `json:"node"`
	Results []Result `json:"results"`
	Passed  bool     `json:"passed"`
}
type Matrix struct {
	Nodes          []NodeResult    `json:"nodes"`
	Coverage       map[string]bool `json:"coverage"`
	CoveragePassed bool            `json:"coverage_passed"`
	SingleNodePass bool            `json:"single_node_pass"`
}
type Controller struct {
	base string
	http *http.Client
}
type selectorInfo struct {
	Name string   `json:"name"`
	Type string   `json:"type"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

func loopbackAddress(s string) error {
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Hostname() == "" || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("matrix requires plain loopback HTTP address")
	}
	if u.Hostname() != "localhost" {
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return errors.New("matrix control and proxy must be on loopback")
		}
	}
	return nil
}
func NewController(base string) (*Controller, error) {
	if e := loopbackAddress(base); e != nil {
		return nil, e
	}
	return &Controller{base: base, http: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}}, nil
}
func (c *Controller) call(ctx context.Context, method string, body []byte) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, method, c.base+"/proxies/RinoTest", bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}
func (c *Controller) group(ctx context.Context) (selectorInfo, error) {
	resp, e := c.call(ctx, http.MethodGet, nil)
	if e != nil {
		return selectorInfo{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return selectorInfo{}, fmt.Errorf("controller GET: HTTP %d", resp.StatusCode)
	}
	var group selectorInfo
	if e := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&group); e != nil {
		return group, e
	}
	if group.Type != "Selector" || group.Now == "" || len(group.All) == 0 {
		return group, errors.New("RinoTest must be a populated Selector group")
	}
	return group, nil
}
func (c *Controller) selectNode(ctx context.Context, name string) error {
	payload, e := json.Marshal(map[string]string{"name": name})
	if e != nil {
		return e
	}
	resp, e := c.call(ctx, http.MethodPut, payload)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return fmt.Errorf("controller PUT: HTTP %d", resp.StatusCode)
	}
	return nil
}
func EvaluateMatrix(rows []NodeResult) Matrix {
	m := Matrix{Nodes: rows, Coverage: map[string]bool{}}
	for _, row := range rows {
		if row.Passed {
			m.SingleNodePass = true
		}
		for _, site := range row.Results {
			if Accepted(site) {
				m.Coverage[site.Name] = true
			}
		}
	}
	m.CoveragePassed = m.Coverage["bilibili"] && m.Coverage["github"] && (m.Coverage["google"] || m.Coverage["youtube"] || m.Coverage["chatgpt"])
	return m
}
func RunMatrix(ctx context.Context, c *Controller, proxy, filter string, limit int, timeout time.Duration) (m Matrix, err error) {
	if err := loopbackAddress(proxy); err != nil {
		return Matrix{}, err
	}
	if limit < 0 {
		return Matrix{}, errors.New("limit must not be negative")
	}
	var re *regexp.Regexp
	if filter != "" {
		re, err = regexp.Compile(filter)
		if err != nil {
			return Matrix{}, err
		}
	}
	group, err := c.group(ctx)
	if err != nil {
		return Matrix{}, err
	}
	defer func() {
		restore, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := c.selectNode(restore, group.Now); e != nil {
			err = errors.Join(err, fmt.Errorf("failed to restore selector: %w", e))
		}
	}()
	var rows []NodeResult
	for _, name := range group.All {
		if name == "DIRECT" || name == "REJECT" || name == "GLOBAL" || name == "RinoTest" {
			continue
		}
		if re != nil && !re.MatchString(name) {
			continue
		}
		if limit > 0 && len(rows) >= limit {
			break
		}
		if ctx.Err() != nil {
			return EvaluateMatrix(rows), ctx.Err()
		}
		if e := c.selectNode(ctx, name); e != nil {
			return EvaluateMatrix(rows), fmt.Errorf("select node failed: %w", e)
		}
		client, e := NewClient(proxy, timeout)
		if e != nil {
			return EvaluateMatrix(rows), e
		}
		client.Transport.(*http.Transport).DisableKeepAlives = true
		results := ProbeAll(ctx, client, DefaultTargets)
		client.CloseIdleConnections()
		rows = append(rows, NodeResult{Node: name, Results: results, Passed: Satisfies(results)})
	}
	if len(rows) == 0 {
		return EvaluateMatrix(rows), errors.New("no nodes matched filter")
	}
	return EvaluateMatrix(rows), nil
}
func MatrixSummary(m Matrix) string {
	var b strings.Builder
	for _, row := range m.Nodes {
		fmt.Fprintf(&b, "%s: ", row.Node)
		for _, site := range row.Results {
			fmt.Fprintf(&b, "%s=%s ", site.Name, site.Status)
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "Coverage condition: %t; single-node condition: %t\n", m.CoveragePassed, m.SingleNodePass)
	return b.String()
}

package health

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	Pass      Status = "PASS"
	Challenge Status = "CHALLENGE"
	Limited   Status = "RATE_LIMIT"
	Fail      Status = "FAIL"
)

type Target struct {
	Name string
	URL  string
}

var DefaultTargets = []Target{
	{Name: "bilibili", URL: "https://api.bilibili.com/x/web-interface/nav"},
	{Name: "github", URL: "https://github.com/"},
	{Name: "google", URL: "https://www.google.com/generate_204"},
	{Name: "youtube", URL: "https://www.youtube.com/"},
	{Name: "chatgpt", URL: "https://chatgpt.com/"},
}

type Result struct {
	Name      string `json:"name"`
	Status    Status `json:"status"`
	HTTPCode  int    `json:"http_code"`
	LatencyMS int64  `json:"latency_ms"`
	Detail    string `json:"detail,omitempty"`
}

func NewClient(proxyAddress string, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 || timeout > 2*time.Minute {
		return nil, errors.New("timeout must be between 1 ns and 2 minutes")
	}
	u, err := url.Parse(proxyAddress)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.Port() == "" {
		return nil, errors.New("provide an explicit HTTP proxy URL (e.g. http://127.0.0.1:7890)")
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(u),
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          6,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("refusing insecure redirect")
			}
			return nil
		},
	}, nil
}

func Probe(ctx context.Context, client *http.Client, target Target) Result {
	result := Result{Name: target.Name, Status: Fail}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		result.Detail = "invalid target"
		return result
	}
	req.Header.Set("User-Agent", "Rino-Proxy/0.2")
	start := time.Now()
	response, err := client.Do(req)
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			result.Detail = "timeout"
		} else {
			result.Detail = "connection or TLS error"
		}
		return result
	}
	defer response.Body.Close()
	result.HTTPCode = response.StatusCode
	body, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	if err != nil {
		result.Detail = "incomplete response"
		return result
	}
	return Classify(result, body)
}

func Classify(result Result, body []byte) Result {
	code := result.HTTPCode
	if code == http.StatusTooManyRequests {
		result.Status, result.Detail = Limited, "HTTP 429"
		return result
	}
	if code == http.StatusForbidden || code == http.StatusUnauthorized {
		if result.Name == "chatgpt" {
			result.Status, result.Detail = Challenge, "access challenge or HTTP 403/401"
		} else {
			result.Detail = "access denied"
		}
		return result
	}
	if result.Name == "google" {
		if code == 204 {
			result.Status = Pass
		} else {
			result.Detail = "expected HTTP 204"
		}
		return result
	}
	if code != http.StatusOK {
		result.Detail = "unexpected HTTP status"
		return result
	}
	switch result.Name {
	case "bilibili":
		var payload struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			result.Detail = "invalid API JSON"
		} else if payload.Code == 0 || payload.Code == -101 {
			result.Status = Pass
			if payload.Code == -101 {
				result.Detail = "API reachable; unauthenticated"
			}
		} else if payload.Code == -352 || payload.Code == -412 {
			result.Status, result.Detail = Challenge, "Bilibili risk-control response"
		} else {
			result.Detail = "unexpected Bilibili API code"
		}
	case "github":
		if strings.Contains(strings.ToLower(string(body)), "github") {
			result.Status = Pass
		} else {
			result.Detail = "unexpected page content"
		}
	case "youtube":
		if strings.Contains(strings.ToLower(string(body)), "youtube") {
			result.Status = Pass
		} else {
			result.Detail = "unexpected page content"
		}
	case "chatgpt":
		lower := strings.ToLower(string(body))
		if strings.Contains(lower, "just a moment") || strings.Contains(lower, "cf-chl") ||
			strings.Contains(lower, "challenge-platform") {
			result.Status, result.Detail = Challenge, "Cloudflare challenge"
		} else if strings.Contains(lower, "chatgpt") {
			result.Status = Pass
			result.Detail = "HTTP reachable; login not verified"
		} else {
			result.Detail = "unexpected page content"
		}
	default:
		result.Detail = "unknown target"
	}
	return result
}

func ProbeAll(ctx context.Context, client *http.Client, targets []Target) []Result {
	results := make([]Result, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target Target) {
			defer wg.Done()
			results[i] = Probe(ctx, client, target)
		}(i, target)
	}
	wg.Wait()
	return results
}

// Satisfies checks: bilibili && github && (google || youtube || chatgpt).
func Satisfies(results []Result) bool {
	pass := make(map[string]bool, len(results))
	for _, r := range results {
		pass[r.Name] = pass[r.Name] || r.Status == Pass
	}
	return pass["bilibili"] && pass["github"] &&
		(pass["google"] || pass["youtube"] || pass["chatgpt"])
}

func isTimeout(err error) bool {
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

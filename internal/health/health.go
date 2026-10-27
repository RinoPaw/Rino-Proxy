package health

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
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
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			// A redirect is already a response from the intended HTTPS endpoint.
			return http.ErrUseLastResponse
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
	// HTTPS CONNECT and TLS verification succeeded and the target responded.
	// Avoid consuming page bodies: app content is outside transport-health scope.
	return Classify(result, nil)
}

// Classify intentionally ignores HTTP status semantics and page content.
// Transport or TLS failures have no HTTP response and stay FAIL in Probe.
func Classify(result Result, _ []byte) Result {
	if result.HTTPCode >= 100 && result.HTTPCode <= 599 {
		result.Status = Pass
		if result.HTTPCode >= 400 {
			result.Detail = "HTTP response received; application access unverified"
		}
	} else {
		result.Status = Fail
		result.Detail = "no valid HTTP response"
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

// Accepted means a valid HTTP response was received from the HTTPS target.
// HTTP status and application-level meaning do not affect connectivity.
func Accepted(r Result) bool {
	return r.HTTPCode >= 100 && r.HTTPCode <= 599
}

// Satisfies checks: bilibili && github && (google || youtube || chatgpt).
func Satisfies(results []Result) bool {
	pass := make(map[string]bool, len(results))
	for _, r := range results {
		pass[r.Name] = pass[r.Name] || Accepted(r)
	}
	return pass["bilibili"] && pass["github"] &&
		(pass["google"] || pass["youtube"] || pass["chatgpt"])
}

func isTimeout(err error) bool {
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

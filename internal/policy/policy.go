package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type result struct {
	Nodes []struct {
		Node    string `json:"node"`
		Results []struct {
			Name      string `json:"name"`
			Status    string `json:"status"`
			HTTPCode  int    `json:"http_code"`
			LatencyMS int64  `json:"latency_ms"`
		} `json:"results"`
	} `json:"nodes"`
}
type envelope struct {
	Results result `json:"results"`
	Nodes   result `json:"nodes"`
}
type site struct {
	name, url string
	domains   []string
}

var sites = []site{
	{"GitHub", "https://github.com/", []string{"github.com", "githubusercontent.com", "githubassets.com", "github.io", "githubcopilot.com"}},
	{"Google", "https://www.gstatic.com/generate_204", []string{"google.com", "googleapis.com", "gstatic.com", "googleusercontent.com"}},
	{"YouTube", "https://www.youtube.com/", []string{"youtube.com", "youtu.be", "ytimg.com", "googlevideo.com", "youtubei.googleapis.com"}},
	{"ChatGPT", "https://chatgpt.com/", []string{"chatgpt.com", "openai.com", "oaistatic.com", "oaiusercontent.com"}},
}

type candidate struct {
	name string
	ms   int64
	rank int
}
type Summary struct {
	Groups map[string]int `json:"groups"`
}

// Render preserves the original core credentials and provider-specific node configs.
// Only the known, private Tencent backup gateway config is accepted.
func Render(original, matrix []byte, maxPerSite int) ([]byte, Summary, error) {
	if maxPerSite < 1 || maxPerSite > 47 {
		return nil, Summary{}, errors.New("max-per-site must be between 1 and 47")
	}
	var cfg map[string]any
	if e := yaml.Unmarshal(original, &cfg); e != nil {
		return nil, Summary{}, e
	}
	if cfg["bind-address"] != "100.64.0.3" || cfg["mixed-port"] != 17890 || cfg["external-controller"] != "127.0.0.1:17891" || cfg["mode"] != "rule" {
		return nil, Summary{}, errors.New("refusing to modify unexpected production endpoint")
	}
	ps, ok := cfg["proxies"].([]any)
	if !ok || len(ps) == 0 {
		return nil, Summary{}, errors.New("no inline proxy nodes")
	}
	known := map[string]bool{}
	for _, p := range ps {
		v, ok := p.(map[string]any)
		if !ok {
			return nil, Summary{}, errors.New("invalid proxy")
		}
		name, ok := v["name"].(string)
		if !ok || name == "" || known[name] {
			return nil, Summary{}, errors.New("invalid or duplicate proxy name")
		}
		known[name] = true
	}
	gs, ok := cfg["proxy-groups"].([]any)
	if !ok {
		return nil, Summary{}, errors.New("no groups")
	}
	kept := make([]any, 0, len(gs)+len(sites))
	hasTX := false
	for _, g := range gs {
		v, ok := g.(map[string]any)
		if !ok {
			return nil, Summary{}, errors.New("invalid group")
		}
		name, _ := v["name"].(string)
		if name == "TX" {
			hasTX = true
		}
		if !strings.HasPrefix(name, "Rino-") {
			kept = append(kept, v)
		}
	}
	if !hasTX {
		return nil, Summary{}, errors.New("existing TX fallback group required")
	}
	var snap struct {
		Results result          `json:"results"`
		Nodes   json.RawMessage `json:"nodes"`
	}
	if e := json.Unmarshal(matrix, &snap); e != nil {
		return nil, Summary{}, e
	}
	rows := snap.Results.Nodes
	if len(rows) == 0 {
		var raw result
		if e := json.Unmarshal(matrix, &raw); e != nil {
			return nil, Summary{}, e
		}
		rows = raw.Nodes
	}
	if len(rows) == 0 {
		return nil, Summary{}, errors.New("no matrix rows")
	}
	summary := Summary{Groups: map[string]int{}}
	for _, site := range sites {
		best := map[string]candidate{}
		for _, row := range rows {
			if !known[row.Node] {
				continue
			}
			for _, v := range row.Results {
				if !strings.EqualFold(v.Name, site.name) {
					continue
				}
				rank := -1
				if v.Status == "PASS" && v.HTTPCode >= 200 && v.HTTPCode < 400 {
					rank = 0
				}
				if site.name == "ChatGPT" && v.Status == "CHALLENGE" && v.HTTPCode == 403 {
					rank = 1
				}
				if rank < 0 {
					continue
				}
				old, seen := best[row.Node]
				if !seen || rank < old.rank || (rank == old.rank && v.LatencyMS < old.ms) {
					best[row.Node] = candidate{name: row.Node, ms: v.LatencyMS, rank: rank}
				}
			}
		}
		if len(best) == 0 {
			return nil, Summary{}, fmt.Errorf("no passing candidates for %s", site.name)
		}
		ranks := make([]candidate, 0, len(best))
		for _, value := range best {
			ranks = append(ranks, value)
		}
		sort.Slice(ranks, func(i, j int) bool {
			if ranks[i].rank != ranks[j].rank {
				return ranks[i].rank < ranks[j].rank
			}
			if ranks[i].ms != ranks[j].ms {
				return ranks[i].ms < ranks[j].ms
			}
			return ranks[i].name < ranks[j].name
		})
		if len(ranks) > maxPerSite {
			ranks = ranks[:maxPerSite]
		}
		names := make([]string, 0, len(ranks)+1)
		for _, v := range ranks {
			names = append(names, v.name)
		}
		names = append(names, "TX")
		group := "Rino-" + site.name
		summary.Groups[group] = len(ranks)
		groupConfig := map[string]any{"name": group, "type": "fallback", "proxies": names, "url": site.url, "interval": 180, "lazy": false, "timeout": 7000}
		if site.name == "ChatGPT" {
			groupConfig["expected-status"] = "200/403"
		}
		kept = append(kept, groupConfig)
	}
	cfg["proxy-groups"] = kept
	rules := []string{"DOMAIN-SUFFIX,bilibili.com,DIRECT", "DOMAIN-SUFFIX,bilivideo.com,DIRECT", "DOMAIN-SUFFIX,hdslb.com,DIRECT"}
	for _, id := range []string{"GitHub", "YouTube", "Google", "ChatGPT"} {
		for _, s := range sites {
			if s.name == id {
				for _, domain := range s.domains {
					rules = append(rules, "DOMAIN-SUFFIX,"+domain+",Rino-"+id)
				}
			}
		}
	}
	cfg["rules"] = append(rules, "MATCH,TX")
	output, e := yaml.Marshal(cfg)
	if e != nil {
		return nil, Summary{}, e
	}
	return output, summary, nil
}

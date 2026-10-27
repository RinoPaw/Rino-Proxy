package profile

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxProfileSize = 16 << 20

type Node = map[string]any

type Profile struct {
	Source string
	Path   string
	Nodes  []Node
}

type Provenance struct {
	Name    string   `json:"name"`
	Sources []string `json:"sources"`
	Aliases []string `json:"aliases"`
}

type Result struct {
	Nodes          []Node
	Provenance     []Provenance
	InputCount     int
	DuplicateCount int
}

// Read loads only inline proxies, preserving protocol-specific fields.
// v0.1 deliberately rejects provider-only and remote subscription inputs.
func Read(path string) (Profile, error) {
	f, err := os.Open(path)
	if err != nil { return Profile{}, fmt.Errorf("open profile %q: %w", path, err) }
	defer f.Close()
	stat, err := f.Stat()
	if err != nil { return Profile{}, err }
	if !stat.Mode().IsRegular() || stat.Size() > maxProfileSize {
		return Profile{}, fmt.Errorf("profile %q must be a regular file of at most 16 MiB", path)
	}
	var data struct { Proxies []Node `yaml:"proxies"` }
	decoder := yaml.NewDecoder(f)
	if err := decoder.Decode(&data); err != nil {
		return Profile{}, fmt.Errorf("parse profile %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return Profile{}, fmt.Errorf("profile %q contains multiple YAML documents", path)
	} else if !errors.Is(err, io.EOF) {
		return Profile{}, fmt.Errorf("parse trailing data in %q: %w", path, err)
	}
	if len(data.Proxies) == 0 {
		return Profile{}, fmt.Errorf("profile %q has no inline proxies; provider-only inputs are unsupported in v0.1", path)
	}
	for i, node := range data.Proxies {
		for _, key := range []string{"name", "type"} {
			value, ok := node[key].(string)
			if !ok || strings.TrimSpace(value) == "" {
				return Profile{}, fmt.Errorf("profile %q: node %d needs a nonempty %s", path, i+1, key)
			}
		}
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if name == "" || name == "." { return Profile{}, fmt.Errorf("invalid source name for %q", path) }
	return Profile{Source: name, Path: path, Nodes: data.Proxies}, nil
}

func Merge(profiles []Profile) (Result, error) {
	if len(profiles) == 0 { return Result{}, errors.New("at least one profile is required") }
	res := Result{Nodes: make([]Node, 0), Provenance: make([]Provenance, 0)}
	seenSources := make(map[string]bool)
	seenConfigs := make(map[[32]byte]int)
	seenNames := make(map[string]bool)
	for _, src := range profiles {
		if src.Source == "" || seenSources[src.Source] {
			return Result{}, fmt.Errorf("ambiguous profile source %q; use unique basenames", src.Source)
		}
		seenSources[src.Source] = true
		for _, node := range src.Nodes {
			res.InputCount++
			label, ok := node["name"].(string)
			if !ok || strings.TrimSpace(label) == "" { return Result{}, fmt.Errorf("source %q contains an unnamed proxy", src.Source) }
			// Canonical JSON keys make the fingerprint independent of YAML field order.
			encoded, err := json.Marshal(node)
			if err != nil { return Result{}, fmt.Errorf("invalid fields in source %q: %w", src.Source, err) }
			var duplicate Node
			if err := json.Unmarshal(encoded, &duplicate); err != nil { return Result{}, err }
			delete(duplicate, "name")
			canonical, err := json.Marshal(duplicate)
			if err != nil { return Result{}, err }
			key := sha256.Sum256(canonical)
			if index, exists := seenConfigs[key]; exists {
				res.DuplicateCount++
				meta := &res.Provenance[index]
				if !contains(meta.Sources, src.Source) { meta.Sources = append(meta.Sources, src.Source) }
				if !contains(meta.Aliases, label) { meta.Aliases = append(meta.Aliases, label) }
				continue
			}
			// Restore the original YAML scalar types when copying protocol options.
			var cloned Node
			y, err := yaml.Marshal(node)
			if err != nil { return Result{}, err }
			if err := yaml.Unmarshal(y, &cloned); err != nil { return Result{}, err }
			name := "[" + src.Source + "] " + label
			if seenNames[name] {
				for n := 2; ; n++ {
					suffixed := fmt.Sprintf("%s #%d", name, n)
					if !seenNames[suffixed] { name = suffixed; break }
				}
			}
			seenNames[name] = true
			cloned["name"] = name
			seenConfigs[key] = len(res.Nodes)
			res.Nodes = append(res.Nodes, cloned)
			res.Provenance = append(res.Provenance, Provenance{Name: name, Sources: []string{src.Source}, Aliases: []string{label}})
		}
	}
	if len(res.Nodes) == 0 { return Result{}, errors.New("no nodes to export") }
	return res, nil
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack { if item == needle { return true } }
	return false
}

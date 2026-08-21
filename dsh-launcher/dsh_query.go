package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	registryOfficial = "https://registry.npmjs.org/@deepseek-ai%2Fdsh"
	registryMirror   = "https://registry.npmmirror.com/@deepseek-ai/dsh"
	packageName      = "@deepseek-ai/dsh"
)

// DSHVersion is one published release with its publish time.
type DSHVersion struct {
	Version   string `json:"version"`
	Published string `json:"published"`
	IsLatest  bool   `json:"isLatest"`
}

// RegistryInfo is the full version view returned to the frontend.
type RegistryInfo struct {
	Package  string       `json:"package"`
	Latest   string       `json:"latest"`
	Next     string       `json:"next"`
	Source   string       `json:"source"`
	Versions []DSHVersion `json:"versions"`
}

type registryDoc struct {
	Name     string            `json:"name"`
	DistTags map[string]string `json:"dist-tags"`
	Time     map[string]string `json:"time"`
}

func fetchRegistry(ctx context.Context, url, source string) (*registryDoc, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "dsh-launcher/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry %s responded %s", source, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var doc registryDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse registry %s response: %w", source, err)
	}
	return &doc, nil
}

// QueryRegistry fetches latest/next dist-tags and the full version list.
// Official registry is tried first, npmmirror as a fallback.
func (a *App) QueryRegistry() (*RegistryInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sources := []struct {
		url    string
		source string
	}{
		{registryOfficial, "registry.npmjs.org"},
		{registryMirror, "registry.npmmirror.com"},
	}

	var lastErr error
	for _, s := range sources {
		doc, err := fetchRegistry(ctx, s.url, s.source)
		if err != nil {
			lastErr = err
			continue
		}
		info := &RegistryInfo{
			Package: doc.Name,
			Latest:  doc.DistTags["latest"],
			Next:    doc.DistTags["next"],
			Source:  s.source,
		}
		// versions come from the "time" map (publication order), filtered to
		// actual package versions (excludes "created"/"modified" keys).
		for v, published := range doc.Time {
			if v == "created" || v == "modified" {
				continue
			}
			if _, err := parseVersion(v); err != nil {
				continue // not a semver key
			}
			info.Versions = append(info.Versions, DSHVersion{
				Version:   v,
				Published: published,
			})
		}
		sort.Slice(info.Versions, func(i, j int) bool {
			ti, _ := time.Parse(time.RFC3339, info.Versions[i].Published)
			tj, _ := time.Parse(time.RFC3339, info.Versions[j].Published)
			return ti.After(tj)
		})
		latest := info.Latest
		for i := range info.Versions {
			info.Versions[i].IsLatest = info.Versions[i].Version == latest
		}
		return info, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no registry source available")
	}
	return nil, fmt.Errorf("查询 npm 最新版本失败: %w", lastErr)
}

// QueryLatestVersion is a convenience wrapper returning just the latest tag.
func (a *App) QueryLatestVersion() (string, error) {
	info, err := a.QueryRegistry()
	if err != nil {
		return "", err
	}
	return info.Latest, nil
}

// detectLocalVersion reads the version of the DSH copy npx would prefer inside
// a directory (dir/node_modules/@deepseek-ai/dsh/package.json). Returns ""
// when no local copy exists.
func detectLocalVersion(dir string) string {
	pkg := filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh", "package.json")
	data, err := os.ReadFile(pkg)
	if err != nil {
		return ""
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return doc.Version
}

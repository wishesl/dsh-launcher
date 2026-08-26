package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The community-curated plugin catalog, ported from dsh-market's
// registry.ts: fetched fresh on every request, revalidated with
// ETag/Last-Modified (304 → reuse), never served from a stale snapshot
// (a stale catalog is a wrong catalog — a plugin published this morning
// would read as "does not exist").
const (
	defaultMarketRegistryURL = "https://awesome-dsh-plugin.com/plugins.json"
	marketFetchTimeout       = 15 * time.Second
	marketFetchAttempts      = 2
)

// MarketPlugin mirrors one curated registry entry
// (awesome-dsh-plugin.com/plugins.json).
type MarketPlugin struct {
	Name        string            `json:"name"`
	Owner       string            `json:"owner"`
	URL         string            `json:"url"`
	Category    string            `json:"category"`
	Description map[string]string `json:"description"` // zh / en
	NPM         *string           `json:"npm"`         // preferred install source when set
	Stars       *int64            `json:"stars"`
	Downloads   *int64            `json:"downloads"` // npm 30-day downloads
	Install     string            `json:"install"`
	Added       string            `json:"added"`
	Deprecated  bool              `json:"deprecated"`
	Replacement string            `json:"replacement"`
}

// MarketCatalog is the full registry payload handed to the frontend.
type MarketCatalog struct {
	Updated    string                    `json:"updated"`
	Count      int                       `json:"count"`
	Categories map[string]map[string]any `json:"categories"` // id -> {zh,en}
	Plugins    []MarketPlugin            `json:"plugins"`
}

// MarketSettings is the market-related launcher configuration (read-only
// profile + configurable registry mirror).
type MarketSettings struct {
	RegistryURL string `json:"registryUrl"`
	Profile     string `json:"profile"`
}

// marketProfileName is the DSH profile the launcher manages plugins for.
// It matches the `web` hardcoded alias the instance launch command uses.
const marketProfileName = "web"

// marketProfileDir resolves <DSH_HOME>/profiles/<profile>, honoring DSH_HOME
// the same way the dsh CLI and dsh-market do.
func marketProfileDir() string {
	home := strings.TrimSpace(os.Getenv("DSH_HOME"))
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil || h == "" {
			h = "."
		}
		home = h
	}
	return filepath.Join(home, "profiles", marketProfileName)
}

// --- catalog cache (in-memory, revalidated on every fetch) ---

type cachedCatalog struct {
	etag, modified string
	data           *MarketCatalog
}

var (
	marketCacheMu sync.Mutex
	marketCache   *cachedCatalog
)

// marketRegistryURL returns the configured mirror, falling back to the
// official curated catalog.
func (a *App) marketRegistryURL() string {
	if a != nil && a.settings != nil {
		if u := strings.TrimSpace(a.settings.get().MarketRegistryURL); u != "" {
			return u
		}
	}
	return defaultMarketRegistryURL
}

// FetchMarketCatalog returns the plugin catalog. When force is true the
// conditional-request validators are dropped so the origin always answers
// fresh (used by the manual refresh button).
func (a *App) FetchMarketCatalog(force bool) (*MarketCatalog, error) {
	url := a.marketRegistryURL()
	var lastErr error
	for attempt := 0; attempt < marketFetchAttempts; attempt++ {
		catalog, err := fetchMarketCatalogOnce(url, force)
		if err == nil {
			return catalog, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("获取插件目录失败: %w", lastErr)
}

func fetchMarketCatalogOnce(url string, force bool) (*MarketCatalog, error) {
	marketCacheMu.Lock()
	cached := marketCache
	marketCacheMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), marketFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "dsh-launcher/1.0")
	if cached != nil && !force {
		if cached.etag != "" {
			req.Header.Set("If-None-Match", cached.etag)
		} else if cached.modified != "" {
			req.Header.Set("If-Modified-Since", cached.modified)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		if cached != nil && cached.data != nil {
			return cached.data, nil
		}
		return nil, fmt.Errorf("目录返回 304 但无缓存可复用")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("目录源响应 %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	var catalog MarketCatalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("解析目录失败: %w", err)
	}
	if len(catalog.Plugins) == 0 {
		return nil, fmt.Errorf("目录为空")
	}

	marketCacheMu.Lock()
	marketCache = &cachedCatalog{
		etag:     resp.Header.Get("Etag"),
		modified: resp.Header.Get("Last-Modified"),
		data:     &catalog,
	}
	marketCacheMu.Unlock()
	return &catalog, nil
}

// findRegistryEntry returns the catalog entry whose url matches (case
// insensitive), or nil.
func findRegistryEntry(catalog *MarketCatalog, entryURL string) *MarketPlugin {
	if catalog == nil {
		return nil
	}
	want := strings.ToLower(strings.TrimSpace(entryURL))
	for i := range catalog.Plugins {
		if strings.ToLower(catalog.Plugins[i].URL) == want {
			return &catalog.Plugins[i]
		}
	}
	return nil
}

// GetMarketSettings exposes the current market configuration (mirror URL +
// fixed profile) to the frontend settings panel.
func (a *App) GetMarketSettings() MarketSettings {
	return MarketSettings{
		RegistryURL: a.marketRegistryURL(),
		Profile:     marketProfileName,
	}
}

// SetMarketRegistryURL updates the registry mirror. Empty restores the
// official curated catalog. The value is validated to be http(s).
func (a *App) SetMarketRegistryURL(url string) error {
	url = strings.TrimSpace(url)
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("镜像地址必须以 http:// 或 https:// 开头")
	}
	if a.settings != nil {
		a.settings.setMarketRegistryURL(url)
		// Drop the cache so the next fetch uses the new source unconditionally.
		marketCacheMu.Lock()
		marketCache = nil
		marketCacheMu.Unlock()
	}
	return nil
}

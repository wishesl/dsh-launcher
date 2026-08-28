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
	// The curated catalog is 300KB+ and grows daily. Some networks download
	// it at ~10KB/s (TTFB is fast, the BODY is slow), so 15s used to kill
	// every first open with "context deadline exceeded" — and the catalog
	// only gets bigger. 90s lets a slow-but-working link finish. Every later
	// open revalidates with ETag/304 (empty body → fast).
	marketFetchTimeout  = 90 * time.Second
	marketFetchAttempts = 2
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
// the same way the dsh CLI and dsh-market do. When DSH_HOME is unset the
// default is <user home>/.dsh — the leading ".dsh" is part of the fallback
// (a missing dot here silently points every read/toggle at a non-existent
// profile directory while the dsh CLI keeps using the real one).
func marketProfileDir() string {
	home := strings.TrimSpace(os.Getenv("DSH_HOME"))
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil || h == "" {
			h = "."
		}
		home = filepath.Join(h, ".dsh")
	}
	return filepath.Join(home, "profiles", marketProfileName)
}

// --- catalog cache (in-memory + disk, revalidated on every fetch) ---

type cachedCatalog struct {
	Etag     string         `json:"etag"`
	Modified string         `json:"modified"`
	Data     *MarketCatalog `json:"data"`
}

var (
	marketCacheMu   sync.Mutex
	marketCache     *cachedCatalog // in-memory cache
	marketCachePath string         // resolved once
)

// marketCacheFile is %APPDATA%\DSHLauncher\market-catalog.json. On a slow
// network the catalog download takes a minute; persisting it (with the
// validators) means a restart revalidates with a 304 instead of re-downloading
// a megabyte at ~10KB/s. The disk copy is NEVER served without the server
// confirming it is current — freshness is still verified on every call.
func marketCacheFile() string {
	if marketCachePath != "" {
		return marketCachePath
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	marketCachePath = filepath.Join(dir, "DSHLauncher", "market-catalog.json")
	return marketCachePath
}

// loadDiskCacheLocked seeds the in-memory cache from disk when nothing is
// loaded yet (caller holds marketCacheMu).
func loadDiskCacheLocked() {
	if marketCache != nil {
		return
	}
	data, err := os.ReadFile(marketCacheFile())
	if err != nil {
		return
	}
	var c cachedCatalog
	if err := json.Unmarshal(data, &c); err != nil || c.Data == nil || len(c.Data.Plugins) == 0 {
		return
	}
	marketCache = &c
}

// persistDiskCache writes the current cache (caller holds marketCacheMu).
func persistDiskCache() {
	if marketCache == nil {
		return
	}
	data, err := json.Marshal(marketCache)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(marketCacheFile()), 0o755)
	_ = os.WriteFile(marketCacheFile(), data, 0o644)
}

// cachedCatalogData returns the catalog already held in memory (nil when it
// was never fetched this session). Read-only and non-blocking — for optional
// cross-checks that must never trigger a network fetch.
func cachedCatalogData() *MarketCatalog {
	marketCacheMu.Lock()
	defer marketCacheMu.Unlock()
	if marketCache != nil && marketCache.Data != nil {
		return marketCache.Data
	}
	return nil
}

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
// fresh (used by the manual refresh button). Downloads route through the
// configured proxy (if any), which also fixes slow/timeout catalog fetches on
// restricted networks.
func (a *App) FetchMarketCatalog(force bool) (*MarketCatalog, error) {
	url := a.marketRegistryURL()
	client := a.proxyHTTPClient(marketFetchTimeout)
	var lastErr error
	for attempt := 0; attempt < marketFetchAttempts; attempt++ {
		catalog, err := fetchMarketCatalogOnce(client, url, force)
		if err == nil {
			return catalog, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("获取插件目录失败: %w", lastErr)
}

func fetchMarketCatalogOnce(client *http.Client, url string, force bool) (*MarketCatalog, error) {
	marketCacheMu.Lock()
	if marketCache == nil {
		loadDiskCacheLocked()
	}
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
		if cached.Etag != "" {
			req.Header.Set("If-None-Match", cached.Etag)
		} else if cached.Modified != "" {
			req.Header.Set("If-Modified-Since", cached.Modified)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		if cached != nil && cached.Data != nil {
			return cached.Data, nil
		}
		return nil, fmt.Errorf("目录返回 304 但无缓存可复用")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("目录源响应 %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
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
		Etag:     resp.Header.Get("Etag"),
		Modified: resp.Header.Get("Last-Modified"),
		Data:     &catalog,
	}
	persistDiskCache()
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

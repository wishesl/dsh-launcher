package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Locally-favorited plugins — a self-contained snapshot (display metadata +
// a server-validated install target) so the 收藏 tab renders and installs
// fully offline, independent of the plugin-market catalog. Persisted to
// %APPDATA%\DSHLauncher\favorites.json (same directory as settings.json), so
// it survives DSH_HOME / profile changes and launcher restarts.

// FavoritePlugin is one favorited plugin as stored on disk.
type FavoritePlugin struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Owner       string            `json:"owner"`
	URL         string            `json:"url"`
	NPM         *string           `json:"npm"`
	Install     string            `json:"install"`
	Source      string            `json:"source"` // catalog | installed
	Category    string            `json:"category"`
	Description map[string]string `json:"description"`
	Stars       *int64            `json:"stars"`
	Downloads   *int64            `json:"downloads"`
	AddedAt     string            `json:"addedAt"`
}

// FavoriteDraft is the frontend payload for AddFavorite. id and install are
// derived server-side from trusted data — never trusted from the client.
type FavoriteDraft struct {
	Name        string            `json:"name"`
	Owner       string            `json:"owner"`
	URL         string            `json:"url"`
	NPM         *string           `json:"npm"`
	Category    string            `json:"category"`
	Description map[string]string `json:"description"`
	Stars       *int64            `json:"stars"`
	Downloads   *int64            `json:"downloads"`
	Source      string            `json:"source"` // catalog | installed
	Spec        string            `json:"spec"`   // installed-source pnpm spec
}

// ShareImportResult reports what a share-code parse/import would add vs skip.
type ShareImportResult struct {
	Imported []FavoritePlugin `json:"imported"`
	Skipped  []string         `json:"skipped"`
}

// favoritesFilePath is the persisted favorites store. A var so tests can point
// it at a temp directory (same pattern as patchFilePath).
var favoritesFilePath = func() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "DSHLauncher", "favorites.json")
}

var favMu sync.Mutex

// favoriteID returns the stable identity key of a favorite: the npm name when
// the package is published on npm, otherwise the lowercased owner/repo, then
// the validated install target when it is an npm name, then the name. This is
// what dedupes across catalog refreshes and source switches.
func favoriteID(p FavoritePlugin) string {
	if p.NPM != nil && strings.TrimSpace(*p.NPM) != "" {
		return strings.TrimSpace(*p.NPM)
	}
	if repo := repoOf(p.URL); repo != "" {
		return repo
	}
	if i := strings.TrimSpace(p.Install); npmNameRe.MatchString(i) {
		return i
	}
	return strings.ToLower(strings.TrimSpace(p.Name))
}

var (
	// githubSpecRe matches a `github:owner/repo[#path:/sub]` install target.
	githubSpecRe = regexp.MustCompile(`^github:([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:#path:/(.+))?$`)
	// gitURLSpecRe matches the git+https form of a github source
	// (e.g. an allowBuilds key).
	gitURLSpecRe = regexp.MustCompile(`^git\+https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git$`)
)

// validateInstallSpec reports whether an install target is a shell-safe, well
// formed npm name or github source. Applied to installed-source favorites and
// share-code imports (catalog-source targets are derived by installTargetFor,
// which is stricter). targetRe is checked first so no shell metacharacter can
// ever reach the command line.
func validateInstallSpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" || !targetRe.MatchString(spec) {
		return false
	}
	if npmNameRe.MatchString(spec) {
		return true
	}
	if gitURLSpecRe.MatchString(spec) {
		return true
	}
	if m := githubSpecRe.FindStringSubmatch(spec); m != nil {
		if m[2] != "" {
			return validSubpath(m[2])
		}
		return true
	}
	return false
}

// githubURLFromSpec maps a `github:owner/repo[#path:/sub]` install spec back
// to its canonical GitHub URL ("" when the spec is not a github source).
func githubURLFromSpec(spec string) string {
	m := githubSpecRe.FindStringSubmatch(strings.TrimSpace(spec))
	if m == nil {
		return ""
	}
	return "https://github.com/" + m[1]
}

// isShareableFavorite reports whether a favorite carries a GitHub address.
// Share codes must stay re-findable/verifiable by their repo URL, so favorites
// without one are excluded from sharing.
func isShareableFavorite(f FavoritePlugin) bool {
	return repoOf(f.URL) != ""
}

func readFavorites() ([]FavoritePlugin, error) {
	data, err := os.ReadFile(favoritesFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return []FavoritePlugin{}, nil
		}
		return nil, err
	}
	var doc struct {
		Version   int              `json:"version"`
		Favorites []FavoritePlugin `json:"favorites"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("解析收藏文件失败: %w", err)
	}
	if doc.Favorites == nil {
		doc.Favorites = []FavoritePlugin{}
	}
	return doc.Favorites, nil
}

// emptyFavorites guards every Wails return: a nil slice serializes as JSON
// `null`, and the frontend calls .map() on the result — a null crash. All
// favorite-list returns must be non-nil arrays.
func emptyFavorites() []FavoritePlugin { return []FavoritePlugin{} }

func writeFavorites(list []FavoritePlugin) error {
	data, err := json.MarshalIndent(struct {
		Version   int              `json:"version"`
		Favorites []FavoritePlugin `json:"favorites"`
	}{Version: 1, Favorites: list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(favoritesFilePath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(favoritesFilePath(), data, 0o644)
}

// sortFavorites orders by addedAt descending (newest first), tie-broken by ID
// for determinism — addedAt is RFC3339Nano so same-second collisions are
// practically impossible, but a stable order is still better than none.
func sortFavorites(list []FavoritePlugin) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].AddedAt != list[j].AddedAt {
			return list[i].AddedAt > list[j].AddedAt
		}
		return list[i].ID < list[j].ID
	})
}

// ListFavorites returns the locally-favorited plugins (pure local read — no
// network, no catalog dependency).
func (a *App) ListFavorites() ([]FavoritePlugin, error) {
	favMu.Lock()
	defer favMu.Unlock()
	list, err := readFavorites()
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = emptyFavorites()
	}
	sortFavorites(list)
	return list, nil
}

// AddFavorite adds (or re-favorites, refreshing the snapshot but keeping the
// original addedAt) a plugin. The install target and identity are derived
// server-side — the client only supplies display metadata.
func (a *App) AddFavorite(d FavoriteDraft) ([]FavoritePlugin, error) {
	src := strings.TrimSpace(d.Source)
	if src != "catalog" && src != "installed" {
		return nil, fmt.Errorf("非法收藏来源: %s", d.Source)
	}
	f := FavoritePlugin{
		Name:        strings.TrimSpace(d.Name),
		Owner:       strings.TrimSpace(d.Owner),
		URL:         strings.TrimSpace(d.URL),
		NPM:         d.NPM,
		Category:    strings.TrimSpace(d.Category),
		Description: d.Description,
		Stars:       d.Stars,
		Downloads:   d.Downloads,
		Source:      src,
		AddedAt:     time.Now().Format(time.RFC3339Nano),
	}
	if f.Name == "" {
		return nil, fmt.Errorf("缺少插件名")
	}
	switch src {
	case "catalog":
		target, ok := installTargetFor(f.URL, f.NPM)
		if !ok || !targetRe.MatchString(target) {
			return nil, fmt.Errorf("该目录条目不可作为安装来源")
		}
		f.Install = target
	default: // installed
		spec := strings.TrimSpace(d.Spec)
		if !validateInstallSpec(spec) {
			return nil, fmt.Errorf("非法安装来源: %s", spec)
		}
		f.Install = spec
		// Record the GitHub address even when the client sent none (e.g. a
		// `github:` spec whose package.json has no homepage): favorites
		// without a GitHub URL cannot be shared.
		if repoOf(f.URL) == "" {
			if u := githubURLFromSpec(spec); u != "" {
				f.URL = u
			}
		}
	}
	f.ID = favoriteID(f)
	if f.ID == "" {
		return nil, fmt.Errorf("无法确定插件身份")
	}

	favMu.Lock()
	defer favMu.Unlock()
	list, err := readFavorites()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == f.ID {
			kept := list[i].AddedAt
			f.AddedAt = kept
			list[i] = f
			if err := writeFavorites(list); err != nil {
				return nil, err
			}
			sortFavorites(list)
			return list, nil
		}
	}
	list = append(list, f)
	if err := writeFavorites(list); err != nil {
		return nil, err
	}
	sortFavorites(list)
	return list, nil
}

// RemoveFavorite deletes a favorite by its identity key.
func (a *App) RemoveFavorite(id string) ([]FavoritePlugin, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("缺少插件身份")
	}
	favMu.Lock()
	defer favMu.Unlock()
	list, err := readFavorites()
	if err != nil {
		return nil, err
	}
	kept := list[:0]
	for _, f := range list {
		if f.ID != id {
			kept = append(kept, f)
		}
	}
	if err := writeFavorites(kept); err != nil {
		return nil, err
	}
	sortFavorites(kept)
	return kept, nil
}

// --- share code: DSH-FAV:v1:<base64url(JSON)> ---
//
// Carries only public plugin metadata (no profile paths, credentials or
// instance info) so it can be pasted into chat / files. Import re-validates
// every install target before anything is merged.

const shareCodePrefix = "DSH-FAV:v1:"

type sharePayload struct {
	App       string            `json:"app"`
	V         int               `json:"v"`
	CreatedAt string            `json:"createdAt"`
	Plugins   []FavoritePlugin  `json:"plugins"`
}

func parseSharePayload(code string) (*sharePayload, error) {
	code = strings.TrimSpace(code)
	if !strings.HasPrefix(code, shareCodePrefix) {
		return nil, fmt.Errorf("分享码格式不正确（需以 %s 开头）", shareCodePrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, shareCodePrefix))
	if err != nil {
		return nil, fmt.Errorf("分享码解码失败: %w", err)
	}
	var payload sharePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("分享码内容解析失败: %w", err)
	}
	if payload.V != 1 {
		return nil, fmt.Errorf("不支持的分享码版本: %d", payload.V)
	}
	return &payload, nil
}

// computeShareImport diffs a payload against the current favorites without
// writing anything; imported are the entries a real import would add.
func computeShareImport(payload *sharePayload, current []FavoritePlugin) *ShareImportResult {
	have := map[string]bool{}
	for _, f := range current {
		have[f.ID] = true
	}
	// Non-nil slices: nil serializes as JSON `null`, and the frontend reads
	// .length on both — a null here crashes the import panel.
	res := &ShareImportResult{Imported: []FavoritePlugin{}, Skipped: []string{}}
	for _, p := range payload.Plugins {
		if !validateInstallSpec(p.Install) {
			continue // skip entries whose target failed re-validation
		}
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			continue
		}
		p.ID = favoriteID(p)
		if p.ID == "" {
			continue
		}
		if have[p.ID] {
			res.Skipped = append(res.Skipped, p.ID)
			continue
		}
		have[p.ID] = true
		res.Imported = append(res.Imported, p)
	}
	return res
}

// pickFavorites returns the favorites whose id is in ids (empty ids = all).
func pickFavorites(list []FavoritePlugin, ids []string) []FavoritePlugin {
	if len(ids) == 0 {
		return list
	}
	want := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	out := []FavoritePlugin{}
	for _, f := range list {
		if want[f.ID] {
			out = append(out, f)
		}
	}
	return out
}

// GenerateShareCode encodes the SELECTED favorites (by identity id) into a
// shareable DSH-FAV code — the user may share only part of their collection.
// An empty ids slice includes every favorite. Favorites WITHOUT a GitHub URL
// are never included (a share code must stay re-findable by repo); if nothing
// shareable remains this is an error.
func (a *App) GenerateShareCode(ids []string) (string, error) {
	favMu.Lock()
	list, err := readFavorites()
	favMu.Unlock()
	if err != nil {
		return "", err
	}
	sel := []FavoritePlugin{}
	for _, f := range pickFavorites(list, ids) {
		if isShareableFavorite(f) {
			sel = append(sel, f)
		}
	}
	if len(sel) == 0 {
		return "", fmt.Errorf("所选收藏均无 GitHub 地址，无法分享")
	}
	payload := sharePayload{
		App:       "dsh-launcher",
		V:         1,
		CreatedAt: time.Now().Format(time.RFC3339Nano),
		Plugins:   sel,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return shareCodePrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

// ParseShareCode decodes + validates a share code and reports which favorites
// it would add vs skip — WITHOUT writing anything (preview before import).
func (a *App) ParseShareCode(code string) (*ShareImportResult, error) {
	payload, err := parseSharePayload(code)
	if err != nil {
		return nil, err
	}
	favMu.Lock()
	list, err := readFavorites()
	favMu.Unlock()
	if err != nil {
		return nil, err
	}
	return computeShareImport(payload, list), nil
}

// ImportShareCode parses a share code and merges the SELECTED parsed
// favorites (by identity id) into the store. An empty ids slice imports every
// valid new entry. Skipped reports entries already present (never imported).
func (a *App) ImportShareCode(code string, ids []string) (*ShareImportResult, error) {
	payload, err := parseSharePayload(code)
	if err != nil {
		return nil, err
	}
	sel := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			sel[id] = true
		}
	}
	favMu.Lock()
	defer favMu.Unlock()
	list, err := readFavorites()
	if err != nil {
		return nil, err
	}
	res := computeShareImport(payload, list)
	// Filter to the user's selection (empty selection = import all).
	picked := []FavoritePlugin{}
	for _, p := range res.Imported {
		if len(sel) == 0 || sel[p.ID] {
			picked = append(picked, p)
		}
	}
	res.Imported = picked
	now := time.Now().Format(time.RFC3339Nano)
	for i := range res.Imported {
		if res.Imported[i].AddedAt == "" {
			res.Imported[i].AddedAt = now
		}
		list = append(list, res.Imported[i])
	}
	if len(res.Imported) > 0 {
		if err := writeFavorites(list); err != nil {
			return nil, err
		}
	}
	return res, nil
}

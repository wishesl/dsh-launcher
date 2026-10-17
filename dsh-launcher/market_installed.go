package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Installed / disable-toggle of plugins, ported from dsh-market's profile.ts
// + patch.ts. Reading the installed set is a pure filesystem read of the
// profile manifest; enabling/disabling writes the official patch layer
// (cordis.patch.yml) so DSH's HMR re-composes without a restart and the
// loader re-applies the choice on every boot.

// inboxBundles are the profile-template bundles the launcher never lists nor
// lets the user touch (community plugins are allowed to publish under the
// official scope, so a whole-scope filter would hide them — name filtering
// only, same as dsh-market).
var inboxBundles = map[string]bool{
	"@deepseek-ai/dsh-base":     true,
	"@deepseek-ai/dsh-web-app":  true,
	"@deepseek-ai/dsh-headless": true,
}

func isInboxBundle(name string) bool { return inboxBundles[name] }

// InstalledPlugin is one community plugin present in the profile manifest.
type InstalledPlugin struct {
	Name        string `json:"name"`
	Spec        string `json:"spec"`
	Version     string `json:"version"`
	Kind        string `json:"kind"`  // npm | github | linked | other
	State       string `json:"state"` // enabled | disabled
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
	// Github is the plugin's GitHub URL when one is derivable from the install
	// spec (`github:owner/repo`) or the package homepage. Empty = no remote
	// repo (e.g. a locally-linked dev plugin), used to tell local from remote.
	Github string `json:"github"`
}

// readInstalledPlugins returns profile manifest dependencies (in-box bundles
// filtered out) as name -> spec. Missing/unreadable profile yields an empty
// map, never an error — the profile may simply not exist yet.
func readInstalledPlugins() (map[string]string, error) {
	dir := marketProfileDir()
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return map[string]string{}, nil
	}
	var doc struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("读取 profile package.json 失败: %w", err)
	}
	out := map[string]string{}
	for name, spec := range doc.Dependencies {
		if !isInboxBundle(name) {
			out[name] = spec
		}
	}
	return out, nil
}

// pluginKind classifies an install spec for display.
func pluginKind(spec string) string {
	switch {
	case strings.HasPrefix(spec, "github:"):
		return "github"
	case strings.HasPrefix(spec, "link:"), strings.HasPrefix(spec, "file:"):
		return "linked"
	default:
		return "npm"
	}
}

// readInstalledVersion reads the installed package version from the profile's
// node_modules ("" when absent).
func readInstalledVersion(name string) string {
	data, err := os.ReadFile(filepath.Join(marketProfileDir(), "node_modules", name, "package.json"))
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

// readInstalledMeta reads description + homepage from the installed package
// manifest, so the installed list still shows something recognizable even
// when the market catalog is unreachable.
func readInstalledMeta(name string) (description, homepage string) {
	data, err := os.ReadFile(filepath.Join(marketProfileDir(), "node_modules", name, "package.json"))
	if err != nil {
		return "", ""
	}
	var doc struct {
		Description string `json:"description"`
		Homepage    string `json:"homepage"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", ""
	}
	return doc.Description, doc.Homepage
}

// ListInstalledPlugins returns the installed community plugins with their
// version, source kind, and enable/disable state. Sorted by name.
// installedGithub derives the GitHub URL of an installed plugin from its
// install spec (`github:owner/repo`) or, failing that, a GitHub homepage.
// Empty result = a local plugin with no remote repo.
func installedGithub(spec, homepage string) string {
	if u := githubURLFromSpec(spec); u != "" {
		return u
	}
	if repoOf(homepage) != "" {
		return homepage
	}
	return ""
}

func (a *App) ListInstalledPlugins() ([]InstalledPlugin, error) {
	installed, err := readInstalledPlugins()
	if err != nil {
		return nil, err
	}
	out := make([]InstalledPlugin, 0, len(installed))
	for name, spec := range installed {
		state := "enabled"
		if packageDisabled(name) {
			state = "disabled"
		}
		description, homepage := readInstalledMeta(name)
		out = append(out, InstalledPlugin{
			Name:        name,
			Spec:        spec,
			Version:     readInstalledVersion(name),
			Kind:        pluginKind(spec),
			State:       state,
			Description: description,
			Homepage:    homepage,
			Github:      installedGithub(spec, homepage),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// packageEntryIDs returns the loader entry ids a package declares via its
// bundle patch (dsh.bundle.patch -> `insert:` block). These ids are what DSH
// identifies entries by — a disable row must target the entry id (e.g.
// `modlens`), NOT the npm package name (`@liustack/modlens`). Port of
// dsh-market's bundlePatchInsertedIds.
func packageEntryIDs(name string) []string {
	profile := marketProfileDir()
	manifestPath := filepath.Join(profile, "node_modules", name, "package.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil
	}
	var doc struct {
		DSH *struct {
			Bundle *struct {
				Patch string `json:"patch"`
			} `json:"bundle"`
		} `json:"dsh"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.DSH == nil || doc.DSH.Bundle == nil || doc.DSH.Bundle.Patch == "" {
		return nil
	}
	patchData, err := os.ReadFile(filepath.Join(profile, "node_modules", name, doc.DSH.Bundle.Patch))
	if err != nil {
		return nil
	}
	return parseInsertedIDs(string(patchData))
}

// parseInsertedIDs collects the `- id:` rows nested under `insert:` blocks of
// a bundle patch — the ids the package itself brings into the tree.
func parseInsertedIDs(text string) []string {
	var out []string
	insertIndent := -1
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if trimmed == "- insert:" || trimmed == "insert:" {
			insertIndent = indent
			continue
		}
		if insertIndent >= 0 && indent <= insertIndent {
			insertIndent = -1
			continue
		}
		if insertIndent >= 0 {
			if m := anyIDRe.FindStringSubmatch(line); m != nil {
				found := false
				for _, id := range out {
					if id == m[1] {
						found = true
						break
					}
				}
				if !found {
					out = append(out, m[1])
				}
			}
		}
	}
	return out
}

// dshMarketDisabledList reads dsh-market's own persisted disable list
// (.dsh-market/state.json). The real market replays this list at every boot
// (and writes it when the user toggles in its settings page), so the
// launcher must agree with it for the state to stick across restarts.
func dshMarketDisabledList() map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(marketProfileDir(), ".dsh-market", "state.json"))
	if err != nil {
		return out
	}
	var doc struct {
		Disabled []string `json:"disabled"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return out
	}
	for _, n := range doc.Disabled {
		out[n] = true
	}
	return out
}

// setDshMarketDisabled adds/removes a package name in dsh-market's persisted
// disable list, preserving every other field of state.json.
func setDshMarketDisabled(name string, disabled bool) error {
	file := filepath.Join(marketProfileDir(), ".dsh-market", "state.json")
	raw := map[string]any{}
	if data, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(data, &raw)
	}
	var list []string
	if v, ok := raw["disabled"].([]any); ok {
		for _, x := range v {
			if s, ok := x.(string); ok {
				list = append(list, s)
			}
		}
	}
	if disabled {
		exists := false
		for _, n := range list {
			if n == name {
				exists = true
				break
			}
		}
		if !exists {
			list = append(list, name)
		}
	} else {
		kept := list[:0]
		for _, n := range list {
			if n != name {
				kept = append(kept, n)
			}
		}
		list = kept
	}
	raw["disabled"] = list
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(file), 0o755)
	return os.WriteFile(file, data, 0o644)
}

// packageDisabled reports whether a package is off: any of its loader entry
// ids carries `disabled: true` in the profile patch layer, the package name
// itself is a patch row, or dsh-market's persisted list says so.
func packageDisabled(name string) bool {
	patch := readPatchDisabled()
	if patch[name] {
		return true // row keyed by package name (older/own writes)
	}
	for _, id := range packageEntryIDs(name) {
		if patch[id] {
			return true
		}
	}
	return dshMarketDisabledList()[name]
}

// clearPluginDisabled removes every disable trace for a package (entry-id
// rows, package-name rows, and dsh-market's persisted list). Used on
// uninstall so a reinstall starts enabled.
func clearPluginDisabled(name string) {
	for _, id := range packageEntryIDs(name) {
		_ = applyPatchState(id, false)
	}
	_ = applyPatchState(name, false)
	_ = setDshMarketDisabled(name, false)
}

// --- cordis.patch.yml line editing (port of dsh-market patch.ts) ---

var (
	topLevelIDRe  = regexp.MustCompile(`^-\s+id:\s*['"]?([^'"\s]+)['"]?\s*$`)
	anyIDRe       = regexp.MustCompile(`^\s*-\s+id:\s*['"]?([^'"\s]+)['"]?\s*$`)
	disabledKeyRe = regexp.MustCompile(`^(\s*)disabled:\s*(true|false)\s*$`)
	patchAnyKeyRe = regexp.MustCompile(`^\s*[A-Za-z][A-Za-z0-9_-]*\s*:`)
)

// patchFilePath returns the profile's user patch layer. A var so tests can
// point it at a temp directory.
var patchFilePath = func() string { return filepath.Join(marketProfileDir(), "cordis.patch.yml") }

// readPatchDisabled scans the patch layer for `- id: X` + `disabled: true`
// rows and returns the set of disabled ids.
func readPatchDisabled() map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(patchFilePath())
	if err != nil {
		return out
	}
	var cur string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := topLevelIDRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			continue
		}
		if cur == "" {
			continue
		}
		if m := disabledKeyRe.FindStringSubmatch(line); m != nil {
			if m[2] == "true" {
				out[cur] = true
			} else {
				delete(out, cur)
			}
		}
	}
	return out
}

// applyPatchState toggles a plugin in the patch layer.
//
//	wantDisabled=true  → ensure a `- id: X` + `disabled: true` row exists.
//	wantDisabled=false → remove the row we own (id + disabled only); if the
//	                     row carries user config it is kept with disabled:false.
func applyPatchState(name string, wantDisabled bool) error {
	if name == "" || strings.ContainsAny(name, " \t\r\n") {
		return fmt.Errorf("非法插件名")
	}
	file := patchFilePath()
	data, err := os.ReadFile(file)
	if err != nil {
		data = []byte("# Your patch layer for this dsh profile — applied after every bundle layer.\n[]\n")
	}
	text := string(data)
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	idLineRe := regexp.MustCompile(`^-\s+id:\s*['"]?` + regexp.QuoteMeta(name) + `['"]?\s*$`)

	// Locate the top-level row whose id equals name.
	rowStart := -1
	for i, ln := range lines {
		if idLineRe.MatchString(ln) {
			rowStart = i
			break
		}
	}

	if rowStart == -1 {
		if !wantDisabled {
			return nil // already enabled
		}
		// Append a new row at the end (before the trailing blank).
		trimmed := strings.TrimRight(text, " \t\r\n")
		sep := ""
		if trimmed != "" {
			sep = eol + eol
		}
		next := trimmed + sep + "- id: " + name + eol + "  disabled: true" + eol
		return os.WriteFile(file, []byte(next), 0o644)
	}

	// Find the end of the row's block (next top-level row).
	rowEnd := len(lines)
	for i := rowStart + 1; i < len(lines); i++ {
		ln := lines[i]
		if topLevelIDRe.MatchString(ln) {
			rowEnd = i
			break
		}
		if strings.HasPrefix(strings.TrimSpace(ln), "- ") && !strings.HasPrefix(ln, "  ") {
			rowEnd = i
			break
		}
	}
	block := lines[rowStart:rowEnd]

	// What keys does the block hold besides id?
	otherKey := false
	disabledIdx := -1
	for i, ln := range block {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if m := disabledKeyRe.FindStringSubmatch(ln); m != nil {
			disabledIdx = i
			continue
		}
		if m := patchAnyKeyRe.FindStringSubmatch(ln); m != nil {
			otherKey = true
		}
	}

	if !wantDisabled {
		// Remove our block entirely when it is only id (+disabled); otherwise
		// flip disabled to false and keep user config.
		if disabledIdx >= 0 && !otherKey {
			lines = append(lines[:rowStart], lines[rowEnd:]...)
		} else if disabledIdx >= 0 {
			lines[rowStart+disabledIdx] = "  disabled: false"
		}
		return os.WriteFile(file, []byte(strings.Join(lines, "\n")+eol), 0o644)
	}

	// want disabled
	if disabledIdx >= 0 {
		lines[rowStart+disabledIdx] = "  disabled: true"
	} else {
		// insert a disabled line right after the id line
		indent := "  "
		inserted := make([]string, 0, len(lines)+1)
		inserted = append(inserted, lines[:rowStart+1]...)
		inserted = append(inserted, indent+"disabled: true")
		inserted = append(inserted, lines[rowStart+1:]...)
		lines = inserted
	}
	return os.WriteFile(file, []byte(strings.Join(lines, "\n")+eol), 0o644)
}

// TogglePlugin enables/disables an installed plugin via the official patch
// layer (no uninstall; DSH HMR re-composes in ~1s, loader re-applies on boot).
//
// Disable rows must target the package's loader ENTRY ids (e.g. `modlens`),
// not the npm package name — the same ids dsh-market writes, otherwise DSH
// ignores the row. dsh-market's own persisted list (.dsh-market/state.json)
// is synced too, so the choice survives that market's boot replay.
func (a *App) TogglePlugin(name string, enabled bool) error {
	if isInboxBundle(name) {
		return fmt.Errorf("官方基础插件不可开关")
	}
	if _, ok, _ := installedLookup(name); !ok {
		return fmt.Errorf("插件未安装: %s", name)
	}
	ids := packageEntryIDs(name)
	if len(ids) == 0 {
		ids = []string{name}
	}
	for _, id := range ids {
		if err := applyPatchState(id, !enabled); err != nil {
			return err
		}
	}
	return setDshMarketDisabled(name, !enabled)
}

func installedLookup(name string) (string, bool, error) {
	installed, err := readInstalledPlugins()
	if err != nil {
		return "", false, err
	}
	spec, ok := installed[name]
	return spec, ok, nil
}

// --- pnpm-workspace.yaml allowBuilds merge (port of profile.ts setAllowBuilds) ---

var allowBuildsBlockRe = regexp.MustCompile(`(?m)^allowBuilds:[ \t]*\r?\n((?:[ \t]+[^\r\n]*\r?\n?)*)`)

var gitAllowKeyRe = regexp.MustCompile(`^[A-Za-z0-9@/_.-]+@git\+https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git$`)

// exactVersionRe matches a fully-qualified semver (kept as-is in allowBuilds).
var exactVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)

// normalizeAllowKey strips a NON-exact version suffix from an allowBuilds key.
// pnpm >= 11 rejects `name@range` keys (e.g. `node-pty@1`) with
// ERR_PNPM_INVALID_VERSION_UNION ("Use exact versions only"), which breaks
// EVERY pnpm operation in the profile (install/uninstall/update). Bare names
// (`node-pty`) and exact versions (`node-pty@1.0.0`) are both accepted by
// pnpm. Scoped names (`@scope/pkg`) are safe because only a NON-leading `@`
// can separate a version range.
func normalizeAllowKey(key string) string {
	if i := strings.LastIndex(key, "@"); i > 0 {
		if !exactVersionRe.MatchString(key[i+1:]) {
			return key[:i]
		}
	}
	return key
}

// sanitizeAllowBuilds rewrites pnpm-workspace.yaml so every allowBuilds key is
// a bare package name (or exact version), undoing `name@range` keys that pnpm
// 11 rejects. Best-effort repair: preserves every other entry, the file's line
// endings, and folds duplicate blocks. Returns true when the file changed.
func sanitizeAllowBuilds(dir string) bool {
	file := filepath.Join(dir, "pnpm-workspace.yaml")
	text := ""
	if data, err := os.ReadFile(file); err == nil {
		text = string(data)
	}
	if len(allowBuildsBlockRe.FindAllString(text, -1)) == 0 {
		return false
	}
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}

	entries := map[string]string{}
	changed := false
	for _, m := range allowBuildsBlockRe.FindAllStringSubmatch(text, -1) {
		for _, raw := range strings.Split(m[1], "\n") {
			line := strings.TrimRight(raw, "\r")
			kv := regexp.MustCompile(`^[ \t]+(\S.*?)\s*:\s*(true|false)?\s*$`).FindStringSubmatch(line)
			if kv == nil || kv[1] == "" {
				continue
			}
			key := kv[1]
			if len(key) >= 2 && (key[0] == '\'' && key[len(key)-1] == '\'' || key[0] == '"' && key[len(key)-1] == '"') {
				key = key[1 : len(key)-1]
			}
			norm := normalizeAllowKey(key)
			if norm != key {
				changed = true
			}
			entries[norm] = kv[2]
		}
	}
	if !changed || len(entries) == 0 {
		return false
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var block strings.Builder
	block.WriteString("allowBuilds:" + eol)
	for _, k := range keys {
		block.WriteString("  " + quoteYamlKey(k) + ": " + entries[k] + eol)
	}
	next := allowBuildsBlockRe.ReplaceAllStringFunc(text, func(string) string { return block.String() })
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	if err := os.WriteFile(file, []byte(next), 0o644); err != nil {
		return false
	}
	return true
}

// quoteYamlKey wraps a YAML block-mapping key when a plain scalar would be
// invalid: scoped npm names start with `@` (a reserved indicator) and keys
// ending in `:` need quoting.
func quoteYamlKey(key string) string {
	if strings.HasPrefix(key, "@") || strings.Contains(key, ": ") || strings.HasSuffix(key, ":") {
		return "'" + strings.ReplaceAll(key, "'", "''") + "'"
	}
	return key
}

// mergeAllowBuilds merges the given packages into the profile's
// pnpm-workspace.yaml `allowBuilds:` block, preserving every existing entry,
// the file's own line endings, and quoting reserved keys. Fixes the previous
// whole-file overwrite in install.go which dropped user entries.
func mergeAllowBuilds(dir string, packages []string) ([]string, error) {
	file := filepath.Join(dir, "pnpm-workspace.yaml")
	text := ""
	if data, err := os.ReadFile(file); err == nil {
		text = string(data)
	}
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}

	// Parse every allowBuilds block into a merged map (fold duplicates that a
	// previous bug may have left behind).
	entries := map[string]string{}
	for _, m := range allowBuildsBlockRe.FindAllStringSubmatch(text, -1) {
		for _, raw := range strings.Split(m[1], "\n") {
			line := strings.TrimRight(raw, "\r")
			kv := regexp.MustCompile(`^[ \t]+(\S.*?)\s*:\s*(true|false)?\s*$`).FindStringSubmatch(line)
			if kv == nil || kv[1] == "" {
				continue
			}
			key := kv[1]
			if len(key) >= 2 && (key[0] == '\'' && key[len(key)-1] == '\'' || key[0] == '"' && key[len(key)-1] == '"') {
				key = key[1 : len(key)-1]
			}
			// Normalize `name@range` keys (pnpm 11 rejects them with
			// ERR_PNPM_INVALID_VERSION_UNION) while merging.
			key = normalizeAllowKey(key)
			entries[key] = kv[2]
		}
	}

	plainRe := regexp.MustCompile(`^[A-Za-z0-9@/_.-]+$`)
	for _, pkg := range packages {
		if plainRe.MatchString(pkg) || gitAllowKeyRe.MatchString(pkg) {
			if entries[pkg] == "" {
				entries[pkg] = "true"
			}
		}
	}
	if len(entries) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var block strings.Builder
	block.WriteString("allowBuilds:" + eol)
	for _, k := range keys {
		block.WriteString("  " + quoteYamlKey(k) + ": " + entries[k] + eol)
	}

	next := text
	if len(allowBuildsBlockRe.FindAllString(text, -1)) == 0 {
		next = strings.TrimRight(text, " \t\r\n") + eol + eol + block.String()
	} else {
		// Replace the first block with the merged one; drop duplicate blocks.
		seen := 0
		next = allowBuildsBlockRe.ReplaceAllStringFunc(text, func(string) string {
			seen++
			if seen == 1 {
				return block.String()
			}
			return ""
		})
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(file, []byte(next), 0o644); err != nil {
		return nil, err
	}
	return keys, nil
}

// --- pnpm-workspace.yaml minimumReleaseAgeExclude dedupe ---

var releaseAgeExcludeBlockRe = regexp.MustCompile(`(?m)^minimumReleaseAgeExclude:[ \t]*\r?\n((?:[ \t]+[^\r\n]*\r?\n?)*)`)

var releaseAgeExcludeItemRe = regexp.MustCompile(`^[ \t]+-[ \t]*(.+?)[ \t]*$`)

// normalizeReleaseAgeExcludes 把 minimumReleaseAgeExclude 里「同一个包的多条记录」
// 收敛成一条（保留版本最高的），返回删掉的条数；没有重复时不改文件。
//
// 为什么必须做：pnpm 11 默认 minimumReleaseAge=24h（拒用 24 小时内发布的版本），并在
// 用户主动升级时把新版本**追加**进这份排除清单。但 pnpm 解析清单时，同一个包只看
// **第一条** —— 于是升级过一次之后，旧版本排在前面、新版本被忽略，接下来任何 pnpm
// 安装都会在「核对 lockfile 的供应链策略」这一步整体失败：
//
//	[ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION] 3 lockfile entries failed verification
//
// 实测（2026-10-18，pnpm 11.5.0）：rc.1 在前 / rc.2 在后 → 失败；调换顺序或只留 rc.2
// → 通过。清单只增不减，所以这个坑会随每次升级越来越容易出现，而且报错完全看不出跟
// 「清单里有一条没用上的旧记录」有关。
//
// 只处理「同包多条、且每条都是单一版本」的情况；含 `||` 的多版本条目、解析不出的条目
// 一律原样保留（不猜）。
func normalizeReleaseAgeExcludes(dir string) (int, error) {
	file := filepath.Join(dir, "pnpm-workspace.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	text := string(data)
	loc := releaseAgeExcludeBlockRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return 0, nil
	}
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}

	// 逐条解析：line 是原样保留用的整行；name/version 只在"单一版本"时才有效。
	type entry struct {
		line    string
		name    string
		version string
		single  bool
	}
	entries := make([]entry, 0, 16)
	for _, raw := range strings.Split(text[loc[2]:loc[3]], "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		e := entry{line: line}
		if m := releaseAgeExcludeItemRe.FindStringSubmatch(line); m != nil {
			spec := strings.Trim(strings.TrimSpace(m[1]), `'"`)
			// 只有 `name@x.y.z` 这种单一版本才参与收敛；`name@a || b` 保持原样。
			if i := strings.LastIndex(spec, "@"); i > 0 && !strings.Contains(spec[i+1:], "|") {
				if _, verr := parseVersion(spec[i+1:]); verr == nil {
					e.name, e.version, e.single = spec[:i], spec[i+1:], true
				}
			}
		}
		entries = append(entries, e)
	}

	// 组内选版本最高的一条。
	drop := make(map[int]bool, len(entries))
	byName := make(map[string][]int, len(entries))
	for i, e := range entries {
		if e.single {
			byName[e.name] = append(byName[e.name], i)
		}
	}
	for _, idxs := range byName {
		if len(idxs) < 2 {
			continue
		}
		keep := idxs[0]
		for _, i := range idxs[1:] {
			if compareSemver(entries[i].version, entries[keep].version) > 0 {
				keep = i
			}
		}
		for _, i := range idxs {
			if i != keep {
				drop[i] = true
			}
		}
	}
	if len(drop) == 0 {
		return 0, nil
	}

	var block strings.Builder
	block.WriteString("minimumReleaseAgeExclude:")
	block.WriteString(eol)
	for i, e := range entries {
		if drop[i] {
			continue
		}
		block.WriteString(e.line)
		block.WriteString(eol)
	}
	next := text[:loc[0]] + block.String() + text[loc[1]:]
	if err := os.WriteFile(file, []byte(next), 0o644); err != nil {
		return 0, err
	}
	return len(drop), nil
}

// releaseAgeViolationMarker 是 pnpm 拒绝装「太新的包」时的稳定片段。
const releaseAgeViolationMarker = "ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION"

// isReleaseAgeViolation reports whether pnpm refused the run because of its
// supply-chain release-age policy (see normalizeReleaseAgeExcludes).
func isReleaseAgeViolation(output string) bool {
	return strings.Contains(output, releaseAgeViolationMarker)
}

// releaseAgeViolationEntryRe 匹配 pnpm 报错里逐条列出的「包@版本」。
var releaseAgeViolationEntryRe = regexp.MustCompile(`(?m)^\s*(\S+@\S+)\s+was published at`)

// releaseAgeViolationSpecs 从 pnpm 的输出里取出被判拒的 `name@version`（去重）。
func releaseAgeViolationSpecs(output string) []string {
	matches := releaseAgeViolationEntryRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		spec := strings.TrimSpace(m[1])
		if spec == "" || seen[spec] {
			continue
		}
		seen[spec] = true
		out = append(out, spec)
	}
	return out
}

// ensureReleaseAgeExcludes 保证每个 `name@version` 都在 minimumReleaseAgeExclude 里，
// 且是**该包的第一条**（pnpm 对同一个包只看第一条）。返回新增/替换的条数。
//
// 只在 pnpm 明确拒绝（ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION）且用户主动安装/更新时
// 调用 —— 等价于 pnpm 自己在 `pnpm add` 时把新版本自动加进豁免清单的行为。
func ensureReleaseAgeExcludes(dir string, specs []string) (int, error) {
	if len(specs) == 0 {
		return 0, nil
	}
	file := filepath.Join(dir, "pnpm-workspace.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	text := string(data)
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}

	lines := make([]string, 0, 16)
	loc := releaseAgeExcludeBlockRe.FindStringSubmatchIndex(text)
	if loc != nil {
		for _, raw := range strings.Split(text[loc[2]:loc[3]], "\n") {
			if line := strings.TrimRight(raw, "\r"); strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}

	nameOf := func(line string) (string, string, bool) {
		m := releaseAgeExcludeItemRe.FindStringSubmatch(line)
		if m == nil {
			return "", "", false
		}
		spec := strings.Trim(strings.TrimSpace(m[1]), `'"`)
		i := strings.LastIndex(spec, "@")
		if i <= 0 {
			return "", "", false
		}
		return spec[:i], spec[i+1:], true
	}

	changed := 0
	for _, wanted := range specs {
		i := strings.LastIndex(wanted, "@")
		if i <= 0 {
			continue
		}
		wantName, wantVer := wanted[:i], wanted[i+1:]

		// 该包已有一条覆盖到目标版本（或更新）的条目 → 什么都不用做。
		satisfied := false
		for _, line := range lines {
			if name, ver, ok := nameOf(line); ok && name == wantName && compareSemver(ver, wantVer) >= 0 {
				satisfied = true
				break
			}
		}
		if satisfied {
			continue
		}

		// 否则丢掉同名的旧记录（pnpm 只看第一条），再追加目标版本。
		kept := make([]string, 0, len(lines)+1)
		for _, line := range lines {
			if name, _, ok := nameOf(line); ok && name == wantName {
				continue
			}
			kept = append(kept, line)
		}
		quote := ""
		if strings.HasPrefix(wantName, "@") {
			quote = "'"
		}
		kept = append(kept, fmt.Sprintf("  - %s%s%s", quote, wanted, quote))
		lines = kept
		changed++
	}
	if changed == 0 {
		return 0, nil
	}

	var block strings.Builder
	block.WriteString("minimumReleaseAgeExclude:")
	block.WriteString(eol)
	for _, line := range lines {
		block.WriteString(line)
		block.WriteString(eol)
	}
	var next string
	if loc == nil {
		next = strings.TrimRight(text, " \t\r\n") + eol + eol + block.String()
	} else {
		next = text[:loc[0]] + block.String() + text[loc[1]:]
	}
	if err := os.WriteFile(file, []byte(next), 0o644); err != nil {
		return 0, err
	}
	return changed, nil
}

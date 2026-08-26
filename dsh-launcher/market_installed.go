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
	Name    string `json:"name"`
	Spec    string `json:"spec"`
	Version string `json:"version"`
	Kind    string `json:"kind"`  // npm | github | linked | other
	State   string `json:"state"` // enabled | disabled
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

// ListInstalledPlugins returns the installed community plugins with their
// version, source kind, and enable/disable state. Sorted by name.
func (a *App) ListInstalledPlugins() ([]InstalledPlugin, error) {
	installed, err := readInstalledPlugins()
	if err != nil {
		return nil, err
	}
	disabled := readPatchDisabled()
	out := make([]InstalledPlugin, 0, len(installed))
	for name, spec := range installed {
		state := "enabled"
		if disabled[name] {
			state = "disabled"
		}
		out = append(out, InstalledPlugin{
			Name:    name,
			Spec:    spec,
			Version: readInstalledVersion(name),
			Kind:    pluginKind(spec),
			State:   state,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// --- cordis.patch.yml line editing (port of dsh-market patch.ts) ---

var (
	topLevelIDRe   = regexp.MustCompile(`^-\s+id:\s*['"]?([^'"\s]+)['"]?\s*$`)
	disabledKeyRe  = regexp.MustCompile(`^(\s*)disabled:\s*(true|false)\s*$`)
	patchAnyKeyRe  = regexp.MustCompile(`^\s*[A-Za-z][A-Za-z0-9_-]*\s*:`)
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
func (a *App) TogglePlugin(name string, enabled bool) error {
	if isInboxBundle(name) {
		return fmt.Errorf("官方基础插件不可开关")
	}
	if _, ok, _ := installedLookup(name); !ok {
		return fmt.Errorf("插件未安装: %s", name)
	}
	return applyPatchState(name, !enabled)
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

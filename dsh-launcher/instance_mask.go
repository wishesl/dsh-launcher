package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Per-instance plugin masking ("屏蔽插件"): which installed plugins a launcher
// instance must NOT load.
//
// Installed plugins live in ONE shared profile (`web`) used by every instance.
// To let different instances run different plugin sets WITHOUT permanently
// changing the shared plugin state, the launcher generates a TEMPORARY
// `--patch` overlay at launch: masked plugins get `disabled: true` rows that
// apply to that run only. Nothing is ever written to the profile's
// cordis.patch.yml or dsh-market state, so the global enable/disable switch
// keeps its exact value and the mask disappears when the instance stops.
// Plugins that have been uninstalled are neither listed nor masked.

// instanceMasksFilePath is the persisted store (%APPDATA%\DSHLauncher).
// A var so tests can point it at a temp directory.
var instanceMasksFilePath = func() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "DSHLauncher", "instance-masks.json")
}

// instanceMaskStore persists instanceID -> masked plugin names.
type instanceMaskStore struct {
	mu   sync.Mutex
	path string
	data map[string][]string
}

func newInstanceMaskStore() *instanceMaskStore {
	s := &instanceMaskStore{path: instanceMasksFilePath(), data: map[string][]string{}}
	s.load()
	return s
}

func (s *instanceMaskStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var d map[string][]string
	if err := json.Unmarshal(data, &d); err != nil || d == nil {
		s.data = map[string][]string{}
		return
	}
	s.data = d
}

func (s *instanceMaskStore) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o755)
	_ = os.WriteFile(s.path, data, 0o644)
}

// maskedFor returns the masked plugin names for an instance (nil/empty = 无).
func (s *instanceMaskStore) maskedFor(instanceID string) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[instanceID]
}

// set stores the masked plugin names; empty names reset the instance to 无屏蔽.
func (s *instanceMaskStore) set(instanceID string, names []string) {
	s.mu.Lock()
	if len(names) == 0 {
		delete(s.data, instanceID)
	} else {
		clean := make([]string, 0, len(names))
		seen := map[string]bool{}
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n != "" && !seen[n] {
				seen[n] = true
				clean = append(clean, n)
			}
		}
		sort.Strings(clean)
		s.data[instanceID] = clean
	}
	s.mu.Unlock()
	s.save()
}

// removeInstance drops a deleted instance's mask set entirely.
func (s *instanceMaskStore) removeInstance(instanceID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.data, instanceID)
	s.mu.Unlock()
	s.save()
}

// removePlugin drops an uninstalled plugin from every instance's mask set.
func (s *instanceMaskStore) removePlugin(name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	changed := false
	for id, names := range s.data {
		kept := names[:0]
		for _, n := range names {
			if n != name {
				kept = append(kept, n)
			}
		}
		if len(kept) != len(names) {
			if len(kept) == 0 {
				delete(s.data, id)
			} else {
				s.data[id] = kept
			}
			changed = true
		}
	}
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

// GetInstanceMasks returns the masked plugin names for an instance, pruned to
// the currently installed plugins (uninstalled ones are dropped — they are
// neither shown nor masked).
func (a *App) GetInstanceMasks(instanceID string) ([]string, error) {
	if a.store.find(instanceID) == nil {
		return nil, fmt.Errorf("实例不存在: %s", instanceID)
	}
	return a.prunedMasks(instanceID), nil
}

// SetInstanceMasks sets which plugins are masked for an instance. Names that
// are not currently installed are dropped silently (they would be skipped at
// launch anyway). Returns the pruned, persisted list.
func (a *App) SetInstanceMasks(instanceID string, names []string) ([]string, error) {
	if a.store.find(instanceID) == nil {
		return nil, fmt.Errorf("实例不存在: %s", instanceID)
	}
	installed, _ := readInstalledPlugins()
	clean := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		if _, ok := installed[n]; !ok {
			continue // 已卸载：不保存、不屏蔽
		}
		seen[n] = true
		clean = append(clean, n)
	}
	sort.Strings(clean)
	a.masks.set(instanceID, clean)
	return a.masks.maskedFor(instanceID), nil
}

// prunedMasks returns the instance's masked names restricted to installed
// plugins (stale entries for uninstalled plugins are hidden).
func (a *App) prunedMasks(instanceID string) []string {
	installed, _ := readInstalledPlugins()
	masked := a.masks.maskedFor(instanceID)
	out := make([]string, 0, len(masked))
	for _, n := range masked {
		if _, ok := installed[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// maskRelName is the temporary overlay file name for an instance, written into
// the INSTANCE directory (the dsh process cwd). A relative name with no spaces
// and no quotes keeps the launch command free of `"` — Go's exec.Command would
// escape embedded quotes for msvcrt (`\"`), which cmd.exe mis-parses into
// literal quotes in the child argv (the failed-overlay-path bug). Node's
// path.resolve() resolves it against process.cwd() = the instance directory.
func maskRelName(instanceID string) string {
	return ".dsh-mask-" + instanceID + ".yml"
}

// writeMaskOverlay writes the temporary `--patch` overlay for an instance into
// its directory and returns the RELATIVE file name, or "" when nothing is
// masked. Masked plugins that are no longer installed are skipped (never fail,
// never covered). The file is a YAML entry list (same format as
// cordis.patch.yml): `disabled: true` rows that only override the disabled
// key, keeping each entry's config/name intact. It is read once at dsh boot
// and is NOT watched by the profile's HMR, so it can be deleted as soon as the
// instance stops.
func (a *App) writeMaskOverlay(instanceID, dir string) (string, error) {
	installed, err := readInstalledPlugins()
	if err != nil {
		return "", err
	}
	var rows []string
	for _, name := range a.masks.maskedFor(instanceID) {
		if _, ok := installed[name]; !ok {
			continue // 已卸载：不临时覆盖
		}
		entryIDs := packageEntryIDs(name)
		if len(entryIDs) == 0 {
			entryIDs = []string{name}
		}
		for _, id := range entryIDs {
			rows = append(rows, "- id: "+id+"\n  disabled: true")
		}
	}
	if len(rows) == 0 {
		return "", nil
	}
	sort.Strings(rows)
	var b strings.Builder
	b.WriteString("# DSH Launcher 临时屏蔽层（--patch overlay，仅本次启动生效）\n")
	b.WriteString(strings.Join(rows, "\n"))
	b.WriteString("\n")
	rel := maskRelName(instanceID)
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

// cleanupMask removes an instance's temporary overlay file from its directory
// (best-effort). Safe once the process has booted: overlays are read once.
func cleanupMask(instanceID, dir string) {
	if dir == "" {
		return
	}
	_ = os.Remove(filepath.Join(dir, maskRelName(instanceID)))
}

// cleanupAllMasks removes every instance's temporary overlay file (called on
// shutdown, after all processes are reaped).
func (a *App) cleanupAllMasks() {
	for _, inst := range a.store.list() {
		cleanupMask(inst.ID, inst.Directory)
	}
}

// webTokenRe matches the dsh `web` subcommand as a standalone whitespace-
// delimited token (case-sensitive), never a segment inside a path like
// `C:\web\app.js`.
var webTokenRe = regexp.MustCompile(`(^|[ \t])web([ \t]|$)`)

// insertPatchFlag places `--patch <mask>` right AFTER the dsh `web` subcommand
// and BEFORE any app argument. The dsh launcher parses its own flags only up
// to the first unrecognized token (which starts the app's inner arguments), so
// appending the flag at the END would put it behind e.g. `--no-open` — commander
// would then pass `--patch` through as an app argument and report it unknown.
// maskRel must be a relative, quote-free name (see writeMaskOverlay) so the
// whole command string stays free of `"`.
func insertPatchFlag(cmdStr, maskRel string) string {
	flag := " --patch " + maskRel
	m := webTokenRe.FindStringSubmatchIndex(cmdStr)
	if m == nil {
		return cmdStr + flag
	}
	after := m[1]
	if after > 1 && (cmdStr[after-1] == ' ' || cmdStr[after-1] == '\t') {
		after-- // drop the matched trailing whitespace; insert right after `web`
	}
	return cmdStr[:after] + flag + cmdStr[after:]
}

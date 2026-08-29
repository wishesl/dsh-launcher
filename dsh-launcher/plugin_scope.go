package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Per-plugin "适用" scope: which launcher instances a plugin applies to.
//
// Installed plugins live in ONE shared profile (`web`) used by every
// instance, so without a scope every instance loads every plugin. A scope
// makes the launcher reconcile the profile's patch layer when an instance
// starts: plugins that do NOT apply to the started instance are masked
// (disabled), plugins that DO apply are unmasked. Absent/empty scope =
// 全部实例 (the default) — such plugins are never touched automatically, so
// the manual enable/disable switch keeps working exactly as before.

// pluginScopeFilePath is the persisted store (%APPDATA%\DSHLauncher).
// A var so tests can point it at a temp directory (same pattern as
// patchFilePath / favoritesFilePath).
var pluginScopeFilePath = func() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "DSHLauncher", "plugin-scope.json")
}

// pluginScopeStore persists plugin name -> applicable instance IDs.
// Empty/missing entry = applies to ALL instances.
type pluginScopeStore struct {
	mu   sync.Mutex
	path string
	data map[string][]string
}

func newPluginScopeStore() *pluginScopeStore {
	s := &pluginScopeStore{path: pluginScopeFilePath(), data: map[string][]string{}}
	s.load()
	return s
}

func (s *pluginScopeStore) load() {
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

func (s *pluginScopeStore) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o755)
	_ = os.WriteFile(s.path, data, 0o644)
}

// scopeFor returns the applicable instance ids for a plugin; nil/empty = 全部.
func (s *pluginScopeStore) scopeFor(name string) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[name]
}

// set stores the applicable instance ids; empty ids reset the plugin to
// 全部实例 (the key is removed).
func (s *pluginScopeStore) set(name string, ids []string) {
	s.mu.Lock()
	if len(ids) == 0 {
		delete(s.data, name)
	} else {
		seen := map[string]bool{}
		clean := make([]string, 0, len(ids))
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id != "" && !seen[id] {
				seen[id] = true
				clean = append(clean, id)
			}
		}
		sort.Strings(clean)
		s.data[name] = clean
	}
	s.mu.Unlock()
	s.save()
}

// delete drops a plugin's scope entry entirely (used on uninstall).
func (s *pluginScopeStore) delete(name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.data, name)
	s.mu.Unlock()
	s.save()
}

// removeInstance drops a deleted instance from every scope entry; a scope
// left empty falls back to 全部实例 (key removed).
func (s *pluginScopeStore) removeInstance(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	changed := false
	for name, ids := range s.data {
		kept := ids[:0]
		for _, x := range ids {
			if x != id {
				kept = append(kept, x)
			}
		}
		if len(kept) != len(ids) {
			if len(kept) == 0 {
				delete(s.data, name)
			} else {
				s.data[name] = kept
			}
			changed = true
		}
	}
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

// snapshot returns a defensive copy of the whole scope map.
func (s *pluginScopeStore) snapshot() map[string][]string {
	out := map[string][]string{}
	if s == nil {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.data {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// SetPluginScope sets which instances an installed plugin applies to.
// instanceIDs == nil/empty → 全部实例 (default). Returns the refreshed
// installed list so the frontend re-renders the 适用 tags immediately.
func (a *App) SetPluginScope(name string, instanceIDs []string) ([]InstalledPlugin, error) {
	if isInboxBundle(name) {
		return nil, fmt.Errorf("官方基础插件不可设置适用实例")
	}
	if _, ok, _ := installedLookup(name); !ok {
		return nil, fmt.Errorf("插件未安装: %s", name)
	}
	a.scope.set(name, instanceIDs)
	return a.ListInstalledPlugins()
}

// reconcilePluginScope applies per-plugin 适用 rules when an instance starts:
// for every installed plugin with an EXPLICIT scope (non-empty), plugins that
// do not apply to the instance are masked (disabled via the patch layer) and
// plugins that do apply are unmasked — so alternating between instances with
// different scopes keeps each one's plugin set correct. Plugins without a
// scope (default 全部实例) are left untouched, preserving the manual switch.
// A one-line summary is streamed to the instance log.
func (a *App) reconcilePluginScope(instanceID string) {
	installed, err := readInstalledPlugins()
	if err != nil {
		return
	}
	if a.scope == nil {
		return
	}
	scopes := a.scope.snapshot()

	var masked, unmasked []string
	for name := range installed {
		ids := scopes[name]
		if len(ids) == 0 {
			continue // 默认全部实例 —— 不自动干预，保留手动开关
		}
		applies := false
		for _, id := range ids {
			if id == instanceID {
				applies = true
				break
			}
		}
		entryIDs := packageEntryIDs(name)
		if len(entryIDs) == 0 {
			entryIDs = []string{name}
		}
		if !applies {
			for _, id := range entryIDs {
				_ = applyPatchState(id, true)
			}
			_ = setDshMarketDisabled(name, true)
			masked = append(masked, name)
		} else {
			for _, id := range entryIDs {
				_ = applyPatchState(id, false)
			}
			_ = setDshMarketDisabled(name, false)
			unmasked = append(unmasked, name)
		}
	}
	if len(masked) > 0 || len(unmasked) > 0 {
		msg := "按「适用」自动调整插件:"
		if len(masked) > 0 {
			msg += " 屏蔽 " + strings.Join(masked, ", ")
		}
		if len(unmasked) > 0 {
			msg += " 启用 " + strings.Join(unmasked, ", ")
		}
		a.systemLog(instanceID, 0, msg)
	}
}

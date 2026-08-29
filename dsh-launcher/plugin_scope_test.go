package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// newScopeTestApp builds an App with an isolated scope store + profile, with a
// single fake installed plugin `@liustack/modlens` whose bundle patch declares
// the loader entry id `modlens`.
func newScopeTestApp(t *testing.T) *App {
	t.Helper()

	// plugin-scope.json → temp path
	orig := pluginScopeFilePath
	t.Cleanup(func() { pluginScopeFilePath = orig })
	pluginScopeFilePath = func() string { return filepath.Join(t.TempDir(), "plugin-scope.json") }

	// marketProfileDir → <tmp>/profiles/web
	t.Setenv("DSH_HOME", t.TempDir())
	profile := marketProfileDir()
	pkgDir := filepath.Join(profile, "node_modules", "@liustack", "modlens")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"@liustack/modlens","dsh":{"bundle":{"patch":"./cordis.patch.yml"}}}`
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "- insert:\n    - id: modlens\n      name: '@liustack/modlens'\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "cordis.patch.yml"), []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
	profilePkg := `{"dependencies":{"@liustack/modlens":"github:liustack/modlens"}}`
	if err := os.WriteFile(filepath.Join(profile, "package.json"), []byte(profilePkg), 0o644); err != nil {
		t.Fatal(err)
	}

	return &App{scope: newPluginScopeStore()}
}

func TestPluginScopeStoreRoundTrip(t *testing.T) {
	orig := pluginScopeFilePath
	t.Cleanup(func() { pluginScopeFilePath = orig })
	p := filepath.Join(t.TempDir(), "plugin-scope.json")
	pluginScopeFilePath = func() string { return p }

	s := newPluginScopeStore()
	s.set("@liustack/modlens", []string{"inst-b", "inst-a", "inst-a", "  "})
	got := s.scopeFor("@liustack/modlens")
	if want := []string{"inst-a", "inst-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scopeFor = %v, want %v (deduped + sorted)", got, want)
	}

	// reload from disk
	s2 := newPluginScopeStore()
	if got := s2.scopeFor("@liustack/modlens"); !reflect.DeepEqual(got, []string{"inst-a", "inst-b"}) {
		t.Fatalf("reloaded scope = %v, want [inst-a inst-b]", got)
	}

	// empty ids = 全部实例 (key removed)
	s.set("@liustack/modlens", nil)
	if s.scopeFor("@liustack/modlens") != nil {
		t.Fatal("empty scope should be nil (全部实例)")
	}
}

func TestPluginScopeStoreRemoveInstance(t *testing.T) {
	orig := pluginScopeFilePath
	t.Cleanup(func() { pluginScopeFilePath = orig })
	pluginScopeFilePath = func() string { return filepath.Join(t.TempDir(), "plugin-scope.json") }

	s := newPluginScopeStore()
	s.set("p1", []string{"a", "b"})
	s.set("p2", []string{"a"})
	s.removeInstance("a")

	if got := s.scopeFor("p1"); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("p1 scope = %v, want [b]", got)
	}
	if s.scopeFor("p2") != nil {
		t.Fatal("p2 scope should fall back to 全部实例 after its only instance is removed")
	}
}

func TestReconcilePluginScope(t *testing.T) {
	app := newScopeTestApp(t)
	app.scope.set("@liustack/modlens", []string{"inst-a"})

	// inst-b starts → plugin not applicable → masked (patch row + dsh-market list)
	app.reconcilePluginScope("inst-b")
	if !readPatchDisabled()["modlens"] {
		t.Fatal("inst-b should mask the plugin (entry id disabled)")
	}
	if !dshMarketDisabledList()["@liustack/modlens"] {
		t.Fatal("inst-b should add the plugin to dsh-market disabled list")
	}

	// inst-a starts → plugin applicable → unmasked
	app.reconcilePluginScope("inst-a")
	if readPatchDisabled()["modlens"] {
		t.Fatal("inst-a should unmask the plugin")
	}
	if dshMarketDisabledList()["@liustack/modlens"] {
		t.Fatal("inst-a should remove the plugin from dsh-market disabled list")
	}

	// scope cleared (全部实例) → reconcile must NOT touch the plugin
	app.scope.set("@liustack/modlens", nil)
	app.reconcilePluginScope("inst-b")
	if readPatchDisabled()["modlens"] {
		t.Fatal("unscoped plugin must never be auto-masked")
	}
}

func TestSetPluginScopeValidation(t *testing.T) {
	app := newScopeTestApp(t)
	if _, err := app.SetPluginScope("not-installed", []string{"a"}); err == nil {
		t.Fatal("SetPluginScope on a missing plugin must error")
	}
	if _, err := app.SetPluginScope("@deepseek-ai/dsh-base", []string{"a"}); err == nil {
		t.Fatal("SetPluginScope on an inbox bundle must error")
	}
	list, err := app.SetPluginScope("@liustack/modlens", []string{"inst-a"})
	if err != nil {
		t.Fatalf("SetPluginScope: %v", err)
	}
	if len(list) != 1 || len(list[0].Scope) != 1 || list[0].Scope[0] != "inst-a" {
		t.Fatalf("ListInstalledPlugins after set = %+v, want scope [inst-a]", list)
	}
}

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// newMaskTestApp builds an App with an isolated mask store + profile, with a
// single fake installed plugin `@liustack/modlens` whose bundle patch declares
// the loader entry id `modlens`.
func newMaskTestApp(t *testing.T) *App {
	t.Helper()

	orig := instanceMasksFilePath
	t.Cleanup(func() { instanceMasksFilePath = orig })
	storePath := t.TempDir()
	instanceMasksFilePath = func() string { return filepath.Join(storePath, "instance-masks.json") }

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

	dir := t.TempDir()
	store := &instanceStore{path: filepath.Join(dir, "instances.json")}
	store.add(Instance{ID: "inst-a", Name: "A", Directory: dir})
	store.add(Instance{ID: "inst-b", Name: "B", Directory: dir})
	return &App{store: store, masks: newInstanceMaskStore()}
}

func TestInstanceMaskStoreRoundTrip(t *testing.T) {
	orig := instanceMasksFilePath
	t.Cleanup(func() { instanceMasksFilePath = orig })
	p := t.TempDir()
	instanceMasksFilePath = func() string { return filepath.Join(p, "instance-masks.json") }

	s := newInstanceMaskStore()
	s.set("inst-a", []string{"@liustack/modlens", "other", "other", " "})
	if got := s.maskedFor("inst-a"); !reflect.DeepEqual(got, []string{"@liustack/modlens", "other"}) {
		t.Fatalf("maskedFor = %v, want deduped+sorted list", got)
	}
	s2 := newInstanceMaskStore() // reload from disk
	if got := s2.maskedFor("inst-a"); !reflect.DeepEqual(got, []string{"@liustack/modlens", "other"}) {
		t.Fatalf("reloaded masks = %v", got)
	}
	s.set("inst-a", nil)
	if s.maskedFor("inst-a") != nil {
		t.Fatal("empty mask set should be nil (无屏蔽)")
	}
}

func TestInstanceMaskStoreCleanup(t *testing.T) {
	orig := instanceMasksFilePath
	t.Cleanup(func() { instanceMasksFilePath = orig })
	p := t.TempDir()
	instanceMasksFilePath = func() string { return filepath.Join(p, "instance-masks.json") }

	s := newInstanceMaskStore()
	s.set("inst-a", []string{"p1", "p2"})
	s.set("inst-b", []string{"p1"})

	s.removePlugin("p1")
	if got := s.maskedFor("inst-a"); !reflect.DeepEqual(got, []string{"p2"}) {
		t.Fatalf("after removePlugin(p1) inst-a = %v, want [p2]", got)
	}
	if s.maskedFor("inst-b") != nil {
		t.Fatal("inst-b mask should fall back to nil after its only plugin is uninstalled")
	}

	s.removeInstance("inst-a")
	if s.maskedFor("inst-a") != nil {
		t.Fatal("removed instance mask must be gone")
	}
}

// THE core guarantee: masking writes ONLY the temporary overlay file — the
// global patch layer and dsh-market state stay untouched, and uninstalled
// plugins are neither shown nor covered.
func TestWriteMaskOverlay(t *testing.T) {
	app := newMaskTestApp(t)
	app.masks.set("inst-b", []string{"@liustack/modlens"})
	instDir := app.store.find("inst-b").Directory

	// inst-b has the plugin masked → overlay rows generated in the instance
	// dir, referenced by a RELATIVE quote-free name (Go's exec escaping would
	// corrupt a quoted absolute path through cmd.exe).
	rel, err := app.writeMaskOverlay("inst-b", instDir)
	if err != nil {
		t.Fatalf("writeMaskOverlay: %v", err)
	}
	if rel == "" {
		t.Fatal("expected a mask file for inst-b")
	}
	if strings.ContainsAny(rel, "\"' \t") {
		t.Fatalf("mask name must be quote/space-free for the command line, got %q", rel)
	}
	full := filepath.Join(instDir, rel)
	data, _ := os.ReadFile(full)
	text := string(data)
	if !strings.Contains(text, "- id: modlens") || !strings.Contains(text, "disabled: true") {
		t.Fatalf("mask file missing disable row:\n%s", text)
	}
	// …and the GLOBAL state is untouched (this is the whole point of the
	// temporary-overlay design).
	if readPatchDisabled()["modlens"] {
		t.Fatal("global cordis.patch.yml must stay untouched by masking")
	}
	if dshMarketDisabledList()["@liustack/modlens"] {
		t.Fatal("dsh-market state must stay untouched by masking")
	}

	// An instance with nothing masked → no overlay at all (old dsh versions
	// without --patch keep working).
	if p, err := app.writeMaskOverlay("inst-a", instDir); err != nil || p != "" {
		t.Fatalf("unmasked instance should produce no overlay, got %q, %v", p, err)
	}

	// A masked plugin that got uninstalled is skipped: no rows, no file.
	if err := os.Remove(filepath.Join(marketProfileDir(), "node_modules", "@liustack", "modlens", "package.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(marketProfileDir(), "package.json")); err != nil {
		t.Fatal(err)
	}
	if p, err := app.writeMaskOverlay("inst-b", instDir); err != nil || p != "" {
		t.Fatalf("uninstalled masked plugin should be skipped, got %q, %v", p, err)
	}

	// Restore the profile manifest; cleanup removes the mask file.
	profilePkg := `{"dependencies":{"@liustack/modlens":"github:liustack/modlens"}}`
	if err := os.WriteFile(filepath.Join(marketProfileDir(), "package.json"), []byte(profilePkg), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err = app.writeMaskOverlay("inst-b", instDir)
	if err != nil {
		t.Fatalf("writeMaskOverlay after restore: %v", err)
	}
	cleanupMask("inst-b", instDir)
	if _, err := os.Stat(filepath.Join(instDir, rel)); !os.IsNotExist(err) {
		t.Fatalf("cleanupMask should remove the overlay file: %v", err)
	}
}

func TestSetInstanceMasks(t *testing.T) {
	app := newMaskTestApp(t)

	if _, err := app.SetInstanceMasks("missing", []string{"@liustack/modlens"}); err == nil {
		t.Fatal("SetInstanceMasks on a missing instance must error")
	}
	if _, err := app.GetInstanceMasks("missing"); err == nil {
		t.Fatal("GetInstanceMasks on a missing instance must error")
	}

	// Uninstalled names are dropped silently (neither saved nor masked).
	got, err := app.SetInstanceMasks("inst-a", []string{"@liustack/modlens", "ghost-plugin"})
	if err != nil {
		t.Fatalf("SetInstanceMasks: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"@liustack/modlens"}) {
		t.Fatalf("SetInstanceMasks returned %v, want [@liustack/modlens]", got)
	}

	// GetInstanceMasks prunes stale entries (simulate an uninstall: drop the
	// plugin from the profile manifest, then read back).
	if err := os.Remove(filepath.Join(marketProfileDir(), "package.json")); err != nil {
		t.Fatal(err)
	}
	if got, err := app.GetInstanceMasks("inst-a"); err != nil || len(got) != 0 {
		t.Fatalf("GetInstanceMasks after uninstall = %v, %v; want empty", got, err)
	}
}

// The dsh launcher parses its own flags only before the first app argument,
// so --patch MUST land right after the `web` subcommand, never after app args
// like --no-open (regression: appending at the end made commander report
// `unknown option '--patch'`).
func TestInsertPatchFlag(t *testing.T) {
	mask := ".dsh-mask-abc.yml"
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"version-mode web with app args",
			"npx @deepseek-ai/dsh web --no-open --port 3081",
			"npx @deepseek-ai/dsh web --patch .dsh-mask-abc.yml --no-open --port 3081"},
		{"source-mode bin.ts web with app args",
			"node --import tsx/esm apps/cli/src/bin.ts web --no-open",
			"node --import tsx/esm apps/cli/src/bin.ts web --patch .dsh-mask-abc.yml --no-open"},
		{"web with no app args",
			"pnpm dsh web",
			"pnpm dsh web --patch .dsh-mask-abc.yml"},
		{"no web token → appends",
			"node apps/cli/src/bin.ts",
			"node apps/cli/src/bin.ts --patch .dsh-mask-abc.yml"},
	}
	for _, c := range cases {
		if got := insertPatchFlag(c.cmd, mask); got != c.want {
			t.Errorf("%s: insertPatchFlag(%q) = %q, want %q", c.name, c.cmd, got, c.want)
		}
	}
	// A path segment named `web` must NOT be treated as the subcommand.
	got := insertPatchFlag(`node C:\projects\web\app.js web`, mask)
	want := `node C:\projects\web\app.js web --patch .dsh-mask-abc.yml`
	if got != want {
		t.Errorf("path segment must be ignored: got %q, want %q", got, want)
	}
	// The command line must stay free of quotes so Go's exec escaping cannot
	// corrupt the mask arg through cmd.exe.
	if strings.ContainsAny(insertPatchFlag("pnpm dsh web --no-open", mask), `"`) {
		t.Fatalf("patched command must not contain quotes: %q", insertPatchFlag("pnpm dsh web --no-open", mask))
	}
}

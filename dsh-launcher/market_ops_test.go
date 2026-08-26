package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallTargetFor(t *testing.T) {
	npm := func(s string) *string { return &s }

	cases := []struct {
		name string
		url  string
		npm  *string
		want string
		ok   bool
	}{
		{"npm preferred", "https://github.com/owner/dsh-x", npm("dsh-x"), "dsh-x", true},
		{"github fallback", "https://github.com/owner/dsh-x", nil, "github:owner/dsh-x", true},
		{"monorepo subpath", "https://github.com/owner/mono/tree/main/pkgs/a", nil, "github:owner/mono#path:/pkgs/a", true},
		{"scoped npm", "https://github.com/scope/repo", npm("@scope/repo"), "@scope/repo", true},
		{"trailing slash", "https://github.com/owner/dsh-x/", nil, "github:owner/dsh-x", true},
		{"non-github rejected", "https://gitlab.com/owner/x", nil, "", false},
		{"dotdot subpath rejected", "https://github.com/owner/mono/tree/main/../evil", nil, "", false},
	}
	for _, c := range cases {
		got, ok := installTargetFor(c.url, c.npm)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: installTargetFor(%q)=%q,%v want %q,%v", c.name, c.url, got, ok, c.want, c.ok)
		}
	}
}

func TestPluginCommand(t *testing.T) {
	cases := []struct {
		name    string
		inst    Instance
		args    []string
		wantSub string
	}{
		{"local", Instance{PkgMgr: "local"}, []string{"add", "dsh-x"}, "npx @deepseek-ai/dsh plugin --profile web add dsh-x"},
		{"npx exact", Instance{PkgMgr: "npx", Version: "0.1.1-rc.2"}, []string{"remove", "dsh-x"}, "npx -y @deepseek-ai/dsh@0.1.1-rc.2 plugin --profile web remove dsh-x"},
		{"pnpm latest", Instance{PkgMgr: "pnpm", Version: "latest"}, []string{"add", "github:a/b"}, "pnpm dlx @deepseek-ai/dsh@latest plugin --profile web add github:a/b"},
	}
	for _, c := range cases {
		got := pluginCommand(c.inst, c.args...)
		if got != c.wantSub {
			t.Errorf("%s: pluginCommand=%q want %q", c.name, got, c.wantSub)
		}
	}
}

func TestParseIgnoredBuilds(t *testing.T) {
	out := "…\nIgnored build scripts: koffi, node-pty. Run \"pnpm approve-builds\" …\n"
	got := parseIgnoredBuilds(out)
	if len(got) != 2 || got[0] != "koffi" || got[1] != "node-pty" {
		t.Fatalf("parseIgnoredBuilds=%v want [koffi node-pty]", got)
	}
	if len(parseIgnoredBuilds("no such line")) != 0 {
		t.Fatal("expected no ignored builds")
	}
}

func TestMergeAllowBuilds(t *testing.T) {
	dir := t.TempDir()
	ws := "allowBuilds:\n  koffi: true\n"
	if err := os.WriteFile(filepath.Join(dir, "pnpm-workspace.yaml"), []byte(ws), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := mergeAllowBuilds(dir, []string{"koffi", "node-pty", "@google/genai"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("mergeAllowBuilds returned %v", got)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "pnpm-workspace.yaml"))
	text := string(data)
	for _, want := range []string{"koffi: true", "node-pty: true", "'@google/genai': true"} {
		if !contains(text, want) {
			t.Errorf("merged yaml missing %q:\n%s", want, text)
		}
	}
	// no duplicate blocks
	if count := countStr(text, "allowBuilds:"); count != 1 {
		t.Errorf("expected 1 allowBuilds block, got %d:\n%s", count, text)
	}
}

func TestApplyPatchState(t *testing.T) {
	dir := t.TempDir()
	// point the patch file at the temp dir
	orig := patchFilePath
	defer func() { patchFilePath = orig }()
	patchFilePath = func() string { return filepath.Join(dir, "cordis.patch.yml") }
	// marketProfileDir is also used by applyPatchState? No — only patchFilePath. Good.

	if err := applyPatchState("dsh-x", true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "cordis.patch.yml"))
	if !contains(string(data), "- id: dsh-x") || !contains(string(data), "disabled: true") {
		t.Fatalf("disable row not written:\n%s", data)
	}
	if !readPatchDisabled()["dsh-x"] {
		t.Fatal("readPatchDisabled should see dsh-x disabled")
	}

	// enable again → row removed (we own id+disabled only)
	if err := applyPatchState("dsh-x", false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "cordis.patch.yml"))
	if contains(string(data), "dsh-x") {
		t.Fatalf("enable should drop our row:\n%s", data)
	}
	if readPatchDisabled()["dsh-x"] {
		t.Fatal("dsh-x should no longer be disabled")
	}
}

func TestMarketProfileDir(t *testing.T) {
	// Regression: without DSH_HOME the fallback MUST include the leading
	// ".dsh" — otherwise every read/toggle/duplicate-guard points at
	// <home>/profiles/web while the dsh CLI still uses <home>/.dsh/profiles/web.
	t.Setenv("DSH_HOME", "")
	got := marketProfileDir()
	if !strings.HasSuffix(got, filepath.Join(".dsh", "profiles", "web")) {
		t.Fatalf("marketProfileDir() without DSH_HOME = %q, want suffix %q", got, filepath.Join(".dsh", "profiles", "web"))
	}

	t.Setenv("DSH_HOME", "X:/custom-home")
	if got := marketProfileDir(); got != filepath.Join("X:/custom-home", "profiles", "web") {
		t.Fatalf("marketProfileDir() with DSH_HOME = %q, want %q", got, filepath.Join("X:/custom-home", "profiles", "web"))
	}
}

func TestMarketCacheFile(t *testing.T) {
	p := marketCacheFile()
	if p == "" || !strings.Contains(p, "DSHLauncher") || !strings.HasSuffix(p, "market-catalog.json") {
		t.Fatalf("marketCacheFile() = %q", p)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func countStr(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}

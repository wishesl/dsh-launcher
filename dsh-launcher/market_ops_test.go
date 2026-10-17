package main

import (
	"encoding/json"
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

func TestParseInsertedIDs(t *testing.T) {
	patch := `# dsh bundle layer
- insert:
    - id: modlens
      name: '@liustack/modlens'
`
	ids := parseInsertedIDs(patch)
	if len(ids) != 1 || ids[0] != "modlens" {
		t.Fatalf("parseInsertedIDs = %v, want [modlens]", ids)
	}

	// Nested insert blocks and non-insert rows (config of OTHER plugins) must
	// be ignored; only ids under `insert:` count as owned.
	patch2 := `- insert:
    - id: a
- id: other
  config:
    x: 1
- insert:
    - id: b
`
	ids2 := parseInsertedIDs(patch2)
	if len(ids2) != 2 || ids2[0] != "a" || ids2[1] != "b" {
		t.Fatalf("parseInsertedIDs(patch2) = %v, want [a b]", ids2)
	}
}

func TestPackageEntryIDs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DSH_HOME", dir) // marketProfileDir = <dir>/profiles/web
	profile := marketProfileDir()
	pkg := filepath.Join(profile, "node_modules", "@liustack", "modlens")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"@liustack/modlens","dsh":{"bundle":{"patch":"./cordis.patch.yml"}}}`
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "- insert:\n    - id: modlens\n      name: '@liustack/modlens'\n"
	if err := os.WriteFile(filepath.Join(pkg, "cordis.patch.yml"), []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
	ids := packageEntryIDs("@liustack/modlens")
	if len(ids) != 1 || ids[0] != "modlens" {
		t.Fatalf("packageEntryIDs = %v, want [modlens]", ids)
	}
	if ids := packageEntryIDs("not-a-plugin"); len(ids) != 0 {
		t.Fatalf("expected no ids for unknown package, got %v", ids)
	}
}

func TestSetDshMarketDisabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DSH_HOME", dir)
	profile := marketProfileDir()
	if err := os.MkdirAll(filepath.Join(profile, ".dsh-market"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"disabled":["a"],"groups":{},"region":"china"}`
	if err := os.WriteFile(filepath.Join(profile, ".dsh-market", "state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setDshMarketDisabled("@liustack/modlens", true); err != nil {
		t.Fatal(err)
	}
	if err := setDshMarketDisabled("a", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(profile, ".dsh-market", "state.json"))
	var doc struct {
		Disabled []string `json:"disabled"`
		Region   string   `json:"region"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Disabled) != 1 || doc.Disabled[0] != "@liustack/modlens" {
		t.Fatalf("disabled = %v, want [@liustack/modlens]", doc.Disabled)
	}
	if doc.Region != "china" {
		t.Fatalf("region lost: %q", doc.Region)
	}
}

func TestNormalizeAllowKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"node-pty@1", "node-pty"},
		{"node-pty@^1.0.0", "node-pty"},
		{"node-pty@~1.2.0", "node-pty"},
		{"node-pty@1.0.0", "node-pty@1.0.0"}, // exact kept
		{"node-pty@1.1.0-beta.16", "node-pty@1.1.0-beta.16"},
		{"@scope/pkg", "@scope/pkg"},
		{"@scope/pkg@1", "@scope/pkg"},
		{"@scope/pkg@2.1.3", "@scope/pkg@2.1.3"},
		{"esbuild", "esbuild"},
		{"koffi", "koffi"},
	}
	for _, c := range cases {
		if got := normalizeAllowKey(c.in); got != c.want {
			t.Errorf("normalizeAllowKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeAllowBuilds(t *testing.T) {
	dir := t.TempDir()
	ws := "packages:\n  - .\n\nnodeLinker: hoisted\nallowBuilds:\n  esbuild: true\n  node-pty@1: true\n  msgpackr-extract: true\n"
	if err := os.WriteFile(filepath.Join(dir, "pnpm-workspace.yaml"), []byte(ws), 0o644); err != nil {
		t.Fatal(err)
	}
	if !sanitizeAllowBuilds(dir) {
		t.Fatal("sanitizeAllowBuilds should report a change")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "pnpm-workspace.yaml"))
	text := string(data)
	if contains(text, "node-pty@1") {
		t.Fatalf("node-pty@1 still present:\n%s", text)
	}
	for _, want := range []string{"node-pty: true", "esbuild: true", "msgpackr-extract: true", "nodeLinker: hoisted"} {
		if !contains(text, want) {
			t.Errorf("sanitized yaml lost %q:\n%s", want, text)
		}
	}
	if countStr(text, "allowBuilds:") != 1 {
		t.Fatalf("expected 1 allowBuilds block, got:\n%s", text)
	}
	// idempotent: a second pass reports no change
	if sanitizeAllowBuilds(dir) {
		t.Fatal("second sanitize should be a no-op")
	}
	// no allowBuilds block → untouched
	dir2 := t.TempDir()
	plain := "packages:\n  - .\n"
	if err := os.WriteFile(filepath.Join(dir2, "pnpm-workspace.yaml"), []byte(plain), 0o644); err != nil {
		t.Fatal(err)
	}
	if sanitizeAllowBuilds(dir2) {
		t.Fatal("file without allowBuilds should not change")
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

func TestInstalledGithub(t *testing.T) {
	cases := []struct {
		name     string
		spec     string
		homepage string
		want     string
	}{
		{"github spec", "github:owner/repo", "", "https://github.com/owner/repo"},
		{"github spec subpath", "github:owner/mono#path:/pkgs/a", "", "https://github.com/owner/mono"},
		{"npm spec with github homepage", "dsh-x", "https://github.com/owner/dsh-x", "https://github.com/owner/dsh-x"},
		{"npm spec with github homepage trailing", "dsh-x", "https://github.com/owner/dsh-x/", "https://github.com/owner/dsh-x/"},
		{"npm spec with other homepage", "dsh-x", "https://example.com/x", ""},
		{"linked local", "link:../my-plugin", "", ""},
		{"file local", "file:../my-plugin", "https://example.com/x", ""},
		{"no homepage", "dsh-x", "", ""},
	}
	for _, c := range cases {
		if got := installedGithub(c.spec, c.homepage); got != c.want {
			t.Errorf("%s: installedGithub(%q,%q) = %q, want %q", c.name, c.spec, c.homepage, got, c.want)
		}
	}
}

// --- pnpm minimumReleaseAgeExclude 收敛 / 补齐 ---

const releaseAgeRealWorldYaml = `packages:
  - .

nodeLinker: hoisted
minimumReleaseAgeExclude:
  - dshmarket@1.9.0 || 1.66.7
  - '@sutong12/dsh-opencode-go-usage@0.1.1'
  - '@deepseek-ai/dsh-brand@0.2.1-rc.1'
  - '@deepseek-ai/dsh-subagent-claude-code@0.2.1-rc.1'
  - '@deepseek-ai/dsh-tool-subagent@0.2.1-rc.1'
  - '@deepseek-ai/dsh-brand@0.2.1-rc.2'
  - '@deepseek-ai/dsh-subagent-claude-code@0.2.1-rc.2'
  - '@deepseek-ai/dsh-tool-subagent@0.2.1-rc.2'
allowBuilds:
  koffi: true
`

// TestNormalizeReleaseAgeExcludes 钉住这次真机故障的根因：
// pnpm 11 对 minimumReleaseAgeExclude 里「同一个包」**只看第一条**，所以升级过一次
// （新版本被 pnpm 追加到列表末尾）之后，旧版本排在前面会让新版本永远被拒 ——
// 任何 pnpm 安装都在「核对 lockfile 供应链策略」这步整体失败。
func TestNormalizeReleaseAgeExcludes(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "pnpm-workspace.yaml")
	if err := os.WriteFile(file, []byte(releaseAgeRealWorldYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := normalizeReleaseAgeExcludes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("应合并 3 条重复项，实际 %d", n)
	}
	data, _ := os.ReadFile(file)
	text := string(data)

	// 每个重复包只留一条，且是版本更高的那条。
	for _, want := range []string{"'@deepseek-ai/dsh-brand@0.2.1-rc.2'"} {
		if !contains(text, want) {
			t.Errorf("保留的应是最高版本 %s：\n%s", want, text)
		}
	}
	for _, gone := range []string{"dsh-brand@0.2.1-rc.1", "dsh-subagent-claude-code@0.2.1-rc.1", "dsh-tool-subagent@0.2.1-rc.1"} {
		if contains(text, gone) {
			t.Errorf("旧版本条目应被删掉：%s\n%s", gone, text)
		}
	}
	// 含 `||` 的多版本条目、以及别的块必须原样保留。
	if !contains(text, "dshmarket@1.9.0 || 1.66.7") {
		t.Errorf("含 || 的条目不该被动：\n%s", text)
	}
	if !contains(text, "'@sutong12/dsh-opencode-go-usage@0.1.1'") {
		t.Errorf("无关条目不该丢：\n%s", text)
	}
	if countStr(text, "minimumReleaseAgeExclude:") != 1 {
		t.Errorf("不该出现重复块：\n%s", text)
	}
	if !contains(text, "allowBuilds:\n  koffi: true") {
		t.Errorf("其它块必须保留：\n%s", text)
	}

	// 幂等：再来一次应该无事可做。
	if n2, err := normalizeReleaseAgeExcludes(dir); err != nil || n2 != 0 {
		t.Errorf("第二次应为 no-op，实际 n=%d err=%v", n2, err)
	}
	// 没有该块 / 文件不存在 → no-op，不报错。
	if n3, err := normalizeReleaseAgeExcludes(t.TempDir()); err != nil || n3 != 0 {
		t.Errorf("缺文件应为 no-op，实际 n=%d err=%v", n3, err)
	}
}

// TestEnsureReleaseAgeExcludes 覆盖"被 pnpm 拒绝后自动补齐并重试"这一步。
func TestEnsureReleaseAgeExcludes(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "pnpm-workspace.yaml")
	if err := os.WriteFile(file, []byte("minimumReleaseAgeExclude:\n  - 'x@1.0.0'\nallowBuilds:\n  koffi: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := ensureReleaseAgeExcludes(dir, []string{"@deepseek-ai/dsh-brand@0.2.1-rc.2", "x@2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("应写入 2 条，实际 %d", n)
	}
	data, _ := os.ReadFile(file)
	text := string(data)
	if !contains(text, "'@deepseek-ai/dsh-brand@0.2.1-rc.2'") {
		t.Errorf("scoped 包要带引号：\n%s", text)
	}
	if !contains(text, "- x@2.0.0") || contains(text, "- 'x@1.0.0'") {
		t.Errorf("同名旧条目应被替换成目标版本：\n%s", text)
	}
	if !contains(text, "allowBuilds:\n  koffi: true") {
		t.Errorf("其它块必须保留：\n%s", text)
	}

	// 已覆盖目标版本 → no-op（不重复追加）。
	if n2, err := ensureReleaseAgeExcludes(dir, []string{"x@2.0.0"}); err != nil || n2 != 0 {
		t.Errorf("已满足时应为 no-op，实际 n=%d err=%v", n2, err)
	}
}

// TestReleaseAgeViolationParsing 覆盖从 pnpm 输出里认出这次拒绝、并取出被拒的版本。
func TestReleaseAgeViolationParsing(t *testing.T) {
	out := `? Verifying lockfile against supply-chain policies (123 entries)...
✗ Lockfile failed supply-chain policy check (123 entries in 603ms)
[ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION] 3 lockfile entries failed verification:
  @deepseek-ai/dsh-brand@0.2.1-rc.2 was published at 2026-10-17T08:16:09.678Z, within the minimumReleaseAge cutoff (2026-10-16T18:02:00.823Z)
  @deepseek-ai/dsh-subagent-claude-code@0.2.1-rc.2 was published at 2026-10-17T08:30:25.118Z, within the minimumReleaseAge cutoff (2026-10-16T18:02:00.823Z)
  @deepseek-ai/dsh-tool-subagent@0.2.1-rc.2 was published at 2026-10-17T08:25:14.755Z, within the minimumReleaseAge cutoff (2026-10-16T18:02:00.823Z)
`
	if !isReleaseAgeViolation(out) {
		t.Fatal("应当认出 minimumReleaseAge 拒绝")
	}
	specs := releaseAgeViolationSpecs(out)
	want := []string{
		"@deepseek-ai/dsh-brand@0.2.1-rc.2",
		"@deepseek-ai/dsh-subagent-claude-code@0.2.1-rc.2",
		"@deepseek-ai/dsh-tool-subagent@0.2.1-rc.2",
	}
	if len(specs) != len(want) {
		t.Fatalf("解析出 %v, want %v", specs, want)
	}
	for i := range want {
		if specs[i] != want[i] {
			t.Errorf("specs[%d] = %q, want %q", i, specs[i], want[i])
		}
	}

	// 别的失败不能被当成发布时效拒绝（否则会去改 profile 的豁免清单）。
	for _, other := range []string{"", "[ERR_PNPM_NO_MATCHING_VERSION] No matching version", "ERR_PNPM_IGNORED_BUILDS Ignored build scripts: koffi@3.1.1"} {
		if isReleaseAgeViolation(other) {
			t.Errorf("不该把 %q 当成发布时效拒绝", other)
		}
	}
	if got := releaseAgeViolationSpecs("no marker here"); got != nil {
		t.Errorf("没有报错行时应返回 nil，实际 %v", got)
	}
}

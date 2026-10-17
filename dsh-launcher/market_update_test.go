package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		// The classic string-compare trap: "1.10.0" < "1.9.0" as text.
		{"1.2.3", "1.10.0", -1},
		{"1.10.0", "1.2.3", 1},
		{"1.2.3", "1.2.4", -1},
		{"2.0.0", "1.99.99", 1},
		// Prerelease ranks below its release.
		{"1.2.3-rc.1", "1.2.3", -1},
		{"1.2.3", "1.2.3-rc.1", 1},
		{"1.2.3-rc.1", "1.2.3-rc.2", -1},
		{"1.2.3-rc.10", "1.2.3-rc.9", 1},
		{"1.2.3-alpha", "1.2.3-1", 1}, // alphanumeric outranks numeric
		{"1.2.3-alpha.1", "1.2.3-alpha", 1},
		{"0.1.1-rc.2", "0.1.2", -1},
		{"1.2.3+build.5", "1.2.3", 0}, // build metadata is ignored
		{"not-a-version", "1.0.0", 1}, // malformed input falls back to string compare, never panics
	}
	for _, c := range cases {
		if got := compareSemver(c.a, c.b); got != c.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestJumpType(t *testing.T) {
	cases := []struct {
		cur, next string
		want      string
	}{
		{"1.2.3", "1.2.3", "none"},
		{"1.2.3", "1.2.4", "patch"},
		{"1.2.3", "1.3.0", "minor"},
		{"1.2.3", "2.0.0", "major"},
		{"0.1.1", "1.0.0", "major"},
		{"1.2.3-rc.1", "1.2.3-rc.2", "prerelease"},
		{"1.2.3-rc.1", "1.2.3", "prerelease"}, // same core, leaving the prerelease channel
		{"2.0.0", "1.9.9", "downgrade"},
		{"", "1.0.0", "unknown"},
	}
	for _, c := range cases {
		if got := jumpType(c.cur, c.next); got != c.want {
			t.Errorf("jumpType(%q, %q) = %q, want %q", c.cur, c.next, got, c.want)
		}
	}
}

func TestInRange(t *testing.T) {
	cases := []struct {
		version, spec string
		ok, known     bool
	}{
		{"1.3.0", "^1.2.3", true, true},
		{"1.2.4", "^1.2.3", true, true},
		{"2.0.0", "^1.2.3", false, true},
		{"0.2.0", "^0.1.0", false, true}, // caret on 0.x only allows patch moves
		{"0.1.5", "^0.1.0", true, true},
		{"1.2.9", "~1.2.3", true, true},
		{"1.3.0", "~1.2.3", false, true},
		{"1.2.3", "1.2.3", true, true},
		{"1.2.4", "1.2.3", false, true},
		{"1.5.0", "1.x", true, true},
		{"2.0.0", "1.5.0", false, true},
		{"9.9.9", "*", true, true},
		{"9.9.9", "latest", true, true},
		{"2.0.0", ">=1.5.0", true, true},
		{"1.0.0", ">=1.5.0", false, true},
		// Prerelease never satisfies a plain range (npm semantics).
		{"1.3.0-rc.1", "^1.2.3", false, true},
		// Shapes we refuse to guess.
		{"1.3.0", "npm:other@^1.0.0", false, false},
		{"1.3.0", "next", false, false},
		{"1.3.0", "^1.0.0 || ^2.0.0", false, false},
		{"1.3.0", "", false, false},
	}
	for _, c := range cases {
		ok, known := inRange(c.version, c.spec)
		if ok != c.ok || known != c.known {
			t.Errorf("inRange(%q, %q) = (%v, %v), want (%v, %v)", c.version, c.spec, ok, known, c.ok, c.known)
		}
	}
}

func TestResolveNpmName(t *testing.T) {
	cases := []struct{ spec, want string }{
		{"dsh-review", "dsh-review"},
		{"dsh-review@^0.1.1", "dsh-review"},
		{"@scope/pkg", "@scope/pkg"},
		{"@scope/pkg@^1.2.3", "@scope/pkg"},
		{"@scope/pkg@1.2.3", "@scope/pkg"},
		{"npm:aliased@^1.0.0", "aliased"},
		{"file:C:/Users/x/.dsh/profiles/web/.dsh-builtin/dsh-launcher-plugin", ""},
		{"link:../local-plugin", ""},
		{"workspace:*", ""},
		{"github:owner/repo", ""},
		{"github:owner/repo#path:/pkgs/a", ""},
		{"git+https://github.com/owner/repo.git", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := resolveNpmName(c.spec); got != c.want {
			t.Errorf("resolveNpmName(%q) = %q, want %q", c.spec, got, c.want)
		}
	}

	// 版本 / 区间型 spec（含**不带范围符的精确版本**）：registry 名在依赖 key 里。
	// 踩过的坑："@deepseek-ai/dsh-subagent-claude-code": "0.2.1-rc.1" 里的 `0.2.1-rc.1`
	// 也符合包名正则，于是启动器去查一个叫 `0.2.1-rc.1` 的包 → 404 → 这一行的更新检查
	// 整个废掉（UI 显示「检查失败」，用户永远看不到更新）。
	withKey := []struct{ spec, key, want string }{
		{"0.2.1-rc.1", "@deepseek-ai/dsh-subagent-claude-code", "@deepseek-ai/dsh-subagent-claude-code"},
		{"1.2.3", "dsh-review", "dsh-review"},
		{"^1.2.3", "dsh-review", "dsh-review"},
		{"~1.0.0", "dsh-review", "dsh-review"},
		{">=1.0.0", "dsh-review", "dsh-review"},
		{"1.x", "dsh-review", "dsh-review"},
		{"next", "dsh-review", "dsh-review"},
		{"latest", "dsh-review", "dsh-review"},
		{"*", "dsh-review", "dsh-review"},
	}
	for _, c := range withKey {
		if got := resolveNpmName(c.spec, c.key); got != c.want {
			t.Errorf("resolveNpmName(%q, %q) = %q, want %q", c.spec, c.key, got, c.want)
		}
	}
	// 没有 fallback 时，这些 spec 不能凭形状被当成包名。
	for _, spec := range []string{"0.2.1-rc.1", "1.2.3", "1.x", "next", "*"} {
		if got := resolveNpmName(spec); got != "" {
			t.Errorf("resolveNpmName(%q) = %q, want \"\"（版本/区间/dist-tag 不是包名）", spec, got)
		}
	}
}

func TestReadLockedCommit(t *testing.T) {
	dir := t.TempDir()
	restore := marketProfileDir
	marketProfileDir = func() string { return dir }
	defer func() { marketProfileDir = restore }()

	lock := `lockfileVersion: '9.0'
importers:
  .:
    dependencies:
      dsh-x:
        specifier: github:Owner/Repo
        version: https://codeload.github.com/Owner/Repo/tar.gz/0123456789abcdef0123456789abcdef01234567
      dsh-y:
        specifier: github:other/thing
        version: https://codeload.github.com/other/thing/tar.gz/fedcba9876543210fedcba9876543210fedcba98
`
	if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readLockedCommit("owner/repo"); got != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("readLockedCommit = %q, want the pinned sha (case-insensitive repo)", got)
	}
	if got := readLockedCommit("nope/missing"); got != "" {
		t.Errorf("readLockedCommit(unknown repo) = %q, want empty", got)
	}
	if got := readLockedCommit(""); got != "" {
		t.Errorf("readLockedCommit(\"\") = %q, want empty", got)
	}

	// Missing lock file must not panic or invent anything.
	restore2 := marketProfileDir
	marketProfileDir = func() string { return filepath.Join(dir, "nope") }
	defer func() { marketProfileDir = restore2 }()
	if got := readLockedCommit("owner/repo"); got != "" {
		t.Errorf("readLockedCommit without lock file = %q, want empty", got)
	}
}

func TestFillNpmVerdict(t *testing.T) {
	cases := []struct {
		name          string
		row           UpdateCheck
		spec, latest  string
		tag           string // 目标来自哪个 dist-tag；"" = latest
		has, risky    bool
		inRange, runs bool
		jump          string
	}{
		{
			name: "in-range minor", row: UpdateCheck{Current: "1.2.3"},
			spec: "^1.2.3", latest: "1.3.0",
			has: true, risky: false, inRange: true, runs: true, jump: "minor",
		},
		{
			name: "major is risky and rewrites spec", row: UpdateCheck{Current: "0.1.32"},
			spec: "^0.1.32", latest: "1.0.0",
			has: true, risky: true, inRange: false, runs: true, jump: "major",
		},
		{
			name: "unsupported spec shape is treated as out of range", row: UpdateCheck{Current: "1.0.0"},
			spec: "next", latest: "1.1.0",
			has: true, risky: true, inRange: false, runs: true, jump: "minor",
		},
		{
			name: "pinned prerelease newer than registry latest is not runnable",
			row:  UpdateCheck{Current: "1.3.0-rc.1"},
			spec: "^1.3.0-rc.1", latest: "1.2.9",
			has: false, risky: false, inRange: false, runs: false, jump: "downgrade",
		},
		{
			// 预发布通道（next/alpha）本身就是"需要确认"的语义：即使 spec 是精确版本、
			// jump 也只算 prerelease，也必须 Risky。
			name: "exact spec but next-channel target is still risky",
			row:  UpdateCheck{Current: "0.2.1-alpha.2"},
			spec: "0.2.1-alpha.2", latest: "0.2.1-rc.1", tag: "next",
			has: true, risky: true, inRange: false, runs: true, jump: "prerelease",
		},
	}
	for _, c := range cases {
		row := c.row
		tag := c.tag
		if tag == "" {
			tag = "latest"
		}
		fillNpmVerdict(&row, c.spec, c.latest, tag)
		if row.HasUpdate != c.has || row.Risky != c.risky || row.InRange != c.inRange || row.Runnable != c.runs || row.Jump != c.jump {
			t.Errorf("%s: got has=%v risky=%v inRange=%v runnable=%v jump=%s; want has=%v risky=%v inRange=%v runnable=%v jump=%s",
				c.name, row.HasUpdate, row.Risky, row.InRange, row.Runnable, row.Jump,
				c.has, c.risky, c.inRange, c.runs, c.jump)
		}
	}
}

// TestPickUpdateTarget 锁住"从哪些 tag 里挑目标"：这正是 dsh-subagent-claude-code /
// dsh-spill-local 那类包的坑 —— latest 常年停在 0.0.1-rc.1，新版挂在 next，只看 latest
// 就永远不提示更新。
func TestPickUpdateTarget(t *testing.T) {
	cases := []struct {
		name            string
		pkg             string
		tags            map[string]string
		current         string
		wantTarget      string
		wantTag         string
		wantNoteContain string
	}{
		{
			name: "普通包只认 latest", pkg: "dshmarket",
			tags:    map[string]string{"latest": "1.66.15", "next": "1.70.0"},
			current: "1.66.15", wantTarget: "1.66.15", wantTag: "latest",
		},
		{
			name: "@deepseek-ai 看 next", pkg: "@deepseek-ai/dsh-subagent-claude-code",
			tags:    map[string]string{"latest": "0.0.1-rc.1", "next": "0.2.1-rc.1", "alpha": "0.2.1-alpha.5"},
			current: "0.2.1-alpha.2", wantTarget: "0.2.1-rc.1", wantTag: "next",
		},
		{
			name: "@deepseek-ai 取 semver 最大的那个 tag（alpha 比 next 大时选 alpha）",
			pkg:  "@deepseek-ai/dsh-x",
			tags: map[string]string{"latest": "0.2.0-rc.2", "next": "0.2.1-rc.1", "alpha": "0.2.2-alpha.1"},
			// 0.2.2-alpha.1 的 core 更大 → 应选 alpha
			current: "0.2.1-rc.1", wantTarget: "0.2.2-alpha.1", wantTag: "alpha",
		},
		{
			name: "@deepseek-ai 本地更新时不选旧版（不降级）", pkg: "@deepseek-ai/dsh-y",
			tags:    map[string]string{"latest": "0.0.1-rc.1", "next": "0.2.0-rc.2"},
			current: "0.2.1-rc.1", wantTarget: "0.2.0-rc.2", wantTag: "next",
			wantNoteContain: "落后于本地",
		},
		{
			name: "普通包 registry 全落后 → 给 note 解释为什么没有更新", pkg: "dsh-z",
			tags:    map[string]string{"latest": "0.0.1-rc.1"},
			current: "0.2.1-rc.1", wantTarget: "0.0.1-rc.1", wantTag: "latest",
			wantNoteContain: "落后于本地",
		},
		{
			name: "没有可用 tag", pkg: "dsh-none", tags: map[string]string{}, current: "1.0.0",
			wantTarget: "", wantTag: "", wantNoteContain: "没有可用的 dist-tag",
		},
		{
			name: "非法 semver 的 tag 被忽略", pkg: "@deepseek-ai/dsh-bad",
			tags:    map[string]string{"latest": "not-a-version", "next": "1.2.3"},
			current: "1.0.0", wantTarget: "1.2.3", wantTag: "next",
		},
	}
	for _, c := range cases {
		target, tag, note := pickUpdateTarget(c.pkg, c.tags, c.current)
		if target != c.wantTarget || tag != c.wantTag {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, target, tag, c.wantTarget, c.wantTag)
		}
		if c.wantNoteContain != "" && !strings.Contains(note, c.wantNoteContain) {
			t.Errorf("%s: note = %q, want it to contain %q", c.name, note, c.wantNoteContain)
		}
		if c.wantNoteContain == "" && note != "" {
			t.Errorf("%s: unexpected note %q", c.name, note)
		}
	}
}

// TestEmitMarketStatusInvalidatesUpdateCache 钉住"操作成功要丢掉更新判定缓存"。
//
// 内置插件那一行的「已装 / 内置」版本**完全来自这份判定**；不失效的话，升级完界面还显示旧
// 版本，只有重启启动器（或耗掉 5 分钟 TTL）才刷新 —— 用户实测报的就是这个。
// 反过来，running / failed / cancelled 什么都没改动，缓存要留着，别白跑一遍 registry。
func TestEmitMarketStatusInvalidatesUpdateCache(t *testing.T) {
	app := &App{}
	prime := func() {
		updateCacheMu.Lock()
		updateCache = &UpdateCheckResult{Checked: "primed", Updatable: 7}
		updateCacheAt = time.Now()
		updateCacheMu.Unlock()
	}
	cached := func() bool {
		updateCacheMu.Lock()
		defer updateCacheMu.Unlock()
		return updateCache != nil
	}

	for _, state := range []string{"running", "failed", "cancelled"} {
		prime()
		app.emitMarketStatus(MarketOpStatus{State: state})
		if !cached() {
			t.Errorf("state=%s 没有改动已装版本，不该失效更新判定缓存", state)
		}
	}

	prime()
	app.emitMarketStatus(MarketOpStatus{State: "done", Kind: "install", Target: "dshmarket"})
	if cached() {
		t.Error("done 之后必须丢掉更新判定缓存（否则界面继续显示升级前的版本号）")
	}
}

// writeProfileJSON drops a file at <profile>/<rel>, creating parents.
func writeProfileJSON(t *testing.T, profile, rel string, doc any) {
	t.Helper()
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profile, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCheckPluginUpdates covers the whole read path against a fake registry:
// kinds, verdicts, and per-item error isolation.
func TestCheckPluginUpdates(t *testing.T) {
	profile := t.TempDir()
	restoreDir := marketProfileDir
	marketProfileDir = func() string { return profile }
	defer func() { marketProfileDir = restoreDir }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tags := map[string]map[string]string{
			"dsh-a": {"latest": "1.3.0"}, // in-range minor
			"dsh-b": {"latest": "2.0.0"}, // major, out of range
			// latest 常年不动的 @deepseek-ai/* 包：真正的新版挂在 next（真实案例：
			// @deepseek-ai/dsh-subagent-claude-code、dsh-spill-local 的 latest 都是 0.0.1-rc.1）。
			"@deepseek-ai/dsh-staletag": {"latest": "0.0.1-rc.1", "next": "0.2.1-rc.1"},
			"dsh-broken":                nil, // 404 → per-row error
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		name = strings.ReplaceAll(name, "%2F", "/")
		v, ok := tags[name]
		if !ok || len(v) == 0 {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"dist-tags": v})
	}))
	defer srv.Close()

	restoreBase, restoreMirror := marketRegistryBase, marketRegistryMirror
	marketRegistryBase = srv.URL + "/"
	marketRegistryMirror = "" // no fallback against the real network
	defer func() { marketRegistryBase, marketRegistryMirror = restoreBase, restoreMirror }()

	writeProfileJSON(t, profile, "package.json", map[string]any{
		"dependencies": map[string]string{
			"dsh-a":                     "^1.2.3",
			"dsh-b":                     "^1.2.3",
			"dsh-broken":                "^1.0.0",
			"@deepseek-ai/dsh-staletag": "^0.2.0",
			"dsh-local":                 "file:../dsh-local",
			selfRestartPluginName:       "file:C:/tmp/dsh-launcher-plugin",
		},
	})
	writeProfileJSON(t, profile, filepath.Join("node_modules", "dsh-a", "package.json"), map[string]any{"version": "1.2.3"})
	writeProfileJSON(t, profile, filepath.Join("node_modules", "dsh-b", "package.json"), map[string]any{"version": "1.2.3"})
	writeProfileJSON(t, profile, filepath.Join("node_modules", "dsh-broken", "package.json"), map[string]any{"version": "1.0.0"})
	writeProfileJSON(t, profile, filepath.Join("node_modules", "@deepseek-ai", "dsh-staletag", "package.json"), map[string]any{"version": "0.2.0-rc.2"})

	app := &App{}
	res, err := app.CheckPluginUpdates(true)
	if err != nil {
		t.Fatalf("CheckPluginUpdates: %v", err)
	}

	byName := map[string]UpdateCheck{}
	for _, p := range res.Plugins {
		byName[p.Name] = p
	}
	// Sorted by name, always 6 rows even when one check fails.
	if len(res.Plugins) != 6 {
		t.Fatalf("got %d rows, want 6: %+v", len(res.Plugins), res.Plugins)
	}
	if names := []string{res.Plugins[0].Name, res.Plugins[1].Name}; names[0] != "@deepseek-ai/dsh-staletag" || names[1] != "dsh-a" {
		t.Errorf("rows are not name-sorted: %v", names)
	}

	if a := byName["dsh-a"]; !a.HasUpdate || !a.Runnable || a.Risky || !a.InRange || a.Jump != "minor" || a.Latest != "1.3.0" || a.Tag != "latest" {
		t.Errorf("dsh-a: %+v", a)
	}
	if b := byName["dsh-b"]; !b.HasUpdate || !b.Risky || b.InRange || b.Jump != "major" {
		t.Errorf("dsh-b should be a risky major update: %+v", b)
	}
	// latest 停在旧预发布、新版挂 next：必须能挑出来，并标明来自 next、标 Risky。
	// （0.2.0-rc.2 → 0.2.1-rc.1 按 jumpType 的分档是 patch：只有第三段变了。）
	if st := byName["@deepseek-ai/dsh-staletag"]; !st.HasUpdate || !st.Runnable || !st.Risky || st.Latest != "0.2.1-rc.1" || st.Tag != "next" || st.Jump != "patch" {
		t.Errorf("@deepseek-ai/dsh-staletag must update from the next tag: %+v", st)
	}
	if br := byName["dsh-broken"]; br.Err == "" || br.HasUpdate {
		t.Errorf("dsh-broken should carry a per-row error and no update: %+v", br)
	}
	if l := byName["dsh-local"]; l.Kind != "linked" || l.Runnable || l.Err != "" {
		t.Errorf("dsh-local should be a non-updatable linked plugin: %+v", l)
	}
	if sb := byName[selfRestartPluginName]; sb.Kind != "builtin" || sb.Runnable || sb.Latest != embeddedBuiltinVersion() {
		t.Errorf("%s should be a builtin row comparing against the embedded version: %+v", selfRestartPluginName, sb)
	}
	if res.Updatable != 3 {
		t.Errorf("Updatable = %d, want 3", res.Updatable)
	}
}

// TestUpdatePluginRejects covers the guard rails that do not need a live
// instance: unknown plugins, linked/github kinds and risky gating.
//
// builtin (dsh-launcher-plugin) is deliberately NOT here any more: it is updatable
// now, and exercising it would run a real pnpm install. Its verdict is covered
// side-effect-free by TestBuiltinUpdateVerdict.
func TestUpdatePluginRejects(t *testing.T) {
	profile := t.TempDir()
	restoreDir := marketProfileDir
	marketProfileDir = func() string { return profile }
	defer func() { marketProfileDir = restoreDir }()

	writeProfileJSON(t, profile, "package.json", map[string]any{
		"dependencies": map[string]string{
			"dsh-local":    "file:../dsh-local",
			"dsh-git":      "github:owner/repo",
			selfRestartPluginName: "file:C:/tmp/dsh-launcher-plugin",
			"dsh-npmish":   "^1.0.0",
		},
	})

	app := &App{store: newInstanceStore()}
	app.store.add(Instance{ID: "i1", Name: "t", Directory: ".", Version: "latest", PkgMgr: "local"})

	cases := []struct {
		name string
		want string
	}{
		{"dsh-local", "本地开发插件"},
		{"dsh-git", "重新安装"},
		{"not-installed", "插件未安装"},
		{"", "不能为空"},
	}
	for _, c := range cases {
		_, err := app.UpdatePlugin("i1", c.name, true)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("UpdatePlugin(%q) error = %v, want it to contain %q", c.name, err, c.want)
		}
	}

	// Instance existence is checked AFTER the kind guards for non-updatable
	// plugins, so the case has to use a plain npm spec to reach that branch.
	if _, err := app.UpdatePlugin("missing", "dsh-npmish", true); err == nil || !strings.Contains(err.Error(), "实例不存在") {
		t.Errorf("UpdatePlugin with unknown instance: %v", err)
	}
}

// TestBuiltinUpdateVerdict covers the launcher-bundled plugin's verdict: an
// older materialized copy must come back runnable — the built-in reinstall is
// the ONLY path that carries a fix like relaxEmbedAuth to users who installed
// before it existed — while a copy matching the embedded version must not
// offer an update. Pure check: no pnpm runs here.
func TestBuiltinUpdateVerdict(t *testing.T) {
	profile := t.TempDir()
	restoreDir := marketProfileDir
	marketProfileDir = func() string { return profile }
	defer func() { marketProfileDir = restoreDir }()

	writeProfileJSON(t, profile, "package.json", map[string]any{
		"dependencies": map[string]string{selfRestartPluginName: "file:C:/tmp/dsh-launcher-plugin"},
	})
	app := &App{}
	builtinPath := filepath.Join(selfRestartBuiltinRel, "package.json")

	// 旧副本 → 可更新且可执行。
	writeProfileJSON(t, profile, builtinPath, map[string]any{"version": "0.0.1"})
	res, err := app.CheckPluginUpdates(true)
	if err != nil {
		t.Fatalf("CheckPluginUpdates: %v", err)
	}
	row := findUpdateCheck(res, selfRestartPluginName)
	if row == nil {
		t.Fatalf("builtin row missing: %+v", res.Plugins)
	}
	if row.Kind != "builtin" || row.Current != "0.0.1" || !row.HasUpdate || !row.Runnable {
		t.Errorf("outdated builtin must be runnable: %+v", *row)
	}
	if row.Latest != embeddedBuiltinVersion() {
		t.Errorf("builtin latest = %q, want embedded %q", row.Latest, embeddedBuiltinVersion())
	}

	// 与内置同版本 → 不提示更新、不可执行。
	writeProfileJSON(t, profile, builtinPath, map[string]any{"version": embeddedBuiltinVersion()})
	res, err = app.CheckPluginUpdates(true)
	if err != nil {
		t.Fatalf("CheckPluginUpdates (same version): %v", err)
	}
	row = findUpdateCheck(res, selfRestartPluginName)
	if row == nil || row.HasUpdate || row.Runnable {
		t.Errorf("up-to-date builtin must not be runnable: %+v", row)
	}
}

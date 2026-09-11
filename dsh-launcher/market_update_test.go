package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		{"file:C:/Users/x/.dsh/profiles/web/.dsh-builtin/dsh-self-mcp", ""},
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
	}
	for _, c := range cases {
		row := c.row
		fillNpmVerdict(&row, c.spec, c.latest)
		if row.HasUpdate != c.has || row.Risky != c.risky || row.InRange != c.inRange || row.Runnable != c.runs || row.Jump != c.jump {
			t.Errorf("%s: got has=%v risky=%v inRange=%v runnable=%v jump=%s; want has=%v risky=%v inRange=%v runnable=%v jump=%s",
				c.name, row.HasUpdate, row.Risky, row.InRange, row.Runnable, row.Jump,
				c.has, c.risky, c.inRange, c.runs, c.jump)
		}
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
		latest := map[string]string{
			"dsh-a":      "1.3.0", // in-range minor
			"dsh-b":      "2.0.0", // major, out of range
			"dsh-broken": "",      // 404 → per-row error
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		name = strings.ReplaceAll(name, "%2F", "/")
		v, ok := latest[name]
		if !ok || v == "" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"dist-tags": map[string]string{"latest": v}})
	}))
	defer srv.Close()

	restoreBase, restoreMirror := marketRegistryBase, marketRegistryMirror
	marketRegistryBase = srv.URL + "/"
	marketRegistryMirror = "" // no fallback against the real network
	defer func() { marketRegistryBase, marketRegistryMirror = restoreBase, restoreMirror }()

	writeProfileJSON(t, profile, "package.json", map[string]any{
		"dependencies": map[string]string{
			"dsh-a":        "^1.2.3",
			"dsh-b":        "^1.2.3",
			"dsh-broken":   "^1.0.0",
			"dsh-local":    "file:../dsh-local",
			"dsh-self-mcp": "file:C:/tmp/dsh-self-mcp",
		},
	})
	writeProfileJSON(t, profile, filepath.Join("node_modules", "dsh-a", "package.json"), map[string]any{"version": "1.2.3"})
	writeProfileJSON(t, profile, filepath.Join("node_modules", "dsh-b", "package.json"), map[string]any{"version": "1.2.3"})
	writeProfileJSON(t, profile, filepath.Join("node_modules", "dsh-broken", "package.json"), map[string]any{"version": "1.0.0"})

	app := &App{}
	res, err := app.CheckPluginUpdates(true)
	if err != nil {
		t.Fatalf("CheckPluginUpdates: %v", err)
	}

	byName := map[string]UpdateCheck{}
	for _, p := range res.Plugins {
		byName[p.Name] = p
	}
	// Sorted by name, always 5 rows even when one check fails.
	if len(res.Plugins) != 5 {
		t.Fatalf("got %d rows, want 5: %+v", len(res.Plugins), res.Plugins)
	}
	if names := []string{res.Plugins[0].Name, res.Plugins[1].Name}; names[0] != "dsh-a" || names[1] != "dsh-b" {
		t.Errorf("rows are not name-sorted: %v", names)
	}

	if a := byName["dsh-a"]; !a.HasUpdate || !a.Runnable || a.Risky || !a.InRange || a.Jump != "minor" || a.Latest != "1.3.0" {
		t.Errorf("dsh-a: %+v", a)
	}
	if b := byName["dsh-b"]; !b.HasUpdate || !b.Risky || b.InRange || b.Jump != "major" {
		t.Errorf("dsh-b should be a risky major update: %+v", b)
	}
	if br := byName["dsh-broken"]; br.Err == "" || br.HasUpdate {
		t.Errorf("dsh-broken should carry a per-row error and no update: %+v", br)
	}
	if l := byName["dsh-local"]; l.Kind != "linked" || l.Runnable || l.Err != "" {
		t.Errorf("dsh-local should be a non-updatable linked plugin: %+v", l)
	}
	if sb := byName["dsh-self-mcp"]; sb.Kind != "builtin" || sb.Runnable || sb.Latest != embeddedBuiltinVersion() {
		t.Errorf("dsh-self-mcp should be a builtin row comparing against the embedded version: %+v", sb)
	}
	if res.Updatable != 2 {
		t.Errorf("Updatable = %d, want 2", res.Updatable)
	}
}

// TestUpdatePluginRejects covers the guard rails that do not need a live
// instance: unknown plugins, linked/github/builtin kinds and risky gating.
func TestUpdatePluginRejects(t *testing.T) {
	profile := t.TempDir()
	restoreDir := marketProfileDir
	marketProfileDir = func() string { return profile }
	defer func() { marketProfileDir = restoreDir }()

	writeProfileJSON(t, profile, "package.json", map[string]any{
		"dependencies": map[string]string{
			"dsh-local":    "file:../dsh-local",
			"dsh-git":      "github:owner/repo",
			"dsh-self-mcp": "file:C:/tmp/dsh-self-mcp",
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
		{"dsh-self-mcp", "内置插件"},
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

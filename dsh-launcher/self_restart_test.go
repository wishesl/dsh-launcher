package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newSelfRestartTestApp builds an App with an isolated profile where the
// plugin `dsh-self-mcp` is installed, plus one instance rooted at a temp dir.
func newSelfRestartTestApp(t *testing.T) (*App, string) {
	t.Helper()
	t.Setenv("DSH_HOME", t.TempDir())
	profile := marketProfileDir()
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	profilePkg := `{"dependencies":{"dsh-self-mcp":"file:../dsh-self-mcp"}}`
	if err := os.WriteFile(filepath.Join(profile, "package.json"), []byte(profilePkg), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store := &instanceStore{path: filepath.Join(dir, "instances.json")}
	store.add(Instance{ID: "inst-a", Name: "A", Directory: dir})
	return &App{store: store, masks: newInstanceMaskStore()}, dir
}

func writeOptInMarker(dir string) {
	_ = os.WriteFile(filepath.Join(dir, selfRestartOptInMarker), []byte("# opt-in\n"), 0o644)
}

func TestSelfRestartEnabled(t *testing.T) {
	_, dir := newSelfRestartTestApp(t)

	// 已装插件但无 opt-in 标记 → 不启用（零残留的核心：其他项目目录没有标记）。
	if selfRestartEnabled(dir) {
		t.Fatal("installed plugin without opt-in marker must NOT enable self-restart")
	}
	writeOptInMarker(dir)
	if !selfRestartEnabled(dir) {
		t.Fatal("installed plugin + opt-in marker must enable self-restart")
	}
	if selfRestartEnabled("") {
		t.Fatal("empty dir must never enable self-restart")
	}

	// 插件被卸载 → 即使有标记也不启用（fail-soft：不生成覆盖层，实例照常启动）。
	if err := os.Remove(filepath.Join(marketProfileDir(), "package.json")); err != nil {
		t.Fatal(err)
	}
	if selfRestartEnabled(dir) {
		t.Fatal("uninstalled plugin must disable self-restart even with marker present")
	}
}

func TestWriteSelfRestartOverlay(t *testing.T) {
	_, dir := newSelfRestartTestApp(t)
	rel, err := writeSelfRestartOverlay("inst-a", dir)
	if err != nil {
		t.Fatalf("writeSelfRestartOverlay: %v", err)
	}
	if rel == "" {
		t.Fatal("expected an overlay file")
	}
	if strings.ContainsAny(rel, "\"' \t") {
		t.Fatalf("overlay name must be quote/space-free for the command line, got %q", rel)
	}
	if rel != selfRestartRelName("inst-a") {
		t.Fatalf("overlay name = %q, want %q", rel, selfRestartRelName("inst-a"))
	}
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "- insert:") {
		t.Fatalf("overlay must use an insert: block (bare rows only override existing entries):\n%s", text)
	}
	if !strings.Contains(text, "- id: self-restart") || !strings.Contains(text, "name: 'dsh-self-mcp'") {
		t.Fatalf("overlay missing mount row:\n%s", text)
	}

	// --patch 必须插在 `web` 子命令后（与屏蔽层同一条纪律）。
	got := insertPatchFlag("npx @deepseek-ai/dsh web --no-open", rel)
	want := "npx @deepseek-ai/dsh web --patch " + rel + " --no-open"
	if got != want {
		t.Fatalf("insertPatchFlag = %q, want %q", got, want)
	}

	// cleanup 同时清掉屏蔽层与自重启覆盖层。
	if err := os.WriteFile(filepath.Join(dir, ".dsh-mask-inst-a.yml"), []byte("- id: x\n  disabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanupMask("inst-a", dir)
	for _, name := range []string{rel, ".dsh-mask-inst-a.yml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("cleanupMask should remove %s: %v", name, err)
		}
	}
}

func TestConsumeRestartRequest(t *testing.T) {
	_, dir := newSelfRestartTestApp(t)

	// 无请求文件 → false
	if consumeRestartRequest(dir) {
		t.Fatal("absent request must return false")
	}
	if consumeRestartRequest("") {
		t.Fatal("empty dir must return false")
	}

	// 空文件 → false（不消费）
	state := filepath.Join(dir, selfRestartStateDir)
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restartRequestPath(dir), []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if consumeRestartRequest(dir) {
		t.Fatal("empty request must not be consumed")
	}

	// 有效请求 → true 且文件被删除（消费即删 → 杜绝重启循环）
	if err := os.WriteFile(restartRequestPath(dir), []byte(`{"instanceId":"inst-a","requestedAt":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !consumeRestartRequest(dir) {
		t.Fatal("valid request must be consumed")
	}
	if _, err := os.Stat(restartRequestPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("consumed request file must be removed: %v", err)
	}
}

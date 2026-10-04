package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newSelfRestartTestApp builds an App with an isolated profile where the
// plugin `dsh-launcher-plugin` is installed, plus one instance rooted at a temp dir.
func newSelfRestartTestApp(t *testing.T) (*App, string) {
	t.Helper()
	t.Setenv("DSH_HOME", t.TempDir())
	profile := marketProfileDir()
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	profilePkg := `{"dependencies":{"dsh-launcher-plugin":"file:../dsh-launcher-plugin"}}`
	if err := os.WriteFile(filepath.Join(profile, "package.json"), []byte(profilePkg), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store := &instanceStore{path: filepath.Join(dir, "instances.json")}
	store.add(Instance{ID: "inst-a", Name: "A", Directory: dir})
	// notifySeen 与 NewApp 保持一致：通知去重是 App 级状态，测试用的 App 也得有
	// （没有它 first() 会 fail-open 放行一切，去重用例就测不出来）。
	app := &App{store: store, masks: newInstanceMaskStore(), processes: make(map[string]*managedProcess), notifySeen: newNotifySeenSet()}
	app.bridge = newLauncherBridge(app)
	return app, dir
}

func TestLauncherPluginGate(t *testing.T) {
	app, _ := newSelfRestartTestApp(t)

	// 已装 + 桥接就绪 → 挂载（无实例级勾选：挂载跟随安装）。
	app.bridge.url = "http://127.0.0.1:1"
	mounted, installed, detail := app.launcherPluginGate()
	if !mounted || !installed {
		t.Fatalf("installed + bridge up must mount: mounted=%v installed=%v detail=%q", mounted, installed, detail)
	}
	if detail == "" {
		t.Fatal("detail 必须给出证据文案")
	}

	// 桥接未就绪 → 不挂载，但 installed 要如实（前端区分「没装」与「挂不上」）。
	app.bridge.url = ""
	mounted, installed, _ = app.launcherPluginGate()
	if mounted || !installed {
		t.Fatalf("bridge down must not mount but still report installed: mounted=%v installed=%v", mounted, installed)
	}

	// 桥接对象缺失（极端路径）→ 同样 fail-open 不 panic。
	app.bridge = nil
	mounted, installed, _ = app.launcherPluginGate()
	if mounted || !installed {
		t.Fatalf("nil bridge must fail open: mounted=%v installed=%v", mounted, installed)
	}
	app.bridge = newLauncherBridge(app)

	// 插件未安装 → 两者都 false（普通 profile 的常态）。
	if err := os.Remove(filepath.Join(marketProfileDir(), "package.json")); err != nil {
		t.Fatal(err)
	}
	mounted, installed, detail = app.launcherPluginGate()
	if mounted || installed {
		t.Fatalf("uninstalled plugin must disable both: mounted=%v installed=%v", mounted, installed)
	}
	if !strings.Contains(detail, selfRestartPluginName) {
		t.Errorf("detail 要点名缺的是哪个插件: %q", detail)
	}
}

func TestExtractEmbeddedSelfRestart(t *testing.T) {
	t.Setenv("DSH_HOME", t.TempDir())
	profile := marketProfileDir()
	dir, err := extractEmbeddedSelfRestart(profile)
	if err != nil {
		t.Fatalf("extractEmbeddedSelfRestart: %v", err)
	}
	if dir != filepath.Join(profile, selfRestartBuiltinRel) {
		t.Fatalf("materialized dir = %q, want %q", dir, filepath.Join(profile, selfRestartBuiltinRel))
	}
	for _, rel := range []string{"package.json", "lib/index.js", "README.md"} {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("embedded file %s missing: %v", rel, err)
		}
		if strings.TrimSpace(string(data)) == "" {
			t.Fatalf("embedded file %s is empty", rel)
		}
	}
	pkg, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pkg), `"name": "dsh-launcher-plugin"`) {
		t.Fatalf("materialized package.json wrong: %s", string(pkg))
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
	if !strings.Contains(text, "- id: launcher-plugin") || !strings.Contains(text, "name: '"+selfRestartPluginName+"'") {
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

func TestBridgeRestartFlag(t *testing.T) {
	app, _ := newSelfRestartTestApp(t)
	b := app.bridge

	// 没有标志 → false。
	if b.consumeRestart("inst-a", "launch-1") {
		t.Fatal("absent restart flag must return false")
	}
	if b.consumeRestart("", "launch-1") {
		t.Fatal("empty instance id must return false")
	}

	// 标志匹配 → true 且一次性消费（杜绝重启循环）。
	b.mu.Lock()
	b.restarts["inst-a"] = "launch-1"
	b.mu.Unlock()
	if !b.consumeRestart("inst-a", "launch-1") {
		t.Fatal("matching flag must be consumed as true")
	}
	if b.consumeRestart("inst-a", "launch-1") {
		t.Fatal("flag must be one-shot (consume-once)")
	}

	// launchId 不匹配的残留标志：取走但不算数（上一次启动的标志不能误触发重启）。
	b.mu.Lock()
	b.restarts["inst-a"] = "stale-launch"
	b.mu.Unlock()
	if b.consumeRestart("inst-a", "launch-1") {
		t.Fatal("stale launch id must not trigger a restart")
	}
	if _, ok := b.restarts["inst-a"]; ok {
		t.Fatal("stale flag must still be cleared (no residue)")
	}
}

func TestBridgeHandshakeLifecycle(t *testing.T) {
	app, _ := newSelfRestartTestApp(t)
	b := app.bridge

	// 无握手 → 面板走 absent 分诊。
	if _, ok := b.handshake("inst-a"); ok {
		t.Fatal("no handshake expected before /connect")
	}

	// /connect 存入 → 读回同一份。
	report := pluginCapabilityReport{
		Plugin:        selfRestartPluginName,
		PluginVersion: "0.1.0",
		LaunchID:      "launch-1",
		InstanceID:    "inst-a",
		Capabilities:  []pluginCapability{{ID: "pluginLoaded", OK: true}},
	}
	b.mu.Lock()
	b.handshakes["inst-a"] = report
	b.mu.Unlock()
	got, ok := b.handshake("inst-a")
	if !ok || got.Plugin != selfRestartPluginName || got.LaunchID != "launch-1" || len(got.Capabilities) != 1 {
		t.Fatalf("handshake round-trip failed: %+v ok=%v", got, ok)
	}

	// 实例重启 → 启动即清（握手的陈旧性由生命周期保证，不做比对）。
	b.clearHandshake("inst-a")
	if _, ok := b.handshake("inst-a"); ok {
		t.Fatal("handshake must be cleared at launch")
	}
	// nil-safe（测试构造的 App 可能没有桥接）。
	var nilBridge *launcherBridge
	nilBridge.clearHandshake("inst-a")
	if _, ok := nilBridge.handshake("inst-a"); ok {
		t.Fatal("nil bridge must report no handshake")
	}
	if nilBridge.consumeRestart("inst-a", "x") {
		t.Fatal("nil bridge must not consume restarts")
	}
}

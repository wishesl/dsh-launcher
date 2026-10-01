package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripURLQuery(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://127.0.0.1:3080/?token=abc123", "http://127.0.0.1:3080/"},
		{"http://127.0.0.1:3080/", "http://127.0.0.1:3080/"},
		{"", ""},
	}
	for _, c := range cases {
		if got := stripURLQuery(c.in); got != c.want {
			t.Errorf("stripURLQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPluginCapabilityItems(t *testing.T) {
	report := pluginCapabilityReport{
		Capabilities: []pluginCapability{
			// 故意乱序，并且带一个启动器不认识的新 id。
			{ID: "embedRelax", OK: false, Reason: "connection 上没有 authorizeIndex / requestRejection"},
			{ID: "restartTool", OK: true},
			{ID: "unknownFutureCap", OK: true},
			{ID: "pluginLoaded", OK: true},
		},
	}
	items := pluginCapabilityItems(report)

	// 已知项按固定顺序在前，未知 id 追加到末尾：面板顺序不随插件写法漂移。
	wantOrder := []string{"pluginLoaded", "restartTool", "embedRelax", "unknownFutureCap"}
	if len(items) != len(wantOrder) {
		t.Fatalf("items = %d 项, want %d", len(items), len(wantOrder))
	}
	for i, id := range wantOrder {
		if items[i].ID != id {
			t.Fatalf("items[%d].ID = %q, want %q（顺序：%v）", i, items[i].ID, id, ids(items))
		}
	}

	// 面板行的关键字段：label 有中文标签、source 标成插件、失败项带原因。
	if items[0].Label == "" || items[0].Source != "plugin" {
		t.Errorf("pluginLoaded 行不完整: %+v", items[0])
	}
	embed := items[2]
	if embed.OK || embed.Reason == "" || embed.Hint == "" {
		t.Errorf("embedRelax 失败行必须带原因与影响说明: %+v", embed)
	}
	// restartDelivery 本次没上报 → 不应凭空补一行。
	for _, it := range items {
		if it.ID == "restartDelivery" {
			t.Error("未上报的能力不应出现在面板里")
		}
	}
}

func ids(items []CapabilityItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func TestStalePluginReport(t *testing.T) {
	// 同一次启动 → 不是过期。
	if stalePluginReport("abc123", "abc123") {
		t.Error("launch id 相同不应判定为过期")
	}
	// 不同启动 → 确实是上一个进程留下的。
	if !stalePluginReport("abc123", "def456") {
		t.Error("launch id 不同应判定为过期")
	}
	// 任一侧没有凭据 → 不下结论。这一条是关键：启动器手上是 cmd /c 外壳的 pid、
	// 插件报的是 node 的 pid，两者按构造永远不等；只要缺凭据就报"已过期"，
	// 用户就会看到"功能一切正常却提示报告过期"（这正是修这个的原因）。
	if stalePluginReport("", "abc123") || stalePluginReport("abc123", "") || stalePluginReport("", "") {
		t.Error("缺少 launch id 时不应判定为过期（宁可不说，也不要谎报）")
	}
}

func TestMpLaunchID(t *testing.T) {
	if got := mpLaunchID(nil); got != "" {
		t.Errorf("没有托管进程时应返回空串，实际 %q", got)
	}
	if got := mpLaunchID(&managedProcess{launchID: "xyz"}); got != "xyz" {
		t.Errorf("mpLaunchID = %q, want xyz", got)
	}
}

func TestNewLaunchID(t *testing.T) {
	a, b := newLaunchID(), newLaunchID()
	if a == "" || b == "" {
		t.Fatal("launch id 不应为空")
	}
	if len(a) != 16 {
		t.Errorf("launch id 长度 = %d, want 16（8 字节 hex）", len(a))
	}
	if a == b {
		t.Error("两次生成的 launch id 不应相同")
	}
}

func TestPluginCopyMismatchReason(t *testing.T) {
	// 版本不一致 = 装的是旧副本：必须给出"重新安装"这个可操作结论，并且由调用方标成
	// unknown（不是故障）—— 功能其实还能用，报红就是误报。
	reason, mismatch := pluginCopyMismatchReason("0.1.0", "0.2.0")
	if !mismatch {
		t.Fatal("版本不一致应当判定为副本过期")
	}
	if !strings.Contains(reason, "0.1.0") || !strings.Contains(reason, "0.2.0") {
		t.Errorf("原因里要同时给出已装与内置版本，实际: %q", reason)
	}
	if !strings.Contains(reason, "重新安装") {
		t.Errorf("原因要可操作（告诉用户怎么办），实际: %q", reason)
	}

	// 一致 / 读不出来 → 不下结论，交给调用方走"可能加载失败"的缺省分支。
	for _, c := range [][2]string{{"0.2.0", "0.2.0"}, {"", "0.2.0"}, {"0.1.0", ""}, {"", ""}} {
		if _, mismatch := pluginCopyMismatchReason(c[0], c[1]); mismatch {
			t.Errorf("pluginCopyMismatchReason(%q, %q) 不应判定为不一致", c[0], c[1])
		}
	}
}

func TestPluginReportAbsentReason(t *testing.T) {
	app, _ := newSelfRestartTestApp(t) // 已安装新插件
	inst := app.store.find("inst-a")

	// 桥接未就绪 → 未挂载，属中性结论（unknown，不标红）。
	reason, unknown := app.pluginReportAbsentReason(inst)
	if !unknown {
		t.Fatalf("bridge down 是良性原因，应为 unknown，实际 reason=%q", reason)
	}
	if !strings.Contains(reason, "未挂载") {
		t.Errorf("原因要说清未挂载，实际: %q", reason)
	}

	// 桥接就绪但实例没运行 → unknown（插件根本没机会握手）。
	app.bridge.url = "http://127.0.0.1:1"
	reason, unknown = app.pluginReportAbsentReason(inst)
	if !unknown {
		t.Fatalf("实例未运行应为 unknown，实际 reason=%q", reason)
	}
	if !strings.Contains(reason, "未运行") {
		t.Errorf("原因要说清实例未运行，实际: %q", reason)
	}

	// 桥接就绪且实例在跑，却没握手 → 红灯（真正值得看的失败）。
	app.mu.Lock()
	app.processes["inst-a"] = &managedProcess{launchID: "launch-1"}
	app.mu.Unlock()
	reason, unknown = app.pluginReportAbsentReason(inst)
	if unknown {
		t.Fatalf("运行中无握手应为红色失败，实际被标成 unknown: %q", reason)
	}
	if !strings.Contains(reason, "握手") {
		t.Errorf("原因要指向缺失的握手，实际: %q", reason)
	}

	// 全局未安装 → unknown（最常见的分诊结果，绝不标红）。
	if err := os.Remove(filepath.Join(marketProfileDir(), "package.json")); err != nil {
		t.Fatal(err)
	}
	reason, unknown = app.pluginReportAbsentReason(inst)
	if !unknown {
		t.Fatalf("未安装插件应为 unknown，实际 reason=%q", reason)
	}
	if !strings.Contains(reason, selfRestartPluginName) {
		t.Errorf("原因要点名插件，实际: %q", reason)
	}
}

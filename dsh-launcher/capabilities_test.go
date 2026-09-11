package main

import (
	"encoding/json"
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

func TestReadPluginCapabilities(t *testing.T) {
	dir := t.TempDir()

	// 没有文件 → 没有报告（面板据此显示"未收到插件报告"）。
	if _, ok := readPluginCapabilities(dir); ok {
		t.Fatal("空目录不应读到能力报告")
	}
	if _, ok := readPluginCapabilities(""); ok {
		t.Fatal("空目录路径不应读到能力报告")
	}

	// 写一份模拟插件的报告（字段名必须与插件 writeCapabilityReport 一致）。
	report := pluginCapabilityReport{
		Schema:        1,
		Plugin:        "dsh-self-mcp",
		PluginVersion: "0.1.0",
		PID:           4242,
		ReportedAt:    "2026-09-12T02:00:00.000Z",
		Launcher:      true,
		InstanceID:    "inst-1",
		Capabilities: []pluginCapability{
			{ID: "pluginLoaded", OK: true},
			{ID: "embedRelax", OK: false, Reason: "connection 上没有 authorizeIndex / requestRejection"},
		},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, capsStateDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(capabilitiesPath(dir), raw, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, ok := readPluginCapabilities(dir)
	if !ok {
		t.Fatal("应当读到能力报告")
	}
	if got.Plugin != "dsh-self-mcp" || got.PluginVersion != "0.1.0" || got.PID != 4242 {
		t.Errorf("报告头部解析错误: %+v", got)
	}
	if len(got.Capabilities) != 2 || got.Capabilities[1].OK || got.Capabilities[1].Reason == "" {
		t.Errorf("能力项解析错误: %+v", got.Capabilities)
	}

	// 坏 JSON 不能 panic，也不能当成有效报告。
	if err := os.WriteFile(capabilitiesPath(dir), []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := readPluginCapabilities(dir); ok {
		t.Fatal("坏 JSON 不应被当作有效报告")
	}
}

func TestCleanupCapabilities(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, capsStateDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(capabilitiesPath(dir), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cleanupCapabilities(dir)
	if _, err := os.Stat(capabilitiesPath(dir)); !os.IsNotExist(err) {
		t.Fatal("陈旧能力报告必须在上次启动后被清掉（否则面板会拿着旧进程的结论谎报正常）")
	}
	// 目录不存在也不应 panic。
	cleanupCapabilities(filepath.Join(dir, "nope"))
	cleanupCapabilities("")
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

func TestSelfRestartGateDetail(t *testing.T) {
	// 未勾选不是故障（按设计就不挂载），措辞必须让用户看出"这是正常的"。
	off := selfRestartGateDetail(Instance{SelfRestart: false}, false)
	if !strings.Contains(off, "按设计") {
		t.Errorf("未勾选的说明要表明这是设计行为，实际: %q", off)
	}
	// 勾了但全局没装 → 要说清后果。
	notInstalled := selfRestartGateDetail(Instance{SelfRestart: true}, false)
	if !strings.Contains(notInstalled, selfRestartPluginName) || !strings.Contains(notInstalled, "不会生效") {
		t.Errorf("全局未装的说明要给出后果，实际: %q", notInstalled)
	}
	// 挂载成功 → 说清两个门控都通过了。
	on := selfRestartGateDetail(Instance{SelfRestart: true}, true)
	if !strings.Contains(on, selfRestartPluginName) {
		t.Errorf("挂载成功的说明应包含插件名，实际: %q", on)
	}
}

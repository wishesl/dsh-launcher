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

// disabledRowLine 是 0.2.1-rc.1 真机日志里的原文（只有后半段的建议被裁掉了）。
const disabledRowLine = `dsh: disabling profile plugin row "tool-subagent": Plugin @deepseek-ai/dsh-tool-subagent@0.2.1-alpha.2 is incompatible with dsh 0.2.1-rc.1: requirements {"@deepseek-ai/dsh-tools":"0.2.1-alpha.2","@deepseek-ai/dsh-llm":"0.2.1-alpha.2"}. Running it may cause crashes or data loss.`

func TestParseDSHDisabledRows(t *testing.T) {
	text := strings.Join([]string{
		"2026-10-17T12:33:47+08:00\tstderr\t" + disabledRowLine,
		"2026-10-17T12:33:47+08:00\tstderr\tdsh: disabling profile plugin row \"tool-subagent-fork\": Plugin @deepseek-ai/dsh-tool-subagent@0.2.1-alpha.2 is incompatible with dsh 0.2.1-rc.1: requirements {\"@deepseek-ai/dsh-tools\":\"0.2.1-alpha.2\"}. Running it may cause crashes or data loss.",
		// 同一行会被重复判定（DSH 重载会重放）：按行 id 去重，保留最后一条。
		"2026-10-17T12:33:52+08:00\tstderr\t" + disabledRowLine,
		// 别的告警不能被当成"不兼容" —— 认不出就不报，是这块面板可信度的前提。
		"2026-10-17T12:33:53+08:00\tstderr\tdsh: warning: 1 entry did not activate",
	}, "\n")

	rows := parseDSHDisabledRows(text)
	if len(rows) != 2 {
		t.Fatalf("解析出 %d 行, want 2: %+v", len(rows), rows)
	}
	if rows[0].Row != "tool-subagent" || rows[0].Pkg != "@deepseek-ai/dsh-tool-subagent@0.2.1-alpha.2" || rows[0].DSH != "0.2.1-rc.1" {
		t.Errorf("行字段解析不对: %+v", rows[0])
	}
	if rows[1].Row != "tool-subagent-fork" {
		t.Errorf("第二行应是 tool-subagent-fork: %+v", rows[1])
	}
	if got := requirementVersions(rows[0].Requirements); len(got) != 1 || got[0] != "0.2.1-alpha.2" {
		t.Errorf("requirements 版本去重后 = %v, want [0.2.1-alpha.2]", got)
	}
	if got := parseDSHDisabledRows("dsh: warning: 1 entry did not activate\n"); got != nil {
		t.Errorf("无关日志不该解析出任何禁用行，实际: %+v", got)
	}
}

func TestDSHDisabledItems(t *testing.T) {
	rows := parseDSHDisabledRows(disabledRowLine)
	items := dshDisabledItems(rows)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.ID != "dshProfileRow:tool-subagent" || it.OK || it.Unknown || it.Source != "dsh" {
		t.Errorf("面板条目形状不对: %+v", it)
	}
	// 内核自己下的结论 = 确定结论：Unknown 必须为 false，前端的红灯/计数才算得上
	// （AGENTS.md §9.2：只有 !ok && !unknown 才计为问题）。
	if !strings.Contains(it.Reason, "0.2.1-rc.1") || !strings.Contains(it.Reason, "0.2.1-alpha.2") {
		t.Errorf("原因里要同时给出插件与内核版本: %q", it.Reason)
	}
	if !strings.Contains(it.Hint, "allow-version") {
		t.Errorf("影响说明要给可操作出口（豁免入口）: %q", it.Hint)
	}
}

func TestCurrentRunTail(t *testing.T) {
	dir := t.TempDir()
	ls := &logStore{dir: dir, files: map[string]*os.File{}, sizes: map[string]int64{}}
	path := filepath.Join(dir, "inst-a.log")

	// 上一轮判过禁用、这一轮已经修好：只取本轮起点之后的内容，旧结论必须消失。
	content := "2026-10-17T12:33:47+08:00\tstderr\t" + disabledRowLine + "\n" +
		"2026-10-17T21:24:45+08:00\tsystem\t正在启动 DSH @0.2.1-rc.1 (目录: X)\n" +
		"2026-10-17T21:25:20+08:00\tstdout\tdsh web: http://127.0.0.1:3080/?token=t\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	text, ok := ls.currentRunTail("inst-a")
	if !ok {
		t.Fatal("日志里有本轮起点，应当 ok=true")
	}
	if strings.Contains(text, "disabling profile plugin row") {
		t.Error("上一轮的禁用行不能算在这一轮头上")
	}

	// 找不到本轮起点（日志刚轮转 / 这一轮还没写）→ ok=false：调用方必须当"没有结论"。
	if err := os.WriteFile(path, []byte("2026-10-17T12:33:47+08:00\tstderr\t"+disabledRowLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ls.currentRunTail("inst-a"); ok {
		t.Error("没有本轮起点时必须 ok=false（宁可不说，也不要把旧结论算进来）")
	}
	if _, ok := ls.currentRunTail("不存在的实例"); ok {
		t.Error("读不到日志时必须 ok=false")
	}
}

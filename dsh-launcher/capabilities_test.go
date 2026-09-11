package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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

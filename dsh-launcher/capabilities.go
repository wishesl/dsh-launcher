package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 兼容性 / 能力探测（compatibility probe）。
//
// 设计原则：判断"某项功能能不能用"靠**探测**，不靠 DSH 版本号分支。版本号是上游
// 控制的时间戳 —— 用户装的是 latest，任何"版本 → 预设"的兼容表都必然滞后一步，
// 而真正的判定条件（那个内部方法还在不在、那行日志还认不认得）与版本号没有因果
// 关系。所以：
//
//   - 探测结果决定入口可用性（前端按 item 置灰 + 显示原因），而不是"点进去撞空"；
//   - 只有探测有盲区时才需要事后补救（形状在、语义变），那种"熔断名单"在真的
//     见到坏组合时再加，不在这里预先为版本表态。
//
// 探测分两侧：
//   - launcher 侧（本文件）：地址 / token 能否从启动日志解析、本次是否挂载了插件覆盖层；
//   - plugin 侧：插件把结论写进 <实例目录>/.dsh-self-mcp/capabilities.json
//     （见插件 writeCapabilityReport），本文件负责读回来合并。
//
// 报告是双方约定的契约文件，不依赖 DSH 的任何私有格式 —— 这就是把耦合点从"别人的
// 内部实现"挪到"我们自己的边界"上的做法。
const (
	capsStateDir = ".dsh-self-mcp"
	capsFile     = "capabilities.json"
)

// pluginCapabilityReport mirrors the JSON the plugin writes at apply() time.
type pluginCapabilityReport struct {
	Schema        int                `json:"schema"`
	Plugin        string             `json:"plugin"`
	PluginVersion string             `json:"pluginVersion"`
	PID           int                `json:"pid"`
	ReportedAt    string             `json:"reportedAt"`
	Launcher      bool               `json:"launcher"`
	InstanceID    string             `json:"instanceId"`
	Capabilities  []pluginCapability `json:"capabilities"`
}

type pluginCapability struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
}

// CapabilityItem is one row of the compatibility panel. IDs are stable contract
// keys — the frontend gates features by looking up an ID (e.g. "embedRelax"),
// so renaming one is a breaking change.
type CapabilityItem struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"` // 证据：解析到的地址 / 版本 / 结论
	Reason string `json:"reason"` // ok=false 时的人话原因（来自插件探测）
	Source string `json:"source"` // "launcher" | "plugin"
	Hint   string `json:"hint"`   // 这一项坏掉意味着什么（给用户看的后果说明）
}

// CapabilityReport is what GetCapabilities returns for one instance.
type CapabilityReport struct {
	InstanceID   string           `json:"instanceId"`
	InstanceName string           `json:"instanceName"`
	Version      string           `json:"version"`
	Status       string           `json:"status"`
	Plugin       string           `json:"plugin"`      // 插件名@版本；未报告时为空
	PluginAt     string           `json:"pluginAt"`    // 插件报告时间（RFC3339）
	Stale        bool             `json:"stale"`       // 报告来自上一个进程（pid 不匹配）
	Items        []CapabilityItem `json:"items"`
}

// pluginCapabilityMeta 是插件上报项的展示文案。顺序即面板顺序（用 pluginCapOrder）。
var pluginCapabilityMeta = map[string]struct{ label, hint string }{
	"pluginLoaded": {"插件已装载（dsh-self-mcp）", "没装载的话，下面所有插件能力都不存在"},
	"restartTool":  {"dsh-restart 工具已注册", "注册失败时自管理重启用不了"},
	"embedRelax":   {"DSH 会话校验已放宽（内置浏览器前置）", "失败时内嵌会 401 / 一直「自动重连中」"},
	"restartDelivery": {
		"重启完成走 plugin/notice 通道",
		"回退到 prompt() 时，重启完成消息会显示成用户气泡",
	},
}

// pluginCapOrder 固定插件能力在面板里的顺序：不看 DSH 版本，只看探测结论。
var pluginCapOrder = []string{"pluginLoaded", "restartTool", "embedRelax", "restartDelivery"}

func capabilitiesPath(dir string) string {
	return filepath.Join(dir, capsStateDir, capsFile)
}

// cleanupCapabilities 删除上一次运行留下的能力报告。
// 报告必须以"本次启动"为准：上一个进程装载过插件、这次没装载时，陈旧文件会让
// 面板谎报"一切正常"—— 这正是我们最想避免的静默失效，所以每次启动先清掉。
func cleanupCapabilities(dir string) {
	if dir == "" {
		return
	}
	_ = os.Remove(capabilitiesPath(dir))
}

// readPluginCapabilities 读回插件的能力报告。ok=false 表示"本次运行没有报告"
// （未装载 / 未启用自管理重启 / DSH 侧加载失败），三者的区分交给调用方用门控信息补。
func readPluginCapabilities(dir string) (pluginCapabilityReport, bool) {
	var report pluginCapabilityReport
	if dir == "" {
		return report, false
	}
	data, err := os.ReadFile(capabilitiesPath(dir))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return report, false
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return report, false
	}
	return report, true
}

// stripURLQuery 去掉 URL 的 query。启动日志里的地址可能带 launch token
// （`?token=...`），面板只展示"哪台机器哪个端口"，不把凭据带到界面上。
func stripURLQuery(raw string) string {
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		return raw[:i]
	}
	return raw
}

// GetCapabilities 汇总一台实例上各项能力"到底能不能用"，供前端置灰入口 +
// 右栏「兼容性」面板展示。绑定为 window.go.main.App.GetCapabilities。
//
// 只做本地读取（文件 + 内存里已捕获的进程输出），不发网络请求：面板会被反复打开，
// 服务可达性另有 ProbeServices / header 的「DSH 已就绪」负责。
func (a *App) GetCapabilities(instanceID string) CapabilityReport {
	inst := a.store.find(instanceID)
	if inst == nil {
		return CapabilityReport{InstanceID: instanceID}
	}

	a.mu.Lock()
	mp := a.processes[instanceID]
	a.mu.Unlock()

	version := strings.TrimSpace(inst.LocalVersion)
	if version == "" {
		version = versionLabel(inst.Version)
	}
	report := CapabilityReport{
		InstanceID:   inst.ID,
		InstanceName: inst.Name,
		Version:      version,
		Status:       inst.Status,
	}

	// ---- launcher 侧探测 ----
	report.Items = append(report.Items, a.launcherCapabilities(inst, mp)...)

	// ---- 插件侧探测（读报告文件）----
	pluginReport, hasPluginReport := readPluginCapabilities(inst.Directory)
	switch {
	case !hasPluginReport:
		report.Items = append(report.Items, CapabilityItem{
			ID:     "pluginLoaded",
			Label:  pluginCapabilityMeta["pluginLoaded"].label,
			OK:     false,
			Detail: "本次运行没有收到插件的能力报告",
			Reason: pluginReportAbsentReason(inst),
			Source: "plugin",
			Hint:   pluginCapabilityMeta["pluginLoaded"].hint,
		})
	default:
		report.Plugin = pluginReport.Plugin
		if pluginReport.PluginVersion != "" {
			report.Plugin += "@" + pluginReport.PluginVersion
		}
		report.PluginAt = pluginReport.ReportedAt
		// 报告来自上一个进程（pid 对不上）→ 标出来但仍展示，别让面板沉默。
		if mp != nil && pluginReport.PID != 0 && pluginReport.PID != mp.pid {
			report.Stale = true
		}
		report.Items = append(report.Items, pluginCapabilityItems(pluginReport)...)
	}

	return report
}

// launcherCapabilities 是启动器自己就能判定的几项：这些是不依赖插件、也不依赖
// DSH 私有接口的耦合点，坏掉通常是"启动日志格式变了"或"门控没通过"。
func (a *App) launcherCapabilities(inst *Instance, mp *managedProcess) []CapabilityItem {
	items := make([]CapabilityItem, 0, 4)

	// ① 从启动日志里解析出 DSH 地址（打开 / 复制 按钮用）
	var candidates []string
	var authURL string
	if mp != nil {
		mp.urlMu.Lock()
		candidates = append(candidates, mp.candidates...)
		authURL = mp.authURL
		mp.urlMu.Unlock()
	}
	webDetail := "尚未从启动日志解析到地址"
	if len(candidates) > 0 {
		webDetail = fmt.Sprintf("已解析到 %s（共 %d 个）", stripURLQuery(candidates[len(candidates)-1]), len(candidates))
	} else if mp == nil {
		webDetail = "该实例当前没有启动器管理的进程"
	}
	items = append(items, CapabilityItem{
		ID:     "webUrl",
		Label:  "能从启动日志解析出 DSH 地址",
		OK:     len(candidates) > 0,
		Detail: webDetail,
		Source: "launcher",
		Hint:   "解析不到时「打开 DSH」没有地址可用（该实例可能不是启动器拉起的）",
	})

	// ② 解析出带 token 的地址（内嵌模式必需；DSH 每次启动都换 token）
	authDetail := "尚未在启动日志里看到带 token 的地址"
	if authURL != "" {
		authDetail = "已解析到 " + stripURLQuery(authURL)
	}
	items = append(items, CapabilityItem{
		ID:     "authToken",
		Label:  "能从启动日志解析出带 token 的地址",
		OK:     authURL != "",
		Detail: authDetail,
		Source: "launcher",
		Hint:   "内嵌视图需要它；解析不到时内嵌会停在「正在从实例启动日志解析 DSH 地址…」",
	})

	// ③ 本次启动是否挂了自管理重启覆盖层（双门控：实例勾选 + 全局已装插件）
	mounted := selfRestartEnabled(*inst)
	items = append(items, CapabilityItem{
		ID:     "selfRestartOverlay",
		Label:  "本次启动已挂载自管理重启插件",
		OK:     mounted,
		Detail: selfRestartGateDetail(*inst, mounted),
		Source: "launcher",
		Hint:   "没挂载时 dsh-restart 工具不存在，插件能力也无从报告",
	})

	return items
}

// selfRestartGateDetail 说明双门控卡在哪一道，比一句"未启用"有用。
func selfRestartGateDetail(inst Instance, mounted bool) string {
	if mounted {
		return "实例已勾选「自管理重启」且全局已安装 " + selfRestartPluginName
	}
	if !inst.SelfRestart {
		return "实例未勾选「自管理重启」"
	}
	return "全局未安装插件 " + selfRestartPluginName
}

// pluginReportAbsentReason 解释"为什么没收到插件报告"——这正是过去静默失效的地方：
// 插件探测到接口变了只写 logger，界面上只表现为内嵌一直转圈。
func pluginReportAbsentReason(inst *Instance) string {
	if !inst.SelfRestart {
		return "实例未勾选「自管理重启」，本次启动没有挂载插件"
	}
	installed, err := readInstalledPlugins()
	if err != nil {
		return "读取全局插件清单失败，无法确认插件是否已安装"
	}
	if _, ok := installed[selfRestartPluginName]; !ok {
		return "全局未安装插件 " + selfRestartPluginName
	}
	return "覆盖层已挂载但插件没有写出报告 —— DSH 侧可能加载失败（插件 API 变了？）"
}

// pluginCapabilityItems 把插件的探测结论转成面板行：保持固定顺序，未知 id 排在末尾。
func pluginCapabilityItems(report pluginCapabilityReport) []CapabilityItem {
	byID := make(map[string]pluginCapability, len(report.Capabilities))
	for _, c := range report.Capabilities {
		byID[c.ID] = c
	}
	items := make([]CapabilityItem, 0, len(report.Capabilities)+len(pluginCapOrder))
	seen := make(map[string]bool, len(pluginCapOrder))
	for _, id := range pluginCapOrder {
		c, ok := byID[id]
		if !ok {
			continue
		}
		seen[id] = true
		items = append(items, pluginCapabilityItem(c))
	}
	for _, c := range report.Capabilities {
		if seen[c.ID] {
			continue
		}
		items = append(items, pluginCapabilityItem(c))
	}
	return items
}

func pluginCapabilityItem(c pluginCapability) CapabilityItem {
	meta, known := pluginCapabilityMeta[c.ID]
	label := meta.label
	if !known {
		label = c.ID
	}
	return CapabilityItem{
		ID:     c.ID,
		Label:  label,
		OK:     c.OK,
		Reason: c.Reason,
		Source: "plugin",
		Hint:   meta.hint,
	}
}

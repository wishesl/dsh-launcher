package main

import (
	"fmt"
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
//   - plugin 侧：插件连上桥接后发第一帧 hello 做全量握手（能力 + 主题快照），
//     存在 launcher 内存里，本文件读回合并。握手即"本次启动"：启动即清（见
//     clearHandshake），陈旧判定整个消失，也没有任何报告文件可残留。

// pluginCapabilityReport mirrors the plugin's hello snapshot (first frame on the bridge).
type pluginCapabilityReport struct {
	Schema        int                `json:"schema"`
	Plugin        string             `json:"plugin"`
	PluginVersion string             `json:"pluginVersion"`
	PID           int                `json:"pid"`
	LaunchID      string             `json:"launchId"`
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
	ID    string `json:"id"`
	Label string `json:"label"`
	OK    bool   `json:"ok"`
	// Unknown 表示"没有结论 / 不适用"，必须与"失败"分开：面板一旦误报就会失去可信度
	// （用户看到明明能用的东西在报红，就会学会无视整个面板）。前端只对 !ok && !unknown
	// 的行标红，unknown 只作中性展示。
	Unknown bool   `json:"unknown"`
	Detail  string `json:"detail"` // 证据：解析到的地址 / 版本 / 结论
	Reason  string `json:"reason"` // ok=false 时的人话原因（来自插件探测或启动器推断）
	Source  string `json:"source"` // "launcher" | "plugin"
	Hint    string `json:"hint"`   // 这一项坏掉意味着什么（给用户看的后果说明）
}

// CapabilityReport is what GetCapabilities returns for one instance.
type CapabilityReport struct {
	InstanceID   string           `json:"instanceId"`
	InstanceName string           `json:"instanceName"`
	Version      string           `json:"version"`
	Status       string           `json:"status"`
	Plugin       string           `json:"plugin"`   // 插件名@版本；未报告时为空
	PluginAt     string           `json:"pluginAt"` // 插件报告时间（RFC3339）
	Stale        bool             `json:"stale"`    // 握手的 launchId 与当前进程不符
	Items        []CapabilityItem `json:"items"`
}

// pluginCapabilityMeta 是插件上报项的展示文案。顺序即面板顺序（用 pluginCapOrder）。
var pluginCapabilityMeta = map[string]struct{ label, hint string }{
	"pluginLoaded": {"插件已装载（dsh-launcher-plugin）", "没装载的话，下面所有插件能力都不存在"},
	"restartTool":  {"dsh-restart 工具已注册", "注册失败时自管理重启用不了"},
	"themeReport":  {"主题同步已上报（ui-theme）", "失败时启动器主题不跟随 DSH 切换"},
	"themeSet":     {"启动器可写 DSH 主题（ui-theme）", "失败时在启动器里切主题改不到 DSH，只在本机生效"},
	"embedRelax":   {"DSH 会话校验已放宽（内置浏览器前置）", "失败时内嵌会 401 / 一直「自动重连中」"},
	"restartDelivery": {
		"重启完成走 plugin/notice 通道",
		"回退到 prompt() 时，重启完成消息会显示成用户气泡",
	},
	"sessionNotify": {
		"会话通知已上报（回答完成 / AI 提问）",
		"失败时启动器收不到通知；旧版插件不推帧（重装内置插件即可）",
	},
}

// pluginCapOrder 固定插件能力在面板里的顺序：不看 DSH 版本，只看探测结论。
var pluginCapOrder = []string{"pluginLoaded", "restartTool", "themeReport", "themeSet", "embedRelax", "restartDelivery", "sessionNotify"}

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

	// ---- 插件侧探测（bridge 握手：hello 帧的全量快照）----
	pluginReport, hasPluginReport := a.bridge.handshake(instanceID)
	switch {
	case !hasPluginReport:
		reason, unknown := a.pluginReportAbsentReason(inst)
		report.Items = append(report.Items, CapabilityItem{
			ID:      "pluginLoaded",
			Label:   pluginCapabilityMeta["pluginLoaded"].label,
			OK:      false,
			Unknown: unknown,
			Detail:  "本次运行没有收到插件的能力报告",
			Reason:  reason,
			Source:  "plugin",
			Hint:    pluginCapabilityMeta["pluginLoaded"].hint,
		})
	default:
		report.Plugin = pluginReport.Plugin
		if pluginReport.PluginVersion != "" {
			report.Plugin += "@" + pluginReport.PluginVersion
		}
		report.PluginAt = pluginReport.ReportedAt
		report.Stale = stalePluginReport(mpLaunchID(mp), pluginReport.LaunchID)
		report.Items = append(report.Items, pluginCapabilityItems(pluginReport)...)
	}

	return report
}

// mpLaunchID 取当前托管进程的启动凭据（没有进程时为空，表示"无从比较"）。
func mpLaunchID(mp *managedProcess) string {
	if mp == nil {
		return ""
	}
	return mp.launchID
}

// stalePluginReport 判断插件写的能力报告是不是"上一次启动"留下的。
//
// 用启动器自己发的一次性 launch id 比，**不要比 pid**：启动器手里是 `cmd /c` 外壳的
// pid，插件报的是 node 进程的 pid（中间隔着 cmd → npx → node），按构造就不会相等 ——
// 拿 pid 当判据会 100% 误报，用户会看到"功能一切正常却提示报告已过期"。
//
// 任一侧拿不到凭据（旧版插件不写这个字段）就不下结论：启动实例前已经删过报告文件，
// "文件在"本身就是很强的证据。宁可不说，也不要谎报。
func stalePluginReport(currentLaunchID, reportedLaunchID string) bool {
	if currentLaunchID == "" || reportedLaunchID == "" {
		return false
	}
	return currentLaunchID != reportedLaunchID
}

// launcherCapabilities 是启动器自己就能判定的几项：这些是不依赖插件、也不依赖
// DSH 私有接口的耦合点，坏掉通常是"启动日志格式变了"或"门控没通过"。
func (a *App) launcherCapabilities(inst *Instance, mp *managedProcess) []CapabilityItem {
	items := make([]CapabilityItem, 0, 5)

	// ① 启动器能否确定这台 DSH 的访问地址（「打开 DSH」用的就是它）。
	//
	// ⚠️ 不要读 managedProcess.candidates：那个列表被 takeWebCandidates() 抽干
	// （probeReady 消费一次就置 nil），实例一旦 ready 就永远是空的 —— 用它判断会
	// 出现"卡片上明明显示着地址、面板却说解析不到"的自相矛盾红灯。
	// 权威来源是实例上的运行时地址（probeReady 探通后写回），其次按配置端口推导。
	serviceURL, determinable := instanceServiceURL(inst)
	webDetail := serviceURL + "（按实例配置的 --port 推导）"
	if strings.TrimSpace(inst.WebUrl) != "" {
		webDetail = stripURLQuery(inst.WebUrl) + "（从进程输出捕获）"
	}
	if !determinable {
		webDetail = "无法确定：--port 0 由系统分配端口，但进程输出里还没出现地址"
	}
	items = append(items, CapabilityItem{
		ID:     "webUrl",
		Label:  "已知这台 DSH 的访问地址",
		OK:     determinable,
		Detail: webDetail,
		Source: "launcher",
		Hint:   "地址定不下来时「打开 DSH」没有可用链接（--port 0 的实例尤其容易）",
	})

	// ② 解析出带 token 的地址（内嵌模式必需；DSH 每次启动都换 token）
	authURL := ""
	if mp != nil {
		authURL = mp.authWebURL()
	}
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

	// ③ 本次启动是否挂了桥接插件覆盖层（门控：全局已装插件 + 桥接就绪）。
	//    没挂载不是"故障"（没装插件的 profile 本来就不挂），所以是 unknown 而不是红灯。
	mounted, _, detail := a.launcherPluginGate()
	items = append(items, CapabilityItem{
		ID:      "selfRestartOverlay",
		Label:   "本次启动已挂载桥接插件",
		OK:      mounted,
		Unknown: !mounted,
		Detail:  detail,
		Source:  "launcher",
		Hint:    "没挂载时主题同步 / dsh-restart 都不存在，插件能力也无从报告",
	})

	// ④ 网页端即时通道：DSH 页面里的客户端半边（dsh-launcher-plugin/lib/client.js）连上后，
	//    点启动器的主题按钮页面会当场换肤，写盘放到后台（省掉实测 330–350ms）。
	//    没接入不是故障（页面可能根本没打开，或 DSH 还没重启加载客户端入口）→ unknown，
	//    因为主题照样同步，只是走服务端写入那条慢路。
	pageReady := a.bridge != nil && a.bridge.hasPage(inst.ID)
	pageDetail := "未接入：主题仍会同步，只是要等 DSH 自己把配置写一遍（实测约 300ms）"
	if pageReady {
		pageDetail = "已接入：主题点击即时生效（页面当场换肤，写盘在后台进行）"
	}
	items = append(items, CapabilityItem{
		ID:      "pageChannel",
		Label:   "网页端已接入主题即时通道",
		OK:      pageReady,
		Unknown: !pageReady,
		Detail:  pageDetail,
		Source:  "launcher",
		Hint:    "接入需要插件 0.2.6+ 且 DSH 重启过一次（客户端入口在启动时装载）",
	})

	return items
}

// pluginReportAbsentReason 分诊"为什么没有握手"。
//
// 不能只有一句"覆盖层已挂载但没有握手 —— DSH 侧可能加载失败"，但最常见的原因其实是
// **已安装的插件副本是旧版**（不支持握手）。把这种情况报成"DSH 接口变了"就是误诊 ——
// 面板一旦误报，用户就会学会无视它。所以返回 (原因, unknown)：
// unknown=true 表示"已知的良性原因 / 不适用"，前端只作中性展示，不标红。
func (a *App) pluginReportAbsentReason(inst *Instance) (string, bool) {
	mounted, installed, detail := a.launcherPluginGate()
	if !installed {
		return "全局未安装插件 " + selfRestartPluginName + "（主题同步 / dsh-restart / 内嵌支持都不可用）", true
	}
	if !mounted {
		return "本次启动未挂载插件（" + detail + "）", true
	}
	// 已装副本 vs 启动器内置：不一致 = 装的是旧副本（不支持握手），重新安装即可。
	// 注意插件版本号必须随能力变更一起 bump，否则新旧副本版本号相同、这里分辨不出来。
	if reason, mismatch := pluginCopyMismatchReason(readInstalledVersion(selfRestartPluginName), embeddedBuiltinVersion()); mismatch {
		return reason, true
	}
	a.mu.Lock()
	_, running := a.processes[inst.ID]
	a.mu.Unlock()
	if !running {
		return "实例未运行，插件未装载（启动后会收到握手）", true
	}
	return "覆盖层已挂载但没有收到插件握手 —— DSH 侧可能加载失败（插件 API 变了？）", false
}

// pluginCopyMismatchReason 比对"已安装的插件副本"与"启动器内置的副本"的版本号。
// 版本不一致时给出可操作的结论（重新安装内置插件），而不是把常见原因误诊成"DSH 接口变了"。
// 任一版本号读不出来（没装 / 读不到 package.json）时不下结论，交给调用方走缺省分支。
func pluginCopyMismatchReason(installed, bundled string) (string, bool) {
	if installed == "" || bundled == "" || installed == bundled {
		return "", false
	}
	return fmt.Sprintf(
		"已安装的插件副本与启动器内置的不一致（已装 v%s / 内置 v%s）—— 重新安装内置插件后才会上报能力",
		installed, bundled,
	), true
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

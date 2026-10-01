# 交接文档：dsh-launcher-plugin 主题同步 + 重启单线方案实施

> 写于实施中途，供后续模型/开发者继续。**动手前请先重读权威方案：`doc/dsh-launcher-plugin实现方案.md`**，
> 本文只记录"已做了什么、卡在哪、还剩什么"，不重复方案细节。

---

## 1. 任务背景

- 用户请求演进：最初要"改造 dsh-restart 插件，DSH 实例状态变化时同步启动器主题"（m00002），
  经多轮讨论收敛为（m00120–m00123）：**新建单一线 `dsh-launcher-plugin` 插件**，同时承载
  ①主题同步 ②dsh-restart ③能力握手，走 launcher 内置 loopback HTTP 桥接，**彻底淘汰文件通道**
  （`.dsh-self-mcp/*.json` 不再读写）。
- 用户拍板：「那动手实现吧」（m00141）；「降级矩阵就不要了，初版设计尽量简洁明了」（m00123）。
- 工作目录：`E:\gopackage2\2026-8\dsh-start`，主体在 `dsh-launcher\`（Go + Wails v2）。
- AGENTS.md 纪律（必须遵守）：三栏布局、回调 useCallback、Wails 事件单一所有者在 App.tsx、
  **完成后必须 `wails build`**、兼容性按能力探测不按版本号、fail-open 不误报、**禁比 pid**
  （用 launchId）、插件能力变更必须 bump 插件 package.json 版本（副本比对分诊依赖它）。

## 2. 架构一句话

launcher 起 `127.0.0.1` 临时端口 HTTP 服务（随机 Bearer token），经 env
`DSH_LAUNCHER_EVENTS` / `DSH_LAUNCHER_TOKEN` / `DSH_INSTANCE_ID` / `DSH_LAUNCH_ID`
注入给插件；插件启动 `POST /connect` 握手（=能力报告全量 + 主题快照），
主题变化 `POST /theme` 增量推送 → launcher `EmitEvent("dsh:theme")` → 前端换肤；
`dsh-restart` 执行 `POST /restart` **拿到 ack 才退出**；重启续跑负载走 `GET /pending`
（插件侧幂等消费，`POST /pending-consumed` 确认删除）。5 接口定义见方案文档第 3 节。
信封格式 `{type, instanceId, launchId, payload}`。

## 3. 已完成（全部落盘，`git status` 可核对）

### 3.1 新插件（untracked 新目录）`dsh-launcher/embed/dsh-launcher-plugin/`

- `package.json`：name `dsh-launcher-plugin`、**version 0.1.0**（能力变更必须 bump）、type module、
  main `lib/index.js`、dep `@deepseek-ai/schemastery ^3.18.1`。
- `lib/index.js`（完整移植自旧 `embed/dsh-self-mcp/lib/index.js` 蓝本，改文件线为 HTTP 线）：
  - `inject=["tools","timer"]`，`CONFIRM_WORD="restart-dsh"`，`RETRY_DELAYS_MS=[500,1500,4000,10000,20000,40000]`。
  - `supervised = DSH_LAUNCHER_EVENTS && DSH_LAUNCHER_TOKEN` 均非空；非 supervised 静默降级（不注册工具）。
  - 桥接层：`envelope()`、`bridgeCall(method,path,payload,query)`（Bearer 头、非 2xx 抛错）、
    `syncSnapshot()`（POST /connect）、`maintain()`（快照成功→online + `deliverPendingOnce()`；
    失败→退避重连，封顶 `RETRY_DELAYS_MS` 末位**永续**）、`pushTheme(pref)`。
  - 主题：`readThemePreference(settings)` 用 `settings.describe()` 找 `ns==='ui-theme'`，
    `preference ∈ {light,dark,system}` 否则 `system`，无条目返回 `null` 不下发；
    `watchTheme()` = 初读 + `settingsCtx.on("settings/document-updated", …)` + 1s `ctxRef.timeout` 轮询兜底。
  - 交付链（重启完成回注）：`restartCompleteText` / `boundSummary`（NOTICE_SUMMARY="DSH 已重启完成，继续执行"）/
    `createPluginNotice`（source `{kind:"plugin:dsh-launcher-plugin", form:"notice", …}`）/
    `deliverAsPluginNotice`（controller.resolveAgent → agent.followup）/
    `deliverRestartComplete`（回退 `controller.prompt` 要求 `result.accepted===true`）/
    `scheduleDelivery`（成功→`pendingRestart=null` + `POST /pending-consumed`；失败退避，用尽→`restartDelivery` 能力置 false）。
  - embed 移植：`relaxEmbedAuth`（patch `authorizeIndex`/`requestRejection`）、
    `EMBED_ALLOWED_ORIGINS={"http://wails.localhost"}`、等长 + `timingSafeEqual` 校验 token、
    幂等标记 `__dshLauncherPluginEmbedPatched`。
  - `dsh-restart` 工具护栏：①confirm ②exec.agent/session 存在 ③`header.origin==="subagent"` 或
    `delegationDepth>0` 拒绝 ④`pendingRestart!==null` 拒绝重复 ⑤非 supervised 拒绝；
    **`POST /restart` 拿到 ack 才 500ms 后 `ctx.get("appExit")(0)`，失败返回 rejected 不退出**。
  - 能力：`pluginLoaded` / `restartTool` / `themeReport` / `embedRelax` / `restartDelivery`，
    变化即 `scheduleSync(150ms 防抖)` 重发握手。
- `README.md`：架构、门控、5 接口、开发须知（bump 版本 / go test / wails build）。

### 3.2 launcher Go 侧（modified）

| 文件 | 改动 |
|---|---|
| `launcher_http.go`（**新增**） | `launcherBridge`：`handshakes/restarts/pending` 三 map + url/token；`newLauncherBridge` 恒初始化三 map（**防 nil map panic，测试踩过**），rand 24 字节 hex token，rand 失败→token=""→auth 恒 401（fail-open）；`start()` listen `127.0.0.1:0` 注册 5 路由全过 `auth`（等长+`subtle.ConstantTimeCompare`）；`stop()` 关服清 url。`handleConnect` 幂等替换握手 + theme 非空即 `emitTheme`；`handleTheme` → `emitTheme`（非法 preference 归一 `system`）→ `b.app.emit("dsh:theme", themeEvent{InstanceID,Preference})`；`handleRestart` 校验 `mp.launchID==env.LaunchID` 否则 409，通过→记 restarts+pending+logs.note+ack；`handlePending` GET **不消费**返回 `{"pending":raw|nil}`；`handlePendingConsumed` delete；`consumeRestart(id,launchID)` 取即清且 launchID 必须匹配（防标志残留误重启）；`handshake/clearHandshake` nil-safe。 |
| `self_restart.go`（重写） | 常量 `selfRestartPluginName="dsh-launcher-plugin"`、`legacySelfRestartPluginName="dsh-self-mcp"`；**删除**文件通道（stateDir/requestFile/restartRequestPath/consumeRestartRequest）；门控 `launcherPluginGate() (mounted, installed bool, detail)` = 已装新插件 && `bridge!=nil && bridge.url!=""`；overlay 内容 `- insert:` + `- id: launcher-plugin` + `name: 'dsh-launcher-plugin'`。 |
| `dsh_process.go` | ①启动处 `mountPlugin, pluginInstalled, mountDetail := a.launcherPluginGate()`，仅 `!mounted && installed` 时 systemLog 提示；`a.bridge.clearHandshake(snapshot.ID)` 取代旧 cleanupCapabilities；②overlay 分支 `if mountPlugin`；③env 注入追加 `DSH_LAUNCHER_EVENTS`/`DSH_LAUNCHER_TOKEN`（**注释明确禁改 pid**）；④exit-reconcile 先无条件 `gotRestart := a.bridge.consumeRestart(snapshot.ID, mp.launchID)` 再 `wantRestart := !crashed && !mp.stopRequested() && gotRestart`。 |
| `capabilities.go` | 删 capsStateDir/capsFile/capabilitiesPath/cleanupCapabilities/readPluginCapabilities/selfRestartGateDetail（imports 减为 fmt/strings）；meta：pluginLoaded 文案改新插件名，新增 `"themeReport": {"主题同步已上报（ui-theme）", "失败时启动器主题不跟随 DSH 切换"}`；`pluginCapOrder=[pluginLoaded,restartTool,themeReport,embedRelax,restartDelivery]`；`GetCapabilities` 改 `a.bridge.handshake(instanceID)`；absent 分诊 `pluginReportAbsentReason` 五链：①未装→unknown ②未挂载→unknown ③副本不一致→unknown ④实例未运行→unknown ⑤运行中无握手→**红**（"覆盖层已挂载但没有收到插件握手 —— DSH 侧可能加载失败"）。 |
| `app.go` | struct 加 `bridge *launcherBridge`；NewApp 里 `a.bridge = newLauncherBridge(a)`（保证非 nil）；startup 在 startTray 后 `a.bridge.start()` + `go a.migrateLegacySelfRestart()`；shutdown 在 reap 循环后 `a.bridge.stop()`（**进程先停、桥接后关**）。 |
| `self_restart_install.go` | builtin 路径改 `.dsh-builtin/dsh-launcher-plugin`、embedded 根改 `embed/dsh-launcher-plugin`；新增 `migrateLegacySelfRestart()`：已装新→return / 没装旧→return；否则 12 次×15s 重试：marketBusy CAS → 选**停机**实例 → preflightMarketOp → installBundledSelfRestart → `pluginCommand remove dsh-self-mcp` 尽力卸旧 → logs.note+systemLog。 |
| `embeddata.go` | `//go:embed embed/dsh-launcher-plugin`（注释：能力变更必须 bump 插件版本）。 |

### 3.3 测试（modified，`go vet ./...` 已干净）

- `self_restart_test.go`：fixture `newSelfRestartTestApp(t)`（**必须含 `processes: make(map[string]*managedProcess)`**，
  否则 capabilities 测试 nil map panic）；`TestLauncherPluginGate`、`TestExtractEmbeddedSelfRestart`、
  `TestWriteSelfRestartOverlay`、`TestBridgeRestartFlag`（stale launchId 取走不算数+清残留）、
  `TestBridgeHandshakeLifecycle`（含 nil bridge 双方法安全）。
- `capabilities_test.go`：删 3 个旧测试；保留 stripURLQuery/pluginCapabilityItems（**注意 wantOrder 尚未加
  themeReport，需按新 pluginCapOrder 核对**）/stale/mpLaunchID/newLaunchID/pluginCopyMismatch；
  新增 `TestPluginReportAbsentReason` 四分支（bridge down→未挂载 unknown / bridge up 未运行→unknown /
  processes 有该实例→红 / 删 package.json→未装 unknown）。
- `market_update_test.go`：6 处旧名改（line ~108 file: 样例、CheckPluginUpdates fixture deps 键、
  byName 断言、TestUpdatePluginRejects fixture、注释、`builtinPath := filepath.Join(selfRestartBuiltinRel, "package.json")`）。

## 4. 当前卡点：`TestShutdownKillsRunningInstance` 失败（**非本轮回归，已证实**）

- 现象：`dsh_shutdown_test.go:147 process not observed running before shutdown` +
  TempDir 清理报 `t1.log: being used by another process`。全量 `go test ./...` 只剩这一个失败。
- **已做对照实验**：把 HEAD 原样（9 个 modified 文件全部还原 HEAD、剔除 launcher_http.go）拷到
  `C:\Users\Tony\AppData\Local\Temp\dsh-r4trUF\baseline-dsh` 跑同一测试 → **同样失败**（5.25s），
  `Select-String launcherPluginGate` 返回 False 确认是纯净 HEAD。⇒ **环境既有问题，不是改动引入**。
- 初步原因线索（未完全定论）：沙箱里 `cmd /c ping -n 3 -l 43210 127.0.0.1` 输出
  `PING: transmit failed. General failure.`（网络受限），但 ping 进程本身起来过（`Get-Process ping` 有）。
  测试用 `powershell Get-CimInstance Win32_Process` 查 `CommandLine -like '*marker*'`（marker=1000+随机数），
  3s 内没查到 → 可能 ping 因 transmit fast-fail 秒退、或 CIM 查询在沙箱里慢/被限。
  同文件另一个测试 `TestShutdownWaitsForInFlightLaunchAndKills` 能过（它在 ping 死后判 0 即通过）。
- **给后续者的建议（按顺序尝试）**：
  1. 在沙箱外（或用户普通终端）跑 `go test -run TestShutdownKillsRunningInstance -count=1 .`（workdir=dsh-launcher，
     **必须先 `$env:GOCACHE='E:\gopackage2\2026-8\dsh-start\.gocache'`**，默认 GOCACHE 目录 Access denied）确认非沙箱环境是否通过。
  2. 若仅沙箱失败：属环境限制，可 `t.Skip`（加 `if os.Getenv("DSH_SANDBOX") != ""` 之类判断需先确认环境变量）
     或在交接报告注明"该测试在受限环境既有失败，与改动无关"，**不要为过测试去改断言逻辑**。
  3. 若非沙箱也失败：继续查 ping 生命周期（把 waitPingAlive 3s 拉长 / 换 `tasklist` 查 / 打印 pingAlive 返回值）。

## 5. 尚未完成（按顺序）

1. **前端换肤（todo #7，完全未动）** —— `git status` 里无任何 frontend 改动：
   - `frontend/src/api.ts`：按现有 onX/offX 包装加 `onTheme/offTheme`（EventsOn("dsh:theme")）。
   - `frontend/src/App.tsx`：在 406-502 的统一事件 useEffect 里订阅，回调写
     `document.documentElement.dataset.theme = preference`（`light/dark` 直设，`system` 设 `"system"` 交给 CSS 解析），
     cleanup offX；同时按方案调 `WindowSetSystemDefaultTheme/Light/Dark`（`wailsjs/runtime/runtime.js:76-85`）。
   - `frontend/src/style.css`：`:root` 只有浅色 token（6-48 行，--bg/--panel/--text/--accent 系）；加
     `[data-theme="dark"]` 覆盖段 **+ `@media (prefers-color-scheme: dark)` 内 `[data-theme="system"]` 同款段**
     （两份一样，含 `color-scheme: dark`）。
   - 清理旧门控痕迹：`util.ts:126 capsAlert` 里 `if(!inst.selfRestart) return false;` 删除（能力面板现在 fail-open）；
     `InstanceForm.tsx` 526-546 第二块 selfRestart 勾选删除；`InstanceCard.tsx:135` self-restart 徽标删除；
     `MarketView.tsx` 旧插件名文案/updatesByName 可改为新插件名（迁移后市场显示）。
   - `wailsjs/go/models.ts` 的 `selfRestart` 字段**保留**（Instance JSON 字段还在）。
2. **全量验证（todo #9/#10）**：`$env:GOCACHE=...; go vet ./...`（已过）、`go test ./...`（除第 4 节既有失败）、
   **`wails build`（AGENTS.md 硬性要求，未跑）**。
3. **旧目录删除**：`dsh-launcher\embed\dsh-self-mcp\` 还在（shell `Remove-Item`/`New-Item` 均 Access denied，
   escalate workspace-write 也拒；**fs 的 write/edit 工具对该区域可用**，可尝试用 fs 后端删，或留报告让用户手删）。
   它已不被 `go:embed` 引用，留着不影响构建。
4. **README/AGENTS/doc 旧名残留**：`README/AGENTS.md:182/doc/插件更新功能实现方案.md` 提旧插件名，最后统一改。
5. `pluginCapabilityItems` 顺序断言（capabilities_test.go `wantOrder`）需确认含 themeReport ——
   实现的 order 是 5 项，若测试 wantOrder 是 4 项会挂，跑测试时留意。

## 6. 环境坑（后续模型必读）

- **GOCACHE**：每个 go 命令前 `$env:GOCACHE='E:\gopackage2\2026-8\dsh-start\.gocache'`，否则 Access denied。
- **git 写被沙箱拒**：`git stash` 报 `index.lock Permission denied`（只读 git status/diff/show 可用）。
- **pwsh workdir 陷阱**：`$env:TEMP` 每个 pwsh 进程不同（形如 `...\Temp\dsh-XXXX\`），跨调用引用会找不到；
  另外 workdir 指向不存在目录时 harness 直接 `spawn node.exe ENOENT`。
- **shell 删文件受限**：`embed\dsh-self-mcp` 目录 shell 新建/删除都拒（icacls 正常，是环境策略），fs 工具可用。
- 每次 go 命令需显式 `workdir=E:\gopackage2\2026-8\dsh-start\dsh-launcher`。

## 7. 关键事实速查

- emit 签名 `app.go:239 func (a *App) emit(event string, payload interface{})`；日志 `logs.note`=`logging.go:110`。
- 主题读取：插件端 `settings.describe()` 找 `ns==='ui-theme'`；事件 `settings/document-updated(ns, revision)`；
  DSH 端持久化在**共享 profile** 的 ui-theme 命名空间（⇒ 多实例全局一致，launcher 跟随最近一条即可）。
- `preference=system` 时插件照发 `"system"`，launcher 不解析，前端用 `@media (prefers-color-scheme)` 解析。
- 挂载门控 = **已装 + bridge.url 非空**（无实例级勾选；Instance.SelfRestart 字段与 wailsjs 保留但不再参与）。
- 旧插件仅由 launcher overlay 挂载 ⇒ 市场残留 `dsh-self-mcp` 不会造成 dsh-restart 重名冲突，迁移只是卫生。
- 前端事件订阅位置：`App.tsx` useEffect（406-502），**单一所有者**，别在别处直接 EventsOn。

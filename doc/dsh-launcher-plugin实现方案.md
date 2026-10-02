# dsh-launcher-plugin 实现方案（初版）

> **⚠️ 本文是初版设计记录 —— 以下内容勿当作现状读。权威契约以
> [`dsh-launcher/embed/dsh-launcher-plugin/README.md`](../dsh-launcher/embed/dsh-launcher-plugin/README.md) 的帧表为准。**
> 落地后的演进（插件当前版本 **0.2.11**）：
>
> 1. **0.2.0 · REST → 一条 WebSocket。** 「一条 loopback HTTP 线 + 5 个 REST 端点」收敛成**一条 loopback WebSocket**
>    （用户要求「只保留 ws，旧接口全搬过来，不做降级」）：`dsh-launcher/launcher_http.go` 已删除，改为
>    `dsh-launcher/launcher_bridge.go`（`GET /ws` 升级）。服务端插件仍带 `Authorization: Bearer <token>`；
>    网页端角色改用 `?token=` 查询参数（浏览器 WebSocket 不能带自定义头）；`CheckOrigin` 只放行无 `Origin` 的
>    Node 客户端、自源、以及本机已知 DSH 网页源。帧仍用同一个 `{type, instanceId, launchId, payload}` 信封：
>    插件→启动器 `hello`（原 `POST /connect` 全量快照）/`theme`/`restart`/`pending-consumed`/`command-result`；
>    启动器→插件 `pending`（原 `GET /pending`，改为服务端主动下发）/`set-theme`/`restart-result`。
>    心跳走 RFC ping/pong（20s ping、60s pong 判死）。
> 2. **0.2.5 · 主题写盘不再自读 CAS。** `settings.update` 内部本来就会跑两次 `describe()`，第三次读取只买到 CAS，
>    不划算（`mergeLayers` 的合并语义本来就保住别人改的其它字段）。
> 3. **0.2.6 · 多出「网页端角色」。** 同一条线上接入第二个角色（帧 `page-hello` / `page-set-theme` / `page-theme` /
>    `page-result`）：服务端半边用 `webserver/index-inject` 把
>    `window.__DSH_LAUNCHER_BRIDGE__ = {url, token, instanceId, launchId, …}` 注入页面，网页端半边 `lib/client.js`
>    直连启动器 —— 点启动器换主题时由**页面自己**乐观换肤（毫秒级重绘），配置写盘在后台跑；页面不在场时自动回退到
>    服务端写盘路径（约 300ms，fail-open）。声明 `dsh.client` + `exports["./client"]` 是 DSH 官方扩展点。
> 4. **0.2.7 · 双装载与定时器护栏。** `dsh.client` 声明会让宿主 Loader 重复装载服务端半边（同一 pid 两条「已装载」）；
>    先装载的那份 fiber 被拆掉后，`cordis-plugin-timer` 的 `ctx.timeout` 定时器**不会**随之取消 → 用失效 ctx 续排
>    轮询会抛 `cannot get required service "timer" in inactive context` 并把 DSH 进程打挂（2026-10-02 00:28 真机 exit 1）。
>    修法：定时器回调全部 try/catch + 自己持有 disposer 并随 fiber 销毁取消 + `apply()` 幂等（第二次装载先收掉旧会话）。
> 5. **0.2.8 · 续跑交付跨装载会话补交。** 「重启完成」消息常落在被拆掉的装载会话 #1 里而静默丢失（2026-10-02 01:27 真机）；
>    负载改为**进程级**、重试绑当时存活的会话、新会话装载时补做，交付链改走可见痕迹（`console.error` + `command-result` 帧）。
> 6. **0.2.9 / 0.2.10 · 交付等 `sessionController` 就绪 + 去掉主题逐次打点。** 前者不再把"服务还没装配好"当失败烧重试
>    （文案也只写确证的事实）；后者把 0.2.5 那批延迟诊断全部删掉 —— 每次点击/事件刷一行，实测把实例日志刷满。
> 7. **0.2.11 · DSH 设置里的「启动器」面板（纯客户端半边）。** 网页端半边多注册一个 `settings.section` 分区
>    （`id: dsh-launcher`，`order: 30`），展示**本页那条 WebSocket 通道自己的状态**（连通/连接中/离线重连/被拒/
>    未接入 + 桥接地址 + launch + 注入的插件版本 + 本页主题 + 重新连接按钮）与一份**静态**功能清单。
>    **没有新帧、没有 Go 改动**：服务端半边那条连接的在线状态与能力探测结论页面读不到（只在 node 进程与启动器内存里），
>    面板因此不显示它们、也不猜。注册走 `ctx.slots.inject("settings.section", …)`，槽不在就只是"没有面板"。
>
> 兼容性面板：插件上报项 `pluginCapOrder` 现为
> `[pluginLoaded, restartTool, themeReport, themeSet, embedRelax, restartDelivery]`，另有启动器侧探针 `pageChannel`
> （「网页端已接入主题即时通道」）。

一个新插件 `dsh-launcher-plugin`，把 launcher ↔ 实例的实时通信收敛为**一条 loopback HTTP 线**，同时承载三个能力：**主题同步（新增）、dsh-restart（迁移）、能力上报（迁移）**。`dsh-self-mcp` 随之退役，文件通道（`restart-request.json` / `pending.json` / `capabilities.json`）全部删除。

设计原则：简洁明了，单线单语义；丢了问题不大（幂等、可自愈），不为极端场景加复杂度。

---

## 1. 总体架构

```
launcher (Go)                              dsh-launcher-plugin (实例内)
┌─────────────────────────┐                ┌──────────────────────────┐
│ HTTP server             │  loopback      │ 客户端（纯客户端,零文件） │
│  127.0.0.1:<临时端口>   │ ◄────────────► │  启动连接 → 快照+能力握手 │
│  Bearer token 鉴权      │   HTTP         │  之后只推增量            │
│  收到推送 → EmitEvent   │                │  断线按 RETRY_DELAYS_MS  │
└─────────────────────────┘                └──────────────────────────┘
        │                                          ▲
        ▼ 订阅 dsh:theme                            │ env 注入
   前端 data-theme + WindowSet*Theme        DSH_LAUNCHER_EVENTS / DSH_LAUNCHER_TOKEN
```

- 插件能存在的前提是 launcher 拉起了该实例（env 双门控），所以插件存活期间 launcher 名义上在——这正是原文件通道的假设，HTTP 只是把它变成"有确认的送达"。
- **一条线三条语义**：状态（主题）幂等可丢、命令（重启）要 ack 才走、能力握手即证明本次启动（陈旧判定整个消失）。

## 2. launcher 侧

### 2.1 HTTP 服务（⚠️ 历史：现为 `dsh-launcher/launcher_bridge.go` 的 WebSocket 桥）

- `net/http`，只绑 `127.0.0.1`，端口由 OS 分配（`:0` 后读实际端口）。
- Token：启动时生成随机 token，经 env 注入；所有请求要求 `Authorization: Bearer <token>`，不符返回 401。
- 收到 `theme` 推送 → `EmitEvent("dsh:theme", payload)`，前端订阅（App.tsx 是唯一事件所有者）。

### 2.2 接口清单（⚠️ 历史：5 个 REST 端点已全部删除，改为 WS 帧，见顶部第 1 条）

统一请求体信封：`{ "type": "...", "instanceId": "...", "launchId": "...", "payload": { ... } }`，`launchId` 沿用 `DSH_LAUNCH_ID`，供前端判陈旧。

| 方法 | 路径 | 用途 | 说明 |
|---|---|---|---|
| POST | `/connect` | 连接握手 | 插件启动即调：带 `capabilities` 全量 + 当前主题**全量快照**。launcher 回 `200`（= 快照已收，自愈完成） |
| POST | `/theme` | 主题增量推送 | 仅 `ui-theme` 变化时调，payload `{preference, resolved}` |
| POST | `/restart` | 重启请求 | 期望 `{instanceId, launchId, reason}`；launcher 校验后在**内存**记一次性标志，回 `ack` |
| GET  | `/pending` | 回取续跑负载 | 插件 init 调，按 `launchId` 返回挂起的 pending 并**原子消费**（返回即标记，待注入成功再 POST 确认） |
| POST | `/pending-consumed` | 续跑确认 | 注入成功后调，launcher 才真正清掉 |

不做鉴权分级、不做路由版本化——一个 token、一个前缀，够用。

### 2.3 改造点

- **env 注入**（[dsh_process.go:303](dsh-launcher/dsh_process.go:303) 附近）：为新插件加独立 env `DSH_LAUNCHER_EVENTS`（含完整 URL）与 `DSH_LAUNCHER_TOKEN`。**不稀释**既有 `DSH_LAUNCHER=1 / DSH_INSTANCE_ID / DSH_LAUNCH_ID` 语义；新插件自己的门控 env 单独给（见 §5）。
- **exit-reconcile**（[dsh_process.go:408](dsh-launcher/dsh_process.go:408)）：不再读 `restart-request.json`，改为查内存里的重启标志（按 launchId，收到 `/restart` 的 ack 时写入，进程退出时消费）。`consumeRestartRequest`、`restartRequestPath`、`writeSelfRestartOverlay` 相关文件逻辑一并删除。
- **overlay 生成**（[self_restart.go:69](dsh-launcher/self_restart.go:69)）：插件名改为 `dsh-launcher-plugin`，overlay 行与 `selfRestartPluginName` 常量同步更新。
- **能力读取**（[capabilities.go:31](dsh-launcher/capabilities.go:31)）：`capabilities.json` 文件读取删除，改为从内存中的"最近握手"取；未连接 = `unknown` = 不拦（fail-open，与现有 [util.ts](dsh-launcher/frontend/src/util.ts) 的 `embedGateReason/capsAlert` 约定一致，文案沿用）。

### 2.4 前端

- **App.tsx** 订阅 `dsh:theme` → `document.documentElement.dataset.theme = resolved`。
- **style.css**：现有 token 全部走 `var()`，新增一段 `[data-theme="dark"] { --bg: ...; --panel: ...; }` 覆盖即可，浅色为缺省，无 JS 兜底负担。
- **原生窗口主题**：`WindowSetDarkTheme()/WindowSetLightTheme()`（[runtime.js:76](dsh-launcher/frontend/src/wailsjs/runtime/runtime.js:76)），跟 `resolved` 对齐。
- `preference=system` 时插件上报的是 DSH 解析后的 `resolved`（light/dark），launcher 不做二次解析。

## 3. 插件侧（新增 `dsh-launcher/embed/dsh-launcher-plugin/`）

`inject=["tools","timer"]`，纯客户端：**零文件、零轮询、零落盘**。

```
apply() 启动流程:
  1. 读 env DSH_LAUNCHER_EVENTS / DSH_LAUNCHER_TOKEN；缺 → 静默降级（不拦、不报错）
  2. 读 ui-theme settings → POST /connect（capabilities + 主题全量快照）
  3. GET /pending（退避重试 2-3 轮，RETRY_DELAYS_MS 风格）
       有负载 → 注入「重启完成」+ 唤起会话 → 成功后 POST /pending-consumed
  4. 注册 dsh-restart 工具；监听 ui-theme 变化（SettingsController.mutate 同源事件）
       → 变化即 POST /theme
  5. 断线 → RETRY_DELAYS_MS 退避重连，重连成功即重发快照+能力（自愈）
```

- **重启流程**：工具调用（确认词 `restart-dsh` 不变）→ `POST /restart` → **收到 ack 才 `ctx.appExit(0)`**；POST 失败 → 工具报错"launcher 不可达"，**不退出**——比原"写完就走、碰运气"更稳，实例不会白死一次。
- **能力**：`restartTool` / `themeReport`（settings 注入失败 → `{ok:false, reason}`，随握手上报）/ `embedRelax`（吸收 `dsh-self-mcp` 的 `relaxEmbedAuth`，内嵌 iframe 放宽会话校验）。
- **主题监听**：注入 `settings` 服务读 `ui-theme` namespace（`preference` 字段，`light|dark|system`）；注入失败不 fatal，仅 `themeReport={ok:false}`。
- 重连退避、`agent.followup()` 优先/`session.prompt()` 回退注入续跑消息——沿用旧 `dsh-self-mcp`（已退役删除）的成熟套路，落在 [`embed/dsh-launcher-plugin/lib/index.js`](dsh-launcher/embed/dsh-launcher-plugin/lib/index.js) 里。

## 4. 多实例与主题归属

- `ui-theme` 存在**共享 profile**（全局，不随实例），launcher 主题天然是全局跟随——不按实例做主题聚合。
- **embed/active 实例优先**：多个实例同时推时，取当前 embed 视图（或最近活跃）实例的快照；其余实例的增量同样汇入同一个事件（全局共享，正常情况下结果一致，不一致时以最近一条为准）。
- 实例断连 → 该实例不再推；launcher 保持最后一个 `resolved`，不做超时回滚。

## 5. 挂载门控与迁移

- **新插件对 launcher 拉起的实例全挂**（主题是外观功能，按实例开关价值不大）：overlay 默认生成，或设置页一个总开关（初版建议：跟 launcher 进程走，不加实例级勾选）。
- **迁移**：市场里已装的 `dsh-self-mcp` 用现有内置安装流（[self_restart_install.go](dsh-launcher/self_restart_install.go)）自动替换/卸载；覆盖层行改指新插件名。**必须处理**，否则两个插件同时注册 `dsh-restart` → 同名工具冲突 + 两套能力报告打架。
- 老实例残留的 `.dsh-self-mcp/*.json` 不读不删（无害），launcher 不再依赖它们。

## 6. 验收清单

1. 切换 DSH 主题（light/dark/system）→ launcher 在**同帧/毫秒级**内换肤，原生窗口标题栏色同步。
2. `dsh-restart`：ack → 干净退出 → launcher 重拉 → 新进程 init 回取 pending → 注入「重启完成」+ 唤起。
3. launcher 在插件 POST 前挂掉 → 插件工具报错、实例存活、不退出。
4. launcher 重启（插件在线重连）→ `/connect` 快照补全，主题与能力立即恢复，无陈旧判定。
5. 未装新插件 / 连不上的实例 → 面板显示 `unknown`，本地设置兜底，**不变红**（fail-open）。
6. `wails build` 通过（AGENTS.md 强制项）。

## 7. 影响面

| 文件 | 动作 |
|---|---|
| `dsh-launcher/launcher_http.go` | 新增：HTTP 服务、鉴权、事件转发 |
| `dsh-launcher/dsh_process.go` | env 注入 + exit-reconcile 改内存标志 |
| `dsh-launcher/self_restart.go` | 插件名常量、删文件通道、overlay 改名 |
| `dsh-launcher/capabilities.go` | 删文件读取，改握手内存读取 |
| `dsh-launcher/self_restart_install.go` | 内置安装流指向新插件 |
| `dsh-launcher/embed/dsh-launcher-plugin/` | 新增插件（主题 + restart + 能力） |
| `dsh-launcher/embed/dsh-self-mcp/` | 退役删除 |
| `frontend/src/App.tsx` | 订阅 `dsh:theme` |
| `frontend/src/style.css` | `[data-theme="dark"]` token 覆盖段 |

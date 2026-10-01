# dsh-launcher-plugin — launcher ↔ 实例桥接插件

一个插件一条线：把 launcher 与实例之间的实时通信收敛为**一条 loopback WebSocket**，
同时承载 **主题双向同步、dsh-restart（自重启）、能力握手** 三个能力。
`dsh-self-mcp` 随之退役，文件通道（`restart-request.json` / `pending.json` /
`capabilities.json`）全部删除 —— 本插件**零文件、零落盘**。

## 工作方式

```
launcher (Go)                                  dsh-launcher-plugin (实例内)
┌─────────────────────────────┐               ┌──────────────────────────────┐
│ WebSocket server            │   loopback    │ 纯客户端                     │
│  127.0.0.1:<OS 分配端口> /ws│ ◄───────────► │ 启动 → hello 全量握手        │
│  握手头 Bearer token 鉴权   │      WS       │ 之后能力/主题增量重发 hello   │
│  收到 theme 帧 → dsh:theme  │               │ 断线按 RETRY_DELAYS_MS 退避   │
│  收到 set-theme → 广播命令  │               │ set-theme → 写 ui-theme       │
└─────────────────────────────┘               └──────────────────────────────┘
        │                                            ▲ env 注入
        ▼ 订阅 dsh:theme                         DSH_LAUNCHER_EVENTS / DSH_LAUNCHER_TOKEN
   前端 data-theme + WindowSet*Theme
```

- **门控**：launcher 挂载覆盖层（`--patch` 临时 overlay）+ env 注入同时生效；
  只有 launcher 拉起的实例才有桥接地址，其余场景静默降级（不拦启动）。
- **握手**：连上后第一帧必须是 `hello`（能力 + 主题全量快照）；hello 之前的帧一律忽略。
  启动器侧未收到握手 = `unknown` = 不标红（fail-open）。同一实例的新连接会踢掉旧连接。
- **心跳**：服务端 20s 发 RFC 6455 ping，60s 收不到 pong 判定断开；客户端（undici
  WebSocket）自动回 pong，无需自己写心跳。

## 帧

两个方向共用同一个信封（`{type, instanceId, launchId, payload}`）。插件 → 启动器：

| type | 含义 |
| --- | --- |
| `hello` | 全量握手快照：`plugin / pluginVersion / reportedAt / capabilities / theme` |
| `theme` | 增量主题 `{preference}` |
| `restart` | 请求自重启 `{id, reason, pending}` |
| `pending-consumed` | 续跑负载已注入成功，launcher 可以清掉 |
| `command-result` | 启动器命令的应答 `{id, ok, error, detail?}`（`set-theme` 用；`detail` 是插件侧要说给 launcher 日志的自由文本） |

启动器 → 插件：

| type | 含义 |
| --- | --- |
| `pending` | 下发重启完成续跑负载（幂等，不消费） |
| `set-theme` | 用启动器里的选择改 DSH 主题 `{id, preference}` |
| `restart-result` | `restart` 的 ack / 拒绝 `{id, ok, error}` |

`restart` / `set-theme` 都靠 `id` 配应答；插件侧用 5s 超时兜底，超时视为失败。

## 主题双向同步

- **DSH → 启动器**：监听共享 profile 的 `ui-theme` settings（`preference`: `light|dark|system`），
  初读 + `settings/document-updated` 事件 + 1s 轮询兜底（事件冒泡不保证），变化就发 `theme` 帧。
  只带 `preference`：`system` 由启动器前端用 `@media (prefers-color-scheme)` 解析，
  插件不做二次解析，也不怕 OS 主题中途变化。
- **启动器 → DSH**：收到 `set-theme` 后调 `settings.update("ui-theme", {preference}, revision)`，
  写成功才算数；版本冲突（`SettingsConflictError`）会重读 revision 再试一次，最多两次。
  写成功后回 `command-result {ok:true}` + 一条 `theme` 帧；失败回 `{ok:false, error}`，
  并把能力 `themeSet` 降级为失败（下一次 hello 上报），启动器据此提示「只在本机生效」。

## dsh-restart

确认词 `restart-dsh` 不变；子代理/无会话/重复请求护栏不变。区别：

1. 请求经 `restart` 帧提交（含重启完成负载），**收到 `restart-result {ok:true}` 才退出**；
   超时/被拒就报错、不退出 —— 实例不会白死一次。
2. 退出走阶梯：`ctx.inject(["appExit"])` 缓存 → `ctx.get("appExit")` → `ctx.appExit`
   依次尝试，日志写清用了哪条；**三条都拿不到就 `process.exit(0)` 兜底**（跳过优雅拆卸，
   并发一帧 `command-result {detail}` 让 launcher 日志留痕）。旧实现只看 `ctx.get`，
   拿不到就静默 no-op —— 用户看到的是「点了重启没反应」（2026-10-01 真机事故）。
3. launcher 侧另有看门狗：ack 后 20s 进程还没退，就强制收掉进程树并按「自重启」重新
   拉起（`launcher_bridge.go` 的 `armRestartWatchdog` + `managedProcess.forceRestart`）。
   插件退出失败最多多花 20 秒，不会把实例卡在"已确认却不动"的状态。
4. 新进程启动后由 launcher 主动推 `pending` 帧；注入「重启完成」成功再发
   `pending-consumed`；确认前负载留在 launcher 内存，下次启动重新下发
   （旧 `pending.json` 的跨重启语义原样保留）。

## 能力握手

`pluginLoaded / restartTool / themeReport / themeSet / embedRelax / restartDelivery`
随 `hello` 快照上报 —— 握手即"本次启动"，陈旧判定（launchId 比对）整个消失。

## 开发

- 改了能力或行为：**bump `package.json` 版本号**（启动器用 内置 vs 已装 版本比对分诊）。
- Go：`cd dsh-launcher && go test ./...`（`launcher_bridge_test.go` 有真 socket 协议测试，
  `launcher_bridge_plugin_test.go` 用 node 跑本插件的真代码做端到端契约测试，没有 node 自动跳过）。
- 构建：`wails build`（AGENTS.md 强制项）。

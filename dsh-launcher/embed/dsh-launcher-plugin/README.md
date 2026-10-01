# dsh-launcher-plugin — launcher ↔ 实例桥接插件

一个插件一条线：把 launcher 与实例之间的实时通信收敛为**一条 loopback HTTP 线**，
同时承载 **主题同步（新增）、dsh-restart（迁移）、能力握手（迁移）** 三个能力。
`dsh-self-mcp` 随之退役，文件通道（`restart-request.json` / `pending.json` /
`capabilities.json`）全部删除 —— 本插件**零文件、零落盘**。

## 工作方式

```
launcher (Go)                                dsh-launcher-plugin (实例内)
┌───────────────────────────┐               ┌────────────────────────────┐
│ HTTP server               │  loopback     │ 纯客户端                   │
│  127.0.0.1:<OS 分配端口>  │ ◄────────────► │ 启动 → /connect 全量握手    │
│  Bearer token 鉴权        │    HTTP       │ 之后能力/主题增量推送        │
│  收到推送 → dsh:theme 事件 │               │ 断线按 RETRY_DELAYS_MS 退避 │
└───────────────────────────┘               └────────────────────────────┘
        │                                            ▲ env 注入
        ▼ 订阅 dsh:theme                         DSH_LAUNCHER_EVENTS / DSH_LAUNCHER_TOKEN
   前端 data-theme + WindowSet*Theme
```

- **门控**：launcher 挂载覆盖层（`--patch` 临时 overlay）+ env 注入同时生效；
  只有 launcher 拉起的实例才有桥接地址，其余场景静默降级（不拦启动）。
- **接口**：`POST /connect`（能力+主题全量快照，幂等自愈）、`POST /theme`
  （增量 `{preference}`）、`POST /restart`（校验后 ack，**收到 ack 才退出**）、
  `GET /pending`（回取重启完成续跑负载，不消费）、`POST /pending-consumed`
  （注入成功后确认，launcher 才清掉）。

## 主题同步

监听共享 profile 的 `ui-theme` settings（`preference`: `light|dark|system`）：
初读 + `settings/document-updated` 事件 + 1s 轮询兜底（事件冒泡不保证）。
推送只带 `preference`：`system` 由前端用 `@media (prefers-color-scheme)` 解析，
launcher 不做二次解析，也不怕 OS 主题中途变化。

## dsh-restart

确认词 `restart-dsh` 不变；子代理/无会话/重复请求护栏不变。区别：

1. 请求经 `POST /restart` 提交（含重启完成负载），**收到 ack 才 `ctx.appExit(0)`**；
   POST 失败报错、不退出 —— 实例不会白死一次。
2. 新进程启动后 `GET /pending` 回取负载，注入「重启完成」成功再
   `POST /pending-consumed`；确认前负载留在 launcher，下次启动重新回取
   （旧 `pending.json` 的跨重启语义原样保留）。

## 能力握手

`pluginLoaded / restartTool / themeReport / embedRelax / restartDelivery`
随 `/connect` 快照上报 —— 握手即"本次启动"，陈旧判定（launchId 比对）整个消失。
启动器侧未收到握手 = `unknown` = 不标红（fail-open）。

## 开发

- 改了能力或行为：**bump `package.json` 版本号**（启动器用 内置 vs 已装 版本比对分诊）。
- Go：`cd dsh-launcher && go test ./...`（`self_restart_test.go`、`capabilities_test.go`）。
- 构建：`wails build`（AGENTS.md 强制项）。

# dsh-launcher-plugin — launcher ↔ 实例桥接插件

一个插件一条线：把 launcher 与实例之间的实时通信收敛为**一条 loopback WebSocket**，
同时承载 **主题双向同步、dsh-restart（自重启）、能力握手** 三个能力。
`dsh-self-mcp` 随之退役，文件通道（`restart-request.json` / `pending.json` /
`capabilities.json`）全部删除 —— 本插件**零文件、零落盘**。

0.2.6 起同一条线多接一个角色：**网页端即时通道**（见下），让"点启动器换主题"从
"等 DSH 写完配置再采纳（约 300ms）"变成**页面当场变色（毫秒级）**。

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

同一个 `/ws` 还接一个**网页端角色**（`role=page`）：DSH 网页里的客户端半边
（`lib/client.js`）用 `?token=…` 连同一个端口，Origin 必须是本机已知的 DSH 网页地址。

```
launcher /ws  ◄── page-hello ──────  dsh-launcher-plugin 客户端半边（DSH 网页里）
              ── page-set-theme ───►  theme.setTheme()  ← 毫秒级变色，写盘后台跑
              ◄── page-theme ───────  theme/change（页面里自己点的主题）
              ◄── page-result ──────  成功/失败（失败则退回服务端写入路径）
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

网页端角色（`role=page`，0.2.6+）只认主题帧，别的帧一律忽略：

| 方向 | type | 含义 |
| --- | --- | --- |
| 页面 → 启动器 | `page-hello` | 页面接上即时通道 `{plugin, pluginVersion}`；launchId 对不上就回 `page-result{ok:false}` 并断开 |
| 启动器 → 页面 | `page-set-theme` | 让页面当场换主题 `{id, preference}`（写盘由 DSH 自己后台跑） |
| 页面 → 启动器 | `page-theme` | 页面里自己换的主题 `{preference}`，启动器当场采纳 |
| 页面 → 启动器 | `page-result` | `page-set-theme` 的结果 `{id, ok, error?, preference?}`；`ok:false` 时启动器退回服务端写入路径 |

## 主题双向同步

- **即时通道（0.2.6+，默认路径）**：DSH 网页里的客户端半边 `lib/client.js` 直接连启动器的
  环回 WS，启动器点主题 → 页面 `theme.setTheme()` **当场变色**（毫秒级），DSH 自己的配置
  写入在后台跑（客户端 `setTheme` 本来就是乐观更新：先 `publish()` 再 `host.set()`）。
  为什么绕这一下：服务端只能改配置项，一次写入要抢文件锁 + 与 HMR 互斥 + 原子写 +
  两次全量 `describe()`，真机实测 300–350ms；而"改配置 → 网页端 `adopt()` 重绘"这条路
  必然带上这段延迟。**页面在场时服务端插件不再重复写**（同一次点击只写一遍配置）。
  客户端半边的坐标（url/token/instanceId/launchId）由服务端半边用 DSH 官方的
  `webserver/index-inject` 钩子注入成 `window.__DSH_LAUNCHER_BRIDGE__`（DSH 主题插件自己
  也用这个钩子）。页面里自己换主题 → 客户端半边监听 `theme/change` 立刻回报，启动器当场采纳。
- **退回路径（fail-open）**：页面没连上、客户端入口没加载（老 DSH / 约定变更）、
  `page-set-theme` 应用失败 —— 任何一步不成立就自动走下面这条服务端写入路径，
  并在 `page-result` 里说明原因；启动器侧 `pageChannel` 能力项如实显示"未接入"。
  绝不假装成功。
- **DSH → 启动器**：监听共享 profile 的 `ui-theme` settings（`preference`: `light|dark|system`），
  初读 + `settings/document-updated` 事件 + **5s 轮询兜底**（事件冒泡在别的 DSH 版本上不保证；
  真机实测事件路径 77–90ms 就到，兜底只是安全网，频率压低是为了少跑全量 `describe()`）。
  只带 `preference`：`system` 由启动器前端用 `@media (prefers-color-scheme)` 解析，
  插件不做二次解析，也不怕 OS 主题中途变化。
- **启动器 → DSH**：收到 `set-theme` 后调 `settings.update("ui-theme", {preference})` ——
  **不带期望 revision**：`describe()` 要把每个命名空间的 schema 序列化成 JSON 快照，而
  `settings.update` 内部本来就要跑两遍（`write` 的 edit 回调里一次、写完再一次），
  插件再补第三次只为 CAS 不划算；`mergeLayers` 的合并语义本来就保住别人改的其它字段。
  失败重试仍保留（最多两次，针对文件锁超时之类的瞬时错误）。
  写入期间置 `themeWriteInFlight`：自己写出来的 `document-updated` 不再 describe 一次，
  写成功后回 `command-result {ok:true}` + 一条 `theme` 帧；失败回 `{ok:false, error}`，
  并把能力 `themeSet` 降级为失败（下一次 hello 上报），启动器据此提示「只在本机生效」。
- **耗时诊断**：每次点击都会往实例日志（stderr）写几行 `主题：…`（收到 set-theme / 写入完成用时 /
  事件命中与读取用时 / 推送时刻）。只走 `console.error`，不发桥接帧 —— 免得刷满启动器日志面板。

## 装载会话与定时器护栏（0.2.7+）

0.2.6 在真机上「DSH 启动 5s 后进程 exit 1」（2026-10-02 00:28 / 00:29 各一次），根因在
**cordis 的计时器不属于调用方**：

- `cordis-plugin-timer` 的 `TimerService.timeout()` 用 `this.ctx.effect(...)` 注册计时器 ——
  `this.ctx` 是 **timer 服务自己的 ctx**，所以 `ctx.timeout(cb, ms)` 排下的定时器
  **不随调用方 fiber 销毁而取消**。
- DSH 会**重复装载**本插件（双半包机制：`dsh.client` 声明让宿主 Loader 里多出一行；真机同一个
  pid 出现两条「已装载」，0.2.5 没有该声明时只有一条）。先装载的那份 fiber 被销毁后，
  它排下的 5s 主题轮询照样到期；回调里再调 `ctx.timeout` 就抛
  `cannot get required service "timer" in inactive context`，而这个异常发生在 timer 回调里
  没人接 → 整个 DSH 进程 exit 1。

0.2.7 的修法（两道，缺一不可）：

1. **装载会话**：每次 `apply` 建一个会话，旧会话先收（取消定时器、关连接）；`ctx.effect` 在
   fiber 销毁时收掉自己的会话（timer 服务不会替我们取消，所以必须自己挂）。留痕：
   `会话：装载会话 #N 开始 / 结束（…）`。
2. **定时器硬护栏** `armTimeout`：所有 `ctx.timeout` 调用统一走它 —— 整个回调包 try/catch
   （异常绝不外逃）、disposer 记进会话、会话已销毁就不排、ctx 失效时留一行
   `定时器 <名字> 排不了（ctx 已失效？已忽略）`。socket 事件与交付重试同样包了护栏。

回归锁：`dsh-launcher/testdata/plugin-dispose-harness.mjs` + Go 用例
`TestPluginTimerGuardSurvivesStaleContext` —— 用假 ctx 复现同一时序（失效后 `timeout` 抛
真机原文的错）：0.2.6 的代码在这条用例里必崩（exit 1，栈落在 `poll`），0.2.7 必须活着退出。

## dsh-restart

确认词 `restart-dsh` 不变；子代理/无会话/重复请求护栏不变。区别：

1. 请求经 `restart` 帧提交（含重启完成负载），**收到 `restart-result {ok:true}` 才退出**；
   超时/被拒就报错、不退出 —— 实例不会白死一次。
2. 退出动作在 ack 后 500ms 开始（先让工具结果落盘），**三条计时通道同时挂上**，谁先响算谁
   （`exitStarted` 幂等，重复触发直接忽略）：
   - `timer` 服务注入后的 ctx（`timerCtxRef.timeout`）；
   - `apply` 的 ctx（`ctx.timeout`；在工具上下文里会抛 `cannot get property "timeout" without inject`）；
   - 全局 `setTimeout`（**普通 npm 插件里就是真计时器**）。

   回调里先取退出入口（`ctx.inject(["appExit"])` 缓存 → `ctx.get("appExit")` → `ctx.appExit`）：
   拿到就 `appExit(0)` 优雅退出；3s 内进程还在，就 `process.exit(0)` → `process.reallyExit(0)`
   → `process.kill(pid, SIGTERM|SIGKILL)` 逐个兜底。全程留痕，**没有任何一条分支是静默 no-op**。
   - 为什么三条都挂（0.2.3 真机教训）：只挂 Host 计时器时，DSH 一旦开始关闭，`ctx.timeout`
     排进去的回调就不再触发 —— `appExit(0)` 之后那个 3s 硬退兜底整个没响，进程又活满 20s
     等 launcher 看门狗；而全局 `setTimeout` 那条照样会响。
   - 为什么"全局 `setTimeout` 在插件里不可用"是错的（0.2.2 之前的结论已推翻）：cordis 的
     sandbox（`@deepseek-ai/dsh-cordis-host-runner` 的 `sandbox.js`）确实会把
     `setTimeout/setInterval/setImmediate` 换成"Node timers are unavailable，请用 ctx.timeout"
     的抛错函数，但它只服务**动态包**（`dyn-<n>`，`evaluateHostCode` 在 vm 里求值模型写的
     函数体）；普通 npm 插件跑在宿主进程里，拿到的是真计时器。0.2.3 探针实测：装载时
     `timerCtx.timeout` / `ctx.timeout` / 全局 `setTimeout` 三条都会触发。
   - 留痕走**双通道**：`console.error`（落实例日志）＋ `command-result {id:"", detail}` 帧
     （落 launcher 的 app.log）。刻意不用 `ctx.logger`：实测它的输出两个日志都不进。
3. launcher 侧另有看门狗：ack 后 20s 进程还没退，就强制收掉进程树并按「自重启」重新
   拉起（`launcher_bridge.go` 的 `armRestartWatchdog` + `managedProcess.forceRestart`）。
   插件退出失败最多多花 20 秒，不会把实例卡在"已确认却不动"的状态。
4. 新进程启动后由 launcher 主动推 `pending` 帧；注入「重启完成」成功再发
   `pending-consumed`；确认前负载留在 launcher 内存，下次启动重新下发
   （旧 `pending.json` 的跨重启语义原样保留）。

## 能力握手

`pluginLoaded / restartTool / themeReport / themeSet / embedRelax / restartDelivery`
随 `hello` 快照上报 —— 握手即"本次启动"，陈旧判定（launchId 比对）整个消失。

另有一个**启动器侧**的项 `pageChannel`（不在上面的快照里，由启动器自己看"这个实例有没有
网页端连接"）：接入 = 主题即时通道可用；未接入 = 主题仍会同步，只是要多等 DSH 自己写一次
配置（约 300ms）。它**不参与**握手版本比对，只作展示与诊断。

## 开发

- 改了能力或行为：**bump `package.json` 版本号**（启动器用 内置 vs 已装 版本比对分诊）。
- Go：`cd dsh-launcher && go test ./...`（`launcher_bridge_test.go` 有真 socket 协议测试，
  `launcher_bridge_plugin_test.go` 用 node 跑本插件的真代码做端到端契约测试，没有 node 自动跳过）。
- 构建：`wails build`（AGENTS.md 强制项）。

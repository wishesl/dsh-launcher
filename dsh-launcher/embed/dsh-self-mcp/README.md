# dsh-self-mcp — DSH 自管理重启插件

提供唯一工具 **`dsh-restart`**：重启整个 DSH 进程。重启完成后，本插件会**自动向发起会话注入「重启完成」消息并继续该会话**，方便调试需要重启才生效的插件。

## 工作原理

```
模型调用 dsh-restart(reason, confirm="restart-dsh")
  └─ execute:
       1. 写 <cwd>/.dsh-self-mcp/pending.json（交付意图：sessionId/callId/reason/ts）
       2. 若 DSH_LAUNCHER=1：写 <cwd>/.dsh-self-mcp/restart-request.json（launcher 契约）
          否则：自 spawn 替换进程（兜底）
       3. 返回 {status:"restarting"}，500ms 后请求 ctx.appExit(0) 干净退出

dsh-launcher（监督者）
  └─ 子进程干净退出后，exit-reconcile 发现 restart-request.json
       → 消费即删（杜绝重启循环）→ 自动重新拉起同一实例

新进程 boot，插件重新挂载
  └─ 读 pending.json → 首选「agent 级 followup + plugin/notice 来源」投递
       · 模型看到完整正文
       · 界面渲染为 inject 折叠行（label=dsh-self-mcp + 一行 summary），不是用户气泡
       · 依据：source.kind='plugin:dsh-self-mcp' + form='notice' （v4 producer-owned，DSH 0.1.7-rc.2 起 kind:'plugin' 被拒） 走 dsh-client-ui-chat
         的 contextProvenance()/contextForm() 分支（只有 kind==='user' 才是用户气泡）
     失败则回退 sessionController.prompt()（界面是用户气泡），保证消息一定送到
     两条通道都在进程内、复用产品自己的消息路径，不经 /api
       （dsh 0.1.2 起 /api 强制 401 且 prompt 载荷有 typert 网关包装，裸客户端不兼容）
     → 成功即删除 pending.json；失败保留待下次启动重试（进程内退避重试，不无限循环）
```

## 装配（内置 + 按实例勾选，零残留）

插件源码内置于 dsh-launcher（`dsh-launcher/embed/dsh-self-mcp/`，`embeddata.go` 用
`//go:embed` 打进 exe）。装配分两步，都由 launcher UI 完成：

1. **安装到全局**（插件市场 → 已安装页签 → dsh-self-mcp 面板「安装到全局」）：
   解出内置源码到 `<profile>/.dsh-builtin/dsh-self-mcp/`，再用常规 pnpm 命令
   （`pnpm add file:<该目录>`）装进全局 profile —— 走命令、不纯 copy，依赖照常解析；
   `.dsh-builtin` 是稳定路径，package.json 里的 `file:` spec 在后续 pnpm rebuild 中不会失效。
2. **实例勾选**（实例表单「启用自管理重启」→ `Instance.SelfRestart`）：
   双重门控 = 插件已装 **&&** 实例勾选 → 本次启动生成临时覆盖层 `.dsh-self-restart-<id>.yml`
   （`insert:` 块新增条目——loader 补丁语义里裸行只按 id 覆盖已有条目、找不到会跳过）：
   ```yaml
   - insert:
       - id: self-restart
         name: 'dsh-self-mcp'
   ```
   并注入 `DSH_LAUNCHER=1`、`DSH_INSTANCE_ID=<id>`。

- **不改 `cordis.patch.yml`**；未勾选的实例（含别的项目/控制台直启）→ 无行引用 →
  工具不存在、状态不碰，零残留。
- 插件未安装时 UI 置灰提示先安装；即使误配也绝不生成覆盖层（fail-soft，实例照常启动）。
- 放弃时：插件市场卸载（`pnpm remove dsh-self-mcp`）即可；`.dsh-self-mcp/` 状态目录
  在实例目录内、已 gitignore、重启完成后自动清空。

## 护栏

- `confirm` 必填且必须为 `restart-dsh`；
- 子代理 / 工作流子代理禁止触发（只看根会话）；
- 已有未交付的重启请求时拒绝（幂等）；
- 重启请求文件被 launcher 消费即删（防循环）。

## 内嵌支持（embed）

让 launcher 能把 DSH 界面嵌进自己的**跨源 iframe**。

**为什么需要放宽**：DSH 的浏览器会话 cookie 是 `SameSite=Strict`
（`dsh-client-connection` 的 `sessionCookie()`），而跨站 iframe 的请求一律不带它。
于是 `/?token=…` 虽然能过，但它返回的 `303 → /` 那一步拿不到 cookie，直接 401 ——
界面永远打不开。（另有一条 `/api` 栅栏：`isTrustedApiRequest()` 会拒绝
`sec-fetch-site: cross-site`。）

**做了什么**：插件在 `connection` service 就绪后替换它的两个方法
（这两处都由调用方**每请求现查 service**，见 `dsh-host-frontend-static` 的
`ctx.connection.authorizeIndex(req, res)` 与 `dsh-api-gateway` 的
`connection.requestRejection(req)`，所以替换实例方法即生效）：

| 方法 | 放宽规则 | 目的 |
|---|---|---|
| `authorizeIndex` | **仅当 `sec-fetch-site: cross-site`** 且 `GET /?token=<有效 launch token>` → 直接渲染 index | 跳过 cookie 往返；普通浏览器仍走原 303+cookie 流程（否则它拿不到 cookie） |
| `requestRejection` | ① `Origin` 命中白名单（默认 `http://wails.localhost`，即启动器页面源）；② 内嵌文档自身的请求：`Origin` **或** `Referer` 的 host 等于请求的 `Host` | iframe 内部对自身源的 `/api` 请求没有 Strict cookie，靠这两条识别 |

> ② 里**必须同时看 `Origin`**：流式端点 **`/api/remote.mux`（实时事件流）只带 `Origin`，
> 既没有 `Referer` 也没有 `Sec-Fetch-Site`**。只按 Referer 判定会导致「界面能打开、
> 但左下角一直显示『自动重连中』」——会话列表、工作区都出不来。

**没有放宽的**（实测仍被拒）：无 token → 401；错 token → 401；
外部来源 `Origin` → 403；外部 `Referer` → 401；无 `Origin` → 401。

**安全代价（要知道）**：规则 ② 意味着「任何 `Origin`/`Referer` 指向本机同 host 的
cookie-less 请求」都会被放行。攻击者网页发出的跨站请求带的是自己的 Origin，Host/Origin
栅栏照旧拒绝；且 `sec-fetch-site: cross-site` 的请求仍被挡住，所以外部站点依旧打不开
界面、也调不动 `/api`。但这条规则的强度确实低于原设计（原设计要求持有签名 cookie）。
不需要内嵌时，把 `relaxEmbedAuth()` 的调用去掉即可恢复原状。

## 状态文件

| 文件 | 位置 | 作用 |
|---|---|---|
| `pending.json` | `<实例目录>/.dsh-self-mcp/` | 交付意图（跨重启的持久状态） |
| `restart-request.json` | 同上 | launcher 契约：请求自动重新拉起 |
| `capabilities.json` | 同上 | 能力报告：探测结论的出口（见下节） |

## 能力报告（capabilities.json）

插件判断「某项功能能不能用」靠**探测私有接口在不在**（例如 `connection` 上还有没有
`authorizeIndex` / `requestRejection`），**不按 DSH 版本号分支** —— 版本号是上游控制的
时间戳，用它当键必然滞后（用户装的是 `latest`，任何"版本 → 预设"的表都慢一步）。

探测必须有出口：以前 `relaxEmbedAuth()` 发现接口变了只写 logger 就 `return`，界面上只
表现为内嵌一直「自动重连中」，排查要好几个来回。现在探测结论会**落盘**成：

```json
{
  "schema": 1,
  "plugin": "dsh-self-mcp",
  "pluginVersion": "0.2.1",
  "pid": 12345,
  "launchId": "3f9c… —— 启动器经 DSH_LAUNCH_ID 注入的一次性凭据",
  "reportedAt": "2026-09-12T02:00:00.000Z",
  "launcher": true,
  "instanceId": "…",
  "capabilities": [
    { "id": "pluginLoaded", "ok": true, "reason": "" },
    { "id": "embedRelax", "ok": false, "reason": "connection 上没有 authorizeIndex / requestRejection（DSH 内部接口变了？）" }
  ]
}
```

- 能力项：`pluginLoaded` / `restartTool` / `embedRelax` / `restartDelivery`（最后一项记录
  上一次重启完成实际走的通道：plugin/notice 还是回退的 `prompt()`）。
- 写入时机：`apply()` 装载时、`relaxEmbedAuth()` 探测后、工具注册后、以及交付重启完成
  消息之后 —— 都是 best-effort，写不出去不影响插件功能。
- 启动器侧：`capabilities.go` 读回报告，补上自己的探测项（地址 / token / 覆盖层门控），
  汇总成右栏「兼容性」标签；`embedRelax` 为 false 时直接把内嵌入口置灰并显示原因。
- **陈旧保护**：启动器每次启动实例前先删掉这个文件（「文件在」=「本次运行报告过」）；
  再用 `launchId` 精确比对这份报告属于哪一次启动，对不上才提示"可能已过期"。
  ⚠️ **不要改用 `pid` 比对**：启动器手里是 `cmd /c` 外壳（cmd.exe）的 pid，这里报的是
  node 进程的 pid，中间隔着 cmd → npx → node，两者按构造永远不会相等 —— 拿 pid 判断
  "报告是否过期"会 100% 误报（功能一切正常却提示过期）。任一侧缺 `launchId`（旧版插件）
  时不下结论。

契约是 launcher 与插件双方约定的，不依赖 DSH 的任何私有格式 —— 这是把耦合点从
"别人的内部实现"挪到"我们自己的边界"上的做法。**升级插件后需要重新安装到全局**
（`pnpm add file:<profile>/.dsh-builtin/dsh-self-mcp`），否则跑的还是旧副本、不会写报告。

## 验证

- Go：`cd dsh-launcher && go test ./...`（含 `self_restart_test.go`、`capabilities_test.go`）
- 覆盖层挂载：`node <dsh>/lib/bin.js web --patch .dsh-self-restart-test.yml --port 0 --no-open`
  启动成功后清理临时覆盖层文件（已 gitignore）。
- 能力报告：启动一个勾选了「自管理重启」的实例，然后看
  `<实例目录>/.dsh-self-mcp/capabilities.json`，或直接开右栏「兼容性」标签。


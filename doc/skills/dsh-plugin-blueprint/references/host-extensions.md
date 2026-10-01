# 宿主侧扩展点总表（注册形态一句话 + 每类坑）

> 读它的时机：确定插件跑在宿主半边、要挑一个挂载点时。行号是 0.2.0-rc.2 快照，检索请按符号名 grep。

| 扩展点 | 形态 | 真实例子 |
|---|---|---|
| 注册 tool | `ctx.tools.register(defineTool({name,description,parameters,output,execute,presentCall}))` → disposer | `dsh-tool-todo/lib/index.js:95` |
| tool 拦截 | `ctx.on('tools/pre-execute'\|'tools/post-execute'\|'tools/execute'\|'tools/result'\|'tools/change', fn)` | `dsh-hooks-codex/lib/index.js:232,245` |
| tool 可见性/守卫 | `ctx.tools.restrict({allow?,deny?})` / `ctx.tools.guard(fn)`（同步） | `dsh-tools/lib/types/index.d.ts:644,655` |
| 提供 Service | `class X extends Service{static inject=[...];static Config=z.object({...});constructor(ctx,cfg){super(ctx,'名字')} async [Service.init](){}}` | `dsh-host-webserver/lib/index.js:139,141,157,228` |
| 消费 Service | 包级 `export const inject=['webServer']` 或类级 `static inject=[...]` | `dsh-client-hmr/lib/index.js:21` |
| 会话投影 | `ctx.sessionProjections.register({key,stateSchema,init,apply,wire,stateVersion})` | `dsh-tool-todo/lib/index.js:80-94` |
| HTTP 路由 | `ctx.webServer.register({kind:'exact'\|'prefix',path,handler})`；升级 `registerUpgrade`；兜底 `registerFallback`；首页改写 `tapIndex` | `dsh-host-webserver/lib/index.js:177,191,206,220` |
| 首页 HTML 注入 | `ctx.on('webserver/index-inject', table => table.push(row))` | `dsh-client-connection/lib/index.js:822`；`dsh-client-ui-theme/lib/index.js:92` |
| 设置表单 | 插件导出 Schemastery `Config`；宿主 `ctx.settings.describe/update/replace/mutate` | `dsh-settings/lib/index.js:470,479,488,501` |
| 斜杠命令 | `ctx.commands.register({definitionId?,name,description,input?,recordInput?,handler})` | `dsh-command-goal/lib/index.js:175-183` |
| 往会话投内容 | `agent.followup/steer/inject/send(createUserMessage({content,source}))` | `dsh-schedule/lib/index.js:1586-1596` |
| 自定义消息来源 | `declare module '@deepseek-ai/dsh-llm'{interface MessageSourceMap{...}}` | `dsh-hooks-codex/lib/types/index.d.ts:13-19` |
| 审批 | `ctx.approval`（`approval/request` 事件） | `dsh-user-approval/lib/types/index.d.ts:20` |
| 子进程/沙箱 | `ctx.subprocess`、`ctx.sandbox`(SandboxProvider)、`ctx.sandboxPolicy` | `dsh-bash-local/lib/index.js:68`；`dsh-sandbox/lib/types/index.d.ts:116-127` |
| 其它宿主服务 | `agents` / `sessions` / `sessionController` / `jobs` / `fs` / `llm` / `subagents` / `settings` / `commands` / `tools` | 各包 `lib/types/index.d.ts` 的 `declare module '@deepseek-ai/cordis'` |

## 要点与坑

- `parameters` 是 DSH 自研 schema 子集（`dsh-tools/lib/types/schema.d.ts:248` 的 `defineTool`），**不是 zod/Schemastery**；`Config` 才是 Schemastery。`register` 注释原文："Scoped tools shadow globals; duplicates within one layer and the reserved `run_code` name fail."
- 工具策略**弱→强**：`restrict()`（只能移除）→ `guard()`（只能拒绝，同步）→ waterfall 监听器（可改写，依赖注册顺序）→ `system-prompt/assemble`（替换整段，**不要用来增删工具/文本**）。waterfall 监听器**不拥有决策就必须 `return next()`**；改写 `agent/pre-step` 决策要展开（`{ ...decision, messages }`）保住 `startsRequestSeries` 等字段。
- **设置**：命名空间 = **Loader 条目 id**（不是任意字符串）；非该 profile 条目抛 `No configurable plugin entry "<ns>"`；**只有 volatile 字段可写**，否则抛 `Config field "…" is not volatile`；带 `expectedRevision` 时 revision 不符抛 `SettingsConflictError`；变更广播 `settings/document-updated`。
- **HTTP 面默认无鉴权**：`dsh-host-webserver` 自身不做鉴权/Origin 校验；真正的门是 `dsh-client-connection` 的 `isTrustedApiRequest`（必须有 Host、必须 loopback 或 trustedHosts、`sec-fetch-site: cross-site` 直接拒、有 Origin 时须同 host）+ 浏览器会话令牌（`token` query / `dsh-auth-` cookie）。前端静态服务自己调 `ctx.connection.authorizeIndex(req,res)` ⇒ **插件自注册的路由默认不过这道门，要自己加门**。重复 `(kind,path)`、重复 upgrade path、第二次 `registerFallback` 都是抛错。
- **会话投递**：`sessions`(日志/持久化) → `agents`(活体注册表) → `agent.session` 同一个 Session；`sessionController.resolveAgent(sessionId)` 是宿主侧门面（失败返回 `{error}` 而不是静默）。`followup/steer/inject/send` **全部 fire-and-forget 返回 void**，失败不在调用点抛；`inject` **不唤醒** agent，要唤醒用 `followup`；投递**不保证落盘**，要自己 `await ctx.sessions.flush(session)` 并检查返回值。消息来源用 `createUserMessage({content, source:{kind, form, summary}})`；`form: "notice"` + `kind: "plugin:<包名>"` 是产品既有的语义通道（模型看到完整 content，聊天 UI 渲染成 inject 折叠行，不会变成"用户气泡"）。注意：**`plugin/notice` 作为服务或事件并不存在**，别把它当 API 名去找。
- **会话事件词表**：`dsh-session/lib/types/known-event-types.js:21-81` 的 `KNOWN_SESSION_EVENT_TYPES`（56 个：`turn/start`、`tool/call`、`tool/result`、`user/message`、`assistant/message`、`todo/write`、`goal/change`、`approval/asked`…）。**仓库外插件自造的会话事件类型不在集合内**，读路径会拒绝解释，除非事件带 envelope 的 `ignorable` 标记——而 live `Session.append()` 无法设置该标记，session 会拒绝重开。所以**不要用新 `type` 追加 session 事件**；要派生状态放 `ctx.sessionProjections`，插件自有数据放 storage 服务。
- **per-session 派生状态**放 `ctx.sessionProjections` 单元（`apply(state,event)` 纯且同步，忽略的事件返回同一引用；`view()` 值不变返回同一引用以抑制发布；字段/折叠语义变化时 bump `stateVersion`）。等 `turn/end`、`assistant/message`、`tool/result` 这类**持久**事件；实时 token 用 `agent/assistant-stream` 渲染；**不要 poll `agent/status`**；`whenIdle()` 不代表一次 follow-up 结束。
- **per-agent 行为**：在 `agent/created` 拿到的 `agent.ctx` 上注册，包一层 `agent.ctx.effect()`，**同时**把 disposer 存进自己插件的 effect（两个所有者，任一侧拆除都能移除）。加 prompt 文本用 `ctx.systemPrompt.section()`。

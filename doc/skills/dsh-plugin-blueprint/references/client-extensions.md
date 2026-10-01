# 浏览器半边：姿势 → 构建 → 装载 → 运行 → 更新（+ 公开面）

> 读它的时机：要写客户端半边、要挂 UI、或要判断"这个能力浏览器侧能不能做"。

## 0 姿势（先看这段）

- **slot 是唯一的挂载面**：`ctx.slots.inject(ownerKey, () => ctx.slots.register({name, id, order}, Component))`（回调里的注册在 owning 声明塌缩时被 dispose、回归时重装）。选**已分配空间**的 slot（如 `conversation.composer.dock`），不要围绕宿主控件规划覆盖层；只有位置已知时才用 `shell.overlay`。
- **主题**：只用 `--dsw-alias-*` / `--dsw-*` token（`cordis_inspect_query` 的 `Theme` 可列全）；字面颜色只用于 artwork。容器与控件必须继承宿主主题。
- **文本**：可见 UI 文本走 Client locale 服务。
- **Chat 行**：`ctx.uiConversation.events.register()` 定义事件 + 在 `conversation.chat.node` slot 按 `kind` 注册视图（分页/Turn/Step 位置与增量组装由 Conversation 层拥有）。
- **服务端派生值**：在宿主投影上声明 `wire.view`，值算好再送客户端，**不要让客户端自己折叠 session 事件**。
- **可参考的 slot 所有者包**（完整客户端包清单见官方 `cordis-composition-reference/references/packages.md` 的 `## client` 组）：`ui-layout`(三栏 AppFrame + `ctx.layout`)、`ui-conversation`(composer/shell/queue)、`ui-settings`(设置域与 slot 类型契约)、`ui-sidebar*`(左右栏/文件树/终端)、`ui-chat`/`ui-tool`/`ui-trajectory`(对话与工具渲染)、`ui-theme`、`ui-commands`/`ui-input-trigger`、`ui-renderer`(React slot 绑定与应用根)。

## 1 构建

必须发布**已构建的经典脚本** `lib/client.js`（`scripts.bundle = tsdown`），`exports["./client"] = { types, default }`，`files` 白名单含它。源码里的动态 `import()` 被切成 `require.async("./client.<name>.js")` 独立 chunk（如 `ui-sidebar-terminal/lib/client.terminal.js`，各自第 1 行同样调 `load()`）。宿主只服务已构建产物，没产物就是激活期响亮失败并列出包/路径。

## 2 装载（宿主侧组装 + 内核）

宿主半边 `dsh-client-modules` 组装 boot graph —— `ctx.on("internal/plugin", fiber => dirty.add(...))` 标脏、注册 `/plugins` prefix 路由、`ctx.on("webserver/index-inject", t => t.push(...))` 注入 4 类行：① head 内联 queue 脚本（装 `window.__ModuleLoader__ = {mode:"queue", pendingQueue, load(), create()}`）② 每个 `phase==="application"` batch 一条 `script-preload` ③ 每个 `phase==="bootstrap"` batch 一条 `script-src` ④ `{kind:"global", name:"__DSH_BOOT__", value: graph}`。渲染在 `dsh-host-webserver/lib/index.js:74-93 renderIndexInjections`（head 行插 `<head>` 后、body 行插 `<body>` 后，末尾 `READY_MARKUP` resolve `__DSH_BOOT_READY__`）。

**内核**（不在任何插件里，在 web 前端 shell 产物内）：等 `__DSH_BOOT_READY__` → `__ModuleLoader__.create({ boot, staticModules, loadBundle })` → `ctx.loader.internal = modules` → `await modules.entries.start(loader, manifest)` → `loader.await()` → 挂应用 = `await ctx.inject(["uiRenderer"], o => o.effect(() => o.uiRenderer.mount(container), "web boot: application mount"))`。

- **隐式 baseline（9 条，唯一能直接 require 的）**：`react`、`react/jsx-runtime`、`react-dom`、`react-dom/client`、`@deepseek-ai/cordis`、`@deepseek-ai/dsh-client-store`、`@deepseek-ai/dsh-client-ui-slots`、`@deepseek-ai/dsh-client-ui-primitives`、`@deepseek-ai/dsh-client-ui-dockkit`。超出这些的请求要在 `dsh.client.external` 声明，且只能请求**已存在**的模块行。
- **激活失败的权威文案**：`web boot: N entries did not activate`，逐条 `"<id>: import failed: <msg>"` 或 `"<id>: pending (waiting for service: <names>)"` —— 后者就是"缺 `inject` 或依赖顺序不可满足"，不用猜。

## 3 `load()` 契约

`load({ id: string /*= 包名，必须等于正在执行的 graph row*/, chunk?: string, factory(require) })`；`require` 同步解析 + `.async()` 加载包内 chunk。**执行 bundle 只登记 factory，模块体副作用（含 CSS 注入）在 materialization 时才跑一次**（loadCache 记忆化）。

## 4 公开服务目录（权威，产品自带）

`dsh-cordis-client-runner/lib/client.js:1125-1710` 的 `SERVICE_API` 只登记 **8 个**：`layout`、`locale`、`sessions`、`slots`、`theme`、`timer`、`uiWorkspace`、`workspaces`；`EVENT_API` 只登记 **4 个**：`connection/reset`、`locale/change`、`slots/changed`、`theme/change`。**这 8+4 就是产品对插件作者承诺的公开面**；`remote`、`configForms`、`uiRenderer`、`connection`、`modules`、`resources`、`shortcuts` 等由插件自己 `provide`，属半内部。

- `slots`：`register(options, component) / registerFactory / inject(key, cb) / entries(key) / spec(key) / subscribe(key, fn) / provideRoot / renderSlot(key, owner)`（`renderSlot` 只允许 `"root"`）。**注册到未声明的槽必 throw**；`inject` 的回调在槽声明提交后跑、折叠时重跑，返回幂等 disposer；变更发 `ctx.emit("slots/changed", key)`。`SlotKind = 'single'|'list'|'keyed'|'chain'`，scope `'root'|'session-maybe'|'session'`。
- `theme`：`getTheme()`（引用稳定）、`setTheme(id)`（未注册 id 抛 `theme "<id>" is not registered`）、`setFontSize(px)`（整数 10..22）、`register(definition)`、`overrideTokens(source, tokens)`；变更发 `theme/change`。token 落地在 `ui-layout` 的 ThemePresenter：写 `documentElement.style.colorScheme`、`data-ds-theme-source`、body `data-ds-dark-theme`、`--dsh-content-font-size` 与逐个 `--dsw-*`。
- `layout`：`selectPanel(panelId)`（未注册的 main panel 抛错）、`beginNavigation()`、`toggleSidebar()`、`openRightbar(track, fullscreen)`、`closeRightbar()`。`root` 槽与其 5 个子槽（`sidebar`/`main`/`rightbar`/`shell.overlay`/`shell.leading`）由 layout 声明。

## 5 UI 扩展点姿势

- **加页面** = 在 `main` 槽注册 keyed 条目（`key` 即 panel id）+ `sidebar.panellist` 放入口 + `ctx.layout.selectPanel(id)` 打开；官方完整配方见 `dsh-client-ui-schedule/lib/client.js:6793-6810`。
- **加设置项** = `settings.general.item`（官方 `appearance` 例子：`dsh-client-ui-theme/lib/client.js:1603-1610`，`order:10`）或 `settings.section`；插件自己的设置页用 `settings.plugins.tab`。
- 官方带示例的槽集合（含 `declaredBy`/`occupants`/`replaceRisk`/`example`）在 `dsh-cordis-client-runner/lib/client.js:2363-2700` 的 `CLIENT_SLOT_API`，`source` 字段直接指向原仓库 `packages/client/*/src/client/contract/slots.ts:N`；`replaceRisk:"shadows-shipped-ui"` 表示覆盖会替换官方 UI。

## 6 跨半边

正规方式只有 `remote`（`connection.rpc.call("/api", endpoint, {args}, signal)` → `{ok:true,value}` / `{ok:false,error}`；流式走 `/api/remote.mux`）。**第三方不能新增 Host RPC 端点**，只能用已 mounted 的 typert namespace（`ctx.remote.<ns>.<method>`）；`validateContribution` 拒绝重复方法与命名空间冲突。

## 7 更新（HMR，`pnpm run dev:web`）

Host 侧 `dsh-client-hmr` 开 `/plugins/events`（SSE，默认 poll 500ms）先发 `{type:"graph"}`、变更发 `{type:"rebuilt", id, rev}`；浏览器侧 `new EventSource` → `graph` 走 `entries.sync`、`rebuilt` 走 `entries.reload`。换装算法 `replace()`：`invalidateForReplacement` → `prefetch` → `tearDownEntryFiber` + `removeOwnedStyles` → `import` → `entry.refresh()` + `fiber.await()`。**成功换装不需刷新页面、不需重启 Host；但没有失败回滚** —— 旧 fiber 已拆，新 import/激活失败就停在 failed，只能 `retry()` 或等下一帧 graph。

## 8 页面全局契约（插件不要 `provide` 覆盖）

`__DSH_BOOT__`、`__ModuleLoader__`、`__DSH_BOOT_READY__`、`__DSH_CONNECTION_RECOVERY__`、`__DSH_TRANSPORT__`（后者由嵌入载体如 Electron preload 提供，Web 路径退化同源）。

## 9 浏览器侧独有判据

1. **算不算客户端插件** → 看 `package.json` 的 `dsh.client.platform === "web"` 且 `exports["./client"]` 能解析出 string；不满足就永远进不了 boot graph。
2. **能不能激活** → 看控制台 `web boot: …` 那行。
3. **某槽能不能注册** → 未声明的槽必 throw，`CLIENT_SLOT_API` 的 `declaredBy` 写明谁声明了它。
4. **某服务是不是公开契约** → 查 `SERVICE_API`/`EVENT_API`。
5. **要不要刷新页面** → 只有不走 `slots`/`entries` 的一次性 DOM 挂载才需要。
6. **客户端能不能干 X** → 先定位实现在哪个半边（`lib/index.js` = 必须经 `remote`；`lib/client.js` 且只用 DOM/fetch = 可独立完成）。

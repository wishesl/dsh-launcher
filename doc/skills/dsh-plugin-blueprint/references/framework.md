# 大框架结构（profile → bundle → patch → entry，Cordis 内核，双半边）

> 读它的时机：定"层/半边/扩展点"之前。读完后应该能回答：这个插件落在哪一层、被谁装载、模块能活多久。

## 1 落地分层：profile → bundle 层 → patch 层 → Loader entry

```
$DSH_HOME/profiles/<name>/            # 一个 profile = 一次组合的落盘物
  package.json         # dependencies + dsh.profile.bundles（有序数组 = 层顺序）
  cordis.yml           # 每次启动被重写成空 []，只作 Loader include root/baseUrl 锚点
  cordis.patch.yml     # 用户层，在所有 bundle 层之后应用（最高优先级）
  compatibility.json   # "name@version" → 精确 DSH 运行时版本数组
  pnpm-workspace.yaml  # packages:[.] / nodeLinker: hoisted / autoInstallPeers: false
  node_modules/        # 宿主 Loader 只从这里解析插件包
  .dsh-builtin/ .dsh-market/ .dsh-mcp-lazy/ .plugin-manager/
```

- 内置模板：`web = [dsh-base, dsh-web-app]`，`headless = [dsh-base, dsh-headless]`，另有 `acp` / `sdk` / `sdk-minimal`。
- **层序（低→高优先级）**：`profile.layers` 各 bundle 的 patch 列表（按 `dsh.profile.bundles` 数组顺序）→ profile 的 `cordis.patch.yml` → `$DSH_HOME/cordis.patch.yml` → `--patch` overlay → telemetry 关闭 patch。
- **bundle** = `package.json` 声明 `dsh.bundle.patch`（一个文件或**有序文件列表**）的包。`@deepseek-ai/dsh-base` 的 `lib/index.js` 只有 `export {};` —— **bundle 的实体是 YAML patch，模块本身没有运行时 API**。
- **entry（行）** = `{ id, name(包 specifier), config, disabled, inject, intercept, isolate }`。`group: true` + `name: cordis:group` 让 `config` 变成可被按 id 插入的嵌套行列表；`cordis:include` 从 `config.path` 加载字面 YAML/JSON 行列表。
- patch 只有三种形态：`{insert:[...]}` 追加根 / `{id:<group>, insert:[...]}` 追加进该 group 的 config / `{id, ...字段}` 覆盖已有行。
- **`config` 是整体替换，永不深合并**。想改一行 config 的一个子键，必须重述该行需要的每个字段。官方原话（`dsh-base/cordis.patch.yml:6-10`）："A patch replaces the targeted row's whole `config` rather than merging into it."
- `disabled` 可为 bool / null / `!!js` 表达式（每次挂载决策时对该行 context 求值）；`!!js` 是 Loader 表达式，**不是 `!js`**，且只在 `config` 内按行内 context 求值。
- `dsh.*` 清单**只有 4 个键**：`manifestVersion`、`bundle.patch`、`profile.bundles`（只写在 profile 自己的 package.json）、`client.{platform,inject,immediately,external}`。没有 `dsh.server` / `dsh.tool`。
- 兼容性**只看 `peerDependencies`**；`engines.dsh` 与 `dsh.manifestVersion` 是声明式的，安装器与 Loader **都不强制**。
- 可复制的 manifest / 入口骨架见 `templates.md`。

## 2 Cordis 4.0.4 内核模型

分层：`Context`(Proxy 服务解析 + isolate/intercept) → `RegistryService`(插件注册) → `Fiber`(生命周期状态机 + effect 清理树) → `ReflectService`(服务表 + 依赖通知) → `EventsService` → `LoggerService`。

- 每个 `ctx.plugin(p)` **无条件 `new Fiber`**（runtime 复用、fiber 不去重）⇒ 同一插件可被装载多次，模块级变量会被互相踩。
- `FiberState`：`PENDING=0, LOADING=1, ACTIVE=2, FAILED=3, DISPOSED=4, UNLOADING=5`（运行时不导出，别把数字当 API）。
- fiber 声明 `inject` 后按**依赖是否就绪**在 PENDING↔ACTIVE 间自动加载/卸载；**提供方被替换也会触发重载**。可选依赖放进 `inject` 或 `ctx.inject([...], cb)`，缺服务的 profile 里插件保持 inactive 而不是抛错。
- **`ctx.effect(fn, label)` 是唯一的资源清理原语**：没有 `ready`/`dispose` 事件（`ctx.on('dispose')` 只会注册一个永不触发的监听器），没有 `ctx.off`（用 `on()` 返回的 disposer）。fiber 进入 UNLOADING 后再建 effect 会抛 `cannot create effect on inactive context`。
- 服务解析严格模式要求**提供方 fiber 处于 ACTIVE**，否则抛 `cannot get required service "<name>" in inactive context`；没写进 `inject` 的名字抛 `cannot get property "<name>" without inject`。这两条错误串是定位"脏 ctx"问题的关键指纹。
- 服务名不能以 `_` 开头，不能叫 `then` / `prototype`，不能是纯数字；同一 isolate label 内 `provide` 重名直接抛 `service "<name>" has been registered at <...>`。
- **没有 `ctx.config`**：用插件第二参数 `config` 或 `ctx.fiber.config`。配置在**每次激活时**校验并填默认值。
- `ctx.on` 返回 disposer；监听器与服务都挂在**当前 fiber** 上，想活过子插件卸载必须注册到 root/父 ctx。
- internal 事件全集（就这 9 个）：`internal/plugin`、`internal/status`、`internal/config`、`internal/service`、`internal/update`、`internal/get`、`internal/set`、`internal/listener`、`internal/dispatch`。`internal/plugin` **创建与销毁都会触发**，不能当"装载成功"用。
- 内核**不提供**文件 IO / 网络 / 定时器 / 持久化；定时器是独立包 `cordis-plugin-timer`，且 `ctx.timeout` 只有**注入 timer 后的 ctx** 才有（用 apply 的 ctx 调会抛 `cannot get property "timeout" without inject`）。
- `cordis-plugin-timer` 的 `ctx.timeout` 把 `setTimeout` 注册在 **timer 服务自己的 ctx** 的 effect 上 ⇒ **不随调用方 fiber 销毁而取消**。想在插件里用定时器：`ctx.inject(["timer"], ...)` 拿注入 ctx、自己记账 disposer、在 `ctx.effect` 里取消、回调 try/catch。

## 3 一个插件的三种形态

**① 宿主半边 · 工具/普通插件 = ESM 四个具名导出（无 default）**

```js
import z from "@deepseek-ai/schemastery";              // 默认导入，不是 { z }
const name = "tool-todo";                              // 插件名 ≠ 包名
const inject = ["tools", "sessionProjections"];
const Config = z.object({ ... });                      // 没有字段也必须写
function apply(ctx, config) { /* 所有资源在这里注册 */ }
export { Config, apply, inject, name };
```

**② 宿主半边 · Service 插件 = `export default class extends Service`**

```js
export default class WebServer extends Service {
  static Config = z.object({ port: z.natural().required() });  // 类形态用 static
  static inject = ["profileContext"];
  constructor(ctx, config) { super(ctx, "webServer"); }        // service key 可与包名不同
  register(route) { /* 重复注册即抛错；返回 disposer */ }
  async [Service.init]() { /* 异步激活，如 listen */ }
}
```
类形态**不导出** `name`/`inject`/`Config`；`Service` 的 `instanceof` 被改写（沿 prototype 链比对）。

**③ 浏览器半边 = `window.__ModuleLoader__.load({...})`**

```js
window.__ModuleLoader__.load({
  id: "<package.json 的 name>",          // 必须完全一致
  factory(require) {                     // lazy：模块副作用延迟到首次物化
    const React = require("react");      // 平台模块表提供，不要自己装 React
    return { inject: ["slots"], apply(ctx) { /* ctx.slots.inject / ctx.effect */ } };
  },
});
```
`factory` 的返回值就是插件对象（等价于手写 `exports.apply = apply; exports.inject = inject`）；客户端半边**没有 Config、没有 name**。样式自己注入 `<style data-plugin-css="<pkg>/<file>">` 去重。

## 4 双半边插件的真实语义（最容易踩）

判据：`package.json` 有 `dsh.client` **且** `exports["./client"]` 指向真产物；两侧产物互不 import。缺 `./client` 直接抛 `client-modules: <pkg> declares dsh.client but exports no "./client" bundle`。

一旦声明 `dsh.client`，就会同时发生：

- 宿主**重复装载服务端半边**（同一 pid 出现两条"已装载"）。实测：桥接 hello 计数 2 → 3。机理未逐行确认（见 `verification.md` §未确认清单），按事实处理即可。
- HMR 重载会**清掉 ESM loadCache 与 CJS require.cache 重新求值** ⇒ 模块顶层变量重置、config 保留。
- 客户端模块副作用延迟到首次物化；`dsh.client.inject` 只是**包名依赖信息**（"not Cordis service injection"）。
- 替换已安装包的版本**必须重启**才能加载新 JS 模块代；不要因为 slot id 没变就以为浏览器代码已更新。

⇒ 服务端半边必须按"随时可能被拆掉重来"写：状态落**进程级（模块级）变量**且可重入，定时器自持 disposer、注册进会话记账、随 fiber 销毁取消，回调 try/catch 吞异常，跨会话的待办在新会话里补做。

## 5 装载链路与失败面

`dsh web` ≡ `dsh --profile web` → `runProfile()` → `composeProfile()` → `boot()` → `ctx.plugin(Loader)` → `mountRootInclude()` → `ctx.loader.create(rootInclude)` → `loader.await()` → `auditStartupEntries()`。

- bundle 目录解析**先 installAnchor（dsh 安装目录），再 profile/package.json**；解析不到即抛 `cannot resolve profile bundle "…"; run 'dsh plugin --profile <name> install'`。
- 单个 bundle 出错 → 进 `skippedBundles`，**不中断启动**；但 **required entry**（`agent-loop`、`webserver`、`modules`、`connection`、`headless-runner`、`acp`、`sdk-jsonrpc-server`）激活失败会 dispose 整个 app 并非零退出。
- `dsh plugin add/remove` 只是**转发给 pnpm**（cwd = profile 目录，包管理器固定 `pnpm`，`package.json` 文件锁 12e4 ms）；它会改 `package.json`、把新 bundle **追加到 `dsh.profile.bundles` 末尾**（= 最高层优先级）、写 `pnpm-lock.yaml`/`node_modules`；插件开关写 profile 的 `cordis.patch.yml` 的 `disabled`。失败时还原 `package.json` + `pnpm-lock.yaml` 并报 `dsh: installation rejected: …`。
- HMR 只监听 profile `package.json`（且只有 **bundles 列表变化**才重组）、`profile.cordis.patch.yml`、`$DSH_HOME/cordis.patch.yml`。

## 6 三条"正规能力通道"

| 通道 | 用途 | 怎么接 |
|---|---|---|
| Cordis service / event | 一切基础：注册工具、路由、设置、投影、事件 | `ctx.provide` / `inject` / `ctx.on` / `ctx.effect` |
| **typert** | 给能力加 schema 校验、让 `cordis_inspect` 与跨 face 看见 | 包导出 `./typert`（`face:"host"`、`schemas[].create()`、`invocations` 严格校验）；行激活即自动 `ctx.typert.register` |
| **`webserver/index-inject`** | 服务端**唯一**能碰浏览器的口子（注入 `window.__X__`、样式、脚本） | `ctx.on("webserver/index-inject", t => t.push({kind, placement, text}))` |

浏览器→宿主的运行时通道走 `@deepseek-ai/dsh-client-connection`（已认证 RPC 传输）；插件自己的即时通道（如 WebSocket）是允许的，但要自己处理鉴权/Origin/生命周期。

# 权威错误串速查（错误串 → 含义 → 修法）

> 读它的时机：现场报错看不懂，或想确认自己踩的是不是已知坑。全部为产品源码/运行时原文，可直接 grep。

## 1 Cordis 内核 / 生命周期

| 错误串 | 含义 | 修法 |
|---|---|---|
| `cannot get required service "<name>" in inactive context` | 提供方 fiber 不在 ACTIVE（多半是自己已 dispose / 依赖被替换） | 别在脏 ctx 上取服务；把依赖写进 `inject` 让它自动重载；定时器用 `ctx.inject(["timer"], ...)` 拿到的 ctx |
| `cannot get property "<name>" without inject` | 该名字没写进 `inject` | 加 `inject`，或用 `ctx.inject([...], cb)` 拿可选依赖 |
| `cannot create effect on inactive context` | fiber 已进入 UNLOADING/DISPOSED 后还建 effect | 所有 effect 在 `apply` 期间建；异步回调里不要再建 |
| `service "<name>" has been registered at <...>` | 同一 isolate label 内 `provide` 重名 | 换服务名，或用 `isolate` 隔离命名空间 |
| `cannot get property "${prop}" without inject` | 与第 2 行同源（仅当名字在 `inject` 里才改写成 required 版） | 同上 |

## 2 装载 / 组合 / 客户端 boot

| 错误串 | 含义 | 修法 |
|---|---|---|
| `cannot resolve profile bundle "…"; run 'dsh plugin --profile <name> install'` | bundle 目录在 installAnchor 与 profile 里都找不到 | 按提示跑 install；别手改 profile |
| `client-modules: <pkg> declares dsh.client but exports no "./client" bundle` | 声明了 `dsh.client` 但没有可用 `./client` 产物 | 补 `exports["./client"]` + 构建产物，见 `templates.md` |
| `web boot: N entries did not activate` | 客户端有行没激活，后面逐条给原因 | 看下面两条 |
| `"<id>: pending (waiting for service: <names>)"` | 缺 `inject` 或依赖顺序不可满足 | 补 `inject`；确认提供方也在 graph 里 |
| `"<id>: import failed: <msg>"` | 模块加载/求值抛错 | 读 `<msg>`；确认产物已构建、路径正确 |
| `web boot: unknown index injection row …` | `index-inject` 行形状不合法 | 用官方 4 类行形状 |
| `client-modules: window.__ModuleLoader__.create called after module-system boot` | queue facade 被重复创建 | 不要自己调 `create()` |
| `client-modules: HTML did not preload @deepseek-ai/dsh-client-modules/client.js` | 页面缺 queue facade | 走正常 `webserver/index-inject`，别自造页面 |
| `.../client.js requested external "<spec>" before the module system existed` | 早期执行期请求了外部模块 | 把请求放进 `factory` 内 |
| `.../client.js did not export the bootstrap module face` | bundle 没有导出 boot 面 | 检查构建产物入口 |
| `slot entry crashed in '<slot>'` | slot 组件抛错 | 修组件；不要依赖 Harness Client 包（会无预告变化） |

## 3 宿主服务 / 设置 / 路由

| 错误串 | 含义 | 修法 |
|---|---|---|
| `webserver: duplicate ${kind} route "${path}"` | `(kind,path)` 重复注册 | 换路径或用 `tapIndex`/fallback 的正确姿势 |
| `No configurable plugin entry "<ns>"` | 设置命名空间不是本 profile 的 Loader 条目 id | 用行 id 作命名空间 |
| `Config field "…" is not volatile` | 写了非 volatile 字段 | 只写 volatile 字段；改 schema 声明 |
| `SettingsConflictError` | `expectedRevision` 与当前 revision 不符 | 重读 revision 或省略 `expectedRevision` |
| `no catalogued Service named "${key}"` | 客户端 `queryServiceApi` 查了不在 8+4 目录里的服务 | 那是半内部服务，不要依赖 |
| `theme "<id>" is not registered` / `theme "<id>" is already registered` | 主题 id 未注册 / 重复注册 | 先 `register`，id 唯一 |
| `font size ${px} is outside 10..22` | 字号越界 | 收进 10..22 整数 |
| `layout.selectPanel: main panel "${panelId}" is not registered` | 打开未注册的 main panel | 先在 `main` 槽注册 keyed 条目 |
| `FS_SANDBOX_DENIED` | 模型工具触发了 fs 沙箱策略 | 与插件沙箱无关（插件本身无沙箱） |
| `dsh: installation rejected: …` | 安装被拒并已还原 `package.json`/`pnpm-lock.yaml` | 看后半段原因；走 `plugin_manager install_bundle` |

## 4 patch 诊断（都是 warn，不中断启动）

| 警告 | 含义 |
|---|---|
| `patch insert: entry %C not found` | `insert` 带了 id 但树上没这行 |
| `patch insert: entry %C is not a group` | `insert` 目标行不是 `group: true`（**只警告跳过**） |
| `patch: id is required for non-insert patches` | 非 insert 补丁没写 id |
| `patch: entry %C not found` | 覆盖目标行不存在 |
| `patch: name mismatch for %C (expected %C, got %C), skipping` | 覆盖时 `name` 与目标行不符 ⇒ 跳过 |

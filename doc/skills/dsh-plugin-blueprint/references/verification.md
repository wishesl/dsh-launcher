# 用源码论证可行性：规划六步 → 取证 → 证伪 → 表述 → 未确认

> 读它的时机：写代码之前、评审方案时、以及"我怀疑这个假设"时。

## 1 规划六步（写代码之前）

1. **解析目标与作用面**：谁用、在哪出现（未指定视觉去向 = 当前 Web UI）、成功判据是什么。
2. **定层**：新 bundle 还是覆盖已有行？—— 覆盖会整体替换 `config`，所以要列出该行全部字段；要插进 group 必须先确认那行 `group: true`。
3. **定半边**：只用宿主（工具/后台）？只用浏览器（纯展示）？还是双半边？—— 双半边请先读 `framework.md` §双半边。
4. **列扩展点并逐个确认签名**：`cordis_inspect_list` → `cordis_inspect_query`（`Config` / `Service` / `Event` / `Slots` / `Theme`）。**已安装的 Harness 才是权威**。没有 inspect 工具的会话里退回读 `lib/types/*.d.ts` + 包 README。
5. **写最小可运行版本并装上去**：不要先造 mock/预览/变体；第一次预览就是装好的插件本身。装完读 **`application` 与 `warnings`** 字段判断是否生效（不是看日志），再用 inspect 确认新行存在。
6. **锁回归**：任何跨装载/时序类行为都要先写本地 harness（假 ctx + 真插件代码）再改代码，并做**反向验证**（把实现改回旧语义，harness 必须 FAIL）。

## 2 取证顺序与机械技巧

顺序：**inspection → 包 README → 源码**。安装包只带 `lib/index.js` + `lib/types/**/*.d.ts` + JSDoc，没有 `src/`；只有源码 checkout 才有 `packages/<group>/<name>/src`。只有在前两步仍有疑问、且从具体安装/运行时故障入手时才读源码。

- 找包 README：`cordis_inspect_query` 的 `Config.listConfigs`（带 `name`）拿 entry → 取 `packageDir` → 读 `<packageDir>/README.md`。**绝不从 `$DSH_PROFILE_DIR` 猜路径**。
- 找包绝对路径：`<pnpm>@<scope>+<name>@<version>_<hash>\node_modules\<scope>\<name>\`，目录名会把包名**截断到约 20 字符**再拼 `_<hash>`（`@deepseek-ai+dsh-tool-cordi_8562…`）⇒ 通配要用 `前缀*`，别用完整名 `-like` 匹配。可先落一张 `包名<TAB>绝对路径` 映射表复用。
- `dsh-vsn/` 之类被 `.gitignore` 忽略的目录，`grep` 工具会跳过 ⇒ 用 pwsh `Select-String -Recurse` 或 `read` 绝对路径。
- `dsh --profile "$DSH_PROFILE" --dump-config` 打印合成后的 profile；`DSH_PROFILE`/`DSH_PROFILE_DIR` 在每个 profile 启动的 shell 调用里都有。
- 有些包**同时发布 `src/*.ts`**（cordis 就是），src 比打包后的 `lib/index.js` 精确，优先读 src。
- 契约载体是 `lib/types/*.d.ts`（`lib/index.js` 是构建产物）。断言某个服务存在，就去搜 `declare module '@deepseek-ai/cordis'` 里有没有那一行。
- 行号是**版本快照**：升级后 `lib/index.js:NNN` 会漂移，检索按符号名 grep（`applyEntryPatches`、`sessionProjections.register`）。
- 运行时事实优先用已认证且已连接的 Harness 页面验证；无浏览器控制时，视觉请求的验证限于 JS 语法、manifest 校验、活的 slot，**不要**启动另一个浏览器、改 `HOME`、搜 token、找光栅化器或自造渲染器 —— mock 页面的截图不是验证。

## 3 证伪清单（先假设，再去打掉）

| 常见假设 | 判据 | 结论 |
|---|---|---|
| 同一插件只装载一次 | `registry.ts` 里 `new Fiber` 是否无条件执行 | ❌ 可多实例（`dsh.client` 会主动重复装载宿主半边） |
| 有 `ready`/`dispose` 事件可监听 | 搜 `'ready'`/`'dispose'` | ❌ 只有 `ctx.effect` |
| `ctx.config` 可读配置 | 搜 `ctx\.config` | ❌ 用第二参数或 `ctx.fiber.config` |
| `Config.merge` 是 schemastery API | 看 schemastery 的 `Static` 接口 | ❌ 是服务自备约定，缺失时浅合并 |
| 脏 ctx 上还能拿服务 | dispose 后再 `ctx.timer` | ❌ 抛 `cannot get required service "…" in inactive context`（抛 `without inject` 说明没写进 inject） |
| patch 能深合并 config | 看 `applyEntryPatches` | ❌ 整体替换 |
| 能 insert 进非 group 行 | 看 `if (!target.group)` | ❌ 只警告跳过 |
| 同一 entry 在 web/headless 各一份 config | 同 id 后写覆盖 | ❌ 只能一份，或 `!!js` 依 `profileContext.name` 分支 |
| `engines.dsh` 能拦版本 | `evaluatePluginCompatibility` 读什么 | ❌ 只读 `peerDependencies`，不强制 |
| 热替换已安装包版本不重启 | HMR / plugin-manager README | ❌ 必须重启 |
| 插件包能声明自己的 bundle 依赖 | 谁读 `dsh.profile.bundles` | ❌ 只读 **profile 自己**的 |
| 设置能任意命名空间/字段写 | `dsh-settings/lib/index.js:504/507/511` | ❌ 非 profile 条目、非 volatile 字段、revision 不符都是抛错 |
| 往会话投内容会被等到/会报错 | `dsh-agent/lib/types/runtime-types.d.ts:186-209` 全返回 `void` | ❌ fire-and-forget；只有 `resolveAgent` 返回错误对象、`sessions.flush` 返回布尔 |
| 能加新的 session 事件类型 | `KNOWN_SESSION_EVENT_TYPES` 里有没有 | ❌ 仓库外类型不在集合内，需 `ignorable` 且 live append 设不了 |
| 我注册的 HTTP 路由自动安全 | `dsh-host-webserver/lib/index.js:132-137` 无鉴权 | ❌ 要自己调 `ctx.connection.authorizeIndex` |
| 想给前端暴露能力不走 typert | `dsh.client.external` 规则 | ⚠️ 可行（自定义路由 + external），但 external 只能请求**已存在**的模块行、不能声明自己；要被 inspect 看见/有 schema 校验必须走 `./typert` |
| 加一个新的 `dsh.xxx` 字段就能被宿主识别 | 全仓只有 `manifestVersion`/`bundle`/`profile`/`client` 四键被读 | ❌ 新键没有读取点 |
| `dsh.client` 能让插件在 headless 跑 | headless 不挂 modules/webserver | ❌ 它只影响浏览器 roster（`__DSH_BOOT__` 的行清单） |
| 改插件源码 HMR 会自动生效 | `dsh-base/cordis.patch.yml` 里 hmr 的 `root` 默认空 | ❌ 要显式配 `root: ["."]`；headless/acp 还显式 `disabled: true` |
| `dsh plugin add` 不碰 `dsh.profile.bundles` | `reconcile` 把有 `dsh.bundle` 的新依赖追加到列表末尾 | ❌ 会追加（= 最低优先级层） |
| 某行写在 `dsh-web-app` 的 patch 里就等于"只在 web 生效" | 层序 = bundle 层 → profile patch → home patch → `--patch` overlay | ⚠️ 更后的层可用同 id 覆盖它，要按层序核实 |
| slot id 没变就说明浏览器代码已更新 | 模块代在进程内缓存 | ❌ 换已安装包版本必须重启 |
| `dsh.client.external` 能声明自己的新模块行 | 解析只认 boot graph 里已有的行 | ❌ 只能请求已存在的模块行 |
| `renderSlot` 能渲染任意槽 | `renderSlot` 只允许 `"root"` | ❌ 其它槽走 slot entry 组件 |
| 客户端半边能 import 任意包 / 用 Node API | 动态包更严：`require` 被拒、`TIMER_REDIRECT`、未声明服务 `rejectGuard`、Context 一律拒 | ❌ 只用 baseline 9 条 + `dsh.client.external` 声明的已存在行 |

## 4 结论表述规范

每条结论写成：**断言 → `绝对路径:行号` → 代码原文或错误串 → 反例/复现方式**。拿不到证据的写进下面这张清单，不要写成结论。

## 5 未确认清单（知道边界在哪，别当结论用）

| 未确认项 | 现状 |
|---|---|
| `dsh.client` 导致宿主半边**重复装载**的逐行机理 | 现象已实测（桥接 hello 计数 2→3、同 pid 两条"已装载"）；`registry.ts` 无条件 `new Fiber` 是必要不充分解释，触发第二次 `ctx.plugin` 的确切代码路径未定位 |
| `__DSH_TRANSPORT__` 由谁注入 | 前端 README 指向 Electron 载体（preload）；Web 路径退化同源，注入点未逐行确认 |
| `/api/remote.mux` 的服务端帧协议 | 只知道是流式 RPC 通道，帧格式未读 |
| 非会话槽的 `registerOptions` 全字段 | `SlotKind`/scope 已知，其余可选字段未穷举 |
| `CLIENT_MODULES_ID` 字面值与 boot 根插件 `mf` | web 前端产物经压缩混淆，未还原 |
| 自造 session 事件类型时 `ignorable` 的确切位置 | 结论（读路径拒绝解释、live append 设不了）已确认，envelope 字段名未逐行核 |
| HMR `root: ["."]` 的默认值来源 | 来自 `dsh-base/cordis.patch.yml` 文本，未读 hmr 包实现确认默认值 |

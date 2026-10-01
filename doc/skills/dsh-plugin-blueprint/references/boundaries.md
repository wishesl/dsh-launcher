# 能力边界：半边对照、禁止项、稳定 vs 内部、没有沙箱

> 读它的时机：方案快成形时用来砍掉不可能的部分；评审别人方案时用来找越界。

## 1 半边能力对照

| 能力 | 宿主半边 | 浏览器半边 |
|---|---|---|
| 注册工具 / 读写 Node API（fs、child_process、http）/ 首屏注入 / 配置 Config / 会话与投影 / typert | ✅ | ❌ |
| DOM / React / slot / 主题 token / locale / 快捷键 | ❌（拿不到 document） | ✅ |
| `ctx.provide` 服务 | ✅ | ✅（各自命名空间） |
| 环境变量 / 宿主 token | ✅ `process.env.*` | 只能由宿主半边经 `index-inject` 注入 `window.__X__` |
| 生命周期清理 | `ctx.effect` | 同左，但模块副作用延迟到首次物化 |

## 2 明确做不到 / 官方禁止

- **不要 `require('@deepseek-ai/dsh-client-ui-primitives')` 或把任何 Harness Client 包当模块加载**（会无预告变化；纯 JS 插件无类型检查；抛错组件让 slot entry 空白并打印 `slot entry crashed in '<slot>'`）。改为自己写控件并对齐宿主（从安装包 `lib/index.js`/`lib/**/*.css` 复制标记/CSS/行为），复制来的类名换自己前缀、只保留 `--dsw-alias-*` 引用。
- **不要从宿主提供 HTML 再用 iframe 嵌入**（iframe 拿不到主题 token、明暗切换与 locale）。
- **不得替换 app root 或往 `document.body` 追加第二个应用**；不得在组件外写 DOM。
- **不得读别的插件的 DOM/样式表/组件源码来估算位置**。
- 客户端半边不能读配置、不能写文件、不能拿 Node API。
- **不要手写 profile 的 `package.json`/`cordis.patch.yml`，不要在 `$DSH_HOME` 下建包，不要在 profile 目录跑 pnpm**；装插件走 `plugin_manager install_bundle`（或 `dsh plugin --profile <name> add`）。

## 3 稳定契约 vs 内部实现

| 稳定（可依赖） | 内部/可变（别依赖） |
|---|---|
| `dsh.bundle.patch`、`dsh.profile.bundles`、`dsh.client.{platform,inject,immediately,external}` | `dsh.manifestVersion`、`engines.dsh`（不强制） |
| `cordis.patch.yml` 三种形态 + `!!js` | `lib/` 的文件切分与哈希名（`plugin-BGnVfe_D.js`） |
| `profileContext`（`name/dir/patchPath/installAnchor/startedBundles/cwd/home/overlays`） | `@deepseek-ai/dsh-plugin-manager/operations` 等子路径导出 |
| `compatibility.json`、`peerDependencies` 兼容语义 | `BootManifest`/`WebBootEntry` wire 细节、`.plugin-manager/run.json` |
| required entry 清单、`ctx.typert` 注册面、`ctx.hmr` 事件 | `FiberState` 数字、`lib/types/*.js` 这类第二份 JS 产物、`internals.*` 注入点 |

## 4 没有沙箱

插件是**宿主进程内的普通 Node ESM 模块**：`import { spawn } from "node:child_process"` 直接可用，能读任意路径、能改宿主状态、能自己起 server。现有"沙箱"只约束**模型工具**（`dsh-fs-sandbox` 按 session 的 sandbox mode 限制写入，拒绝码 `FS_SANDBOX_DENIED`；`dsh-sandbox-policy` 统一策略；审批走 `ctx.approval`）。动态包的 `node:vm` 沙箱官方明说 "is not a security boundary … Treat a dynamic package like bash access"。唯一的策略性拒绝点在组合边界（被拒行变 `disabled: true`、bundle 进 `skippedBundles`，**永不 import 该模块**）。

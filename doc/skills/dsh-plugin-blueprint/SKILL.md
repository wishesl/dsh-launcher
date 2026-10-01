---
name: dsh-plugin-blueprint
description: DSH(DeepSeek Harness) 插件架构蓝图与源码取证法。设计、评审、实现 DSH 插件（bundle/双半边/工具/UI slot/typert），或判断某个能力在该版本 DSH 上是否可行时使用。
---

# DSH 插件架构蓝图（源码实证，0.2.0-rc.2）

面向「要写 / 要评审一个 DSH 插件」或「要判断某个能力能不能做」。全部结论来自
`E:\gopackage2\2026-8\dsh-start\dsh-vsn\0.2.0-rc.2\node_modules\.pnpm\` 下的真实安装包源码与官方 skill 原文，
**不接受「文档里好像说过」**。

## 0 三条铁律

1. **先定层、再定半边、再定扩展点**，三者都定不下来就不要写代码。
2. **每条 API 用法都要有 `文件:行号` 或 `cordis_inspect_query` 结果支撑**；拿不到证据就写「未确认」。
3. **任何「只装载一次 / 活到进程结束 / 调用成功即生效」的假设都是错的** —— 见 `references/framework.md` §双半边。

## 1 三十秒选型

| 问题 | 判据 | 去哪读 |
|---|---|---|
| 该做成插件吗？ | 要挂进 profile 组合、要吃 config、要被 HMR/设置看见 → 是；一次性改一行内置行为 → 优先 patch 覆盖，别造包 | `references/framework.md` §落地分层 |
| 宿主半边、浏览器半边、还是双半边？ | 碰 fs/子进程/会话/工具 → 宿主；碰 DOM/slot/主题 → 浏览器；两者都要 → 双半边（先读四条后果） | `references/boundaries.md` §半边对照 |
| 宿主侧挂哪个扩展点？ | 工具 / 路由 / 设置 / 投影 / 命令 / 事件 / 审批 | `references/host-extensions.md` |
| 浏览器侧挂哪个槽？ | 已分配空间的 slot；加页面 = `main` keyed + `panellist` + `selectPanel` | `references/client-extensions.md` |
| 这个能力到底存不存在？ | 先按 §3.2 取证顺序查，再按 §3.3 证伪清单打掉自己的假设 | `references/verification.md` |
| 报错看不懂？ | 权威错误串对照表 | `references/error-strings.md` |
| 要一份能直接抄的 manifest / 骨架？ | 双半边 `package.json` + 两侧入口 | `references/templates.md` |

## 2 文件索引（按需读，不要整目录读）

| 文件 | 内容 |
|---|---|
| `references/framework.md` | profile → bundle → patch → entry 分层、Cordis 4.0.4 内核模型、插件三种形态、双半边真实语义、装载链路与失败面、三条正规通道 |
| `references/host-extensions.md` | 宿主侧扩展点总表（tool/Service/路由/设置/投影/命令/审批/子进程）+ 每类要点与坑 |
| `references/client-extensions.md` | 浏览器半边：slot 姿势、构建 → 装载 → 运行 → 更新、公开服务目录（8+4）、跨半边 remote、页面全局契约 |
| `references/boundaries.md` | 半边能力对照、明确做不到/官方禁止、稳定契约 vs 内部实现、没有沙箱 |
| `references/verification.md` | 规划六步、取证顺序与机械技巧、证伪清单、结论表述规范、未确认清单 |
| `references/error-strings.md` | 权威错误串 → 含义 → 修法 |
| `references/templates.md` | 可复制的双半边 `package.json` 与两侧入口骨架 |
| `references/lessons.md` | 本仓库 dsh-launcher-plugin 0.2.6–0.2.9 的五条实战教训 + 参考坐标 |

## 3 最容易翻车的四件事（全文最贵的四条）

1. **`ctx.plugin` 无条件 `new Fiber`，同一插件可多实例**；声明 `dsh.client` 会让宿主半边被重复装载。⇒ 模块级变量会被互相踩，幂等闩（`handled = true`）会永久闩死。
2. **没有 `ready`/`dispose` 事件，`ctx.effect` 是唯一清理原语**；`ctx.timeout` 注册在 timer 服务自己的 ctx 上，**不随调用方 fiber 取消** ⇒ 脏 ctx 续排定时器 = 进程级崩溃。
3. **patch 的 `config` 是整体替换，永不深合并** ⇒ 想改一个子键必须重述该行全部字段。
4. **依赖"还没就绪"不是"不存在"**：重启/重载后头 1~2 秒 `ctx.get("<svc>")` 可能取不到。就绪要用 `ctx.inject([...], cb)` 的事件等，**不要用重试次数等**；错误文案只写确证的事实。证据核对先按时间戳切出**本次运行段**（累积日志会让你误判"没修好"）。见 `references/verification.md` §6。

## 4 版本与坐标约定

- 本文件所有 `lib/index.js:NNN` 行号是 **0.2.0-rc.2 安装包快照**，升级后必然漂移；**检索请按符号名 grep**（如 `sessionProjections.register`、`applyEntryPatches`），行号只作辅助。
- `dsh-vsn/` 之类被 `.gitignore` 忽略的目录，`grep` 工具会跳过 ⇒ 用 pwsh `Select-String -Recurse` 或 `read` 绝对路径。
- 官方 skill（`cordis-plugin-development` / `cordis-composition-reference`）在 web profile 会话里**默认不可见**，需要时直接读安装包里的原文，见 `references/lessons.md` §参考坐标。

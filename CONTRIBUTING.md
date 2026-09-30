# 贡献指南

感谢参与 DSH Launcher 的改进！本文是给**贡献者**的快速指引；仓库还有一份 [AGENTS.md](AGENTS.md)
（开发流程约定与踩坑速查），改代码前请务必读一遍 —— 里面的布局铁律和事件订阅约定是踩过坑总结的。

## 开发环境

- Go 1.25+
- Node.js 20+
- [Wails v2 CLI](https://wails.io/docs/gettingstarted/installation)：`go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0`

## 本地运行与构建

```bash
cd dsh-launcher

# 实时开发模式（前端热更新）
wails dev

# 前端类型检查 + 构建（必须通过）
cd frontend && npm run build     # = tsc && vite build

# 打包桌面产物（build/bin/dsh-launcher.exe）
cd .. && wails build
```

**提交代码前这两条构建必须是绿的**（CI 也会跑同样的检查：`.github/workflows/ci.yml`）。

## 提交约定

- 中文提交信息，前缀与仓库历史一致：`feat:` / `fix:` / `ui:` / `docs:` / `refactor:`。
- 例：`ui: 运行日志改为右侧常驻第三栏（侧边栏|内容|日志）`
- 只提交源码；`frontend/dist/`、`build/bin/`、`*.exe` 不入库（已在 .gitignore）。

## Pull Request

- 小改动直接提 PR；大改动建议先开 issue 讨论方案。
- PR 模板里的检查清单请逐项确认，重点三条：
  1. **进度 / 日志输出进右侧第三栏**，禁止覆盖式浮层（[AGENTS.md §0](AGENTS.md)）；
  2. 传给子组件的回调用 `useCallback` 稳定化；
  3. Wails 事件订阅只在 `App.tsx` 一个地方。
- PR 会触发 CI（前端构建 + Go vet/build），绿了才便于合并。

## Issue

- bug 报告请带**启动器版本 + 操作系统 + 日志**（用 bug 报告模板）。
- 功能建议先描述**问题场景**，再给方案（用功能建议模板）。
- 安全问题不要公开提，见 [SECURITY.md](SECURITY.md)。

## 文档与设计资料

- 方案文档放在 [`doc/`](doc/)；界面截图也按日期归档在 `doc/<日期>/`。
- 品牌素材源文件目前不入库（`.gitignore` 忽略 `/branding/`、`/logo.jpg`），需要请提 issue 联系。

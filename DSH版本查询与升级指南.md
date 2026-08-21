# DeepSeek Harness (DSH) 版本查询与升级指南

> 整理日期：2026-08-13（调查时点）
> 适用范围：本机 Windows + `npx @deepseek-ai/dsh web` 启动方式

---

## 0. 结论摘要（TL;DR）

| 项目 | 值 |
|---|---|
| **本机实际运行的 DSH 版本** | **`0.1.0-rc.6`** |
| **npm 最新版（latest）** | **`0.1.1-rc.2`** |
| 启动方式 | `npx @deepseek-ai/dsh web`（从 `D:\Users\Tony\Desktop` 启动） |
| npm 镜像源 | `https://registry.npmmirror.com/`（`C:\Users\Tony\.npmrc`） |
| 全局 npm 前缀 | `C:\Users\Tony\AppData\Roaming\npm` |

**为什么本机是旧版**：npx 会优先命中启动目录里的本地 `node_modules` 副本
（`D:\Users\Tony\Desktop\node_modules\@deepseek-ai\dsh@0.1.0-rc.6`），
所以无论 npm 上发了几版新版，`npx @deepseek-ai/dsh web` 都一直跑旧版。

**结论**：你的记忆没错——最新版确实是 **0.1.1-rc.2**；本机之所以是 rc.6，是因为 npx 解析到了本地副本而非 registry。

---

## 1. 调查过程记录（我是怎么查的）

### 1.1 本机实际版本（可直接复现的命令）

```powershell
# ① 项目根 package.json 声明的依赖范围
Get-Content 'D:\Users\Tony\Desktop\package.json'
#    → "dependencies": { "@deepseek-ai/dsh": "^0.1.0-rc.6" }

# ② 实际安装的包版本
Get-Content 'D:\Users\Tony\Desktop\node_modules\@deepseek-ai\dsh\package.json'
#    → "version": "0.1.0-rc.6"

# ③ 运行中 CLI 自报版本（最可靠）
node 'D:\Users\Tony\Desktop\node_modules\@deepseek-ai\dsh\lib\bin.js' --version
#    → 0.1.0-rc.6

# ④ 全部 @deepseek-ai/* 子包版本（180+ 个全为 0.1.0-rc.6）
Get-ChildItem 'D:\Users\Tony\Desktop\node_modules\@deepseek-ai' -Directory |
  ForEach-Object {
    $j = Get-Content (Join-Path $_.FullName 'package.json') -Raw | ConvertFrom-Json
    [PSCustomObject]@{ pkg = $_.Name; ver = $j.version }
  } | Sort-Object ver -Descending | Format-Table -AutoSize
```

### 1.2 确认启动方式（环境变量证据）

当前 shell 里存在这些环境变量，证明 DSH 是从 Desktop 用 npx 拉起的：

```
npm_config_local_prefix = D:\Users\Tony\Desktop
npm_package_json        = D:\Users\Tony\Desktop\package.json
npm_config_registry     = https://registry.npmmirror.com/
npm_config_prefix       = C:\Users\Tony\AppData\Roaming\npm
npm_config_cache        = C:\Users\Tony\AppData\Local\npm-cache
```

### 1.3 遇到的坑（记录，避免下次重复踩）

| 现象 | 原因 |
|---|---|
| `npm` / `npx` 命令直接报错 `StandardOutputEncoding is only supported when standard output is redirected` | 全局 `C:\Users\Tony\AppData\Roaming\npm\npm.ps1` 包装脚本通过管道捕获子进程输出，被沙箱限制拦截（**仅沙箱内如此，正常终端不受影响**） |
| `Invoke-RestMethod https://registry.npmjs.org/...` 报 `Authentication failed` | 本机网络经代理/镜像，直连官方 registry 被拦 |
| `Invoke-RestMethod https://registry.npmmirror.com/...` 同样报 `Authentication failed` | 同上，沙箱内网络请求也被代理拦截 |

**绕过手段**：改用 Tavily 网页搜索确认 npm 最新版（见 §2.5）。
在正常终端里，`npm view` 命令本身是可以用的。

### 1.4 全局安装目录现状

- 全局前缀：`C:\Users\Tony\AppData\Roaming\npm`
- 该目录已全局装过：npm、pnpm、mcporter、opencode-ai、reasonix、uipro-cli、
  @anthropic-ai、@openai、@qwen-code、@musistudio 等
- **尚未全局安装 dsh**（没有 `dsh.cmd`，`node_modules` 里也没有 `@deepseek-ai`）

---

## 2. 如何查询最新版本（核心章节）

> 分"官方 API"和"兜底手段"两档。优先用官方 API。

### 2.1 最常用：`npm view`（推荐）

```bash
# 查看最新版本（latest tag）
npm view @deepseek-ai/dsh version

# 查看所有 dist-tag（latest / next 等）
npm view @deepseek-ai/dsh dist-tags

# 查看完整版本历史（含所有 rc）
npm view @deepseek-ai/dsh versions

# 查看某个字段的详细信息
npm view @deepseek-ai/dsh time --json
```

注意：你的镜像配置为 npmmirror，通常同步很快；个别新包可能滞后几小时。
若怀疑滞后，可用 `--registry` 临时指定官方源再查：

```bash
npm view @deepseek-ai/dsh version --registry=https://registry.npmjs.org/
```

### 2.2 纯 HTTP API（不开 npm，可用 curl / PowerShell）

```powershell
# npm 官方 registry（JSON，含 dist-tags 与所有版本）
curl -s https://registry.npmjs.org/@deepseek-ai%2Fdsh | ConvertFrom-Json |
  Select-Object -ExpandProperty 'dist-tags'

# npmmirror（国内镜像，速度快）
curl -s https://registry.npmmirror.com/@deepseek-ai/dsh | ConvertFrom-Json |
  Select-Object -ExpandProperty 'dist-tags'
```

### 2.3 网页直接看（零命令）

- npm 官方页面：<https://www.npmjs.com/package/@deepseek-ai/dsh>
- deps.dev 依赖视图：<https://deps.dev/npm/@deepseek-ai/dsh>

### 2.4 GitHub 仓库

- 主仓库：<https://github.com/deepseek-ai/DeepSeek-Harness>
- 根 `package.json` 的 `version` 字段反映源码最新开发版
  （调查时点为 `0.1.0-rc.8`，注意：**源码版 ≠ npm 发布版**，npm 才是发布入口）
- Releases / Tags：<https://github.com/deepseek-ai/DeepSeek-Harness/tags>

### 2.5 兜底：网页搜索（当本机网络/镜像不可用时）

```text
Tavily / 任意搜索引擎 查询："@deepseek-ai/dsh" npm version latest
```

本次调查即用此法确认：**npm 上 `@deepseek-ai/dsh` 的 latest 已是 `0.1.1-rc.2`**，
佐证来源包括真实升级记录帖与第三方版本追踪站点。

### 2.6 本地自己是多少（三招）

```bash
# ① 正在跑的进程（启动目录内的本地副本）
node D:\Users\Tony\Desktop\node_modules\@deepseek-ai\dsh\lib\bin.js --version

# ② npx 会用到的那份（加 -v）
npx @deepseek-ai/dsh --version

# ③ 全局装的（若装过）
dsh --version
```

---

## 3. 如何升级

### 方案 A：npx 强制用最新版（官方推荐，最省事）

```bash
npx -y @deepseek-ai/dsh@latest web
```

- `-y` = `--yes`，忽略本地旧副本、直接去 registry 下载最新版。
- 想固定版本（推荐，DSH 快速迭代可能有不兼容变更）：

```bash
npx -y @deepseek-ai/dsh@0.1.1-rc.2 web
```

### 方案 B：把本地安装也升上去（一劳永逸）

```bash
cd D:\Users\Tony\Desktop
npm install @deepseek-ai/dsh@latest
npx @deepseek-ai/dsh web
```

`^0.1.0-rc.6` 的 caret 范围本就允许升到 0.1.1-rc.2，装完即用新版。

### 方案 C：全局安装

```bash
npm install -g @deepseek-ai/dsh@latest
dsh web
```

全局安装的位置（Windows）：

| 内容 | 路径 |
|---|---|
| 包本体 | `C:\Users\Tony\AppData\Roaming\npm\node_modules\@deepseek-ai\dsh\` |
| 命令入口 | `C:\Users\Tony\AppData\Roaming\npm\dsh`、`dsh.cmd`、`dsh.ps1` |

安装时 npm 会把该目录加进 PATH，之后**任意目录**敲 `dsh web` 即可。

### 方案 D（备选）：pnpm 执行

```bash
pnpm dlx @deepseek-ai/dsh@latest web
```

**已知坑**：`npm` 直装新版时可能卡在依赖解析、CPU 占用近 100% 起不来，
改用 `pnpm dlx` 可绕开；安装时会询问是否构建 `node-pty`、`koffi` 等原生模块——
按 `a` 全选 → 回车 → 输入 `y` 确认。

### 升级必看注意事项

1. **必须重启才生效**：升级只改磁盘文件，**正在运行的旧进程（当前 GUI）不会自动变新版**。需先停旧服务再重新启动。
2. **版本别混着用**：全局版与 npx 本地副本是两个独立版本来源。
   PATH 中全局目录排在项目目录之前，但 `npx @deepseek-ai/dsh web` 仍会优先命中
   Desktop 的本地副本。二选一，别混。
3. **环境要求**：Node `^22.19.0 || >=24.0.0`。本机 node v22.20.0 / npm 11.7.0 满足。
4. **兼容性**：DSH 处于 developer preview，官方声明"会有不兼容破坏性变更"，
   升完建议对照官方 README 检查：<https://github.com/deepseek-ai/DeepSeek-Harness#1>

---

## 4. 一句话速查

```bash
# 我本机是多少
node D:\Users\Tony\Desktop\node_modules\@deepseek-ai\dsh\lib\bin.js --version
# npm 最新是多少
npm view @deepseek-ai/dsh version
# 升级并启动（最新）
npx -y @deepseek-ai/dsh@latest web
# 升级并启动（固定版本）
npx -y @deepseek-ai/dsh@0.1.1-rc.2 web
```

---

*本文档基于 2026-08-13 的实际调查整理。版本号随时间变化，以 §2 的实时查询命令为准。*

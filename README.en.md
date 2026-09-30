# DSH Launcher

English | [简体中文](README.md)

[![CI](https://img.shields.io/github/actions/workflow/status/wishesl/dsh-launcher/ci.yml?branch=master&style=flat-square&label=CI)](https://github.com/wishesl/dsh-launcher/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/wishesl/dsh-launcher?style=flat-square&label=Release)](https://github.com/wishesl/dsh-launcher/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](LICENSE)

A **cross-platform desktop GUI launcher** (Windows / macOS / Linux) for starting
**DeepSeek Harness (DSH)** with a specified directory and a specified version, plus visual version queries, instance management, plugin assembly, and live logs.

DSH ultimately starts as a single `npx -y @deepseek-ai/dsh@<版本> web` command run in some working directory.
The launcher wraps "pick a directory + pick a version + start/stop + configure plugins + view logs" into a ready-to-use GUI,
and can even **embed the DSH UI directly inside its own window** as a pseudo desktop app.

## UI Preview

**Instance management** (one instance = one directory + one version; cards are drag-sortable, and the "Compatibility Check" entry sits to the right of the title):

![Instance management](doc/2026-10-1/20261001-043544.jpg)

**Version history** (npm `latest` / `next`, all versions and release times; "Best fit" marks versions the launcher has actually verified):

![Version history](doc/2026-10-1/20261001-043525.jpg)

**Plugin Market · Discover** (filter the catalog by category / downloads, install in one click, star your favorites; watch install progress in the "Market Tasks" tab of the right panel):

![Plugin market](doc/2026-10-1/20261001-043528.jpg)

**Built-in DSH view** (embeds the DSH UI inside the launcher window — the top bar keeps Refresh / Exit, everything else is handed over to DSH):

![Built-in DSH view](doc/2026-10-1/20261001-043520.jpg)

> Screenshots of earlier versions are kept under [`doc/`](doc/) for historical reference only (the UI has changed many times).

## Core highlights: manage everything safely without starting DSH

Get DSH installed, configured, and plugin-tuned first, then start it cleanly — **none of this requires opening DSH up front**:

- **Plugin management (no web UI)**: discover / install / uninstall / enable / disable plugins in the market, with progress visible in real time;
  toggles are written straight into the profile's patch layer (HMR takes effect in about 1 second, survives restarts); you can also favorite plugins offline and generate / import share codes.
- **Core management (no web UI)**: visually query npm `latest` / `next`, all versions, and release times;
  "Install to directory" really installs the chosen version into the directory's `node_modules` (pnpm install + auto-approval of native module builds),
  so agents can read the actual source; you can also "launch from source" against a source directory (init / build / start commands run in one click).
- **Per-instance plugin masking**: tick plugins to block for each instance — at launch a **temporary `--patch` override layer** is injected,
  effective only for that one launch, **without changing global toggle state**, and restored automatically when the instance stops; uninstalled plugins are automatically hidden and never masked.
- **Safety first**: plugin install / uninstall / toggle all require the instance to be stopped (plugin files are never modified while it runs);
  masking goes through a temporary override layer instead of rewriting global config; uninstalling automatically cleans up disable traces.

> In short: **pick a directory → install a version → configure plugins → hit Start**, all through the GUI — no command line, and DSH never needs to be started early.

## Built-in DSH view (pseudo desktop app)

Click the monitor icon in the top bar and the lower half of the window becomes the DSH web page, with the menu and run-log columns stepping aside — it looks like a native desktop DSH.
The top bar keeps only the **Refresh / Exit** buttons and gives all remaining space to DSH; pressing `Esc` also exits.

**Where the address comes from**: every DSH launch issues a fresh session URL carrying a token, printed only in the startup log.
The launcher **parses it automatically** from the instance's launch process — no manual pasting needed. The token in the URL is masked in the UI
(used only inside the iframe).

**Prerequisites** (all three must hold to embed):

1. The instance was **started by the launcher** (the address comes from its startup log);
2. The instance has "self-managed restart" enabled;
3. The built-in plugin `dsh-self-mcp` is installed globally.

Items 2 and 3 are not formalities: DSH's session cookie is `SameSite=Strict`, so a cross-origin iframe can never obtain it, and without relaxation you get a permanent 401. The built-in plugin performs only a **minimal relaxation** — it allows only "home-page requests carrying a valid launch token" and "`/api` requests from the launcher's origin"; everything else is still blocked by the Host/Origin fence and `Sec-Fetch-Site`.

> When the prerequisites are not met, the entry is **greyed out with the reason shown**, instead of letting you click through and watch it reconnect forever.

## Capability probing: no more silent breakage when upstream changes its internals

DSH iterates fast, and the launcher inevitably couples to its internals in several places (startup log format, the auth method on `connection`, Cordis loader config, ...). These coupling points are **probed by capability, not decided by version number**: the version number is a timestamp controlled by upstream, users install `latest`, and any "version → what gets enabled" table is bound to be one step behind.

Probe conclusions have an explicit outlet, collected in the third tab of the right panel, "Compatibility", plus the "Compatibility Check" entry to the right of the instance page title (the count of problematic instances lands directly on the button):

- Lists each piece of **probe evidence**: whether the access URL / tokened URL can be parsed, where the plugin assembly gate is stuck, whether the plugin is actually loaded, whether `dsh-restart` registered successfully, whether session validation was relaxed, the delivery channel for restart completion, ...
- Every row is **tri-state**: green (usable), red (a **confirmed** fault), grey (no conclusion / not applicable).
  Distinguishing "failed" from "don't know" is deliberate — once the panel cries wolf, users learn to ignore it.
- Failures go from "I clicked and nothing happened / endless spinner" to "which item is red + why + what it breaks".

**Best-fit version** is the other side of the same idea: the launcher marks the DSH versions it has **actually verified**
(a tag in the version list, a note in the instance form with a hint when you deviate), but it is a **recommendation, not a permission** —
whether things work is always decided by the live probing above, and installing another version still launches fine.

## Quick start

1. **Prepare the environment**: running DSH needs Node.js (pnpm recommended); building this app needs the [Wails v2 CLI](https://wails.io/docs/gettingstarted/installation) + Go 1.25+.
   Linux additionally needs system dependencies (Debian/Ubuntu): `sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev libayatana-appindicator3-dev librsvg2-dev`,
   and the build must pass `-tags webkit2_41` (`wails build -tags webkit2_41`; Ubuntu 24.04 removed webkit2gtk-4.0).
   The launcher's "Settings" panel can detect whether npm / pnpm is available and install pnpm in one click.
2. **Get the app**: download the package for your platform directly from [Releases](https://github.com/wishesl/dsh-launcher/releases)
   (Windows `dsh-launcher-windows-amd64.exe` / macOS `dsh-launcher-darwin-*.zip` / Linux `dsh-launcher-linux-amd64.tar.gz`, no installation required);
   or clone the repository and build it yourself with `cd dsh-launcher && wails build`.
3. **Add an instance**: left sidebar "Instances" → "+ Add Instance" → choose the DSH launch directory (e.g. your project directory) →
   pick a version ("Latest" or a specific one) → set the launch mode to **Local copy** (officially recommended; agents can read the real source) → Save.
4. **Start and open**: click "Start" on a card; the run-log panel on the right opens automatically and streams in real time. When
   "DSH ready · name · address" appears at the top, click it to open the DSH web UI in your browser (the card's URL is also clickable to copy).
5. **Install to directory**: if the instance reports "local copy not installed", click "Install to directory" on the card first to really install that version into
   the directory's `node_modules` — this avoids npx re-fetching from the network repeatedly and lets agents read the source.
6. **Enable self-managed restart** (optional, for plugin debugging): tick "self-managed restart" in the instance form,
   and install the built-in plugin `dsh-self-mcp` globally from the plugin market. The model can then call the `dsh-restart` tool to restart
   DSH; when the restart finishes, the launcher automatically injects a "restart complete" message back into the originating session so the conversation continues.
7. **Open DSH in the built-in view** (optional): monitor icon in the top bar → the lower half becomes the DSH UI; press `Esc` or the top-bar "Exit" to return.
8. **Stop / restart**: the card's "Stop" ends the process; the ↻ button at the top restarts the current instance in one click.
9. **Daily habits**: clicking ✕ minimizes to the tray by default (DSH keeps running in the background); use the tray icon to bring it back or quit;
   individual instance cards can enable "auto start" so they are launched when the launcher opens.

> Tip: DSH is essentially `npx -y @deepseek-ai/dsh@<版本> web` running in the selected directory, and multiple instances never interfere with each other;
> one directory corresponds to one version — don't mix them (see [`DSH版本查询与升级指南.md`](doc/DSH版本查询与升级指南.md)).

## Why this exists (background)

Referring to [`DSH版本查询与升级指南.md`](doc/DSH版本查询与升级指南.md), this mainly solves two pain points:

1. **npx prefers a local `node_modules` copy in the launch directory**, so "npm has a new version but this machine keeps running an old one".
2. Version queries / upgrades mean typing a pile of CLI commands (`npm view` / `npx ... web`), which is easy to get wrong.

What the launcher adds:

- One instance = **one directory + one version**; multiple instances never interfere (matching the guide's "don't mix versions").
- Visual display of **npm latest / all versions / release times / the actual local version**, avoiding memory bias.
- One-click start / stop with live log echo — no more typing npx commands by hand.

## Features

- **Instance management**: each instance binds a directory and a version; card-based list with status indicators
  (starting / running / ready / stopped / crashed), supporting Start / Stop / Delete / Open Web.
  Cards support **drag reordering** (↑/↓ keys do the same), with the order persisted and the tray menu kept in sync.
- **Version queries**: shows the `latest`, `next` dist-tags, the full version history, and release times; marks the **best-fit version**;
  the actual local version is probed by reading `目录/node_modules/@deepseek-ai/dsh/package.json`.
  Version sources: **official registry first, npmmirror as fallback**.
- **Launch mode**: `npx -y @deepseek-ai/dsh@<version> web`, default/recommended "local copy (local)", plus one-click "Install to directory" so npx stops re-fetching over the network.
- **Source launch mode**: pick a DSH source directory; the init / build / start commands are customizable
  (defaults `pnpm install` / `pnpm run build` / `pnpm dsh web`), executed in one click.
- **Built-in DSH view**: embeds the DSH UI inside the launcher window as a pseudo desktop app; the address is parsed automatically from the startup log, the token is masked,
  and the entry is greyed out with the reason shown when prerequisites are unmet (see the dedicated section above).
- **Self-managed restart**: an instance-level switch plus the built-in plugin `dsh-self-mcp` providing the `dsh-restart` tool;
  after a restart, a "restart complete" message is injected into the originating session and wakes it up to continue (shown collapsed as a plugin notice, not a user bubble).
- **Capability probing**: the "Compatibility" tab in the right panel plus the "Compatibility Check" entry on the instance page list the conclusion and evidence for every capability,
  tri-state (usable / confirmed fault / no conclusion), turning silent breakage into something you see at a glance (see the dedicated section above).
- **Plugin market**: discover / install / uninstall community plugins (reusing the official `dsh plugin --profile web` channel);
  enable / disable writes straight into the profile's `cordis.patch.yml` (HMR takes effect in ~1 second, survives restarts);
  operation progress streams into the right-hand log panel, with mirror-source and network proxy support.
- **Favorites and sharing**: favorite plugins offline (the favorites file lives locally, so installs still work offline), and generate / import share codes for batch sharing.
- **Per-instance plugin masking**: tick plugins to mask per instance; a temporary `--patch` override layer is injected at launch,
  leaving global toggle state untouched and restoring automatically when the instance stops; uninstalled plugins are automatically hidden and never masked.
- **Live logs**: streaming startup logs; readiness detection (auto-detects the web address), crash vs. clean-exit distinction,
  log persistence for auto-started instances, and cleanup of leftover orphan DSH processes on exit.
- **Three-column layout**: menu | content | run logs, with both gutters **draggable to resize** (double-click to reset), widths stored in `settings.json`;
  the first tab of the right panel is instance logs (a dropdown next to the title switches instances), followed by market tasks and compatibility.
- **Minimize to system tray**: clicking the window ✕ hides to the tray instead of quitting (can be disabled); tray icon + menu
  (show main window / hide / quit), with settings persisted.
- **Single launch (single instance)**: launching the exe again doesn't open a second window or a second tray icon —
  it just brings the running instance's window back to the foreground.
- **Prerequisite environment setup**: the Settings panel detects whether npm / pnpm is available and their versions, and can install / upgrade pnpm in one click.
- **Auto start and exit choices**: instance cards can individually enable "start at boot"; clicking ✕ pops a dialog choosing
  "hide to tray" or "quit directly".

## Tech stack

| Layer | Technology |
|---|---|
| Desktop shell / backend | **Wails v2** + **Go 1.25** (Windows / macOS / Linux) |
| Frontend | **React 18** + **TypeScript** + **Vite 3** |
| System tray | `fyne.io/systray` (message loop on its own goroutine, works on all three platforms) |
| Single instance | Wails `options.SingleInstanceLock` |
| Cross-platform process management | Platform abstraction layer (`procattr_windows.go` / `procattr_unix.go`): Windows uses `cmd /c` + Job Object + taskkill; macOS/Linux use `sh -c` + Setsid process-group tree kill |
| Built-in plugin | `dsh-self-mcp` (a Cordis plugin whose source is embedded into the launcher binary, installable to the profile in one click) |

## Architecture and directory structure

```
dsh-launcher/
├── main.go            # 应用入口：窗口/托盘/单实例锁/绑定
├── app.go             # App 生命周期 + 实例增删改查等绑定方法
├── version.go         # 启动器版本 + 最佳适配的 DSH 版本（都是 ldflags 可覆盖的变量）
├── instances.go       # 实例持久化（%APPDATA%\DSHLauncher\instances.json）
├── instance_mask.go   # 实例级插件屏蔽（名单持久化 + 临时 --patch 覆盖层生成/清理）
├── self_restart.go    # 自管理重启契约（双门控、覆盖层、restart-request.json 消费）
├── self_restart_install.go # 内置插件 dsh-self-mcp 的解出与安装（embed.FS → profile）
├── embeddata.go       # 内置插件源码的 embed 声明
├── capabilities.go    # 兼容性探测（读插件能力报告 + 启动器侧探针 → 面板数据）
├── dsh_query.go       # 版本查询（npm registry / 本地版本探测）
├── dsh_process.go     # 进程管理（npx/pnpm 启动、进程树停止、日志推送、就绪探测、地址/token 解析）
├── dsh_job_windows.go # Windows 进程树管理（Job Object / taskkill）
├── dsh_job_other.go   # 其它平台的空实现
├── install.go         # 「安装到目录」（pnpm 安装 + 批准原生模块构建）
├── market_catalog.go  # 插件市场目录拉取（镜像源 / ETag 缓存）
├── market_ops.go      # 插件安装 / 卸载（复用官方 dsh plugin CLI）
├── market_installed.go# 已装插件读取与启用/禁用（写 cordis.patch.yml 补丁层）
├── market_update.go   # 插件更新检查与执行（含内置插件）
├── favorites.go       # 插件收藏与分享码
├── service_probe.go   # 独立端口服务探测（驱动「已就绪」+ 打开按钮）
├── proxy.go           # 网络代理（npm/pnpm/git/registry）
├── env.go             # 前置环境检测（npm/pnpm）与 pnpm 安装
├── settings.go        # 设置持久化（布局、三栏宽度、托盘行为等）
├── logging.go         # 实例日志落盘
├── tray.go            # 系统托盘
├── procattr_*.go      # 平台进程属性 / 杀进程树
├── embed/dsh-self-mcp/ # 内置插件源码（Cordis 插件，见其 README）
└── frontend/
    └── src/
        ├── App.tsx / api.ts / types.ts / util.ts
        └── components/
            ├── Header.tsx / Sidebar.tsx / WinControls.tsx  # 顶栏 / 左侧菜单 / 窗口按钮
            ├── InstancesView.tsx / InstanceCard.tsx / InstanceForm.tsx
            ├── MaskPluginsDialog.tsx           # 实例「屏蔽插件」选择弹窗
            ├── VersionView.tsx / VersionPanel.tsx  # 版本历史
            ├── MarketView.tsx                  # 插件市场（发现 / 已安装 / 收藏）
            ├── LogDrawer.tsx / LogPanel.tsx    # 右侧运行日志第三栏（含兼容性面板）
            ├── Resizer.tsx                     # 三栏宽度拖拽
            ├── SettingsView.tsx                # 设置
            └── ExitDialog.tsx / ShareCodeDialog.tsx / Switch.tsx
    └── wailsjs/                    # Wails 自动生成的前端绑定
```

The backend exposes binding methods to the frontend via `window.go.main.App.*`, and events flow through `window.runtime.EventsOn/Off`:

| Event | Payload | Purpose |
|---|---|---|
| `dsh:log` | `LogEvent` | Instance log line |
| `dsh:status` | `StatusEvent` | Instance status change |
| `dsh:service` | `ServiceState` | Service reachability (drives "DSH ready") |
| `dsh:notice` | `NoticeEvent` | Top toast |
| `dsh:market-log` | `MarketLogEvent` | Market operation output line |
| `dsh:market-status` | `MarketStatusEvent` | Market task running / completed / failed / cancelled |
| `dsh:env-log` | `EnvLogEvent` | Environment check / pnpm install output |
| `dsh:close-requested` | — | Window ✕ clicked |

## Development and build

Prerequisites: [Wails v2 CLI](https://wails.io/docs/gettingstarted/installation) + Go 1.25+ + Node.js.

```bash
cd dsh-launcher

# 实时开发模式（前端热更新，Windows 上同样支持 WebView2）
wails dev

# 前端类型检查 + 构建
cd frontend && npm run build

# 构建可分发生产包（产物 build/bin/dsh-launcher.exe）
cd .. && wails build

# 带版本号（顶栏会显示）；最佳适配版本也可用同样方式覆盖
wails build -ldflags "-X main.version=0.1.5 -X main.bestFitDSHVersion=0.1.6-rc.1"
```

> The generated app is named `dsh-launcher` (see `wails.json`).
> After changing backend binding methods, regenerate the frontend bindings (`wails generate module`; `wails build` does this automatically too).

### Releasing a new version (GitHub Actions auto-build + Releases)

Pushing a `v*` tag triggers GitHub Actions to automatically build on **Windows / macOS / Linux** and publish to
[Releases](https://github.com/wishesl/dsh-launcher/releases):

```bash
git tag v0.1.0
git push origin v0.1.0
```

Every release automatically produces four artifacts (with auto-generated release notes):

| Platform | Artifact |
|---|---|
| Windows x64 | `dsh-launcher-windows-amd64.exe` |
| macOS (Apple Silicon) | `dsh-launcher-darwin-arm64.zip` |
| macOS (Intel) | `dsh-launcher-darwin-amd64.zip` |
| Linux x64 | `dsh-launcher-linux-amd64.tar.gz` |

> Note: artifacts are not code-signed; Windows SmartScreen / macOS Gatekeeper may warn about an "unknown publisher" on first run — choose "Run anyway / Open" as prompted.

## Data and configuration locations

| Item | Path |
|---|---|
| Instance list | `%APPDATA%\DSHLauncher\instances.json` |
| App settings (layout, three-column widths, tray behavior, etc.) | `%APPDATA%\DSHLauncher\settings.json` |
| Plugin favorites | `%APPDATA%\DSHLauncher\favorites.json` |
| Per-instance plugin mask list | `%APPDATA%\DSHLauncher\instance-masks.json` |
| Plugin market catalog cache | `%APPDATA%\DSHLauncher\market-catalog.json` |
| Instance run logs | `%APPDATA%\DSHLauncher\logs\<实例ID>.log` |
| Temporary plugin mask layer (while running) | `.dsh-mask-<实例ID>.yml` in the instance directory (deleted automatically after stop) |
| Temporary self-managed restart override layer (while running) | `.dsh-self-restart-<实例ID>.yml` in the instance directory (deleted automatically after stop) |
| Plugin capability report | `.dsh-self-mcp\capabilities.json` in the instance directory (cleared before every launch, written by the built-in plugin) |
| Built-in plugin extraction location | `.dsh-builtin\dsh-self-mcp` in the profile directory |

## Related documents

- [`AGENTS.md`](AGENTS.md) — development workflow conventions and pitfall quick reference (layout rules, frontend conventions, capability-probing principles)
- [`dsh-launcher/embed/dsh-self-mcp/README.md`](dsh-launcher/embed/dsh-self-mcp/README.md) — the built-in plugin: restart semantics, capability report format, security boundary of the embed relaxation
- [`需求.md`](doc/需求.md) — full requirements and key decision records
- [`DSH版本查询与升级指南.md`](doc/DSH版本查询与升级指南.md) — background research on DSH version querying and upgrading
- [`插件市场实现方案.md`](doc/插件市场实现方案.md) — plugin market design decisions
- [`插件收藏功能实现方案.md`](doc/插件收藏功能实现方案.md) — favorites and share-code design decisions
- [`插件更新功能实现方案.md`](doc/插件更新功能实现方案.md) — plugin update check and execution design decisions
- [`版本升级实现方案.md`](doc/版本升级实现方案.md) — launcher self-update design decisions (check → download → verify → apply)

## Contributing and security

Issues and PRs are welcome — see [`CONTRIBUTING.md`](CONTRIBUTING.md) for the contribution process and [`SECURITY.md`](SECURITY.md) for vulnerability reporting
(please don't open public security issues).

## License

Released under the [MIT License](LICENSE).

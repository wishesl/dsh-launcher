# agents.md — DSH Launcher 开发流程约定（AI 代理工作手册）

> 本文档是后续所有开发任务的**强制流程**。做任何改动前先读一遍；改动完成后必须按「构建」一节自己收尾，
> 不要等用户来跑命令。

## 0. 铁律（每次开发必须遵守）

1. **布局约定：菜单放左边，输出放右边。**
   - 左侧 = 主导航菜单（Sidebar：实例 / 插件市场 / 设置）。
   - 右侧 = 运行日志 / 进度输出面板（三栏布局的第三列）。
   - 任何新功能、进度展示、日志输出都必须放进**右侧第三栏**，**禁止做成覆盖式悬浮层/抽屉弹层**
     （用户已明确否决过 overlay 抽屉：不要 `position: fixed` + transform 滑出的盖层，要做成随窗口伸缩的常驻列）。
2. **完成后自己执行 `wails build`**（见第 5 节）。这是收尾动作，不是可选项。
3. 布局改动后，主内容必须自动让出宽度给右栏（`flex` 布局），右栏收起时主内容回满全宽。

---

## 1. 项目一句话

**DSH Launcher**：一个 Windows 桌面 GUI 启动器（Wails v2 + Go 后端 + React 18 / TS / Vite 前端），
以「目录 + 版本」的方式启动 DeepSeek Harness，提供实例管理、版本查询、实时日志、插件市场、系统托盘。

## 2. 技术栈

| 层 | 技术 |
|---|---|
| 桌面壳 / 后端 | Wails v2.10.2 + Go（绑定方法在 `dsh-launcher/*.go`） |
| 前端 | React 18 + TypeScript + Vite 3（`dsh-launcher/frontend/`） |
| 前端样式 | 单文件 CSS：`src/style.css`（设计 token）+ `src/components/market.css`（市场页） |
| 前后端通信 | `window.go.main.App.*`（绑定方法）+ `window.runtime.EventsOn/Off`（事件流） |

## 3. 界面布局规范（三栏）

```
+-------------------------------------------------------------+
| Header：… [刷新版本] [最小化到托盘] [运行日志]                |
+--------+----------------------------------+-----------------+
| Sidebar| 主内容                           | 运行日志右栏    |
| 菜单   | (实例 / 插件市场 / 设置)          | (440px)         |
| (204px)|                                  | 实例标签 / 市场  |
+--------+----------------------------------+-----------------+
```

- **左侧**：`Sidebar.tsx`，三个导航项：实例 / 插件市场 / 设置。
- **中间**：`InstancesView` / `MarketView` / `SettingsView`，随 `view` 切换。
- **右侧**：`LogDrawer.tsx`，常驻第三列 `log-drawer`（`open` / `closed` 通过宽度 440px↔0 过渡）。
  右栏内部：
  - 顶部：标题「运行日志」+ 副标题 + ✕ 收起。
  - 标签行：每个实例一个标签（`●` 实心=运行中 / `○` 空心=停止），末尾固定「市场任务」标签。
  - 主体：实例标签 → `LogPanel`（实时日志 / 过滤 / 搜索 / 自动滚动）；市场任务标签 → `market-drawer-panel`（安装/卸载进度流 + 取消/清空）。
- **Header 按钮顺序**：`运行日志` 按钮**常驻在「最小化到托盘」右边**（顺序不能乱）。
  打开时高亮 `btn-accent` 并显示「收起日志」；有实例启动/运行或市场任务时带绿色脉冲点 `live-dot`。

### 自动弹出规则（必须保持）
- **实例启动 / 重启 / 自动启动** → 自动打开右栏并选中该实例标签（`openLogs('logs')` + `setActiveLogId`）。
- **插件市场安装 / 卸载** → 自动打开右栏并切到「市场任务」标签（`onShowMarketLogs()` → `openLogs('market')`）。
- 市场页只保留一条**精简状态条**（`market-strip`：正在安装 X… + 取消 + 查看进度），完整输出在右栏。

## 4. 前端代码约定

### 4.1 状态管理
- **跨组件共享状态一律提升到 `App.tsx`**：实例日志 `logs`、`activeLogId`、右栏开合 `logsOpen` / `logsTab`、
  市场任务流 `marketLogs` / `marketOp`（`MarketOpState`，见 `types.ts`）。
- **传给子组件的回调必须用 `useCallback` 稳定化**（`showToast` / `openLogs` / `clearMarketLogs` / `cancelMarket` /
  `showMarketLogs` / `setMarketRunning`）。
  ⚠️ 踩过的坑：内联箭头函数每次渲染都是新引用 → 子组件挂载 effect（如 `api.marketOpRunning()`）会反复触发，
  把运行态覆盖成 false。子组件要同步的一次性状态请在挂载时做，且其 `onMarketRunning` 等回调必须稳定。
- 事件订阅**单一所有者**：Wails 事件全部在 `App.tsx` 的 effect 里订阅并统一清理
  （`api.onLog/onStatus/onNotice/onMarketLog/onMarketStatus/onCloseRequest` + 对应 `off*`）。
  子组件（如 MarketView）**不要**自己再 `EventsOn` 同一事件，避免重复订阅/互相 `EventsOff` 清掉对方。

### 4.2 事件流速查（api.ts）
| 事件 | 载荷 | 用途 |
|---|---|---|
| `dsh:log` | `LogEvent` | 实例日志行 |
| `dsh:status` | `StatusEvent` | 实例状态变化 |
| `dsh:notice` | `NoticeEvent` | 顶部 toast |
| `dsh:market-log` | `MarketLogEvent` | 市场操作输出行 |
| `dsh:market-status` | `MarketStatusEvent` | 市场任务运行/完成/失败/取消 |
| `dsh:close-requested` | — | 点窗口 ✕ |

### 4.3 样式
- 新增 UI 用现有设计 token（`--bg/--panel/--accent/--border/--radius/--sp-*` 等，见 `style.css` 顶部），不要自创色值。
- 通用样式进 `style.css`；仅市场页相关的进 `market.css`。

## 5. 构建（完成后的收尾动作，自己做）

```bash
# 1) 前端类型检查 + 构建（必须通过）
cd dsh-launcher/frontend && npm run build        # = tsc && vite build

# 2) 打包桌面 exe（自动重编前端 + Go，产物 build/bin/dsh-launcher.exe）
cd dsh-launcher && wails build
```

> `frontend/dist/`、`build/bin/`、`*.exe` 均在 `.gitignore`，不入库。

## 6. 验证方式

- 前端纯 UI 改动可先用浏览器预览验证：`cd dsh-launcher/frontend && npm run preview -- --port <port>`
- 用 Playwright 打开 `http://localhost:<port>` 前，**注入 stub** 覆盖 `window.runtime` 与 `window.go.main.App`
  （Wails 绑定只在 WebView2 内存在，普通浏览器里会抛错）：
  - `window.runtime.EventsOn/EventsOff` 把回调存到 `window.__dshCbs`，用 `EventsEmit(name, data)` 模拟事件。
  - `window.go.main.App.*` 全部返回 async 桩数据（如 `GetInstances` 返回示例实例数组）。
- 可验证：三栏几何（sidebar 204 / 内容 / 右栏 440，收起=0）、Header 按钮开关、启动自动弹出 + 日志滚动、
  市场安装自动弹出 + 进度流 + 运行态保持。
- 构建产物验证以 `wails build` 成功为准（产物在 `build/bin/dsh-launcher.exe`，无需额外部署）。

## 7. 提交约定

- 中文提交信息，前缀与仓库历史一致：`feat:` / `fix:` / `ui:` / `docs:` / `refactor:`。
- 例：`ui: 运行日志改为右侧常驻第三栏（侧边栏|内容|日志）+ 实例启动/插件安装自动弹出展示进度`。
- 只提交 `dsh-launcher/frontend/src/**` 等源码；`dist/`、`build/bin/`、`*.exe` 不入库。
- 换行符警告（LF→CRLF）可忽略，不影响提交。

## 8. 常见坑速查

1. 布局必须是三栏常驻列，**不是浮层**——用户明确否决过 overlay 抽屉。
2. 回调不 `useCallback` → 子组件 effect 反复触发 → 状态被覆盖（已修过一次，别再犯）。
3. 同一 Wails 事件只能有一个订阅所有者（在 App），子组件勿重复 `EventsOn`/`EventsOff`。
4. 浏览器里 `window.runtime`/`window.go` 不存在，预览必须先 stub。
5. `npm run build` 通过 ≠ 桌面已更新；必须 `wails build`（产物 `build/bin/dsh-launcher.exe`）才算完成。
6. 启动/安装类动作的 toast、日志提示语统一指向「右侧」面板（如“日志见右侧面板”），别写“下方”。

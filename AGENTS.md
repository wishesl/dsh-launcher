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
  - 标签行：**三个平分宽度的纯标签**「实例日志 / 市场任务 / 兼容性」。
  - 实例选择器不在标签行里，而是挂在标题「运行日志」右边（`.log-inst-select`）：它是"我在看哪台"的
    全局上下文，混在标签里读起来不一致（用户明确要求）。
    - 换实例**不切标签**（`pickLogInstance` 只 `setActiveLogId`）：在「兼容性」页换实例应留在兼容性页。
      实例卡片上的「查看日志」走的是另一个回调（`showLog`，会打开右栏并切到日志页）。
    - 右栏打开时若还没选实例，`App.tsx` 会自动选中一台（优先运行中的），否则日志面板是空的。
    - ⚠️ 不要再改回"每个实例一个标签"：实例一多就横向滚动，把后面两个固定标签挤没（用户已否决）。
  - 主体：实例标签 → `LogPanel`（实时日志 / 过滤 / 搜索 / 自动滚动）；市场任务标签 → `market-drawer-panel`（安装/卸载进度流 + 取消/清空）；兼容性标签 → `caps-panel`（能力探测结论，见第 9 节）。
- **Header 按钮顺序**：`运行日志` 按钮**常驻在「最小化到托盘」右边**（顺序不能乱）。
  打开时高亮 `btn-accent` 并显示「收起日志」；有实例启动/运行或市场任务时带绿色脉冲点 `live-dot`。

### 自动弹出规则（必须保持）
- **实例启动 / 重启 / 自动启动** → 自动打开右栏并选中该实例标签（`openLogs('logs')` + `setActiveLogId`）。
- **插件市场安装 / 卸载** → 自动打开右栏并切到「市场任务」标签（`onShowMarketLogs()` → `openLogs('market')`）。
- 市场页只保留一条**精简状态条**（`market-strip`：正在安装 X… + 取消 + 查看进度），完整输出在右栏。

### 实例顺序（可拖拽）
- `InstancesView` 卡片左上角手柄（`.inst-grip`）可拖动重排，键盘聚焦后 ↑/↓ 等价；Esc 取消不落盘。
- `instanceStore` 的顺序是**唯一真相**——实例列表、日志标签页、托盘菜单都按它渲染。
- 落盘走 `ReorderInstances`：后端**只接受当前 id 集合的一个排列**，stale / 缺项 / 重复项一律拒绝并原样返回，
  前端用返回值把 UI 掰回（所以永远不会因为前端列表过期而丢实例）。
- 拖动时列表是实时重排的（不给浮动 ghost），并且**不要用 pointer capture**：实时重排会让 React 挪动 DOM 节点，
  节点被重新插入就会丢 capture。用 window 级 pointermove/pointerup 监听。

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
>
> **版本号**：顶栏品牌区的版本 pill 读 `dsh-launcher/version.go` 的 `version`（默认 `dev`，
> 表示本地构建）。发版时用 ldflags 注入，不要手改源码：
> `wails build -ldflags "-X main.version=0.1.5"`。前端拿不到版本时不渲染 pill。
>
> **最佳适配版本**：同一个文件里的 `bestFitDSHVersion` = "我们实际验证过、各项能力都正常的 DSH 版本"。
> 它**只用于推荐**（版本列表打「最佳适配」标签、实例表单在下拉里标注并在偏离时给一句提示），
> **绝不用来门控** —— 功能能不能用始终由能力探测决定（见第 9 节）。完整验证过一个新版本后改它，
> 或 `wails build -ldflags "-X main.bestFitDSHVersion=0.1.6-rc.1"`。取不到就不打标签、不提示。

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
7. 预览 stub 里 `window.runtime` 必须实现 `EventsOnMultiple`（Wails v2 生成代码在用它，缺了直接白屏）。
8. **跨 shell 层不要比 pid。** 实例 / 市场命令走 `cmd /c`（`shellCommand`），`cmd.Process.Pid` 是
   **外壳 cmd.exe** 的 pid，而 DSH / 插件报的是 node 进程的 pid（cmd → npx → node）。两者按构造
   永远不相等 —— 拿它判断"是不是同一次运行"会 100% 误报。要判断同一次启动请用注入的一次性
   `DSH_LAUNCH_ID`（见 `newLaunchID` / `stalePluginReport`）。
9. **拿不准就不要下结论。** 本仓库反复踩到同一类错误：把探测的"没有证据"当成"失败"，于是功能
   一切正常却报红。凡是缺凭据、缺报告、状态还没落定的场合一律不报（或标 `unknown`），
   把确定的问题留给确定的证据。

---

## 9. 兼容性探测（能力门控）——不要绑 DSH 版本号

上游 DSH 迭代快，启动器有几处必然咬住它内部实现的地方（启动日志格式、loader YAML、插件私有接口）。
对这些耦合点，**按能力探测，不按版本号分支**：版本号是上游控制的时间戳，用户装的是 `latest`，
任何"版本 → 预设"的白名单表都必然滞后一步；而真正的判定条件（那个内部方法还在不在）与版本号没有因果关系。

规则：

1. **探测结论必须有出口。** 只写 logger 等于静默失效（内嵌那次排查了好几轮，就是因为插件探测到
   接口变了只 `return`）。插件把结论写 `<实例目录>/.dsh-self-mcp/capabilities.json`（见插件 README），
   launcher 在 `capabilities.go` 读回并补上自己的探测项，汇总到右栏「兼容性」标签。
2. **只在有明确结论时才拦。** `embedRelax=false` → 对应入口置灰 + 显示原因；没有报告 / 取不到报告
   → **不拦**（fail-open）。"没有报告"混淆了多种原因（插件版本旧、未启用、报告还没写出来），
   硬拦会把本来能用的功能锁死。判断逻辑集中在 `util.ts` 的 `embedGateReason()`。
   ⚠️ **面板行是三态**：`ok` / `fail`（标红）/ `unknown`（中性灰，`CapabilityItem.Unknown=true`）。
   unknown = "没有结论 / 不适用"：插件没装、实例没勾选自管理重启、已装副本是旧版都属于**已知良性原因**。
   **面板一旦误报就会失去可信度** —— 用户看到功能明明正常却满屏红灯，就会学会无视它。所以凡是
   "功能其实还能用"的情况一律 unknown，不要图省事标红；前端所有计数（按钮徽标、标签红点、面板副标题）
   只统计 `!ok && !unknown`。
3. **契约放在自己的边界上。** 报告文件的 schema 由 launcher + 插件双方约定，不依赖 DSH 的任何私有格式。
   面板里每个 `CapabilityItem.id` 是稳定契约键（前端按 id 门控），改名等于破坏兼容。
4. **陈旧报告要失效。** 启动实例前先删报告文件（"文件在" = "本次运行报告过"），再用启动器注入的
   一次性 `DSH_LAUNCH_ID` 与报告里的 `launchId` 比对，对不上才标"可能已过期"。
   ⚠️ **不要用 pid 判断**（见第 8 节第 8 条：跨 `cmd /c` 外壳层的 pid 按构造不相等，会 100% 误报）；
   任一侧缺 `launchId`（旧版插件）时不下结论。
5. **熔断名单只在真出事时加。** 探测有盲区（形状在、语义变），那时才需要"已知坏组合 → 禁用"的黑名单；
   不要预先为每个版本写启用表。

入口：实例页标题右侧的「兼容性检查」按钮（`InstancesView` 的 `.compat-check-btn`）。有问题的实例数直接
落在按钮上（**只统计已跑起来且启用了自管理重启的实例** —— 启动中不判定，否则"报告还没写出来"会一直误报），
点击后打开右栏「兼容性」标签并落到第一台出问题的实例。判定规则集中在 `util.ts` 的 `capsAlert()`。
注意 `.instances-toolbar` 是 `VersionView` 共用的，实例页的布局微调要用 `.instances-toolbar-main` 修饰类收窄。


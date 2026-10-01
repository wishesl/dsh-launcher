# agents.md — DSH Launcher 开发流程约定（AI 代理工作手册）

> 本文档是后续所有开发任务的**强制流程**。做任何改动前先读一遍；改动完成后必须按「构建」一节自己收尾，
> 不要等用户来跑命令。

## 0. 铁律（每次开发必须遵守）

1. **布局约定：菜单放左边，输出放右边。**
   - 左侧 = 主导航菜单（Sidebar：实例 / 插件市场 / 设置）。
   - 右侧 = 运行日志 / 进度输出面板（三栏布局的第三列）。
   - 任何新功能、进度展示、日志输出都必须放进**右侧第三栏**，**禁止做成覆盖式悬浮层/抽屉弹层**
     （用户已明确否决过 overlay 抽屉：不要 `position: fixed` + transform 滑出的盖层，要做成随窗口伸缩的常驻列）。
   - **唯一例外（已拍板，别再"纠正"回去）**：启动器自更新弹窗 `UpdateDialog` **自带完整日志**。
     自更新是系统级动作，与"当前实例"这个上下文无关，塞进右栏会污染标签语义（右栏三个标签都挂在
     当前实例上）；所以 `LogTab` 保持三标签不变，升级进度与日志留在弹窗里（见 `doc/版本升级实现方案.md` §6.3）。
     除这一处外，进度 / 日志输出仍一律进右侧第三列，仍然禁止覆盖式抽屉。
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
- **启动器自更新入口**（`update.go` + `UpdateDialog.tsx`）：顶栏版本 pill 可点（有更新时变强调色 + 红点）；
  mac 布局与内嵌 DSH 视图下品牌区不渲染、没有这个 pill，那两种场景走「设置 → 关于与更新」或托盘「检查更新」。
  更新是**系统级动作**：它的进度与完整日志留在弹窗里（§0.1 的唯一例外），**不要**搬去右栏、
  也**不要**为此新增右栏标签（右栏三个标签都挂在"当前实例"这个上下文上）。
- **自更新跨平台**（`update_apply.go`）：三平台共用"重命名运行中的目标 → 新内容写回原路径"这条策略；
  Windows 换裸 exe、Linux 换 tar.gz 里的裸二进制、macOS 换 `ditto` 打的 zip 里的整个 `.app`（沿用原包名）。
  ⚠️ 解包必须**先整体验路径再解**（tar.gz 两遍扫描）：边扫边取会在遇到目标文件时提前返回，
  后面的 `../` 条目就漏检了。

### 自动弹出规则（必须保持）
- **实例启动 / 重启 / 自动启动** → 自动打开右栏并选中该实例标签（`openLogs('logs')` + `setActiveLogId`）。
- **插件市场安装 / 卸载** → 自动打开右栏并切到「市场任务」标签（`onShowMarketLogs()` → `openLogs('market')`）。
- 市场页只保留一条**精简状态条**（`market-strip`：正在安装 X… + 取消 + 查看进度），完整输出在右栏。

### 实例顺序（可拖拽）
- `InstancesView` 卡片左上角手柄（`.inst-grip`）可拖动重排，键盘聚焦后 ↑/↓ 等价；Esc 取消不落盘。
- **新建的实例插到列表最前面**（`instanceStore.add` 前插，不是 append）：刚建完就该在第一眼看到的位置。
- `instanceStore` 的顺序是**唯一真相**——实例列表、日志标签页、托盘菜单都按它渲染。
- 落盘走 `ReorderInstances`：后端**只接受当前 id 集合的一个排列**，stale / 缺项 / 重复项一律拒绝并原样返回，
  前端用返回值把 UI 掰回（所以永远不会因为前端列表过期而丢实例）。
- 拖动时列表是实时重排的（不给浮动 ghost），并且**不要用 pointer capture**：实时重排会让 React 挪动 DOM 节点，
  节点被重新插入就会丢 capture。用 window 级 pointermove/pointerup 监听。

### 实例卡片（两行两段，别再改回 ⋯ 下拉 / 单行铺一排裸文字）
- 卡片两行，左右两段：左 `.instance-ident`（行 1 = 名称 + 状态徽标；行 2 = 版本 / 来源 pill + 自启开关 + 目录尾巴
  `.instance-path`），右 `.instance-ops`（上行 `.instance-ops-main` = 服务标签 + 启动/停止；下行 `.inst-bar` 次级操作条）。
  两边等高、右侧整列贴右；目录那一格是弹性尾巴，专门用来吃掉卡片中段的空白。
- 操作**全部摊开**、不再收进 ⋯ 菜单（用户明确要求"几个子菜单放出来"），统一放在 `.inst-bar` 里，
  **图标 + 文字**（`lucide-react`），顺序：安装·重装 / 初始化｜屏蔽插件｜查看日志｜编辑｜删除。
- **没有独立的「打开」按钮**：卡片上打开 DSH web 的入口就是「已就绪」标签（`button.instance-svc-tag`，
  带 `ExternalLink` 小图标）——左键打开（只有服务可达时才可点，否则 `aria-disabled` + `is-static`），**右键复制地址**。
- **主操作（启动/停止）单独一个、固定在最右上角**：启动是 `btn-primary`（实心，`Play` 图标）、
  停止是 `btn-ghost`（`Square` 图标），`.inst-power` 定宽所以两种状态不跳位。
- **窄档（卡片 ≤760px）只有一条规则：操作区往左压，什么都不换行**（用户原话："右边按钮直接在左边之上给他
  压住就行了"）。`container-type: inline-size` 挂在 `.instance-card` 上，`@container` 判定 —— 跟窗口宽度
  不是一回事，侧栏 / 右栏都会吃宽度：
  - 行 1 = 名称 + 状态 …… 服务标签 + 启动/停止（**永远不被压**）；行 2 = 版本 / 来源 / 自启 / 目录，
    右段被操作条**压住**（硬切，不是省略号、不是整行隐藏、不是折到第三行）。
  - 做法：操作区盒子**宽度算 0**（`flex: 0 0 0; width: 0`）且**不设 `margin-left:auto`**（auto margin 会吃掉
    剩余空间、身份区就不再 grow），身份区因此拿到整行宽度、操作区被挤到行尾；操作区内部仍是"启动 / 操作条"
    两行（高 62，靠 `align-items: flex-end` 整块向左溢出）。`.instance-ident` 的 `flex-basis` 同时降到 140px
    （它只是"什么时候换行"的预留、不是实际宽度；不降的话运行中的卡片「服务标签 + 启动 = 166px」放不进行 1）。
  - ⚠️ 子块必须 `flex: none; width: max-content`：容器宽度是 0，不写的话操作条被压成 0 宽、
    5 个按钮一列一列折下去，卡片涨到 200px。
  - ⚠️ **别改成 `position:absolute` + `display:contents`**：那样操作区高度塌成一行，操作条上沿会盖住
    「启动」按钮（踩过）。
  - ⚠️ **别再加"放不下就把第二行藏掉 / 让操作条另起一行"的兜底** —— 那就又变成两种样子了，用户明确否过。
  - 卡片 ≤640px 时只把按钮变窄（`.inst-act { font-size: 0; gap: 0 }`，只留图标，文字仍在 `title` 里），
    规则不变、继续压。不这么做，386px 的操作条在更窄的卡片上会盖住自启开关、甚至溢出卡片左边。
  - 实测（卡片 360~1060 逐档扫）：**高度全程 84px**（含宽档，一屏永远等高）；名称 / 状态从不被压；
    操作条从不溢出卡片；自启开关在卡片 ≥440px 时始终可见可点（<440 会被压住，那一档没有更好的解）。
- 宽档（>760px）：身份区两行 + 操作区两行并排。身份区 `flex-basis`（300px = 名称 `max-width` 260 +
  状态徽标 ≈ 60 + 间隙）就是"什么时候让操作区换行"的阈值 —— 宽档真放不下时仍走
  `.instance-body { flex-wrap: wrap }` + `.instance-ops { margin-left: auto }` 这条兜底（正常窗口不触发）。
  ⚠️ 这个值写大了会**提前**换行：写 380 时「身份区 380 + 操作区 374 + 间隙 20」＝ 774，
  超过常见可用宽度（实测 724），于是停止中的卡片全部掉成四行（高 137px），
  而运行中的卡片操作条少一个「重装」（309）没超、仍是两行 84px —— 一屏里高矮不齐。
  调这个值前先量 `.inst-bar` 的实际宽度和可用宽度，别凭感觉写。
  ⚠️ `.instance-ops` 的 flex 必须是 `0 1 auto`（写 `none` 会在窄窗口横向溢出卡片）。
- **第二行（版本 / 来源 / 自启 / 目录）放不下时整行隐藏**（`.instance-ident-meta.is-hidden` → `display:none`），
  不把目录剪成「…\deepseek\harness…」：剪过的目录既看不出是哪个目录、又白占版面。判定在 `InstanceCard.tsx`
  里**实测**——把这行的 `width` 临时设成 `max-content`，量出"不剪字需要多宽"，再跟 `.instance-ident` 的
  可用宽度比。两条纪律：
  1. **需求宽度只在内容变化时量一次**（`useLayoutEffect` 清缓存 + `ResizeObserver` 观察身份区，之后只做比较），
     否则每张卡片每帧都要强制重排；
  2. **不能靠"先藏起来再量"**：`display:none` 量出 0 → 判定放得下 → 显示 → 又放不下 → 来回抖。藏起来时沿用
     上次量到的需求宽度，只有内容变了才恢复显示重测。
  隐藏后启动版本 / 自启状态只在 tooltip（`metaTip`，已补上这两项）和「编辑」表单里能看到。
- 拖拽手柄是自绘的 `GripDots`（两列 × 5 行圆点，见 `InstanceCard.tsx`）：lucide 的 `GripVertical` 只有 3 行，
  在两行高的卡片里又短又小。手柄按钮仍是 `.inst-grip` + `align-self: stretch`。
- 实例名 / 目录的 tooltip（`metaTip`）第一行是完整名字：窄窗口名字会被省略号截断，用户永远能看到全称。

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
| `dsh:update-log` | `{ line }` | 启动器自更新输出行（**渲染在 `UpdateDialog` 里**，见 §0.1 例外） |
| `dsh:update-status` | `UpdateStatusEvent` | 自更新阶段/进度（check→download→verify→apply） |
| `dsh:update-open` | — | 托盘「检查更新」→ 前端开弹窗并发起检查 |
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
   接口变了只 `return`）。插件连上 loopback WebSocket 桥后第一帧 `hello` 把结论全量上报（见插件 README），
   launcher 在 `capabilities.go` 读回并补上自己的探测项，汇总到右栏「兼容性」标签。
2. **只在有明确结论时才拦。** `embedRelax=false` → 对应入口置灰 + 显示原因；没有报告 / 取不到报告
   → **不拦**（fail-open）。"没有报告"混淆了多种原因（插件版本旧、未安装、报告还没上报），
   硬拦会把本来能用的功能锁死。判断逻辑集中在 `util.ts` 的 `embedGateReason()`。
   ⚠️ **面板行是三态**：`ok` / `fail`（标红）/ `unknown`（中性灰，`CapabilityItem.Unknown=true`）。
   unknown = "没有结论 / 不适用"：插件没装、桥接没起来、已装副本是旧版都属于**已知良性原因**。
   **面板一旦误报就会失去可信度** —— 用户看到功能明明正常却满屏红灯，就会学会无视它。所以凡是
   "功能其实还能用"的情况一律 unknown，不要图省事标红；前端所有计数（按钮徽标、标签红点、面板副标题）
   只统计 `!ok && !unknown`。分诊集中在后端 `pluginReportAbsentReason()`，前端不再做二次判断。
3. **契约放在自己的边界上。** 桥接信封的 schema 由 launcher + 插件双方约定，不依赖 DSH 的任何私有格式。
   面板里每个 `CapabilityItem.id` 是稳定契约键（前端按 id 门控），改名等于破坏兼容。
4. **陈旧报告要失效。** 启动实例前先清掉该实例的内存握手记录（"有握手" = "本次运行报告过"），
   再用启动器注入的一次性 `DSH_LAUNCH_ID` 与握手里的 `launchId` 比对，对不上才标"可能已过期"。
   ⚠️ **不要用 pid 判断**（见第 8 节第 8 条：跨 `cmd /c` 外壳层的 pid 按构造不相等，会 100% 误报）；
   任一侧缺 `launchId`（旧版插件）时不下结论。
5. **熔断名单只在真出事时加。** 探测有盲区（形状在、语义变），那时才需要"已知坏组合 → 禁用"的黑名单；
   不要预先为每个版本写启用表。
6. **内置插件是两条半边，而且会被重复装载。** `lib/index.js`（服务端半边：桥接握手、`dsh-restart`、主题写盘）与
   `lib/client.js`（网页端半边：只连桥接做主题即时通道）分别靠 `package.json` 里的 `dsh.client` + `exports["./client"]`
   声明被宿主 Loader 和 DSH 网页加载。⚠️ 一旦声明 `dsh.client`，宿主会**重复装载服务端半边**（同一 pid 两条「已装载」），
   于是模块级状态与定时器都必须按"随时可能被拆掉重来"写：定时器回调一律包 try/catch，自己持有 disposer 并随 fiber
   销毁取消（`cordis-plugin-timer` 的 `ctx.timeout` **不替调用方取消**，拿失效 ctx 续排会抛
   `cannot get required service "timer" in inactive context` 直接把 DSH 打挂）；跨装载会话的续跑负载要落在**进程级**
   变量里，新会话装载时补做。**改了能力或行为必须 bump 插件 `package.json` 版本**（启动器用「内置 vs 已装」版本比对分诊）。

入口：实例页标题右侧的「兼容性检查」按钮（`InstancesView` 的 `.compat-check-btn`）。有问题的实例数直接
落在按钮上（**只统计已跑起来（ready/running）的实例** —— 启动中不判定，否则"报告还没上报"会一直误报；
是否装了桥接插件、桥有没有起来由后端 `pluginReportAbsentReason()` 分诊，判定为 unknown 而非红色），
点击后打开右栏「兼容性」标签并落到第一台出问题的实例。判定规则集中在 `util.ts` 的 `capsAlert()`。
注意 `.instances-toolbar` 是 `VersionView` 共用的，实例页的布局微调要用 `.instances-toolbar-main` 修饰类收窄。


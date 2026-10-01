// Shared frontend types mirroring the Go backend JSON shape.
// These intentionally mirror the generated wailsjs models but as plain
// interfaces so we can pass/receive plain JSON objects.

export type InstanceStatus =
  | 'stopped'
  | 'starting'
  | 'running'   // process alive, web not confirmed yet
  | 'ready'     // web port reachable (URL captured from process output)
  | 'stopping'
  | 'restarting' // dsh-restart: clean self-exit consumed, relaunching the same instance
  | 'crashed';  // process exited on its own with a non-zero code

export interface Instance {
  id: string;
  name: string;
  directory: string;
  version: string;      // "latest" or an exact version
  localVersion: string; // detected in dir, informational
  extraArgs: string;    // optional extra CLI args after "web"
  pkgMgr: string;       // "local" (recommended) | "pnpm" | "npx"
  source?: boolean;     // 源码启动：不用 npm 版本，直接运行目录内 DSH 源码
  initCmd?: string;     // 初始化命令（源码启动，「安装到目录」第一步，默认 "pnpm install"）
  buildCmd?: string;    // 构建命令（源码启动，「安装到目录」第二步，默认 "pnpm run build"）
  startCmd?: string;    // 启动命令（源码启动，「启动」时执行，默认 "pnpm dsh web"）
  autoStart: boolean;
  /** @deprecated 旧实例配置里的历史字段（自管理重启勾选）。挂载桥接插件现在
   *  只取决于「全局是否装了 dsh-launcher-plugin」，与实例无关，UI 已无此项；
   *  保留只为读写老 instances.json 时不丢字段。 */
  selfRestart?: boolean;
  createdAt: any;       // RFC3339 string
  pid: number;
  status: InstanceStatus | string; // union kept loose for forward compat
  webUrl?: string | null; // runtime-captured working URL (from "ready")
}

export interface DSHVersion {
  version: string;
  published: string;
  isLatest: boolean;
}

export interface RegistryInfo {
  package: string;
  latest: string;
  next: string;
  source: string;
  versions: DSHVersion[];
}

export interface LogEvent {
  instanceId: string;
  pid: number;
  line: string;
  stream: 'stdout' | 'stderr' | 'system';
  time: string;
}

export interface StatusEvent {
  instanceId: string;
  status: string;
  pid: number;
  webUrl?: string;        // set when status === 'ready'
  exitCode?: number;      // set when status === 'crashed'
}

// Independent service reachability: does the DSH service this instance is
// configured to serve answer on its port right now? Decoupled from whether the
// launcher itself is managing the process — drives the header "已就绪" + open.
export interface ServiceState {
  instanceId: string;
  url: string;        // "" when not determinable yet (--port 0, no runtime URL)
  reachable: boolean; // the URL answered an HTTP request
}

export interface NoticeEvent {
  msg: string;
}

// --- prerequisite environment (Settings panel) ---
export interface ToolStatus {
  name: string;
  found: boolean;
  version: string; // "" when not found
}

export interface EnvReport {
  npm: ToolStatus;
  pnpm: ToolStatus;
}

export interface EnvLogEvent {
  line: string;
}

// What the user picked in the exit chooser on window close.
export type ExitChoice = 'tray' | 'quit';

// --- plugin market ---
export interface MarketPlugin {
  name: string;
  owner: string;
  url: string;
  category: string;
  description: Record<string, string>; // zh / en
  npm?: string | null;                  // preferred install source when set
  stars?: number | null;
  downloads?: number | null;            // npm 30-day downloads
  install: string;
  added: string;
  deprecated?: boolean;
  replacement?: string;
}

export interface MarketCatalog {
  updated: string;
  count: number;
  categories: Record<string, Record<string, string>>; // id -> {zh,en}
  plugins: MarketPlugin[];
}

export interface InstalledPlugin {
  name: string;
  spec: string;
  version: string;
  kind: string;  // npm | github | linked | other
  state: string; // enabled | disabled
  description: string;
  homepage: string;
  github?: string; // GitHub URL when derivable (spec `github:` or homepage)
}

export interface MarketOpResult {
  ok: boolean;
  cancelled: boolean;
  already?: boolean; // package already installed — soft state, not a failure
  installed: string[];
  blockedBuilds: string[];
  output: string;
  error: string;
}

// --- plugin updates ---
// One availability verdict per installed plugin, produced by
// CheckPluginUpdates (5-minute cache; the 「检查更新」 button forces a refresh).
export interface UpdateCheck {
  name: string;
  current: string;            // installed version
  latest: string;             // npm dist-tags.latest / embedded builtin version
  kind: string;               // npm | github | linked | builtin
  hasUpdate: boolean;
  runnable: boolean;          // false for kinds this release cannot update
  jump: string;               // patch | minor | major | prerelease | downgrade | none | unknown
  inRange: boolean;           // fits the spec → plain `pnpm update` (no spec rewrite)
  risky: boolean;             // needs explicit confirmation
  target: string;             // pnpm target derived server-side
  remote: string;             // npm name or owner/repo
  err: string;                // per-item failure; other rows are unaffected
}

export interface UpdateCheckResult {
  checked: string;            // RFC3339
  plugins: UpdateCheck[];
  updatable: number;
}

export interface MarketSettings {
  registryUrl: string;
  profile: string;
}

// --- network proxy (Settings panel) ---
export interface ProxySettings {
  proxy: string; // "" = direct (no proxy)
}

// UI layout override (Settings panel): "" = auto per OS, "mac", "win".
export type LayoutMode = '' | 'mac' | 'win';

// 三栏里两条可拖拽缝的宽度，持久化在 settings.json（0 = 未设置，用前端默认值）。
export interface UIWidths {
  sidebar: number;
  log: number;
}

export interface MarketLogEvent {
  line: string;
}

export interface MarketStatusEvent {
  state: string; // running | done | failed | cancelled
  kind: string;  // install | uninstall | update
  target: string;
  error?: string;
  blockedBuilds?: string[];
  // Plugin-update version span (absent for install/uninstall).
  name?: string;
  from?: string;
  to?: string;
}

// Live state of the plugin-market operation (hoisted to App so the right-side
// run-log drawer can render the "市场任务" tab from anywhere).
export interface MarketOpState {
  running: boolean;
  kind: string;   // install | uninstall | update
  target: string; // plugin / package name
  from?: string;  // version span, update only
  to?: string;
}

// --- plugin favorites (local, offline, independent of the catalog) ---
// Field shapes mirror the Wails-generated models (Go pointers → optional):
// npm/stars/downloads are `?` + nullable.
export interface FavoritePlugin {
  id: string;             // identity key: npm name (preferred) or owner/repo
  name: string;
  owner: string;
  url: string;            // github url ("" when favorited from installed w/o catalog)
  npm?: string | null;
  install: string;        // pnpm install target (server-validated)
  source: string;         // "catalog" | "installed"
  category: string;
  description: Record<string, string>; // zh / en snapshot
  stars?: number | null;
  downloads?: number | null;
  addedAt: string;        // local favorite time (RFC3339)
}

// Payload for AddFavorite — display metadata only; id/install are derived
// server-side.
export interface FavoriteDraft {
  name: string;
  owner: string;
  url: string;
  npm?: string | null;
  category: string;
  description: Record<string, string>;
  stars?: number | null;
  downloads?: number | null;
  source: string;         // "catalog" | "installed"
  spec?: string;          // installed-source pnpm spec from package.json
}

export interface ShareImportResult {
  imported: FavoritePlugin[];
  skipped: string[];      // ids already present (deduped)
}

// ---------- 兼容性 / 能力探测（见 capabilities.go 与 capabilities.json） ----------

/** 右栏第三个标签页（与实例日志 / 市场任务并列）。 */
export type LogTab = 'logs' | 'market' | 'compat';

/**
 * 面板上的一行。`id` 是稳定契约键 —— 前端按 id 做功能门控
 * （例如 `embedRelax` 决定内嵌入口是否置灰），改名等于破坏兼容。
 */
export interface CapabilityItem {
  id: string;
  label: string;
  ok: boolean;
  /** true = 没有结论 / 不适用（插件未启用、副本是旧版……）。只作中性展示，**不标红** ——
   *  面板一旦误报就会失去可信度，所以要严格区分"失败"和"没有结论"。 */
  unknown: boolean;
  detail: string;   // 证据：解析到的地址 / 门控卡在哪一道
  reason: string;   // ok=false 时的人话原因
  source: string;   // "launcher" | "plugin"
  hint: string;     // 这一项坏掉的后果
}

export interface CapabilityReport {
  instanceId: string;
  instanceName: string;
  version: string;
  status: string;
  plugin: string;   // "dsh-launcher-plugin@0.2.0"；插件未报告时为空
  pluginAt: string; // 报告时间（RFC3339）
  stale: boolean;   // 报告来自上一个进程（pid 不匹配）
  items: CapabilityItem[];
}

// ---------- 顶栏「内置插件连接状态」胶囊 ----------

/**
 * 胶囊的视觉档位。只有 `alert` 是确定的坏消息，其余都是「中性 / 还没结论」：
 * 把「没握手」画成红色等于误报，用户会学会无视它（见 AGENTS.md §9.2）。
 */
export type BridgeTone = 'ok' | 'unknown' | 'stale' | 'alert';

/** 顶栏胶囊要显示的一句话结论（推导规则见 util.ts 的 bridgeStatusOf）。 */
export interface BridgeStatus {
  tone: BridgeTone;
  /** 主文案（短，能塞进胶囊）。 */
  label: string;
  /** 右侧强调字段：版本号或告警计数，'' 表示没有。 */
  value: string;
  /** 悬停详情：点名哪台实例 + 原因原文 + 点下去会发生什么。 */
  title: string;
}

// ---------- 主题同步（dsh-launcher-plugin → launcher，见《dsh-launcher-plugin实现方案.md》） ----------

/**
 * `dsh:theme` 的载荷。`preference` 由后端归一为 `light | dark | system` 三态：
 * system 交给 CSS 的 `prefers-color-scheme` 解析，前端不猜。
 */
export interface ThemeEvent {
  instanceId: string;
  preference: 'light' | 'dark' | 'system' | string;
}

// ---------- 启动器自更新（见 update.go /《版本升级实现方案.md》） ----------

/** 本平台该下载哪个资产（后端按 GOOS/GOARCH 选好，前端只展示）。 */
export interface UpdateAssetRef {
  name: string;
  size: number;
  url: string;
}

/**
 * 启动器的版本结论。三态在后端就分好了：
 * - `err` 非空 = **没有结论**（断网 / 限流 / 接口变了）→ 中性展示，不是错误；
 * - `comparable === false` = 本地 dev 构建（未注入版本号），不参与比较；
 * - `supported === false` = 本平台/本 Release 不能应用内更新 → 给「打开 Release 页」兜底。
 */
export interface LauncherRelease {
  current: string;
  latest: string;
  hasUpdate: boolean;
  comparable: boolean;
  prerelease: boolean;
  name: string;
  notes: string;
  publishedAt: string;
  htmlUrl: string;
  asset: UpdateAssetRef;
  supported: boolean;
  supportNote: string;
  checkedAt: string;
  cached: boolean;
  skipped: boolean;
  err: string;
}

export interface UpdateSettings {
  autoCheck: boolean;
  includePrerelease: boolean;
  skippedVersion: string;
  sourceRepo: string;
}

/** dsh:update-status 的载荷（阶段 + 进度）。 */
export interface UpdateStatusEvent {
  state: string; // running | done | failed | cancelled
  phase: string; // check | download | verify | apply
  percent: number;
  bytes: number;
  total: number;
  error?: string;
}

/** 弹窗持有的自更新运行态（对齐 MarketOpState 的形态，提升到 App）。 */
export interface UpdateOpState {
  running: boolean;
  state: string;
  phase: string;
  percent: number;
  bytes: number;
  total: number;
  error: string;
  /** 本次运行已就位（替换完成，退出后生效）的版本；"" = 没有。 */
  appliedVersion: string;
}


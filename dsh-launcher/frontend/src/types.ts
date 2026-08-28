// Shared frontend types mirroring the Go backend JSON shape.
// These intentionally mirror the generated wailsjs models but as plain
// interfaces so we can pass/receive plain JSON objects.

export type InstanceStatus =
  | 'stopped'
  | 'starting'
  | 'running'   // process alive, web not confirmed yet
  | 'ready'     // web port reachable (URL captured from process output)
  | 'stopping'
  | 'crashed';  // process exited on its own with a non-zero code

export interface Instance {
  id: string;
  name: string;
  directory: string;
  version: string;      // "latest" or an exact version
  localVersion: string; // detected in dir, informational
  extraArgs: string;    // optional extra CLI args after "web"
  pkgMgr: string;       // "local" (recommended) | "pnpm" | "npx"
  autoStart: boolean;
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

export interface MarketSettings {
  registryUrl: string;
  profile: string;
}

// --- network proxy (Settings panel) ---
export interface ProxySettings {
  proxy: string; // "" = direct (no proxy)
}

export interface MarketLogEvent {
  line: string;
}

export interface MarketStatusEvent {
  state: string; // running | done | failed | cancelled
  kind: string;  // install | uninstall
  target: string;
  error?: string;
  blockedBuilds?: string[];
}

// Live state of the plugin-market operation (hoisted to App so the right-side
// run-log drawer can render the "市场任务" tab from anywhere).
export interface MarketOpState {
  running: boolean;
  kind: string;   // install | uninstall
  target: string; // plugin / package name
}

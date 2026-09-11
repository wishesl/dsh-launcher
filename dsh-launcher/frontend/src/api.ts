import {
  AddFavorite,
  ApproveBuilds,
  CancelMarketOp,
  CheckEnvironment,
  CheckPluginUpdates,
  DetectLocalVersion,
  DirectoryExists,
  FetchMarketCatalog,
  GenerateShareCode,
  GetAppDataPath,
  GetEmbedURL,
  GetInstanceMasks,
  GetInstances,
  GetLauncherVersion,
  GetLayout,
  GetUIWidths,
  GetMarketSettings,
  GetProxySettings,
  HideToTray,
  ImportShareCode,
  InstallPnpm,
  InstallPlugin,
  InstallFavorite,
  InstallSelfRestartPlugin,
  InstallToDirectory,
  LaunchInstance,
  ListFavorites,
  ListInstalledPlugins,
  MarketOpRunning,
  ParseShareCode,
  ProbeServices,
  QueryRegistry,
  QuitApp,
  RemoveFavorite,
  RemoveInstance,
  RequestClose,
  RunAutoStartInstances,
  SaveInstance,
  SelectDirectory,
  SelfRestartPluginInstalled,
  SetAutoStart,
  SetInstanceMasks,
  SetLayout,
  SetMarketRegistryURL,
  SetProxy,
  SetUIWidths,
  StopInstance,
  TogglePlugin,
  UninstallPlugin,
  UninstallSelfRestartPlugin,
  UpdatePlugin,
} from '../wailsjs/go/main/App';
import { EventsOff, EventsOn } from '../wailsjs/runtime/runtime';
import type {
  EnvLogEvent,
  EnvReport,
  FavoriteDraft,
  FavoritePlugin,
  Instance,
  InstalledPlugin,
  LogEvent,
  LayoutMode,
  MarketCatalog,
  MarketLogEvent,
  MarketOpResult,
  MarketSettings,
  MarketStatusEvent,
  NoticeEvent,
  ProxySettings,
  RegistryInfo,
  ServiceState,
  ShareImportResult,
  StatusEvent,
  UIWidths,
  UpdateCheckResult,
} from './types';

// Typed wrappers around the Wails-generated bindings, plus event wiring.
// The generated bindings type the models as classes (with helper methods);
// we cast to our plain-interface types since the runtime just JSON-serializes.
export const api = {
  getInstances: (): Promise<Instance[]> => GetInstances(),
  getInstanceMasks: (id: string): Promise<string[]> => GetInstanceMasks(id),
  setInstanceMasks: (id: string, names: string[]): Promise<string[]> =>
    SetInstanceMasks(id, names),
  saveInstance: (i: Instance): Promise<Instance[]> => SaveInstance(i as any),
  removeInstance: (id: string): Promise<Instance[]> => RemoveInstance(id),
  launchInstance: (id: string): Promise<void> => LaunchInstance(id),
  stopInstance: (id: string): Promise<void> => StopInstance(id),
  installToDirectory: (id: string): Promise<Instance[]> => InstallToDirectory(id),
  setAutoStart: (id: string, enabled: boolean): Promise<Instance[]> => SetAutoStart(id, enabled),
  selectDirectory: (): Promise<string> => SelectDirectory(),
  detectLocalVersion: (dir: string): Promise<string> => DetectLocalVersion(dir),
  directoryExists: (dir: string): Promise<boolean> => DirectoryExists(dir),
  probeServices: (): Promise<ServiceState[]> => ProbeServices(),
  queryRegistry: (): Promise<RegistryInfo> => QueryRegistry(),
  runAutoStartInstances: (): Promise<string[]> => RunAutoStartInstances(),
  getAppDataPath: (): Promise<string> => GetAppDataPath(),

  // window close / tray
  hideToTray: (): Promise<void> => HideToTray(),
  quitApp: (): Promise<void> => QuitApp(),
  requestClose: (): Promise<void> => RequestClose(),

  // prerequisite environment (Settings)
  checkEnvironment: (): Promise<EnvReport> => CheckEnvironment(),
  installPnpm: (): Promise<void> => InstallPnpm(),

  // network proxy (Settings)
  getProxy: (): Promise<ProxySettings> => GetProxySettings(),
  setProxy: (url: string): Promise<void> => SetProxy(url),

  // plugin market
  fetchMarketCatalog: (force: boolean): Promise<MarketCatalog> => FetchMarketCatalog(force),
  installPlugin: (instanceId: string, entryUrl: string): Promise<MarketOpResult> =>
    InstallPlugin(instanceId, entryUrl),
  uninstallPlugin: (instanceId: string, name: string): Promise<MarketOpResult> =>
    UninstallPlugin(instanceId, name),
  // 内置插件 dsh-self-mcp（自管理重启）
  selfRestartPluginInstalled: (): Promise<boolean> => SelfRestartPluginInstalled(),
  installSelfRestartPlugin: (instanceId: string): Promise<MarketOpResult> =>
    InstallSelfRestartPlugin(instanceId),
  uninstallSelfRestartPlugin: (instanceId: string): Promise<MarketOpResult> =>
    UninstallSelfRestartPlugin(instanceId),
  cancelMarketOp: (): Promise<boolean> => CancelMarketOp(),
  marketOpRunning: (): Promise<boolean> => MarketOpRunning(),
  listInstalledPlugins: (): Promise<InstalledPlugin[]> => ListInstalledPlugins(),
  togglePlugin: (name: string, enabled: boolean): Promise<void> => TogglePlugin(name, enabled),
  approveBuilds: (names: string[]): Promise<void> => ApproveBuilds(names),
  // 插件更新：检查（force 绕过 5 分钟缓存）+ 单个更新（allowRisky 需前端二次确认）
  checkPluginUpdates: (force: boolean): Promise<UpdateCheckResult> => CheckPluginUpdates(force),
  updatePlugin: (instanceId: string, name: string, allowRisky: boolean): Promise<MarketOpResult> =>
    UpdatePlugin(instanceId, name, allowRisky),
  getMarketSettings: (): Promise<MarketSettings> => GetMarketSettings(),
  setMarketRegistryURL: (url: string): Promise<void> => SetMarketRegistryURL(url),

  // UI layout (Settings)
  getLayout: (): Promise<LayoutMode> => GetLayout() as any,
  setLayout: (mode: LayoutMode): Promise<void> => SetLayout(mode),
  // 三栏可拖拽宽度（落盘在 settings.json，松手时才写）
  getUIWidths: (): Promise<UIWidths> => GetUIWidths() as any,
  setUIWidths: (w: UIWidths): Promise<void> => SetUIWidths(w as any),
  // 内嵌 DSH 视图用：带 token 的地址（后端从实例启动日志解析，每次启动都不同）
  getEmbedURL: (instanceId: string): Promise<string> => GetEmbedURL(instanceId),

  // 启动器自身版本（顶栏品牌区的版本 pill）；发版用 -ldflags 注入，未注入时为 "dev"
  getLauncherVersion: (): Promise<string> => GetLauncherVersion(),

  // plugin favorites (local, offline)
  listFavorites: (): Promise<FavoritePlugin[]> => ListFavorites(),
  addFavorite: (d: FavoriteDraft): Promise<FavoritePlugin[]> => AddFavorite(d as any),
  removeFavorite: (id: string): Promise<FavoritePlugin[]> => RemoveFavorite(id),
  generateShareCode: (ids: string[]): Promise<string> => GenerateShareCode(ids),
  parseShareCode: (code: string): Promise<ShareImportResult> => ParseShareCode(code),
  importShareCode: (code: string, ids: string[]): Promise<ShareImportResult> =>
    ImportShareCode(code, ids),
  installFavorite: (instanceId: string, fav: FavoritePlugin): Promise<MarketOpResult> =>
    InstallFavorite(instanceId, fav as any),

  onMarketLog(cb: (e: MarketLogEvent) => void): void {
    EventsOn('dsh:market-log', cb);
  },
  offMarketLog(): void {
    EventsOff('dsh:market-log');
  },
  onMarketStatus(cb: (e: MarketStatusEvent) => void): void {
    EventsOn('dsh:market-status', cb);
  },
  offMarketStatus(): void {
    EventsOff('dsh:market-status');
  },
  onLog(cb: (e: LogEvent) => void): void {
    EventsOn('dsh:log', cb);
  },
  offLog(): void {
    EventsOff('dsh:log');
  },
  onStatus(cb: (e: StatusEvent) => void): void {
    EventsOn('dsh:status', cb);
  },
  offStatus(): void {
    EventsOff('dsh:status');
  },
  onService(cb: (e: ServiceState) => void): void {
    EventsOn('dsh:service', cb);
  },
  offService(): void {
    EventsOff('dsh:service');
  },
  onNotice(cb: (e: NoticeEvent) => void): void {
    EventsOn('dsh:notice', cb);
  },
  offNotice(): void {
    EventsOff('dsh:notice');
  },
  onCloseRequest(cb: () => void): void {
    EventsOn('dsh:close-requested', cb);
  },
  offCloseRequest(): void {
    EventsOff('dsh:close-requested');
  },
  onEnvLog(cb: (e: EnvLogEvent) => void): void {
    EventsOn('dsh:env-log', cb);
  },
  offEnvLog(): void {
    EventsOff('dsh:env-log');
  },
};

export function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

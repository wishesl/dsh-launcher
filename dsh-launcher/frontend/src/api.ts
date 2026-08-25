import {
  CheckEnvironment,
  DetectLocalVersion,
  DirectoryExists,
  GetAppDataPath,
  GetInstances,
  HideToTray,
  InstallPnpm,
  InstallToDirectory,
  LaunchInstance,
  QueryRegistry,
  QuitApp,
  RemoveInstance,
  RunAutoStartInstances,
  SaveInstance,
  SelectDirectory,
  SetAutoStart,
  StopInstance,
} from '../wailsjs/go/main/App';
import { EventsOff, EventsOn } from '../wailsjs/runtime/runtime';
import type {
  EnvLogEvent,
  EnvReport,
  Instance,
  LogEvent,
  NoticeEvent,
  RegistryInfo,
  StatusEvent,
} from './types';

// Typed wrappers around the Wails-generated bindings, plus event wiring.
// The generated bindings type the models as classes (with helper methods);
// we cast to our plain-interface types since the runtime just JSON-serializes.
export const api = {
  getInstances: (): Promise<Instance[]> => GetInstances(),
  saveInstance: (i: Instance): Promise<Instance[]> => SaveInstance(i as any),
  removeInstance: (id: string): Promise<Instance[]> => RemoveInstance(id),
  launchInstance: (id: string): Promise<void> => LaunchInstance(id),
  stopInstance: (id: string): Promise<void> => StopInstance(id),
  installToDirectory: (id: string): Promise<Instance[]> => InstallToDirectory(id),
  setAutoStart: (id: string, enabled: boolean): Promise<Instance[]> => SetAutoStart(id, enabled),
  selectDirectory: (): Promise<string> => SelectDirectory(),
  detectLocalVersion: (dir: string): Promise<string> => DetectLocalVersion(dir),
  directoryExists: (dir: string): Promise<boolean> => DirectoryExists(dir),
  queryRegistry: (): Promise<RegistryInfo> => QueryRegistry(),
  runAutoStartInstances: (): Promise<string[]> => RunAutoStartInstances(),
  getAppDataPath: (): Promise<string> => GetAppDataPath(),

  // window close / tray
  hideToTray: (): Promise<void> => HideToTray(),
  quitApp: (): Promise<void> => QuitApp(),

  // prerequisite environment (Settings)
  checkEnvironment: (): Promise<EnvReport> => CheckEnvironment(),
  installPnpm: (): Promise<void> => InstallPnpm(),

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

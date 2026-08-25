import {
  DetectLocalVersion,
  GetAppDataPath,
  GetCloseToTray,
  GetInstances,
  HideToTray,
  InstallToDirectory,
  LaunchInstance,
  QueryLatestVersion,
  QueryRegistry,
  RemoveInstance,
  SaveInstance,
  SelectDirectory,
  SetCloseToTray,
  StopInstance,
} from '../wailsjs/go/main/App';
import { EventsOff, EventsOn } from '../wailsjs/runtime/runtime';
import type { Instance, LogEvent, RegistryInfo, StatusEvent } from './types';

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
  selectDirectory: (): Promise<string> => SelectDirectory(),
  detectLocalVersion: (dir: string): Promise<string> => DetectLocalVersion(dir),
  queryRegistry: (): Promise<RegistryInfo> => QueryRegistry(),
  queryLatestVersion: (): Promise<string> => QueryLatestVersion(),
  getAppDataPath: (): Promise<string> => GetAppDataPath(),
  hideToTray: (): Promise<void> => HideToTray(),
  getCloseToTray: (): Promise<boolean> => GetCloseToTray(),
  setCloseToTray: (v: boolean): Promise<void> => SetCloseToTray(v),

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
};

export function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

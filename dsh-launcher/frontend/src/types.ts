// Shared frontend types mirroring the Go backend JSON shape.
// These intentionally mirror the generated wailsjs models but as plain
// interfaces so we can pass/receive plain JSON objects.

export interface Instance {
  id: string;
  name: string;
  directory: string;
  version: string;      // "latest" or an exact version
  localVersion: string; // detected in dir, informational
  extraArgs: string;    // optional extra CLI args after "web"
  pkgMgr: string;       // "pnpm" (recommended) | "npx"
  autoStart: boolean;
  createdAt: any;       // RFC3339 string
  pid: number;
  status: string;       // "stopped" | "running" | "starting" | "stopping"
}

export type InstanceStatus = 'stopped' | 'running' | 'starting' | 'stopping';

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
}

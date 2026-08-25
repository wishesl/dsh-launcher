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

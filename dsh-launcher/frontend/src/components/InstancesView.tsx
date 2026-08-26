import { useState } from 'react';
import type { Instance, LogEvent, RegistryInfo } from '../types';
import InstanceCard from './InstanceCard';
import LogDrawer from './LogDrawer';
import VersionPanel from './VersionPanel';

interface Props {
  instances: Instance[];
  registry: RegistryInfo | null;
  registryLoading: boolean;
  busyId: string | null;
  activeLogId: string | null;
  logs: Record<string, LogEvent[]>;
  onAdd: () => void;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onInstall: (id: string) => void;
  onOpen: (url: string) => void;
  onCopyUrl: (url: string) => void;
  onEdit: (inst: Instance) => void;
  onDelete: (id: string) => void;
  onSelectLog: (id: string) => void;
  onClearLog: (id: string) => void;
  onToggleAutoStart: (id: string, v: boolean) => void;
  onRefreshRegistry: () => void;
}

export default function InstancesView({
  instances,
  registry,
  registryLoading,
  busyId,
  activeLogId,
  logs,
  onAdd,
  onStart,
  onStop,
  onInstall,
  onOpen,
  onCopyUrl,
  onEdit,
  onDelete,
  onSelectLog,
  onClearLog,
  onToggleAutoStart,
  onRefreshRegistry,
}: Props) {
  const [showVersion, setShowVersion] = useState(false);
  const running = instances.filter(
    (i) => i.status === 'running' || i.status === 'starting' || i.status === 'ready'
  ).length;
  const liveCount = Object.values(logs).reduce((n, arr) => n + arr.length, 0);

  return (
    <div className="instances-view">
      <div className="instances-toolbar">
        <h2>实例</h2>
        <div className="status-strip" title="运行中 / 实例总数">
          <span>运行 <b className="live">{running}</b> / <b>{instances.length}</b></span>
          <span className="muted">·</span>
          <span>日志 <b>{liveCount}</b> 行</span>
        </div>
        <button className="btn btn-ghost btn-sm" onClick={() => setShowVersion((v) => !v)} title="npm 版本历史">
          {showVersion ? '收起版本' : '版本历史'}
        </button>
        <button className="btn btn-ghost btn-sm" onClick={onRefreshRegistry} disabled={registryLoading}>
          {registryLoading ? '刷新中…' : '刷新版本'}
        </button>
        <button className="btn btn-primary" onClick={onAdd}>+ 添加实例</button>
      </div>

      {showVersion && (
        <div className="version-panel">
          <VersionPanel registry={registry} loading={registryLoading} />
        </div>
      )}

      <div className="instance-grid">
        {instances.length === 0 && (
          <div className="empty">
            <p>还没有任何实例</p>
            <p className="muted">点击「添加实例」，选择目录和 DSH 版本即可在不同目录启动不同版本。</p>
          </div>
        )}
        {instances.map((inst) => (
          <InstanceCard
            key={inst.id}
            instance={inst}
            registry={registry}
            busy={busyId === inst.id}
            activeLog={activeLogId === inst.id}
            onStart={onStart}
            onStop={onStop}
            onInstall={onInstall}
            onOpen={onOpen}
            onCopyUrl={onCopyUrl}
            onEdit={onEdit}
            onDelete={onDelete}
            onToggleLog={onSelectLog}
            onToggleAutoStart={onToggleAutoStart}
          />
        ))}
      </div>

      <LogDrawer
        instances={instances}
        logs={logs}
        activeLogId={activeLogId}
        onSelect={onSelectLog}
        onClear={onClearLog}
      />
    </div>
  );
}

import type { Instance, LogEvent, RegistryInfo, ServiceState } from '../types';
import InstanceCard from './InstanceCard';

interface Props {
  instances: Instance[];
  service: Record<string, ServiceState>;
  registry: RegistryInfo | null;
  registryLoading: boolean;
  busyId: string | null;
  activeLogId: string | null;
  logsOpen: boolean;
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
  onToggleAutoStart: (id: string, v: boolean) => void;
}

export default function InstancesView({
  instances,
  service,
  registry,
  registryLoading,
  busyId,
  activeLogId,
  logsOpen,
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
  onToggleAutoStart,
}: Props) {
  const running = instances.filter(
    (i) => i.status === 'running' || i.status === 'starting' || i.status === 'ready'
  ).length;
  const liveCount = Object.values(logs).reduce((n, arr) => n + arr.length, 0);

  return (
    <div className="view-page">
      <div className="instances-toolbar">
        <h2>实例</h2>
        <div className="status-strip" title="运行中 / 实例总数">
          <span>运行 <b className="live">{running}</b> / <b>{instances.length}</b></span>
          <span className="muted">·</span>
          <span>日志 <b>{liveCount}</b> 行</span>
        </div>
        <button className="btn btn-primary" onClick={onAdd}>+ 添加实例</button>
      </div>

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
            service={service[inst.id] ?? null}
            registry={registry}
            busy={busyId === inst.id}
            activeLog={activeLogId === inst.id && logsOpen}
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
    </div>
  );
}

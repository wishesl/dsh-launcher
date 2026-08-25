import type { Instance, RegistryInfo } from '../types';
import InstanceCard from './InstanceCard';

interface Props {
  instances: Instance[];
  registry: RegistryInfo | null;
  busyId: string | null;
  activeLogId: string | null;
  onAdd: () => void;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onInstall: (id: string) => void;
  onOpen: (url: string) => void;
  onCopyUrl: (url: string) => void;
  onEdit: (inst: Instance) => void;
  onDelete: (id: string) => void;
  onToggleLog: (id: string) => void;
  onToggleAutoStart: (id: string, v: boolean) => void;
}

export default function InstanceList({
  instances,
  registry,
  busyId,
  activeLogId,
  onAdd,
  onStart,
  onStop,
  onInstall,
  onOpen,
  onCopyUrl,
  onEdit,
  onDelete,
  onToggleLog,
  onToggleAutoStart,
}: Props) {
  return (
    <section className="panel instances-panel">
      <div className="panel-head">
        <h2>实例列表</h2>
        <span className="count-badge">{instances.length}</span>
        <button className="btn btn-primary btn-sm" onClick={onAdd}>+ 添加实例</button>
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
            onToggleLog={onToggleLog}
            onToggleAutoStart={onToggleAutoStart}
          />
        ))}
      </div>
    </section>
  );
}

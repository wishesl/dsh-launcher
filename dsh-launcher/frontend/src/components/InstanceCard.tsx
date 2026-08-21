import type { Instance, RegistryInfo } from '../types';

interface Props {
  instance: Instance;
  registry: RegistryInfo | null;
  busy: boolean;
  activeLog: boolean;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onEdit: (inst: Instance) => void;
  onDelete: (id: string) => void;
  onToggleLog: (id: string) => void;
}

const STATUS_META: Record<string, { label: string; cls: string }> = {
  running: { label: '运行中', cls: 'status-running' },
  starting: { label: '启动中…', cls: 'status-starting' },
  stopping: { label: '停止中…', cls: 'status-stopping' },
  stopped: { label: '已停止', cls: 'status-stopped' },
};

export default function InstanceCard({
  instance,
  registry,
  busy,
  activeLog,
  onStart,
  onStop,
  onEdit,
  onDelete,
  onToggleLog,
}: Props) {
  const st = STATUS_META[instance.status] ?? STATUS_META.stopped;
  const isRunning = instance.status === 'running' || instance.status === 'starting';
  const isBusy = busy || instance.status === 'starting' || instance.status === 'stopping';

  const outdated = instance.localVersion && registry && registry.latest && instance.localVersion !== registry.latest;

  return (
    <div className={`instance-card ${activeLog ? 'active' : ''}`}>
      <div className="instance-top">
        <span className={`status-dot ${st.cls}`} title={st.label} />
        <span className="instance-name" title={instance.directory}>{instance.name}</span>
        <span className={`pill pill-version ${instance.version === 'latest' ? 'pill-accent' : ''}`}>
          {instance.version === 'latest' ? 'latest' : instance.version}
        </span>
        <span className="instance-state">{st.label}</span>
      </div>

      <div className="instance-dir" title={instance.directory}>{instance.directory}</div>

      <div className="instance-meta">
        {instance.localVersion ? (
          <span className="meta-item" title="目录内 node_modules 中实际安装的 DSH 版本（npx 优先使用它）">
            本地副本 <b>{instance.localVersion}</b>
            {outdated && <span className="tag-warn">有新版</span>}
          </span>
        ) : (
          <span className="meta-item muted">本地无副本（将从 registry 拉取）</span>
        )}
        {instance.pid > 0 && <span className="meta-item">PID {instance.pid}</span>}
        {instance.extraArgs && <span className="meta-item mono">args: {instance.extraArgs}</span>}
      </div>

      <div className="instance-actions">
        {isRunning ? (
          <button className="btn btn-danger btn-sm" onClick={() => onStop(instance.id)} disabled={isBusy}>
            停止
          </button>
        ) : (
          <button className="btn btn-primary btn-sm" onClick={() => onStart(instance.id)} disabled={isBusy}>
            启动
          </button>
        )}
        <button className="btn btn-ghost btn-sm" onClick={() => onToggleLog(instance.id)}>
          {activeLog ? '收起日志' : '查看日志'}
        </button>
        <button className="btn btn-ghost btn-sm" onClick={() => onEdit(instance)}>编辑</button>
        <button className="btn btn-ghost btn-sm danger-text" onClick={() => onDelete(instance.id)}>删除</button>
      </div>
    </div>
  );
}

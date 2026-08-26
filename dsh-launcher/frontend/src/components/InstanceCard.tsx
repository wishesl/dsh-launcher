import type { Instance, RegistryInfo } from '../types';
import { getWebUrl } from '../util';
import Switch from './Switch';

interface Props {
  instance: Instance;
  registry: RegistryInfo | null;
  busy: boolean;
  activeLog: boolean;
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

const STATUS_META: Record<string, { label: string; cls: string; rail: string }> = {
  running: { label: '运行中', cls: 'sb-running', rail: 'rail-running' },
  starting: { label: '启动中…', cls: 'sb-starting', rail: 'rail-starting' },
  ready: { label: '已就绪', cls: 'sb-ready', rail: 'rail-ready' },
  stopping: { label: '停止中…', cls: 'sb-stopping', rail: 'rail-stopping' },
  stopped: { label: '已停止', cls: 'sb-stopped', rail: 'rail-stopped' },
  crashed: { label: '异常退出', cls: 'sb-crashed', rail: 'rail-crashed' },
};

const PKGMGR_LABEL: Record<string, string> = {
  local: '本地',
  pnpm: 'pnpm',
  npx: 'npx',
};

export default function InstanceCard({
  instance,
  registry,
  busy,
  activeLog,
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
  const st = STATUS_META[instance.status] ?? STATUS_META.stopped;
  const isRunning =
    instance.status === 'running' ||
    instance.status === 'starting' ||
    instance.status === 'ready';
  const isBusy = busy || instance.status === 'starting' || instance.status === 'stopping';
  const pkgMgr = instance.pkgMgr || 'local';

  const outdated = instance.localVersion && registry && registry.latest && instance.localVersion !== registry.latest;
  const needsInstall = pkgMgr === 'local' && !instance.localVersion;
  const webUrl = getWebUrl(instance);
  const canOpen = !!instance.webUrl || (isRunning && !webUrl.autoPort);
  const openTitle = instance.webUrl
    ? '在浏览器打开 DSH web'
    : webUrl.autoPort
      ? '--port 0 自动选端口，等待进程输出实际地址后可点击'
      : isRunning
        ? '在浏览器打开 DSH web（若尚未就绪可能打不开）'
        : '实例未运行';

  return (
    <div className={`instance-card ${st.rail} ${activeLog ? 'active' : ''}`}>
      <div className="instance-top">
        <span className="instance-name" title={instance.directory}>{instance.name}</span>
        <span className={`status-badge ${st.cls}`}>
          <span className="status-dot" />
          {st.label}
        </span>
        <span className={`pill pill-version ${instance.version === 'latest' ? 'pill-accent' : ''}`}>
          {instance.version === 'latest' ? 'latest' : instance.version}
        </span>
        <span className="pill" title="启动方式">{PKGMGR_LABEL[pkgMgr] ?? pkgMgr}</span>
        <label
          className="autostart-toggle"
          title="随启动器自动启动此实例：打开 DSH Launcher 时自动拉起"
        >
          <Switch checked={instance.autoStart} onChange={(v) => onToggleAutoStart(instance.id, v)} />
          自启
        </label>
      </div>

      <div className="instance-dir" title={instance.directory}>{instance.directory}</div>

      <div className="instance-url-row">
        <span className="meta-item">web:</span>
        <code
          className={`mono url-text ${webUrl.runtime ? 'url-runtime' : ''}`}
          title={webUrl.runtime ? '从运行中进程捕获的真实地址（点击复制）' : 'DSH web 地址（点击复制）'}
          onClick={() => onCopyUrl(webUrl.url)}
        >
          {webUrl.url}
        </code>
        <button
          className="btn btn-primary btn-sm"
          onClick={() => onOpen(webUrl.url)}
          disabled={!canOpen}
          title={openTitle}
        >
          打开
        </button>
      </div>

      <div className="instance-meta">
        {instance.localVersion ? (
          <span className="meta-item" title="目录内 node_modules 中实际安装的 DSH 版本（npx 优先使用它）">
            本地副本 <b>{instance.localVersion}</b>
            {outdated && <span className="tag-warn">有新版</span>}
          </span>
        ) : (
          <span className={`meta-item ${needsInstall ? 'meta-warn' : ''}`}>
            {needsInstall ? '本地副本未安装 — 点「安装到目录」' : '本地无副本（将从 registry 拉取）'}
          </span>
        )}
        {instance.pid > 0 && <span className="meta-item">PID {instance.pid}</span>}
        {instance.extraArgs && <span className="meta-item mono">args: {instance.extraArgs}</span>}
      </div>

      <div className="instance-actions">
        {isRunning ? (
          <button className="btn btn-danger" onClick={() => onStop(instance.id)} disabled={isBusy}>
            停止
          </button>
        ) : (
          <button className="btn btn-primary" onClick={() => onStart(instance.id)} disabled={isBusy}>
            启动
          </button>
        )}
        {!isRunning && (
          <button
            className={`btn ${needsInstall ? 'btn-accent' : 'btn-ghost'}`}
            onClick={() => onInstall(instance.id)}
            disabled={isBusy}
            title={needsInstall ? '把该版本真实安装进目录（生成可读源码 node_modules）' : '重新安装该版本到目录（生成可读源码）'}
          >
            安装到目录
          </button>
        )}
        <button className="btn btn-ghost" onClick={() => onToggleLog(instance.id)}>
          {activeLog ? '收起日志' : '查看日志'}
        </button>
        <button className="btn btn-ghost" onClick={() => onEdit(instance)}>编辑</button>
        <button className="btn btn-ghost danger-text" onClick={() => onDelete(instance.id)}>删除</button>
      </div>
    </div>
  );
}

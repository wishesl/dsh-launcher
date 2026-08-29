import type { Instance, RegistryInfo, ServiceState } from '../types';
import { getWebUrl } from '../util';
import Switch from './Switch';

interface Props {
  instance: Instance;
  service?: ServiceState | null; // independent port-service reachability
  registry: RegistryInfo | null;
  busy: boolean;
  activeLog: boolean;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onInstall: (id: string) => void;
  onMask: (inst: Instance) => void;
  onOpen: (url: string) => void;
  onCopyUrl: (url: string) => void;
  onEdit: (inst: Instance) => void;
  onDelete: (id: string) => void;
  onToggleLog: (id: string) => void;
  onToggleAutoStart: (id: string, v: boolean) => void;
}

// Process-managed state (launcher spawn/kill). "ready" is folded into 运行中:
// whether the DSH service is actually up is shown by the independent service
// indicator below, not by the process badge — the two must not affect each other.
const STATUS_META: Record<string, { label: string; cls: string; rail: string }> = {
  running: { label: '运行中', cls: 'sb-running', rail: 'rail-running' },
  starting: { label: '启动中…', cls: 'sb-starting', rail: 'rail-starting' },
  ready: { label: '运行中', cls: 'sb-running', rail: 'rail-running' },
  stopping: { label: '停止中…', cls: 'sb-stopping', rail: 'rail-stopping' },
  restarting: { label: '重启中…', cls: 'sb-restarting', rail: 'rail-restarting' },
  stopped: { label: '已停止', cls: 'sb-stopped', rail: 'rail-stopped' },
  crashed: { label: '异常退出', cls: 'sb-crashed', rail: 'rail-crashed' },
};

export default function InstanceCard({
  instance,
  service,
  registry,
  busy,
  activeLog,
  onStart,
  onStop,
  onInstall,
  onMask,
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
  const isBusy = busy || instance.status === 'starting' || instance.status === 'stopping' || instance.status === 'restarting';
  const pkgMgr = instance.pkgMgr || 'local';
  const isSource = !!instance.source; // 源码启动：目录内源码 + 自定义命令

  const outdated = !isSource && instance.localVersion && registry && registry.latest && instance.localVersion !== registry.latest;
  const needsInstall = !isSource && pkgMgr === 'local' && !instance.localVersion;

  // Service state (decoupled from process state): only a reachable port gives
  // a usable 打开 button.
  const svcUrl = service && service.reachable && service.url ? service.url : null;
  const webUrl = getWebUrl(instance);
  const displayUrl = svcUrl ?? webUrl.url;
  const canOpen = !!svcUrl;
  const svcTag =
    service == null
      ? { text: '服务检测中…', cls: 'tag-muted', title: '正在检测端口服务…' }
      : service.reachable
        ? { text: '服务已就绪', cls: 'tag-ok', title: '配置端口当前可访问 DSH 服务' }
        : {
            text: '服务未就绪',
            cls: 'tag-warn',
            title: service.url
              ? `端口 ${service.url} 当前未响应`
              : '端口未知（--port 0 时需等待进程输出实际地址）',
          };
  const openTitle = svcUrl
    ? '在浏览器打开 DSH web'
    : service == null
      ? '正在检测端口服务，稍后可点击'
      : webUrl.autoPort
        ? '--port 0 自动选端口，等待进程输出实际地址后可点击'
        : '服务未就绪（端口未响应），无法打开';

  return (
    <div className={`instance-card ${st.rail} ${activeLog ? 'active' : ''}`}>
      <div className="instance-top">
        <span className="instance-name" title={instance.directory}>{instance.name}</span>
        <span className={`status-badge ${st.cls}`}>
          <span className="status-dot" />
          {st.label}
        </span>
        {!isSource && (
          <span className={`pill pill-version ${instance.version === 'latest' ? 'pill-accent' : ''}`}>
            {instance.version === 'latest' ? 'latest' : instance.version}
          </span>
        )}
        <span
          className="pill"
          title={isSource ? '源码启动：直接执行自定义命令（初始化 / 构建 / 启动）' : '版本启动：从 npm 安装 DSH 按版本启动'}
        >
          {isSource ? 'code' : 'npm'}
        </span>
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
          className={`mono url-text ${svcUrl ? 'url-runtime' : ''}`}
          title={svcUrl ? 'DSH 服务当前地址（点击复制）' : 'DSH web 地址（点击复制）'}
          onClick={() => onCopyUrl(displayUrl)}
        >
          {displayUrl}
        </code>
        <span className={`tag ${svcTag.cls}`} title={svcTag.title}>{svcTag.text}</span>
        <button
          className="btn btn-primary btn-sm"
          onClick={() => onOpen(displayUrl)}
          disabled={!canOpen}
          title={openTitle}
        >
          打开
        </button>
      </div>

      <div className="instance-meta">
        {isSource ? (
          <>
            {instance.localVersion && (
              <span className="meta-item" title="目录内 node_modules 中实际安装的 DSH 版本">
                本地副本 <b>{instance.localVersion}</b>
              </span>
            )}
            <span className="meta-item mono" title="启动命令（点击「启动」执行）；「安装到目录」执行初始化+构建">
              启动: {instance.startCmd || 'pnpm dsh web'}
            </span>
          </>
        ) : instance.localVersion ? (
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
        {instance.selfRestart && <span className="meta-item mono">self-restart</span>}
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
            title={isSource
              ? '执行初始化+构建命令（默认 pnpm install + pnpm run build）'
              : needsInstall
                ? '把该版本真实安装进目录（生成可读源码 node_modules）'
                : '重新安装该版本到目录（生成可读源码）'}
          >
            安装到目录
          </button>
        )}
        <button
          className="btn btn-ghost"
          onClick={() => onMask(instance)}
          title="选择该实例启动时临时屏蔽的插件（仅本次启动生效，不改全局开关，停止后恢复）"
        >
          屏蔽插件
        </button>
        <button
          className={`btn btn-ghost ${activeLog ? 'active-log-btn' : ''}`}
          onClick={() => onToggleLog(instance.id)}
          title="在右侧运行日志面板查看该实例的日志"
        >
          查看日志
        </button>
        <button className="btn btn-ghost" onClick={() => onEdit(instance)}>编辑</button>
        <button className="btn btn-ghost danger-text" onClick={() => onDelete(instance.id)}>删除</button>
      </div>
    </div>
  );
}

import { useEffect, useRef, useState } from 'react';
import type { Instance, RegistryInfo, ServiceState } from '../types';
import { GripVertical, MoreHorizontal } from 'lucide-react';
import { getWebUrl } from '../util';
import Switch from './Switch';

interface Props {
  instance: Instance;
  service?: ServiceState | null; // independent port-service reachability
  registry: RegistryInfo | null;
  busy: boolean;
  activeLog: boolean;
  /** 刚要落到新位置（拖拽松手后的一小段）：给个轻量落位动画遮掉"占位条突然展开"的生硬感。 */
  landed?: boolean;
  onDragHandleDown: (e: React.PointerEvent<HTMLElement>, id: string) => void;
  onDragHandleKeyDown: (e: React.KeyboardEvent<HTMLElement>, id: string) => void;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onInstall: (id: string) => void;
  onMask: (inst: Instance) => void;
  onOpen: (url: string) => void;
  onCopyUrl: (url: string) => void;
  onEdit: (inst: Instance) => void;
  onDelete: (id: string) => void;
  onSelectLog: (id: string) => void;
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

/** 状态对应的左侧色条 class（设置 --rail）。幽灵卡片复用同一套配色。 */
export function statusRail(status: string): string {
  return (STATUS_META[status] ?? STATUS_META.stopped).rail;
}

export default function InstanceCard({
  instance,
  service,
  registry,
  busy,
  activeLog,
  landed,
  onDragHandleDown,
  onDragHandleKeyDown,
  onStart,
  onStop,
  onInstall,
  onMask,
  onOpen,
  onCopyUrl,
  onEdit,
  onDelete,
  onSelectLog,
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

  // "有新版"判定：本地副本既不等于 latest 稳定版、也不等于 next 预发布版，才算落后。
  // 之前只跟 latest 比，导致比 latest 还新的 rc 版也被误标成"有新版"。
  const outdated = !isSource
    && instance.localVersion
    && registry
    && registry.latest
    && instance.localVersion !== registry.latest
    && instance.localVersion !== registry.next;
  const needsInstall = !isSource && pkgMgr === 'local' && !instance.localVersion;

  // Service state (decoupled from process state): only a reachable port gives
  // a usable 打开 button.
  const svcUrl = service && service.reachable && service.url ? service.url : null;
  const webUrl = getWebUrl(instance);
  const displayUrl = svcUrl ?? webUrl.url;
  const canOpen = !!svcUrl;
  const svcTag =
    service == null
      ? { text: '检测中', cls: 'tag-muted', title: '正在检测端口服务…' }
      : service.reachable
        ? { text: '已就绪', cls: 'tag-ok', title: '配置端口当前可访问 DSH 服务' }
        : {
            text: '未就绪',
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

  // ⋯ 下拉：收纳低频操作（安装 / 屏蔽 / 日志 / 编辑 / 删除）。
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!menuOpen) return;
    const onDoc = (e: MouseEvent) => {
      if (!menuRef.current?.contains(e.target as Node)) setMenuOpen(false);
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setMenuOpen(false); };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, [menuOpen]);

  // 给 tooltip 用的完整 meta 串（鼠标悬停在版本/URL 行时看到）。
  const metaTip = [
    `目录：${instance.directory}`,
    instance.localVersion ? `本地副本：${instance.localVersion}${outdated ? '（有新版）' : ''}` : '本地无副本',
    instance.pid > 0 ? `PID ${instance.pid}` : null,
    instance.extraArgs ? `args: ${instance.extraArgs}` : null,
    instance.selfRestart ? 'self-restart' : null,
  ].filter(Boolean).join('\n');

  return (
    <div
      className={`instance-card ${st.rail} ${activeLog ? 'active' : ''} ${landed ? 'inst-landed' : ''}`}
      data-inst-id={instance.id}
    >
      {/* 拖拽手柄：放在卡片直接子元素层级（不包在行 1 里），用 align-self:stretch
          让它贯穿整张卡片的高度（从行 1 顶部到行 2 底部）。 */}
      <button
        type="button"
        className="inst-grip"
        title="拖动调整顺序（也可聚焦后按 ↑ / ↓）"
        aria-label={`调整「${instance.name}」的顺序`}
        onPointerDown={(e) => onDragHandleDown(e, instance.id)}
        onKeyDown={(e) => onDragHandleKeyDown(e, instance.id)}
      >
        <GripVertical size={16} strokeWidth={2} aria-hidden />
      </button>

      {/* 主体列：行 1（身份）+ 行 2（操作）包成一列，手柄在左、这列在右 */}
      <div className="instance-body">
      {/* 行 1：身份信息 —— 名称 | 状态 | 版本 | 来源 | 自启 ... 服务 tag | ⋯ */}
      <div className="instance-top">
        <span className="instance-name" title={metaTip}>{instance.name}</span>
        <span className={`status-badge ${st.cls}`}>
          <span className="status-dot" />
          {st.label}
        </span>
        {!isSource && (
          <span
            className={`pill pill-version ${instance.version === 'latest' ? 'pill-accent' : ''}`}
            title={`启动版本：${instance.version}${outdated ? '（本地副本有新版）' : ''}`}
          >
            {instance.version === 'latest' ? 'latest' : instance.version}
            {outdated && <span className="pill-dot-warn" />}
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

        <span className="instance-top-spacer" />

        {/* 运行中的实例才显示「已就绪/未就绪」tag（停止的实例端口没监听，显示这个没意义） */}
        {isRunning && (
          <span
            className={`tag ${svcTag.cls}`}
            title={`${svcTag.title}\n${metaTip}`}
            onClick={() => onCopyUrl(displayUrl)}
            style={{ cursor: 'pointer' }}
          >
            {svcTag.text}
          </span>
        )}

        {/* ⋯ 菜单：低频操作全收这里 */}
        <div className="inst-menu" ref={menuRef}>
          <button
            type="button"
            className="btn btn-ghost btn-sm inst-menu-btn"
            onClick={() => setMenuOpen((v) => !v)}
            title="更多操作"
            aria-label="更多操作"
            aria-haspopup="menu"
            aria-expanded={menuOpen}
          >
            <MoreHorizontal size={14} strokeWidth={2} aria-hidden />
          </button>
          {menuOpen && (
            <div className="inst-menu-pop" role="menu">
              {!isRunning && (
                <button
                  type="button"
                  role="menuitem"
                  className={`inst-menu-item ${needsInstall ? 'accent' : ''}`}
                  onClick={() => { setMenuOpen(false); onInstall(instance.id); }}
                  disabled={isBusy}
                >
                  {needsInstall ? '安装到目录（首次）' : '重新安装到目录'}
                </button>
              )}
              <button
                type="button"
                role="menuitem"
                className="inst-menu-item"
                onClick={() => { setMenuOpen(false); onMask(instance); }}
              >
                屏蔽插件…
              </button>
              <button
                type="button"
                role="menuitem"
                className={`inst-menu-item ${activeLog ? 'active' : ''}`}
                onClick={() => { setMenuOpen(false); onSelectLog(instance.id); }}
              >
                查看日志
              </button>
              <button
                type="button"
                role="menuitem"
                className="inst-menu-item"
                onClick={() => { setMenuOpen(false); onEdit(instance); }}
              >
                编辑…
              </button>
              <div className="inst-menu-sep" />
              <button
                type="button"
                role="menuitem"
                className="inst-menu-item danger"
                onClick={() => { setMenuOpen(false); onDelete(instance.id); }}
              >
                删除…
              </button>
            </div>
          )}
        </div>
      </div>

      {/* 行 2：主操作 —— 启动/停止 + 打开（左对齐，与身份信息的缩进对齐）。
          所有按钮统一用 btn-ghost（白底浅边框），避免深色实心在一排卡片里太突兀。 */}
      <div className="instance-actions-row">
        {isRunning ? (
          <button className="btn btn-ghost btn-sm" onClick={() => onStop(instance.id)} disabled={isBusy}>
            ■ 停止
          </button>
        ) : (
          <button className="btn btn-ghost btn-sm" onClick={() => onStart(instance.id)} disabled={isBusy}>
            ▶ 启动
          </button>
        )}
        <button
          className="btn btn-ghost btn-sm"
          onClick={() => onOpen(displayUrl)}
          disabled={!canOpen}
          title={openTitle}
        >
          打开
        </button>
      </div>
      </div>{/* /.instance-body */}
    </div>
  );
}

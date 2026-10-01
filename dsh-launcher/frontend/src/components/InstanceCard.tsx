import { useLayoutEffect, useRef, useState } from 'react';
import type { Instance, RegistryInfo, ServiceState } from '../types';
import {
  ExternalLink,
  EyeOff,
  Hammer,
  Pencil,
  Play,
  RefreshCw,
  Square,
  Terminal,
  Trash2,
} from 'lucide-react';
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

/** 拖拽手柄图标：两列 × 5 行圆点。
 *  lucide 的 GripVertical 只有 3 行（size=16），在两行高的卡片里显得又短又小；
 *  这里自己画一串更长的点，手柄一眼可辨。 */
function GripDots() {
  const rows = [3, 7, 11, 15, 19];
  const cols = [3.5, 8.5];
  return (
    <svg width="12" height="22" viewBox="0 0 12 22" fill="currentColor" aria-hidden focusable="false">
      {rows.map((cy) => cols.map((cx) => <circle key={`${cx}-${cy}`} cx={cx} cy={cy} r="1.35" />))}
    </svg>
  );
}

/** 目录只显示"尾巴"（…\父\当前）：一眼看出跑的是哪个目录，又不把第二行撑爆。 */
function shortDir(dir: string): string {
  const trimmed = dir.replace(/[\\/]+$/, '');
  const sep = trimmed.includes('\\') ? '\\' : '/';
  const parts = trimmed.split(/[\\/]/).filter(Boolean);
  if (parts.length <= 2) return trimmed;
  return `…${sep}${parts.slice(-2).join(sep)}`;
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
  // 实例名就是版本号时（很多人这么命名）不再重复挂一枚一模一样的 version pill。
  const versionRedundant = instance.name === instance.version;

  // Service state (decoupled from process state): only a reachable port can be opened.
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
  // 去掉独立的「打开」按钮后，服务标签就是这台实例打开 DSH web 的入口：
  // 左键打开（仅"已就绪"时可用），右键复制地址。
  const svcTagTitle = canOpen
    ? `在浏览器打开 DSH web：${displayUrl}\n右键复制地址`
    : `${svcTag.title}\n服务可达后才能打开；右键可复制地址`;

  // 安装/初始化按钮：源码实例是"初始化+构建"，本地无副本是"安装"，其余是"重装"。
  const installLabel = isSource ? '初始化' : needsInstall ? '安装' : '重装';
  const installTitle = isSource
    ? '按实例的自定义命令执行初始化 / 构建'
    : needsInstall
      ? '本地还没有 DSH 副本：先安装到目录'
      : '重新安装到目录（覆盖本地副本）';

  // 给 tooltip 用的完整 meta 串（鼠标悬停在名称 / 目录时看到；窄窗口名字会被省略号截断，
  // 所以第一行就是完整名字，保证任何时候都能看到全称）。
  // 第二行被隐藏时（见下方 metaHidden），启动版本与自启状态就只有这里能看到了。
  const metaTip = [
    instance.name,
    `目录：${instance.directory}`,
    !isSource ? `启动版本：${instance.version}` : null,
    instance.localVersion ? `本地副本：${instance.localVersion}${outdated ? '（有新版）' : ''}` : '本地无副本',
    `自启：${instance.autoStart ? '开' : '关'}`,
    instance.pid > 0 ? `PID ${instance.pid}` : null,
    instance.extraArgs ? `args: ${instance.extraArgs}` : null,
  ].filter(Boolean).join('\n');

  // 第二行（版本 / 来源 / 自启 / 目录）是可选的次要信息：窄卡片里放不下时**整行隐藏**，
  // 而不是把目录剪成「…\deepseek\harness…」——剪过的目录既看不出是哪个目录、又白占版面。
  // 判定用实测：量这一行"不剪字"需要的宽度（max-content），跟身份区可用宽度比。
  // ⚠️ 需求宽度只在内容变化时量一次（之后只做比较）：既避开每帧强制重排，
  // 也避开"藏了量不到 0 → 以为放得下 → 显示 → 又放不下"的来回抖动。
  const [metaHidden, setMetaHidden] = useState(false);
  const identRef = useRef<HTMLDivElement | null>(null);
  const metaRef = useRef<HTMLDivElement | null>(null);
  const metaNeedRef = useRef(0);

  // 内容变了（目录 / 版本 / 自启…）：作废缓存并先恢复显示，好让下一次布局能量到新宽度。
  // 都在 layout effect 里跑，浏览器不会画到中间态。
  useLayoutEffect(() => {
    metaNeedRef.current = 0;
    setMetaHidden(false);
  }, [isSource, instance.version, outdated, instance.autoStart, instance.directory]);

  useLayoutEffect(() => {
    const ident = identRef.current;
    const meta = metaRef.current;
    if (!ident || !meta) return;
    const measure = () => {
      // 只有显示着才量得到（display:none 能量出 0）；藏起来时沿用上次量到的需求宽度。
      if (metaNeedRef.current === 0 && meta.offsetParent !== null) {
        const prev = meta.style.width;
        meta.style.width = 'max-content';
        metaNeedRef.current = Math.ceil(meta.getBoundingClientRect().width);
        meta.style.width = prev;
      }
      if (metaNeedRef.current > 0) setMetaHidden(ident.clientWidth < metaNeedRef.current);
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(ident);
    return () => ro.disconnect();
  }, [metaHidden, isSource, instance.version, outdated, instance.autoStart, instance.directory]);

  return (
    <div
      className={`instance-card ${st.rail} ${activeLog ? 'active' : ''} ${landed ? 'inst-landed' : ''}`}
      data-inst-id={instance.id}
    >
      {/* 拖拽手柄：放在卡片直接子元素层级（不包在身份区里），用 align-self:stretch
          让它贯穿整张卡片的高度。 */}
      <button
        type="button"
        className="inst-grip"
        title="拖动调整顺序（也可聚焦后按 ↑ / ↓）"
        aria-label={`调整「${instance.name}」的顺序`}
        onPointerDown={(e) => onDragHandleDown(e, instance.id)}
        onKeyDown={(e) => onDragHandleKeyDown(e, instance.id)}
      >
        <GripDots />
      </button>

      {/* 主体：左「身份区」（两行）+ 右「操作区」（两行），两侧等高、右对齐。
          窗口够宽时并排；不够宽时整个操作区换行（见 .instance-ops 的 margin-left:auto）。 */}
      <div className="instance-body">
        {/* 身份区 —— 行 1：名称 + 状态；行 2：版本 / 来源 / 自启 / 目录（放不下时整行隐藏） */}
        <div className="instance-ident" ref={identRef}>
          <div className="instance-ident-top">
            <span className="instance-name" title={metaTip}>{instance.name}</span>
            <span className={`status-badge ${st.cls}`}>
              <span className="status-dot" />
              {st.label}
            </span>
          </div>

          <div className={`instance-ident-meta${metaHidden ? ' is-hidden' : ''}`} ref={metaRef}>
            {!isSource && !versionRedundant && (
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

            {/* 目录：第二行的弹性尾巴，把卡片中段那段空白用真实信息填掉（完整路径在 tooltip 里） */}
            <span className="instance-path" title={metaTip}>
              {shortDir(instance.directory)}
            </span>
          </div>
        </div>

        {/* 操作区 —— 上行：服务标签 + 启动/停止（主操作单独一个）；下行：次级操作条 */}
        <div className="instance-ops">
          <div className="instance-ops-main">
            {/* 运行中的实例才显示「已就绪/未就绪」：停止的实例端口没监听，显示这个没意义 */}
            {isRunning && (
              <button
                type="button"
                className={`tag ${svcTag.cls} instance-svc-tag${canOpen ? '' : ' is-static'}`}
                aria-disabled={!canOpen}
                onClick={() => { if (canOpen) onOpen(displayUrl); }}
                onContextMenu={(e) => { e.preventDefault(); onCopyUrl(displayUrl); }}
                title={svcTagTitle}
              >
                {svcTag.text}
                {canOpen && <ExternalLink size={12} strokeWidth={2} aria-hidden />}
              </button>
            )}

            {isRunning ? (
              <button
                className="btn btn-ghost btn-sm inst-power"
                onClick={() => onStop(instance.id)}
                disabled={isBusy}
                title="停止这台实例（结束进程）"
              >
                <Square size={10} strokeWidth={2.5} fill="currentColor" aria-hidden />
                停止
              </button>
            ) : (
              <button
                className="btn btn-primary btn-sm inst-power"
                onClick={() => onStart(instance.id)}
                disabled={isBusy}
                title="启动这台实例（右侧运行日志会自动打开）"
              >
                <Play size={12} strokeWidth={2} fill="currentColor" aria-hidden />
                启动
              </button>
            )}
          </div>

          {/* 次级操作条：浅底分组（工具栏观感），比"一排裸文字"更有结构、点击区也更大 */}
          <div className="inst-bar" role="group" aria-label={`「${instance.name}」的操作`}>
            {!isRunning && (
              <>
                <button
                  type="button"
                  className={`inst-act${needsInstall ? ' accent' : ''}`}
                  onClick={() => onInstall(instance.id)}
                  disabled={isBusy}
                  title={installTitle}
                >
                  {isSource
                    ? <Hammer size={13} strokeWidth={2} aria-hidden />
                    : <RefreshCw size={13} strokeWidth={2} aria-hidden />}
                  {installLabel}
                </button>
                <span className="inst-act-sep" aria-hidden />
              </>
            )}
            <button
              type="button"
              className="inst-act"
              onClick={() => onMask(instance)}
              title="配置要屏蔽的插件（输出进右侧「市场任务」面板）"
            >
              <EyeOff size={13} strokeWidth={2} aria-hidden />
              屏蔽插件
            </button>
            <span className="inst-act-sep" aria-hidden />
            <button
              type="button"
              className={`inst-act${activeLog ? ' active' : ''}`}
              onClick={() => onSelectLog(instance.id)}
              title="在右侧运行日志里查看这台实例"
            >
              <Terminal size={13} strokeWidth={2} aria-hidden />
              查看日志
            </button>
            <span className="inst-act-sep" aria-hidden />
            <button
              type="button"
              className="inst-act"
              onClick={() => onEdit(instance)}
              title="编辑实例配置（目录 / 版本 / 参数）"
            >
              <Pencil size={13} strokeWidth={2} aria-hidden />
              编辑
            </button>
            <span className="inst-act-sep" aria-hidden />
            <button
              type="button"
              className="inst-act danger"
              onClick={() => onDelete(instance.id)}
              title="删除实例（目录里的文件不动）"
            >
              <Trash2 size={13} strokeWidth={2} aria-hidden />
              删除
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

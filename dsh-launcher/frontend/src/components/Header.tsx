import type { Instance, RegistryInfo } from '../types';
import appicon from '../../../build/appicon.png';

interface Props {
  registry: RegistryInfo | null;
  registryLoading: boolean;
  onRefreshRegistry: () => void;
  onHideToTray: () => void;
  // DSH quick status: the ready / first-running instance, or null.
  dshLive: Instance | null;
  onRestartDsh: () => void;
  onOpenWeb: (url: string) => void;
}

export default function Header({
  registry,
  registryLoading,
  onRefreshRegistry,
  onHideToTray,
  dshLive,
  onRestartDsh,
  onOpenWeb,
}: Props) {
  const ready = dshLive?.status === 'ready' && !!dshLive.webUrl;
  const running = !!dshLive && dshLive.status !== 'stopped' && dshLive.status !== 'crashed';

  return (
    <header className="app-header">
      <div className="brand">
        <img className="brand-logo-img" src={appicon} alt="DSH Launcher" draggable={false} />
        <div className="brand-text">
          <h1>DSH Launcher</h1>
          <p className="brand-sub">DeepSeek Harness 启动器</p>
        </div>
      </div>

      <div className="header-right">
        {/* DSH 快捷状态 + 重启 */}
        <div
          className="dsh-chip"
          title={ready ? 'DSH web 已就绪，点击打开' : running ? 'DSH 正在运行，等待 web 就绪' : '当前没有运行中的 DSH 实例'}
        >
          <span className={`dot ${ready ? 'dot-live' : running ? 'dot-warn' : ''}`} />
          {ready ? (
            <button className="dsh-chip-open" onClick={() => onOpenWeb((dshLive as Instance).webUrl as string)}>
              DSH 已就绪 · {dshLive!.name} · {dshLive!.webUrl}
            </button>
          ) : running ? (
            <span className="chip-label">{dshLive!.status === 'starting' ? 'DSH 启动中…' : 'DSH 运行中…'}</span>
          ) : (
            <span className="chip-label">DSH 未运行</span>
          )}
        </div>
        <button
          className="btn btn-ghost btn-sm"
          onClick={onRestartDsh}
          disabled={!dshLive}
          title={dshLive ? `重启「${dshLive.name}」的 DSH web` : '没有运行中的实例可重启'}
        >
          重启
        </button>

        <div className="latest-chip" title={`npm 最新版本（来源: ${registry?.source ?? '-'}）`}>
          <span className={`dot ${registry ? 'dot-live' : ''}`} />
          {registryLoading ? (
            <span className="chip-label">查询中…</span>
          ) : registry ? (
            <>
              <span className="chip-label">npm latest</span>
              <span className="chip-version">{registry.latest}</span>
              {registry.next && registry.next !== registry.latest && (
                <>
                  <span className="chip-label">next</span>
                  <span className="chip-version chip-muted">{registry.next}</span>
                </>
              )}
            </>
          ) : (
            <span className="chip-label">无法获取版本</span>
          )}
        </div>
        <button className="btn btn-ghost btn-sm" onClick={onRefreshRegistry} disabled={registryLoading} title="刷新最新版本">
          {registryLoading ? '刷新中…' : '刷新版本'}
        </button>
        <button className="btn btn-ghost btn-sm" onClick={onHideToTray} title="隐藏到系统托盘，进程继续运行">
          最小化到托盘
        </button>
      </div>
    </header>
  );
}

import { useCallback, useEffect, useState } from 'react';
import { WindowIsMaximised, WindowMinimise, WindowToggleMaximise } from '../../wailsjs/runtime/runtime';
import type { Instance, RegistryInfo } from '../types';
import appicon from '../../../build/appicon.png';

interface Props {
  registry: RegistryInfo | null;
  registryLoading: boolean;
  // DSH quick status: the ready / first-running instance, or null.
  dshLive: Instance | null;
  onRestartDsh: () => void;
  onOpenWeb: (url: string) => void;
  // Right-side run-log drawer toggle (permanent header button).
  logsOpen: boolean;
  logsLive: boolean;
  onToggleLogs: () => void;
  // Frameless 窗口右上角自定义关闭按钮 → 与原生 ✕ 相同的关闭流程。
  onCloseRequest: () => void;
}

export default function Header({
  registry,
  registryLoading,
  dshLive,
  onRestartDsh,
  onOpenWeb,
  logsOpen,
  logsLive,
  onToggleLogs,
  onCloseRequest,
}: Props) {
  const ready = dshLive?.status === 'ready' && !!dshLive.webUrl;
  const running = !!dshLive && dshLive.status !== 'stopped' && dshLive.status !== 'crashed';

  // 自定义窗口控制：最小化 / 最大化-还原 / 关闭（替代被去掉的原生标题栏按钮）。
  const [maximized, setMaximized] = useState(false);

  const syncMaximized = useCallback(async () => {
    try {
      setMaximized(await WindowIsMaximised());
    } catch {
      /* 浏览器预览时无 runtime，忽略 */
    }
  }, []);

  useEffect(() => {
    syncMaximized();
    window.addEventListener('resize', syncMaximized);
    return () => window.removeEventListener('resize', syncMaximized);
  }, [syncMaximized]);

  const onMin = useCallback(() => {
    try {
      WindowMinimise();
    } catch {
      /* ignore */
    }
  }, []);

  const onMax = useCallback(() => {
    try {
      WindowToggleMaximise();
      // 切完刷新图标状态（最大化也会触发 resize，双保险）。
      window.setTimeout(() => syncMaximized(), 100);
    } catch {
      /* ignore */
    }
  }, [syncMaximized]);

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
        <button
          className={`btn btn-sm log-toggle-btn ${logsOpen ? 'btn-accent' : 'btn-ghost'}`}
          onClick={onToggleLogs}
          title={logsOpen ? '收起右侧运行日志面板' : '打开右侧运行日志面板（实例启动 / 插件安装时自动弹出）'}
        >
          {logsLive && <span className="live-dot" title="有实例正在启动或有任务运行中" />}
          {logsOpen ? '收起日志' : '运行日志'}
        </button>

        {/* 自定义窗口控制：最小化 / 最大化-还原 / 关闭（替代原生标题栏按钮） */}
        <div className="win-controls">
          <button className="win-btn" onClick={onMin} title="最小化" aria-label="最小化">
            <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
              <path d="M0 5h10" stroke="currentColor" strokeWidth="1" />
            </svg>
          </button>
          <button
            className="win-btn"
            onClick={onMax}
            title={maximized ? '还原' : '最大化'}
            aria-label={maximized ? '还原' : '最大化'}
          >
            {maximized ? (
              <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
                <rect x="0.5" y="2.5" width="7" height="7" fill="none" stroke="currentColor" />
                <path d="M2.5 2.5v-2h7v7h-2" fill="none" stroke="currentColor" />
              </svg>
            ) : (
              <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
                <rect x="0.5" y="0.5" width="9" height="9" fill="none" stroke="currentColor" />
              </svg>
            )}
          </button>
          <button className="win-btn win-close" onClick={onCloseRequest} title="关闭" aria-label="关闭">
            <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
              <path d="M0.5 0.5l9 9M9.5 0.5l-9 9" stroke="currentColor" strokeWidth="1" />
            </svg>
          </button>
        </div>
      </div>
    </header>
  );
}

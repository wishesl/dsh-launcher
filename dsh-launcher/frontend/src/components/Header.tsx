import type { Instance, RegistryInfo } from '../types';
import { RotateCw } from 'lucide-react';

interface Props {
  registry: RegistryInfo | null;
  registryLoading: boolean;
  // Service-driven "DSH 已就绪": the instance whose configured port answers
  // HTTP right now — independent of whether the launcher manages its process.
  serviceLive: { id: string; name: string; url: string } | null;
  // Process-managed running instance (运行中… fallback + 重启 button).
  dshLive: Instance | null;
  onRestartDsh: () => void;
  onOpenWeb: (url: string) => void;
  // Right-side run-log drawer toggle (permanent header button).
  logsOpen: boolean;
  logsLive: boolean;
  onToggleLogs: () => void;
}

export default function Header({
  registry,
  registryLoading,
  serviceLive,
  dshLive,
  onRestartDsh,
  onOpenWeb,
  logsOpen,
  logsLive,
  onToggleLogs,
}: Props) {
  // Ready = the configured port actually serves DSH (service state), NOT the
  // launcher's process status — so an externally-started DSH on the same port
  // still counts and the open button always works when the service is up.
  const ready = !!serviceLive;
  const running = !ready && !!dshLive && dshLive.status !== 'stopped' && dshLive.status !== 'crashed';

  return (
    <header className="app-header">
      <div className="header-right">
        {/* DSH 快捷状态 + 重启 */}
        <div
          className="dsh-chip"
          title={ready ? 'DSH 服务可达，点击打开' : running ? 'DSH 正在运行，等待服务就绪' : '当前没有运行中的 DSH 实例'}
        >
          <span className={`dot ${ready ? 'dot-live' : running ? 'dot-warn' : ''}`} />
          {ready ? (
            <button className="dsh-chip-open" onClick={() => onOpenWeb(serviceLive!.url)}>
              DSH 已就绪 · {serviceLive!.name} · {serviceLive!.url}
            </button>
          ) : running ? (
            <span className="chip-label">{dshLive!.status === 'starting' ? 'DSH 启动中…' : 'DSH 运行中…'}</span>
          ) : (
            <span className="chip-label">DSH 未运行</span>
          )}
        </div>

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
          className="btn btn-ghost btn-icon"
          onClick={onRestartDsh}
          disabled={!dshLive}
          title={dshLive ? `重启「${dshLive.name}」的 DSH web` : '没有运行中的实例可重启'}
          aria-label="重启"
        >
          <RotateCw size={16} strokeWidth={1.75} aria-hidden />
        </button>
        <button
          className={`btn btn-sm log-toggle-btn ${logsOpen ? 'btn-accent' : 'btn-ghost'}`}
          onClick={onToggleLogs}
          title={logsOpen ? '收起右侧运行日志面板' : '打开右侧运行日志面板（实例启动 / 插件安装时自动弹出）'}
        >
          {logsLive && <span className="live-dot" title="有实例正在启动或有任务运行中" />}
          {logsOpen ? '收起日志' : '运行日志'}
        </button>
      </div>
    </header>
  );
}

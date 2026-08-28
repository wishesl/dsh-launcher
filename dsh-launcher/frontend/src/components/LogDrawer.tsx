import { useEffect, useRef } from 'react';
import type { Instance, LogEvent, MarketOpState } from '../types';
import LogPanel from './LogPanel';

interface Props {
  open: boolean;
  onClose: () => void;
  instances: Instance[];
  logs: Record<string, LogEvent[]>;
  activeLogId: string | null;
  onSelect: (id: string) => void;
  onClear: (id: string) => void;
  tab: 'logs' | 'market';
  onTabChange: (t: 'logs' | 'market') => void;
  marketLogs: string[];
  marketOp: MarketOpState;
  onClearMarketLogs: () => void;
  onCancelMarket: () => void;
}

export default function LogDrawer({
  open,
  onClose,
  instances,
  logs,
  activeLogId,
  onSelect,
  onClear,
  tab,
  onTabChange,
  marketLogs,
  marketOp,
  onClearMarketLogs,
  onCancelMarket,
}: Props) {
  const logRef = useRef<HTMLDivElement>(null);
  const marketRef = useRef<HTMLDivElement>(null);

  const activeInstance = instances.find((i) => i.id === activeLogId) ?? null;
  const activeLogs = activeLogId ? logs[activeLogId] ?? [] : [];
  const isLive = (inst: Instance) =>
    inst.status === 'running' || inst.status === 'starting' || inst.status === 'ready';
  const marketBusy = marketOp.running;

  // Scroll instance logs to the bottom when the drawer opens / instance changes.
  useEffect(() => {
    if (open && tab === 'logs') {
      setTimeout(() => logRef.current?.scrollTo({ top: logRef.current.scrollHeight }), 60);
    }
  }, [open, tab, activeLogId]);

  // Market tab: keep scrolled to the bottom while output streams in.
  useEffect(() => {
    if (open && tab === 'market') {
      const el = marketRef.current;
      if (el) el.scrollTop = el.scrollHeight;
    }
  }, [open, tab, marketLogs]);

  const opLabel = marketOp.kind === 'uninstall' ? '卸载' : '安装';

  // The run-log pane is an in-flow third column of the app body: it takes a
  // slice of the window width when open and collapses to 0 when closed, so the
  // whole interface always reads as sidebar | content | run-log.
  return (
    <aside className={`log-drawer ${open ? 'open' : 'closed'}`} aria-label="运行日志" aria-hidden={!open}>
      <div className="log-drawer-head">
        <span className="log-drawer-title">运行日志</span>
        <span className="log-drawer-sub">
          {tab === 'market'
            ? marketBusy
              ? `正在${opLabel} ${marketOp.target}…`
              : '插件市场任务'
            : activeInstance
              ? `${activeInstance.name} · ${activeLogs.length} 行`
              : '选择一个实例查看日志'}
        </span>
        <button className="log-drawer-close" onClick={onClose} title="收起日志">✕</button>
      </div>

      <div className="log-drawer-tabs">
        {instances.length === 0 && <span className="muted" style={{ padding: '6px 10px' }}>暂无实例</span>}
        {instances.map((inst) => (
          <button
            key={inst.id}
            className={`log-tab ${tab === 'logs' && activeLogId === inst.id ? 'active' : ''}`}
            onClick={() => {
              onSelect(inst.id);
              onTabChange('logs');
            }}
          >
            <span className={isLive(inst) ? 'live' : undefined}>●</span> {inst.name}
          </button>
        ))}
        <button
          className={`log-tab log-tab-market ${tab === 'market' ? 'active' : ''} ${marketBusy ? 'busy' : ''}`}
          onClick={() => onTabChange('market')}
        >
          {marketBusy ? <span className="live">●</span> : <span className="dim">○</span>} 市场任务
        </button>
      </div>

      <div className="log-drawer-body">
        {tab === 'market' ? (
          <div className="market-drawer-panel">
            <div className="market-drawer-head">
              <span className="market-drawer-status">
                {marketBusy ? (
                  <>
                    <span className="spin" />
                    正在{opLabel} {marketOp.target}…
                  </>
                ) : marketLogs.length > 0 ? (
                  <>上次{opLabel}输出（{marketOp.target || ''}）</>
                ) : (
                  <>暂无任务</>
                )}
              </span>
              <div className="row">
                {marketBusy && (
                  <button className="btn btn-ghost btn-sm" onClick={onCancelMarket}>取消</button>
                )}
                <button className="btn btn-ghost btn-sm" onClick={onClearMarketLogs}>清空</button>
              </div>
            </div>
            <div className="market-drawer-body" ref={marketRef}>
              {marketLogs.length === 0 ? (
                <span className="muted">
                  {marketBusy ? '等待输出…' : '在「插件市场」安装 / 卸载插件时，进度会实时显示在这里。'}
                </span>
              ) : (
                marketLogs.map((l, i) => <div key={i}>{l}</div>)
              )}
            </div>
          </div>
        ) : (
          <LogPanel
            instance={activeInstance}
            logs={activeLogs}
            onClear={() => activeLogId && onClear(activeLogId)}
            logRef={logRef}
          />
        )}
      </div>
    </aside>
  );
}

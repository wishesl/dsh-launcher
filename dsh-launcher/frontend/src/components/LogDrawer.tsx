import { useEffect, useRef } from 'react';
import type { CapabilityReport, Instance, LogEvent, LogTab, MarketOpState } from '../types';
import { hasCapabilityFailure } from '../util';
import LogPanel from './LogPanel';

interface Props {
  open: boolean;
  onClose: () => void;
  /** 展开态的宽度（可拖拽）。收起时内联宽度取 0，让 .22s 过渡照常播放。 */
  width: number;
  instances: Instance[];
  logs: Record<string, LogEvent[]>;
  activeLogId: string | null;
  onSelect: (id: string) => void;
  onClear: (id: string) => void;
  tab: LogTab;
  onTabChange: (t: LogTab) => void;
  marketLogs: string[];
  marketOp: MarketOpState;
  onClearMarketLogs: () => void;
  onCancelMarket: () => void;
  /** 当前选中实例的能力探测结果（「兼容性」标签用；未探测到时为 undefined）。 */
  caps?: CapabilityReport;
  onRefreshCaps: () => void;
}

export default function LogDrawer({
  open,
  onClose,
  width,
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
  caps,
  onRefreshCaps,
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
    <aside
      className={`log-drawer ${open ? 'open' : 'closed'}`}
      style={{ width: open ? width : 0 }}
      aria-label="运行日志"
      aria-hidden={!open}
    >
      <div className="log-drawer-head">
        <span className="log-drawer-title">运行日志</span>
        {/* 实例选择器属于"我在看哪台"的上下文，放在标题旁边；标签行留给纯标签，
            否则一个下拉混在两个标签里读起来不一致。 */}
        {instances.length === 0 ? (
          <span className="log-drawer-sub">暂无实例</span>
        ) : (
          <select
            className="log-inst-select"
            value={activeLogId ?? instances[0].id}
            onChange={(e) => onSelect(e.target.value)}
            title="选择要查看的实例（日志与兼容性都跟着它）"
          >
            {instances.map((inst) => (
              <option key={inst.id} value={inst.id}>
                {isLive(inst) ? '● ' : '○ '}
                {inst.name}
                {isLive(inst) ? '（运行中）' : '（已停止）'}
              </option>
            ))}
          </select>
        )}
        <span className="log-drawer-sub">
          {tab === 'compat'
            ? caps
              ? `${caps.items.filter((i) => !i.ok && !i.unknown).length} 项不可用`
              : '探测中…'
            : tab === 'market'
              ? marketBusy
                ? `正在${opLabel} ${marketOp.target}…`
                : '插件市场任务'
              : activeInstance
                ? `${activeLogs.length} 行`
                : '选择一个实例查看日志'}
        </span>
        <button className="log-drawer-close" onClick={onClose} title="收起日志">✕</button>
      </div>

      <div className="log-drawer-tabs">
        {/* 三个纯标签平分宽度：实例日志是默认页（实例由标题旁的下拉决定）。 */}
        <button
          className={`log-tab ${tab === 'logs' ? 'active' : ''}`}
          onClick={() => onTabChange('logs')}
        >
          {activeInstance && isLive(activeInstance) ? (
            <span className="live">●</span>
          ) : (
            <span className="dim">○</span>
          )}{' '}
          实例日志
        </button>
        <button
          className={`log-tab log-tab-market ${tab === 'market' ? 'active' : ''} ${marketBusy ? 'busy' : ''}`}
          onClick={() => onTabChange('market')}
        >
          {marketBusy ? <span className="live">●</span> : <span className="dim">○</span>} 市场任务
        </button>
        {/* 兼容性：探测结果有红灯时标签上直接亮红点 —— 这就是"让失败可见"的入口，
            不用等用户点进去才发现某项能力已经失效。 */}
        <button
          className={`log-tab log-tab-compat ${tab === 'compat' ? 'active' : ''}`}
          onClick={() => onTabChange('compat')}
          title="兼容性探测：这台实例上各项能力到底能不能用（不按 DSH 版本号判断）"
        >
          {hasCapabilityFailure(caps) ? (
            <span className="caps-bad-dot">●</span>
          ) : (
            <span className="dim">○</span>
          )}{' '}
          兼容性
        </button>
      </div>

      <div className="log-drawer-body">
        {tab === 'compat' ? (
          <div className="caps-panel">
            <div className="caps-head">
              <span className="caps-head-title">
                {caps ? `${caps.instanceName} · DSH ${caps.version || '版本未知'}` : '正在探测…'}
              </span>
              <button
                className="btn btn-ghost btn-sm"
                onClick={onRefreshCaps}
                disabled={!caps && !activeInstance}
              >
                重新探测
              </button>
            </div>
            {!caps ? (
              <span className="muted">
                {activeInstance
                  ? '正在读取能力报告…'
                  : '先在左侧选一个实例，或在上面点一个实例标签。'}
              </span>
            ) : (
              <>
                <div className="caps-meta">
                  <span>插件 {caps.plugin || '未报告'}</span>
                  {caps.pluginAt && <span>{new Date(caps.pluginAt).toLocaleTimeString()}</span>}
                </div>
                {caps.stale && (
                  <p className="caps-stale">
                    ⚠ 这份报告来自上一个进程（pid 对不上），结论可能已过期 —— 重新启动该实例即可刷新。
                  </p>
                )}
                <div className="caps-list">
                  {caps.items.map((it) => {
                    // 三态：ok / bad（确定失败，标红）/ unknown（没有结论、不适用 —— 中性）
                    const state = it.ok ? 'ok' : it.unknown ? 'unknown' : 'bad';
                    return (
                      <div key={it.id} className={`caps-row ${state}`}>
                        <span className="caps-dot" />
                        <div className="caps-body">
                          <span className="caps-label">{it.label}</span>
                          {it.detail && <span className="caps-detail">{it.detail}</span>}
                          {!it.ok && it.reason && (
                            <span className={it.unknown ? 'caps-note' : 'caps-reason'}>{it.reason}</span>
                          )}
                          {state === 'bad' && it.hint && (
                            <span className="caps-hint">影响：{it.hint}</span>
                          )}
                        </div>
                        <span className="caps-source">{it.source === 'plugin' ? '插件' : '启动器'}</span>
                      </div>
                    );
                  })}
                </div>
                <p className="caps-foot">
                  这些是探测结论，不是按 DSH 版本号推断的 —— 上游改了内部实现时，
                  这里会直接变成红灯，而不是让功能静默失效。
                </p>
              </>
            )}
          </div>
        ) : tab === 'market' ? (
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

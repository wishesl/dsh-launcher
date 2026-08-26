import { useEffect, useRef, useState } from 'react';
import type { Instance, LogEvent } from '../types';
import LogPanel from './LogPanel';

interface Props {
  instances: Instance[];
  logs: Record<string, LogEvent[]>;
  activeLogId: string | null;
  onSelect: (id: string) => void;
  onClear: (id: string) => void;
}

export default function LogDrawer({ instances, logs, activeLogId, onSelect, onClear }: Props) {
  const [open, setOpen] = useState(false);
  const logRef = useRef<HTMLDivElement>(null);
  // Remember the last selection so auto-expand fires only when the user picks
  // a NEW instance — never when they just collapsed the drawer (which would
  // otherwise be immediately forced open again).
  const prevActive = useRef<string | null>(null);

  const activeInstance = instances.find((i) => i.id === activeLogId) ?? null;
  const activeLogs = activeLogId ? logs[activeLogId] ?? [] : [];
  const isLive = (inst: Instance) =>
    inst.status === 'running' || inst.status === 'starting' || inst.status === 'ready';

  // Selecting a different instance (from a card button or after launching)
  // expands the drawer; collapsing while the same one is selected stays closed.
  useEffect(() => {
    if (activeLogId && activeLogId !== prevActive.current) {
      setOpen(true);
    } else if (activeLogId === null) {
      setOpen(false); // deselected (card's "收起日志") → collapse the drawer
    }
    prevActive.current = activeLogId;
  }, [activeLogId]);

  // Scroll to bottom when opening or switching tabs.
  useEffect(() => {
    if (open) {
      setTimeout(() => logRef.current?.scrollTo({ top: logRef.current.scrollHeight }), 60);
    }
  }, [open, activeLogId]);

  const toggleOpen = () => setOpen((o) => !o);

  return (
    <div className={`log-drawer ${open ? '' : 'collapsed'}`}>
      <div className="log-drawer-bar" onClick={toggleOpen} role="button" title={open ? '收起日志' : '展开日志'}>
        <span className="log-drawer-title">运行日志</span>
        <span className="log-drawer-sub">
          {activeInstance ? `${activeInstance.name} · ${activeLogs.length} 行` : '选择一个实例查看日志'}
        </span>
        <span className="log-drawer-toggle">{open ? '▾ 收起' : '▴ 展开'}</span>
      </div>

      {open && (
        <>
          <div className="log-drawer-tabs">
            {instances.length === 0 && <span className="muted" style={{ padding: '4px 10px' }}>暂无实例</span>}
            {instances.map((inst) => (
              <button
                key={inst.id}
                className={`log-tab ${activeLogId === inst.id ? 'active' : ''}`}
                onClick={() => onSelect(inst.id)}
              >
                <span className={isLive(inst) ? 'live' : undefined}>●</span> {inst.name}
              </button>
            ))}
          </div>
          <div className="log-drawer-body">
            <LogPanel
              instance={activeInstance}
              logs={activeLogs}
              onClear={() => activeLogId && onClear(activeLogId)}
              logRef={logRef}
            />
          </div>
        </>
      )}
    </div>
  );
}

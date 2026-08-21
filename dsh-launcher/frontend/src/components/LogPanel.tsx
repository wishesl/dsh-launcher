import { useEffect, useMemo, useRef, type RefObject } from 'react';
import type { Instance, LogEvent } from '../types';

interface Props {
  instance: Instance | null;
  logs: LogEvent[];
  onClear: () => void;
  logRef: RefObject<HTMLDivElement>;
}

const STREAM_CLS: Record<string, string> = {
  stdout: 'log-stdout',
  stderr: 'log-stderr',
  system: 'log-system',
};

export default function LogPanel({ instance, logs, onClear, logRef }: Props) {
  const last = useRef<HTMLDivElement>(null);

  const body = useMemo(() => {
    if (!instance) {
      return <div className="log-empty">选择左侧一个实例，查看它的运行日志</div>;
    }
    if (logs.length === 0) {
      return <div className="log-empty">暂无日志。启动实例后，输出会实时显示在这里。</div>;
    }
    return logs.map((e, i) => (
      <div key={i} className={`log-line ${STREAM_CLS[e.stream] ?? ''}`}>
        <span className="log-time">{new Date(e.time).toLocaleTimeString()}</span>
        <span className="log-tag">{e.stream}</span>
        <span className="log-text">{e.line}</span>
      </div>
    ));
  }, [instance, logs]);

  useEffect(() => {
    if (last.current) last.current.scrollIntoView({ block: 'end' });
  }, [logs]);

  return (
    <section className="panel log-panel">
      <div className="panel-head">
        <h2>运行日志</h2>
        {instance ? (
          <span className="count-badge">{logs.length} 行</span>
        ) : null}
        <button className="btn btn-ghost btn-sm" onClick={onClear} disabled={!instance}>
          清空
        </button>
      </div>

      <div className="log-view" ref={logRef}>
        {body}
        <div ref={last} />
      </div>
    </section>
  );
}

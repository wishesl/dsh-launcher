import { useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import type { Instance, LogEvent } from '../types';

interface Props {
  instance: Instance | null;
  logs: LogEvent[];
  onClear: () => void;
  logRef: RefObject<HTMLDivElement>;
}

type StreamFilter = 'all' | 'stdout' | 'stderr' | 'system';

const STREAM_CLS: Record<string, string> = {
  stdout: 'log-stdout',
  stderr: 'log-stderr',
  system: 'log-system',
};

export default function LogPanel({ instance, logs, onClear, logRef }: Props) {
  const [streamFilter, setStreamFilter] = useState<StreamFilter>('all');
  const [search, setSearch] = useState('');
  const searchRef = useRef<HTMLInputElement>(null);
  // Smart autoscroll: follow new output only while the user is already at
  // (or near) the bottom — never yank them away while reading history.
  const atBottomRef = useRef(true);

  // Ctrl+F focuses the search box.
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === 'f') {
        ev.preventDefault();
        searchRef.current?.focus();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // Waiting hint (#10): process up but web not confirmed yet — first launches
  // may download dependencies for a couple of minutes.
  const waiting =
    !!instance &&
    (instance.status === 'starting' ||
      (instance.status === 'running' && !instance.webUrl));

  // Filter + search are purely local (client-side over the in-memory buffer).
  const filtered = useMemo(() => {
    if (streamFilter === 'all' && !search.trim()) return logs;
    const q = search.trim().toLowerCase();
    return logs.filter((e) => {
      if (streamFilter !== 'all' && e.stream !== streamFilter) return false;
      if (q && !e.line.toLowerCase().includes(q)) return false;
      return true;
    });
  }, [logs, streamFilter, search]);

  useEffect(() => {
    const el = logRef.current;
    if (el && atBottomRef.current) el.scrollTo({ top: el.scrollHeight });
  }, [filtered, logRef]);

  const onScroll = () => {
    const el = logRef.current;
    if (!el) return;
    atBottomRef.current = el.scrollTop + el.clientHeight >= el.scrollHeight - 24;
  };

  const body = useMemo(() => {
    if (!instance) {
      return <div className="log-empty">从下方标签页选择一个实例，查看它的运行日志</div>;
    }
    if (logs.length === 0) {
      return <div className="log-empty">暂无日志。启动实例后，输出会实时显示在这里。</div>;
    }
    if (filtered.length === 0) {
      return <div className="log-empty">没有符合当前过滤条件的日志。</div>;
    }
    return filtered.map((e, i) => (
      <div key={i} className={`log-line ${STREAM_CLS[e.stream] ?? ''}`}>
        <span className="log-time">{new Date(e.time).toLocaleTimeString()}</span>
        <span className="log-tag">{e.stream}</span>
        <span className="log-text">{e.line}</span>
      </div>
    ));
  }, [instance, logs, filtered]);

  return (
    <section className="panel log-panel">
      <div className="panel-head">
        <h2>运行日志</h2>
        {instance ? (
          <span className="count-badge">
            {filtered.length === logs.length ? logs.length : `${filtered.length}/${logs.length}`} 行
          </span>
        ) : null}
        <button className="btn btn-ghost btn-sm" onClick={onClear} disabled={!instance}>
          清空
        </button>
      </div>

      {instance && (
        <div className="log-toolbar">
          <select
            className="log-filter"
            value={streamFilter}
            onChange={(e) => setStreamFilter(e.target.value as StreamFilter)}
            title="按来源过滤"
          >
            <option value="all">全部来源</option>
            <option value="stdout">stdout</option>
            <option value="stderr">stderr</option>
            <option value="system">system</option>
          </select>
          <input
            ref={searchRef}
            className="log-search"
            type="text"
            placeholder="搜索日志…（Ctrl+F 聚焦）"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          {search && (
            <button className="btn btn-ghost btn-sm" onClick={() => setSearch('')}>
              ✕
            </button>
          )}
        </div>
      )}

      {waiting && (
        <div className="log-hint">
          正在准备环境，等待 DSH web 端口就绪… 首次启动需下载依赖，约 1-2 分钟。
        </div>
      )}
      {instance?.status === 'ready' && instance.webUrl && (
        <div className="log-hint log-hint-ok">web 已就绪：{instance.webUrl}</div>
      )}
      {instance?.status === 'crashed' && (
        <div className="log-hint log-hint-err">进程异常退出，请查看 stderr / system 日志定位原因。</div>
      )}

      <div className="log-view" ref={logRef} onScroll={onScroll}>
        {body}
      </div>
    </section>
  );
}

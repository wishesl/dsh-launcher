import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Instance, LogEvent, RegistryInfo, ServiceState } from '../types';
import InstanceCard from './InstanceCard';

interface Props {
  instances: Instance[];
  service: Record<string, ServiceState>;
  registry: RegistryInfo | null;
  registryLoading: boolean;
  busyId: string | null;
  activeLogId: string | null;
  logsOpen: boolean;
  logs: Record<string, LogEvent[]>;
  onAdd: () => void;
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
  /** 拖拽 / 键盘调整顺序后回调新的 id 顺序（由 App 落盘）。 */
  onReorder: (ids: string[]) => void;
}

/** 自动滚动触发区：指针进入列表上/下边缘这么多像素内就开始滚动。 */
const EDGE = 48;

export default function InstancesView({
  instances,
  service,
  registry,
  registryLoading,
  busyId,
  activeLogId,
  logsOpen,
  logs,
  onAdd,
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
  onReorder,
}: Props) {
  const running = instances.filter(
    (i) => i.status === 'running' || i.status === 'starting' || i.status === 'ready'
  ).length;
  const liveCount = Object.values(logs).reduce((n, arr) => n + arr.length, 0);

  const gridRef = useRef<HTMLDivElement | null>(null);
  // 拖动中的临时顺序（null = 直接用 props 顺序）。松手后立刻置回 null：
  // App 会在同一个事件里乐观更新 instances，所以不会闪回旧顺序。
  const [dragOrder, setDragOrder] = useState<string[] | null>(null);
  const [dragId, setDragId] = useState<string | null>(null);
  // 高频路径用 ref：pointermove 到 React 提交渲染之间有延迟，按 state 判定会丢事件
  // （和 Resizer 的 draggingRef 同一个坑）。
  const orderRef = useRef<string[] | null>(null);
  const dragIdRef = useRef<string | null>(null);
  const baseRef = useRef<string[]>([]);
  const pointerY = useRef<number | null>(null);

  const setOrder = useCallback((ids: string[] | null) => {
    orderRef.current = ids;
    setDragOrder(ids);
  }, []);

  // 渲染顺序：拖动期间按临时顺序，其余时候完全跟随 props。
  const ordered = useMemo(() => {
    if (!dragOrder) return instances;
    const byId = new Map(instances.map((i) => [i.id, i]));
    const list = dragOrder.map((id) => byId.get(id)).filter((i): i is Instance => !!i);
    return list.length === instances.length ? list : instances;
  }, [instances, dragOrder]);

  // 按指针 Y 找插入位置：只拿「非拖拽项」的矩形来算，避免拖拽项自己占位导致的
  // 目标索引差一（列表是实时重排的，DOM 顺序每帧都在变，所以每次都重新量）。
  const applyPointerTarget = useCallback(() => {
    const grid = gridRef.current;
    const id = dragIdRef.current;
    const y = pointerY.current;
    if (!grid || !id || y === null) return;
    const cards = Array.from(
      grid.querySelectorAll<HTMLElement>('.instance-card[data-inst-id]')
    ).filter((el) => el.dataset.instId !== id);
    let target = cards.length;
    for (let i = 0; i < cards.length; i++) {
      const r = cards[i].getBoundingClientRect();
      if (y < r.top + r.height / 2) {
        target = i;
        break;
      }
    }
    const others = (orderRef.current ?? []).filter((x) => x !== id);
    const next = [...others.slice(0, target), id, ...others.slice(target)];
    const cur = orderRef.current ?? [];
    if (next.length === cur.length && next.every((x, i) => x === cur[i])) return;
    setOrder(next);
  }, [setOrder]);

  // 拖动期间：window 级监听 + 边缘自动滚动的 rAF 循环。
  // 不用 pointer capture：列表实时重排会让 React 挪动 DOM 节点，节点一旦被重新插入
  // 就会丢 capture（lostpointercapture），拖到一半就断了。
  useEffect(() => {
    if (!dragId) return;
    let raf = 0;

    const onMove = (e: PointerEvent) => {
      pointerY.current = e.clientY;
      applyPointerTarget();
    };
    const scrollStep = () => {
      raf = requestAnimationFrame(scrollStep);
      const grid = gridRef.current;
      const y = pointerY.current;
      if (!grid || y === null) return;
      const r = grid.getBoundingClientRect();
      let dy = 0;
      if (y < r.top + EDGE) dy = -Math.ceil((r.top + EDGE - y) / 3);
      else if (y > r.bottom - EDGE) dy = Math.ceil((y - (r.bottom - EDGE)) / 3);
      if (dy === 0) return;
      const before = grid.scrollTop;
      grid.scrollTop += dy;
      // 滚动位置变了 → 卡片矩形也变了，重新算一次插入位置（鼠标没动也要跟）。
      if (grid.scrollTop !== before) applyPointerTarget();
    };
    raf = requestAnimationFrame(scrollStep);

    const finish = (commit: boolean) => {
      const ids = orderRef.current;
      const base = baseRef.current;
      dragIdRef.current = null;
      pointerY.current = null;
      setDragId(null);
      setOrder(null);
      if (commit && ids && (ids.length !== base.length || ids.some((x, i) => x !== base[i]))) {
        onReorder(ids);
      }
    };
    const onUp = () => finish(true);
    const onCancel = () => finish(false);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        finish(false);
      }
    };

    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    window.addEventListener('pointercancel', onCancel);
    window.addEventListener('keydown', onKey);
    document.body.classList.add('reordering');
    return () => {
      cancelAnimationFrame(raf);
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onCancel);
      window.removeEventListener('keydown', onKey);
      document.body.classList.remove('reordering');
    };
  }, [dragId, applyPointerTarget, onReorder, setOrder]);

  const startDrag = useCallback(
    (e: React.PointerEvent<HTMLElement>, id: string) => {
      if (e.button !== 0) return;
      e.preventDefault();
      const base = instances.map((i) => i.id);
      baseRef.current = base;
      dragIdRef.current = id;
      pointerY.current = e.clientY;
      setOrder(base);
      setDragId(id);
    },
    [instances, setOrder]
  );

  // 键盘等价操作：手柄聚焦后 ↑/↓ 换位（不需要进入拖动状态，直接落盘）。
  const moveByKey = useCallback(
    (e: React.KeyboardEvent<HTMLElement>, id: string) => {
      const dir = e.key === 'ArrowUp' ? -1 : e.key === 'ArrowDown' ? 1 : 0;
      if (dir === 0) return;
      e.preventDefault();
      const ids = instances.map((i) => i.id);
      const from = ids.indexOf(id);
      const to = from + dir;
      if (from < 0 || to < 0 || to >= ids.length) return;
      [ids[from], ids[to]] = [ids[to], ids[from]];
      onReorder(ids);
    },
    [instances, onReorder]
  );

  return (
    <div className="view-page">
      <div className="instances-toolbar">
        <h2>实例</h2>
        <div className="status-strip" title="运行中 / 实例总数">
          <span>运行 <b className="live">{running}</b> / <b>{instances.length}</b></span>
          <span className="muted">·</span>
          <span>日志 <b>{liveCount}</b> 行</span>
        </div>
        <button className="btn btn-primary" onClick={onAdd}>+ 添加实例</button>
      </div>

      <div className={`instance-grid ${dragId ? 'reordering' : ''}`} ref={gridRef}>
        {instances.length === 0 && (
          <div className="empty">
            <p>还没有任何实例</p>
            <p className="muted">点击「添加实例」，选择目录和 DSH 版本即可在不同目录启动不同版本。</p>
          </div>
        )}
        {ordered.map((inst) => (
          <InstanceCard
            key={inst.id}
            instance={inst}
            service={service[inst.id] ?? null}
            registry={registry}
            busy={busyId === inst.id}
            activeLog={activeLogId === inst.id && logsOpen}
            dragging={dragId === inst.id}
            onDragHandleDown={startDrag}
            onDragHandleKeyDown={moveByKey}
            onStart={onStart}
            onStop={onStop}
            onInstall={onInstall}
            onMask={onMask}
            onOpen={onOpen}
            onCopyUrl={onCopyUrl}
            onEdit={onEdit}
            onDelete={onDelete}
            onToggleLog={onSelectLog}
            onToggleAutoStart={onToggleAutoStart}
          />
        ))}
      </div>
    </div>
  );
}

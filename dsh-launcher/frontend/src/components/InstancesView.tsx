import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { GripVertical, ShieldCheck } from 'lucide-react';
import type { Instance, LogEvent, RegistryInfo, ServiceState } from '../types';
import InstanceCard, { statusRail } from './InstanceCard';

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
  /** 有兼容性问题的实例数（跑着的实例里判定，规则见 util.ts 的 capsAlert）。 */
  capsAlertCount: number;
  /** 打开右栏「兼容性」标签（有问题时直接落到第一台出问题的实例）。 */
  onOpenCompat: () => void;
}

/** 自动滚动触发区：指针进入列表上/下边缘这么多像素内就开始滚动。 */
const EDGE = 48;
/** 让位动画时长：与 style.css 里 .instance-card 的 FLIP transition 保持一致。 */
const SLIDE_MS = 200;
/** 幽灵卡片相对指针的水平偏移（指针左边一点，不挡着看落点）。 */
const GHOST_DX = 16;
/** 启动阈值：手柄按下后要先移动这么多像素才真正进入拖动态。
 *  否则"点一下手柄不拖"会让卡片塌成占位条再弹回来，闪一下。 */
const DRAG_THRESHOLD = 4;

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
  capsAlertCount,
  onOpenCompat,
}: Props) {
  const running = instances.filter(
    (i) => i.status === 'running' || i.status === 'starting' || i.status === 'ready'
  ).length;
  const liveCount = Object.values(logs).reduce((n, arr) => n + arr.length, 0);

  const gridRef = useRef<HTMLDivElement | null>(null);
  const ghostRef = useRef<HTMLDivElement | null>(null);
  // 拖动中的临时顺序（null = 直接用 props 顺序）。松手后立刻置回 null：
  // App 会在同一个事件里乐观更新 instances，所以不会闪回旧顺序。
  const [dragOrder, setDragOrder] = useState<string[] | null>(null);
  const [dragId, setDragId] = useState<string | null>(null);
  /** 手柄已按下（监听已挂），但还没越过阈值 —— 用来挂 window 监听。 */
  const [dragPending, setDragPending] = useState(false);
  // 刚落位的那张卡：给它一个轻微的落位动画，遮掉"3px 占位条突然展开成整卡"的生硬感。
  const [landedId, setLandedId] = useState<string | null>(null);
  // 高频路径用 ref：pointermove 到 React 提交渲染之间有延迟，按 state 判定会丢事件
  // （和 Resizer 的 draggingRef 同一个坑）。
  const orderRef = useRef<string[] | null>(null);
  const dragIdRef = useRef<string | null>(null);
  const baseRef = useRef<string[]>([]);
  const pointerY = useRef<number | null>(null);
  // 幽灵卡片的位置直接写 DOM（不走 state）：每次 pointermove 都 setState 会让整列表重渲染，
  // 跟手感立刻变差。
  const ghostAt = useRef({ x: 0, y: 0 });
  // 手柄按下但还没越过阈值：此时监听已经挂上，但列表还没进入拖动态。
  const pendingRef = useRef<{ id: string; x: number; y: number } | null>(null);
  const activeRef = useRef(false);

  const setOrder = useCallback((ids: string[] | null) => {
    orderRef.current = ids;
    setDragOrder(ids);
  }, []);

  // ---- FLIP：让"其余卡片让位"是滑过去，而不是瞬移 ----
  // 每次改动顺序前先记下每张卡的 offsetTop，渲染完再按差值反向位移 + transition 归零。
  // 用 offsetTop 而不是 getBoundingClientRect：它不含滚动偏移，自动滚动时不会算出假位移。
  const flipTops = useRef<Map<string, number>>(new Map());
  const snapshotTops = useCallback(() => {
    const grid = gridRef.current;
    if (!grid) return;
    const m = new Map<string, number>();
    grid.querySelectorAll<HTMLElement>('.instance-card[data-inst-id]').forEach((el) => {
      if (el.dataset.instId) m.set(el.dataset.instId, el.offsetTop);
    });
    flipTops.current = m;
  }, []);

  useLayoutEffect(() => {
    const grid = gridRef.current;
    const prev = flipTops.current;
    if (!grid || prev.size === 0) return;
    flipTops.current = new Map();
    if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return;
    grid.querySelectorAll<HTMLElement>('.instance-card[data-inst-id]').forEach((el) => {
      const id = el.dataset.instId as string;
      const before = prev.get(id);
      if (before === undefined) return; // 拖动中它是占位条，没有旧位置
      const dy = before - el.offsetTop;
      if (dy === 0) return;
      el.style.transition = 'none';
      el.style.transform = `translateY(${dy}px)`;
      void el.offsetHeight; // 强制回流：下面这行才会从位移态开始过渡
      el.style.transition = `transform ${SLIDE_MS}ms cubic-bezier(.2,.8,.2,1)`;
      el.style.transform = '';
      window.setTimeout(() => {
        el.style.transition = '';
      }, SLIDE_MS + 40);
    });
  // 依赖里带上 instances：键盘 ↑/↓ 换位走的是 props（不经过 dragOrder），
  // 只挂 dragOrder 的话键盘路径就没有让位动画。没有快照时这里直接返回，开销为零。
  }, [dragOrder, instances]);

  useEffect(() => {
    if (!landedId) return;
    const t = window.setTimeout(() => setLandedId(null), 280);
    return () => window.clearTimeout(t);
  }, [landedId]);

  // 渲染顺序：拖动期间按临时顺序，其余时候完全跟随 props。
  const ordered = useMemo(() => {
    if (!dragOrder) return instances;
    const byId = new Map(instances.map((i) => [i.id, i]));
    const list = dragOrder.map((id) => byId.get(id)).filter((i): i is Instance => !!i);
    return list.length === instances.length ? list : instances;
  }, [instances, dragOrder]);

  const ghostInst = dragId ? ordered.find((i) => i.id === dragId) ?? null : null;

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
    snapshotTops(); // 先量旧位置，再改顺序
    setOrder(next);
  }, [setOrder, snapshotTops]);

  const moveGhost = useCallback((x: number, y: number) => {
    const el = ghostRef.current;
    if (!el) return;
    el.style.transform = `translate3d(${x + GHOST_DX}px, ${y}px, 0) translateY(-50%)`;
  }, []);

  // 拖动期间：window 级监听 + 边缘自动滚动的 rAF 循环。
  // 不用 pointer capture：列表实时重排会让 React 挪动 DOM 节点，节点一旦被重新插入
  // 就会丢 capture（lostpointercapture），拖到一半就断了。
  // 监听在"手柄按下"时就挂上（dragPending），真正进入拖动态要等指针越过 DRAG_THRESHOLD。
  useEffect(() => {
    if (!dragPending) return;
    let raf = 0;

    // 越过阈值 → 正式进入拖动态：卡片塌成占位条、幽灵卡片出现。
    const activate = (x: number, y: number, id: string) => {
      activeRef.current = true;
      dragIdRef.current = id;
      pointerY.current = y;
      ghostAt.current = { x, y };
      snapshotTops();
      setOrder(baseRef.current);
      setDragId(id);
      // 真正拖起来才切整窗的抓取光标：点一下手柄不该改光标。
      document.body.classList.add('reordering');
    };

    const onMove = (e: PointerEvent) => {
      if (!activeRef.current) {
        const p = pendingRef.current;
        if (!p) return;
        if (Math.abs(e.clientX - p.x) < DRAG_THRESHOLD && Math.abs(e.clientY - p.y) < DRAG_THRESHOLD) return;
        activate(e.clientX, e.clientY, p.id);
        return;
      }
      pointerY.current = e.clientY;
      moveGhost(e.clientX, e.clientY);
      applyPointerTarget();
    };
    const scrollStep = () => {
      raf = requestAnimationFrame(scrollStep);
      if (!activeRef.current) return;
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
      const id = dragIdRef.current;
      const ids = orderRef.current;
      const base = baseRef.current;
      dragIdRef.current = null;
      pointerY.current = null;
      if (activeRef.current) {
        snapshotTops(); // 占位条要展开回整卡，其余卡片同样滑过去
        setDragId(null);
        setOrder(null);
        if (id) setLandedId(id);
        if (commit && ids && (ids.length !== base.length || ids.some((x, i) => x !== base[i]))) {
          onReorder(ids);
        }
      }
      pendingRef.current = null;
      activeRef.current = false;
      document.body.classList.remove('reordering');
      setDragPending(false);
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
    return () => {
      cancelAnimationFrame(raf);
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onCancel);
      window.removeEventListener('keydown', onKey);
      document.body.classList.remove('reordering');
    };
  }, [dragPending, applyPointerTarget, onReorder, setOrder, snapshotTops, moveGhost]);

  // 幽灵卡片挂上之后再定位（不能写在 pointerdown 里：那一帧它还没渲染出来）。
  useLayoutEffect(() => {
    if (dragId) moveGhost(ghostAt.current.x, ghostAt.current.y);
  }, [dragId, moveGhost]);

  const startDrag = useCallback((e: React.PointerEvent<HTMLElement>, id: string) => {
    if (e.button !== 0) return;
    e.preventDefault();
    baseRef.current = instances.map((i) => i.id);
    pendingRef.current = { id, x: e.clientX, y: e.clientY };
    activeRef.current = false;
    setDragPending(true);
  }, [instances]);

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
      snapshotTops(); // 键盘换位也走同一套滑动
      [ids[from], ids[to]] = [ids[to], ids[from]];
      onReorder(ids);
    },
    [instances, onReorder, snapshotTops]
  );

  return (
    <div className="view-page">
      <div className="instances-toolbar instances-toolbar-main">
        <h2>实例</h2>
        {/* 兼容性检查入口：探测结论在右栏「兼容性」标签里，这里给一个显式入口，
            有问题的实例数直接落在按钮上 —— 不用等用户想起来去翻标签行。 */}
        <button
          className={`btn btn-ghost btn-sm compat-check-btn ${capsAlertCount > 0 ? 'has-alert' : ''}`}
          onClick={onOpenCompat}
          title={
            capsAlertCount > 0
              ? `${capsAlertCount} 台运行中的实例有兼容性提示 —— 点开右栏「兼容性」看具体哪一项`
              : '探测这台 DSH 上各项能力到底能不能用（日志解析 / 插件装载 / 内嵌前置），结果在右栏「兼容性」'
          }
        >
          <ShieldCheck size={15} strokeWidth={1.75} aria-hidden />
          兼容性检查
          {capsAlertCount > 0 && <span className="compat-badge">{capsAlertCount}</span>}
        </button>
        <div className="status-strip" title="运行中 / 实例总数">
          <span>运行 <b className="live">{running}</b> / <b>{instances.length}</b></span>
          <span className="muted">·</span>
          <span>日志 <b>{liveCount}</b> 行</span>
        </div>
        <button className="btn btn-primary" onClick={onAdd}>+ 添加实例</button>
      </div>

      <div className="instance-grid" ref={gridRef}>
        {instances.length === 0 && (
          <div className="empty">
            <p>还没有任何实例</p>
            <p className="muted">点击「添加实例」，选择目录和 DSH 版本即可在不同目录启动不同版本。</p>
          </div>
        )}
        {ordered.map((inst) =>
          inst.id === dragId ? (
            // 大卡片离场，原地只留一枚细的落点占位；实际视觉由跟手的幽灵卡片承担。
            <div key={inst.id} className="inst-slot" aria-hidden />
          ) : (
            <InstanceCard
              key={inst.id}
              instance={inst}
              service={service[inst.id] ?? null}
              registry={registry}
              busy={busyId === inst.id}
              activeLog={activeLogId === inst.id && logsOpen}
              landed={landedId === inst.id}
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
              onSelectLog={onSelectLog}
              onToggleAutoStart={onToggleAutoStart}
            />
          )
        )}
      </div>

      {/* 跟随光标的"虚拟小卡片"：只承载名字 + 状态点，跟手且不给长列表做重排动画的负担。 */}
      {ghostInst && (
        <div
          className={`inst-ghost ${statusRail(ghostInst.status)}`}
          ref={ghostRef}
          aria-hidden="true"
        >
          <GripVertical size={13} strokeWidth={2} aria-hidden />
          <span className="inst-ghost-name">{ghostInst.name}</span>
          <span className="inst-ghost-dot" />
        </div>
      )}
    </div>
  );
}

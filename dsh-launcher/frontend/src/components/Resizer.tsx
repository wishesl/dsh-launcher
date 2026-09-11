import { useCallback, useEffect, useRef, useState } from 'react';
import { clamp } from '../util';

interface Props {
  /** 'left'  = 左栏（菜单）的右边界：向右拖变宽。
   *  'right' = 右栏（运行日志）的左边界：向左拖变宽（位移取反）。 */
  side: 'left' | 'right';
  /** 当前生效宽度（调用方已按窗口宽度 clamp 过）。 */
  width: number;
  min: number;
  max: number;
  /** 目标栏处于收起态时禁用（左栏 collapsed / 右栏 closed）。 */
  disabled?: boolean;
  /** 拖动中（高频）：只改布局宽度，不落盘。 */
  onDrag: (w: number) => void;
  /** 松手 / 键盘调整结束：把最终宽度落盘。 */
  onCommit: (w: number) => void;
  /** 双击复位到默认宽度。 */
  onReset: () => void;
  label: string;
}

/** 键盘微调步长（px）。 */
const KEY_STEP = 16;

// 三栏之间的可拖拽分隔条。位置与命中区宽度都由 CSS 变量 --sidebar-w /
// --drawer-w 算出来（见 style.css 的 .resizer），所以它自己不占 flex 空间、
// 也不参与三栏宽度分配。
export default function Resizer({
  side,
  width,
  min,
  max,
  disabled,
  onDrag,
  onCommit,
  onReset,
  label,
}: Props) {
  const [dragging, setDragging] = useState(false);
  const startX = useRef(0);
  const startW = useRef(0);
  // 拖拽闸门用 ref 而不是 state：pointerdown 到 React 提交渲染之间还有一段时间，
  // 期间到达的 pointermove 若按 state 判定就会被丢掉（快速甩动时表现为"拖不动"）。
  const draggingRef = useRef(false);
  // latest 跟随真正生效的宽度：调用方可能因窗口太窄而再次 clamp，
  // 落盘的必须是"实际生效值"，而不是手指最后落点。
  const latest = useRef(width);

  useEffect(() => {
    latest.current = width;
  }, [width]);

  // 拖拽期间禁止选中文字，并让整个窗口保持 col-resize 光标
  // （指针已 capture，但鼠标移出手柄时 cursor 仍会变回默认）。
  useEffect(() => {
    if (!dragging) return;
    document.body.classList.add('resizing');
    return () => document.body.classList.remove('resizing');
  }, [dragging]);

  const onPointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (disabled || e.button !== 0) return;
      e.preventDefault();
      // pointer capture：拖到窗口任何角落都还收得到事件，不必往 window 挂监听。
      e.currentTarget.setPointerCapture(e.pointerId);
      startX.current = e.clientX;
      startW.current = width;
      latest.current = width;
      draggingRef.current = true;
      setDragging(true);
    },
    [disabled, width]
  );

  const onPointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (!draggingRef.current) return;
      const dx = e.clientX - startX.current;
      const next = clamp(side === 'left' ? startW.current + dx : startW.current - dx, min, max);
      latest.current = next;
      onDrag(next);
    },
    [side, min, max, onDrag]
  );

  const finishDrag = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (!draggingRef.current) return;
      draggingRef.current = false;
      if (e.currentTarget.hasPointerCapture(e.pointerId)) {
        e.currentTarget.releasePointerCapture(e.pointerId);
      }
      setDragging(false);
      onCommit(latest.current);
    },
    [onCommit]
  );

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      if (disabled) return;
      const step = e.key === 'ArrowRight' ? KEY_STEP : e.key === 'ArrowLeft' ? -KEY_STEP : 0;
      if (step === 0) return;
      e.preventDefault();
      const next = clamp(width + (side === 'left' ? step : -step), min, max);
      latest.current = next;
      onDrag(next);
      onCommit(next);
    },
    [disabled, side, width, min, max, onDrag, onCommit]
  );

  return (
    <div
      className={`resizer resizer-${side} ${dragging ? 'dragging' : ''} ${disabled ? 'disabled' : ''}`}
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-hidden={disabled}
      aria-valuenow={Math.round(width)}
      aria-valuemin={min}
      aria-valuemax={max}
      tabIndex={disabled ? -1 : 0}
      title="拖动调整宽度（双击复位）"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={finishDrag}
      onPointerCancel={finishDrag}
      onDoubleClick={() => {
        if (!disabled) onReset();
      }}
      onKeyDown={onKeyDown}
    />
  );
}

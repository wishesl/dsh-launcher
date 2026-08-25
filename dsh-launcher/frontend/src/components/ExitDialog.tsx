import { useEffect, useState } from 'react';
import type { ExitChoice } from '../types';

interface Props {
  /** Dismiss without doing anything (backdrop click / Esc). */
  onClose: () => void;
  /** User picked an action; remember = "本次启动不再提示" was ticked. */
  onChoose: (action: ExitChoice, remember: boolean) => void;
}

// Exit chooser shown when the window close (✕) is pressed: minimize to tray
// (DSH instances keep running) or quit for real. A backdrop click / Esc
// dismisses the dialog WITHOUT any action.
export default function ExitDialog({ onClose, onChoose }: Props) {
  const [remember, setRemember] = useState(false);

  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        // blank-area click: close, do nothing
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal exit-modal" role="dialog" aria-modal="true">
        <div className="modal-head">
          <h2>关闭 DSH Launcher</h2>
        </div>
        <div className="form-body">
          <p className="exit-question">要最小化到系统托盘，还是直接退出？</p>
          <p className="field-hint">
            最小化后窗口收进托盘，DSH 实例继续在后台运行，可随时从托盘图标唤起。
          </p>
          <label className="field-check">
            <input
              type="checkbox"
              checked={remember}
              onChange={(e) => setRemember(e.target.checked)}
            />
            本次启动不再提示（记住选择，下次打开启动器时恢复询问）
          </label>
        </div>
        <div className="modal-foot">
          <button className="btn btn-ghost" onClick={() => onChoose('tray', remember)}>
            最小化到托盘
          </button>
          <button className="btn btn-danger" onClick={() => onChoose('quit', remember)}>
            直接退出
          </button>
        </div>
      </div>
    </div>
  );
}

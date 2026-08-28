import { useCallback, useEffect, useState } from 'react';
import {
  WindowIsMaximised,
  WindowMinimise,
  WindowToggleMaximise,
} from '../../wailsjs/runtime/runtime';

interface Props {
  onCloseRequest: () => void;
}

// macOS 风格交通灯窗口控制（红/黄/绿），自管理最大化状态。
export default function WinControls({ onCloseRequest }: Props) {
  const [maximized, setMaximized] = useState(false);

  const syncMaximized = useCallback(async () => {
    try {
      setMaximized(await WindowIsMaximised());
    } catch {
      /* 浏览器预览时无 runtime，忽略 */
    }
  }, []);

  useEffect(() => {
    syncMaximized();
    window.addEventListener('resize', syncMaximized);
    return () => window.removeEventListener('resize', syncMaximized);
  }, [syncMaximized]);

  const onMin = useCallback(() => {
    try {
      WindowMinimise();
    } catch {
      /* ignore */
    }
  }, []);

  const onMax = useCallback(() => {
    try {
      WindowToggleMaximise();
      window.setTimeout(() => syncMaximized(), 100);
    } catch {
      /* ignore */
    }
  }, [syncMaximized]);

  return (
    <div className="win-controls">
      <button className="win-btn win-min" onClick={onMin} title="最小化" aria-label="最小化">
        <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
          <path d="M0 5h10" stroke="currentColor" strokeWidth="1" />
        </svg>
      </button>
      <button
        className="win-btn win-max"
        onClick={onMax}
        title={maximized ? '还原' : '最大化'}
        aria-label={maximized ? '还原' : '最大化'}
      >
        {maximized ? (
          <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
            <rect x="0.5" y="2.5" width="7" height="7" fill="none" stroke="currentColor" />
            <path d="M2.5 2.5v-2h7v7h-2" fill="none" stroke="currentColor" />
          </svg>
        ) : (
          <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
            <rect x="0.5" y="0.5" width="9" height="9" fill="none" stroke="currentColor" />
          </svg>
        )}
      </button>
      <button className="win-btn win-close" onClick={onCloseRequest} title="关闭" aria-label="关闭">
        <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
          <path d="M0.5 0.5l9 9M9.5 0.5l-9 9" stroke="currentColor" strokeWidth="1" />
        </svg>
      </button>
    </div>
  );
}

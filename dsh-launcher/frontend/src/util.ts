import type { Instance } from './types';

export interface WebUrlInfo {
  url: string;      // http://127.0.0.1:<port>
  port: number;
  autoPort: boolean; // true when --port 0 (OS 自动选端口，无法预知地址)
}

// 从实例的 extraArgs 里解析 --port（支持 `--port 3081` 和 `--port=3081`）。
// 未指定时 DSH web 默认监听 3080。
export function getWebUrl(inst: Instance): WebUrlInfo {
  const extra = inst.extraArgs || '';
  const m = /--port(?:[=\s]+(\d+))?/i.exec(extra);
  const raw = m && m[1] !== undefined ? parseInt(m[1], 10) : 3080;
  const parsed = isNaN(raw) ? 3080 : raw;
  const autoPort = parsed === 0; // --port 0 → OS 自动分配
  const port = autoPort ? 0 : parsed;
  return { url: `http://127.0.0.1:${port || 3080}`, port, autoPort };
}

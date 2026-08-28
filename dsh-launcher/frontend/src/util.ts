import type { Instance } from './types';

export interface WebUrlInfo {
  url: string;
  port: number;
  autoPort: boolean; // true when --port 0 (OS 自动选端口，无法预知地址)
  runtime: boolean;  // true when the URL was captured from live process output
}

// 从实例的 extraArgs 里解析 --port（支持 `--port 3081` 和 `--port=3081`）。
// 未指定时 DSH web 默认监听 3080。
// 运行中实例优先使用后端从进程输出捕获的真实地址（runtime=true），
// 这样 --port 0 等动态端口场景也能拿到可点击的 URL。
export function getWebUrl(inst: Instance): WebUrlInfo {
  if (inst.webUrl) {
    const m = /:(\d{2,5})\/?$/.exec(inst.webUrl);
    const port = m ? parseInt(m[1], 10) : 0;
    return { url: inst.webUrl, port, autoPort: false, runtime: true };
  }
  const extra = inst.extraArgs || '';
  const m = /--port(?:[=\s]+(\d+))?/i.exec(extra);
  const raw = m && m[1] !== undefined ? parseInt(m[1], 10) : 3080;
  const parsed = isNaN(raw) ? 3080 : raw;
  const autoPort = parsed === 0; // --port 0 → OS 自动分配
  const port = autoPort ? 0 : parsed;
  return { url: `http://127.0.0.1:${port || 3080}`, port, autoPort, runtime: false };
}

// semver 与后端 parseVersion 保持一致：x.y.z[-prerelease]
const SEMVER_RE = /^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/;

export function isValidVersion(v: string): boolean {
  return SEMVER_RE.test(v.trim());
}

// Extract `owner/repo` (lowercased) from a GitHub URL, or null.
export function githubRepoOf(url: string): string | null {
  const m = /^https:\/\/github\.com\/([A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+?)(?:[/?#].*)?$/.exec(url);
  return m ? m[1].toLowerCase() : null;
}

// Extract `owner/repo` (lowercased) from a `github:owner/repo[#path:/…]` pnpm
// spec, or null.
export function specRepoOf(spec: string): string | null {
  const m = /^github:([A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+?)(?:#path:.*)?$/.exec(spec.trim());
  return m ? m[1].toLowerCase() : null;
}

// Build the canonical GitHub URL from a `github:` spec ("" when not a github
// source) — lets an installed-source favorite record its GitHub address even
// when the package.json has no homepage.
export function githubURLFromSpec(spec: string): string {
  const repo = specRepoOf(spec);
  return repo ? `https://github.com/${repo}` : '';
}

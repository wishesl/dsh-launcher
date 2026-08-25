import { useState } from 'react';
import type { RegistryInfo } from '../types';

interface Props {
  registry: RegistryInfo | null;
  loading: boolean;
}

function fmtDate(s: string): string {
  if (!s) return '—';
  const d = new Date(s);
  if (isNaN(d.getTime())) return s;
  return d.toLocaleString();
}

export default function VersionPanel({ registry, loading }: Props) {
  const [showAll, setShowAll] = useState(false);
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);

  const npxCmd = registry
    ? `npx -y @deepseek-ai/dsh@${registry.latest} web`
    : 'npx -y @deepseek-ai/dsh@latest web';

  const copyCmd = async () => {
    try {
      await navigator.clipboard.writeText(npxCmd);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable in some WebView2 contexts — tell the user */
      setCopyFailed(true);
      setTimeout(() => setCopyFailed(false), 1500);
    }
  };

  const versions = registry?.versions ?? [];
  const visible = showAll ? versions : versions.slice(0, 8);

  return (
    <section className="panel">
      <div className="panel-head">
        <h2>版本信息</h2>
        {loading && <span className="spin" />}
        <a
          className="link-btn"
          href="https://www.npmjs.com/package/@deepseek-ai/dsh"
          target="_blank"
          rel="noreferrer"
        >
          npm 页面 ↗
        </a>
      </div>

      <div className="ver-hero">
        <div className="ver-main">
          <span className="ver-label">npm latest</span>
          <span className="ver-big">{registry ? registry.latest : '—'}</span>
        </div>
        <div className="ver-side">
          <div>
            <span className="ver-label">next</span>
            <span className="ver-big small">{registry?.next || '—'}</span>
          </div>
          <div>
            <span className="ver-label">来源</span>
            <span className="ver-big small">{registry?.source || '—'}</span>
          </div>
        </div>
      </div>

      <div className="cmd-box">
        <code className="mono">{npxCmd}</code>
        <button className="btn btn-ghost btn-sm" onClick={copyCmd}>
          {copied ? '已复制 ✓' : copyFailed ? '复制失败 ✕' : '复制命令'}
        </button>
      </div>

      <div className="ver-table">
        <div className="ver-table-head">
          <span>版本</span>
          <span>发布时间</span>
          <span>标记</span>
        </div>
        {versions.length === 0 && (
          <div className="muted pad">暂无版本数据（点击「刷新版本」获取）</div>
        )}
        {visible.map((v) => (
          <div key={v.version} className={`ver-row ${v.isLatest ? 'latest' : ''}`}>
            <span className="mono">{v.version}</span>
            <span>{fmtDate(v.published)}</span>
            <span>
              {v.isLatest && <span className="tag-latest">latest</span>}
              {v.version === registry?.next && <span className="tag-next">next</span>}
            </span>
          </div>
        ))}
        {versions.length > 8 && (
          <button className="link-btn pad" onClick={() => setShowAll(!showAll)}>
            {showAll ? '收起' : `查看全部 ${versions.length} 个版本`}
          </button>
        )}
      </div>

      <p className="panel-note">
        提示：npx 会优先使用启动目录内 node_modules 的本地副本。若目录内版本较旧，即使 npm 有新版本也会运行旧版 — 这也是本启动器在表单中检测「本地副本」的原因。
      </p>
    </section>
  );
}

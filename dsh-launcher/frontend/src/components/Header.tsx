import type { RegistryInfo } from '../types';
import logo from '../assets/images/logo-universal.png';

interface Props {
  registry: RegistryInfo | null;
  registryLoading: boolean;
  onRefreshRegistry: () => void;
  onHideToTray: () => void;
}

export default function Header({ registry, registryLoading, onRefreshRegistry, onHideToTray }: Props) {
  return (
    <header className="app-header">
      <div className="brand">
        <img className="brand-logo-img" src={logo} alt="DSH Launcher" draggable={false} />
        <div className="brand-text">
          <h1>DSH Launcher</h1>
          <p className="brand-sub">DeepSeek Harness 启动器</p>
        </div>
      </div>

      <div className="header-right">
        <div className="latest-chip" title={`npm 最新版本（来源: ${registry?.source ?? '-'}）`}>
          <span className={`dot ${registry ? 'dot-live' : ''}`} />
          {registryLoading ? (
            <span className="chip-label">查询中…</span>
          ) : registry ? (
            <>
              <span className="chip-label">npm latest</span>
              <span className="chip-version">{registry.latest}</span>
              {registry.next && registry.next !== registry.latest && (
                <>
                  <span className="chip-label">next</span>
                  <span className="chip-version chip-muted">{registry.next}</span>
                </>
              )}
            </>
          ) : (
            <span className="chip-label">无法获取版本</span>
          )}
        </div>
        <button className="btn btn-ghost btn-sm" onClick={onRefreshRegistry} disabled={registryLoading} title="刷新最新版本">
          {registryLoading ? '刷新中…' : '刷新版本'}
        </button>
        <button className="btn btn-ghost btn-sm" onClick={onHideToTray} title="隐藏到系统托盘，进程继续运行">
          最小化到托盘
        </button>
      </div>
    </header>
  );
}

import type { RegistryInfo } from '../types';

interface Props {
  registry: RegistryInfo | null;
  registryLoading: boolean;
  appDataPath: string;
  onRefreshRegistry: () => void;
  onOpenSettings: () => void;
  onHideToTray: () => void;
}

export default function Header({
  registry,
  registryLoading,
  appDataPath,
  onRefreshRegistry,
  onOpenSettings,
  onHideToTray,
}: Props) {
  return (
    <header className="app-header">
      <div className="brand">
        <span className="brand-logo">DSH</span>
        <div className="brand-text">
          <h1>DSH Launcher</h1>
          <p className="brand-sub">DeepSeek Harness 版本启动器</p>
        </div>
      </div>

      <div className="header-right">
        <div className="latest-chip" title={`npm 最新版本（来源: ${registry?.source ?? '-'}）`}>
          <span className={`dot ${registry ? 'dot-live' : ''}`} />
          {registryLoading ? (
            <span>查询中…</span>
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
            <span>无法获取版本</span>
          )}
        </div>
        <button className="btn btn-ghost" onClick={onRefreshRegistry} disabled={registryLoading} title="刷新最新版本">
          {registryLoading ? '刷新中…' : '刷新版本'}
        </button>

        <button className="btn btn-ghost" onClick={onHideToTray} title="隐藏到系统托盘，进程继续运行">
          最小化到托盘
        </button>
        <button className="btn btn-ghost" onClick={onOpenSettings} title="前置环境检查 / 安装 pnpm">
          ⚙ 设置
        </button>

        <span className="appdata" title="配置文件位置">{appDataPath}</span>
      </div>
    </header>
  );
}

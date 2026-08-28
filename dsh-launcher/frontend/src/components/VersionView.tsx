import VersionPanel from './VersionPanel';
import type { RegistryInfo } from '../types';

interface Props {
  registry: RegistryInfo | null;
  registryLoading: boolean;
  onRefreshRegistry: () => void;
}

// 版本历史页：独立菜单页（首页默认打开），完整版本列表 + 手动刷新。
export default function VersionView({ registry, registryLoading, onRefreshRegistry }: Props) {
  return (
    <div className="view-page">
      <div className="instances-toolbar">
        <h2>版本历史</h2>
        <button
          className="btn btn-ghost btn-sm"
          onClick={onRefreshRegistry}
          disabled={registryLoading}
          title="从 npm 重新拉取最新版本列表"
        >
          {registryLoading ? '刷新中…' : '刷新版本'}
        </button>
      </div>
      <div className="version-page-body">
        <VersionPanel registry={registry} loading={registryLoading} />
      </div>
    </div>
  );
}

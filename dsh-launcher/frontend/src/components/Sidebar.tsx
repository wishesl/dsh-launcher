export type ViewKey = 'versions' | 'instances' | 'market' | 'settings';

interface Props {
  view: ViewKey;
  onNavigate: (v: ViewKey) => void;
}

const NAV: { key: ViewKey; label: string; icon: string }[] = [
  { key: 'versions', label: '版本历史', icon: '🕘' },
  { key: 'instances', label: '实例', icon: '🖥' },
  { key: 'market', label: '插件市场', icon: '🛒' },
  { key: 'settings', label: '设置', icon: '⚙' },
];

export default function Sidebar({ view, onNavigate }: Props) {
  return (
    <nav className="sidebar" aria-label="主导航">
      {NAV.map((n) => (
        <div
          key={n.key}
          className={`side-nav ${view === n.key ? 'active' : ''}`}
          role="tab"
          aria-selected={view === n.key}
          tabIndex={0}
          onClick={() => onNavigate(n.key)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault();
              onNavigate(n.key);
            }
          }}
        >
          <span className="nav-ico">{n.icon}</span>
          <span>{n.label}</span>
        </div>
      ))}
      <div className="sidebar-foot">DSH Launcher · 本地管理工具</div>
    </nav>
  );
}

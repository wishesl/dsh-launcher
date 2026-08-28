import { History, Server, Store, Settings } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';

export type ViewKey = 'versions' | 'instances' | 'market' | 'settings';

interface Props {
  view: ViewKey;
  onNavigate: (v: ViewKey) => void;
}

const NAV: { key: ViewKey; label: string; icon: LucideIcon }[] = [
  { key: 'versions', label: '版本历史', icon: History },
  { key: 'instances', label: '实例', icon: Server },
  { key: 'market', label: '插件市场', icon: Store },
  { key: 'settings', label: '设置', icon: Settings },
];

export default function Sidebar({ view, onNavigate }: Props) {
  return (
    <nav className="sidebar" aria-label="主导航">
      {NAV.map((n) => {
        const Icon = n.icon;
        return (
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
            <Icon className="nav-ico" size={18} strokeWidth={1.75} aria-hidden />
            <span>{n.label}</span>
          </div>
        );
      })}
      <div className="sidebar-foot">DSH Launcher · 本地管理工具</div>
    </nav>
  );
}

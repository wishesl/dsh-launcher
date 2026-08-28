import { History, Server, Store, Settings } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import dshLogo from '../assets/dsh.svg';

export type ViewKey = 'versions' | 'instances' | 'market' | 'settings';

interface Props {
  view: ViewKey;
  onNavigate: (v: ViewKey) => void;
  collapsed: boolean;
}

const NAV: { key: ViewKey; label: string; icon: LucideIcon }[] = [
  { key: 'versions', label: '版本历史', icon: History },
  { key: 'instances', label: '实例', icon: Server },
  { key: 'market', label: '插件市场', icon: Store },
  { key: 'settings', label: '设置', icon: Settings },
];

export default function Sidebar({ view, onNavigate, collapsed }: Props) {
  return (
    <nav className={`sidebar ${collapsed ? 'collapsed' : ''}`} aria-label="主导航">
      <div className="side-head">
        <div className="brand">
          <img className="brand-logo-img" src={dshLogo} alt="DSH Launcher" draggable={false} />
          <div className="brand-text">
            <h1>DSH Launcher</h1>
            <p className="brand-sub">DeepSeek Harness 启动器</p>
          </div>
        </div>
      </div>

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
            <span className="nav-label">{n.label}</span>
          </div>
        );
      })}
      <div className="sidebar-foot">DSH Launcher · 本地管理工具</div>
    </nav>
  );
}

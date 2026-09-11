import { History, Server, Store, Settings } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import dshLogo from '../assets/dsh.svg';

export type ViewKey = 'versions' | 'instances' | 'market' | 'settings';

interface Props {
  view: ViewKey;
  onNavigate: (v: ViewKey) => void;
  collapsed: boolean;
  /** 展开态的宽度（可拖拽）。收起态由 .sidebar.collapsed 的 64px 决定，
   *  此时这里传 undefined —— 否则内联宽度会盖掉收起态的 CSS。 */
  width?: number;
}

const NAV: { key: ViewKey; label: string; icon: LucideIcon }[] = [
  { key: 'versions', label: '版本历史', icon: History },
  { key: 'instances', label: '实例', icon: Server },
  { key: 'market', label: '插件市场', icon: Store },
  { key: 'settings', label: '设置', icon: Settings },
];

export default function Sidebar({ view, onNavigate, collapsed, width }: Props) {
  return (
    <nav
      className={`sidebar ${collapsed ? 'collapsed' : ''}`}
      style={width === undefined ? undefined : { width }}
      aria-label="主导航"
    >
      <div className="side-head">
        {/* 与顶栏品牌区同一套皮：裸 mark + 单行字标（副标题降级为 title 提示）。 */}
        <div className="brand" title="DeepSeek Harness 启动器">
          <img className="brand-logo-img" src={dshLogo} alt="DSH Launcher" draggable={false} />
          <h1 className="brand-name">DSH Launcher</h1>
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

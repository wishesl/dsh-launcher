import { History, Server, Store, Settings, Plus } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import dshLogo from '../assets/dsh.svg';
import type { Instance } from '../types';

export type ViewKey = 'versions' | 'instances' | 'market' | 'settings';

interface Props {
  view: ViewKey;
  onNavigate: (v: ViewKey) => void;
  collapsed: boolean;
  /** 展开态的宽度（可拖拽）。收起态由 .sidebar.collapsed 的 64px 决定，
   *  此时这里传 undefined —— 否则内联宽度会盖掉收起态的 CSS。 */
  width?: number;
  /** 所有实例（用于「运行状态」概览卡）。收起态不渲染这块。 */
  instances?: Instance[];
  /** 新建实例按钮（左栏顶部主操作）。 */
  onAddInstance?: () => void;
}

const NAV: { key: ViewKey; label: string; icon: LucideIcon }[] = [
  { key: 'versions', label: '版本历史', icon: History },
  { key: 'instances', label: '实例', icon: Server },
  { key: 'market', label: '插件市场', icon: Store },
  { key: 'settings', label: '设置', icon: Settings },
];

const RUNNING_SET = new Set(['running', 'starting', 'ready']);

export default function Sidebar({ view, onNavigate, collapsed, width, instances = [], onAddInstance }: Props) {
  // 运行中（含启动中/就绪）的实例：给左栏一块概览卡，把"现在有几台在跑"
  // 从主内容区的胶囊里解放出来，免得左栏下半部分空着。
  const running = instances.filter((i) => RUNNING_SET.has(i.status));
  const runningLabel =
    running.length === 0
      ? '当前没有运行中的实例'
      : running.length === 1
        ? '1 台实例运行中'
        : `${running.length} 台实例运行中`;

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

      {/* 主操作按钮：在导航项最上面，给左栏顶部一个视觉重心。
          收起态只渲染一个 + 号按钮。 */}
      {onAddInstance && (
        collapsed ? (
          <button
            type="button"
            className="side-add-collapsed"
            onClick={onAddInstance}
            title="新建实例"
            aria-label="新建实例"
          >
            <Plus size={18} strokeWidth={2} aria-hidden />
          </button>
        ) : (
          <button
            type="button"
            className="side-add-btn"
            onClick={onAddInstance}
          >
            <Plus size={16} strokeWidth={2.25} aria-hidden />
            新建实例
          </button>
        )
      )}

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

      {/* 状态概览卡：用 margin-top:auto 推到最底部，紧贴 footer。
          导航项在顶部，状态卡在底部，中间自然留白。收起态只渲染一个小圆点。 */}
      {!collapsed ? (
        <div className="side-status">
          <div className="side-status-head">
            <span className={`side-status-dot ${running.length > 0 ? 'on' : ''}`} />
            <span className="side-status-title">{runningLabel}</span>
          </div>
          {running.length > 0 && (
            <ul className="side-status-list">
              {running.slice(0, 5).map((i) => (
                <li key={i.id}>
                  <button
                    type="button"
                    className="side-status-item"
                    title={i.directory}
                    onClick={() => onNavigate('instances')}
                  >
                    <span className="side-status-item-dot" data-status={i.status} />
                    <span className="side-status-item-name">{i.name}</span>
                  </button>
                </li>
              ))}
              {running.length > 5 && (
                <li className="side-status-more" onClick={() => onNavigate('instances')}>
                  还有 {running.length - 5} 台…
                </li>
              )}
            </ul>
          )}
        </div>
      ) : (
        <div
          className="side-status-collapsed"
          title={runningLabel}
          onClick={() => onNavigate('instances')}
        >
          <span className={`side-status-dot ${running.length > 0 ? 'on' : ''}`} />
        </div>
      )}

      <div className="sidebar-foot">DSH Launcher · 本地管理工具</div>
    </nav>
  );
}

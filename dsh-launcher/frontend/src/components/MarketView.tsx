import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { api, errMsg } from '../api';
import type {
  FavoriteDraft,
  FavoritePlugin,
  Instance,
  InstalledPlugin,
  MarketCatalog,
  MarketOpState,
  MarketPlugin,
} from '../types';
import { ChevronDown } from 'lucide-react';
import Switch from './Switch';
import ShareCodeDialog from './ShareCodeDialog';
import './market.css';

interface Props {
  instances: Instance[];
  showToast: (msg: string, kind?: 'ok' | 'error') => void;
  // Market operation stream is hoisted to App so the right-side run-log drawer
  // can render the same progress (auto-popped on install/uninstall).
  marketOp: MarketOpState;
  onClearMarketLogs: () => void;
  onCancelMarket: () => void;
  onShowMarketLogs: () => void;              // open the drawer on the 市场任务 tab
  onMarketRunning: (running: boolean) => void; // sync from marketOpRunning() at boot
}

const PAGE_SIZE = 60;
const LANG = navigator.language?.toLowerCase().startsWith('zh') ? 'zh' : 'en';

type SortKey = 'downloads-desc' | 'stars-desc' | 'added-desc' | 'name-asc';
type MarketTab = 'discover' | 'installed' | 'favorites';

// Port of dsh-market's visiblePlugins(): category → query → sort, matching
// name / owner / localized description, case-insensitive.
function filterPlugins(plugins: MarketPlugin[], category: string, query: string, sort: SortKey): MarketPlugin[] {
  const q = query.trim().toLowerCase();
  const list = plugins.filter((p) => {
    if (p.name === 'dsh-market' || p.npm === 'dshmarket') return false;
    if (category !== 'all' && p.category !== category) return false;
    if (!q) return true;
    const desc = (p.description && (p.description[LANG] || p.description.en)) || '';
    return (
      p.name.toLowerCase().includes(q) ||
      p.owner.toLowerCase().includes(q) ||
      desc.toLowerCase().includes(q)
    );
  });
  const hasDownloads = (p: MarketPlugin): p is MarketPlugin & { downloads: number } =>
    typeof p.downloads === 'number';
  const arr = [...list];
  switch (sort) {
    case 'stars-desc':
      return arr.sort((a, b) => (b.stars ?? -1) - (a.stars ?? -1));
    case 'added-desc':
      return arr.sort((a, b) => String(b.added).localeCompare(String(a.added)));
    case 'name-asc':
      return arr.sort((a, b) => a.name.localeCompare(b.name));
    case 'downloads-desc':
    default:
      return arr.sort((a, b) => {
        if (hasDownloads(a) && hasDownloads(b)) return b.downloads - a.downloads;
        if (hasDownloads(a)) return -1;
        if (hasDownloads(b)) return 1;
        return (b.stars ?? -1) - (a.stars ?? -1);
      });
  }
}

function fmtCount(n: number | null | undefined): string {
  if (typeof n !== 'number') return '—';
  if (n >= 10000) return (n / 10000).toFixed(1).replace(/\.0$/, '') + 'w';
  if (n >= 1000) return (n / 1000).toFixed(1).replace(/\.0$/, '') + 'k';
  return String(n);
}

function fmtDate(s: string): string {
  if (!s) return '';
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) return s;
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

// PluginDesc: clamps the description to 2 lines; when it actually overflows,
// shows a downward chevron that expands/collapses the full text.
function PluginDesc({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false);
  const [clamped, setClamped] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  const measure = useCallback(() => {
    const el = ref.current;
    if (!el) return;
    setClamped(el.scrollHeight - el.clientHeight > 1);
  }, []);

  useLayoutEffect(() => {
    measure();
  }, [measure, text, expanded]);

  useEffect(() => {
    window.addEventListener('resize', measure);
    return () => window.removeEventListener('resize', measure);
  }, [measure]);

  const showToggle = clamped || expanded;

  return (
    <>
      <div ref={ref} className={`plugin-desc ${expanded ? 'expanded' : 'clamped'}`}>
        {text}
      </div>
      {showToggle && (
        <button
          type="button"
          className="desc-toggle"
          onClick={() => setExpanded((v) => !v)}
          aria-expanded={expanded}
          title={expanded ? '收起描述' : '展开完整描述'}
        >
          <ChevronDown size={14} strokeWidth={1.75} className={expanded ? 'rot' : ''} aria-hidden />
        </button>
      )}
    </>
  );
}

// githubRepoOf extracts `owner/repo` from a catalog entry URL.
function githubRepoOf(url: string): string | null {
  const m = /^https:\/\/github\.com\/([A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+?)(?:[/?#].*)?$/.exec(url);
  return m ? m[1].toLowerCase() : null;
}

// findCatalogEntry matches an installed package back to its catalog entry by
// npm name, catalog display name, or GitHub repo — so the Installed tab can
// show the recognizable name / category / owner / description.
function findCatalogEntry(installed: InstalledPlugin, catalog: MarketCatalog | null): MarketPlugin | undefined {
  if (!catalog) return undefined;
  const name = installed.name.toLowerCase();
  const specRepo = /^github:([A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+)/.exec(installed.spec)?.[1]?.toLowerCase();
  return catalog.plugins.find((p) => {
    if (p.npm && p.npm.toLowerCase() === name) return true;
    if (p.name.toLowerCase() === name) return true;
    if (specRepo) {
      const cr = githubRepoOf(p.url);
      if (cr === specRepo) return true;
    }
    return false;
  });
}

export default function MarketView({
  instances,
  showToast,
  marketOp,
  onClearMarketLogs,
  onCancelMarket,
  onShowMarketLogs,
  onMarketRunning,
}: Props) {
  const [tab, setTab] = useState<MarketTab>('discover');
  const [catalog, setCatalog] = useState<MarketCatalog | null>(null);
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [catalogError, setCatalogError] = useState('');
  const [query, setQuery] = useState('');
  const [category, setCategory] = useState('all');
  const [sort, setSort] = useState<SortKey>('downloads-desc');
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE);
  const [installed, setInstalled] = useState<InstalledPlugin[]>([]);
  const [favorites, setFavorites] = useState<FavoritePlugin[]>([]);
  const [targetId, setTargetId] = useState('');
  const [pendingApprove, setPendingApprove] = useState<{ names: string[]; retry: () => void } | null>(null);
  // Share-code flows open as a modal (pick subset to share / preview before import).
  const [shareDialog, setShareDialog] = useState<'gen' | 'import' | null>(null);
  const wasRunningRef = useRef(false);
  // Local busy flag: disables install/uninstall the instant it's clicked, so
  // there is immediate feedback even before the backend's "running" status
  // event arrives (it first revalidates the catalog, which can take seconds).
  const [busy, setBusy] = useState(false);

  const loadCatalog = useCallback(async (force: boolean) => {
    setCatalogLoading(true);
    setCatalogError('');
    try {
      setCatalog(await api.fetchMarketCatalog(force));
    } catch (e) {
      setCatalogError(errMsg(e));
    } finally {
      setCatalogLoading(false);
    }
  }, []);

  const loadInstalled = useCallback(async () => {
    try {
      setInstalled(await api.listInstalledPlugins());
    } catch (e) {
      showToast('读取已装插件失败: ' + errMsg(e), 'error');
    }
  }, [showToast]);

  const loadFavorites = useCallback(async () => {
    try {
      setFavorites((await api.listFavorites()) ?? []);
    } catch (e) {
      showToast('读取收藏失败: ' + errMsg(e), 'error');
    }
  }, [showToast]);

  useEffect(() => {
    loadCatalog(false);
    loadInstalled();
    loadFavorites();
    api.marketOpRunning().then((running) => onMarketRunning(running)).catch(() => undefined);
  }, [loadCatalog, loadInstalled, loadFavorites, onMarketRunning]);

  // When an operation settles (running → done/failed/cancelled), refresh the
  // installed list. marketOp is driven by dsh:market-status (subscribed in App).
  useEffect(() => {
    if (wasRunningRef.current && !marketOp.running) {
      loadInstalled();
    }
    wasRunningRef.current = marketOp.running;
  }, [marketOp.running, loadInstalled]);

  // Default target instance = first.
  useEffect(() => {
    if (!targetId && instances.length > 0) setTargetId(instances[0].id);
  }, [instances, targetId]);

  // Reset pagination when filters change.
  useEffect(() => {
    setVisibleCount(PAGE_SIZE);
  }, [query, category, sort]);

  const filtered = useMemo(
    () => (catalog ? filterPlugins(catalog.plugins, category, query, sort) : []),
    [catalog, category, query, sort]
  );
  const visible = filtered.slice(0, visibleCount);
  const hasMore = filtered.length > visibleCount;
  const targetInstance = instances.find((i) => i.id === targetId) ?? null;
  const installedNames = useMemo(() => new Set(installed.map((i) => i.name)), [installed]);
  const installedNamesLower = useMemo(() => new Set(installed.map((i) => i.name.toLowerCase())), [installed]);
  const favIDSet = useMemo(() => new Set(favorites.map((f) => f.id.toLowerCase())), [favorites]);

  // Stop the target instance (returns whether it was running) so plugin files
  // are never replaced under a live process; the caller relaunches afterwards.
  const stopIfRunning = async (): Promise<boolean> => {
    if (!targetInstance) return false;
    if (!['running', 'ready', 'starting'].includes(targetInstance.status)) return false;
    const ok = window.confirm(
      `实例「${targetInstance.name}」正在运行。插件变更需重启才生效，是否先停止该实例，操作完成后再启动？`
    );
    if (!ok) return false;
    try {
      await api.stopInstance(targetId);
      return true;
    } catch (e) {
      showToast('停止实例失败: ' + errMsg(e), 'error');
      return false;
    }
  };

  const install = async (entry: MarketPlugin) => {
    if (!targetInstance) {
      showToast('请先在主界面添加实例，再选择安装目标', 'error');
      return;
    }
    const wasRunning = await stopIfRunning();
    if (targetInstance.status !== 'stopped' && targetInstance.status !== 'crashed' && !wasRunning) return;
    onClearMarketLogs();
    setPendingApprove(null);
    // Auto-pop the right-side run-log drawer on the 市场任务 tab to show progress.
    onShowMarketLogs();
    setBusy(true);
    try {
      const r = await api.installPlugin(targetId, entry.url);
      if (r.already) {
        showToast(r.error || '该插件已安装');
      } else if (r.ok) {
        const names = r.installed.length ? r.installed.join(', ') : entry.name;
        showToast(`已安装 ${names}，重启实例后生效`);
        if (wasRunning) {
          showToast('正在重新启动实例…');
          await api.launchInstance(targetId);
        }
      } else if (r.cancelled) {
        showToast('已取消安装', 'error');
      } else if (r.blockedBuilds && r.blockedBuilds.length > 0) {
        setPendingApprove({ names: r.blockedBuilds, retry: () => install(entry) });
        showToast(`构建脚本被拦截（${r.blockedBuilds.join(', ')}），请放行后重试`, 'error');
      } else {
        showToast(r.error || '安装失败，详见输出', 'error');
      }
    } catch (e) {
      showToast('安装失败: ' + errMsg(e), 'error');
    } finally {
      setBusy(false);
      loadInstalled();
    }
  };

  // Install straight from the favorites tab — reuses the same marketOp flow
  // (right-drawer auto-pop, single-flight, toast) but via InstallFavorite so
  // it works offline / when the catalog entry vanished.
  const installFavorite = async (f: FavoritePlugin) => {
    if (!targetInstance) {
      showToast('请先在主界面添加实例，再选择安装目标', 'error');
      return;
    }
    const wasRunning = await stopIfRunning();
    if (targetInstance.status !== 'stopped' && targetInstance.status !== 'crashed' && !wasRunning) return;
    onClearMarketLogs();
    setPendingApprove(null);
    onShowMarketLogs();
    setBusy(true);
    try {
      const r = await api.installFavorite(targetId, f);
      if (r.already) {
        showToast(r.error || '该插件已安装');
      } else if (r.ok) {
        const names = r.installed.length ? r.installed.join(', ') : f.name;
        showToast(`已安装 ${names}，重启实例后生效`);
        if (wasRunning) {
          showToast('正在重新启动实例…');
          await api.launchInstance(targetId);
        }
      } else if (r.cancelled) {
        showToast('已取消安装', 'error');
      } else if (r.blockedBuilds && r.blockedBuilds.length > 0) {
        setPendingApprove({ names: r.blockedBuilds, retry: () => installFavorite(f) });
        showToast(`构建脚本被拦截（${r.blockedBuilds.join(', ')}），请放行后重试`, 'error');
      } else {
        showToast(r.error || '安装失败，详见输出', 'error');
      }
    } catch (e) {
      showToast('安装失败: ' + errMsg(e), 'error');
    } finally {
      setBusy(false);
      loadInstalled();
    }
  };

  const approveAndRetry = async () => {
    if (!pendingApprove) return;
    const { names, retry } = pendingApprove;
    try {
      await api.approveBuilds(names);
      showToast(`已放行: ${names.join(', ')}，正在重试安装`);
      setPendingApprove(null);
      await retry();
    } catch (e) {
      showToast('放行构建脚本失败: ' + errMsg(e), 'error');
    }
  };

  const uninstall = async (p: InstalledPlugin) => {
    if (!targetInstance) {
      showToast('请先添加实例', 'error');
      return;
    }
    if (!window.confirm(`确定卸载插件「${p.name}」？将同步移除其补丁层禁用行。`)) return;
    const wasRunning = await stopIfRunning();
    if (targetInstance.status !== 'stopped' && targetInstance.status !== 'crashed' && !wasRunning) return;
    onClearMarketLogs();
    onShowMarketLogs();
    setBusy(true);
    try {
      const r = await api.uninstallPlugin(targetId, p.name);
      if (r.ok) {
        showToast(`已卸载 ${p.name}`);
        if (wasRunning) {
          showToast('正在重新启动实例…');
          await api.launchInstance(targetId);
        }
      } else if (r.cancelled) {
        showToast('已取消卸载', 'error');
      } else {
        showToast(r.error || '卸载失败，详见输出', 'error');
      }
    } catch (e) {
      showToast('卸载失败: ' + errMsg(e), 'error');
    } finally {
      setBusy(false);
      loadInstalled();
    }
  };

  const toggle = async (p: InstalledPlugin, enabled: boolean) => {
    try {
      await api.togglePlugin(p.name, enabled);
      showToast(enabled ? `已启用 ${p.name}` : `已禁用 ${p.name}（约 1 秒后生效，重启后保持）`);
      loadInstalled();
    } catch (e) {
      showToast('切换失败: ' + errMsg(e), 'error');
    }
  };

  // --- favorites ---

  // Identity key used for removal must match favoriteID() in Go: npm name
  // (preferred), else lowercased owner/repo, else lowercased name.
  const favIDForEntry = (p: MarketPlugin): string => {
    const npm = p.npm;
    const repo = githubRepoOf(p.url);
    return (npm || repo || p.name).toLowerCase();
  };

  const favDraftFromEntry = (p: MarketPlugin): FavoriteDraft => ({
    name: p.name,
    owner: p.owner,
    url: p.url,
    npm: p.npm ?? null,
    category: p.category,
    description: p.description,
    stars: p.stars ?? null,
    downloads: p.downloads ?? null,
    source: 'catalog',
  });

  const favIDForInstalled = (p: InstalledPlugin): string => {
    const cat = findCatalogEntry(p, catalog);
    if (cat) return favIDForEntry(cat);
    return p.name.toLowerCase();
  };

  const favDraftFromInstalled = (p: InstalledPlugin): FavoriteDraft => {
    const cat = findCatalogEntry(p, catalog);
    if (cat) return favDraftFromEntry(cat);
    return {
      name: p.name,
      owner: '',
      url: p.homepage || '',
      npm: p.kind === 'npm' ? p.name : null,
      category: '',
      description: p.description ? { en: p.description } : {},
      stars: null,
      downloads: null,
      source: 'installed',
      spec: p.spec,
    };
  };

  const isFavorite = (candidates: Array<string | null | undefined>): boolean =>
    candidates.some((c) => c && favIDSet.has(c.toLowerCase()));

  const isFavoriteEntry = (p: MarketPlugin): boolean => isFavorite([p.npm, githubRepoOf(p.url), p.name]);

  const isFavoriteInstalled = (p: InstalledPlugin): boolean => {
    const cat = findCatalogEntry(p, catalog);
    if (cat) return isFavoriteEntry(cat);
    return isFavorite([p.name]);
  };

  const toggleFavorite = async (draft: FavoriteDraft, id: string, displayName: string) => {
    if (favIDSet.has(id.toLowerCase())) {
      try {
        setFavorites((await api.removeFavorite(id)) ?? []);
        showToast(`已取消收藏 ${displayName}`);
      } catch (e) {
        showToast('取消失败: ' + errMsg(e), 'error');
      }
    } else {
      try {
        setFavorites((await api.addFavorite(draft)) ?? []);
        showToast(`已收藏 ${displayName}`);
      } catch (e) {
        showToast('收藏失败: ' + errMsg(e), 'error');
      }
    }
  };

  const removeFavoriteClick = async (f: FavoritePlugin) => {
    try {
      setFavorites((await api.removeFavorite(f.id)) ?? []);
      showToast(`已取消收藏 ${f.name}`);
    } catch (e) {
      showToast('取消失败: ' + errMsg(e), 'error');
    }
  };

  const favoriteInstalled = (f: FavoritePlugin): boolean => {
    const name = (f.npm || f.name).toLowerCase();
    if (installedNamesLower.has(name)) return true;
    const repo = githubRepoOf(f.url);
    if (repo) {
      return installed.some((i) => i.spec.toLowerCase().includes(repo));
    }
    return false;
  };

  const installedForFavorite = (f: FavoritePlugin): InstalledPlugin | undefined =>
    installed.find((i) => i.name.toLowerCase() === (f.npm || f.name).toLowerCase());

  const categories = catalog?.categories ?? {};
  const categoryEntries = Object.entries(categories);

  return (
    <main className="market">
      <div className="market-bar">
        <h2>插件市场</h2>
        <div className="market-bar-right">
          <select
            className="market-instance-select"
            value={targetId}
            onChange={(e) => setTargetId(e.target.value)}
            title="安装/卸载目标实例（profile: web）"
          >
            {instances.length === 0 && <option value="">（无实例）</option>}
            {instances.map((i) => (
              <option key={i.id} value={i.id}>
                {i.name} · {i.directory}
              </option>
            ))}
          </select>
          <span className="market-profile-chip" title="所有实例共用该 profile">profile: web</span>
        </div>
      </div>

      <div className="market-tabs">
        <button className={`market-tab ${tab === 'discover' ? 'active' : ''}`} onClick={() => setTab('discover')}>
          发现
        </button>
        <button className={`market-tab ${tab === 'installed' ? 'active' : ''}`} onClick={() => setTab('installed')}>
          已安装{installed.length > 0 ? ` (${installed.length})` : ''}
        </button>
        <button className={`market-tab ${tab === 'favorites' ? 'active' : ''}`} onClick={() => setTab('favorites')}>
          收藏{favorites.length > 0 ? ` (${favorites.length})` : ''}
        </button>
      </div>

      {tab === 'discover' && (
        <div className="market-discover">
          <div className="market-toolbar">
            <input
              className="market-search"
              placeholder="搜索插件名 / 作者 / 描述…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            <select className="market-sort" value={sort} onChange={(e) => setSort(e.target.value as SortKey)}>
              <option value="downloads-desc">下载量 ↓</option>
              <option value="stars-desc">Star ↓</option>
              <option value="added-desc">新发布 ↓</option>
              <option value="name-asc">名称 A→Z</option>
            </select>
            <button className="btn btn-ghost" onClick={() => loadCatalog(true)} disabled={catalogLoading}>
              {catalogLoading ? '刷新中…' : '刷新目录'}
            </button>
            {catalog && <span className="market-count">{filtered.length} / {catalog.plugins.length}</span>}
          </div>

          <div className="market-cats">
            <button className={`pill market-cat ${category === 'all' ? 'pill-accent' : ''}`} onClick={() => setCategory('all')}>
              全部
            </button>
            {categoryEntries.map(([id, names]) => (
              <button
                key={id}
                className={`pill market-cat ${category === id ? 'pill-accent' : ''}`}
                onClick={() => setCategory(category === id ? 'all' : id)}
              >
                {(names[LANG] || names.en || id)}
              </button>
            ))}
          </div>

          {catalogError && (
            <div className="log-hint log-hint-err">
              {catalogError}
              <div className="row" style={{ marginTop: 6 }}>
                <button className="btn btn-ghost btn-sm" onClick={() => loadCatalog(false)}>重试</button>
                <span className="field-hint">网络较慢或受限时，可在「设置 → 插件市场」配置镜像源</span>
              </div>
            </div>
          )}

          {!catalog && !catalogError && (
            <div className="market-loading">
              <div className="market-grid">
                {Array.from({ length: 8 }).map((_, i) => (
                  <div key={i} className="skeleton-card" />
                ))}
              </div>
              <p className="muted" style={{ textAlign: 'center', marginTop: 10, fontSize: 12 }}>
                正在加载插件目录… 首次下载较慢（视网络 1-2 分钟），之后秒开
              </p>
            </div>
          )}

          {catalog && filtered.length === 0 && (
            <div className="empty"><p>没有匹配的插件</p></div>
          )}

          {catalog && filtered.length > 0 && (
            <div className="market-grid">
              {visible.map((p) => {
                  const isInstalled = installedNames.has(p.name) || installedNames.has(p.npm || '');
                  const isFav = isFavoriteEntry(p);
                  return (
                    <div key={p.url} className="plugin-card">
                      <div className="plugin-card-head">
                        <span className="plugin-name" title={p.name}>{p.name}</span>
                        <span className="pill">{p.category}</span>
                      </div>
                      <div className="plugin-owner">{p.owner}</div>
                      <PluginDesc
                        text={(p.description && (p.description[LANG] || p.description.en)) || '暂无描述'}
                      />
                      <div className="plugin-meta">
                        <span title="Star">★ {fmtCount(p.stars)}</span>
                        <span title="npm 30天下载量">⬇ {fmtCount(p.downloads)}</span>
                        <span className="pill pill-soft">{p.npm ? 'npm' : 'github'}</span>
                      </div>
                      {p.deprecated && (
                        <div className="plugin-deprecated">
                          已弃用{p.replacement ? `，建议改用 ${p.replacement}` : ''}
                        </div>
                      )}
                      <div className="plugin-actions">
                        <button
                          className={`star-btn ${isFav ? 'on' : ''}`}
                          title={isFav ? '取消收藏' : '收藏'}
                          onClick={() => toggleFavorite(favDraftFromEntry(p), favIDForEntry(p), p.name)}
                        >
                          {isFav ? '★' : '☆'}
                        </button>
                        {isInstalled ? (
                          <span className="btn btn-ghost btn-sm" style={{ opacity: 0.55, cursor: 'default' }}>
                            已安装
                          </span>
                        ) : (
                          <button
                            className="btn btn-primary btn-sm"
                            disabled={marketOp.running || busy}
                            onClick={() => install(p)}
                          >
                            {busy && !marketOp.running ? '准备中…' : '安装'}
                          </button>
                        )}
                        <a className="link-btn" href={p.url} target="_blank" rel="noreferrer">GitHub ↗</a>
                      </div>
                    </div>
                  );
                })}
              {hasMore && (
                <div className="market-load-more">
                  <button
                    className="btn btn-ghost"
                    onClick={() => setVisibleCount((n) => n + PAGE_SIZE)}
                  >
                    加载更多（{filtered.length - visibleCount} 个）
                  </button>
                </div>
              )}
              {!hasMore && (
                <div className="market-end">已全部加载 · 共 {filtered.length} 个</div>
              )}
            </div>
          )}
        </div>
      )}

      {tab === 'installed' && (
        <div className="market-installed">
          <div className="market-toolbar">
            <button className="btn btn-ghost" onClick={loadInstalled}>刷新</button>
            <span className="field-hint">
              开关写入 profile 的 cordis.patch.yml，约 1 秒内生效（HMR），重启后保持
            </span>
          </div>
          {installed.length === 0 && <div className="empty"><p>暂无已装社区插件</p></div>}
          {installed.map((p) => {
            const cat = findCatalogEntry(p, catalog);
            const displayName = cat?.name || p.name;
            const desc =
              (cat && (cat.description[LANG] || cat.description.en)) ||
              p.description ||
              '（无描述）';
            const catLabel =
              (cat && catalog?.categories?.[cat.category]?.[LANG]) ||
              (cat && catalog?.categories?.[cat.category]?.en) ||
              cat?.category ||
              '';
            const isFav = isFavoriteInstalled(p);
            return (
              <div key={p.name} className="installed-row">
                <div className="installed-info">
                  <div className="installed-head">
                    <span className="installed-name" title={p.name}>{displayName}</span>
                    {catLabel && <span className="pill">{catLabel}</span>}
                    <span className="pill pill-soft">{p.kind}</span>
                    {p.version && <span className="installed-version mono">v{p.version}</span>}
                    {p.state === 'disabled' && <span className="pill tag-warn">已停用</span>}
                  </div>
                  <div className="installed-desc">{desc}</div>
                  <div className="installed-sub">
                    <span className="mono">{p.name}</span>
                    {cat?.owner && <span> · {cat.owner}</span>}
                    {cat?.stars != null && <span> · ★ {fmtCount(cat.stars)}</span>}
                    {cat?.downloads != null && <span> · ⬇ {fmtCount(cat.downloads)}</span>}
                  </div>
                </div>
                <div className="installed-actions">
                  <button
                    className={`star-btn ${isFav ? 'on' : ''}`}
                    title={isFav ? '取消收藏' : '收藏'}
                    onClick={() => toggleFavorite(favDraftFromInstalled(p), favIDForInstalled(p), p.name)}
                  >
                    {isFav ? '★' : '☆'}
                  </button>
                  <label className="installed-toggle" title="写入 cordis.patch.yml 的 disabled 开关">
                    <Switch
                      checked={p.state !== 'disabled'}
                      disabled={marketOp.running || busy}
                      onChange={(v) => toggle(p, v)}
                    />
                    <span className={p.state === 'disabled' ? 'muted' : ''}>
                      {p.state === 'disabled' ? '已停用' : '已启用'}
                    </span>
                  </label>
                  <button className="btn btn-ghost btn-sm danger-text" disabled={marketOp.running || busy} onClick={() => uninstall(p)}>
                    卸载
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {tab === 'favorites' && (
        <div className="market-favorites">
          <div className="market-toolbar">
            <button className="btn btn-ghost" onClick={loadFavorites}>刷新</button>
            <button className="btn btn-ghost" onClick={() => setShareDialog('gen')}>生成分享码</button>
            <button className="btn btn-ghost" onClick={() => setShareDialog('import')}>解析分享码</button>
            <span className="field-hint">收藏保存在本机（favorites.json），断网也能查看和安装</span>
          </div>

          {favorites.length === 0 && (
            <div className="empty"><p>还没有收藏的插件，可在「发现」或「已安装」页点击 ☆ 收藏</p></div>
          )}

          {favorites.length > 0 && (
            <div className="market-grid">
              {favorites.map((f) => {
                const isInstalled = favoriteInstalled(f);
                return (
                  <div key={f.id} className="plugin-card">
                    <div className="plugin-card-head">
                      <span className="plugin-name" title={f.name}>{f.name}</span>
                      <span className="pill">{f.category || f.source}</span>
                    </div>
                    <div className="plugin-owner">{f.owner || '—'}</div>
                    <PluginDesc
                      text={(f.description && (f.description[LANG] || f.description.en)) || '暂无描述'}
                    />
                    <div className="plugin-meta">
                      {f.stars != null && <span title="Star">★ {fmtCount(f.stars)}</span>}
                      {f.downloads != null && <span title="npm 30天下载量">⬇ {fmtCount(f.downloads)}</span>}
                      <span className="pill pill-soft">{f.source === 'catalog' ? '收藏自目录' : '收藏自已装'}</span>
                      {f.addedAt && <span className="field-hint" title={f.addedAt}>{fmtDate(f.addedAt)}</span>}
                    </div>
                    <div className="plugin-actions">
                      {isInstalled ? (
                        <button
                          className="btn btn-ghost btn-sm danger-text"
                          disabled={marketOp.running || busy}
                          onClick={() => {
                            const ip = installedForFavorite(f);
                            if (ip) uninstall(ip);
                          }}
                        >
                          卸载
                        </button>
                      ) : (
                        <button
                          className="btn btn-primary btn-sm"
                          disabled={marketOp.running || busy}
                          onClick={() => installFavorite(f)}
                        >
                          {busy && !marketOp.running ? '准备中…' : '安装'}
                        </button>
                      )}
                      <button className="btn btn-ghost btn-sm" onClick={() => removeFavoriteClick(f)}>取消收藏</button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}

      {pendingApprove && (
        <div className="log-hint">
          构建脚本被拦截：{pendingApprove.names.join(', ')}。
          <button className="btn btn-accent btn-sm" onClick={approveAndRetry}>放行并重试</button>
        </div>
      )}

      {/* Slim status strip — only while an install/uninstall is in flight;
          full streamed output lives in the right-side run-log drawer
          (auto-popped on install/uninstall), and completion is toasted. */}
      {marketOp.running && (
        <div className="market-strip busy">
          <span className="spin" />
          <span className="market-strip-text">
            正在{marketOp.kind === 'uninstall' ? '卸载' : '安装'} {marketOp.target}…
          </span>
          <button className="btn btn-ghost btn-sm" onClick={onCancelMarket}>取消</button>
          <button className="btn btn-ghost btn-sm" onClick={onShowMarketLogs}>查看进度</button>
        </div>
      )}

      {/* Share-code flows open as a modal (pick subset / preview before import). */}
      {shareDialog === 'gen' && (
        <ShareCodeDialog
          mode="gen"
          favorites={favorites}
          showToast={showToast}
          onImported={() => setShareDialog(null)}
          onClose={() => setShareDialog(null)}
        />
      )}
      {shareDialog === 'import' && (
        <ShareCodeDialog
          mode="import"
          favorites={favorites}
          showToast={showToast}
          onImported={() => {
            loadFavorites();
            setShareDialog(null);
          }}
          onClose={() => setShareDialog(null)}
        />
      )}
    </main>
  );
}

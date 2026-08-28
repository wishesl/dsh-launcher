import { useCallback, useEffect, useRef, useState } from 'react';
import { api, errMsg } from './api';
import { BrowserOpenURL } from '../wailsjs/runtime/runtime';
import type { ExitChoice, Instance, LogEvent, MarketOpState, RegistryInfo, ServiceState } from './types';
import Header from './components/Header';
import Sidebar, { type ViewKey } from './components/Sidebar';
import VersionView from './components/VersionView';
import InstancesView from './components/InstancesView';
import MarketView from './components/MarketView';
import SettingsView from './components/SettingsView';
import InstanceForm from './components/InstanceForm';
import ExitDialog from './components/ExitDialog';
import LogDrawer from './components/LogDrawer';

type ModalState = { mode: 'new' } | { mode: 'edit'; instance: Instance } | null;
type Toast = { msg: string; kind: 'ok' | 'error' } | null;

export default function App() {
  const [view, setView] = useState<ViewKey>('versions'); // 首页默认打开「版本历史」
  const [instances, setInstances] = useState<Instance[]>([]);
  // Independent service reachability per instance (does the configured port
  // answer HTTP right now). Decoupled from process state — drives the header
  // "已就绪" + the open button.
  const [service, setService] = useState<Record<string, ServiceState>>({});
  const [registry, setRegistry] = useState<RegistryInfo | null>(null);
  const [registryLoading, setRegistryLoading] = useState(false);
  const [appDataPath, setAppDataPath] = useState('');
  const [logs, setLogs] = useState<Record<string, LogEvent[]>>({});
  const [activeLogId, setActiveLogId] = useState<string | null>(null);
  // Right-side run-log drawer: open state + which tab (instance logs / market).
  const [logsOpen, setLogsOpen] = useState(false);
  // Sidebar (菜单栏) 展开/收起：收起后变为仅图标小卡片。
  const [collapsed, setCollapsed] = useState(false);
  const [logsTab, setLogsTab] = useState<'logs' | 'market'>('logs');
  // Plugin-market operation stream (hoisted so the drawer can show it too).
  const [marketLogs, setMarketLogs] = useState<string[]>([]);
  const [marketOp, setMarketOp] = useState<MarketOpState>({ running: false, kind: '', target: '' });
  const [modal, setModal] = useState<ModalState>(null);
  // Window ✕ pressed and the user wants to be asked (no remembered choice).
  const [exitAsk, setExitAsk] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [toast, setToast] = useState<Toast>(null);

  const logsRef = useRef<Record<string, LogEvent[]>>({});
  const marketLogsRef = useRef<string[]>([]);
  const toastTimer = useRef<number | undefined>(undefined);
  // "本次启动不再提示": remembered exit choice for THIS app run only —
  // deliberately not persisted, the chooser asks again on next launch.
  const exitChoiceRef = useRef<ExitChoice | null>(null);

  // Unified feedback channel: success (default) and error toasts.
  const showToast = useCallback((msg: string, kind: 'ok' | 'error' = 'ok') => {
    setToast({ msg, kind });
    if (toastTimer.current) window.clearTimeout(toastTimer.current);
    toastTimer.current = window.setTimeout(
      () => setToast(null),
      kind === 'error' ? 6500 : 3000
    );
  }, []);

  // Open the right-side run-log drawer on a given tab.
  const openLogs = useCallback((tab: 'logs' | 'market') => {
    setLogsTab(tab);
    setLogsOpen(true);
  }, []);

  const clearMarketLogs = useCallback(() => {
    marketLogsRef.current = [];
    setMarketLogs([]);
  }, []);

  const cancelMarket = useCallback(() => {
    api.cancelMarketOp().catch(() => undefined);
  }, []);

  // Stable callbacks for MarketView — a new identity each render would re-fire
  // its mount effect and clobber the live marketOp state.
  const showMarketLogs = useCallback(() => openLogs('market'), [openLogs]);
  const setMarketRunning = useCallback((running: boolean) => {
    setMarketOp((o) => ({ ...o, running }));
  }, []);

  const refresh = useCallback(async () => {
    try {
      setInstances(await api.getInstances());
    } catch (e) {
      showToast('加载实例失败: ' + errMsg(e), 'error');
    }
  }, [showToast]);

  const refreshRegistry = useCallback(async () => {
    setRegistryLoading(true);
    try {
      const info = await api.queryRegistry();
      setRegistry(info);
    } catch (e) {
      showToast('获取最新版本失败: ' + errMsg(e), 'error');
    } finally {
      setRegistryLoading(false);
    }
  }, [showToast]);

  // Re-read the backend's service-reachability snapshot (best-effort).
  const refreshServices = useCallback(async () => {
    try {
      const list = await api.probeServices();
      setService((prev) => {
        const next = { ...prev };
        for (const s of list) next[s.instanceId] = s;
        return next;
      });
    } catch {
      /* service probe is best-effort; the dsh:service stream fills gaps */
    }
  }, []);

  useEffect(() => {
    refresh();
    refreshRegistry();
    refreshServices();
    api.getAppDataPath().then(setAppDataPath).catch(() => undefined);
  }, [refresh, refreshRegistry, refreshServices]);

  // Wire Go -> frontend events. Return cleanup so StrictMode's double-mount
  // (and hot reloads) don't stack duplicate listeners.
  useEffect(() => {
    api.onLog((e: LogEvent) => {
      const prev = logsRef.current[e.instanceId] ?? [];
      const arr = [...prev, e];
      if (arr.length > 3000) arr.splice(0, arr.length - 3000);
      const map = { ...logsRef.current, [e.instanceId]: arr };
      logsRef.current = map;
      setLogs(map);
    });
    api.onStatus((e) => {
      setInstances((prev) =>
        prev.map((i) => {
          if (i.id !== e.instanceId) return i;
          const next: Instance = {
            ...i,
            status: e.status as Instance['status'],
            pid: e.pid,
          };
          if (e.status === 'ready' && e.webUrl) next.webUrl = e.webUrl;
          else next.webUrl = null; // stale URL once not running/ready
          return next;
        })
      );
    });
    // Independent service reachability (decoupled from process state).
    api.onService((e) => {
      setService((prev) => ({ ...prev, [e.instanceId]: e }));
    });
    api.onNotice((n) => showToast(n.msg));
    // Plugin-market operation stream (drawer shows it as the "市场任务" tab).
    api.onMarketLog((e) => {
      const arr = [...marketLogsRef.current.slice(-400), e.line];
      marketLogsRef.current = arr;
      setMarketLogs(arr);
    });
    api.onMarketStatus((e) => {
      setMarketOp({ running: e.state === 'running', kind: e.kind, target: e.target });
    });
    api.onCloseRequest(() => {
      const remembered = exitChoiceRef.current;
      if (remembered === 'tray') {
        api.hideToTray().catch(() => undefined);
      } else if (remembered === 'quit') {
        api.quitApp().catch(() => undefined);
      } else {
        setExitAsk(true);
      }
    });
    // Auto-start must run only AFTER the listeners above are registered —
    // Wails events emitted before subscription are dropped.
    api.runAutoStartInstances()
      .then((ids) => {
        if (ids && ids.length > 0) {
          setActiveLogId((cur) => cur ?? ids[0]);
          openLogs('logs');
        }
      })
      .catch(() => undefined);
    return () => {
      api.offLog();
      api.offStatus();
      api.offService();
      api.offNotice();
      api.offMarketLog();
      api.offMarketStatus();
      api.offCloseRequest();
    };
  }, [showToast, openLogs]);

  const start = async (id: string) => {
    setBusyId(id);
    try {
      await api.launchInstance(id);
      setActiveLogId(id);
      openLogs('logs');
      showToast('已发起启动，日志见右侧面板');
    } catch (e) {
      showToast(errMsg(e), 'error');
    } finally {
      setBusyId(null);
    }
  };

  const stop = async (id: string) => {
    setBusyId(id);
    try {
      await api.stopInstance(id);
      showToast('已停止');
    } catch (e) {
      showToast(errMsg(e), 'error');
    } finally {
      setBusyId(null);
    }
  };

  const remove = async (id: string) => {
    if (!window.confirm('确定删除该实例？运行中的进程会被停止。')) return;
    try {
      const list = await api.removeInstance(id);
      setInstances(list);
      setService((prev) => {
        const next = { ...prev };
        delete next[id];
        return next;
      });
      if (activeLogId === id) setActiveLogId(null);
      showToast('已删除');
    } catch (e) {
      showToast(errMsg(e), 'error');
    }
  };

  const install = async (id: string) => {
    setBusyId(id);
    setActiveLogId(id);
    try {
      const list = await api.installToDirectory(id);
      setInstances(list);
      showToast('安装完成，本地副本已生成');
    } catch (e) {
      showToast('安装失败: ' + errMsg(e), 'error');
    } finally {
      setBusyId(null);
    }
  };

  const openWeb = (url: string) => {
    try {
      BrowserOpenURL(url);
    } catch (e) {
      showToast('打开浏览器失败: ' + errMsg(e), 'error');
    }
  };

  const copyUrl = (url: string) => {
    navigator.clipboard
      .writeText(url)
      .then(() => showToast('地址已复制'))
      .catch(() => showToast('复制失败: ' + url, 'error'));
  };

  const hideToTray = () => {
    api.hideToTray().catch((e) => showToast('隐藏失败: ' + errMsg(e), 'error'));
  };

  // Frameless 自定义关闭按钮 → 复用与原生 ✕ 相同的关闭链路（后端
  // RequestClose → dsh:close-requested → 托盘/退出选择框）。
  const requestClose = useCallback(() => {
    api.requestClose().catch(() => undefined);
  }, []);

  const chooseExit = (action: ExitChoice, remember: boolean) => {
    if (remember) exitChoiceRef.current = action;
    setExitAsk(false);
    if (action === 'tray') {
      hideToTray();
    } else {
      api.quitApp().catch((e) => showToast('退出失败: ' + errMsg(e), 'error'));
    }
  };

  const toggleAutoStart = async (id: string, v: boolean) => {
    try {
      setInstances(await api.setAutoStart(id, v));
    } catch (e) {
      showToast('修改自动启动失败: ' + errMsg(e), 'error');
    }
  };

  // Select which instance's log the drawer shows (and open the drawer).
  const selectLog = (id: string) => {
    // 再次点击当前正在查看的实例：收起日志面板（toggle 关闭）；否则切换到该实例并打开。
    if (activeLogId === id && logsOpen) {
      setLogsOpen(false);
    } else {
      setActiveLogId(id);
      openLogs('logs');
    }
  };

  const clearLog = (id: string) => {
    const map = { ...logsRef.current };
    map[id] = [];
    logsRef.current = map;
    setLogs(map);
  };

  // Service-driven "DSH 已就绪": the first instance whose configured port
  // answers HTTP right now — independent of whether the launcher manages its
  // process (an externally-started DSH on the same port still counts).
  const serviceLive = (() => {
    for (const i of instances) {
      const s = service[i.id];
      if (s && s.reachable && s.url) return { id: i.id, name: i.name, url: s.url };
    }
    return null;
  })();

  // Process-managed running instance: drives the header's "运行中…" fallback
  // and the 重启 button (restart only makes sense for a launcher-managed
  // process). Deliberately separate from serviceLive.
  const dshLive =
    instances.find((i) => i.status === 'ready') ??
    instances.find((i) => i.status === 'running' || i.status === 'starting') ??
    null;

  // Live activity badge for the header 运行日志 button.
  const logsLive =
    instances.some((i) => i.status === 'starting' || i.status === 'running') || marketOp.running;

  // Quick restart of the tracked DSH instance (stop → launch).
  const restartDsh = async () => {
    if (!dshLive) {
      showToast('当前没有运行中的 DSH 实例', 'error');
      return;
    }
    if (!window.confirm(`确定重启「${dshLive.name}」？运行中的会话会中断，实例目录与插件不变。`)) return;
    try {
      await api.stopInstance(dshLive.id);
      showToast(`正在重启 ${dshLive.name}…`);
      setActiveLogId(dshLive.id);
      openLogs('logs');
      await api.launchInstance(dshLive.id);
    } catch (e) {
      showToast('重启失败: ' + errMsg(e), 'error');
    }
  };

  return (
    <div className="app">
      <Header
        registry={registry}
        registryLoading={registryLoading}
        serviceLive={serviceLive}
        dshLive={dshLive}
        onRestartDsh={restartDsh}
        onOpenWeb={openWeb}
        logsOpen={logsOpen}
        logsLive={logsLive}
        onToggleLogs={() => setLogsOpen((o) => !o)}
        onCloseRequest={requestClose}
        collapsed={collapsed}
        onToggleCollapse={() => setCollapsed((v) => !v)}
      />

      <div className="app-body">
        <Sidebar
          view={view}
          onNavigate={setView}
          collapsed={collapsed}
        />
        <div className="app-main">
        <div className="app-content">
          {view === 'versions' && (
            <VersionView
              registry={registry}
              registryLoading={registryLoading}
              onRefreshRegistry={refreshRegistry}
            />
          )}
          {view === 'instances' && (
            <InstancesView
              instances={instances}
              service={service}
              registry={registry}
              registryLoading={registryLoading}
              busyId={busyId}
              activeLogId={activeLogId}
              logsOpen={logsOpen}
              logs={logs}
              onAdd={() => setModal({ mode: 'new' })}
              onStart={start}
              onStop={stop}
              onInstall={install}
              onOpen={openWeb}
              onCopyUrl={copyUrl}
              onEdit={(inst) => setModal({ mode: 'edit', instance: inst })}
              onDelete={remove}
              onSelectLog={selectLog}
              onToggleAutoStart={toggleAutoStart}
            />
          )}
          {view === 'market' && (
            <MarketView
              instances={instances}
              showToast={showToast}
              marketOp={marketOp}
              onClearMarketLogs={clearMarketLogs}
              onCancelMarket={cancelMarket}
              onShowMarketLogs={showMarketLogs}
              onMarketRunning={setMarketRunning}
            />
          )}
          {view === 'settings' && <SettingsView showToast={showToast} appDataPath={appDataPath} />}
        </div>

        {/* Right-side run-log pane: in-flow third column (sidebar | content | logs) */}
        <LogDrawer
          open={logsOpen}
          onClose={() => setLogsOpen(false)}
          instances={instances}
          logs={logs}
          activeLogId={activeLogId}
          onSelect={selectLog}
          onClear={clearLog}
          tab={logsTab}
          onTabChange={setLogsTab}
          marketLogs={marketLogs}
          marketOp={marketOp}
          onClearMarketLogs={clearMarketLogs}
          onCancelMarket={cancelMarket}
        />
      </div>
      </div>

      {modal && (
        <InstanceForm
          registry={registry}
          editing={modal.mode === 'edit' ? modal.instance : null}
          onClose={() => setModal(null)}
          onSaved={(list, note) => {
            setInstances(list);
            if (note) showToast(note);
          }}
        />
      )}

      {exitAsk && <ExitDialog onClose={() => setExitAsk(false)} onChoose={chooseExit} />}

      {toast && (
        <div className={`toast ${toast.kind === 'error' ? 'toast-error' : ''}`}>{toast.msg}</div>
      )}
    </div>
  );
}

import { useCallback, useEffect, useRef, useState } from 'react';
import { api, errMsg } from './api';
import { BrowserOpenURL } from '../wailsjs/runtime/runtime';
import type { ExitChoice, Instance, LogEvent, RegistryInfo } from './types';
import Header from './components/Header';
import InstanceList from './components/InstanceList';
import InstanceForm from './components/InstanceForm';
import VersionPanel from './components/VersionPanel';
import LogPanel from './components/LogPanel';
import ExitDialog from './components/ExitDialog';
import SettingsModal from './components/SettingsModal';
import MarketView from './components/MarketView';

type ModalState = { mode: 'new' } | { mode: 'edit'; instance: Instance } | null;
type Toast = { msg: string; kind: 'ok' | 'error' } | null;

export default function App() {
  const [instances, setInstances] = useState<Instance[]>([]);
  const [registry, setRegistry] = useState<RegistryInfo | null>(null);
  const [registryLoading, setRegistryLoading] = useState(false);
  const [appDataPath, setAppDataPath] = useState('');
  const [logs, setLogs] = useState<Record<string, LogEvent[]>>({});
  const [activeLogId, setActiveLogId] = useState<string | null>(null);
  const [modal, setModal] = useState<ModalState>(null);
  const [showSettings, setShowSettings] = useState(false);
  const [view, setView] = useState<'main' | 'market'>('main');
  // Window ✕ pressed and the user wants to be asked (no remembered choice).
  const [exitAsk, setExitAsk] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [toast, setToast] = useState<Toast>(null);

  const logsRef = useRef<Record<string, LogEvent[]>>({});
  const logViewRef = useRef<HTMLDivElement>(null);
  const toastTimer = useRef<number | undefined>(undefined);
  // "本次启动不再提示": remembered exit choice for THIS app run only —
  // deliberately not persisted, the chooser asks again on next launch.
  const exitChoiceRef = useRef<ExitChoice | null>(null);

  // Unified feedback channel: success (default) and error toasts replace the
  // old split of "error banner (manual close)" + "success toast (3s)".
  const showToast = useCallback((msg: string, kind: 'ok' | 'error' = 'ok') => {
    setToast({ msg, kind });
    if (toastTimer.current) window.clearTimeout(toastTimer.current);
    toastTimer.current = window.setTimeout(
      () => setToast(null),
      kind === 'error' ? 6500 : 3000
    );
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

  useEffect(() => {
    refresh();
    refreshRegistry();
    api.getAppDataPath().then(setAppDataPath).catch(() => undefined);
  }, [refresh, refreshRegistry]);

  // Wire Go -> frontend events. Return cleanup so StrictMode's double-mount
  // (and hot reloads) don't stack duplicate listeners.
  useEffect(() => {
    api.onLog((e: LogEvent) => {
      // Immutable update: always create a NEW array so LogPanel's
      // useMemo([instance, logs]) invalidates and log lines render live.
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
    api.onNotice((n) => showToast(n.msg));
    // Window close is decided in the frontend: show the exit chooser, or run
    // the choice remembered via "本次启动不再提示" straight away.
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
    // Wails events emitted before subscription are dropped, which is why
    // auto-started instances used to show an empty log panel. The returned
    // IDs let us open the log panel for the first auto-started instance.
    api.runAutoStartInstances()
      .then((ids) => {
        if (ids && ids.length > 0) setActiveLogId((cur) => cur ?? ids[0]);
      })
      .catch(() => undefined);
    return () => {
      api.offLog();
      api.offStatus();
      api.offNotice();
      api.offCloseRequest();
    };
  }, [showToast]);

  const start = async (id: string) => {
    setBusyId(id);
    try {
      await api.launchInstance(id);
      setActiveLogId(id);
      showToast('已发起启动，日志见下方');
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

  // Exit-chooser answer. `remember` = "本次启动不再提示" was ticked: keep the
  // choice for this app run so later ✕ presses skip the dialog.
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

  const toggleLog = (id: string) => {
    setActiveLogId((cur) => (cur === id ? null : id));
    if (logViewRef.current) {
      // scroll to bottom when opening logs
      setTimeout(() => logViewRef.current?.scrollTo({ top: logViewRef.current.scrollHeight }), 50);
    }
  };

  const activeInstance = instances.find((i) => i.id === activeLogId) ?? null;

  return (
    <div className="app">
      <Header
        registry={registry}
        registryLoading={registryLoading}
        appDataPath={appDataPath}
        onRefreshRegistry={refreshRegistry}
        onOpenSettings={() => setShowSettings(true)}
        onHideToTray={hideToTray}
        onOpenMarket={() => setView((v) => (v === 'market' ? 'main' : 'market'))}
        marketActive={view === 'market'}
      />

      {view === 'market' ? (
        <MarketView instances={instances} showToast={showToast} onBack={() => setView('main')} />
      ) : (
        <main className="layout">
          <div className="col-left">
            <InstanceList
              instances={instances}
              registry={registry}
              busyId={busyId}
              activeLogId={activeLogId}
              onAdd={() => setModal({ mode: 'new' })}
              onStart={start}
              onStop={stop}
              onInstall={install}
              onOpen={openWeb}
              onCopyUrl={copyUrl}
              onEdit={(inst) => setModal({ mode: 'edit', instance: inst })}
              onDelete={remove}
              onToggleLog={toggleLog}
              onToggleAutoStart={toggleAutoStart}
            />
            <LogPanel
              instance={activeInstance}
              logs={activeLogId ? logs[activeLogId] ?? [] : []}
              onClear={() => {
                if (activeLogId) {
                  const map = { ...logsRef.current };
                  map[activeLogId] = [];
                  logsRef.current = map;
                  setLogs(map);
                }
              }}
              logRef={logViewRef}
            />
          </div>

          <div className="col-right">
            <VersionPanel registry={registry} loading={registryLoading} />
          </div>
        </main>
      )}

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

      {showSettings && (
        <SettingsModal onClose={() => setShowSettings(false)} showToast={showToast} />
      )}

      {exitAsk && <ExitDialog onClose={() => setExitAsk(false)} onChoose={chooseExit} />}

      {toast && (
        <div className={`toast ${toast.kind === 'error' ? 'toast-error' : ''}`}>{toast.msg}</div>
      )}
    </div>
  );
}

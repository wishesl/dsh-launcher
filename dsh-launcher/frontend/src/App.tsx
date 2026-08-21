import { useCallback, useEffect, useRef, useState } from 'react';
import { api, errMsg } from './api';
import type { Instance, LogEvent, RegistryInfo } from './types';
import Header from './components/Header';
import InstanceList from './components/InstanceList';
import InstanceForm from './components/InstanceForm';
import VersionPanel from './components/VersionPanel';
import LogPanel from './components/LogPanel';

type ModalState = { mode: 'new' } | { mode: 'edit'; instance: Instance } | null;

export default function App() {
  const [instances, setInstances] = useState<Instance[]>([]);
  const [registry, setRegistry] = useState<RegistryInfo | null>(null);
  const [registryLoading, setRegistryLoading] = useState(false);
  const [appDataPath, setAppDataPath] = useState('');
  const [logs, setLogs] = useState<Record<string, LogEvent[]>>({});
  const [activeLogId, setActiveLogId] = useState<string | null>(null);
  const [modal, setModal] = useState<ModalState>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);

  const logsRef = useRef<Record<string, LogEvent[]>>({});
  const logViewRef = useRef<HTMLDivElement>(null);
  const toastTimer = useRef<number | undefined>(undefined);

  const showToast = useCallback((msg: string) => {
    setToast(msg);
    if (toastTimer.current) window.clearTimeout(toastTimer.current);
    toastTimer.current = window.setTimeout(() => setToast(null), 3000);
  }, []);

  const refresh = useCallback(async () => {
    try {
      setInstances(await api.getInstances());
    } catch (e) {
      setError(errMsg(e));
    }
  }, []);

  const refreshRegistry = useCallback(async () => {
    setRegistryLoading(true);
    try {
      const info = await api.queryRegistry();
      setRegistry(info);
    } catch (e) {
      setError('获取最新版本失败: ' + errMsg(e));
    } finally {
      setRegistryLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
    refreshRegistry();
    api.getAppDataPath().then(setAppDataPath).catch(() => undefined);
  }, [refresh, refreshRegistry]);

  // Wire Go -> frontend events.
  useEffect(() => {
    api.onLog((e: LogEvent) => {
      const map = { ...logsRef.current };
      const arr = map[e.instanceId] ?? [];
      arr.push(e);
      if (arr.length > 3000) arr.splice(0, arr.length - 3000);
      map[e.instanceId] = arr;
      logsRef.current = map;
      setLogs(map);
    });
    api.onStatus((e) => {
      setInstances((prev) =>
        prev.map((i) => (i.id === e.instanceId ? { ...i, status: e.status as Instance['status'], pid: e.pid } : i))
      );
    });
  }, []);

  const start = async (id: string) => {
    setBusyId(id);
    try {
      await api.launchInstance(id);
      setActiveLogId(id);
      showToast('已发起启动，日志见下方');
    } catch (e) {
      setError(errMsg(e));
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
      setError(errMsg(e));
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
      setError(errMsg(e));
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
      />

      {error && (
        <div className="error-banner">
          <span>{error}</span>
          <button className="link-btn" onClick={() => setError(null)}>关闭</button>
        </div>
      )}

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
            onEdit={(inst) => setModal({ mode: 'edit', instance: inst })}
            onDelete={remove}
            onToggleLog={toggleLog}
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

      {modal && (
        <InstanceForm
          registry={registry}
          editing={modal.mode === 'edit' ? modal.instance : null}
          onClose={() => setModal(null)}
          onSaved={(list) => setInstances(list)}
        />
      )}

      {toast && <div className="toast">{toast}</div>}
    </div>
  );
}

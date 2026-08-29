import { useCallback, useEffect, useRef, useState } from 'react';
import { api, errMsg } from '../api';
import type { EnvReport, LayoutMode, MarketSettings, ToolStatus } from '../types';

interface Props {
  showToast: (msg: string, kind?: 'ok' | 'error') => void;
  appDataPath: string;
  layout: LayoutMode;
  onSetLayout: (mode: LayoutMode) => Promise<void>;
}

function ToolRow({ tool, checking }: { tool: ToolStatus | undefined; checking: boolean }) {
  return (
    <div className="env-row">
      <div className="env-main">
        <div className="env-name">{tool?.name ?? '—'}</div>
        <div className="env-desc">
          {tool?.name === 'npm'
            ? 'Node.js 包管理器（DSH 运行 / 安装 pnpm 的基础）'
            : '高性能包管理器，「安装到目录」与插件安装使用它'}
        </div>
      </div>
      {checking ? (
        <span className="env-missing">检测中…</span>
      ) : tool?.found ? (
        <span className="env-ver" title={`${tool.name} 版本`}>v{tool.version}</span>
      ) : (
        <span className="env-missing">未检测到</span>
      )}
    </div>
  );
}

export default function SettingsView({ showToast, appDataPath, layout, onSetLayout }: Props) {
  const [env, setEnv] = useState<EnvReport | null>(null);
  const [checking, setChecking] = useState(true);
  const [installing, setInstalling] = useState(false);
  const [lines, setLines] = useState<string[]>([]);
  const outRef = useRef<HTMLPreElement>(null);
  const [market, setMarket] = useState<MarketSettings | null>(null);
  const [marketUrl, setMarketUrl] = useState('');
  const [savingMarket, setSavingMarket] = useState(false);
  const [proxy, setProxy] = useState('');
  const [savingProxy, setSavingProxy] = useState(false);
  const [layoutMode, setLayoutMode] = useState<LayoutMode>(layout);
  const [savingLayout, setSavingLayout] = useState(false);

  // Sync local selection when the app-wide preference changes (e.g. after save).
  useEffect(() => {
    setLayoutMode(layout);
  }, [layout]);

  const check = useCallback(async () => {
    setChecking(true);
    try {
      setEnv(await api.checkEnvironment());
    } catch (e) {
      showToast('环境检查失败: ' + errMsg(e), 'error');
    } finally {
      setChecking(false);
    }
  }, [showToast]);

  useEffect(() => {
    check();
  }, [check]);

  useEffect(() => {
    api.getMarketSettings().then((m) => {
      setMarket(m);
      setMarketUrl(m.registryUrl);
    }).catch(() => undefined);
  }, []);

  useEffect(() => {
    api.getProxy().then((p) => setProxy(p.proxy)).catch(() => undefined);
  }, []);

  useEffect(() => {
    api.onEnvLog((e) => setLines((prev) => [...prev.slice(-400), e.line]));
    return () => api.offEnvLog();
  }, []);

  useEffect(() => {
    const el = outRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines]);

  const installPnpm = async () => {
    if (installing) return;
    setInstalling(true);
    setLines([]);
    try {
      await api.installPnpm();
      showToast('pnpm 安装完成');
    } catch (e) {
      showToast('pnpm 安装失败，详见输出: ' + errMsg(e), 'error');
    } finally {
      setInstalling(false);
      check();
    }
  };

  const saveMarket = async () => {
    setSavingMarket(true);
    try {
      await api.setMarketRegistryURL(marketUrl.trim());
      showToast('市场设置已保存');
    } catch (e) {
      showToast('保存失败: ' + errMsg(e), 'error');
    } finally {
      setSavingMarket(false);
    }
  };

  const saveProxy = async () => {
    setSavingProxy(true);
    try {
      await api.setProxy(proxy.trim());
      showToast('代理设置已保存');
    } catch (e) {
      showToast('保存失败: ' + errMsg(e), 'error');
    } finally {
      setSavingProxy(false);
    }
  };

  const saveLayout = async () => {
    setSavingLayout(true);
    try {
      await onSetLayout(layoutMode);
      showToast('布局设置已保存');
    } catch (e) {
      showToast('保存失败: ' + errMsg(e), 'error');
    } finally {
      setSavingLayout(false);
    }
  };

  const layoutLabel = (m: LayoutMode) =>
    m === 'mac' ? 'Mac 布局' : m === 'win' ? 'Windows / Linux 布局' : '自动（按系统）';

  return (
    <div className="settings-view">
      <div className="settings-section">
        <h3>前置环境</h3>
        <p className="desc">npm / pnpm 是 DSH 安装与插件管理的基础。缺失时可用下方按钮一键安装 pnpm。</p>
        <div className="env-list">
          <ToolRow tool={env?.npm} checking={checking} />
          <ToolRow tool={env?.pnpm} checking={checking} />
        </div>
        <div className="row">
          <button className="btn btn-ghost" onClick={check} disabled={checking || installing}>
            {checking ? '检查中…' : '一键检查版本'}
          </button>
          <button
            className="btn btn-accent"
            onClick={installPnpm}
            disabled={installing || checking}
            title="执行 npm install -g pnpm（已安装则升级）"
          >
            {installing ? '安装中…' : env?.pnpm.found && !checking ? '重装/升级 pnpm' : '安装 pnpm'}
          </button>
        </div>
        {(installing || lines.length > 0) && (
          <pre className="env-output" ref={outRef}>
            {lines.join('\n')}
          </pre>
        )}
      </div>

      <div className="settings-section">
        <h3>网络代理</h3>
        <p className="desc">
          安装到目录、插件安装、pnpm 安装等下载超时或网络受限时，可填写 HTTP/HTTPS/SOCKS 代理地址（如
          http://127.0.0.1:20171）。留空则走直连。
        </p>
        <div className="row">
          <input
            type="text"
            value={proxy}
            onChange={(e) => setProxy(e.target.value)}
            placeholder="http://127.0.0.1:20171"
          />
          <button className="btn btn-accent" onClick={saveProxy} disabled={savingProxy}>
            {savingProxy ? '保存中…' : '保存'}
          </button>
        </div>
        {proxy.trim() && (
          <p className="desc">
            当前代理：<b className="mono">{proxy.trim()}</b>（将对新启动的安装/下载任务生效）
          </p>
        )}
      </div>

      <div className="settings-section">
        <h3>插件市场</h3>
        <p className="desc">
          插件目录源默认官方 awesome-dsh-plugin.com。网络受限时可填镜像地址，留空恢复官方。
        </p>
        <div className="row">
          <input
            type="text"
            value={marketUrl}
            onChange={(e) => setMarketUrl(e.target.value)}
            placeholder="https://your-mirror.example/plugins.json"
          />
          <button className="btn btn-accent" onClick={saveMarket} disabled={savingMarket}>
            {savingMarket ? '保存中…' : '保存'}
          </button>
        </div>
        <p className="desc">安装目标 profile：<b className="mono">{market?.profile || 'web'}</b>（所有实例共用）</p>
      </div>

      <div className="settings-section">
        <h3>界面布局</h3>
        <p className="desc">
          选择顶栏 / 菜单的布局风格。默认「自动」按当前系统：macOS 用 Mac 布局，Windows / Linux 用经典布局。
        </p>
        <div className="row">
          <select
            className="layout-select"
            value={layoutMode}
            onChange={(e) => setLayoutMode(e.target.value as LayoutMode)}
            title="界面布局风格"
          >
            <option value="">自动（按系统）</option>
            <option value="mac">Mac 布局</option>
            <option value="win">Windows / Linux 布局</option>
          </select>
          <button className="btn btn-accent" onClick={saveLayout} disabled={savingLayout}>
            {savingLayout ? '保存中…' : '保存'}
          </button>
        </div>
        <p className="desc">当前：<b className="mono">{layoutLabel(layoutMode)}</b>（保存后立即生效）</p>
      </div>

      <div className="settings-section">
        <h3>应用</h3>
        <p className="desc">配置文件与实例数据存放位置：</p>
        <div className="appdata-line">{appDataPath}</div>
      </div>
    </div>
  );
}

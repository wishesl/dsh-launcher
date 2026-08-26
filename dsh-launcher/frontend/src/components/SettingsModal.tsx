import { useCallback, useEffect, useRef, useState } from 'react';
import { api, errMsg } from '../api';
import type { EnvReport, MarketSettings, ToolStatus } from '../types';

interface Props {
  onClose: () => void;
  showToast: (msg: string, kind?: 'ok' | 'error') => void;
}

function ToolRow({
  tool,
  checking,
}: {
  tool: ToolStatus | undefined;
  checking: boolean;
}) {
  return (
    <div className="env-row">
      <div className="env-main">
        <div className="env-name">{tool?.name ?? '—'}</div>
        <div className="env-desc">
          {tool?.name === 'npm'
            ? 'Node.js 包管理器（DSH 运行 / 安装 pnpm 的基础）'
            : '高性能包管理器，「安装到目录」使用它安装 DSH'}
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

// Settings dialog. Currently hosts the prerequisite-environment section:
// one-click npm/pnpm version probe and a streamed "install pnpm" action.
export default function SettingsModal({ onClose, showToast }: Props) {
  const [env, setEnv] = useState<EnvReport | null>(null);
  const [checking, setChecking] = useState(true);
  const [installing, setInstalling] = useState(false);
  const [lines, setLines] = useState<string[]>([]);
  const outRef = useRef<HTMLPreElement>(null);
  const [market, setMarket] = useState<MarketSettings | null>(null);
  const [marketUrl, setMarketUrl] = useState('');
  const [savingMarket, setSavingMarket] = useState(false);

  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

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

  // Probe once when the dialog opens.
  useEffect(() => {
    check();
  }, [check]);

  // Load plugin-market settings (registry mirror) when the dialog opens.
  useEffect(() => {
    api.getMarketSettings().then((m) => {
      setMarket(m);
      setMarketUrl(m.registryUrl);
    }).catch(() => undefined);
  }, []);

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

  // Live output of the pnpm install.
  useEffect(() => {
    api.onEnvLog((e) => setLines((prev) => [...prev.slice(-400), e.line]));
    return () => api.offEnvLog();
  }, []);

  // Keep the output box pinned to the bottom while lines stream in.
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
      check(); // refresh the version badge
    }
  };

  return (
    <div className="modal-backdrop">
      {/* backdrop click intentionally does NOT close (same rule as the
          instance form): use ✕ / Esc */}
      <div className="modal settings-modal" role="dialog" aria-modal="true">
        <div className="modal-head">
          <h2>设置</h2>
          <button className="btn btn-ghost btn-sm" onClick={onClose}>✕</button>
        </div>

        <div className="form-body">
          <div className="field">
            <span className="field-label">前置环境</span>
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

          <div className="field">
            <span className="field-label">插件市场</span>
            <div className="field-hint">
              插件目录源（默认官方 <b>{market?.registryUrl === '' || market?.registryUrl?.startsWith('https://awesome-dsh-plugin.com') ? 'awesome-dsh-plugin.com' : '官方目录'}</b>）。
              网络受限时可填镜像地址，留空恢复官方。
            </div>
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
          </div>
        </div>

        <div className="modal-foot">
          <button className="btn btn-primary" onClick={onClose}>完成</button>
        </div>
      </div>
    </div>
  );
}

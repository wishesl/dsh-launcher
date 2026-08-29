import { useEffect, useRef, useState } from 'react';
import { api, errMsg } from '../api';
import type { DSHVersion, Instance, RegistryInfo } from '../types';
import { isValidVersion } from '../util';
import Switch from './Switch';

interface Props {
  registry: RegistryInfo | null;
  editing: Instance | null; // null => creating a new instance
  onClose: () => void;
  onSaved: (list: Instance[], note?: string) => void;
}

const DEFAULT_INSTANCE = (): Instance => ({
  id: '',
  name: '',
  directory: '',
  version: 'latest',
  localVersion: '',
  extraArgs: '',
  pkgMgr: 'local', // 推荐：目录内本地副本（官方设计，agent 可读真实源码）
  autoStart: false,
  selfRestart: false,
  createdAt: null,
  pid: 0,
  status: 'stopped',
});

const CUSTOM_OPTION = '__custom__';

// 源码启动的默认三命令：pnpm install → pnpm run build → pnpm dsh web。
const DEFAULT_SOURCE_INIT = 'pnpm install';
const DEFAULT_SOURCE_BUILD = 'pnpm run build';
const DEFAULT_SOURCE_START = 'pnpm dsh web';

export default function InstanceForm({ registry, editing, onClose, onSaved }: Props) {
  const [form, setForm] = useState<Instance>(() =>
    editing ? { ...DEFAULT_INSTANCE(), ...editing } : DEFAULT_INSTANCE()
  );
  // 实例类型：版本启动（现有行为） / 源码启动（目录内源码 + 自定义命令）。
  const [sourceMode, setSourceMode] = useState<boolean>(editing ? !!editing.source : false);
  const [versionMode, setVersionMode] = useState<'latest' | 'spec'>(
    !editing || editing.version === 'latest' ? 'latest' : 'spec'
  );
  // Local copy of the version list so the dropdown works even if the
  // App-level registry query failed or hasn't finished yet.
  const [versionList, setVersionList] = useState<DSHVersion[]>(registry?.versions ?? []);
  const [versionListLoading, setVersionListLoading] = useState(false);
  const [customMode, setCustomMode] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);
  const dirInputRef = useRef<HTMLInputElement>(null);
  const modalRef = useRef<HTMLDivElement>(null);

  // #9: directory existence probe (debounced) — warn about typos early.
  const [dirExists, setDirExists] = useState<boolean | null>(null);
  const dirProbeSeq = useRef(0);

  useEffect(() => {
    const dir = form.directory.trim();
    if (!dir) {
      setDirExists(null);
      return;
    }
    const seq = ++dirProbeSeq.current;
    const t = window.setTimeout(async () => {
      try {
        const ok = await api.directoryExists(dir);
        if (dirProbeSeq.current === seq) setDirExists(ok);
      } catch {
        if (dirProbeSeq.current === seq) setDirExists(null);
      }
    }, 350);
    return () => window.clearTimeout(t);
  }, [form.directory]);

  // #8: Esc closes the dialog; Enter submits.
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const set = (patch: Partial<Instance>) => setForm((f) => ({ ...f, ...patch }));

  // Keep local list in sync when the App-level registry updates.
  useEffect(() => {
    if (registry && registry.versions.length > 0) {
      setVersionList(registry.versions);
    }
  }, [registry]);

  // Auto-fetch the version list when switching to "指定版本" and we have none.
  useEffect(() => {
    if (versionMode === 'spec' && versionList.length === 0) {
      refreshVersionList();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [versionMode]);

  const refreshVersionList = async () => {
    setVersionListLoading(true);
    try {
      const info = await api.queryRegistry();
      if (info.versions.length > 0) setVersionList(info.versions);
      if (versionMode === 'spec') {
        setForm((f) => {
          if (f.version === 'latest' || f.version === '') {
            return { ...f, version: info.latest };
          }
          return f;
        });
      }
    } catch (e) {
      setError('获取版本列表失败: ' + errMsg(e));
    } finally {
      setVersionListLoading(false);
    }
  };

  const pickDir = async () => {
    try {
      const dir = await api.selectDirectory();
      if (!dir) return;
      set({ directory: dir });
      if (!form.name || form.name === form.directory) {
        set({ name: dir.split(/[\\/]/).pop() || dir });
      }
      setChecking(true);
      const local = await api.detectLocalVersion(dir);
      set({ localVersion: local });
      setChecking(false);
    } catch (e) {
      setChecking(false);
      setError(errMsg(e));
    }
  };

  const detectLocal = async () => {
    if (!form.directory) return;
    setChecking(true);
    try {
      const local = await api.detectLocalVersion(form.directory);
      set({ localVersion: local });
    } catch (e) {
      setError(errMsg(e));
    } finally {
      setChecking(false);
    }
  };

  useEffect(() => {
    if (dirInputRef.current) dirInputRef.current.focus();
  }, []);

  const selectVersion = (value: string) => {
    if (value === CUSTOM_OPTION) {
      setCustomMode(true);
      return;
    }
    setCustomMode(false);
    set({ version: value });
  };

  // #9: custom version must be a clean semver before it may be saved.
  const versionInvalid =
    versionMode === 'spec' &&
    form.version.trim() !== '' &&
    form.version !== 'latest' &&
    !isValidVersion(form.version);

  const save = async () => {
    if (!form.directory.trim()) {
      setError('请选择启动目录');
      return;
    }
    if (!sourceMode && versionInvalid) {
      setError('版本号格式不正确：应为 x.y.z 或 x.y.z-预发布（如 0.1.1-rc.2）');
      return;
    }
    setError(null);
    setSaving(true);
    try {
      const payload: Instance = { ...form, source: sourceMode };
      if (sourceMode) {
        // 源码模式不使用版本号；三命令留空时回退到默认值。
        payload.version = 'latest';
        payload.initCmd = (form.initCmd || '').trim() || DEFAULT_SOURCE_INIT;
        payload.buildCmd = (form.buildCmd || '').trim() || DEFAULT_SOURCE_BUILD;
        payload.startCmd = (form.startCmd || '').trim() || DEFAULT_SOURCE_START;
      }
      const finalVersion = sourceMode
        ? 'latest'
        : (versionMode === 'latest' ? 'latest' : (form.version.trim() || 'latest'));
      const list = await api.saveInstance({ ...payload, version: finalVersion });
      // #13: editing a live instance only takes effect on next start.
      const wasLive =
        editing && (editing.status === 'running' || editing.status === 'starting' || editing.status === 'ready');
      onSaved(list, wasLive ? '已保存。该实例正在运行，修改将在下次启动时生效。' : undefined);
      onClose();
    } catch (e) {
      setError(errMsg(e));
      setSaving(false);
    }
  };

  const showCustom = customMode || (form.version !== '' && form.version !== 'latest' && !versionList.some((v) => v.version === form.version));

  return (
    <div className="modal-backdrop" ref={modalRef}>
      {/* backdrop click intentionally does NOT close (#8): half-filled forms
          must not be lost to a stray click; use ✕ / 取消 / Esc */}
      <form
        className="modal"
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
      >
        <div className="modal-head">
          <h2>{editing ? '编辑实例' : '添加实例'}</h2>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose}>✕</button>
        </div>

        <div className="form-body">
          <div className="field">
            <span className="field-label">实例类型</span>
            <div className="radio-row">
              <label className={`radio-card ${!sourceMode ? 'selected' : ''}`}>
                <input
                  type="radio"
                  name="itype"
                  checked={!sourceMode}
                  onChange={() => setSourceMode(false)}
                />
                <span className="radio-title">版本启动</span>
                <span className="radio-sub">从 npm 安装 DSH 按版本启动</span>
              </label>
              <label className={`radio-card ${sourceMode ? 'selected' : ''}`}>
                <input
                  type="radio"
                  name="itype"
                  checked={sourceMode}
                  onChange={() => setSourceMode(true)}
                />
                <span className="radio-title">源码启动 <span className="tag-warn">新</span></span>
                <span className="radio-sub">目录内源码：pnpm install / build / dsh web</span>
              </label>
            </div>
          </div>

          <label className="field">
            <span className="field-label">名称</span>
            <input
              value={form.name}
              onChange={(e) => set({ name: e.target.value })}
              placeholder="实例名称（默认取目录名）"
            />
          </label>

          <label className="field">
            <span className="field-label">启动目录</span>
            <div className="row">
              <input
                ref={dirInputRef}
                value={form.directory}
                onChange={(e) => set({ directory: e.target.value })}
                placeholder="选择/输入 DSH 启动目录，如 D:\Users\<用户名>\Desktop"
              />
              <button type="button" className="btn btn-ghost" onClick={pickDir}>浏览…</button>
            </div>
            <div className="field-hint">
              {checking ? (
                <span>检测本地副本中…</span>
              ) : form.localVersion ? (
                <span>
                  本地已安装 DSH <b>{form.localVersion}</b>
                  <button type="button" className="link-btn" onClick={detectLocal}>重新检测</button>
                </span>
              ) : form.directory ? (
                sourceMode ? (
                  <span>
                    源码模式：不安装 DSH 副本，直接运行目录内源码（先点「安装到目录」执行初始化+构建）
                  </span>
                ) : (
                  <span>
                    该目录没有本地 DSH 副本（启动时将按版本从 npm 拉取）
                    <button type="button" className="link-btn" onClick={detectLocal}>检测</button>
                  </span>
                )
              ) : null}
            </div>
            {/* #9: existence warning */}
            {form.directory.trim() && dirExists === false && (
              <div className="field-warn">
                ⚠ 目录当前不存在：直接「启动」会失败；「安装到目录」会自动创建它。请确认路径没有手误。
              </div>
            )}
          </label>

          {!sourceMode && (
          <>
          <div className="field">
            <span className="field-label">DSH 版本</span>
            <div className="radio-row">
              <label className={`radio-card ${versionMode === 'latest' ? 'selected' : ''}`}>
                <input
                  type="radio"
                  name="vmode"
                  checked={versionMode === 'latest'}
                  onChange={() => setVersionMode('latest')}
                />
                <span className="radio-title">最新版</span>
                <span className="radio-sub">{registry ? registry.latest : '…'}</span>
              </label>
              <label className={`radio-card ${versionMode === 'spec' ? 'selected' : ''}`}>
                <input
                  type="radio"
                  name="vmode"
                  checked={versionMode === 'spec'}
                  onChange={() => setVersionMode('spec')}
                />
                <span className="radio-title">指定版本</span>
                <span className="radio-sub">从列表选择</span>
              </label>
            </div>

            {versionMode === 'spec' && (
              <div className="spec-version">
                {showCustom ? (
                  <div className="row">
                    <input
                      className={versionInvalid ? 'input-invalid' : ''}
                      value={form.version}
                      onChange={(e) => set({ version: e.target.value })}
                      placeholder="输入任意版本号，如 0.1.0-rc.6"
                    />
                    <button type="button" className="btn btn-ghost" onClick={() => { setCustomMode(false); set({ version: versionList[0]?.version ?? 'latest' }); }}>
                      取消
                    </button>
                  </div>
                ) : (
                  <div className="row">
                    <select
                      className="version-select"
                      value={form.version}
                      onChange={(e) => selectVersion(e.target.value)}
                      disabled={versionListLoading}
                    >
                      <option value="latest">latest — 最新版{registry?.latest ? `（${registry.latest}）` : ''}</option>
                      {versionList.map((v) => (
                        <option key={v.version} value={v.version}>
                          {v.version}{v.isLatest ? '（latest）' : ''}
                        </option>
                      ))}
                      <option value={CUSTOM_OPTION}>✏️ 自定义版本号…</option>
                    </select>
                    <button
                      type="button"
                      className="btn btn-ghost"
                      onClick={refreshVersionList}
                      title="刷新版本列表"
                    >
                      {versionListLoading ? '…' : '刷新'}
                    </button>
                  </div>
                )}
                {versionInvalid && (
                  <div className="field-warn">
                    版本号应为 x.y.z 或 x.y.z-预发布（如 0.1.1-rc.2），否则无法保存。
                  </div>
                )}
                <div className="field-hint">
                  {versionListLoading
                    ? '正在从 npm registry 获取版本列表…'
                    : versionList.length > 0
                    ? `共 ${versionList.length} 个版本，最新 ${versionList[0].version}（${versionList[0].published ? new Date(versionList[0].published).toLocaleDateString() : ''} 发布）`
                    : '未获取到版本列表，点击「刷新」重试'}
                </div>
              </div>
            )}
          </div>

          <div className="field">
            <span className="field-label">启动方式</span>
            <div className="radio-row radio-row-3">
              <label className={`radio-card ${form.pkgMgr === 'local' ? 'selected' : ''}`}>
                <input
                  type="radio"
                  name="pkgmgr"
                  checked={form.pkgMgr === 'local'}
                  onChange={() => set({ pkgMgr: 'local' })}
                />
                <span className="radio-title">本地副本 <span className="tag-warn">推荐</span></span>
                <span className="radio-sub">官方设计：目录内真实源码可读</span>
              </label>
              <label className={`radio-card ${form.pkgMgr === 'pnpm' ? 'selected' : ''}`}>
                <input
                  type="radio"
                  name="pkgmgr"
                  checked={form.pkgMgr === 'pnpm'}
                  onChange={() => set({ pkgMgr: 'pnpm' })}
                />
                <span className="radio-title">pnpm dlx</span>
                <span className="radio-sub">从 pnpm 缓存拉取，不占目录</span>
              </label>
              <label className={`radio-card ${form.pkgMgr === 'npx' ? 'selected' : ''}`}>
                <input
                  type="radio"
                  name="pkgmgr"
                  checked={form.pkgMgr === 'npx'}
                  onChange={() => set({ pkgMgr: 'npx' })}
                />
                <span className="radio-title">npx</span>
                <span className="radio-sub">标准方式（本机 npm 安装可能卡住）</span>
              </label>
            </div>
            <div className="field-hint">
              {form.pkgMgr === 'local'
                ? '用目录内 node_modules 的 DSH 源码启动（agent 可读码开发插件）。选它后可点实例卡片的「安装到目录」把版本装进来。'
                : form.pkgMgr === 'npx'
                ? 'npx -y 从 registry 拉取。'
                : '首次启动会下载 DSH（180+ 子包），约 1-2 分钟，之后走缓存秒启。'}
            </div>
          </div>

          <div className="field">
            <span className="field-label">附加参数 <span className="muted">（可选）</span></span>
            <input
              value={form.extraArgs}
              onChange={(e) => set({ extraArgs: e.target.value })}
              placeholder="如 --port 3081（将追加到 dsh web 之后）"
            />
            <div className="field-hint">
              最终命令示例：
              <code className="mono">{form.pkgMgr === 'local' ? 'npx @deepseek-ai/dsh' : (form.pkgMgr === 'npx' ? 'npx -y' : 'pnpm dlx')} {form.pkgMgr === 'local' ? '' : '@deepseek-ai/dsh@' + (versionMode === 'latest' ? 'latest' : (form.version || '…'))} web{form.extraArgs ? ' ' + form.extraArgs : ''}</code>
            </div>
          </div>
          </>
          )}

          {/* 源码启动：初始化 / 构建 / 启动命令（默认 pnpm install / pnpm run build / pnpm dsh web） */}
          {sourceMode && (
            <>
              <label className="field">
                <span className="field-label">初始化命令 <span className="muted">（「安装到目录」第一步）</span></span>
                <input
                  value={form.initCmd || DEFAULT_SOURCE_INIT}
                  onChange={(e) => set({ initCmd: e.target.value })}
                  placeholder={DEFAULT_SOURCE_INIT}
                />
              </label>
              <label className="field">
                <span className="field-label">构建命令 <span className="muted">（「安装到目录」第二步）</span></span>
                <input
                  value={form.buildCmd || DEFAULT_SOURCE_BUILD}
                  onChange={(e) => set({ buildCmd: e.target.value })}
                  placeholder={DEFAULT_SOURCE_BUILD}
                />
              </label>
              <label className="field">
                <span className="field-label">启动命令</span>
                <input
                  value={form.startCmd || DEFAULT_SOURCE_START}
                  onChange={(e) => set({ startCmd: e.target.value })}
                  placeholder={DEFAULT_SOURCE_START}
                />
                <div className="field-hint">
                  点击实例卡片的「启动」直接执行该命令（默认 <code className="mono">{DEFAULT_SOURCE_START}</code>），可自行修改。
                </div>
              </label>
            </>
          )}

          <div className="form-autostart">
            <Switch
              checked={form.autoStart}
              onChange={(v) => set({ autoStart: v })}
            />
            <span>
              随启动器自动启动此实例
              <span className="muted">（打开 DSH Launcher 时自动拉起）</span>
            </span>
          </div>

          <div className="form-autostart">
            <Switch
              checked={!!form.selfRestart}
              onChange={(v) => set({ selfRestart: v })}
            />
            <span>
              启用自管理重启（dsh-restart）
              <span className="muted">（需已在全局安装 dsh-self-mcp 插件；启动时挂载 dsh-restart 工具，重启后自动注入「重启完成」并继续）</span>
            </span>
          </div>

          {error && <div className="form-error">{error}</div>}
        </div>

        <div className="modal-foot">
          <button type="button" className="btn btn-ghost" onClick={onClose}>取消</button>
          <button type="submit" className="btn btn-primary" disabled={saving || (!sourceMode && versionInvalid)}>
            {saving ? '保存中…' : editing ? '保存修改' : '添加并保存'}
          </button>
        </div>
      </form>
    </div>
  );
}

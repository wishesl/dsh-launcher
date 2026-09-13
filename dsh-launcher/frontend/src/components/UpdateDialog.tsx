import { useEffect, useRef, useState } from 'react';
import { Download, ExternalLink, RefreshCw, SkipForward, X } from 'lucide-react';
import type { LauncherRelease, UpdateOpState, UpdateSettings } from '../types';

interface Props {
  open: boolean;
  info: LauncherRelease | null;
  checking: boolean;
  op: UpdateOpState;
  /** 弹窗内的完整升级日志（对 AGENTS.md §0.1 的显式例外，见方案 §6.3）。 */
  logs: string[];
  settings: UpdateSettings;
  /** 正在运行的 DSH 实例数：「立即重启」的二次确认要说清代价。 */
  runningInstances: number;
  onClose: () => void;
  onCheck: () => void;
  onStart: () => void;
  onCancel: () => void;
  onRestart: () => void;
  onOpenRelease: () => void;
  onSkip: () => void;
  onClearLogs: () => void;
  onSettings: (s: UpdateSettings) => void;
}

const PHASE_LABEL: Record<string, string> = {
  check: '检查更新',
  download: '下载中',
  verify: '校验中',
  apply: '安装中',
};

function humanSize(n: number): string {
  if (!n || n <= 0) return '';
  const mb = 1024 * 1024;
  return n >= mb ? `${(n / mb).toFixed(1)} MB` : `${Math.round(n / 1024)} KB`;
}

function fmtTime(iso: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString();
}

const versionLabel = (v: string) => (!v ? '未知' : /^\d/.test(v) ? `v${v}` : v);

/**
 * 启动器自更新弹窗：点顶栏版本 pill / 设置页「检查更新」/ 托盘「检查更新」都会打开这里。
 *
 * ⚠️ 关弹窗 ≠ 取消任务（与 InstanceForm 的既有语义一致）：下载在后台继续跑，重开还能看日志；
 * 停只能点「取消下载」。进度与日志都留在这个弹窗里 —— 这是 AGENTS.md §0.1 的唯一显式例外。
 */
export default function UpdateDialog({
  open,
  info,
  checking,
  op,
  logs,
  settings,
  runningInstances,
  onClose,
  onCheck,
  onStart,
  onCancel,
  onRestart,
  onOpenRelease,
  onSkip,
  onClearLogs,
  onSettings,
}: Props) {
  const logRef = useRef<HTMLDivElement>(null);
  const [restartAsk, setRestartAsk] = useState(false);

  // 日志跟着下载滚动到底（只在开着的时候动，避免无谓布局）。
  useEffect(() => {
    if (!open) return;
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [logs, open]);

  if (!open) return null;

  const hasNew = !!info?.hasUpdate && !info?.skipped;
  const busy = op.running;
  // 三态（对齐 AGENTS.md §9.2：没有结论 ≠ 失败）
  const noConclusion = !!info?.err;
  const devBuild = !!info && !info.comparable && !info.err;
  const upToDate = !!info && info.comparable && !info.hasUpdate && !info.err;
  const unsupported = !!info?.hasUpdate && !info.err && !info.supported;
  const canInstall = hasNew && !!info?.supported && !busy;

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal update-modal" role="dialog" aria-label="DSH Launcher 更新">
        <div className="modal-head">
          <h2>DSH Launcher 更新</h2>
          <button className="btn btn-ghost btn-icon" onClick={onClose} title="关闭（下载不会中断）">
            <X size={16} strokeWidth={1.75} aria-hidden />
          </button>
        </div>

        <div className="form-body">
          <div className="update-ver-row">
            <span className="update-ver-cur" title="当前运行的启动器版本">
              {info ? versionLabel(info.current) : '—'}
            </span>
            <span className="update-arrow">→</span>
            <span className={`update-ver-new ${hasNew ? 'is-new' : ''}`}>
              {info ? versionLabel(info.latest) : '—'}
            </span>
            {hasNew && <span className="update-badge new">有新版本</span>}
            {!!info?.skipped && <span className="update-badge muted">已跳过此版本</span>}
            {upToDate && <span className="update-badge ok">已是最新</span>}
            {devBuild && <span className="update-badge muted">本地构建 · 跳过比较</span>}
            {noConclusion && <span className="update-badge muted">暂时查不到</span>}
          </div>

          <div className="update-meta">
            {info?.publishedAt && <span>发布于 {fmtTime(info.publishedAt)}</span>}
            {info?.cached && <span>· 缓存结果</span>}
            {checking && <span>· 检查中…</span>}
            <button className="link-btn" onClick={onCheck} disabled={checking}>
              <RefreshCw size={12} strokeWidth={1.75} aria-hidden /> 重新检查
            </button>
            <button className="link-btn" onClick={onOpenRelease}>
              <ExternalLink size={12} strokeWidth={1.75} aria-hidden /> 打开 Release 页
            </button>
          </div>

          {noConclusion && (
            <p className="field-hint">
              {info?.err} —— 这不影响启动器其它功能，可稍后重试或手动下载。
            </p>
          )}
          {!noConclusion && unsupported && (
            <p className="field-hint">
              {info?.supportNote || '当前平台暂不支持应用内更新，请用「打开 Release 页」手动下载。'}
            </p>
          )}
          {devBuild && (
            <p className="field-hint">
              本地构建（没有注入版本号）不参与版本比较 —— 自动更新只对正式 Release 生效。
            </p>
          )}
          {!info && !checking && <p className="field-hint">还没有检查过更新。</p>}

          {info?.notes && (
            <div className="update-notes-wrap">
              <div className="field-label">更新说明</div>
              <pre className="update-notes">{info.notes}</pre>
            </div>
          )}

          {busy && (
            <div className="update-progress">
              <div className="update-progress-head">
                <span className="spin" />
                <span>{PHASE_LABEL[op.phase] || '处理中'}…</span>
                {op.phase === 'download' && op.total > 0 && (
                  <span className="mono">
                    {op.percent}% · {humanSize(op.bytes)} / {humanSize(op.total)}
                  </span>
                )}
              </div>
              <div className="update-bar">
                <span style={{ width: `${Math.max(2, Math.min(100, op.percent))}%` }} />
              </div>
            </div>
          )}

          {!!op.appliedVersion && !busy && (
            <div className="update-done">
              已就位：<b className="mono">v{op.appliedVersion}</b> —— 退出启动器后，下次打开即生效。
            </div>
          )}
          {!busy && op.state === 'failed' && op.error && <p className="update-error">{op.error}</p>}

          <div className="update-log-wrap">
            <div className="update-log-head">
              <span className="field-label">升级日志</span>
              <div className="row">
                <button
                  className="btn btn-ghost btn-sm"
                  onClick={() => {
                    void navigator.clipboard?.writeText(logs.join('\n'));
                  }}
                  disabled={logs.length === 0}
                >
                  复制
                </button>
                <button className="btn btn-ghost btn-sm" onClick={onClearLogs} disabled={logs.length === 0}>
                  清空
                </button>
              </div>
            </div>
            <div className="update-log" ref={logRef}>
              {logs.length === 0 ? (
                <span className="muted">下载 / 校验 / 安装的输出会显示在这里。</span>
              ) : (
                logs.map((l, i) => <div key={i}>{l}</div>)
              )}
            </div>
          </div>

          <label className="update-toggle">
            <input
              type="checkbox"
              checked={settings.autoCheck}
              onChange={(e) => onSettings({ ...settings, autoCheck: e.target.checked })}
            />
            启动后自动检查更新
          </label>
          <label className="update-toggle">
            <input
              type="checkbox"
              checked={settings.includePrerelease}
              onChange={(e) => onSettings({ ...settings, includePrerelease: e.target.checked })}
            />
            包含预发布版本（rc / beta）
          </label>

          {restartAsk && (
            <div className="update-restart-ask">
              <p>
                立即重启会先退出启动器：正在运行的 {runningInstances} 个 DSH 实例会被停止
                （勾了「随启动器自动启动」的会在新版本起来后自动拉起）。
              </p>
              <div className="row">
                <button
                  className="btn btn-danger btn-sm"
                  onClick={() => {
                    setRestartAsk(false);
                    onRestart();
                  }}
                >
                  确认重启
                </button>
                <button className="btn btn-ghost btn-sm" onClick={() => setRestartAsk(false)}>
                  取消
                </button>
              </div>
            </div>
          )}
        </div>

        <div className="modal-foot">
          <button className="btn btn-ghost" onClick={onClose}>
            关闭
          </button>
          {hasNew && (
            <button className="btn btn-ghost" onClick={onSkip} disabled={busy} title="不再提示这个版本">
              <SkipForward size={14} strokeWidth={1.75} aria-hidden /> 跳过此版本
            </button>
          )}
          {busy && (
            <button className="btn btn-ghost" onClick={onCancel}>
              取消下载
            </button>
          )}
          {!!op.appliedVersion && !busy && !restartAsk && (
            <button className="btn btn-accent" onClick={() => setRestartAsk(true)}>
              立即重启生效
            </button>
          )}
          {hasNew && (
            <button
              className="btn btn-primary"
              onClick={onStart}
              disabled={!canInstall}
              title={info?.supported ? '下载并替换启动器（当前进程继续运行）' : '本平台不支持应用内更新'}
            >
              <Download size={14} strokeWidth={1.75} aria-hidden />{' '}
              {op.appliedVersion ? '重新下载' : '下载并安装'}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

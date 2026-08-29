import { useEffect, useState } from 'react';
import { api, errMsg } from '../api';
import type { Instance, InstalledPlugin } from '../types';

interface Props {
  instance: Instance;
  showToast: (msg: string, kind?: 'ok' | 'error') => void;
  onClose: () => void;
}

// Modal (same .modal pattern as InstanceForm): pick which installed plugins
// this instance must NOT load. Masking is temporary — the launcher passes a
// --patch overlay at launch, so the global enable/disable switch in the market
// is never changed and the mask disappears when the instance stops. Plugins
// that were uninstalled are neither listed nor masked (backend prunes them).
export default function MaskPluginsDialog({ instance, showToast, onClose }: Props) {
  const [plugins, setPlugins] = useState<InstalledPlugin[]>([]);
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const [list, masked] = await Promise.all([
          api.listInstalledPlugins(),
          api.getInstanceMasks(instance.id),
        ]);
        if (!alive) return;
        setPlugins(list ?? []);
        setChecked(new Set(masked ?? []));
      } catch (e) {
        if (alive) setError(errMsg(e));
      } finally {
        if (alive) setLoading(false);
      }
    })();
    return () => {
      alive = false;
    };
  }, [instance.id]);

  const toggle = (name: string) => {
    setChecked((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  };

  const save = async () => {
    setSaving(true);
    try {
      await api.setInstanceMasks(instance.id, [...checked]);
      showToast(
        checked.size === 0
          ? '已清除该实例的插件屏蔽'
          : `已屏蔽 ${checked.size} 个插件（仅该实例启动时生效）`
      );
      onClose();
    } catch (e) {
      setError(errMsg(e));
      setSaving(false);
    }
  };

  return (
    <div className="modal-backdrop">
      <div className="modal mask-modal">
        <div className="modal-head">
          <h2>屏蔽插件 · {instance.name}</h2>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose}>✕</button>
        </div>

        <div className="form-body">
          <p className="mask-hint">
            勾选后，该实例<strong>启动时</strong>将临时禁用这些插件：仅在本次启动生效（--patch
            覆盖层），<strong>不改全局开关</strong>，实例停止后自动恢复。已卸载的插件不会显示、也不会屏蔽。
          </p>
          {error && <div className="form-error">{error}</div>}
          {loading ? (
            <p className="muted">加载已安装插件…</p>
          ) : plugins.length === 0 ? (
            <div className="empty"><p>暂无已安装的社区插件</p></div>
          ) : (
            <div className="mask-list">
              {plugins.map((p) => (
                <label key={p.name} className="field-check mask-row">
                  <input type="checkbox" checked={checked.has(p.name)} onChange={() => toggle(p.name)} />
                  <span className="mask-name">{p.name}</span>
                  <span className="muted mono">v{p.version || '—'}</span>
                </label>
              ))}
            </div>
          )}
        </div>

        <div className="modal-foot">
          <button type="button" className="btn btn-ghost" onClick={onClose}>取消</button>
          <button type="button" className="btn btn-primary" disabled={saving || loading} onClick={save}>
            {saving ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </div>
  );
}

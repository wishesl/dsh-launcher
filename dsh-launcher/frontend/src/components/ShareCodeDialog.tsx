import { useState } from 'react';
import { api, errMsg } from '../api';
import { githubRepoOf } from '../util';
import type { FavoritePlugin, ShareImportResult } from '../types';

interface Props {
  mode: 'gen' | 'import';
  favorites: FavoritePlugin[];   // gen: the collection to pick a subset from
  showToast: (msg: string, kind?: 'ok' | 'error') => void;
  onImported: () => void;        // import succeeded → refresh favorites + close
  onClose: () => void;
}

// Modal for the two share-code flows, reusing the .modal-backdrop/.modal
// pattern (same as InstanceForm):
//  - gen:    checkbox list to pick which favorites to share → 生成 → show code
//  - import: paste → 解析 → preview list (checkbox per plugin) → 添加收藏
// Nothing is added to favorites automatically on parse — the user confirms.
export default function ShareCodeDialog({ mode, favorites, showToast, onImported, onClose }: Props) {
  // Only favorites carrying a GitHub URL can be shared (the share code must
  // stay re-findable by repo); the rest are shown greyed-out and excluded.
  const shareable = favorites.filter((f) => githubRepoOf(f.url));
  const [picked, setPicked] = useState<Set<string>>(() => new Set(shareable.map((f) => f.id)));
  const [code, setCode] = useState('');
  const [generating, setGenerating] = useState(false);
  const [text, setText] = useState('');
  const [preview, setPreview] = useState<ShareImportResult | null>(null);
  const [pickedImport, setPickedImport] = useState<Set<string>>(new Set());
  const [parsing, setParsing] = useState(false);
  const [importing, setImporting] = useState(false);

  const togglePicked = (id: string) => {
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const gen = async () => {
    if (picked.size === 0) {
      showToast('请至少勾选一个收藏', 'error');
      return;
    }
    setGenerating(true);
    try {
      setCode(await api.generateShareCode([...picked]));
    } catch (e) {
      showToast('生成分享码失败: ' + errMsg(e), 'error');
    } finally {
      setGenerating(false);
    }
  };

  const copy = async () => {
    if (!code) return;
    try {
      await navigator.clipboard.writeText(code);
      showToast('分享码已复制');
    } catch (e) {
      showToast('复制失败，请手动选择复制: ' + errMsg(e), 'error');
    }
  };

  const parse = async () => {
    const c = text.trim();
    if (!c) {
      showToast('请先粘贴分享码', 'error');
      return;
    }
    setParsing(true);
    try {
      const res = await api.parseShareCode(c);
      const imported = res.imported ?? [];
      const skipped = res.skipped ?? [];
      setPreview({ imported, skipped });
      setPickedImport(new Set(imported.map((p) => p.id)));
      if (imported.length === 0) {
        showToast(skipped.length > 0 ? '分享码中的插件都已收藏过' : '分享码中没有可添加的插件', 'error');
      }
    } catch (e) {
      showToast('解析失败: ' + errMsg(e), 'error');
    } finally {
      setParsing(false);
    }
  };

  const toggleImport = (id: string) => {
    setPickedImport((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const confirmImport = async () => {
    if (!preview) return;
    if (pickedImport.size === 0) {
      showToast('请至少勾选一个插件', 'error');
      return;
    }
    setImporting(true);
    try {
      const res = await api.importShareCode(text.trim(), [...pickedImport]);
      showToast(`已添加 ${(res.imported ?? []).length} 个收藏`);
      onImported();
    } catch (e) {
      showToast('导入失败: ' + errMsg(e), 'error');
    } finally {
      setImporting(false);
    }
  };

  return (
    <div className="modal-backdrop" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div className="modal share-dialog">
        <div className="modal-head">
          <h2>{mode === 'gen' ? '生成收藏分享码' : '解析收藏分享码'}</h2>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} title="关闭">✕</button>
        </div>

        {mode === 'gen' ? (
          <>
            <div className="form-body">
              {favorites.length === 0 ? (
                <p className="muted">暂无收藏，先去「发现」或「已安装」页收藏插件吧。</p>
              ) : code ? (
                <>
                  <p className="field-hint">已生成 {picked.size} 个收藏的分享码（DSH-FAV:v1:…）：</p>
                  <textarea
                    className="share-code"
                    readOnly
                    rows={5}
                    value={code}
                    onFocus={(e) => e.currentTarget.select()}
                  />
                </>
              ) : (
                <>
                  <div className="row" style={{ marginBottom: 8 }}>
                    <span className="field-label">勾选要分享的收藏（{picked.size}/{shareable.length}）</span>
                    <span style={{ marginLeft: 'auto' }}>
                      <button type="button" className="btn btn-ghost btn-sm" onClick={() => setPicked(new Set(shareable.map((f) => f.id)))}>全选</button>
                      <button type="button" className="btn btn-ghost btn-sm" onClick={() => setPicked(new Set())}>清空</button>
                    </span>
                  </div>
                  <p className="field-hint" style={{ marginTop: -2 }}>
                    无 GitHub 地址的收藏不可分享（已置灰）
                  </p>
                  <div className="share-list">
                    {favorites.map((f) => {
                      const ok = githubRepoOf(f.url) != null;
                      return (
                        <label
                          key={f.id}
                          className={`share-row ${picked.has(f.id) ? 'checked' : ''} ${ok ? '' : 'disabled'}`}
                          title={ok ? undefined : '无 GitHub 地址，不可分享'}
                        >
                          <input type="checkbox" checked={picked.has(f.id)} disabled={!ok} onChange={() => togglePicked(f.id)} />
                          <span className="share-row-main">
                            <span className="share-row-name">{f.name}</span>
                            <span className="share-row-sub">
                              {ok ? (f.npm || f.url) : '无 GitHub 地址，不可分享'}
                            </span>
                          </span>
                        </label>
                      );
                    })}
                  </div>
                </>
              )}
            </div>
            <div className="modal-foot">
              <button type="button" className="btn btn-ghost" onClick={() => (code ? setCode('') : onClose())}>
                {code ? '返回选择' : '取消'}
              </button>
              {!code && (
                <button type="button" className="btn btn-accent" disabled={generating || picked.size === 0} onClick={gen}>
                  {generating ? '生成中…' : '生成分享码'}
                </button>
              )}
              {code && (
                <>
                  <button type="button" className="btn btn-accent" onClick={copy}>复制</button>
                  <button type="button" className="btn btn-primary" onClick={onClose}>完成</button>
                </>
              )}
            </div>
          </>
        ) : (
          <>
            <div className="form-body">
              {!preview ? (
                <>
                  <p className="field-hint">粘贴 DSH-FAV:v1:… 开头的分享码，点击「解析」预览后由你勾选确认收藏。</p>
                  <textarea
                    className="share-code"
                    rows={4}
                    placeholder="粘贴分享码…"
                    value={text}
                    onChange={(e) => { setText(e.target.value); setPreview(null); }}
                  />
                </>
              ) : (
                <>
                  <div className="row" style={{ marginBottom: 8 }}>
                    <span className="field-label">
                      可添加 {preview.imported.length} 个
                      {preview.skipped.length > 0 ? `，已存在跳过 ${preview.skipped.length} 个` : ''}
                    </span>
                    {preview.imported.length > 0 && (
                      <span style={{ marginLeft: 'auto' }}>
                        <button
                          type="button"
                          className="btn btn-ghost btn-sm"
                          onClick={() => setPickedImport(new Set(preview.imported.map((p) => p.id)))}
                        >
                          全选
                        </button>
                      </span>
                    )}
                  </div>
                  <div className="share-list">
                    {preview.imported.map((p) => (
                      <label key={p.id} className={`share-row ${pickedImport.has(p.id) ? 'checked' : ''}`}>
                        <input type="checkbox" checked={pickedImport.has(p.id)} onChange={() => toggleImport(p.id)} />
                        <span className="share-row-main">
                          <span className="share-row-name">{p.name}</span>
                          <span className="share-row-sub">{p.npm || p.install}</span>
                        </span>
                      </label>
                    ))}
                    {preview.imported.length === 0 && preview.skipped.length > 0 && (
                      <p className="muted">分享码中的插件都已在收藏中。</p>
                    )}
                  </div>
                </>
              )}
            </div>
            <div className="modal-foot">
              <button type="button" className="btn btn-ghost" onClick={onClose}>{preview ? '取消' : '关闭'}</button>
              {!preview ? (
                <button type="button" className="btn btn-accent" disabled={parsing} onClick={parse}>
                  {parsing ? '解析中…' : '解析'}
                </button>
              ) : (
                <button
                  type="button"
                  className="btn btn-accent"
                  disabled={importing || pickedImport.size === 0}
                  onClick={confirmImport}
                >
                  {importing ? '添加中…' : `添加收藏 (${pickedImport.size})`}
                </button>
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

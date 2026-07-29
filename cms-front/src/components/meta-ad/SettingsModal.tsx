import React, { useState, useEffect } from 'react';
import { createPortal } from 'react-dom';
import type { ConfigData } from '../../types/meta';
import { X, Key, Building2, RefreshCw, CheckCircle2, ShieldCheck, Calendar, Eye, EyeOff, Trash2 } from 'lucide-react';

interface Props {
  isOpen: boolean;
  onClose: () => void;
  onSave: (token: string, bmID: string, apiVersion: string) => Promise<void>;
  onTriggerSync: (preset: string) => Promise<void>;
  onPurge: () => Promise<void>;
  config: ConfigData | null;
}

export const SettingsModal: React.FC<Props> = ({
  isOpen,
  onClose,
  onSave,
  onTriggerSync,
  onPurge,
  config,
}) => {
  const [token, setToken] = useState('');
  const [bmID, setBmID] = useState('');
  const [apiVersion, setApiVersion] = useState('v25.0');
  const [showToken, setShowToken] = useState(false);
  const [syncPreset, setSyncPreset] = useState('last_7d');
  const [saving, setSaving] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [purging, setPurging] = useState(false);
  const [errorMsg, setErrorMsg] = useState('');

  useEffect(() => {
    if (config) {
      setToken(config.meta_access_token);
      setBmID(config.meta_business_id);
      if (config.meta_api_version) {
        setApiVersion(config.meta_api_version);
      }
    }
  }, [config]);

  if (!isOpen) return null;

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setErrorMsg('');
    try {
      await onSave(token, bmID, apiVersion || 'v25.0');
      onClose();
    } catch (err: any) {
      setErrorMsg(err.message || '保存配置失败');
    } finally {
      setSaving(false);
    }
  };

  const handleSync = async () => {
    setSyncing(true);
    setErrorMsg('');
    try {
      await onTriggerSync(syncPreset);
    } catch (err: any) {
      let msg = err.message || '同步数据失败';
      if (msg.includes('190') || msg.includes('expired')) {
        msg = 'Meta Access Token 已失效/过期 (OAuth Error 190)！请粘贴最新的有效 Access Token 后重新保存。';
      }
      setErrorMsg(msg);
    } finally {
      setSyncing(false);
    }
  };

  const handlePurge = async () => {
    if (!window.confirm('警告：确定要清空数据库中所有 Meta 广告数据吗？此操作无法撤销。')) {
      return;
    }
    setPurging(true);
    setErrorMsg('');
    try {
      await onPurge();
      onClose();
    } catch (err: any) {
      setErrorMsg(err.message || '清空数据失败');
    } finally {
      setPurging(false);
    }
  };

  return createPortal(
    <div
      style={{
        position: 'fixed',
        top: 0,
        left: 0,
        right: 0,
        bottom: 0,
        backgroundColor: 'rgba(15, 23, 42, 0.4)',
        backdropFilter: 'blur(4px)',
        WebkitBackdropFilter: 'blur(4px)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1000,
        padding: '20px',
      }}
      onClick={onClose}
    >
      <div
        className="glass-panel animate-fade-in"
        style={{
          width: '100%',
          maxWidth: '520px',
          backgroundColor: 'hsl(var(--bg-surface))',
          border: '1px solid hsl(var(--border))',
          borderRadius: '16px',
          padding: '24px',
          position: 'relative',
          boxShadow: 'var(--shadow-lg)',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <button
          onClick={onClose}
          style={{ position: 'absolute', right: '16px', top: '16px', background: 'none', border: 'none', cursor: 'pointer', color: 'hsl(var(--text-muted))', padding: '4px', display: 'flex' }}
        >
          <X size={20} />
        </button>

        <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '20px' }}>
          <div style={{ padding: '10px', backgroundColor: 'hsl(var(--primary) / 0.1)', color: 'hsl(var(--primary))', borderRadius: '10px', display: 'flex' }}>
            <ShieldCheck size={24} />
          </div>
          <div>
            <h3 style={{ fontSize: '1.125rem', fontWeight: 700, margin: 0, color: 'hsl(var(--text-primary))' }}>Meta API & 数据同步配置</h3>
            <p style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', margin: '2px 0 0 0' }}>设置 Meta 访问令牌或手动选择范围同步数据</p>
          </div>
        </div>

        {errorMsg && (
          <div style={{ marginBottom: '16px', padding: '12px', backgroundColor: 'hsl(var(--accent-pink) / 0.1)', border: '1px solid hsl(var(--accent-pink) / 0.3)', color: 'hsl(var(--accent-pink))', borderRadius: '8px', fontSize: '0.75rem' }}>
            <strong>⚠️ 提醒：</strong> {errorMsg}
          </div>
        )}

        {/* 手动同步控制区 */}
        <div style={{ marginBottom: '20px', padding: '14px 16px', borderRadius: '10px', backgroundColor: 'hsl(var(--bg-base))', border: '1px solid hsl(var(--border))' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: '12px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))' }}>
              <Calendar size={14} style={{ color: 'hsl(var(--primary))' }} />
              <span>同步范围:</span>
              <select
                value={syncPreset}
                onChange={(e) => setSyncPreset(e.target.value)}
                className="custom-select"
                style={{ height: '32px', fontSize: '0.75rem', padding: '0 28px 0 8px' }}
              >
                <option value="last_1d">近 1 天数据</option>
                <option value="last_3d">近 3 天数据</option>
                <option value="last_7d">近 7 天数据</option>
                <option value="last_14d">近 14 天数据</option>
                <option value="last_30d">近 30 天数据</option>
              </select>
            </div>

            <button
              onClick={handleSync}
              disabled={syncing}
              className="btn-primary"
              style={{ height: '32px', fontSize: '0.75rem', padding: '0 12px', gap: '6px' }}
            >
              <RefreshCw size={14} className={syncing ? 'animate-spin' : ''} />
              {syncing ? '同步中...' : '手动同步'}
            </button>
          </div>
        </div>

        <form onSubmit={handleSave} style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
          <div>
            <label style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.75rem', fontWeight: 600, color: 'hsl(var(--text-primary))', marginBottom: '6px' }}>
              <Key size={14} style={{ color: 'hsl(var(--primary))' }} />
              Meta Access Token (保存将触发同步)
            </label>
            <div style={{ position: 'relative' }}>
              <input
                type={showToken ? 'text' : 'password'}
                placeholder="EAAY..."
                value={token}
                onChange={(e) => setToken(e.target.value)}
                className="input-field"
                style={{ paddingRight: '36px' }}
              />
              <button
                type="button"
                onClick={() => setShowToken(!showToken)}
                style={{ position: 'absolute', right: '10px', top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', cursor: 'pointer', color: 'hsl(var(--text-muted))', padding: '2px', display: 'flex' }}
                title={showToken ? '隐藏 Token' : '显示 Token'}
              >
                {showToken ? <EyeOff size={15} /> : <Eye size={15} />}
              </button>
            </div>
          </div>

          <div>
            <label style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.75rem', fontWeight: 600, color: 'hsl(var(--text-primary))', marginBottom: '6px' }}>
              <Building2 size={14} style={{ color: '#a855f7' }} />
              Business Manager ID (BM ID / 广告账户 ID)
            </label>
            <input
              type="text"
              placeholder="bm_123456789 或 act_123456789"
              value={bmID}
              onChange={(e) => setBmID(e.target.value)}
              className="input-field"
            />
          </div>

          <div>
            <label style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.75rem', fontWeight: 600, color: 'hsl(var(--text-primary))', marginBottom: '6px' }}>
              <Calendar size={14} style={{ color: 'hsl(var(--accent-green))' }} />
              Meta API / CAPI 版本 (默认 v25.0)
            </label>
            <input
              type="text"
              placeholder="v25.0"
              value={apiVersion}
              onChange={(e) => setApiVersion(e.target.value)}
              className="input-field"
            />
          </div>

          <div style={{ paddingTop: '16px', display: 'flex', justifyContent: 'space-between', alignItems: 'center', borderTop: '1px solid hsl(var(--border))', marginTop: '8px' }}>
            <button
              type="button"
              onClick={handlePurge}
              disabled={purging}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'hsl(var(--accent-pink))', fontSize: '0.75rem', fontWeight: 500, display: 'flex', alignItems: 'center', gap: '6px', padding: '6px 10px', borderRadius: '6px' }}
            >
              <Trash2 size={14} />
              {purging ? '清空中...' : '清空历史数据'}
            </button>

            <div style={{ display: 'flex', gap: '10px' }}>
              <button
                type="button"
                onClick={onClose}
                className="btn-secondary"
                style={{ height: '36px', fontSize: '0.75rem', padding: '0 16px' }}
              >
                取消
              </button>
              <button
                type="submit"
                disabled={saving}
                className="btn-primary"
                style={{ height: '36px', fontSize: '0.75rem', padding: '0 16px', gap: '6px' }}
              >
                {saving ? (
                  <>
                    <RefreshCw size={14} className="animate-spin" />
                    保存中...
                  </>
                ) : (
                  <>
                    <CheckCircle2 size={14} />
                    保存配置并同步
                  </>
                )}
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>,
    document.body
  );
};

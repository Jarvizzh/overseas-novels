import { useState } from 'react';
import { apiRequest } from '../utils/api';
import type { AdminUser } from '../utils/api';
import { Shield, Key, User, Activity } from 'lucide-react';

interface LoginProps {
  onLoginSuccess: (token: string, admin: AdminUser) => void;
}

export default function Login({ onLoginSuccess }: LoginProps) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [seedSuccess, setSeedSuccess] = useState('');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    setSeedSuccess('');

    try {
      const data = await apiRequest('POST', '/auth/login', { username, password });
      onLoginSuccess(data.token, data.admin);
    } catch (err: any) {
      setError(err.message || '登录失败，请检查您的账号和密码。');
    } finally {
      setLoading(false);
    }
  };

  const handleSeedAdmin = async () => {
    setLoading(true);
    setError('');
    setSeedSuccess('');
    try {
      await apiRequest('POST', '/auth/register', {
        username: 'admin',
        password: 'admin123',
        nickname: '超级管理员',
        role: 'SuperAdmin'
      });
      setSeedSuccess('成功初始化默认管理员账号！请使用：admin / admin123 登录');
      setUsername('admin');
      setPassword('admin123');
    } catch (err: any) {
      setError(err.message || '初始化失败，管理员账号可能已存在。');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div style={{
      display: 'flex',
      minHeight: '100vh',
      alignItems: 'center',
      justifyContent: 'center',
      padding: '24px',
      background: 'radial-gradient(circle at center, #f4f7fb 0%, #e1e7f0 100%)'
    }}>
      <div className="glass-panel animate-fade-in" style={{
        width: '100%',
        maxWidth: '420px',
        padding: '36px',
        border: '1px solid hsl(var(--border) / 0.8)',
        boxShadow: '0 20px 40px -15px rgba(37, 99, 235, 0.08)'
      }}>
        {/* Logo */}
        <div style={{ textAlign: 'center', marginBottom: '32px' }}>
          <div className="gradient-cyber" style={{
            display: 'inline-flex',
            padding: '12px',
            borderRadius: '16px',
            marginBottom: '16px',
            boxShadow: 'var(--shadow-neon)'
          }}>
            <Shield size={32} color="#fff" />
          </div>
          <h2 style={{ fontSize: '1.8rem', fontWeight: 700, marginBottom: '6px' }} className="gradient-text">
            STAR NOVEL
          </h2>
          <p style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.9rem' }}>
            海外小说内容与推广管理系统
          </p>
        </div>

        {error && (
          <div style={{
            backgroundColor: 'rgba(239, 68, 68, 0.1)',
            border: '1px solid rgba(239, 68, 68, 0.25)',
            color: '#f87171',
            padding: '12px',
            borderRadius: '8px',
            fontSize: '0.85rem',
            marginBottom: '20px'
          }}>
            {error}
          </div>
        )}

        {seedSuccess && (
          <div style={{
            backgroundColor: 'rgba(16, 185, 129, 0.1)',
            border: '1px solid rgba(16, 185, 129, 0.25)',
            color: '#10b981',
            padding: '12px',
            borderRadius: '8px',
            fontSize: '0.85rem',
            marginBottom: '20px'
          }}>
            {seedSuccess}
          </div>
        )}

        <form onSubmit={handleSubmit}>
          <div style={{ marginBottom: '18px' }}>
            <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px', fontWeight: 500 }}>
              管理员账号
            </label>
            <div style={{ position: 'relative' }}>
              <span style={{ position: 'absolute', left: '12px', top: '12px', color: 'hsl(var(--text-muted))' }}>
                <User size={16} />
              </span>
              <input
                type="text"
                className="input-field"
                style={{ paddingLeft: '38px' }}
                placeholder="请输入管理员用户名"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
              />
            </div>
          </div>

          <div style={{ marginBottom: '24px' }}>
            <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px', fontWeight: 500 }}>
              登录密码
            </label>
            <div style={{ position: 'relative' }}>
              <span style={{ position: 'absolute', left: '12px', top: '12px', color: 'hsl(var(--text-muted))' }}>
                <Key size={16} />
              </span>
              <input
                type="password"
                className="input-field"
                style={{ paddingLeft: '38px' }}
                placeholder="请输入登录密码"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
          </div>

          <button
            type="submit"
            className="btn-primary"
            disabled={loading}
            style={{ width: '100%', padding: '12px', marginBottom: '16px' }}
          >
            {loading ? '正在验证身份...' : '登录管理后台'}
          </button>
        </form>

        <div style={{
          borderTop: '1px dashed hsl(var(--border))',
          paddingTop: '20px',
          textAlign: 'center'
        }}>
          <p style={{ color: 'hsl(var(--text-muted))', fontSize: '0.75rem', marginBottom: '10px' }}>
            首次部署？一键初始化默认超级管理员账号。
          </p>
          <button
            onClick={handleSeedAdmin}
            className="btn-secondary"
            disabled={loading}
            style={{ width: '100%', fontSize: '0.8rem', padding: '8px 12px' }}
          >
            <Activity size={14} style={{ marginRight: '6px' }} />
            初始化默认账号 (admin/admin123)
          </button>
        </div>
      </div>
    </div>
  );
}

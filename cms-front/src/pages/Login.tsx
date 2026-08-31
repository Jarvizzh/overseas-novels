import { useState, useEffect } from 'react';
import { apiRequest } from '../utils/api';
import type { AdminUser } from '../utils/api';
import { Key, User, Sun, Moon } from 'lucide-react';

interface LoginProps {
  onLoginSuccess: (token: string, admin: AdminUser) => void;
}

export default function Login({ onLoginSuccess }: LoginProps) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const [theme, setTheme] = useState<'light' | 'dark'>(() => {
    return (localStorage.getItem('cms_theme') as 'light' | 'dark') || 'dark';
  });

  const toggleTheme = () => {
    const nextTheme = theme === 'light' ? 'dark' : 'light';
    setTheme(nextTheme);
    localStorage.setItem('cms_theme', nextTheme);
    document.documentElement.setAttribute('data-theme', nextTheme);
  };

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
  }, [theme]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');

    try {
      const data = await apiRequest('POST', '/auth/login', { username, password });
      onLoginSuccess(data.token, data.admin);
    } catch (err: any) {
      setError(err.message || '登录失败，请检查您的账号和密码。');
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
      position: 'relative',
      background: theme === 'dark'
        ? 'radial-gradient(circle at 50% 30%, #0f172a 0%, #020617 100%)'
        : 'radial-gradient(circle at 50% 30%, #f8fafc 0%, #e2e8f0 100%)'
    }}>
      {/* Theme Switcher Button */}
      <div style={{ position: 'absolute', top: '24px', right: '24px', zIndex: 10 }}>
        <button
          onClick={toggleTheme}
          className="btn-secondary"
          style={{
            padding: '6px 12px',
            fontSize: '0.8rem',
            display: 'flex',
            gap: '6px',
            alignItems: 'center',
            cursor: 'pointer',
            borderRadius: '8px',
            color: 'hsl(var(--text-primary))',
            backgroundColor: 'hsl(var(--bg-card))',
            border: '1px solid hsl(var(--border))',
            transition: 'all 0.2s ease'
          }}
          title={theme === 'light' ? '切换为深色模式' : '切换为浅色模式'}
        >
          {theme === 'light' ? <Moon size={15} style={{ color: 'hsl(var(--primary))' }} /> : <Sun size={15} style={{ color: 'hsl(var(--accent-orange))' }} />}
          <span>{theme === 'light' ? '深色模式' : '浅色模式'}</span>
        </button>
      </div>

      <div className="glass-panel animate-fade-in" style={{
        width: '100%',
        maxWidth: '420px',
        padding: '36px',
        border: '1px solid hsl(var(--border) / 0.8)',
        boxShadow: theme === 'dark'
          ? '0 20px 50px -10px rgba(0, 0, 0, 0.5), 0 0 20px 0 rgba(37, 99, 235, 0.15)'
          : '0 20px 40px -15px rgba(37, 99, 235, 0.08)'
      }}>
        {/* Logo */}
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', textAlign: 'center', marginBottom: '32px' }}>
          <img
            src={theme === 'dark' ? '/assets/logo_dark.png?v=twin_exact' : '/assets/logo_light.png?v=twin_exact'}
            alt="STAR NOVEL Logo"
            style={{
              width: '64px',
              height: '64px',
              objectFit: 'contain',
              borderRadius: '16px',
              marginBottom: '16px',
              boxShadow: 'var(--shadow-neon)',
              border: '1px solid hsl(var(--border))'
            }}
          />
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
            style={{ width: '100%', padding: '12px', marginBottom: '0' }}
          >
            {loading ? '正在验证身份...' : '登录管理后台'}
          </button>
        </form>
      </div>
    </div>
  );
}

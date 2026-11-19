import { useState } from 'react';
import { Key, User } from 'lucide-react';
import { apiRequest } from '../utils/api';
import type { AdminUser } from '../utils/api';
import { useThemeMode } from '../utils/theme';
import { ThemeSwitcher } from '../components/ThemeSwitcher';

interface LoginProps {
  onLoginSuccess: (token: string, admin: AdminUser) => void;
}

export default function Login({ onLoginSuccess }: LoginProps) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const { mode, resolvedTheme, setMode } = useThemeMode();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (loading) return;
    setLoading(true);
    setError('');

    try {
      const data = await apiRequest('POST', '/auth/login', { username, password });
      onLoginSuccess(data.token, data.admin);
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '';
      setError(msg || '登录失败，请检查您的账号和密码。');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center p-6 relative bg-[var(--cms-bg)] text-[var(--cms-fg)] transition-colors">
      {/* Icon-only Theme Switcher */}
      <div className="absolute top-6 right-6 z-10">
        <ThemeSwitcher mode={mode} resolvedTheme={resolvedTheme} onChange={setMode} />
      </div>

      <div
        className="w-full max-w-[420px] bg-[var(--cms-card)] border border-[var(--cms-border)] rounded-2xl p-9 space-y-7 animate-fade-in"
        style={{ boxShadow: 'var(--shadow-lg)' }}
      >
        {/* Brand */}
        <div className="flex flex-col items-center text-center space-y-3">
          <img
            src={resolvedTheme === 'dark' ? '/assets/logo_dark.png?v=twin_exact' : '/assets/logo_light.png?v=twin_exact'}
            alt="STAR NOVEL Logo"
            className="w-14 h-14 object-contain rounded-2xl border border-[var(--cms-border)]"
          />
          <div>
            <h1 className="text-2xl font-bold tracking-tight text-[var(--cms-fg)] m-0">STAR NOVEL</h1>
            <p className="text-sm text-[var(--cms-muted-fg)] mt-1 m-0">海外小说内容与推广管理系统</p>
          </div>
        </div>

        {error && (
          <div
            role="alert"
            className="px-3.5 py-2.5 rounded-xl text-xs font-medium border"
            style={{
              backgroundColor: 'color-mix(in srgb, var(--cms-error) 10%, transparent)',
              borderColor: 'color-mix(in srgb, var(--cms-error) 25%, transparent)',
              color: 'var(--cms-error)',
            }}
          >
            {error}
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          <label className="block space-y-1.5">
            <span className="block text-xs font-medium text-[var(--cms-muted-fg)]">管理员账号</span>
            <div className="relative">
              <span className="absolute left-3.5 top-1/2 -translate-y-1/2 text-[var(--cms-placeholder)] pointer-events-none">
                <User size={15} aria-hidden="true" />
              </span>
              <input
                type="text"
                className="input-field"
                style={{ paddingLeft: '38px' }}
                placeholder="请输入管理员用户名"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
              />
            </div>
          </label>

          <label className="block space-y-1.5">
            <span className="block text-xs font-medium text-[var(--cms-muted-fg)]">登录密码</span>
            <div className="relative">
              <span className="absolute left-3.5 top-1/2 -translate-y-1/2 text-[var(--cms-placeholder)] pointer-events-none">
                <Key size={15} aria-hidden="true" />
              </span>
              <input
                type="password"
                className="input-field"
                style={{ paddingLeft: '38px' }}
                placeholder="请输入登录密码"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
          </label>

          <button type="submit" disabled={loading} className="btn-primary w-full h-11 text-sm font-medium mt-2">
            {loading ? '正在验证身份...' : '登录管理后台'}
          </button>
        </form>
      </div>
    </div>
  );
}

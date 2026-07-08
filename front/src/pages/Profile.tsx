import React, { useState, useEffect } from 'react';
import { GoldCoin } from '../components/GoldCoin';
import { api } from '../utils/api';
import type { User, Transaction } from '../utils/api';

interface ProfileProps {
  shelfBookIds: number[];
  readingProgress: {
    [bookId: number]: {
      chapterIndex: number;
      scrollOffsetPercentage: number;
    };
  };
  onNavigate: (page: string, params?: any) => void;
  globalTheme: 'light' | 'dark';
  onChangeGlobalTheme: (theme: 'light' | 'dark') => void;
  userCoins: number;
  transactionHistory: Transaction[];
  currentUser: User | null;
  onLoginSuccess: (token: string, user: User) => void;
  onLogout: () => void;
}

export const Profile: React.FC<ProfileProps> = ({
  shelfBookIds,
  readingProgress,
  onNavigate,
  globalTheme,
  onChangeGlobalTheme,
  userCoins,
  transactionHistory,
  currentUser,
  onLoginSuccess,
  onLogout,
}) => {
  // Mock reading time that persists
  const [readingTime, setReadingTime] = useState<number>(() => {
    const time = localStorage.getItem('profile-reading-time');
    return time ? parseInt(time) : 185;
  });

  const [activeSubTab, setActiveSubTab] = useState<'settings' | 'history'>('settings');
  const [authMode, setAuthMode] = useState<'none' | 'login' | 'register'>('none');
  
  // Auth Form fields
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [nickname, setNickname] = useState('');
  const [authError, setAuthError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  useEffect(() => {
    const interval = setInterval(() => {
      setReadingTime((prev) => {
        const next = prev + 1;
        localStorage.setItem('profile-reading-time', next.toString());
        return next;
      });
    }, 60000);
    
    return () => clearInterval(interval);
  }, []);

  const totalBooksRead = Object.keys(readingProgress).length;

  const handleClearCache = () => {
    if (window.confirm("Are you sure you want to clear all reading progress and book shelf data?")) {
      localStorage.clear();
      window.location.reload();
    }
  };

  const getThemeValueLabel = () => {
    if (globalTheme === 'dark') return 'Dark Mode 🌙';
    return 'Light Mode ☀️';
  };

  const handleCycleTheme = () => {
    onChangeGlobalTheme(globalTheme === 'light' ? 'dark' : 'light');
  };

  // Auth Action Handler
  const handleAuthSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setAuthError(null);
    setIsSubmitting(true);

    try {
      if (authMode === 'login') {
        const res = await api.login({ email, password });
        onLoginSuccess(res.token, res.user);
        setAuthMode('none');
      } else if (authMode === 'register') {
        const res = await api.register({ email, password, nickname });
        onLoginSuccess(res.token, res.user);
        setAuthMode('none');
      }
      // Reset inputs
      setEmail('');
      setPassword('');
      setNickname('');
    } catch (err: any) {
      setAuthError(err.message || "Authentication failed. Please check credentials.");
    } finally {
      setIsSubmitting(false);
    }
  };

  const isGuest = !currentUser || !currentUser.email;

  return (
    <div className="scroll-container animate-fade-in" style={{ paddingBottom: '32px' }}>
      
      {/* Profile Header */}
      <div className="profile-card" style={{ display: 'flex', alignItems: 'center', gap: '16px', position: 'relative' }}>
        <div className="profile-avatar" style={{ 
          background: isGuest ? 'linear-gradient(135deg, #a1a1aa 0%, #71717a 100%)' : 'linear-gradient(135deg, #6366f1 0%, #4338ca 100%)',
          color: 'white',
          fontSize: '20px',
          fontWeight: 'bold',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: '52px',
          height: '52px',
          borderRadius: '50%'
        }}>
          {isGuest ? 'G' : (currentUser?.nickname ? currentUser.nickname[0].toUpperCase() : 'U')}
        </div>
        <div style={{ flex: 1 }}>
          <h2 className="profile-name" style={{ fontSize: '18px', fontWeight: 800 }}>
            {currentUser?.nickname || 'Star Guest'}
          </h2>
          <p className="profile-bio" style={{ fontSize: '12px', color: 'var(--text-tertiary)' }}>
            {isGuest ? 'Guest Account (Sync not persisted)' : currentUser.email}
          </p>
        </div>
        {isGuest ? (
          <button 
            onClick={() => setAuthMode('login')}
            style={{
              position: 'absolute',
              right: '16px',
              padding: '6px 12px',
              fontSize: '11px',
              fontWeight: 700,
              backgroundColor: 'var(--accent-color)',
              color: 'white',
              border: 'none',
              borderRadius: '20px',
              cursor: 'pointer'
            }}
          >
            Sign In / Bind
          </button>
        ) : (
          <button 
            onClick={onLogout}
            style={{
              position: 'absolute',
              right: '16px',
              padding: '6px 12px',
              fontSize: '11px',
              fontWeight: 700,
              backgroundColor: 'var(--bg-tertiary)',
              color: '#ef4444',
              border: '1px solid var(--border-color)',
              borderRadius: '20px',
              cursor: 'pointer'
            }}
          >
            Sign Out
          </button>
        )}
      </div>

      {/* Wallet Card Section */}
      <div style={{
        background: 'linear-gradient(135deg, #020617 0%, #1e293b 100%)',
        color: 'white',
        borderRadius: '16px',
        padding: '20px',
        marginBottom: '20px',
        border: '1px solid var(--border-color)',
        boxShadow: 'var(--card-shadow)',
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center'
      }}>
        <div>
          <span style={{ fontSize: '11px', color: '#94a3b8' }}>My Wallet</span>
          <div style={{ display: 'flex', alignItems: 'center', gap: '6px', marginTop: '4px' }}>
            <GoldCoin size={20} />
            <span style={{ fontSize: '24px', fontWeight: 800, fontFamily: 'monospace' }}>{userCoins}</span>
            <span style={{ fontSize: '12px', color: '#94a3b8' }}>Coins</span>
          </div>
        </div>
        <button 
          className="btn-cta-primary" 
          onClick={() => onNavigate('recharge')}
          style={{ 
            flex: 'none', 
            padding: '8px 16px', 
            fontSize: '12px', 
            width: 'auto',
            boxShadow: '0 4px 10px rgba(79,70,229,0.3)'
          }}
        >
          Top Up +
        </button>
      </div>

      {/* Reading Statistics */}
      <div className="stats-grid">
        <div className="stats-card">
          <div className="stats-val">{readingTime}m</div>
          <div className="stats-lbl">Reading Time</div>
        </div>
        <div className="stats-card">
          <div className="stats-val">{shelfBookIds.length}</div>
          <div className="stats-lbl">In Shelf</div>
        </div>
      </div>

      {/* Sub Tabs Selector */}
      <div className="detail-tabs" style={{ margin: '10px -16px 16px', padding: '0 16px' }}>
        <button 
          className={`detail-tab ${activeSubTab === 'settings' ? 'active' : ''}`}
          onClick={() => setActiveSubTab('settings')}
        >
          Preferences
        </button>
        <button 
          className={`detail-tab ${activeSubTab === 'history' ? 'active' : ''}`}
          onClick={() => setActiveSubTab('history')}
        >
          Bill History ({transactionHistory.length})
        </button>
      </div>

      {/* Settings list */}
      {activeSubTab === 'settings' ? (
        <div>
          <div className="settings-list">
            <div className="settings-item" onClick={handleCycleTheme}>
              <span>App Display Theme</span>
              <span className="settings-item-value">{getThemeValueLabel()}</span>
            </div>
            
            <div className="settings-item" onClick={() => onNavigate('shelf')}>
              <span>Reading History</span>
              <span className="settings-item-value">View ({totalBooksRead}) ›</span>
            </div>

            <div className="settings-item" onClick={() => alert("Multi-language: English is currently the default language.")}>
              <span>Interface Language</span>
              <span className="settings-item-value">English (US) ›</span>
            </div>
          </div>

          <h3 className="hot-tags-title" style={{ fontSize: '13px', margin: '24px 0 10px' }}>Maintenance</h3>
          <div className="settings-list">
            <div className="settings-item" onClick={handleClearCache} style={{ color: '#ef4444' }}>
              <span>Reset Reading Progress & Shelf</span>
              <span className="settings-item-value" style={{ color: '#ef4444' }}>Wipe Storage ›</span>
            </div>

            <div className="settings-item" onClick={() => alert("StarNovel H5 Demo v1.0.0. Powered by Go backend.")}>
              <span>About App Version</span>
              <span className="settings-item-value">v1.0.0 ›</span>
            </div>
          </div>
        </div>
      ) : (
        /* Bill transaction history list */
        <div style={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
          {transactionHistory.length > 0 ? (
            transactionHistory.map((item, index) => (
              <div 
                key={index}
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  padding: '12px 16px',
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: '12px',
                  border: '1px solid var(--border-color)'
                }}
              >
                <div>
                  <h4 style={{ fontSize: '13px', fontWeight: 700, color: 'var(--text-primary)' }}>{item.desc}</h4>
                  <span style={{ fontSize: '10px', color: 'var(--text-tertiary)', marginTop: '2px', display: 'block' }}>
                    {new Date(item.date).toLocaleString()}
                  </span>
                </div>
                <span 
                  style={{ 
                    fontSize: '14px', 
                    fontWeight: 700, 
                    color: item.type === 'credit' ? '#22c55e' : '#ef4444',
                    fontFamily: 'monospace'
                  }}
                >
                  {item.type === 'credit' ? '+' : '-'}{item.amount}
                </span>
              </div>
            ))
          ) : (
            <div className="shelf-empty-state">
              <span className="shelf-empty-icon">
                <GoldCoin size={48} />
              </span>
              <p>No transaction history yet.</p>
              <p style={{ fontSize: '12px', marginTop: '4px' }}>Recharge coins or complete tasks to start reading paid books.</p>
            </div>
          )}
        </div>
      )}

      {/* Auth Modal Overlay */}
      {authMode !== 'none' && (
        <div style={{
          position: 'fixed',
          inset: 0,
          backgroundColor: 'rgba(0,0,0,0.6)',
          backdropFilter: 'blur(5px)',
          zIndex: 999,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '20px'
        }}>
          <div className="animate-fade-in" style={{
            backgroundColor: 'var(--bg-secondary)',
            borderRadius: '16px',
            padding: '24px',
            width: '100%',
            maxWidth: '340px',
            boxShadow: '0 20px 25px -5px rgba(0,0,0,0.3)',
            border: '1px solid var(--border-color)',
            position: 'relative'
          }}>
            <button 
              onClick={() => {
                setAuthMode('none');
                setAuthError(null);
              }}
              style={{
                position: 'absolute',
                top: '14px',
                right: '14px',
                background: 'none',
                border: 'none',
                color: 'var(--text-secondary)',
                fontSize: '20px',
                cursor: 'pointer'
              }}
            >
              ×
            </button>

            <h3 style={{ fontSize: '18px', fontWeight: 800, marginBottom: '20px', color: 'var(--text-primary)', textAlign: 'center' }}>
              {authMode === 'login' ? 'Sign In to Account' : 'Register New Account'}
            </h3>

            <form onSubmit={handleAuthSubmit} style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
              <div>
                <label style={{ fontSize: '12px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px' }}>Email Address</label>
                <input 
                  type="email" 
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required 
                  style={{
                    width: '100%',
                    padding: '10px 14px',
                    borderRadius: '8px',
                    border: '1px solid var(--border-color)',
                    backgroundColor: 'var(--bg-primary)',
                    color: 'var(--text-primary)',
                    fontSize: '14px'
                  }}
                />
              </div>

              <div>
                <label style={{ fontSize: '12px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px' }}>Password</label>
                <input 
                  type="password" 
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required 
                  minLength={6}
                  style={{
                    width: '100%',
                    padding: '10px 14px',
                    borderRadius: '8px',
                    border: '1px solid var(--border-color)',
                    backgroundColor: 'var(--bg-primary)',
                    color: 'var(--text-primary)',
                    fontSize: '14px'
                  }}
                />
              </div>

              {authMode === 'register' && (
                <div>
                  <label style={{ fontSize: '12px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px' }}>Nickname</label>
                  <input 
                    type="text" 
                    value={nickname}
                    onChange={(e) => setNickname(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '10px 14px',
                      borderRadius: '8px',
                      border: '1px solid var(--border-color)',
                      backgroundColor: 'var(--bg-primary)',
                      color: 'var(--text-primary)',
                      fontSize: '14px'
                    }}
                  />
                </div>
              )}

              {authError && (
                <div style={{ color: '#ef4444', fontSize: '12px', textAlign: 'center', marginTop: '4px' }}>
                  ⚠️ {authError}
                </div>
              )}

              <button 
                type="submit" 
                disabled={isSubmitting}
                style={{
                  width: '100%',
                  padding: '12px',
                  backgroundColor: 'var(--accent-color)',
                  color: 'white',
                  border: 'none',
                  borderRadius: '24px',
                  fontSize: '15px',
                  fontWeight: 700,
                  cursor: isSubmitting ? 'not-allowed' : 'pointer',
                  boxShadow: '0 4px 14px rgba(79,70,229,0.3)',
                  marginTop: '10px'
                }}
              >
                {isSubmitting ? 'Processing...' : (authMode === 'login' ? 'Sign In' : 'Register & Bind')}
              </button>
            </form>

            <div style={{ marginTop: '16px', textAlign: 'center', fontSize: '12px', color: 'var(--text-secondary)' }}>
              {authMode === 'login' ? (
                <>
                  New to Star Novel?{' '}
                  <span 
                    onClick={() => {
                      setAuthMode('register');
                      setAuthError(null);
                    }} 
                    style={{ color: 'var(--accent-color)', fontWeight: 700, cursor: 'pointer' }}
                  >
                    Create Account
                  </span>
                </>
              ) : (
                <>
                  Already have an account?{' '}
                  <span 
                    onClick={() => {
                      setAuthMode('login');
                      setAuthError(null);
                    }} 
                    style={{ color: 'var(--accent-color)', fontWeight: 700, cursor: 'pointer' }}
                  >
                    Sign In
                  </span>
                </>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

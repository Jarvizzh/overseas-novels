import React, { useState, useEffect } from 'react';
import { useToast } from '../context/ToastContext';
import { GoldCoin } from '../components/GoldCoin';
import { api } from '../utils/api';
import type { User, Transaction } from '../utils/api';

import { LegalModal } from '../components/LegalModal';
import type { LegalModalType } from '../components/LegalModal';

interface ProfileProps {
  shelfBookIds: number[];
  readingProgress: {
    [bookId: number]: {
      chapterIndex: number;
      scrollOffsetPercentage: number;
    };
  };
  onNavigate: (page: string, params?: any) => void;
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
  const [authMode, setAuthMode] = useState<'none' | 'login' | 'signup'>('none');
  const [activeLegalModal, setActiveLegalModal] = useState<LegalModalType | null>(null);
  
  // Auth Form fields
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [nickname, setNickname] = useState('');
  const [authError, setAuthError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const { showToast } = useToast();

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
  const isGuest = !currentUser || !currentUser.email;

  // Auth Action Handler
  const handleAuthSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setAuthError(null);
    setIsSubmitting(true);

    try {
      if (authMode === 'login') {
        const res = await api.login({ email, password });
        onLoginSuccess(res.token, res.user);
        showToast("Welcome back!", "success");
        setAuthMode('none');
      } else if (authMode === 'signup') {
        if (isGuest) {
          const res = await api.bindEmail({ email, password, nickname });
          onLoginSuccess(res.token, res.user);
          showToast("Account created successfully! Your coins and progress are secured.", "success");
        } else {
          const res = await api.register({ email, password, nickname });
          onLoginSuccess(res.token, res.user);
          showToast("Account created successfully!", "success");
        }
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

  return (
    <div className="scroll-container animate-fade-in" style={{ paddingBottom: '90px' }}>
      
      {/* Profile Header Card */}
      <div 
        className="profile-card" 
        style={{ 
          display: 'flex', 
          alignItems: 'center', 
          gap: '12px', 
          padding: '16px',
          backgroundColor: 'var(--bg-secondary)',
          borderRadius: '16px',
          border: '1px solid var(--border-color)',
          marginBottom: '16px',
          boxShadow: 'var(--card-shadow)'
        }}
      >
        <div className="profile-avatar" style={{ 
          flex: 'none',
          background: isGuest ? 'linear-gradient(135deg, #a1a1aa 0%, #71717a 100%)' : 'linear-gradient(135deg, #6366f1 0%, #4338ca 100%)',
          color: 'white',
          fontSize: '20px',
          fontWeight: 'bold',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: '48px',
          height: '48px',
          borderRadius: '50%'
        }}>
          {isGuest ? 'G' : (currentUser?.nickname ? currentUser.nickname[0].toUpperCase() : 'U')}
        </div>
        <div style={{ flex: 1, minWidth: 0, overflow: 'hidden' }}>
          <h2 className="profile-name" style={{ fontSize: '15px', fontWeight: 800, margin: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'var(--text-primary)' }}>
            {currentUser?.nickname || 'Star Guest'}
          </h2>
          <p className="profile-bio" style={{ fontSize: '11px', color: 'var(--text-tertiary)', margin: '4px 0 0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {isGuest ? 'Guest Account (Sync not persisted)' : currentUser.email}
          </p>
        </div>
        {isGuest ? (
          <button 
            onClick={() => {
              setAuthMode('signup');
              setAuthError(null);
            }}
            style={{
              flex: 'none',
              padding: '7px 12px',
              fontSize: '11px',
              fontWeight: 700,
              backgroundColor: 'var(--accent-color)',
              color: 'white',
              border: 'none',
              borderRadius: '20px',
              cursor: 'pointer',
              whiteSpace: 'nowrap',
              boxShadow: '0 2px 8px rgba(79, 70, 229, 0.3)'
            }}
          >
            Sign Up / Sign In
          </button>
        ) : (
          <button 
            onClick={onLogout}
            style={{
              flex: 'none',
              padding: '7px 12px',
              fontSize: '11px',
              fontWeight: 700,
              backgroundColor: 'var(--bg-tertiary)',
              color: '#ef4444',
              border: '1px solid var(--border-color)',
              borderRadius: '20px',
              cursor: 'pointer',
              whiteSpace: 'nowrap'
            }}
          >
            Sign Out
          </button>
        )}
      </div>

      {/* Wallet Card Section */}
      <div style={{
        background: 'linear-gradient(135deg, var(--bg-secondary) 0%, var(--bg-tertiary) 100%)',
        borderRadius: '16px',
        padding: '20px',
        marginBottom: '20px',
        border: '1px solid var(--border-color)',
        boxShadow: 'var(--card-shadow)',
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        transition: 'all 0.3s ease'
      }}>
        <div>
          <span style={{ fontSize: '11px', color: 'var(--accent-color)', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
            My Wallet
          </span>
          <div style={{ display: 'flex', alignItems: 'center', gap: '6px', marginTop: '4px' }}>
            <GoldCoin size={22} />
            <span style={{ fontSize: '26px', fontWeight: 800, fontFamily: 'monospace', color: 'var(--text-primary)' }}>
              {userCoins}
            </span>
            <span style={{ fontSize: '13px', color: 'var(--text-secondary)', fontWeight: 500 }}>
              Coins
            </span>
          </div>
        </div>
        <button 
          className="btn-cta-primary" 
          onClick={() => onNavigate('recharge')}
          style={{ 
            flex: 'none', 
            padding: '10px 18px', 
            fontSize: '13px', 
            fontWeight: 700,
            width: 'auto',
            borderRadius: '99px',
            boxShadow: '0 4px 14px rgba(79, 70, 229, 0.35)',
            border: 'none',
            color: 'white',
            cursor: 'pointer'
          }}
        >
          Top Up
        </button>
      </div>

      {/* Sub Tabs: Settings vs History */}
      <div className="sub-tabs-container" style={{ marginBottom: '16px' }}>
        <button 
          className={`sub-tab-btn ${activeSubTab === 'settings' ? 'active' : ''}`}
          onClick={() => setActiveSubTab('settings')}
        >
          Account Settings
        </button>
        <button 
          className={`sub-tab-btn ${activeSubTab === 'history' ? 'active' : ''}`}
          onClick={() => setActiveSubTab('history')}
        >
          Coin History
        </button>
      </div>

      {activeSubTab === 'settings' ? (
        <div className="settings-section">
          <div className="settings-list">
            <div className="settings-item" onClick={() => onNavigate('rewards')}>
              <span>Daily Rewards & Tasks</span>
              <span className="settings-item-value">Earn Coins ›</span>
            </div>

            <div className="settings-item">
              <span>Total Reading Time</span>
              <span className="settings-item-value">{readingTime} mins</span>
            </div>

            <div className="settings-item" onClick={() => onNavigate('shelf')}>
              <span>Reading History & Shelf</span>
              <span className="settings-item-value">View ({shelfBookIds.length} in shelf, {totalBooksRead} read) ›</span>
            </div>
          </div>

          <div style={{ marginTop: '20px' }}>
            <h3 className="hot-tags-title" style={{ fontSize: '13px', marginBottom: '10px' }}>Support & Legal Policies</h3>
            <div className="settings-list">
              <div className="settings-item" onClick={() => setActiveLegalModal('contact')}>
                <span>Contact Us & Customer Support</span>
                <span className="settings-item-value">Support ›</span>
              </div>
              <div className="settings-item" onClick={() => setActiveLegalModal('refund')}>
                <span>Refund & Purchase Policy</span>
                <span className="settings-item-value">View ›</span>
              </div>
              <div className="settings-item" onClick={() => setActiveLegalModal('terms')}>
                <span>Terms of Service</span>
                <span className="settings-item-value">View ›</span>
              </div>
              <div className="settings-item" onClick={() => setActiveLegalModal('privacy')}>
                <span>Privacy Policy</span>
                <span className="settings-item-value">View ›</span>
              </div>
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
            maxWidth: '350px',
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

            {/* 2-Tab Auth Switcher */}
            <div style={{
              display: 'flex',
              backgroundColor: 'var(--bg-primary)',
              borderRadius: '10px',
              padding: '3px',
              marginBottom: '16px'
            }}>
              <button
                type="button"
                onClick={() => {
                  setAuthMode('signup');
                  setAuthError(null);
                }}
                style={{
                  flex: 1,
                  padding: '8px 4px',
                  fontSize: '12px',
                  fontWeight: authMode === 'signup' ? 700 : 500,
                  borderRadius: '8px',
                  border: 'none',
                  backgroundColor: authMode === 'signup' ? 'var(--bg-secondary)' : 'transparent',
                  color: authMode === 'signup' ? 'var(--accent-color)' : 'var(--text-secondary)',
                  cursor: 'pointer',
                  boxShadow: authMode === 'signup' ? '0 2px 4px rgba(0,0,0,0.1)' : 'none'
                }}
              >
                Sign Up
              </button>
              <button
                type="button"
                onClick={() => {
                  setAuthMode('login');
                  setAuthError(null);
                }}
                style={{
                  flex: 1,
                  padding: '8px 4px',
                  fontSize: '12px',
                  fontWeight: authMode === 'login' ? 700 : 500,
                  borderRadius: '8px',
                  border: 'none',
                  backgroundColor: authMode === 'login' ? 'var(--bg-secondary)' : 'transparent',
                  color: authMode === 'login' ? 'var(--accent-color)' : 'var(--text-secondary)',
                  cursor: 'pointer',
                  boxShadow: authMode === 'login' ? '0 2px 4px rgba(0,0,0,0.1)' : 'none'
                }}
              >
                Sign In
              </button>
            </div>

            <h3 style={{ fontSize: '17px', fontWeight: 800, marginBottom: '6px', color: 'var(--text-primary)', textAlign: 'center' }}>
              {authMode === 'signup' ? 'Sign Up for Account' : 'Sign In to Account'}
            </h3>
            <p style={{ fontSize: '11px', color: 'var(--text-tertiary)', textAlign: 'center', marginBottom: '16px' }}>
              {authMode === 'signup' 
                ? 'Sign up to keep your coins and reading progress safe.'
                : 'Sign in to access your existing account and reading library.'}
            </p>

            <form onSubmit={handleAuthSubmit} style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
              <div>
                <label style={{ fontSize: '12px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px' }}>Email Address</label>
                <input 
                  type="email" 
                  value={email}
                  placeholder="name@example.com"
                  onChange={(e) => setEmail(e.target.value)}
                  required 
                  style={{
                    width: '100%',
                    padding: '10px 14px',
                    borderRadius: '8px',
                    border: '1px solid var(--border-color)',
                    backgroundColor: 'var(--bg-primary)',
                    color: 'var(--text-primary)',
                    fontSize: '14px',
                    boxSizing: 'border-box'
                  }}
                />
              </div>

              <div>
                <label style={{ fontSize: '12px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px' }}>
                  {authMode === 'login' ? 'Password' : 'Set Password (min 6 chars)'}
                </label>
                <input 
                  type="password" 
                  value={password}
                  placeholder="••••••••"
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
                    fontSize: '14px',
                    boxSizing: 'border-box'
                  }}
                />
              </div>

              {authMode === 'signup' && (
                <div>
                  <label style={{ fontSize: '12px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px' }}>Nickname (Optional)</label>
                  <input 
                    type="text" 
                    value={nickname}
                    placeholder="Star Reader"
                    onChange={(e) => setNickname(e.target.value)}
                    style={{
                      width: '100%',
                      padding: '10px 14px',
                      borderRadius: '8px',
                      border: '1px solid var(--border-color)',
                      backgroundColor: 'var(--bg-primary)',
                      color: 'var(--text-primary)',
                      fontSize: '14px',
                      boxSizing: 'border-box'
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
                  marginTop: '6px'
                }}
              >
                {isSubmitting ? 'Processing...' : (
                  authMode === 'signup' ? 'Sign Up & Save' : 'Sign In'
                )}
              </button>
            </form>

            <div style={{ marginTop: '16px', textAlign: 'center', fontSize: '12px', color: 'var(--text-secondary)' }}>
              {authMode === 'login' ? (
                <>
                  New to Star Novel?{' '}
                  <span 
                    onClick={() => {
                      setAuthMode('signup');
                      setAuthError(null);
                    }} 
                    style={{ color: 'var(--accent-color)', fontWeight: 700, cursor: 'pointer' }}
                  >
                    Sign Up
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

      {/* Legal & Compliance Policy Modal */}
      <LegalModal
        isOpen={activeLegalModal !== null}
        type={activeLegalModal || 'contact'}
        onClose={() => setActiveLegalModal(null)}
        defaultEmail={currentUser?.email || ''}
      />
    </div>
  );
};

import React, { useState, useEffect, useRef, useId } from 'react';
import { ChevronRight, Sun, Moon, Monitor, Crown, X, AlertCircle } from 'lucide-react';
import { useToast } from '../context/toastStore';
import { GoldCoin } from '../components/GoldCoin';
import { api } from '../utils/api';
import type { User, Transaction, SubscriptionInfo } from '../utils/api';
import { LegalModal, type LegalModalType } from '../components/LegalModal';
import type { ThemePreference } from '../utils/theme';

interface ProfileProps {
  shelfBookIds: number[];
  onNavigate: (page: string, params?: Record<string, unknown>) => void;
  userCoins: number;
  chargedCoins: number;
  bonusCoins: number;
  isVip: boolean;
  subscription: SubscriptionInfo | null;
  transactionHistory: Transaction[];
  hasMoreTransactions?: boolean;
  loadingMoreTransactions?: boolean;
  onLoadMoreTransactions?: () => void;
  currentUser: User | null;
  themePreference: ThemePreference;
  systemTheme: 'light' | 'dark';
  onThemePreferenceChange: (pref: ThemePreference) => void;
  onLoginSuccess: (token: string, user: User) => void;
  onLogout: () => void;
}

export const Profile: React.FC<ProfileProps> = ({
  shelfBookIds,
  onNavigate,
  userCoins,
  chargedCoins,
  bonusCoins,
  isVip,
  subscription,
  transactionHistory,
  hasMoreTransactions = false,
  loadingMoreTransactions = false,
  onLoadMoreTransactions,
  currentUser,
  themePreference,
  systemTheme,
  onThemePreferenceChange,
  onLoginSuccess,
  onLogout,
}) => {
  const [activeSubTab, setActiveSubTab] = useState<'settings' | 'history'>('settings');
  const [authMode, setAuthMode] = useState<'none' | 'login' | 'signup'>('none');
  const [activeLegalModal, setActiveLegalModal] = useState<LegalModalType | null>(null);

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [nickname, setNickname] = useState('');
  const [authError, setAuthError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const { showToast } = useToast();
  const authDialogRef = useRef<HTMLDialogElement>(null);
  const pressedOutsideRef = useRef(false);

  const authTitleId = useId();
  const emailInputId = useId();
  const passwordInputId = useId();
  const nicknameInputId = useId();

  useEffect(() => {
    const dialog = authDialogRef.current;
    if (!dialog) return;

    if (authMode !== 'none' && !dialog.open) {
      dialog.showModal();
    } else if (authMode === 'none' && dialog.open) {
      dialog.close();
    }
  }, [authMode]);

  const isGuest = !currentUser || !currentUser.email;

  const closeAuthModal = () => {
    setAuthMode('none');
    setAuthError(null);
    setEmail('');
    setPassword('');
    setNickname('');
  };

  const handleAuthSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setAuthError(null);
    setIsSubmitting(true);

    try {
      if (authMode === 'login') {
        const res = await api.login({ email, password });
        onLoginSuccess(res.token, res.user);
        showToast('Welcome back!', 'success');
        closeAuthModal();
      } else if (authMode === 'signup') {
        if (isGuest) {
          const res = await api.bindEmail({ email, password, nickname });
          onLoginSuccess(res.token, res.user);
          showToast('Account secured! Your coins and progress are saved.', 'success');
        } else {
          const res = await api.register({ email, password, nickname });
          onLoginSuccess(res.token, res.user);
          showToast('Account created successfully!', 'success');
        }
        closeAuthModal();
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Authentication failed. Please check your credentials.';
      setAuthError(msg);
    } finally {
      setIsSubmitting(false);
    }
  };

  const isPointerOutside = (e: React.PointerEvent<HTMLDialogElement>, dialog: HTMLDialogElement) => {
    const r = dialog.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  };

  const vipSummary = isVip
    ? subscription?.current_period_end
      ? `VIP Active · Until ${new Date(subscription.current_period_end).toLocaleDateString()}`
      : 'VIP Unlimited Active'
    : 'Standard Reader';

  return (
    <div className="scroll-container animate-fade-in">
      <div className="sn-narrow-container">
        {/* Profile + Wallet Cards (1 col mobile, 2 cols tablet+) */}
        <div className="profile-top-grid">
          <div className="profile-card">
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px', flex: 1, minWidth: 0 }}>
              <div className={`profile-avatar ${isGuest ? 'guest' : ''}`}>
                {isGuest ? 'G' : (currentUser?.nickname ? currentUser.nickname[0].toUpperCase() : 'U')}
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <h2 style={{ fontFamily: 'var(--font-display)', fontSize: '1.0625rem', fontWeight: 600, color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {currentUser?.nickname || 'StarNovel Guest'}
                </h2>
                <p style={{ fontSize: '0.75rem', color: 'var(--text-tertiary)', marginTop: '3px', lineHeight: 1.4, overflowWrap: 'anywhere' }}>
                  {isGuest ? 'Guest session · Sign up to sync across devices' : currentUser.email}
                </p>
              </div>
            </div>
            {isGuest ? (
              <button
                type="button"
                className="btn-cta-primary"
                style={{ minHeight: '36px', padding: '6px 14px', borderRadius: '999px', fontSize: '0.75rem', flexShrink: 0 }}
                onClick={() => {
                  setAuthMode('signup');
                  setAuthError(null);
                }}
              >
                Sign In
              </button>
            ) : (
              <button
                type="button"
                className="btn-cta-secondary"
                style={{ minHeight: '36px', padding: '6px 14px', borderRadius: '999px', fontSize: '0.75rem', color: '#DC2626', flexShrink: 0 }}
                onClick={onLogout}
              >
                Sign Out
              </button>
            )}
          </div>

          <div className="profile-wallet-card">
            <div style={{ minWidth: 0 }}>
              <span style={{ fontSize: '0.6875rem', color: 'var(--accent-text)', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                Wallet Balance
              </span>
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginTop: '4px' }}>
                <GoldCoin size={20} />
                <span className="tabular-nums" style={{ fontSize: '1.5rem', fontWeight: 700, color: 'var(--text-primary)' }}>
                  {userCoins.toLocaleString()}
                </span>
                <span style={{ fontSize: '0.8125rem', color: 'var(--text-secondary)' }}>Coins</span>
              </div>
              <div className="tabular-nums" style={{ fontSize: '0.6875rem', color: 'var(--text-tertiary)', marginTop: '4px' }}>
                {chargedCoins.toLocaleString()} Purchased · {bonusCoins.toLocaleString()} Bonus
              </div>
            </div>
            <button
              type="button"
              className="btn-cta-primary"
              onClick={() => onNavigate('recharge')}
              style={{ borderRadius: '999px', minHeight: '40px', padding: '8px 18px', flexShrink: 0 }}
            >
              Top Up
            </button>
          </div>
        </div>

        {/* Sub Tabs: Settings vs Coin History */}
        <div className="sub-tabs-container" role="tablist">
          <button
            type="button"
            role="tab"
            aria-selected={activeSubTab === 'settings'}
            className={`sub-tab-btn ${activeSubTab === 'settings' ? 'active' : ''}`}
            onClick={() => setActiveSubTab('settings')}
          >
            Account &amp; Preferences
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeSubTab === 'history'}
            className={`sub-tab-btn ${activeSubTab === 'history' ? 'active' : ''}`}
            onClick={() => setActiveSubTab('history')}
          >
            Coin History
          </button>
        </div>

        {activeSubTab === 'settings' ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
            {/* Membership & Appearance */}
            <div className="settings-list">
              <button type="button" className="settings-item" onClick={() => onNavigate('recharge')}>
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: '8px' }}>
                  <Crown size={16} strokeWidth={1.8} style={{ color: 'var(--accent-color)' }} aria-hidden="true" />
                  <span>Membership Status</span>
                </span>
                <span className="settings-item-value" style={{ color: isVip ? 'var(--accent-text)' : 'var(--text-tertiary)', fontWeight: isVip ? 600 : 400 }}>
                  <span>{vipSummary}</span>
                  <ChevronRight size={15} strokeWidth={1.8} aria-hidden="true" />
                </span>
              </button>

              <div className="settings-item settings-item-static settings-item-wrap">
                <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                  <span>Appearance</span>
                  <span style={{ fontSize: '0.6875rem', color: 'var(--text-tertiary)', fontWeight: 400 }}>
                    {themePreference === 'system'
                      ? `Following OS (${systemTheme === 'dark' ? 'Dark' : 'Light'})`
                      : `Pinned to ${themePreference === 'dark' ? 'Dark' : 'Light'} mode`}
                  </span>
                </div>
                <div className="theme-segmented" role="group" aria-label="Appearance theme">
                  <button
                    type="button"
                    className={`theme-seg-btn ${themePreference === 'light' ? 'active' : ''}`}
                    onClick={() => onThemePreferenceChange('light')}
                    aria-pressed={themePreference === 'light'}
                  >
                    <Sun size={13} strokeWidth={1.9} aria-hidden="true" />
                    <span>Light</span>
                  </button>
                  <button
                    type="button"
                    className={`theme-seg-btn ${themePreference === 'dark' ? 'active' : ''}`}
                    onClick={() => onThemePreferenceChange('dark')}
                    aria-pressed={themePreference === 'dark'}
                  >
                    <Moon size={13} strokeWidth={1.9} aria-hidden="true" />
                    <span>Dark</span>
                  </button>
                  <button
                    type="button"
                    className={`theme-seg-btn ${themePreference === 'system' ? 'active' : ''}`}
                    onClick={() => onThemePreferenceChange('system')}
                    aria-pressed={themePreference === 'system'}
                  >
                    <Monitor size={13} strokeWidth={1.9} aria-hidden="true" />
                    <span>System</span>
                  </button>
                </div>
              </div>

              <button type="button" className="settings-item" onClick={() => onNavigate('rewards')}>
                <span>Daily Rewards</span>
                <span className="settings-item-value">
                  <span>Check-in</span>
                  <ChevronRight size={15} strokeWidth={1.8} aria-hidden="true" />
                </span>
              </button>

              <button type="button" className="settings-item" onClick={() => onNavigate('shelf')}>
                <span>Saved Library</span>
                <span className="settings-item-value">
                  <span>{shelfBookIds.length} {shelfBookIds.length === 1 ? 'book' : 'books'}</span>
                  <ChevronRight size={15} strokeWidth={1.8} aria-hidden="true" />
                </span>
              </button>
            </div>

            {/* Support & Legal */}
            <div>
              <h3 className="hot-tags-title">Support &amp; Legal Policies</h3>
              <div className="settings-list">
                <button type="button" className="settings-item" onClick={() => setActiveLegalModal('contact')}>
                  <span>Contact Us &amp; Customer Support</span>
                  <span className="settings-item-value">
                    <span>Support</span>
                    <ChevronRight size={15} strokeWidth={1.8} aria-hidden="true" />
                  </span>
                </button>
                <button type="button" className="settings-item" onClick={() => setActiveLegalModal('refund')}>
                  <span>Refund &amp; Purchase Policy</span>
                  <span className="settings-item-value">
                    <span>View</span>
                    <ChevronRight size={15} strokeWidth={1.8} aria-hidden="true" />
                  </span>
                </button>
                <button type="button" className="settings-item" onClick={() => setActiveLegalModal('terms')}>
                  <span>Terms of Service</span>
                  <span className="settings-item-value">
                    <span>View</span>
                    <ChevronRight size={15} strokeWidth={1.8} aria-hidden="true" />
                  </span>
                </button>
                <button type="button" className="settings-item" onClick={() => setActiveLegalModal('privacy')}>
                  <span>Privacy Policy</span>
                  <span className="settings-item-value">
                    <span>View</span>
                    <ChevronRight size={15} strokeWidth={1.8} aria-hidden="true" />
                  </span>
                </button>
              </div>
            </div>
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
            {transactionHistory.length > 0 ? (
              <>
                {transactionHistory.map((item) => (
                  <div
                    key={item.id}
                    style={{
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                      gap: '12px',
                      padding: '14px 16px',
                      backgroundColor: 'var(--bg-secondary)',
                      borderRadius: '12px',
                      border: '1px solid var(--border-color)',
                    }}
                  >
                    <div style={{ minWidth: 0 }}>
                      <div style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--text-primary)' }}>
                        {item.desc}
                      </div>
                      <span style={{ fontSize: '0.6875rem', color: 'var(--text-tertiary)', marginTop: '3px', display: 'block' }}>
                        {new Date(item.date).toLocaleString()}
                      </span>
                    </div>
                    <span
                      className="tabular-nums"
                      style={{
                        fontSize: '0.9375rem',
                        fontWeight: 700,
                        color: item.type === 'credit' ? '#16a34a' : '#dc2626',
                        flexShrink: 0,
                      }}
                    >
                      {item.type === 'credit' ? '+' : '-'}{item.amount.toLocaleString()}
                    </span>
                  </div>
                ))}
                {hasMoreTransactions && onLoadMoreTransactions && (
                  <div style={{ display: 'flex', justifyContent: 'center', marginTop: '8px' }}>
                    <button
                      type="button"
                      className="btn-cta-secondary"
                      disabled={loadingMoreTransactions}
                      onClick={onLoadMoreTransactions}
                    >
                      {loadingMoreTransactions ? 'Loading...' : 'Load More History'}
                    </button>
                  </div>
                )}
              </>
            ) : (
              <div className="shelf-empty-state">
                <div className="shelf-empty-icon-wrap">
                  <GoldCoin size={28} />
                </div>
                <p style={{ fontWeight: 600, color: 'var(--text-primary)' }}>No transaction history yet</p>
                <p style={{ fontSize: '0.8125rem' }}>
                  Top up coins or claim daily rewards to unlock paid chapters.
                </p>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Native <dialog> Auth Modal */}
      <dialog
        ref={authDialogRef}
        className="sn-dialog"
        aria-labelledby={authTitleId}
        onCancel={(e) => {
          e.preventDefault();
          closeAuthModal();
        }}
        onPointerDown={(e) => {
          if (authDialogRef.current) {
            pressedOutsideRef.current = isPointerOutside(e, authDialogRef.current);
          }
        }}
        onPointerUp={(e) => {
          if (authDialogRef.current && pressedOutsideRef.current && isPointerOutside(e, authDialogRef.current)) {
            closeAuthModal();
          }
          pressedOutsideRef.current = false;
        }}
        style={{ width: '100%', maxWidth: '380px', padding: '24px' }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '14px' }}>
          <h2 id={authTitleId} style={{ fontFamily: 'var(--font-display)', fontSize: '1.25rem', fontWeight: 600 }}>
            {authMode === 'signup' ? 'Create Account' : 'Sign In'}
          </h2>
          <button
            type="button"
            className="header-btn"
            onClick={closeAuthModal}
            aria-label="Close dialog"
          >
            <X size={18} strokeWidth={1.8} aria-hidden="true" />
          </button>
        </div>

        <div className="sub-tabs-container" style={{ marginBottom: '14px' }}>
          <button
            type="button"
            className={`sub-tab-btn ${authMode === 'signup' ? 'active' : ''}`}
            onClick={() => {
              setAuthMode('signup');
              setAuthError(null);
            }}
          >
            Sign Up
          </button>
          <button
            type="button"
            className={`sub-tab-btn ${authMode === 'login' ? 'active' : ''}`}
            onClick={() => {
              setAuthMode('login');
              setAuthError(null);
            }}
          >
            Sign In
          </button>
        </div>

        <p style={{ fontSize: '0.75rem', color: 'var(--text-tertiary)', marginBottom: '16px', lineHeight: 1.5 }}>
          {authMode === 'signup'
            ? 'Bind an email to protect your coins, VIP pass, and reading library across devices.'
            : 'Sign in to restore your existing StarNovel account and library.'}
        </p>

        <form onSubmit={handleAuthSubmit} style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
          <div>
            <label htmlFor={emailInputId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
              Email Address
            </label>
            <input
              id={emailInputId}
              type="email"
              autoComplete="email"
              className="sn-input"
              value={email}
              placeholder="name@example.com"
              onChange={(e) => setEmail(e.target.value)}
              required
            />
          </div>

          <div>
            <label htmlFor={passwordInputId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
              {authMode === 'login' ? 'Password' : 'Password (min. 6 characters)'}
            </label>
            <input
              id={passwordInputId}
              type="password"
              autoComplete={authMode === 'login' ? 'current-password' : 'new-password'}
              className="sn-input"
              value={password}
              placeholder="Enter password"
              onChange={(e) => setPassword(e.target.value)}
              required
              minLength={6}
            />
          </div>

          {authMode === 'signup' && (
            <div>
              <label htmlFor={nicknameInputId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
                Nickname (Optional)
              </label>
              <input
                id={nicknameInputId}
                type="text"
                autoComplete="nickname"
                placeholder="Up to 50 characters"
                className="sn-input"
                value={nickname}
                onChange={(e) => setNickname(e.target.value)}
              />
            </div>
          )}

          {authError && (
            <div
              role="alert"
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '6px',
                color: '#DC2626',
                fontSize: '0.75rem',
                marginTop: '2px',
              }}
            >
              <AlertCircle size={14} strokeWidth={2} aria-hidden="true" style={{ flexShrink: 0 }} />
              <span>{authError}</span>
            </div>
          )}

          <button
            type="submit"
            disabled={isSubmitting}
            className="btn-cta-primary"
            style={{ width: '100%', borderRadius: '999px', marginTop: '6px' }}
          >
            {isSubmitting ? 'Processing...' : authMode === 'signup' ? 'Sign Up & Save' : 'Sign In'}
          </button>
        </form>

        <div style={{ marginTop: '14px', textAlign: 'center', fontSize: '0.75rem', color: 'var(--text-secondary)' }}>
          {authMode === 'login' ? (
            <>
              New to StarNovel?{' '}
              <button
                type="button"
                onClick={() => {
                  setAuthMode('signup');
                  setAuthError(null);
                }}
                style={{ background: 'none', border: 'none', color: 'var(--accent-text)', fontWeight: 600, cursor: 'pointer', padding: 0 }}
              >
                Sign Up
              </button>
            </>
          ) : (
            <>
              Already have an account?{' '}
              <button
                type="button"
                onClick={() => {
                  setAuthMode('login');
                  setAuthError(null);
                }}
                style={{ background: 'none', border: 'none', color: 'var(--accent-text)', fontWeight: 600, cursor: 'pointer', padding: 0 }}
              >
                Sign In
              </button>
            </>
          )}
        </div>
      </dialog>

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

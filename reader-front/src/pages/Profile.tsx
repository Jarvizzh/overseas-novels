import React, { useState, useEffect, useRef, useId } from 'react';
import { ChevronRight, Sun, Moon, Monitor, Crown, X, AlertCircle, Pencil } from 'lucide-react';
import { useToast } from '../context/toastStore';
import { GoldCoin } from '../components/GoldCoin';
import { api } from '../utils/api';
import type { User, Transaction, SubscriptionInfo } from '../utils/api';
import { LegalModal, type LegalModalType } from '../components/LegalModal';
import type { ThemePreference } from '../utils/theme';

interface ProfileProps {
  shelfBookIds: number[];
  recentBookCount?: number;
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
  onProfileUpdated: (user: User, token?: string) => void;
  onLogout: () => void;
}

export const Profile: React.FC<ProfileProps> = ({
  shelfBookIds,
  recentBookCount = 0,
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
  onProfileUpdated,
  onLogout,
}) => {
  const [activeSubTab, setActiveSubTab] = useState<'settings' | 'history'>('settings');
  const [authMode, setAuthMode] = useState<'none' | 'login' | 'signup'>('none');
  const [activeLegalModal, setActiveLegalModal] = useState<LegalModalType | null>(null);

  const [email, setEmail] = useState('');
  const [emailTouched, setEmailTouched] = useState(false);
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [confirmTouched, setConfirmTouched] = useState(false);
  const [nickname, setNickname] = useState('');
  const [authError, setAuthError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  // Edit profile modal state
  const [editOpen, setEditOpen] = useState(false);
  const [editNickname, setEditNickname] = useState('');
  const [editEmail, setEditEmail] = useState('');
  const [editEmailTouched, setEditEmailTouched] = useState(false);
  const [editCurrentPassword, setEditCurrentPassword] = useState('');
  const [editNewPassword, setEditNewPassword] = useState('');
  const [editConfirmPassword, setEditConfirmPassword] = useState('');
  const [editConfirmTouched, setEditConfirmTouched] = useState(false);
  const [editError, setEditError] = useState<string | null>(null);
  const [isUpdatingProfile, setIsUpdatingProfile] = useState(false);

  const { showToast } = useToast();
  const authDialogRef = useRef<HTMLDialogElement>(null);
  const editDialogRef = useRef<HTMLDialogElement>(null);
  const confirmInputRef = useRef<HTMLInputElement>(null);
  const pressedOutsideRef = useRef(false);

  const authTitleId = useId();
  const emailInputId = useId();
  const emailErrorId = useId();
  const passwordInputId = useId();
  const confirmPasswordInputId = useId();
  const confirmErrorId = useId();
  const nicknameInputId = useId();

  const editTitleId = useId();
  const editNicknameId = useId();
  const editEmailId = useId();
  const editEmailErrorId = useId();
  const editNewPasswordId = useId();
  const editConfirmPasswordId = useId();
  const editConfirmErrorId = useId();
  const editCurrentPasswordId = useId();

  useEffect(() => {
    const dialog = authDialogRef.current;
    if (!dialog) return;

    if (authMode !== 'none' && !dialog.open) {
      dialog.showModal();
    } else if (authMode === 'none' && dialog.open) {
      dialog.close();
    }
  }, [authMode]);

  useEffect(() => {
    const dialog = editDialogRef.current;
    if (!dialog) return;

    if (editOpen && !dialog.open) {
      dialog.showModal();
    } else if (!editOpen && dialog.open) {
      dialog.close();
    }
  }, [editOpen]);

  const isGuest = !currentUser || !currentUser.email;

  useEffect(() => {
    if (isGuest && editOpen) {
      setEditOpen(false);
    }
  }, [isGuest, editOpen]);

  const isValidEmailFormat = (value: string) => {
    const trimmed = value.trim();
    return trimmed.length > 0 && trimmed.length <= 255 && /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(trimmed);
  };

  const authEmailInvalid =
    authMode !== 'none' &&
    emailTouched &&
    email.trim().length > 0 &&
    !isValidEmailFormat(email);

  const editEmailInvalid =
    editOpen &&
    !isGuest &&
    editEmailTouched &&
    editEmail.trim().length > 0 &&
    !isValidEmailFormat(editEmail);

  const confirmMismatch =
    authMode === 'signup' &&
    confirmTouched &&
    confirmPassword.length > 0 &&
    confirmPassword !== password;

  const editConfirmMismatch =
    editOpen &&
    editNewPassword.length > 0 &&
    editConfirmTouched &&
    editConfirmPassword !== editNewPassword;

  const closeAuthModal = () => {
    setAuthMode('none');
    setAuthError(null);
    setEmail('');
    setEmailTouched(false);
    setPassword('');
    setConfirmPassword('');
    setConfirmTouched(false);
    setNickname('');
  };

  const openEditModal = () => {
    if (isGuest) return;
    setEditNickname(currentUser?.nickname || '');
    setEditEmail(currentUser?.email || '');
    setEditEmailTouched(false);
    setEditCurrentPassword('');
    setEditNewPassword('');
    setEditConfirmPassword('');
    setEditConfirmTouched(false);
    setEditError(null);
    setEditOpen(true);
  };

  const closeEditModal = () => {
    setEditOpen(false);
    setEditError(null);
    setEditEmailTouched(false);
    setEditCurrentPassword('');
    setEditNewPassword('');
    setEditConfirmPassword('');
    setEditConfirmTouched(false);
  };

  const handleAuthSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setAuthError(null);

    if (!isValidEmailFormat(email)) {
      setEmailTouched(true);
      setAuthError('Please enter a valid email address.');
      return;
    }

    if (authMode === 'signup' && password !== confirmPassword) {
      setConfirmTouched(true);
      setAuthError('Passwords do not match. Please re-enter to confirm.');
      confirmInputRef.current?.focus();
      return;
    }

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

  const trimmedEditEmail = editEmail.trim();
  const isSensitiveProfileChange =
    !isGuest &&
    (trimmedEditEmail !== (currentUser?.email || '') || editNewPassword.length > 0);

  const handleEditSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (isGuest) return;
    setEditError(null);

    const trimmedNickname = editNickname.trim();
    if (!trimmedNickname) {
      setEditError('Nickname cannot be empty.');
      return;
    }
    if (!isGuest && !isValidEmailFormat(trimmedEditEmail)) {
      setEditEmailTouched(true);
      setEditError('Please enter a valid email address.');
      return;
    }
    if (!isGuest && editNewPassword.length > 0 && editNewPassword !== editConfirmPassword) {
      setEditConfirmTouched(true);
      setEditError('New passwords do not match.');
      return;
    }
    if (isSensitiveProfileChange && !editCurrentPassword) {
      setEditError('Please enter your current password to save email or password changes.');
      return;
    }

    setIsUpdatingProfile(true);
    try {
      const payload: {
        nickname?: string;
        email?: string;
        current_password?: string;
        new_password?: string;
      } = {
        nickname: trimmedNickname,
      };
      if (!isGuest) {
        if (trimmedEditEmail && trimmedEditEmail !== (currentUser?.email || '')) {
          payload.email = trimmedEditEmail;
        }
        if (editNewPassword) {
          payload.new_password = editNewPassword;
        }
        if (isSensitiveProfileChange) {
          payload.current_password = editCurrentPassword;
        }
      }
      const res = await api.updateProfile(payload);
      onProfileUpdated(res.user, res.token);
      showToast('Profile updated successfully!', 'success');
      closeEditModal();
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Failed to update profile.';
      setEditError(msg);
    } finally {
      setIsUpdatingProfile(false);
    }
  };

  const isPointerOutside = (e: React.PointerEvent<HTMLDialogElement>, dialog: HTMLDialogElement) => {
    const r = dialog.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  };

  const paidThroughDate = subscription?.paid_through || subscription?.current_period_end;
  const vipSummary = subscription?.normalized_status === 'cancel_pending'
    ? paidThroughDate
      ? `Cancel Pending · Through ${new Date(paidThroughDate).toLocaleDateString()}`
      : 'Cancellation Pending'
    : subscription?.normalized_status === 'canceled_with_paid_period'
      ? paidThroughDate
        ? `Auto-Renew Off · Through ${new Date(paidThroughDate).toLocaleDateString()}`
        : 'Auto-Renew Off'
      : subscription?.normalized_status === 'renewal_failed'
        ? 'Renewal Payment Issue'
        : isVip
          ? paidThroughDate
            ? `Subscription Active · Until ${new Date(paidThroughDate).toLocaleDateString()}`
            : 'Subscription Active'
          : 'Manage Subscription';

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
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', flexShrink: 0 }}>
              {!isGuest && (
                <button
                  type="button"
                  className="btn-cta-secondary"
                  style={{ minHeight: '36px', padding: '6px 12px', borderRadius: '999px', fontSize: '0.75rem', display: 'inline-flex', alignItems: 'center', gap: '5px' }}
                  onClick={openEditModal}
                  aria-label="Edit profile"
                >
                  <Pencil size={13} strokeWidth={1.85} aria-hidden="true" />
                  <span>Edit</span>
                </button>
              )}
              {isGuest ? (
                <button
                  type="button"
                  className="btn-cta-primary"
                  style={{ minHeight: '36px', padding: '6px 14px', borderRadius: '999px', fontSize: '0.75rem' }}
                  onClick={() => {
                    setAuthMode('signup');
                    setAuthError(null);
                    setConfirmPassword('');
                    setConfirmTouched(false);
                  }}
                >
                  Sign In
                </button>
              ) : (
                <button
                  type="button"
                  className="btn-cta-secondary"
                  style={{ minHeight: '36px', padding: '6px 14px', borderRadius: '999px', fontSize: '0.75rem', color: '#DC2626' }}
                  onClick={onLogout}
                >
                  Sign Out
                </button>
              )}
            </div>
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
              {!isGuest && (
                <button type="button" className="settings-item" onClick={openEditModal}>
                  <span>Account Profile</span>
                  <span className="settings-item-value">
                    <span>Nickname, Email &amp; Password</span>
                    <ChevronRight size={15} strokeWidth={1.8} aria-hidden="true" />
                  </span>
                </button>
              )}

              <button type="button" className="settings-item" onClick={() => onNavigate('subscription')}>
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: '8px' }}>
                  <Crown size={16} strokeWidth={1.8} style={{ color: 'var(--accent-color)' }} aria-hidden="true" />
                  <span>My Subscription</span>
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

              <button type="button" className="settings-item" onClick={() => onNavigate('shelf', { tab: 'recent' })}>
                <span>Recently Read</span>
                <span className="settings-item-value">
                  <span>{recentBookCount} {recentBookCount === 1 ? 'book' : 'books'}</span>
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
              setEmailTouched(false);
              setConfirmPassword('');
              setConfirmTouched(false);
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
              setEmailTouched(false);
              setConfirmPassword('');
              setConfirmTouched(false);
            }}
          >
            Sign In
          </button>
        </div>

        <p style={{ fontSize: '0.75rem', color: 'var(--text-tertiary)', marginBottom: '16px', lineHeight: 1.5 }}>
          {authMode === 'signup'
            ? 'Bind an email to protect your coins, subscription, and reading library across devices.'
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
              spellCheck={false}
              className="sn-input"
              value={email}
              maxLength={255}
              placeholder="name@example.com"
              aria-invalid={authEmailInvalid}
              aria-describedby={authEmailInvalid ? emailErrorId : undefined}
              style={authEmailInvalid ? { borderColor: '#DC2626' } : undefined}
              onChange={(e) => setEmail(e.target.value)}
              onBlur={() => setEmailTouched(true)}
              required
            />
            {authEmailInvalid && (
              <p id={emailErrorId} role="status" style={{ fontSize: '0.72rem', color: '#DC2626', marginTop: '4px', marginBottom: 0 }}>
                Please enter a valid email address.
              </p>
            )}
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
              maxLength={72}
            />
          </div>

          {authMode === 'signup' && (
            <>
              <div>
                <label htmlFor={confirmPasswordInputId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
                  Confirm Password
                </label>
                <input
                  ref={confirmInputRef}
                  id={confirmPasswordInputId}
                  type="password"
                  autoComplete="new-password"
                  className="sn-input"
                  value={confirmPassword}
                  placeholder="Re-enter password"
                  aria-invalid={confirmMismatch}
                  aria-describedby={confirmMismatch ? confirmErrorId : undefined}
                  style={confirmMismatch ? { borderColor: '#DC2626' } : undefined}
                  onChange={(e) => setConfirmPassword(e.target.value)}
                  onBlur={() => setConfirmTouched(true)}
                  required
                  minLength={6}
                  maxLength={72}
                />
                {confirmMismatch && (
                  <p id={confirmErrorId} role="status" style={{ fontSize: '0.72rem', color: '#DC2626', marginTop: '4px', marginBottom: 0 }}>
                    Passwords do not match.
                  </p>
                )}
              </div>

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
                  maxLength={50}
                  onChange={(e) => setNickname(e.target.value)}
                />
              </div>
            </>
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
                  setEmailTouched(false);
                  setConfirmPassword('');
                  setConfirmTouched(false);
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
                  setEmailTouched(false);
                  setConfirmPassword('');
                  setConfirmTouched(false);
                }}
                style={{ background: 'none', border: 'none', color: 'var(--accent-text)', fontWeight: 600, cursor: 'pointer', padding: 0 }}
              >
                Sign In
              </button>
            </>
          )}
        </div>
      </dialog>

      {/* Edit Profile Modal (<dialog>) */}
      <dialog
        ref={editDialogRef}
        className="sn-dialog"
        aria-labelledby={editTitleId}
        onCancel={(e) => {
          e.preventDefault();
          closeEditModal();
        }}
        onPointerDown={(e) => {
          if (editDialogRef.current) {
            pressedOutsideRef.current = isPointerOutside(e, editDialogRef.current);
          }
        }}
        onPointerUp={(e) => {
          if (editDialogRef.current && pressedOutsideRef.current && isPointerOutside(e, editDialogRef.current)) {
            closeEditModal();
          }
          pressedOutsideRef.current = false;
        }}
        style={{ width: '100%', maxWidth: '400px', padding: '24px' }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '14px' }}>
          <h2 id={editTitleId} style={{ fontFamily: 'var(--font-display)', fontSize: '1.25rem', fontWeight: 600 }}>
            Edit Profile
          </h2>
          <button type="button" className="header-btn" onClick={closeEditModal} aria-label="Close dialog">
            <X size={18} strokeWidth={1.8} aria-hidden="true" />
          </button>
        </div>

        <p style={{ fontSize: '0.75rem', color: 'var(--text-tertiary)', marginBottom: '16px', lineHeight: 1.5 }}>
          Update your nickname, email address, or password. Changing email or password requires your current password.
        </p>

        <form onSubmit={handleEditSubmit} style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
          <div>
            <label htmlFor={editNicknameId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
              Nickname
            </label>
            <input
              id={editNicknameId}
              type="text"
              autoComplete="nickname"
              className="sn-input"
              value={editNickname}
              maxLength={50}
              placeholder="Up to 50 characters"
              onChange={(e) => setEditNickname(e.target.value)}
              required
            />
          </div>

          {!isGuest && (
            <>
              <div>
                <label htmlFor={editEmailId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
                  Email Address
                </label>
                <input
                  id={editEmailId}
                  type="email"
                  autoComplete="email"
                  spellCheck={false}
                  className="sn-input"
                  value={editEmail}
                  maxLength={255}
                  placeholder="name@example.com"
                  aria-invalid={editEmailInvalid}
                  aria-describedby={editEmailInvalid ? editEmailErrorId : undefined}
                  style={editEmailInvalid ? { borderColor: '#DC2626' } : undefined}
                  onChange={(e) => setEditEmail(e.target.value)}
                  onBlur={() => setEditEmailTouched(true)}
                  required
                />
                {editEmailInvalid && (
                  <p id={editEmailErrorId} role="status" style={{ fontSize: '0.72rem', color: '#DC2626', marginTop: '4px', marginBottom: 0 }}>
                    Please enter a valid email address.
                  </p>
                )}
              </div>

              <div>
                <label htmlFor={editNewPasswordId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
                  New Password (Optional, min. 6 characters)
                </label>
                <input
                  id={editNewPasswordId}
                  type="password"
                  autoComplete="new-password"
                  className="sn-input"
                  value={editNewPassword}
                  placeholder="Leave blank to keep current password"
                  minLength={6}
                  maxLength={72}
                  onChange={(e) => {
                    const val = e.target.value;
                    setEditNewPassword(val);
                    if (!val) {
                      setEditConfirmPassword('');
                      setEditConfirmTouched(false);
                    }
                  }}
                />
              </div>

              {editNewPassword.length > 0 && (
                <div>
                  <label htmlFor={editConfirmPasswordId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
                    Confirm New Password
                  </label>
                  <input
                    id={editConfirmPasswordId}
                    type="password"
                    autoComplete="new-password"
                    className="sn-input"
                    value={editConfirmPassword}
                    placeholder="Re-enter new password"
                    aria-invalid={editConfirmMismatch}
                    aria-describedby={editConfirmMismatch ? editConfirmErrorId : undefined}
                    style={editConfirmMismatch ? { borderColor: '#DC2626' } : undefined}
                    minLength={6}
                    maxLength={72}
                    required
                    onChange={(e) => setEditConfirmPassword(e.target.value)}
                    onBlur={() => setEditConfirmTouched(true)}
                  />
                  {editConfirmMismatch && (
                    <p id={editConfirmErrorId} role="status" style={{ fontSize: '0.72rem', color: '#DC2626', marginTop: '4px', marginBottom: 0 }}>
                      New passwords do not match.
                    </p>
                  )}
                </div>
              )}

              {isSensitiveProfileChange && (
                <div>
                  <label htmlFor={editCurrentPasswordId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
                    Current Password (required for email/password change)
                  </label>
                  <input
                    id={editCurrentPasswordId}
                    type="password"
                    autoComplete="current-password"
                    className="sn-input"
                    value={editCurrentPassword}
                    placeholder="Enter current password"
                    required
                    onChange={(e) => setEditCurrentPassword(e.target.value)}
                  />
                </div>
              )}
            </>
          )}

          {editError && (
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
              <span>{editError}</span>
            </div>
          )}

          <button
            type="submit"
            disabled={isUpdatingProfile}
            className="btn-cta-primary"
            style={{ width: '100%', borderRadius: '999px', marginTop: '6px' }}
          >
            {isUpdatingProfile ? 'Saving...' : 'Save Changes'}
          </button>
        </form>
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

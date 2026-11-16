import React, { useState, useEffect, useCallback, useRef, useId } from 'react';
import { ChevronLeft, Crown, Check, Sparkles, BookOpen } from 'lucide-react';
import { useToast } from '../context/toastStore';
import { GoldCoin } from '../components/GoldCoin';
import { api, ApiError, type SubscriptionInfo } from '../utils/api';
import { LegalModal, type LegalModalType } from '../components/LegalModal';

interface RechargeProps {
  authReady?: boolean;
  userCoins?: number;
  novelId?: number;
  novelTitle?: string;
  onAddCoins: (amount: number, reason: string) => void | Promise<void>;
  onRefreshStatus?: () => Promise<void>;
  onBack: () => void;
  onNavigate?: (pageOrPath: string, params?: Record<string, unknown>) => void;
}

function stripPayPalQueryParams(): void {
  try {
    const clean = new URL(window.location.href);
    ['payment', 'token', 'subscription_id', 'ba_token', 'PayerID'].forEach((k) => clean.searchParams.delete(k));
    const nextSearch = clean.searchParams.toString();
    window.history.replaceState({}, '', `${clean.pathname}${nextSearch ? `?${nextSearch}` : ''}`);
  } catch {
    // Fallback safely if URL manipulation fails
  }
}

export const Recharge: React.FC<RechargeProps> = ({
  authReady = true,
  onAddCoins,
  novelId,
  novelTitle,
  onRefreshStatus,
  onBack,
  onNavigate,
}) => {
  const { showToast } = useToast();
  const [selectedPack, setSelectedPack] = useState<number | null>(null);
  const [isProcessing, setIsProcessing] = useState(false);
  const [paymentStep, setPaymentStep] = useState(0);
  const [showSuccess, setShowSuccess] = useState(false);
  const [successAmount, setSuccessAmount] = useState(0);
  const [isWholeBookSuccess, setIsWholeBookSuccess] = useState(false);
  const paymentReturnRef = useRef('');
  const hasReturnedFromPayPalRef = useRef(false);
  const pendingReturnPathRef = useRef('');
  const [isVipSuccess, setIsVipSuccess] = useState(false);
  const [vipSuccessTitle, setVipSuccessTitle] = useState('');
  const [activeLegalModal, setActiveLegalModal] = useState<LegalModalType | null>(null);

  const [isVIP, setIsVIP] = useState(false);
  const [userSubscription, setUserSubscription] = useState<SubscriptionInfo | null>(null);

  const [slots, setSlots] = useState<Array<{
    id: number;
    template_id: number;
    slot_index: number;
    type: 'single' | 'subscription' | 'vip' | 'whole_book';
    coins: number;
    bonus: number;
    vip_duration: string;
    subscription_cycle?: string;
    vip_name: string;
    vip_desc: string;
    price: string;
    price_cents: number;
  }>>([]);
  const [loadingTemplates, setLoadingTemplates] = useState(true);
  const [offersNovelId, setOffersNovelId] = useState<number | undefined | null>(null);
  const offersRequestRef = useRef<AbortController | null>(null);

  const successDialogRef = useRef<HTMLDialogElement>(null);
  const pressedOutsideRef = useRef(false);
  const successTitleId = useId();

  useEffect(() => {
    const dialog = successDialogRef.current;
    if (!dialog) return;
    if (showSuccess && !dialog.open) {
      dialog.showModal();
    } else if (!showSuccess && dialog.open) {
      dialog.close();
    }
  }, [showSuccess, loadingTemplates, offersNovelId, novelId]);

  const fetchStatusAndTemplates = useCallback(async () => {
    offersRequestRef.current?.abort();
    const controller = new AbortController();
    offersRequestRef.current = controller;
    setLoadingTemplates(true);
    try {
      const [templateData, subData] = await Promise.all([
        api.getRechargeTemplates(novelId, controller.signal),
        api.getSubscriptionStatus(controller.signal).catch(() => null),
      ]);

      if (controller.signal.aborted) return;
      const slotList = templateData.slots || [];
      setOffersNovelId(novelId);
      setSlots(slotList);

      if (subData?.is_vip) {
        setIsVIP(true);
        setUserSubscription(subData.subscription);
      } else {
        setIsVIP(false);
        setUserSubscription(null);
      }

      if (slotList.length === 0) setSelectedPack(null);
      if (slotList.length > 0) {
        const subSlot = slotList.find((s) => s.type === 'subscription' || s.type === 'vip');
        setSelectedPack((prev) => slotList.some((slot) => slot.id === prev) ? prev : (subSlot || slotList[0]).id);
      }
    } catch (err) {
      if (controller.signal.aborted) return;
      setSlots([]);
      setSelectedPack(null);
      setOffersNovelId(novelId);
      console.error('Failed to load recharge data:', err);
    } finally {
      if (!controller.signal.aborted) setLoadingTemplates(false);
    }
  }, [novelId]);

  useEffect(() => {
    if (!authReady) return;
    void fetchStatusAndTemplates();
    return () => offersRequestRef.current?.abort();
  }, [authReady, fetchStatusAndTemplates]);

  const onAddCoinsRef = useRef(onAddCoins);
  useEffect(() => {
    onAddCoinsRef.current = onAddCoins;
  }, [onAddCoins]);

  const onRefreshStatusRef = useRef(onRefreshStatus);
  useEffect(() => {
    onRefreshStatusRef.current = onRefreshStatus;
  }, [onRefreshStatus]);

  const showToastRef = useRef(showToast);
  useEffect(() => {
    showToastRef.current = showToast;
  }, [showToast]);

  // Handle PayPal return while preserving UTM/fbclid params
  useEffect(() => {
    if (!authReady) return;
    const handlePayPalReturn = async () => {
      const urlParams = new URLSearchParams(window.location.search);
      const paymentStatus = urlParams.get('payment');
      const token = urlParams.get('token');
      const subscriptionId = urlParams.get('subscription_id');

      const consumePendingReturnPath = () => {
        try {
          const saved = sessionStorage.getItem('pending_paypal_recharge');
          if (saved) {
            const parsed = JSON.parse(saved);
            if (typeof parsed.returnContentPath === 'string' && parsed.returnContentPath) {
              pendingReturnPathRef.current = parsed.returnContentPath;
            }
          }
        } catch {
          // Ignore parse errors
        } finally {
          sessionStorage.removeItem('pending_paypal_recharge');
        }
      };

      if (paymentStatus === 'cancel') {
        hasReturnedFromPayPalRef.current = true;
        consumePendingReturnPath();
        showToastRef.current('PayPal payment was cancelled.', 'info');
        stripPayPalQueryParams();
        return;
      }

      if (token || subscriptionId) {
        const paymentId = subscriptionId || token || '';
        if (paymentReturnRef.current === paymentId) return;
        paymentReturnRef.current = paymentId;
        hasReturnedFromPayPalRef.current = true;
        consumePendingReturnPath();
        setIsProcessing(true);
        setPaymentStep(2);
        try {
          if (subscriptionId || paymentId.startsWith('I-')) {
            await api.activateSubscription(paymentId, 'paypal');
            const status = await api.getSubscriptionStatus();
            const active = status.is_vip && status.subscription?.subscription_id === paymentId;
            if (active) {
              setIsVIP(true);
              setIsVipSuccess(true);
              setIsWholeBookSuccess(false);
              setVipSuccessTitle('StarNovel Unlimited');
              setShowSuccess(true);
            } else {
              showToastRef.current('VIP will be available once PayPal confirms payment.', 'info');
            }
            await fetchStatusAndTemplates();
            await onRefreshStatusRef.current?.();
          } else {
            const capture = await api.capturePayPalPayment(paymentId);
            await onAddCoinsRef.current(capture.coins_awarded, 'PayPal purchase');
            setSuccessAmount(capture.coins_awarded);
            setIsVipSuccess(false);
            setIsWholeBookSuccess(capture.order_type === 'whole_book');
            setShowSuccess(true);
          }
          stripPayPalQueryParams();
        } catch (err) {
          const msg = err instanceof Error ? err.message : 'Failed to confirm PayPal payment.';
          showToastRef.current(msg, 'error');
        } finally {
          setIsProcessing(false);
          setPaymentStep(0);
        }
      }
    };

    handlePayPalReturn();
  }, [authReady, fetchStatusAndTemplates]);

  const subscriptionPacks = slots
    .filter((s) => s.type === 'subscription' || s.type === 'vip')
    .map((s) => ({
      id: s.id,
      name:
        s.vip_name ||
        (s.subscription_cycle === 'day'
          ? 'VIP Daily Pass'
          : s.subscription_cycle === 'week'
            ? 'VIP Weekly Pass'
            : 'VIP Monthly Pass'),
      price: s.price,
      priceCents: s.price_cents,
      desc: s.vip_desc || 'Unlimited reading across all titles. Auto-renews, cancel anytime.',
      cycle: s.subscription_cycle || s.vip_duration || 'month',
      type: s.type,
    }));

  const coinPacks = slots
    .filter((s) => s.type === 'single' || (s.type === 'whole_book' && novelId))
    .map((s) => ({
      id: s.id,
      coins: s.coins,
      bonus: s.bonus,
      price: s.price,
      priceCents: s.price_cents,
      total: s.coins + s.bonus,
      name: s.vip_name,
      type: s.type,
    }));

  const handlePurchase = async () => {
    if (selectedPack === null || isProcessing || loadingTemplates || offersNovelId !== novelId) return;
    const tSlot = slots.find((p) => p.id === selectedPack);
    if (!tSlot) return;

    setIsProcessing(true);
    setPaymentStep(1);

    try {
      const isSub = tSlot.type === 'subscription' || tSlot.type === 'vip';
      const lastContentPath = localStorage.getItem('last_read_content_path');

      sessionStorage.setItem(
        'pending_paypal_recharge',
        JSON.stringify({
          slotId: tSlot.id,
          coins: tSlot.coins + tSlot.bonus,
          type: tSlot.type,
          name: tSlot.vip_name || tSlot.price,
          price: tSlot.price,
          time: Date.now(),
          returnContentPath: lastContentPath || '',
        })
      );

      const currentUrl = new URL(window.location.href);
      ['token', 'subscription_id', 'ba_token', 'PayerID', 'payment'].forEach((k) => currentUrl.searchParams.delete(k));
      const returnURL = new URL(currentUrl);
      returnURL.searchParams.set('payment', 'success');
      currentUrl.searchParams.set('payment', 'cancel');
      const returnUrl = returnURL.href;
      const cancelUrl = currentUrl.href;

      if (isSub) {
        const resp = await api.createSubscription(tSlot.id, 'paypal', returnUrl, cancelUrl);
        if (resp?.approve_url) {
          window.location.href = resp.approve_url;
        } else {
          throw new Error('No PayPal subscription URL received');
        }
      } else {
        const resp = await api.createPayPalOrder(tSlot.id, tSlot.type === 'whole_book' ? novelId : 0, returnUrl, cancelUrl);
        if (resp?.approve_url) {
          window.location.href = resp.approve_url;
        } else {
          throw new Error('No PayPal checkout URL received');
        }
      }
    } catch (err) {
      setIsProcessing(false);
      setPaymentStep(0);
      if (err instanceof ApiError && [400, 404, 409].includes(err.status)) {
        await fetchStatusAndTemplates();
      }
      const msg = err instanceof Error ? err.message : 'Failed to initiate PayPal checkout. Please try again.';
      showToast(msg, 'error');
    }
  };

  const selectedSlot = slots.find((p) => p.id === selectedPack);

  const handleReturnToContent = () => {
    let returnPath = pendingReturnPathRef.current;
    if (!returnPath) {
      try {
        const saved = sessionStorage.getItem('pending_paypal_recharge');
        if (saved) {
          const parsed = JSON.parse(saved);
          if (parsed.returnContentPath) {
            returnPath = parsed.returnContentPath;
          }
        }
      } catch {
        // Ignore parse errors
      }
    }

    if (!returnPath) {
      returnPath = localStorage.getItem('last_read_content_path') || '';
    }

    if (returnPath) {
      if (onNavigate) {
        onNavigate(returnPath);
      } else {
        window.location.href = returnPath;
      }
    } else if (novelId && onNavigate) {
      onNavigate('reader', { id: novelId });
    } else if (onNavigate) {
      onNavigate('shelf');
    } else {
      onBack();
    }
  };

  const isPointerOutside = (e: React.PointerEvent<HTMLDialogElement>, dialog: HTMLDialogElement) => {
    const r = dialog.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  };

  if (loadingTemplates || offersNovelId !== novelId) {
    return (
      <div className="page-container-full" style={{ alignItems: 'center', justifyContent: 'center', gap: '12px', color: 'var(--text-secondary)' }}>
        <div className="sn-spinner" />
        <span style={{ fontSize: '0.875rem' }}>Loading membership plans...</span>
      </div>
    );
  }

  return (
    <div className="page-container-full animate-fade-in" style={{ backgroundColor: 'var(--bg-primary)' }}>
      {/* Top Bar */}
      <header className="detail-top-bar glass-panel">
        <div className="sn-narrow-container" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <button
            type="button"
            className="header-btn"
            onClick={() => {
              if (hasReturnedFromPayPalRef.current) {
                handleReturnToContent();
              } else {
                onBack();
              }
            }}
            aria-label="Go back"
          >
            <ChevronLeft size={20} strokeWidth={1.8} aria-hidden="true" />
          </button>
          <h1 className="header-title">Membership &amp; Coins</h1>
          <div style={{ width: '40px' }} />
        </div>
      </header>

      <div className="scroll-container" style={{ flex: 1, minHeight: 0 }}>
        <div className="sn-narrow-container" style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
          {/* Active VIP Member Card Banner (Clean, no cancel control per requirement) */}
          {isVIP && userSubscription && (
            <div
              style={{
                backgroundColor: 'var(--hero-bg)',
                color: 'var(--hero-text)',
                borderRadius: '18px',
                padding: '20px',
                border: '1px solid var(--accent-color)',
              }}
            >
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: '12px' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                  <div
                    style={{
                      width: '40px',
                      height: '40px',
                      borderRadius: '12px',
                      backgroundColor: 'rgba(212, 174, 104, 0.16)',
                      color: '#E2BB6B',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      flexShrink: 0,
                    }}
                  >
                    <Crown size={20} strokeWidth={1.8} aria-hidden="true" />
                  </div>
                  <div>
                    <h2 style={{ fontFamily: 'var(--font-display)', fontSize: '1.125rem', fontWeight: 600, color: '#FAF8F5' }}>
                      VIP Unlimited Member
                    </h2>
                    <p style={{ fontSize: '0.75rem', color: 'rgba(250, 248, 245, 0.72)', marginTop: '2px' }}>
                      All-Access Pass · Unlimited reading across all stories
                    </p>
                  </div>
                </div>
                <span
                  style={{
                    backgroundColor: 'rgba(212, 174, 104, 0.18)',
                    color: '#E2BB6B',
                    fontSize: '0.6875rem',
                    fontWeight: 600,
                    padding: '4px 10px',
                    borderRadius: '999px',
                    textTransform: 'uppercase',
                    flexShrink: 0,
                  }}
                >
                  {userSubscription.cycle} Plan
                </span>
              </div>

              <div
                style={{
                  marginTop: '16px',
                  paddingTop: '12px',
                  borderTop: '1px solid rgba(255, 255, 255, 0.12)',
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  fontSize: '0.75rem',
                }}
              >
                <div>
                  <span style={{ color: 'rgba(250, 248, 245, 0.65)' }}>Valid Until: </span>
                  <span style={{ fontWeight: 600 }}>
                    {userSubscription.current_period_end
                      ? new Date(userSubscription.current_period_end).toLocaleDateString()
                      : 'Active'}
                  </span>
                </div>
                <span style={{ color: '#86efac', fontWeight: 600, display: 'inline-flex', alignItems: 'center', gap: '5px' }}>
                  <span style={{ width: '6px', height: '6px', borderRadius: '50%', backgroundColor: '#22c55e' }} />
                  {userSubscription.status === 'CANCELLED' ? 'Auto-renew off' : userSubscription.status === 'CANCEL_PENDING' ? 'Cancellation pending' : 'Active'}
                </span>
              </div>
            </div>
          )}

          {/* VIP Passes */}
          {subscriptionPacks.length > 0 && (
            <section style={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
              <h2 className="hot-tags-title" style={{ margin: 0 }}>Unlimited Membership</h2>
              {subscriptionPacks.map((pack) => {
                const isSelected = selectedPack === pack.id;
                return (
                  <button
                    type="button"
                    key={pack.id}
                    onClick={() => setSelectedPack(pack.id)}
                    aria-pressed={isSelected}
                    style={{
                      width: '100%',
                      textAlign: 'left',
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                      gap: '16px',
                      backgroundColor: isSelected ? 'var(--hero-bg)' : 'var(--bg-secondary)',
                      color: isSelected ? 'var(--hero-text)' : 'var(--text-primary)',
                      borderRadius: '16px',
                      padding: '16px 18px',
                      border: isSelected ? '1.5px solid var(--accent-color)' : '1px solid var(--border-color)',
                      cursor: 'pointer',
                      transition: 'var(--transition-fast)',
                    }}
                  >
                    <div style={{ display: 'flex', gap: '12px', alignItems: 'flex-start', minWidth: 0 }}>
                      <Crown
                        size={20}
                        strokeWidth={1.8}
                        style={{ color: isSelected ? '#E2BB6B' : 'var(--accent-color)', marginTop: '2px', flexShrink: 0 }}
                        aria-hidden="true"
                      />
                      <div style={{ minWidth: 0 }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap' }}>
                          <span style={{ fontFamily: 'var(--font-display)', fontSize: '1.0625rem', fontWeight: 600 }}>
                            {pack.name}
                          </span>
                          <span
                            style={{
                              fontSize: '0.625rem',
                              backgroundColor: isSelected ? 'rgba(212, 174, 104, 0.2)' : 'var(--accent-light)',
                              color: isSelected ? '#E2BB6B' : 'var(--accent-text)',
                              fontWeight: 600,
                              padding: '2px 8px',
                              borderRadius: '999px',
                              textTransform: 'uppercase',
                            }}
                          >
                            {pack.cycle}
                          </span>
                        </div>
                        <p
                          style={{
                            fontSize: '0.75rem',
                            color: isSelected ? 'rgba(250, 248, 245, 0.74)' : 'var(--text-secondary)',
                            marginTop: '4px',
                            lineHeight: 1.45,
                          }}
                        >
                          {pack.desc}
                        </p>
                      </div>
                    </div>

                    <div style={{ textAlign: 'right', flexShrink: 0 }}>
                      <div className="tabular-nums" style={{ fontSize: '1.0625rem', fontWeight: 700 }}>
                        {pack.price}
                      </div>
                      <div style={{ fontSize: '0.6875rem', opacity: 0.7 }}>/{pack.cycle}</div>
                    </div>
                  </button>
                );
              })}
            </section>
          )}

          {/* Coin Packs Grid */}
          {coinPacks.length > 0 && (
            <section style={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
                <h2 className="hot-tags-title" style={{ margin: 0 }}>Single Purchases</h2>
                <span style={{ fontSize: '0.6875rem', color: 'var(--text-tertiary)' }}>Never expire</span>
              </div>

              <div className="coin-packs-grid">
                {coinPacks.map((pack) => {
                  const isSelected = selectedPack === pack.id;
                  return (
                    <button
                      type="button"
                      key={pack.id}
                      onClick={() => setSelectedPack(pack.id)}
                      aria-pressed={isSelected}
                      style={{
                        backgroundColor: 'var(--bg-secondary)',
                        color: 'var(--text-primary)',
                        borderRadius: '14px',
                        padding: '14px 12px',
                        border: isSelected ? '1.5px solid var(--accent-color)' : '1px solid var(--border-color)',
                        boxShadow: isSelected ? '0 0 0 1px var(--accent-color)' : 'none',
                        cursor: 'pointer',
                        display: 'flex',
                        flexDirection: 'column',
                        alignItems: 'stretch',
                        justifyContent: 'space-between',
                        gap: '10px',
                        textAlign: 'left',
                        transition: 'var(--transition-fast)',
                      }}
                    >
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '6px' }}>
                        {pack.type === 'whole_book' ? (
                          <span
                            style={{
                              backgroundColor: 'var(--accent-light)',
                              color: 'var(--accent-text)',
                              fontSize: '0.625rem',
                              fontWeight: 600,
                              padding: '2px 7px',
                              borderRadius: '999px',
                            }}
                          >
                            Full Book
                          </span>
                        ) : pack.bonus > 0 ? (
                          <span
                            className="tabular-nums"
                            style={{
                              backgroundColor: 'var(--accent-light)',
                              color: 'var(--accent-text)',
                              fontSize: '0.625rem',
                              fontWeight: 600,
                              padding: '2px 7px',
                              borderRadius: '999px',
                            }}
                          >
                            +{pack.bonus} bonus
                          </span>
                        ) : (
                          <span />
                        )}

                        <span
                          aria-hidden="true"
                          style={{
                            width: '16px',
                            height: '16px',
                            borderRadius: '50%',
                            border: isSelected ? 'none' : '1.5px solid var(--border-strong)',
                            backgroundColor: isSelected ? 'var(--accent-color)' : 'transparent',
                            color: '#FFFFFF',
                            display: 'inline-flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            flexShrink: 0,
                          }}
                        >
                          {isSelected && <Check size={11} strokeWidth={2.6} />}
                        </span>
                      </div>

                      {pack.type === 'whole_book' ? (
                        <div>
                          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                            <BookOpen size={15} strokeWidth={1.9} style={{ color: 'var(--accent-color)', flexShrink: 0 }} aria-hidden="true" />
                            <span style={{ fontSize: '0.9375rem', fontWeight: 700, color: 'var(--text-primary)', lineHeight: 1.25 }}>
                              {pack.name || 'Unlock Entire Novel'}
                            </span>
                          </div>
                          <span
                            style={{
                              fontSize: '0.6875rem',
                              color: 'var(--text-tertiary)',
                              display: 'block',
                              marginTop: '3px',
                              overflow: 'hidden',
                              textOverflow: 'ellipsis',
                              whiteSpace: 'nowrap',
                            }}
                          >
                            {novelTitle || 'Current Novel'}
                          </span>
                        </div>
                      ) : (
                        <div>
                          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                            <GoldCoin size={16} />
                            <span className="tabular-nums" style={{ fontSize: '1.125rem', fontWeight: 700, color: 'var(--text-primary)' }}>
                              {pack.coins.toLocaleString()}
                            </span>
                          </div>
                          <span className="tabular-nums" style={{ fontSize: '0.6875rem', color: 'var(--text-tertiary)', display: 'block', marginTop: '2px' }}>
                            Total: {pack.total.toLocaleString()} Coins
                          </span>
                        </div>
                      )}

                      <div
                        className="tabular-nums"
                        style={{
                          fontSize: '0.8125rem',
                          fontWeight: 600,
                          backgroundColor: isSelected ? 'var(--cta-bg)' : 'var(--bg-tertiary)',
                          color: isSelected ? 'var(--cta-text)' : 'var(--text-primary)',
                          textAlign: 'center',
                          padding: '7px 0',
                          borderRadius: '8px',
                        }}
                      >
                        {pack.price}
                      </div>
                    </button>
                  );
                })}
              </div>
            </section>
          )}

          {/* Empty State if no packages are configured */}
          {subscriptionPacks.length === 0 && coinPacks.length === 0 && (
            <div
              style={{
                backgroundColor: 'var(--bg-secondary)',
                borderRadius: '16px',
                padding: '32px 20px',
                border: '1px solid var(--border-color)',
                textAlign: 'center',
                color: 'var(--text-secondary)',
                fontSize: '0.875rem',
              }}
            >
              No membership or coin packages are currently available. Please try again later.
            </div>
          )}

          {/* PayPal Checkout Button */}
          {selectedSlot && (
            <button
              type="button"
              onClick={handlePurchase}
              disabled={isProcessing}
              style={{
                width: '100%',
                minHeight: '50px',
                backgroundColor: '#F5C344',
                color: '#0F265C',
                border: 'none',
                borderRadius: '999px',
                fontSize: '0.9375rem',
                fontWeight: 700,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: '8px',
                cursor: isProcessing ? 'not-allowed' : 'pointer',
              }}
            >
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true" style={{ flexShrink: 0 }}>
                <path d="M7.076 21.337H2.47a.64.64 0 0 1-.633-.74L4.931 2.378A.853.853 0 0 1 5.774 1.7h7.24c3.483 0 5.69 1.642 5.093 5.372-.44 2.75-2.228 4.316-4.945 4.316h-2.14l-1.07 6.776-.026.173a.853.853 0 0 1-.85.8z" fill="#003087" />
                <path d="M8.766 18.066l1.24-7.852h2.64c2.717 0 4.505-1.566 4.945-4.316.597-3.73-1.61-5.372-5.093-5.372H5.258a.853.853 0 0 0-.843.678L1.321 19.723a.64.64 0 0 0 .633.74h4.606l1.07-6.776.026-.173a.853.853 0 0 1 .85-.8z" fill="#0079C1" />
                <path d="M9.13 14.77l1.07-6.776h2.14c2.717 0 4.505-1.566 4.945-4.316.14-.876.105-1.614-.078-2.217C16.89 2.532 15.65 2 13.904 2H8.354a.853.853 0 0 0-.843.678L4.417 21.2a.64.64 0 0 0 .633.74h4.606l1.07-6.776.026-.173a.853.853 0 0 1 .85-.8z" fill="#00457C" />
              </svg>
              <span>
                {isProcessing
                  ? 'Connecting to PayPal...'
                  : selectedSlot.type === 'subscription' || selectedSlot.type === 'vip'
                    ? `Subscribe with PayPal · ${selectedSlot.price}`
                    : `Pay with PayPal · ${selectedSlot.price}`}
              </span>
            </button>
          )}

          {/* Notes */}
          <div
            style={{
              backgroundColor: 'var(--bg-secondary)',
              borderRadius: '14px',
              padding: '14px 16px',
              border: '1px solid var(--border-color)',
              fontSize: '0.75rem',
              color: 'var(--text-secondary)',
              lineHeight: 1.65,
            }}
          >
            <p>• <strong>VIP Unlimited Pass:</strong> Instant access to all novel chapters across the site without consuming coins.</p>
            <p>• <strong>Coin Top-up:</strong> Permanent coins to unlock individual chapters across StarNovel.</p>
            <p>
              • Need help?{' '}
              <button
                type="button"
                onClick={() => setActiveLegalModal('contact')}
                style={{ background: 'none', border: 'none', padding: 0, color: 'var(--accent-text)', fontWeight: 600, textDecoration: 'underline', cursor: 'pointer' }}
              >
                Contact Customer Support
              </button>.
            </p>
          </div>

          {/* Legal Links */}
          <div
            style={{
              display: 'flex',
              flexWrap: 'wrap',
              justifyContent: 'center',
              alignItems: 'center',
              gap: '8px 12px',
              paddingBottom: '12px',
              fontSize: '0.75rem',
              color: 'var(--text-tertiary)',
            }}
          >
            <button type="button" onClick={() => setActiveLegalModal('terms')} style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer', textDecoration: 'underline' }}>
              Terms of Service
            </button>
            <span aria-hidden="true">·</span>
            <button type="button" onClick={() => setActiveLegalModal('privacy')} style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer', textDecoration: 'underline' }}>
              Privacy Policy
            </button>
            <span aria-hidden="true">·</span>
            <button type="button" onClick={() => setActiveLegalModal('refund')} style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer', textDecoration: 'underline' }}>
              Refund Policy
            </button>
            <span aria-hidden="true">·</span>
            <button type="button" onClick={() => setActiveLegalModal('contact')} style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer', textDecoration: 'underline' }}>
              Contact Us
            </button>
          </div>
        </div>
      </div>

      {/* Payment Processing Loading Overlay */}
      {isProcessing && (
        <div
          role="status"
          aria-live="polite"
          style={{
            position: 'fixed',
            inset: 0,
            backgroundColor: 'rgba(15, 15, 15, 0.72)',
            backdropFilter: 'blur(6px)',
            zIndex: 9999,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            gap: '14px',
            color: '#FAF8F5',
            padding: '24px',
            textAlign: 'center',
          }}
        >
          <div className="sn-spinner" />
          <p style={{ fontSize: '1rem', fontWeight: 600 }}>
            {paymentStep === 1 ? 'Redirecting to PayPal Checkout...' : 'Completing PayPal transaction...'}
          </p>
          <p style={{ fontSize: '0.8125rem', color: 'rgba(250, 248, 245, 0.72)' }}>Please keep this window open</p>
        </div>
      )}

      {/* Native <dialog> Payment Success Modal */}
      <dialog
        ref={successDialogRef}
        className="sn-dialog"
        aria-labelledby={successTitleId}
        onCancel={(e) => {
          e.preventDefault();
          setShowSuccess(false);
        }}
        onPointerDown={(e) => {
          if (successDialogRef.current) {
            pressedOutsideRef.current = isPointerOutside(e, successDialogRef.current);
          }
        }}
        onPointerUp={(e) => {
          if (successDialogRef.current && pressedOutsideRef.current && isPointerOutside(e, successDialogRef.current)) {
            setShowSuccess(false);
          }
          pressedOutsideRef.current = false;
        }}
        style={{ width: '100%', maxWidth: '340px', padding: '26px 24px', textAlign: 'center' }}
      >
        <div
          style={{
            width: '52px',
            height: '52px',
            borderRadius: '50%',
            backgroundColor: 'var(--accent-light)',
            color: 'var(--accent-text)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            margin: '0 auto 14px',
          }}
        >
          {isVipSuccess ? <Crown size={24} strokeWidth={1.8} aria-hidden="true" /> : <Sparkles size={24} strokeWidth={1.8} aria-hidden="true" />}
        </div>
        <h2 id={successTitleId} style={{ fontFamily: 'var(--font-display)', fontSize: '1.25rem', fontWeight: 600, marginBottom: '8px' }}>
          {isVipSuccess ? 'VIP Unlimited Activated' : 'Purchase Complete'}
        </h2>
        <p style={{ fontSize: '0.875rem', color: 'var(--text-secondary)', marginBottom: '18px', lineHeight: 1.5 }}>
          {isVipSuccess
            ? 'You now have unlimited access to every story and chapter across StarNovel.'
            : isWholeBookSuccess
              ? 'This novel is now unlocked, including future chapters.'
              : 'Your coins have been credited to your wallet. Enjoy reading!'}
        </p>

        {isVipSuccess ? (
          <div
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '6px',
              backgroundColor: 'var(--accent-light)',
              color: 'var(--accent-text)',
              fontWeight: 600,
              padding: '7px 14px',
              borderRadius: '999px',
              margin: '0 auto 20px',
              fontSize: '0.8125rem',
            }}
          >
            <Crown size={14} strokeWidth={1.9} aria-hidden="true" />
            <span>{vipSuccessTitle} Active</span>
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '8px', margin: '0 auto 20px' }}>
            {isWholeBookSuccess && (
              <div
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: '6px',
                  backgroundColor: 'var(--accent-light)',
                  color: 'var(--accent-text)',
                  fontWeight: 600,
                  padding: '7px 14px',
                  borderRadius: '999px',
                  fontSize: '0.8125rem',
                }}
              >
                <BookOpen size={14} strokeWidth={1.9} aria-hidden="true" />
                <span>{novelTitle ? `${novelTitle} Unlocked` : 'Full Novel Unlocked'}</span>
              </div>
            )}
            {successAmount > 0 && (
              <div
                className="tabular-nums"
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: '6px',
                  backgroundColor: 'var(--accent-light)',
                  color: 'var(--accent-text)',
                  fontWeight: 700,
                  padding: '7px 16px',
                  borderRadius: '999px',
                  fontSize: '0.875rem',
                }}
              >
                <GoldCoin size={15} />
                <span>+{successAmount.toLocaleString()} Coins Added</span>
              </div>
            )}
          </div>
        )}

        <button
          type="button"
          autoFocus
          className="btn-cta-primary"
          style={{ width: '100%', borderRadius: '999px' }}
          onClick={() => {
            setShowSuccess(false);
            handleReturnToContent();
          }}
        >
          Start Reading Now
        </button>
      </dialog>

      <LegalModal
        isOpen={activeLegalModal !== null}
        type={activeLegalModal || 'contact'}
        onClose={() => setActiveLegalModal(null)}
      />
    </div>
  );
};

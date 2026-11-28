import { readerStorage } from '../utils/readerStorage';
import React, { useState, useEffect, useRef, useId } from 'react';
import { ChevronLeft, Crown, Sparkles, BookOpen } from 'lucide-react';
import { useToast } from '../context/toastStore';
import { GoldCoin } from '../components/GoldCoin';
import { api, ApiError, type PurchaseIntent } from '../utils/api';
import {
  isPendingPurchaseStatus,
  type ReturnedLookupState,
} from '../utils/rechargeSelectionHelpers';
import { evaluatePurchaseLookupOutcome } from '../utils/checkoutPendingHelpers';
import {
  clearCheckoutOrigin,
  purchaseRequestStorageKey,
  readCheckoutOrigin,
  readingReturnPath,
  type PaymentProvider,
} from '../utils/rechargeCatalogHelpers';
import { RechargeCheckoutPanel } from '../components/RechargeCheckoutPanel';

interface RechargeProps {
  authReady?: boolean;
  userId?: number;
  userCoins?: number;
  novelId?: number;
  novelTitle?: string;
  onRefreshStatus?: () => Promise<void>;
  onBack: () => void;
  onNavigate?: (
    pageOrPath: string,
    params?: Record<string, unknown>,
    options?: { replace?: boolean }
  ) => void;
}

function initialPurchaseLocator() {
  const query = new URLSearchParams(window.location.search);
  const stripeVIP = query.get('stripe_vip');
  if (stripeVIP) return { intent: stripeVIP, token: '', vip: true, stripeVIP: true, cancelled: false };
  const cancelled = query.get('payment') === 'cancel';
  const intentParam = query.get('purchase') || '';
  const vipIntent = query.get('vip_purchase') || '';
  const rawToken = query.get('subscription_id') || query.get('token') || '';
  const token = cancelled && (vipIntent || intentParam) ? '' : rawToken;
  if (vipIntent || query.has('subscription_id') || rawToken.startsWith('I-')) {
    return { intent: vipIntent || intentParam, token, vip: true, stripeVIP: false, cancelled };
  }
  return { intent: intentParam, token, vip: false, stripeVIP: false, cancelled };
}

function resolveCancelReaderPath(intentId: string, fallbackReturnPath?: string): string {
  const origin = readCheckoutOrigin(intentId);
  if (origin?.source !== 'reader') return '';
  return readingReturnPath(origin.path) || readingReturnPath(fallbackReturnPath);
}

export const Recharge: React.FC<RechargeProps> = ({
  authReady = true,
  userId,
  userCoins,
  novelId,
  novelTitle,
  onRefreshStatus,
  onBack,
  onNavigate,
}) => {
  const { showToast } = useToast();
  const [showSuccess, setShowSuccess] = useState(false);
  const [successAmount, setSuccessAmount] = useState(0);
  const [isWholeBookSuccess, setIsWholeBookSuccess] = useState(false);
  const hasReturnedFromPaymentRef = useRef(false);
  const pendingReturnPathRef = useRef('');
  const successOriginRef = useRef<{ source: 'reader' | 'recharge'; path: string } | null>(null);
  const [successOriginSource, setSuccessOriginSource] = useState<'reader' | 'recharge' | null>(null);
  const [purchaseLocator, setPurchaseLocator] = useState(initialPurchaseLocator);
  const [purchaseRefresh, setPurchaseRefresh] = useState(0);
  const [returnedPendingIntent, setReturnedPendingIntent] = useState<PurchaseIntent | null>(null);
  const [returnedLookupState, setReturnedLookupState] = useState<ReturnedLookupState>(
    () => (purchaseLocator.intent || purchaseLocator.token ? 'loading' : 'none'),
  );
  const [returningAfterCancel, setReturningAfterCancel] = useState<boolean>(
    () => Boolean(purchaseLocator.cancelled && purchaseLocator.intent && resolveCancelReaderPath(purchaseLocator.intent)),
  );
  const shownPurchaseRef = useRef('');
  const shownFailureRef = useRef('');
  const [isVipSuccess, setIsVipSuccess] = useState(false);
  const [vipSuccessTitle, setVipSuccessTitle] = useState('');

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
  }, [showSuccess]);

  const onRefreshStatusRef = useRef(onRefreshStatus);
  useEffect(() => {
    onRefreshStatusRef.current = onRefreshStatus;
  }, [onRefreshStatus]);

  const showToastRef = useRef(showToast);
  useEffect(() => {
    showToastRef.current = showToast;
  }, [showToast]);

  const onNavigateRef = useRef(onNavigate);
  useEffect(() => {
    onNavigateRef.current = onNavigate;
  }, [onNavigate]);

  const showPurchase = (id: string, subscriptionProvider?: PaymentProvider) => {
    setPurchaseLocator({ intent: id, token: '', vip: Boolean(subscriptionProvider), stripeVIP: subscriptionProvider === 'stripe', cancelled: false });
    const current = new URL(window.location.href);
    ['payment', 'token', 'PayerID', 'vip_purchase', 'subscription_id', 'purchase', 'stripe_vip'].forEach((name) => current.searchParams.delete(name));
    current.searchParams.set(subscriptionProvider === 'stripe' ? 'stripe_vip' : subscriptionProvider === 'paypal' ? 'vip_purchase' : 'purchase', id);
    window.history.replaceState(window.history.state, '', current.href);
  };

  useEffect(() => {
    if (!authReady || !userId || (!purchaseLocator.intent && !purchaseLocator.token)) return;
    const controller = new AbortController();
    const startedAt = Date.now();
    const endAt = startedAt + 75_000;
    let timer: number | undefined;
    let loading = false;

    const lookup = async () => {
      if (loading || controller.signal.aborted || document.visibilityState !== 'visible') return;
      window.clearTimeout(timer);
      loading = true;
      try {
        const signal = typeof AbortSignal.any === 'function' && typeof AbortSignal.timeout === 'function'
          ? AbortSignal.any([controller.signal, AbortSignal.timeout(15_000)])
          : controller.signal;
        const intent = purchaseLocator.stripeVIP
          ? await api.getStripeVIPPurchase(purchaseLocator.intent, signal)
          : await api.getReturnedPurchase(purchaseLocator.intent, purchaseLocator.token, purchaseLocator.vip, signal);
        if (controller.signal.aborted) return;
        hasReturnedFromPaymentRef.current = true;
        pendingReturnPathRef.current = intent.return_path || '';
        setReturnedPendingIntent(isPendingPurchaseStatus(intent.status) ? intent : null);
        setReturnedLookupState('resolved');
        const decision = evaluatePurchaseLookupOutcome(intent, {
          cancelled: purchaseLocator.cancelled,
          elapsedMs: Date.now() - startedAt,
          maxWaitMs: endAt - startedAt,
        });
        if (decision.shouldClearDraft) {
          const originIntentId = intent.intent_id || purchaseLocator.intent;
          const recordedOrigin = readCheckoutOrigin(originIntentId);
          clearCheckoutOrigin(originIntentId);
          const storageKey = purchaseRequestStorageKey(userId, intent.slot_id, intent.product_id || 0, intent.novel_id || 0);
          try {
            const saved = JSON.parse(sessionStorage.getItem(storageKey) || 'null');
            if (saved?.intentId === intent.intent_id) sessionStorage.removeItem(storageKey);
          } catch {
            // Server-owned purchase status does not depend on the draft.
          }
          if (decision.shouldShowSuccess && shownPurchaseRef.current !== (intent.intent_id || intent.order_id)) {
            setReturningAfterCancel(false);
            successOriginRef.current = recordedOrigin;
            setSuccessOriginSource(recordedOrigin?.source ?? null);
            shownPurchaseRef.current = intent.intent_id || intent.order_id || '';
            setSuccessAmount(intent.coins);
            setIsVipSuccess(intent.order_type === 'subscription');
            if (intent.order_type === 'subscription') {
              setVipSuccessTitle('StarNovel Unlimited');
            }
            setIsWholeBookSuccess(intent.order_type === 'whole_book');
            setShowSuccess(true);
            if (decision.shouldRefreshWallet) {
              void onRefreshStatusRef.current?.();
            }
          }
        }
        if (purchaseLocator.cancelled && !decision.shouldShowSuccess && !decision.shouldContinuePolling) {
          const originIntentId = intent.intent_id || purchaseLocator.intent;
          const readerCancelPath = resolveCancelReaderPath(originIntentId, intent.return_path);
          clearCheckoutOrigin(originIntentId);
          if (readerCancelPath) {
            if (onNavigateRef.current) {
              onNavigateRef.current(readerCancelPath, undefined, { replace: true });
            } else {
              window.location.replace(readerCancelPath);
            }
            return;
          }
          setReturningAfterCancel(false);
        }
        if (decision.outcome === 'terminal_failure') {
          const failureKey = `${intent.intent_id}:${intent.status}:${intent.confirmation_code || ''}`;
          if (shownFailureRef.current !== failureKey) {
            shownFailureRef.current = failureKey;
            showToastRef.current(
              intent.confirmation_message || 'Payment could not be completed. Please try again or contact support.',
              'error',
            );
          }
        }
        if (decision.shouldContinuePolling && decision.nextPollDelayMs !== null) {
          timer = window.setTimeout(() => void lookup(), decision.nextPollDelayMs);
        }
      } catch (error) {
        if (controller.signal.aborted) return;
        const isPermanentLookupError = error instanceof ApiError && [400, 401, 403, 404].includes(error.status);
        const canRetry = !isPermanentLookupError && !purchaseLocator.cancelled && Date.now() < endAt;
        if (canRetry) {
          setReturnedLookupState((prev) => (prev === 'resolved' ? prev : 'loading'));
          const delayMs = Date.now() - startedAt < 10_000 ? 1500 : 3000;
          timer = window.setTimeout(() => void lookup(), delayMs);
        } else {
          setReturnedLookupState((prev) => (prev === 'resolved' ? prev : 'unresolved'));
          if (purchaseLocator.cancelled) {
            const readerCancelPath = resolveCancelReaderPath(purchaseLocator.intent);
            clearCheckoutOrigin(purchaseLocator.intent);
            if (readerCancelPath) {
              if (onNavigateRef.current) {
                onNavigateRef.current(readerCancelPath, undefined, { replace: true });
              } else {
                window.location.replace(readerCancelPath);
              }
              return;
            }
            setReturningAfterCancel(false);
          } else {
            showToastRef.current(
              isPermanentLookupError
                ? 'This checkout could not be found. Please make sure you are signed in with the account that paid.'
                : 'Payment status is temporarily unavailable. Please try again later.',
              'error',
            );
          }
        }
      } finally {
        loading = false;
      }
    };

    const onVisibilityChange = () => {
      window.clearTimeout(timer);
      if (document.visibilityState === 'visible') void lookup();
    };
    void lookup();
    document.addEventListener('visibilitychange', onVisibilityChange);
    return () => {
      controller.abort();
      window.clearTimeout(timer);
      document.removeEventListener('visibilitychange', onVisibilityChange);
    };
  }, [authReady, userId, purchaseLocator, purchaseRefresh]);

  const clearReturnQueryFromUrl = () => {
    const current = new URL(window.location.href);
    ['payment', 'token', 'PayerID', 'vip_purchase', 'subscription_id', 'purchase', 'stripe_vip', 'type'].forEach((name) =>
      current.searchParams.delete(name),
    );
    window.history.replaceState(window.history.state, '', current.href);
  };

  const handleReturnToContent = () => {
    let returnPath =
      readingReturnPath(successOriginRef.current?.source === 'reader' ? successOriginRef.current.path : '') ||
      readingReturnPath(pendingReturnPathRef.current);
    if (!returnPath) {
      try {
        const saved = sessionStorage.getItem('pending_paypal_recharge');
        if (saved) {
          const parsed = JSON.parse(saved);
          if (parsed.returnContentPath) {
            returnPath = readingReturnPath(parsed.returnContentPath);
          }
        }
      } catch {
        // Ignore parse errors
      }
    }

    if (!returnPath) {
      returnPath = readingReturnPath(readerStorage.getItem('last_read_content_path'));
    }

    if (returnPath) {
      if (onNavigate) {
        onNavigate(returnPath, undefined, { replace: true });
      } else {
        window.location.replace(returnPath);
      }
    } else if (novelId && onNavigate) {
      onNavigate('reader', { id: novelId }, { replace: true });
    } else if (onNavigate) {
      onNavigate('shelf', undefined, { replace: true });
    } else {
      onBack();
    }
  };

  const isPointerOutside = (e: React.PointerEvent<HTMLDialogElement>, dialog: HTMLDialogElement) => {
    const r = dialog.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  };

  if (returningAfterCancel) {
    return (
      <div
        className="page-container-full animate-fade-in"
        style={{
          backgroundColor: 'var(--bg-primary)',
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          gap: '12px',
          color: 'var(--text-secondary)',
        }}
      >
        <div className="sn-spinner" />
        <p style={{ fontSize: '0.875rem', margin: 0 }}>Returning to chapter...</p>
      </div>
    );
  }

  return (
    <>
      <div className="page-container-full animate-fade-in" style={{ backgroundColor: 'var(--bg-primary)' }}>
        {/* Top Bar */}
        <header className="detail-top-bar glass-panel">
          <div className="sn-narrow-container" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '8px' }}>
            <button
              type="button"
              className="header-btn"
              onClick={() => {
                if (hasReturnedFromPaymentRef.current && successOriginRef.current?.source !== 'recharge') {
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
            {typeof userCoins === 'number' ? (
              <div className="recharge-balance-pill" aria-label={`Current balance: ${userCoins.toLocaleString()} coins`}>
                <GoldCoin size={14} />
                <span className="tabular-nums">{userCoins.toLocaleString()}</span>
              </div>
            ) : (
              <div style={{ width: '40px' }} />
            )}
          </div>
        </header>

        <div className="scroll-container" style={{ flex: 1, minHeight: 0 }}>
          <div className="sn-narrow-container">
            <RechargeCheckoutPanel
              authReady={authReady}
              userId={userId}
              novelId={novelId}
              novelTitle={novelTitle}
              returnedPendingIntent={returnedPendingIntent}
              returnedLookupState={returnedLookupState}
              onRefreshStatus={onRefreshStatus}
              onIntentTracked={(intentId, refresh) => {
                showPurchase(intentId);
                if (refresh) setPurchaseRefresh((value) => value + 1);
              }}
            />
          </div>
        </div>
      </div>

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
          {isVipSuccess ? 'Unlimited Membership Activated' : 'Purchase Complete'}
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
            if (successOriginSource === 'recharge') {
              clearReturnQueryFromUrl();
            } else {
              handleReturnToContent();
            }
          }}
        >
          {successOriginSource === 'recharge' ? 'Done' : 'Start Reading Now'}
        </button>
      </dialog>
    </>
  );
};

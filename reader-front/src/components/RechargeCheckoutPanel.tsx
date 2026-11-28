import { readerStorage } from '../utils/readerStorage';
import React, { useState, useEffect, useCallback, useRef, useId } from 'react';
import { Crown, BookOpen, CreditCard } from 'lucide-react';
import { useToast } from '../context/toastStore';
import { GoldCoin } from './GoldCoin';
import { api, ApiError, type PurchaseIntent } from '../utils/api';
import {
  buildCoinPacks,
  buildSubscriptionPacks,
  calcCoinBonusPercent,
  clearLastCheckoutSelection,
  formatSubscriptionEquivalent,
  purchaseRequestStorageKey,
  readLastCheckoutSelection,
  readingReturnPath,
  resolveInitialCarouselIndex,
  sanitizeSubscriptionCopy,
  saveCheckoutOrigin,
  saveLastCheckoutSelection,
  type PaymentProvider,
  type RechargeSlotItem,
} from '../utils/rechargeCatalogHelpers';
import {
  isPendingPurchaseStatus,
  resolveRechargeSelection,
  type ReturnedLookupState,
} from '../utils/rechargeSelectionHelpers';
import { isCheckoutPendingConflict } from '../utils/checkoutPendingHelpers';

function isSubscriptionSlot(slot: RechargeSlotItem): boolean {
  return slot.type === 'subscription' || slot.type === 'vip' || slot.product_type === 'vip';
}

function startCheckout(provider: PaymentProvider, intent: string, signal?: AbortSignal) {
  return provider === 'stripe' ? api.startStripeCheckout(intent, signal) : api.startPayPalCheckout(intent, signal);
}

type PaymentMethodsState = {
  paypal_single: boolean;
  paypal_vip: boolean;
  stripe_single: boolean;
  stripe_vip: boolean;
  environment?: string;
  stripe_slots: number[];
  stripe_vip_slots: number[];
  stripe_sandbox: boolean;
};

const emptyPaymentMethods: PaymentMethodsState = {
  paypal_single: false,
  paypal_vip: false,
  stripe_single: false,
  stripe_vip: false,
  stripe_slots: [],
  stripe_vip_slots: [],
  stripe_sandbox: false,
};

export interface RechargeCheckoutPanelProps {
  authReady?: boolean;
  userId?: number;
  novelId?: number;
  novelTitle?: string;
  embedded?: boolean;
  returnPathOverride?: string;
  returnedPendingIntent?: PurchaseIntent | null;
  returnedLookupState?: ReturnedLookupState;
  onRefreshStatus?: () => Promise<void>;
  onIntentTracked?: (intentId: string, refresh?: boolean) => void;
  onLoadingChange?: (loading: boolean) => void;
}

export const RechargeCheckoutPanel: React.FC<RechargeCheckoutPanelProps> = ({
  authReady = true,
  userId,
  novelId,
  novelTitle,
  embedded = false,
  returnPathOverride,
  returnedPendingIntent = null,
  returnedLookupState = 'none',
  onRefreshStatus,
  onIntentTracked,
  onLoadingChange,
}) => {
  const { showToast } = useToast();
  const checkoutSource = embedded ? 'reader' : 'recharge';
  const initialSavedSelectionRef = useRef(readLastCheckoutSelection(checkoutSource));
  const [selectedPack, setSelectedPack] = useState<number | null>(null);
  const [selectedProvider, setSelectedProvider] = useState<PaymentProvider>(
    () => initialSavedSelectionRef.current?.provider ?? 'paypal',
  );
  const [isProcessing, setIsProcessing] = useState(false);
  const [activeCheckoutProvider, setActiveCheckoutProvider] = useState<PaymentProvider>('paypal');
  const selectedPackRef = useRef<number | null>(null);
  selectedPackRef.current = selectedPack;
  const userManuallySelectedRef = useRef(false);
  const staleReturnedIntentToastRef = useRef('');
  const isMountedRef = useRef(true);
  const purchaseStarting = useRef(false);
  const redirectingRef = useRef(false);
  const redirectResetTimerRef = useRef<number | undefined>(undefined);
  const purchaseScope = useRef<AbortController | null>(null);
  const requestDraft = useRef<{ storageKey: string; requestKey: string; returnPath: string; intentId?: string } | null>(null);
  const [activeSlideIndex, setActiveSlideIndex] = useState(0);
  const [autoRotatePaused, setAutoRotatePaused] = useState(false);
  const carouselRef = useRef<HTMLDivElement | null>(null);
  const hasAlignedInitialSlideRef = useRef(false);
  const activeSlideIndexRef = useRef(0);
  const programmaticScrollUntilRef = useRef(0);

  const [slots, setSlots] = useState<RechargeSlotItem[]>([]);
  const [loadingTemplates, setLoadingTemplates] = useState(true);
  const [paymentMethods, setPaymentMethods] = useState<PaymentMethodsState>(emptyPaymentMethods);
  const [offersNovelId, setOffersNovelId] = useState<number | undefined | null>(null);
  const offersRequestRef = useRef<AbortController | null>(null);
  const paypalClipId = useId();
  const modalPaypalClipId = useId();

  const onRefreshStatusRef = useRef(onRefreshStatus);
  useEffect(() => {
    onRefreshStatusRef.current = onRefreshStatus;
  }, [onRefreshStatus]);

  const showToastRef = useRef(showToast);
  useEffect(() => {
    showToastRef.current = showToast;
  }, [showToast]);

  const onLoadingChangeRef = useRef(onLoadingChange);
  useEffect(() => {
    onLoadingChangeRef.current = onLoadingChange;
  }, [onLoadingChange]);

  const isOffersLoading = loadingTemplates || offersNovelId !== novelId;

  useEffect(() => {
    onLoadingChangeRef.current?.(isOffersLoading);
  }, [isOffersLoading]);

  const fetchStatusAndTemplates = useCallback(async () => {
    if (!isMountedRef.current) return;
    offersRequestRef.current?.abort();
    const controller = new AbortController();
    offersRequestRef.current = controller;
    setLoadingTemplates(true);
    try {
      const [templateData, methods] = await Promise.all([
        api.getRechargeTemplates(novelId, controller.signal),
        api.getPaymentMethods(controller.signal).catch(() => emptyPaymentMethods),
      ]);

      if (controller.signal.aborted || !isMountedRef.current) return;
      const slotList = templateData.slots || [];
      setOffersNovelId(novelId);
      setSlots(slotList);
      setPaymentMethods({
        paypal_single: Boolean(methods.paypal_single),
        paypal_vip: Boolean(methods.paypal_vip),
        stripe_single: Boolean(methods.stripe_single),
        stripe_vip: Boolean(methods.stripe_vip),
        environment: methods.environment,
        stripe_slots: methods.stripe_slots ?? [],
        stripe_vip_slots: methods.stripe_vip_slots ?? [],
        stripe_sandbox: methods.stripe_sandbox ?? false,
      });
    } catch (err) {
      if (controller.signal.aborted || !isMountedRef.current) return;
      setSlots([]);
      setSelectedPack(null);
      setOffersNovelId(novelId);
      console.error('Failed to load recharge data:', err);
    } finally {
      if (!controller.signal.aborted && isMountedRef.current) setLoadingTemplates(false);
    }
  }, [novelId]);

  useEffect(() => {
    if (!authReady) return;
    void fetchStatusAndTemplates();
    return () => offersRequestRef.current?.abort();
  }, [authReady, fetchStatusAndTemplates]);

  useEffect(() => {
    isMountedRef.current = true;
    const controller = new AbortController();
    purchaseScope.current = controller;
    const onPageShow = () => {
      window.clearTimeout(redirectResetTimerRef.current);
      redirectingRef.current = false;
      purchaseStarting.current = false;
      setIsProcessing(false);
    };
    window.addEventListener('pageshow', onPageShow);
    return () => {
      isMountedRef.current = false;
      controller.abort();
      window.clearTimeout(redirectResetTimerRef.current);
      window.removeEventListener('pageshow', onPageShow);
    };
  }, []);

  useEffect(() => {
    if (returnedLookupState === 'resolved' && !returnedPendingIntent) {
      requestDraft.current = null;
    }
  }, [returnedLookupState, returnedPendingIntent]);

  const scrollCarouselTo = useCallback((index: number, behavior: ScrollBehavior = 'smooth') => {
    const track = carouselRef.current;
    if (!track || track.clientWidth <= 0) return;
    programmaticScrollUntilRef.current = behavior === 'smooth' ? Date.now() + 600 : 0;
    track.scrollTo({ left: index * track.clientWidth, behavior });
  }, []);

  useEffect(() => {
    if (loadingTemplates || offersNovelId !== novelId) return;
    const subPacks = buildSubscriptionPacks(slots);
    const visibleIds = new Set([
      ...subPacks.map((pack) => pack.id),
      ...buildCoinPacks(slots, novelId).map((pack) => pack.id),
    ]);
    const visibleSlots = slots.filter((slot) => visibleIds.has(slot.id));

    const savedSelection = initialSavedSelectionRef.current;
    if (
      savedSelection &&
      visibleSlots.length > 0 &&
      !userManuallySelectedRef.current &&
      returnedLookupState !== 'loading'
    ) {
      initialSavedSelectionRef.current = null;
      clearLastCheckoutSelection();
      const conflictsWithReturnedIntent = Boolean(
        returnedPendingIntent &&
          isPendingPurchaseStatus(returnedPendingIntent.status) &&
          returnedPendingIntent.slot_id > 0 &&
          returnedPendingIntent.slot_id !== savedSelection.slotId,
      );
      if (!conflictsWithReturnedIntent) {
        const matchedSaved = visibleSlots.find(
          (slot) =>
            slot.id === savedSelection.slotId &&
            (!savedSelection.productId || !slot.product_id || slot.product_id === savedSelection.productId),
        );
        if (matchedSaved) {
          userManuallySelectedRef.current = true;
          setAutoRotatePaused(true);
          if (matchedSaved.id !== selectedPackRef.current) {
            setSelectedPack(matchedSaved.id);
          }
          const subIdx = subPacks.findIndex((pack) => pack.id === matchedSaved.id);
          if (subIdx >= 0) {
            hasAlignedInitialSlideRef.current = true;
            activeSlideIndexRef.current = subIdx;
            setActiveSlideIndex(subIdx);
            requestAnimationFrame(() => {
              scrollCarouselTo(subIdx, 'auto');
            });
          }
          return;
        }
      }
    }

    const { selectedSlotId, staleReturnedIntent } = resolveRechargeSelection({
      visibleSlots,
      currentSelected: selectedPackRef.current,
      userManuallySelected: userManuallySelectedRef.current,
      returnedLookupState,
      returnedPendingIntent,
    });
    if (selectedSlotId !== selectedPackRef.current) {
      setSelectedPack(selectedSlotId);
    }
    if (
      staleReturnedIntent &&
      returnedPendingIntent &&
      staleReturnedIntentToastRef.current !== returnedPendingIntent.intent_id
    ) {
      staleReturnedIntentToastRef.current = returnedPendingIntent.intent_id;
      showToastRef.current(
        'Your previous package is no longer available. Please select a package to continue.',
        'error',
      );
    }
  }, [slots, offersNovelId, novelId, loadingTemplates, returnedPendingIntent, returnedLookupState, scrollCarouselTo]);

  const handleSelectPack = (packId: number) => {
    userManuallySelectedRef.current = true;
    setAutoRotatePaused(true);
    setSelectedPack(packId);
  };

  const subscriptionPacks = buildSubscriptionPacks(slots);
  const coinPacks = buildCoinPacks(slots, novelId);
  const subscriptionPacksRef = useRef(subscriptionPacks);
  subscriptionPacksRef.current = subscriptionPacks;

  useEffect(() => {
    hasAlignedInitialSlideRef.current = false;
  }, [novelId]);

  useEffect(() => {
    if (loadingTemplates || offersNovelId !== novelId || subscriptionPacks.length === 0) return;
    if (hasAlignedInitialSlideRef.current || autoRotatePaused) return;
    if (returnedLookupState === 'loading') return;

    const visibleIds = new Set([
      ...subscriptionPacks.map((pack) => pack.id),
      ...coinPacks.map((pack) => pack.id),
    ]);
    const visibleSlots = slots.filter((slot) => visibleIds.has(slot.id));
    const pendingSelection = resolveRechargeSelection({
      visibleSlots,
      currentSelected: selectedPack,
      userManuallySelected: userManuallySelectedRef.current,
      returnedLookupState,
      returnedPendingIntent,
    });
    if (pendingSelection.selectedSlotId !== null && selectedPack !== pendingSelection.selectedSlotId) return;

    const initialIndex = resolveInitialCarouselIndex(subscriptionPacks, selectedPack);
    hasAlignedInitialSlideRef.current = true;
    activeSlideIndexRef.current = initialIndex;
    setActiveSlideIndex(initialIndex);
    requestAnimationFrame(() => {
      scrollCarouselTo(initialIndex, 'auto');
    });
  }, [
    loadingTemplates,
    offersNovelId,
    novelId,
    slots,
    subscriptionPacks,
    coinPacks,
    selectedPack,
    returnedLookupState,
    returnedPendingIntent,
    autoRotatePaused,
    scrollCarouselTo,
  ]);

  useEffect(() => {
    if (loadingTemplates || offersNovelId !== novelId) return;
    if (subscriptionPacks.length <= 1 || autoRotatePaused || isProcessing) return;
    if (typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
      return;
    }

    const intervalId = window.setInterval(() => {
      if (document.visibilityState !== 'visible') return;
      const packs = subscriptionPacksRef.current;
      if (packs.length <= 1) return;
      const next = (activeSlideIndexRef.current + 1) % packs.length;
      activeSlideIndexRef.current = next;
      setActiveSlideIndex(next);
      scrollCarouselTo(next, 'smooth');
      if (selectedPackRef.current !== null && packs.some((pack) => pack.id === selectedPackRef.current)) {
        setSelectedPack(packs[next].id);
      }
    }, 3000);

    return () => window.clearInterval(intervalId);
  }, [loadingTemplates, offersNovelId, novelId, subscriptionPacks.length, autoRotatePaused, isProcessing, scrollCarouselTo]);

  const startUnifiedPurchase = async (
    tSlot: RechargeSlotItem,
    book: number,
    returnPath: string,
    provider: PaymentProvider,
    onTrackIntent: (intentId: string, refresh?: boolean) => void,
    signal?: AbortSignal,
  ) => {
    if (!userId) return;
    const storageKey = purchaseRequestStorageKey(userId, tSlot.id, tSlot.product_id || 0, book);
    let draft = requestDraft.current?.storageKey === storageKey ? requestDraft.current : null;
    try {
      const saved = JSON.parse(sessionStorage.getItem(storageKey) || 'null');
      if (saved && typeof saved.requestKey === 'string' && typeof saved.returnPath === 'string') {
        draft = { storageKey, requestKey: saved.requestKey, returnPath: saved.returnPath, intentId: saved.intentId };
      }
    } catch {
      // The in-memory draft covers browsers that deny storage access.
    }
    draft ??= { storageKey, requestKey: crypto.randomUUID(), returnPath };
    requestDraft.current = draft;
    const saveDraft = () => {
      try {
        sessionStorage.setItem(storageKey, JSON.stringify(draft));
      } catch {
        // The server creates or retrieves the purchase using the same key.
      }
    };
    saveDraft();
    const canReuseReturnedSingleIntent =
      !isSubscriptionSlot(tSlot) &&
      returnedPendingIntent &&
      isPendingPurchaseStatus(returnedPendingIntent.status) &&
      returnedPendingIntent.slot_id === tSlot.id &&
      (!returnedPendingIntent.product_id || !tSlot.product_id || returnedPendingIntent.product_id === tSlot.product_id) &&
      (returnedPendingIntent.novel_id || 0) === book;
    let intent = canReuseReturnedSingleIntent
      ? returnedPendingIntent
      : await api.preparePurchase(tSlot.id, book, draft.requestKey, draft.returnPath, signal, tSlot.product_id);
    if (signal?.aborted) return;

    const repeatablePurchase = tSlot.type !== 'whole_book';
    if (repeatablePurchase && ['completed', 'refunded', 'closed'].includes(intent.status)) {
      draft = { storageKey, requestKey: crypto.randomUUID(), returnPath };
      requestDraft.current = draft;
      saveDraft();
      intent = await api.preparePurchase(tSlot.id, book, draft.requestKey, draft.returnPath, signal, tSlot.product_id);
      if (signal?.aborted) return;
    }

    draft.intentId = intent.intent_id;
    saveDraft();
    if (['ready', 'retryable', 'awaiting_approval', 'confirming'].includes(intent.status)) {
      if (isSubscriptionSlot(tSlot)) {
        try {
          sessionStorage.setItem('pending_paypal_recharge', JSON.stringify({ returnContentPath: returnPath }));
        } catch {
          // Server-persisted return_path remains available on redirect.
        }
      }
      let checkout;
      try {
        checkout = await startCheckout(provider, intent.intent_id, signal);
      } catch (err) {
        if (!signal?.aborted && isMountedRef.current && err instanceof ApiError && isCheckoutPendingConflict(err)) {
          onTrackIntent(intent.intent_id, true);
        }
        throw err;
      }
      if (signal?.aborted || !isMountedRef.current) return;
      if (checkout.approve_url) {
        if (!embedded) {
          onTrackIntent(intent.intent_id, false);
        }
        saveCheckoutOrigin(intent.intent_id, {
          source: embedded ? 'reader' : 'recharge',
          path: embedded ? returnPath : '/recharge',
        });
        saveLastCheckoutSelection({
          provider,
          slotId: tSlot.id,
          productId: tSlot.product_id,
          source: checkoutSource,
        });
        redirectingRef.current = true;
        window.clearTimeout(redirectResetTimerRef.current);
        redirectResetTimerRef.current = window.setTimeout(() => {
          redirectingRef.current = false;
          purchaseStarting.current = false;
          if (isMountedRef.current) setIsProcessing(false);
        }, 12_000);
        window.location.assign(checkout.approve_url);
      } else {
        if (checkout.confirmation_message) {
          showToastRef.current(checkout.confirmation_message, 'error');
        }
        onTrackIntent(intent.intent_id, true);
      }
    } else {
      if (intent.confirmation_message) {
        showToastRef.current(intent.confirmation_message, 'error');
      }
      onTrackIntent(intent.intent_id, false);
    }
  };

  const handlePurchase = async (provider: PaymentProvider) => {
    if (selectedPack === null || purchaseStarting.current || isProcessing || loadingTemplates || offersNovelId !== novelId) return;
    const tSlot = slots.find((p) => p.id === selectedPack);
    if (!tSlot) return;

    purchaseStarting.current = true;
    redirectingRef.current = false;
    setActiveCheckoutProvider(provider);
    setIsProcessing(true);

    const signal = purchaseScope.current?.signal;
    let handedOffFromEmbedded = false;
    const trackIntent = (intentId: string, refresh?: boolean) => {
      if (embedded && onIntentTracked) {
        handedOffFromEmbedded = true;
      }
      onIntentTracked?.(intentId, refresh);
    };

    try {
      const returnPath =
        readingReturnPath(returnPathOverride) ||
        readingReturnPath(readerStorage.getItem('last_read_content_path')) ||
        (novelId ? `/content?novel_id=${novelId}` : '');
      const book = tSlot.type === 'whole_book' ? novelId || 0 : 0;
      await startUnifiedPurchase(tSlot, book, returnPath, provider, trackIntent, signal);
    } catch (err) {
      if (signal?.aborted || !isMountedRef.current) return;
      redirectingRef.current = false;
      setIsProcessing(false);
      if (err instanceof ApiError && err.status === 409) {
        if (
          err.code &&
          ['vip_plan_switch_approval_open', 'vip_plan_switch_pending', 'vip_plan_switch_confirming'].includes(err.code)
        ) {
          if (err.currentIntent?.intent_id) {
            trackIntent(err.currentIntent.intent_id, true);
          }
          if (err.code === 'vip_plan_switch_confirming') {
            void onRefreshStatusRef.current?.();
          }
        } else if (err.code === 'subscription_conflict') {
          void onRefreshStatusRef.current?.();
        }
      }
      if (!handedOffFromEmbedded && isMountedRef.current && err instanceof ApiError && [400, 404, 409].includes(err.status)) {
        await fetchStatusAndTemplates();
      }
      const msg = err instanceof Error ? err.message : 'Failed to start checkout. Please try again.';
      showToastRef.current(msg, 'error');
    } finally {
      if (isMountedRef.current && !redirectingRef.current) {
        purchaseStarting.current = false;
        setIsProcessing(false);
      }
    }
  };

  const visibleSlotIds = new Set([
    ...subscriptionPacks.map((pack) => pack.id),
    ...coinPacks.map((pack) => pack.id),
  ]);
  const selectedSlot = selectedPack !== null && visibleSlotIds.has(selectedPack)
    ? slots.find((p) => p.id === selectedPack)
    : undefined;
  const selectedSubscriptionPack = selectedSlot ? subscriptionPacks.find((p) => p.id === selectedSlot.id) : undefined;
  const selectedSubscription = Boolean(selectedSubscriptionPack);
  const selectedPayPalAvailable = Boolean(
    selectedSlot &&
    (selectedSlot.paypal_ready ?? true) &&
    (selectedSubscription ? paymentMethods.paypal_vip : paymentMethods.paypal_single),
  );
  const selectedStripeAvailable = Boolean(
    selectedSlot &&
    (selectedSlot.stripe_ready ?? true) &&
    (selectedSubscription
      ? paymentMethods.stripe_vip || paymentMethods.stripe_vip_slots.includes(selectedSlot.id)
      : paymentMethods.stripe_single || paymentMethods.stripe_slots.includes(selectedSlot.id)),
  );
  const anyPayPalAvailable = selectedSlot
    ? selectedPayPalAvailable
    : paymentMethods.paypal_single || paymentMethods.paypal_vip;
  const anyStripeAvailable = selectedSlot
    ? selectedStripeAvailable
    : paymentMethods.stripe_single || paymentMethods.stripe_vip;
  const effectiveProvider: PaymentProvider =
    selectedProvider === 'paypal'
      ? anyPayPalAvailable
        ? 'paypal'
        : anyStripeAvailable
          ? 'stripe'
          : 'paypal'
      : anyStripeAvailable
        ? 'stripe'
        : anyPayPalAvailable
          ? 'paypal'
          : 'stripe';

  useEffect(() => {
    if (isOffersLoading) return;
    if (selectedProvider === 'paypal' && !anyPayPalAvailable && anyStripeAvailable) {
      setSelectedProvider('stripe');
    } else if (selectedProvider === 'stripe' && !anyStripeAvailable && anyPayPalAvailable) {
      setSelectedProvider('paypal');
    }
  }, [isOffersLoading, selectedProvider, anyPayPalAvailable, anyStripeAvailable]);

  const cardLabel = 'Card';
  const selectedSummaryHint = !selectedSlot
    ? ''
    : selectedSubscriptionPack
      ? `Auto-renews at ${selectedSubscriptionPack.price}/${selectedSubscriptionPack.cycle} · Cancel anytime`
      : selectedSlot.type === 'whole_book'
        ? 'One-time unlock · Forever access'
        : `Instant delivery · ${(selectedSlot.coins + selectedSlot.bonus).toLocaleString()} total coins never expire`;

  if (isOffersLoading) {
    return (
      <div
        style={{
          display: 'flex',
          flexDirection: embedded ? 'row' : 'column',
          alignItems: 'center',
          justifyContent: 'center',
          gap: '10px',
          padding: embedded ? '24px 0' : '80px 0',
          color: 'var(--text-secondary)',
        }}
      >
        <div
          className="sn-spinner"
          style={embedded ? { width: '24px', height: '24px', borderWidth: '2.5px' } : undefined}
        />
        <span style={{ fontSize: embedded ? '0.8125rem' : '0.875rem' }}>
          {embedded ? 'Loading top-up options...' : 'Loading subscription plans...'}
        </span>
      </div>
    );
  }

  return (
    <>
      <div className={`recharge-panel-stack${embedded ? ' is-embedded' : ''}`}>
        {/* 1. Top Payment Method Selector Pills (PayPal / Card) */}
        {(subscriptionPacks.length > 0 || coinPacks.length > 0) && (anyPayPalAvailable || anyStripeAvailable) && (
          <div className="provider-selector" role="radiogroup" aria-label="Payment method">
            <button
              type="button"
              role="radio"
              aria-checked={effectiveProvider === 'paypal'}
              disabled={!anyPayPalAvailable || isProcessing}
              className={`provider-pill${effectiveProvider === 'paypal' ? ' is-active' : ''}`}
              onClick={() => setSelectedProvider('paypal')}
            >
              <svg width="17" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true" style={{ flexShrink: 0 }}>
                <defs>
                  <clipPath id={paypalClipId}>
                    <path d="M5.8 1.5h7.4c4.2 0 6.4 2 5.6 6-.7 3.6-3.4 5.5-7.4 5.5H8.5l-1.1 7.2H3.0L5.8 1.5Z" />
                  </clipPath>
                </defs>
                <path
                  className="recharge-paypal-back"
                  d="M5.8 1.5h7.4c4.2 0 6.4 2 5.6 6-.7 3.6-3.4 5.5-7.4 5.5H8.5l-1.1 7.2H3.0L5.8 1.5Z"
                />
                <path
                  className="recharge-paypal-front"
                  d="M8.5 5.2h6.3c3.9 0 5.9 1.9 5.2 5.5-.7 3.3-3.1 5.1-6.8 5.1h-2.5l-1.1 6.7H5.9L8.5 5.2Z"
                />
                <path
                  className="recharge-paypal-overlap"
                  clipPath={`url(#${paypalClipId})`}
                  d="M8.5 5.2h6.3c3.9 0 5.9 1.9 5.2 5.5-.7 3.3-3.1 5.1-6.8 5.1h-2.5l-1.1 6.7H5.9L8.5 5.2Z"
                />
              </svg>
              <span className="provider-pill-paypal-wordmark">
                <span className="checkout-pp-pay">Pay</span>
                <span className="checkout-pp-pal">Pal</span>
              </span>
            </button>

            <button
              type="button"
              role="radio"
              aria-checked={effectiveProvider === 'stripe'}
              disabled={!anyStripeAvailable || isProcessing}
              className={`provider-pill${effectiveProvider === 'stripe' ? ' is-active' : ''}`}
              onClick={() => setSelectedProvider('stripe')}
            >
              <CreditCard size={17} strokeWidth={2} aria-hidden="true" style={{ flexShrink: 0 }} />
              <span>{cardLabel}</span>
            </button>
          </div>
        )}

        {/* 2. Compact Unlimited Reading Carousel Banner */}
        {subscriptionPacks.length > 0 && (
          <section className="vip-banner-section" aria-label="Unlimited Reading plans">
            <div
              className={`vip-banner-shell${subscriptionPacks[activeSlideIndex]?.id === selectedPack ? ' is-selected' : ''}`}
            >
              <div
                ref={carouselRef}
                className="vip-carousel-track"
                aria-live="off"
                onFocus={() => setAutoRotatePaused(true)}
                onPointerDown={() => {
                  programmaticScrollUntilRef.current = 0;
                  setAutoRotatePaused(true);
                }}
                onTouchStart={() => {
                  programmaticScrollUntilRef.current = 0;
                  setAutoRotatePaused(true);
                }}
                onWheel={() => {
                  programmaticScrollUntilRef.current = 0;
                  setAutoRotatePaused(true);
                }}
                onScroll={(e) => {
                  if (Date.now() < programmaticScrollUntilRef.current) return;
                  const track = e.currentTarget;
                  if (track.clientWidth <= 0) return;
                  const nextIndex = Math.max(
                    0,
                    Math.min(subscriptionPacks.length - 1, Math.round(track.scrollLeft / track.clientWidth)),
                  );
                  if (nextIndex !== activeSlideIndexRef.current) {
                    activeSlideIndexRef.current = nextIndex;
                    setActiveSlideIndex(nextIndex);
                    if (selectedSubscription && subscriptionPacks[nextIndex]) {
                      userManuallySelectedRef.current = true;
                      setSelectedPack(subscriptionPacks[nextIndex].id);
                    }
                  }
                }}
              >
                {subscriptionPacks.map((pack) => {
                  const isSelected = selectedPack === pack.id;
                  const eqPrice = formatSubscriptionEquivalent(pack.priceCents, pack.cycle);
                  return (
                    <div key={pack.id} className="vip-carousel-slide">
                      <button
                        type="button"
                        onClick={() => handleSelectPack(pack.id)}
                        aria-pressed={isSelected}
                        className="vip-banner-card"
                      >
                        {pack.badge && <span className="vip-banner-ribbon">{pack.badge}</span>}
                        <div className={`vip-banner-body${pack.badge ? ' has-ribbon' : ''}`}>
                          <div className="vip-banner-left">
                            <div className="vip-banner-icon" aria-hidden="true">
                              <Crown size={19} strokeWidth={1.9} />
                            </div>
                            <div className="vip-banner-copy">
                              <div className="vip-banner-title-row">
                                <span className="vip-banner-name">{sanitizeSubscriptionCopy(pack.name)}</span>
                                {eqPrice && <span className="vip-banner-eq tabular-nums">{eqPrice.replace(/^≈\s*/, '')}</span>}
                              </div>
                              <span className="vip-banner-desc">{sanitizeSubscriptionCopy(pack.desc)}</span>
                            </div>
                          </div>
                          <div className="vip-banner-right">
                            <span className="vip-banner-price-pill tabular-nums">{pack.price}</span>
                            {pack.listPrice && (
                              <span className="vip-banner-list-price tabular-nums">{pack.listPrice}</span>
                            )}
                          </div>
                        </div>
                      </button>
                    </div>
                  );
                })}
              </div>

              {subscriptionPacks.length > 1 && (
                <div className="vip-carousel-dots" role="group" aria-label="Subscription plan slides">
                  {subscriptionPacks.map((pack, idx) => (
                    <button
                      key={pack.id}
                      type="button"
                      aria-pressed={activeSlideIndex === idx}
                      aria-label={`View ${sanitizeSubscriptionCopy(pack.name)}`}
                      className={`vip-carousel-dot${activeSlideIndex === idx ? ' is-active' : ''}`}
                      onClick={() => {
                        setAutoRotatePaused(true);
                        activeSlideIndexRef.current = idx;
                        setActiveSlideIndex(idx);
                        scrollCarouselTo(idx, 'smooth');
                        handleSelectPack(pack.id);
                      }}
                    />
                  ))}
                </div>
              )}
            </div>
          </section>
        )}

        {/* 3. Compact 2x2 Coin Packs Grid */}
        {coinPacks.length > 0 && (
          <section className="coin-packs-section" aria-label="Coin packages">
            <div className="coin-packs-grid">
              {coinPacks.map((pack) => {
                const isSelected = selectedPack === pack.id;
                const bonusPct = pack.type === 'single' ? calcCoinBonusPercent(pack.coins, pack.bonus) : '';
                const isWholeBook = pack.type === 'whole_book';
                return (
                  <button
                    type="button"
                    key={pack.id}
                    onClick={() => handleSelectPack(pack.id)}
                    aria-pressed={isSelected}
                    className={`recharge-coin-card${isSelected ? ' is-selected' : ''}${isWholeBook ? ' coin-pack-whole-book' : ''}${!isWholeBook && pack.badge ? ' has-tag' : ''}`}
                  >
                    {!isWholeBook && pack.badge && <span className="recharge-coin-tag">{pack.badge}</span>}
                    {isWholeBook ? (
                      <div className="recharge-whole-book-body">
                        <div style={{ minWidth: 0 }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                            <BookOpen size={15} strokeWidth={1.9} style={{ color: 'var(--accent-color)', flexShrink: 0 }} aria-hidden="true" />
                            <span style={{ fontSize: '0.9375rem', fontWeight: 700, color: 'var(--text-primary)', lineHeight: 1.25 }}>
                              {pack.name || 'Unlock Entire Novel'}
                            </span>
                          </div>
                          <span className="recharge-whole-book-sub">
                            {novelTitle ? `${novelTitle} · Forever access` : 'One-time unlock · Forever access'}
                          </span>
                        </div>
                        <div className="recharge-whole-book-prices tabular-nums">
                          <span className="recharge-coin-price">{pack.price}</span>
                          {pack.listPrice && <span className="recharge-list-price">{pack.listPrice}</span>}
                        </div>
                      </div>
                    ) : (
                      <>
                        <div className="recharge-coin-top">
                          <GoldCoin size={16} />
                          <span className="recharge-coin-amount tabular-nums">{pack.coins.toLocaleString()}</span>
                          {pack.bonus > 0 && (
                            <span className="recharge-coin-bonus tabular-nums">+{pack.bonus.toLocaleString()}</span>
                          )}
                          {bonusPct && <span className="recharge-coin-pct tabular-nums">{bonusPct}</span>}
                        </div>
                        <div className="recharge-coin-footer tabular-nums">
                          <span className="recharge-coin-price">{pack.price}</span>
                          {pack.listPrice && <span className="recharge-list-price">{pack.listPrice}</span>}
                        </div>
                      </>
                    )}
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
              padding: '28px 20px',
              border: '1px solid var(--border-color)',
              textAlign: 'center',
              color: 'var(--text-secondary)',
              fontSize: '0.875rem',
            }}
          >
            No subscription or coin packages are currently available. Please try again later.
          </div>
        )}

        {/* 4. Single Full-Width Primary CTA (directly below SKUs) */}
        {selectedSlot ? (
          <section className="recharge-checkout-section" aria-label="Checkout">
            {selectedPayPalAvailable || selectedStripeAvailable ? (
              <>
                <button
                  type="button"
                  className="recharge-pay-now-btn"
                  onClick={() => void handlePurchase(effectiveProvider)}
                  disabled={isProcessing}
                >
                  <span>
                    {isProcessing ? 'Connecting...' : 'Pay Now'}
                  </span>
                </button>
                <div className="recharge-pay-trust">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
                    <rect x="3" y="11" width="18" height="11" rx="2" />
                    <path d="M7 11V7a5 5 0 0 1 10 0v4" />
                  </svg>
                  <span>{selectedSummaryHint}</span>
                </div>
              </>
            ) : (
              <p role="status" style={{ textAlign: 'center', fontSize: '0.8125rem', color: 'var(--text-secondary)', margin: 0 }}>
                No payment channel is currently active for the selected item.
              </p>
            )}
          </section>
        ) : (subscriptionPacks.length > 0 || coinPacks.length > 0) ? (
          <section className="recharge-checkout-section" aria-label="Package selection prompt">
            <p role="status" style={{ textAlign: 'center', fontSize: '0.8125rem', color: 'var(--text-secondary)', margin: 0 }}>
              {returnedLookupState === 'loading'
                ? 'Checking your previous checkout… or select a package above to continue.'
                : 'Select a package above to continue.'}
            </p>
          </section>
        ) : null}
      </div>

      {/* Payment Redirect Brand Transition Overlay */}
      {isProcessing && (
        <div className="checkout-transition-backdrop" role="status" aria-live="polite" aria-busy="true">
          <div className={`checkout-transition-card checkout-transition-card--${activeCheckoutProvider}`}>
            {activeCheckoutProvider === 'paypal' ? (
              <>
                <div className="checkout-transition-brand">
                  <svg width="26" height="28" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                    <defs>
                      <clipPath id={modalPaypalClipId}>
                        <path d="M5.8 1.5h7.4c4.2 0 6.4 2 5.6 6-.7 3.6-3.4 5.5-7.4 5.5H8.5l-1.1 7.2H3.0L5.8 1.5Z" />
                      </clipPath>
                    </defs>
                    <path
                      fill="#002C8A"
                      d="M5.8 1.5h7.4c4.2 0 6.4 2 5.6 6-.7 3.6-3.4 5.5-7.4 5.5H8.5l-1.1 7.2H3.0L5.8 1.5Z"
                    />
                    <path
                      fill="#009CDE"
                      d="M8.5 5.2h6.3c3.9 0 5.9 1.9 5.2 5.5-.7 3.3-3.1 5.1-6.8 5.1h-2.5l-1.1 6.7H5.9L8.5 5.2Z"
                    />
                    <path
                      fill="#001C64"
                      clipPath={`url(#${modalPaypalClipId})`}
                      d="M8.5 5.2h6.3c3.9 0 5.9 1.9 5.2 5.5-.7 3.3-3.1 5.1-6.8 5.1h-2.5l-1.1 6.7H5.9L8.5 5.2Z"
                    />
                  </svg>
                  <span className="checkout-transition-wordmark checkout-transition-wordmark--paypal">
                    <span className="checkout-pp-pay">Pay</span>
                    <span className="checkout-pp-pal">Pal</span>
                  </span>
                </div>
                <p className="checkout-transition-subtitle">Secure checkout via PayPal.</p>
                <svg className="checkout-transition-spin" width="38" height="38" viewBox="0 0 40 40" fill="none" aria-hidden="true">
                  <circle className="checkout-transition-spinner-track" cx="20" cy="20" r="16" strokeWidth="3.6" />
                  <path d="M20 4 A16 16 0 1 1 6.2 28" stroke="#002C8A" strokeWidth="3.6" strokeLinecap="round" />
                  <path d="M13 5.6 A16 16 0 0 1 25 4.8" stroke="#009CDE" strokeWidth="3.6" strokeLinecap="round" />
                </svg>
              </>
            ) : (
              <>
                <div className="checkout-transition-brand">
                  <svg width="28" height="28" viewBox="0 0 28 28" fill="none" aria-hidden="true">
                    <rect width="28" height="28" rx="4.5" fill="#533AFD" />
                    <path fill="#FAF9F6" d="M6.3 9.2L21.7 5.2V18.8L6.3 22.8Z" />
                  </svg>
                  <span className="checkout-transition-wordmark checkout-transition-wordmark--stripe">stripe</span>
                </div>
                <p className="checkout-transition-subtitle">Secure checkout via stripe</p>
                <svg className="checkout-transition-spin" width="38" height="38" viewBox="0 0 40 40" fill="none" aria-hidden="true">
                  <circle className="checkout-transition-spinner-track" cx="20" cy="20" r="16" strokeWidth="3.6" />
                  <path d="M20 4 A16 16 0 1 1 6.2 28" stroke="#533AFD" strokeWidth="3.6" strokeLinecap="round" />
                  <path d="M13 5.6 A16 16 0 0 1 25 4.8" stroke="#00D4B6" strokeWidth="3.6" strokeLinecap="round" />
                </svg>
              </>
            )}
          </div>
        </div>
      )}
    </>
  );
};

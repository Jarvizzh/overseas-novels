import { readerStorage } from '../utils/readerStorage';
import React, { useState, useEffect, useCallback, useRef, useId } from 'react';
import { ChevronLeft, Crown, Sparkles, BookOpen, CreditCard } from 'lucide-react';
import { useToast } from '../context/toastStore';
import { GoldCoin } from '../components/GoldCoin';
import { api, ApiError, type PurchaseIntent } from '../utils/api';
import { LegalModal, type LegalModalType } from '../components/LegalModal';
import {
  buildCoinPacks,
  buildSubscriptionPacks,
  calcCoinBonusPercent,
  formatSubscriptionEquivalent,
  resolveInitialCarouselIndex,
  type RechargeSlotItem,
} from '../utils/rechargeCatalogHelpers';
import {
  isPendingPurchaseStatus,
  resolveRechargeSelection,
  type ReturnedLookupState,
} from '../utils/rechargeSelectionHelpers';
import {
  evaluatePurchaseLookupOutcome,
  isCheckoutPendingConflict,
} from '../utils/checkoutPendingHelpers';

type PaymentProvider = 'paypal' | 'stripe';

function isSubscriptionSlot(slot: RechargeSlotItem): boolean {
  return slot.type === 'subscription' || slot.type === 'vip' || slot.product_type === 'vip';
}

function startCheckout(provider: PaymentProvider, intent: string, signal?: AbortSignal) {
  return provider === 'stripe' ? api.startStripeCheckout(intent, signal) : api.startPayPalCheckout(intent, signal);
}

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

function readingReturnPath(value: unknown): string {
  if (typeof value !== 'string' || !value) return '';
  try {
    const target = new URL(value, window.location.origin);
    return target.origin !== 'null' && target.origin === window.location.origin && target.pathname === '/content'
      ? target.pathname + target.search : '';
  } catch {
    return '';
  }
}

const purchaseRequestStorageKey = (userId: number, slot: number, productId: number, novel: number) =>
  `pending-purchase:${userId}:${slot}:${productId}:${novel}`;

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
  const [selectedPack, setSelectedPack] = useState<number | null>(null);
  const [isProcessing, setIsProcessing] = useState(false);
  const [paymentStep, setPaymentStep] = useState(0);
  const [activeCheckoutProvider, setActiveCheckoutProvider] = useState<PaymentProvider>('paypal');
  const [showSuccess, setShowSuccess] = useState(false);
  const [successAmount, setSuccessAmount] = useState(0);
  const [isWholeBookSuccess, setIsWholeBookSuccess] = useState(false);
  const hasReturnedFromPaymentRef = useRef(false);
  const pendingReturnPathRef = useRef('');
  const [purchaseLocator, setPurchaseLocator] = useState(initialPurchaseLocator);
  const [purchaseRefresh, setPurchaseRefresh] = useState(0);
  const [returnedPendingIntent, setReturnedPendingIntent] = useState<PurchaseIntent | null>(null);
  const [returnedLookupState, setReturnedLookupState] = useState<ReturnedLookupState>(
    () => (purchaseLocator.intent || purchaseLocator.token ? 'loading' : 'none'),
  );
  const selectedPackRef = useRef<number | null>(null);
  selectedPackRef.current = selectedPack;
  const userManuallySelectedRef = useRef(false);
  const staleReturnedIntentToastRef = useRef('');
  const shownPurchaseRef = useRef('');
  const shownFailureRef = useRef('');
  const purchaseStarting = useRef(false);
  const purchaseScope = useRef<AbortController | null>(null);
  const requestDraft = useRef<{ storageKey: string; requestKey: string; returnPath: string; intentId?: string } | null>(null);
  const [isVipSuccess, setIsVipSuccess] = useState(false);
  const [vipSuccessTitle, setVipSuccessTitle] = useState('');
  const [activeLegalModal, setActiveLegalModal] = useState<LegalModalType | null>(null);
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

  const successDialogRef = useRef<HTMLDialogElement>(null);
  const pressedOutsideRef = useRef(false);
  const successTitleId = useId();
  const paypalClipId = useId();

  useEffect(() => {
    const dialog = successDialogRef.current;
    if (!dialog) return;
    if (showSuccess && !dialog.open) {
      dialog.showModal();
    } else if (!showSuccess && dialog.open) {
      dialog.close();
    }
  }, [showSuccess]);

  const fetchStatusAndTemplates = useCallback(async () => {
    offersRequestRef.current?.abort();
    const controller = new AbortController();
    offersRequestRef.current = controller;
    setLoadingTemplates(true);
    try {
      const [templateData, methods] = await Promise.all([
        api.getRechargeTemplates(novelId, controller.signal),
        api.getPaymentMethods(controller.signal).catch(() => emptyPaymentMethods),
      ]);

      if (controller.signal.aborted) return;
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
    if (loadingTemplates || offersNovelId !== novelId) return;
    const visibleIds = new Set([
      ...buildSubscriptionPacks(slots).map((pack) => pack.id),
      ...buildCoinPacks(slots, novelId).map((pack) => pack.id),
    ]);
    const visibleSlots = slots.filter((slot) => visibleIds.has(slot.id));
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
  }, [slots, offersNovelId, novelId, loadingTemplates, returnedPendingIntent, returnedLookupState]);

  const handleSelectPack = (packId: number) => {
    userManuallySelectedRef.current = true;
    setAutoRotatePaused(true);
    setSelectedPack(packId);
  };

  useEffect(() => {
    if (!authReady) return;
    void fetchStatusAndTemplates();
    return () => offersRequestRef.current?.abort();
  }, [authReady, fetchStatusAndTemplates]);

  useEffect(() => {
    const controller = new AbortController();
    purchaseScope.current = controller;
    return () => controller.abort();
  }, []);

  const onRefreshStatusRef = useRef(onRefreshStatus);
  useEffect(() => {
    onRefreshStatusRef.current = onRefreshStatus;
  }, [onRefreshStatus]);

  const showToastRef = useRef(showToast);
  useEffect(() => {
    showToastRef.current = showToast;
  }, [showToast]);

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
          const storageKey = purchaseRequestStorageKey(userId, intent.slot_id, intent.product_id || 0, intent.novel_id || 0);
          try {
            const saved = JSON.parse(sessionStorage.getItem(storageKey) || 'null');
            if (saved?.intentId === intent.intent_id) sessionStorage.removeItem(storageKey);
          } catch {
            // Server-owned purchase status does not depend on the draft.
          }
          if (requestDraft.current?.intentId === intent.intent_id) requestDraft.current = null;
          if (decision.shouldShowSuccess && shownPurchaseRef.current !== (intent.intent_id || intent.order_id)) {
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
          if (!purchaseLocator.cancelled) {
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

  const subscriptionPacks = buildSubscriptionPacks(slots);
  const coinPacks = buildCoinPacks(slots, novelId);

  const scrollCarouselTo = useCallback((index: number, behavior: ScrollBehavior = 'smooth') => {
    const track = carouselRef.current;
    if (!track || track.clientWidth <= 0) return;
    programmaticScrollUntilRef.current = behavior === 'smooth' ? Date.now() + 600 : 0;
    track.scrollTo({ left: index * track.clientWidth, behavior });
  }, []);

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
      const next = (activeSlideIndexRef.current + 1) % subscriptionPacks.length;
      activeSlideIndexRef.current = next;
      setActiveSlideIndex(next);
      scrollCarouselTo(next, 'smooth');
    }, 3000);

    return () => window.clearInterval(intervalId);
  }, [loadingTemplates, offersNovelId, novelId, subscriptionPacks.length, autoRotatePaused, isProcessing, scrollCarouselTo]);

  const startUnifiedPurchase = async (tSlot: RechargeSlotItem, book: number, returnPath: string, provider: PaymentProvider) => {
    if (!userId) return;
    const signal = purchaseScope.current?.signal;
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
        if (!signal?.aborted && err instanceof ApiError && isCheckoutPendingConflict(err)) {
          showPurchase(intent.intent_id);
          setPurchaseRefresh((value) => value + 1);
        }
        throw err;
      }
      if (signal?.aborted) return;
      showPurchase(intent.intent_id);
      if (checkout.approve_url) {
        window.location.assign(checkout.approve_url);
      } else {
        if (checkout.confirmation_message) {
          showToast(checkout.confirmation_message, 'error');
        }
        setPurchaseRefresh((value) => value + 1);
      }
    } else {
      showPurchase(intent.intent_id);
      if (intent.confirmation_message) {
        showToast(intent.confirmation_message, 'error');
      }
    }
  };

  const handlePurchase = async (provider: PaymentProvider) => {
    if (selectedPack === null || purchaseStarting.current || isProcessing || loadingTemplates || offersNovelId !== novelId) return;
    const tSlot = slots.find((p) => p.id === selectedPack);
    if (!tSlot) return;

    purchaseStarting.current = true;
    setActiveCheckoutProvider(provider);
    setIsProcessing(true);
    setPaymentStep(1);

    try {
      const returnPath = readingReturnPath(readerStorage.getItem('last_read_content_path')) || (novelId ? `/content?novel_id=${novelId}` : '');
      const book = tSlot.type === 'whole_book' ? novelId || 0 : 0;
      await startUnifiedPurchase(tSlot, book, returnPath, provider);
    } catch (err) {
      setIsProcessing(false);
      setPaymentStep(0);
      if (err instanceof ApiError && err.status === 409) {
        if (
          err.code &&
          ['vip_plan_switch_approval_open', 'vip_plan_switch_pending', 'vip_plan_switch_confirming'].includes(err.code)
        ) {
          if (err.currentIntent?.intent_id) {
            showPurchase(err.currentIntent.intent_id);
            setPurchaseRefresh((value) => value + 1);
          }
          if (err.code === 'vip_plan_switch_confirming') {
            void onRefreshStatusRef.current?.();
          }
        } else if (err.code === 'subscription_conflict') {
          void onRefreshStatusRef.current?.();
        }
      }
      if (err instanceof ApiError && [400, 404, 409].includes(err.status)) {
        await fetchStatusAndTemplates();
      }
      const msg = err instanceof Error ? err.message : 'Failed to start checkout. Please try again.';
      showToast(msg, 'error');
    } finally {
      purchaseStarting.current = false;
      setIsProcessing(false);
      setPaymentStep(0);
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
  useEffect(() => {
    hasAlignedInitialSlideRef.current = false;
  }, [novelId]);

  const cardLabel = 'Card';
  const selectedSummaryTitle = !selectedSlot
    ? ''
    : selectedSubscriptionPack
      ? selectedSubscriptionPack.name
      : selectedSlot.type === 'whole_book'
        ? selectedSlot.display_alias || selectedSlot.name || 'Unlock Entire Novel'
        : `${selectedSlot.coins.toLocaleString()} Coins${selectedSlot.bonus > 0 ? ` (+${selectedSlot.bonus} bonus)` : ''}`;
  const selectedSummaryPrice = !selectedSlot
    ? ''
    : selectedSubscriptionPack
      ? `${selectedSubscriptionPack.price}/${selectedSubscriptionPack.cycle}`
      : selectedSlot.price;
  const selectedSummaryHint = !selectedSlot
    ? ''
    : selectedSubscriptionPack
      ? `Auto-renews at ${selectedSubscriptionPack.price}/${selectedSubscriptionPack.cycle} · Cancel anytime`
      : selectedSlot.type === 'whole_book'
        ? 'One-time unlock · Forever access'
        : 'Instant delivery · Coins never expire';

  const handleReturnToContent = () => {
    let returnPath = readingReturnPath(pendingReturnPathRef.current);
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

  const isOffersLoading = loadingTemplates || offersNovelId !== novelId;

  return (
    <>
      {isOffersLoading ? (
        <div className="page-container-full" style={{ alignItems: 'center', justifyContent: 'center', gap: '12px', color: 'var(--text-secondary)' }}>
          <div className="sn-spinner" />
          <span style={{ fontSize: '0.875rem' }}>Loading membership plans...</span>
        </div>
      ) : (
        <div className="page-container-full animate-fade-in" style={{ backgroundColor: 'var(--bg-primary)' }}>
          {/* Top Bar */}
          <header className="detail-top-bar glass-panel">
            <div className="sn-narrow-container" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '8px' }}>
              <button
                type="button"
                className="header-btn"
                onClick={() => {
                  if (hasReturnedFromPaymentRef.current) {
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

          <div className="scroll-container recharge-scroll-container" style={{ flex: 1, minHeight: 0 }}>
            <div className="sn-narrow-container" style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
              {/* Unlimited Reading Full-Width Carousel */}
              {subscriptionPacks.length > 0 && (
                <section style={{ display: 'flex', flexDirection: 'column', gap: '10px' }} aria-label="Unlimited Reading plans">
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: '12px' }}>
                    <h2 className="hot-tags-title" style={{ margin: 0 }}>Unlimited Reading</h2>
                    {subscriptionPacks.length > 1 && (
                      <button
                        type="button"
                        className="vip-carousel-status-btn tabular-nums"
                        onClick={() => setAutoRotatePaused((prev) => !prev)}
                        aria-label={autoRotatePaused ? 'Resume plan auto-rotation' : 'Pause plan auto-rotation'}
                      >
                        {autoRotatePaused ? '' : 'Auto · '}
                        {Math.min(activeSlideIndex + 1, subscriptionPacks.length)}/{subscriptionPacks.length}
                      </button>
                    )}
                  </div>

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
                            className={`vip-carousel-card${isSelected ? ' is-selected' : ''}`}
                          >
                            <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: '10px' }}>
                              <div style={{ display: 'flex', alignItems: 'center', gap: '8px', minWidth: 0 }}>
                                <Crown
                                  size={18}
                                  strokeWidth={1.9}
                                  style={{ color: isSelected ? '#E2BB6B' : 'var(--accent-color)', flexShrink: 0 }}
                                  aria-hidden="true"
                                />
                                <span
                                  style={{
                                    fontFamily: 'var(--font-display)',
                                    fontSize: '1.0625rem',
                                    fontWeight: 600,
                                    overflow: 'hidden',
                                    textOverflow: 'ellipsis',
                                    whiteSpace: 'nowrap',
                                  }}
                                >
                                  {pack.name}
                                </span>
                              </div>
                              {pack.badge && (
                                <span
                                  style={{
                                    fontSize: '0.625rem',
                                    backgroundColor: isSelected ? 'rgba(226, 187, 107, 0.24)' : 'var(--accent-light)',
                                    color: isSelected ? '#E2BB6B' : 'var(--accent-text)',
                                    border: isSelected ? '1px solid rgba(226, 187, 107, 0.42)' : 'none',
                                    fontWeight: 700,
                                    padding: '2px 8px',
                                    borderRadius: '999px',
                                    whiteSpace: 'nowrap',
                                    flexShrink: 0,
                                  }}
                                >
                                  {pack.badge}
                                </span>
                              )}
                            </div>

                            <span
                              style={{
                                display: 'block',
                                fontSize: '0.75rem',
                                color: isSelected ? 'rgba(250, 248, 245, 0.76)' : 'var(--text-secondary)',
                                lineHeight: 1.45,
                              }}
                            >
                              {pack.desc}
                            </span>

                            <div
                              style={{
                                display: 'flex',
                                alignItems: 'baseline',
                                justifyContent: 'space-between',
                                gap: '12px',
                                paddingTop: '10px',
                                borderTop: isSelected
                                  ? '1px solid rgba(255, 255, 255, 0.14)'
                                  : '1px solid var(--border-color)',
                              }}
                            >
                              <span
                                className="tabular-nums"
                                style={{
                                  fontSize: '0.75rem',
                                  fontWeight: 600,
                                  color: isSelected ? '#E2BB6B' : 'var(--accent-text)',
                                }}
                              >
                                {eqPrice || 'Cancel anytime'}
                              </span>
                              <span className="tabular-nums" style={{ fontSize: '1.125rem', fontWeight: 700 }}>
                                {pack.price}
                                <span style={{ fontSize: '0.75rem', fontWeight: 400, opacity: 0.72 }}>/{pack.cycle}</span>
                              </span>
                            </div>
                          </button>
                        </div>
                      );
                    })}
                  </div>

                  {subscriptionPacks.length > 1 && (
                    <div className="vip-carousel-dots" role="group" aria-label="Membership plan slides">
                      {subscriptionPacks.map((pack, idx) => (
                        <button
                          key={pack.id}
                          type="button"
                          aria-pressed={activeSlideIndex === idx}
                          aria-label={`View ${pack.name}`}
                          className={`vip-carousel-dot${activeSlideIndex === idx ? ' is-active' : ''}`}
                          onClick={() => {
                            setAutoRotatePaused(true);
                            activeSlideIndexRef.current = idx;
                            setActiveSlideIndex(idx);
                            scrollCarouselTo(idx, 'smooth');
                          }}
                        />
                      ))}
                    </div>
                  )}
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
                      const bonusPct = pack.type === 'single' ? calcCoinBonusPercent(pack.coins, pack.bonus) : '';
                      const isWholeBook = pack.type === 'whole_book';
                      return (
                        <button
                          type="button"
                          key={pack.id}
                          onClick={() => handleSelectPack(pack.id)}
                          aria-pressed={isSelected}
                          className={`recharge-coin-card${isSelected ? ' is-selected' : ''}${isWholeBook ? ' coin-pack-whole-book' : ''}`}
                        >
                          {isWholeBook ? (
                            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '12px' }}>
                              <div style={{ minWidth: 0 }}>
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
                                  {novelTitle ? `${novelTitle} · Forever access` : 'One-time unlock · Forever access'}
                                </span>
                              </div>
                              <span className="tabular-nums" style={{ fontSize: '0.9375rem', fontWeight: 700, color: 'var(--text-primary)', flexShrink: 0 }}>
                                {pack.price}
                              </span>
                            </div>
                          ) : (
                            <>
                              <div>
                                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '6px' }}>
                                  <div style={{ display: 'flex', alignItems: 'center', gap: '6px', minWidth: 0 }}>
                                    <GoldCoin size={15} />
                                    <span className="tabular-nums" style={{ fontSize: '1.0625rem', fontWeight: 700, color: 'var(--text-primary)' }}>
                                      {pack.coins.toLocaleString()}
                                    </span>
                                  </div>
                                  {bonusPct && (
                                    <span
                                      className="tabular-nums"
                                      style={{
                                        backgroundColor: 'var(--accent-light)',
                                        color: 'var(--accent-text)',
                                        fontSize: '0.625rem',
                                        fontWeight: 700,
                                        padding: '2px 7px',
                                        borderRadius: '999px',
                                        flexShrink: 0,
                                      }}
                                    >
                                      {bonusPct}
                                    </span>
                                  )}
                                </div>
                                <span className="tabular-nums" style={{ fontSize: '0.6875rem', color: 'var(--text-tertiary)', display: 'block', marginTop: '3px' }}>
                                  {pack.bonus > 0 ? `+${pack.bonus.toLocaleString()} bonus` : pack.name || 'Coin Top-up'}
                                </span>
                              </div>

                              <div
                                style={{
                                  display: 'flex',
                                  alignItems: 'baseline',
                                  justifyContent: 'space-between',
                                  gap: '8px',
                                  paddingTop: '8px',
                                  borderTop: '1px solid var(--border-color)',
                                }}
                              >
                                <span className="tabular-nums" style={{ fontSize: '0.6875rem', color: 'var(--text-tertiary)' }}>
                                  {pack.total.toLocaleString()} total
                                </span>
                                <span className="tabular-nums" style={{ fontSize: '0.9375rem', fontWeight: 700, color: 'var(--text-primary)' }}>
                                  {pack.price}
                                </span>
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
                <p>• <strong>Unlimited Membership:</strong> Instant access to all novel chapters across the site without consuming coins.</p>
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
                  paddingBottom: '8px',
                  fontSize: '0.75rem',
                  color: 'var(--text-tertiary)',
                }}
              >
                <button type="button" onClick={() => setActiveLegalModal('terms')} style={{ background: 'none', border: 'none', padding: 0, color: 'inherit', cursor: 'pointer', textDecoration: 'underline' }}>
                  Terms of Service
                </button>
                <span aria-hidden="true">·</span>
                <button type="button" onClick={() => setActiveLegalModal('privacy')} style={{ background: 'none', border: 'none', padding: 0, color: 'inherit', cursor: 'pointer', textDecoration: 'underline' }}>
                  Privacy Policy
                </button>
                <span aria-hidden="true">·</span>
                <button type="button" onClick={() => setActiveLegalModal('refund')} style={{ background: 'none', border: 'none', padding: 0, color: 'inherit', cursor: 'pointer', textDecoration: 'underline' }}>
                  Refund Policy
                </button>
                <span aria-hidden="true">·</span>
                <button type="button" onClick={() => setActiveLegalModal('contact')} style={{ background: 'none', border: 'none', padding: 0, color: 'inherit', cursor: 'pointer', textDecoration: 'underline' }}>
                  Contact Us
                </button>
              </div>
            </div>
          </div>

          {/* Sticky Bottom Checkout Dock */}
          {selectedSlot ? (
            <section className="recharge-checkout-dock" aria-label="Payment methods">
              <div className="sn-narrow-container recharge-checkout-section">
                <div className="recharge-checkout-summary">
                  <div style={{ minWidth: 0 }}>
                    <div className="recharge-checkout-summary-title">{selectedSummaryTitle}</div>
                    <div className="recharge-checkout-summary-hint">{selectedSummaryHint}</div>
                  </div>
                  <span className="recharge-checkout-summary-price tabular-nums">{selectedSummaryPrice}</span>
                </div>

                {selectedPayPalAvailable || selectedStripeAvailable ? (
                  <div
                    className="recharge-checkout-actions"
                    style={{
                      gridTemplateColumns:
                        selectedPayPalAvailable && selectedStripeAvailable ? 'repeat(2, minmax(0, 1fr))' : '1fr',
                    }}
                  >
                    {selectedPayPalAvailable && (
                      <button
                        type="button"
                        className="recharge-pay-btn recharge-pay-btn--paypal"
                        onClick={() => void handlePurchase('paypal')}
                        disabled={isProcessing}
                      >
                        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true" style={{ flexShrink: 0 }}>
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
                        <span className="recharge-btn-label">{isProcessing && activeCheckoutProvider === 'paypal' ? 'Connecting...' : 'PayPal'}</span>
                      </button>
                    )}

                    {selectedStripeAvailable && (
                      <button
                        type="button"
                        className="recharge-pay-btn recharge-pay-btn--card"
                        disabled={isProcessing}
                        onClick={() => void handlePurchase('stripe')}
                      >
                        <CreditCard size={17} strokeWidth={2} aria-hidden="true" style={{ flexShrink: 0 }} />
                        <span className="recharge-btn-label">{isProcessing && activeCheckoutProvider === 'stripe' ? 'Connecting...' : cardLabel}</span>
                        {!isProcessing && (
                          <span className="recharge-card-brands" aria-hidden="true">
                            <span className="recharge-brand-visa">VISA</span>
                            <span className="recharge-brand-mc">
                              <span className="recharge-brand-mc-left" />
                              <span className="recharge-brand-mc-right" />
                            </span>
                          </span>
                        )}
                      </button>
                    )}
                  </div>
                ) : (
                  <p role="status" style={{ textAlign: 'center', fontSize: '0.8125rem', color: 'var(--text-secondary)', margin: 0 }}>
                    No payment channel is currently active for the selected item.
                  </p>
                )}
              </div>
            </section>
          ) : (subscriptionPacks.length > 0 || coinPacks.length > 0) ? (
            <section className="recharge-checkout-dock" aria-label="Package selection prompt">
              <div className="sn-narrow-container recharge-checkout-section">
                <p role="status" style={{ textAlign: 'center', fontSize: '0.8125rem', color: 'var(--text-secondary)', margin: 0 }}>
                  {returnedLookupState === 'loading'
                    ? 'Checking your previous checkout… or select a package above to continue.'
                    : 'Select a package above to continue.'}
                </p>
              </div>
            </section>
          ) : null}

          {/* Payment Processing Loading Overlay */}
          {isProcessing && (
            <div
              aria-busy="true"
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
                {activeCheckoutProvider === 'stripe'
                  ? paymentStep === 1
                    ? 'Redirecting to card checkout...'
                    : 'Completing card payment...'
                  : paymentStep === 1
                    ? 'Redirecting to PayPal Checkout...'
                    : 'Completing PayPal transaction...'}
              </p>
              <p style={{ fontSize: '0.8125rem', color: 'rgba(250, 248, 245, 0.72)' }}>Please keep this window open</p>
            </div>
          )}

          <LegalModal
            isOpen={activeLegalModal !== null}
            type={activeLegalModal || 'contact'}
            onClose={() => setActiveLegalModal(null)}
          />
        </div>
      )}

      {/* Native <dialog> Payment Success Modal (mounted outside offers loading state) */}
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
            handleReturnToContent();
          }}
        >
          Start Reading Now
        </button>
      </dialog>
    </>
  );
};

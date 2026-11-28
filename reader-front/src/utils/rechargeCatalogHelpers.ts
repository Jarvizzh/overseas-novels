export type PaymentProvider = 'paypal' | 'stripe';

export function readingReturnPath(value: unknown): string {
  if (typeof value !== 'string' || !value) return '';
  try {
    const target = new URL(value, window.location.origin);
    return target.origin !== 'null' && target.origin === window.location.origin && target.pathname === '/content'
      ? target.pathname + target.search
      : '';
  } catch {
    return '';
  }
}

export const purchaseRequestStorageKey = (userId: number, slot: number, productId: number, novel: number) =>
  `pending-purchase:${userId}:${slot}:${productId}:${novel}`;

export type CheckoutOrigin = {
  source: 'reader' | 'recharge';
  path: string;
};

export const checkoutOriginStorageKey = (intentId: string) => `checkout-origin:${intentId}`;

export function saveCheckoutOrigin(intentId: string, origin: CheckoutOrigin): void {
  if (!intentId) return;
  try {
    sessionStorage.setItem(checkoutOriginStorageKey(intentId), JSON.stringify(origin));
  } catch {
    // Ignore storage errors in restricted browser contexts.
  }
}

export function readCheckoutOrigin(intentId: string): CheckoutOrigin | null {
  if (!intentId) return null;
  try {
    const raw = sessionStorage.getItem(checkoutOriginStorageKey(intentId));
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (
      parsed &&
      (parsed.source === 'reader' || parsed.source === 'recharge') &&
      typeof parsed.path === 'string'
    ) {
      return { source: parsed.source, path: parsed.path };
    }
  } catch {
    // Ignore parse/storage errors.
  }
  return null;
}

export function clearCheckoutOrigin(intentId: string): void {
  if (!intentId) return;
  try {
    sessionStorage.removeItem(checkoutOriginStorageKey(intentId));
  } catch {
    // Ignore storage errors.
  }
}

export type CheckoutSelectionSnapshot = {
  provider: PaymentProvider;
  slotId: number;
  productId?: number;
  source?: 'reader' | 'recharge';
};

const LAST_CHECKOUT_SELECTION_KEY = 'last-checkout-selection';
const LAST_CHECKOUT_SELECTION_TTL_MS = 30 * 60 * 1000;

export function saveLastCheckoutSelection(snapshot: CheckoutSelectionSnapshot, now = Date.now()): void {
  try {
    sessionStorage.setItem(
      LAST_CHECKOUT_SELECTION_KEY,
      JSON.stringify({ ...snapshot, savedAt: now }),
    );
  } catch {
    // Ignore storage errors.
  }
}

export function readLastCheckoutSelection(
  expectedSource?: 'reader' | 'recharge',
  now = Date.now(),
): CheckoutSelectionSnapshot | null {
  try {
    const raw = sessionStorage.getItem(LAST_CHECKOUT_SELECTION_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (
      typeof parsed?.savedAt === 'number' &&
      Number.isFinite(parsed.savedAt) &&
      now - parsed.savedAt > LAST_CHECKOUT_SELECTION_TTL_MS
    ) {
      sessionStorage.removeItem(LAST_CHECKOUT_SELECTION_KEY);
      return null;
    }
    const parsedSource =
      parsed?.source === 'reader' || parsed?.source === 'recharge' ? parsed.source : undefined;
    if (expectedSource && parsedSource && parsedSource !== expectedSource) {
      return null;
    }
    if (
      parsed &&
      (parsed.provider === 'paypal' || parsed.provider === 'stripe') &&
      typeof parsed.slotId === 'number' &&
      parsed.slotId > 0
    ) {
      const snapshot: CheckoutSelectionSnapshot = {
        provider: parsed.provider,
        slotId: parsed.slotId,
        productId: typeof parsed.productId === 'number' && parsed.productId > 0 ? parsed.productId : undefined,
      };
      if (parsedSource) {
        snapshot.source = parsedSource;
      }
      return snapshot;
    }
  } catch {
    // Ignore parse/storage errors.
  }
  return null;
}

export function clearLastCheckoutSelection(): void {
  try {
    sessionStorage.removeItem(LAST_CHECKOUT_SELECTION_KEY);
  } catch {
    // Ignore storage errors.
  }
}

export type PendingPaymentSuccess = {
  intentId: string;
  coins: number;
  isVip: boolean;
  isWholeBook: boolean;
  vipTitle?: string;
};

const PENDING_PAYMENT_SUCCESS_KEY = 'pending-payment-success';
const PENDING_PAYMENT_SUCCESS_TTL_MS = 10 * 60 * 1000;

export function savePendingPaymentSuccess(payload: PendingPaymentSuccess, now = Date.now()): void {
  try {
    sessionStorage.setItem(
      PENDING_PAYMENT_SUCCESS_KEY,
      JSON.stringify({ ...payload, savedAt: now }),
    );
  } catch {
    // Ignore storage errors.
  }
}

export function readPendingPaymentSuccess(now = Date.now()): PendingPaymentSuccess | null {
  try {
    const raw = sessionStorage.getItem(PENDING_PAYMENT_SUCCESS_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (
      typeof parsed?.savedAt === 'number' &&
      Number.isFinite(parsed.savedAt) &&
      now - parsed.savedAt > PENDING_PAYMENT_SUCCESS_TTL_MS
    ) {
      sessionStorage.removeItem(PENDING_PAYMENT_SUCCESS_KEY);
      return null;
    }
    if (parsed && typeof parsed.intentId === 'string') {
      return {
        intentId: parsed.intentId,
        coins: typeof parsed.coins === 'number' ? parsed.coins : 0,
        isVip: Boolean(parsed.isVip),
        isWholeBook: Boolean(parsed.isWholeBook),
        vipTitle: typeof parsed.vipTitle === 'string' ? parsed.vipTitle : undefined,
      };
    }
  } catch {
    // Ignore parse/storage errors.
  }
  return null;
}

export function clearPendingPaymentSuccess(): void {
  try {
    sessionStorage.removeItem(PENDING_PAYMENT_SUCCESS_KEY);
  } catch {
    // Ignore storage errors.
  }
}

export function consumePendingPaymentSuccess(now = Date.now()): PendingPaymentSuccess | null {
  const payload = readPendingPaymentSuccess(now);
  clearPendingPaymentSuccess();
  return payload;
}

export type RechargeSlotItem = {
  id: number;
  template_id: number;
  slot_index: number;
  product_id?: number;
  product_code?: string;
  product_type?: 'single' | 'whole_book' | 'vip';
  type: 'single' | 'subscription' | 'vip' | 'whole_book';
  name?: string;
  description?: string;
  coins: number;
  bonus: number;
  vip_duration: string;
  subscription_cycle?: string;
  vip_name: string;
  vip_desc: string;
  badge?: string;
  is_highlighted?: boolean;
  display_alias?: string;
  novel_scope_type?: string;
  paypal_ready?: boolean;
  stripe_ready?: boolean;
  price: string;
  price_cents: number;
  list_price?: string;
  list_price_cents?: number;
};

export function formatValidListPrice(
  listPriceCents?: number,
  priceCents?: number,
  listPrice?: string,
): string {
  if (
    typeof listPriceCents !== 'number' ||
    !Number.isFinite(listPriceCents) ||
    typeof priceCents !== 'number' ||
    !Number.isFinite(priceCents) ||
    listPriceCents <= priceCents ||
    listPriceCents <= 0
  ) {
    return '';
  }
  const trimmed = typeof listPrice === 'string' ? listPrice.trim() : '';
  return trimmed || `$${(listPriceCents / 100).toFixed(2)}`;
}

export function sanitizeSubscriptionCopy(text: string): string {
  if (!text) return '';
  return text
    .replace(/\bMembership\s*&\s*Subscriptions?\b/gi, 'Subscription')
    .replace(/\bVIP\s+Subscriptions?\b/gi, 'Subscription')
    .replace(/\bVIP\s+Memberships?\b/gi, 'Subscription')
    .replace(/\bVIP\s+Pass(es)?\b/gi, 'Subscription')
    .replace(/\bMemberships?\b/gi, 'Subscription')
    .replace(/\bvip\b/gi, 'Subscription')
    .replace(/\bSubscription(\s+Subscription)+\b/gi, 'Subscription')
    .replace(/\s{2,}/g, ' ')
    .trim();
}

export function vipCycleLabel(cycle?: string): string {
  switch ((cycle || '').toLowerCase()) {
    case 'day':
      return 'Daily Plan';
    case 'week':
      return 'Weekly Plan';
    case 'year':
      return 'Annual Plan';
    case 'month':
    default:
      return 'Monthly Plan';
  }
}

export function formatSubscriptionEquivalent(priceCents: number, cycle?: string): string {
  if (!Number.isFinite(priceCents) || priceCents <= 0) return '';
  switch ((cycle || '').toLowerCase()) {
    case 'year':
      return `≈ $${(priceCents / 100 / 12).toFixed(2)}/mo`;
    case 'month':
      return `≈ $${(priceCents / 100 / 30).toFixed(2)}/day`;
    case 'week':
      return `≈ $${(priceCents / 100 / 7).toFixed(2)}/day`;
    case 'day':
      return `$${(priceCents / 100).toFixed(2)}/day`;
    default:
      return '';
  }
}

export function calcCoinBonusPercent(coins: number, bonus: number): string {
  if (!Number.isFinite(coins) || !Number.isFinite(bonus) || coins <= 0 || bonus <= 0) return '';
  const pct = Math.round((bonus / coins) * 100);
  return pct > 0 ? `+${pct}%` : '';
}

export function resolveInitialCarouselIndex(
  subscriptionPacks: Array<{ id: number; isHighlighted: boolean }>,
  selectedPack: number | null,
): number {
  if (subscriptionPacks.length === 0) return 0;
  if (selectedPack !== null) {
    const selectedIdx = subscriptionPacks.findIndex((pack) => pack.id === selectedPack);
    if (selectedIdx >= 0) return selectedIdx;
  }
  const highlightedIdx = subscriptionPacks.findIndex((pack) => pack.isHighlighted);
  return highlightedIdx >= 0 ? highlightedIdx : 0;
}

export function buildSubscriptionPacks(slots: RechargeSlotItem[]) {
  return slots
    .filter((s) => s.type === 'subscription' || s.type === 'vip' || s.product_type === 'vip')
    .map((s) => {
      const cycle = s.subscription_cycle || s.vip_duration || 'month';
      return {
        id: s.id,
        productId: s.product_id,
        name: s.display_alias || s.name || s.vip_name || vipCycleLabel(cycle),
        badge: s.badge || (s.is_highlighted ? 'Popular' : ''),
        isHighlighted: Boolean(s.is_highlighted),
        price: s.price,
        priceCents: s.price_cents,
        listPrice: formatValidListPrice(s.list_price_cents, s.price_cents, s.list_price),
        listPriceCents: s.list_price_cents && s.list_price_cents > s.price_cents ? s.list_price_cents : 0,
        desc: s.description || s.vip_desc || 'Unlimited reading across all titles. Auto-renews, cancel anytime.',
        cycle,
        type: s.type,
      };
    });
}

export function buildCoinPacks(slots: RechargeSlotItem[], novelId?: number) {
  return slots
    .filter((s) => s.type === 'single' || (s.type === 'whole_book' && novelId))
    .map((s) => {
      const bonus = s.type === 'single' ? s.bonus : 0;
      return {
        id: s.id,
        productId: s.product_id,
        coins: s.coins,
        bonus,
        price: s.price,
        priceCents: s.price_cents,
        listPrice: formatValidListPrice(s.list_price_cents, s.price_cents, s.list_price),
        listPriceCents: s.list_price_cents && s.list_price_cents > s.price_cents ? s.list_price_cents : 0,
        total: s.coins + bonus,
        name: s.display_alias || s.name || s.vip_name || '',
        badge: s.badge || '',
        isHighlighted: Boolean(s.is_highlighted),
        type: s.type,
      };
    });
}

export function formatCoinPackSubtitle(pack: { name: string; bonus: number; total: number }): string {
  if (pack.name) {
    return pack.bonus > 0 ? `${pack.name} · +${pack.bonus} bonus` : pack.name;
  }
  return `Total: ${pack.total.toLocaleString()} Coins`;
}

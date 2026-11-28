import type {
  SubscriptionCancelResponse,
  SubscriptionInfo,
  SubscriptionNormalizedStatus,
} from './api';

export type SubscriptionStatusTone = 'active' | 'warning' | 'muted' | 'danger';

export interface SubscriptionViewModel {
  subscriptionId: string;
  provider: string;
  providerLabel: string;
  salesModeLabel: string;
  planName: string;
  priceLabel: string;
  normalizedStatus: SubscriptionNormalizedStatus;
  statusTone: SubscriptionStatusTone;
  statusLabel: string;
  statusDescription: string;
  paidThroughRaw: string | null;
  paidThroughLabel: string | null;
  nextBillingRaw: string | null;
  nextBillingLabel: string | null;
  canCancelAutoRenew: boolean;
  cancelPending: boolean;
  canOpenBillingPortal: boolean;
  canOpenMoRLinkPortal: boolean;
  morLinkUrl: string | null;
}

export function isSafeHttpsUrl(raw?: string | null): boolean {
  if (!raw || typeof raw !== 'string') return false;
  try {
    const parsed = new URL(raw);
    return parsed.protocol === 'https:' && Boolean(parsed.hostname);
  } catch {
    return false;
  }
}

export function formatSubscriptionDate(iso?: string | null): string | null {
  if (!iso) return null;
  const ts = Date.parse(iso);
  if (Number.isNaN(ts)) return null;
  return new Date(ts).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
}

export function formatSubscriptionPrice(priceCents: number, currency: string, cycle: string): string {
  const code = (currency || 'USD').toUpperCase();
  const amount = Number.isFinite(priceCents) && priceCents > 0 ? (priceCents / 100).toFixed(2) : '';
  const priceText = amount ? (code === 'USD' ? `$${amount}` : `${code} ${amount}`) : '';
  const cycleText = (cycle || '').trim().toLowerCase();
  if (priceText && cycleText) return `${priceText} / ${cycleText}`;
  return priceText || cycleText || '';
}

export function resolveNormalizedStatus(sub: SubscriptionInfo, nowMs: number = Date.now()): SubscriptionNormalizedStatus {
  if (sub.normalized_status) {
    return sub.normalized_status;
  }
  const status = (sub.status || '').toUpperCase();
  const paidThroughMs = sub.paid_through || sub.current_period_end ? Date.parse(sub.paid_through || sub.current_period_end || '') : NaN;
  const hasPaidPeriod = Number.isFinite(paidThroughMs) && paidThroughMs > nowMs;
  if (status === 'CANCEL_PENDING') return 'cancel_pending';
  if (status === 'CANCELLED' || status === 'EXPIRED') {
    return hasPaidPeriod ? 'canceled_with_paid_period' : 'canceled';
  }
  if (status === 'SUSPENDED') return 'renewal_failed';
  if (status === 'ACTIVE') {
    if (sub.renewal_failed_at) {
      const failMs = Date.parse(sub.renewal_failed_at);
      const lastPayMs = sub.last_payment_time ? Date.parse(sub.last_payment_time) : NaN;
      if (Number.isFinite(failMs) && (!Number.isFinite(lastPayMs) || failMs > lastPayMs)) {
        return 'renewal_failed';
      }
    }
    return 'active';
  }
  if (status === 'PENDING') return 'pending';
  return 'none';
}

export function buildSubscriptionViewModel(
  sub: SubscriptionInfo,
  nowMs: number = Date.now(),
): SubscriptionViewModel {
  const provider = (sub.provider || sub.payment_method || '').toLowerCase();
  const providerLabel =
    provider === 'stripe' ? 'Stripe' : provider === 'paypal' ? 'PayPal' : provider ? provider.toUpperCase() : 'Subscription';

  const salesMode = (sub.sales_mode || '').toLowerCase();
  const salesModeLabel =
    provider === 'stripe'
      ? salesMode === 'direct'
        ? 'Card Billing'
        : salesMode === 'merchant_of_record'
          ? 'Link Order Management'
          : ''
      : '';

  const fallbackPlanName =
    sub.cycle === 'day'
      ? 'Daily Plan'
      : sub.cycle === 'week'
        ? 'Weekly Plan'
        : sub.cycle === 'month'
          ? 'Monthly Plan'
          : sub.cycle === 'year'
            ? 'Annual Plan'
            : 'Unlimited Subscription';
  const planName = sub.plan_name?.trim() || fallbackPlanName;
  const priceLabel = formatSubscriptionPrice(sub.price_cents, sub.currency, sub.cycle);

  const normalizedStatus = resolveNormalizedStatus(sub, nowMs);
  const paidThroughRaw = sub.paid_through || sub.current_period_end || null;
  const paidThroughLabel = formatSubscriptionDate(paidThroughRaw);

  const nextBillingRaw =
    sub.next_billing_at !== undefined
      ? sub.next_billing_at || null
      : normalizedStatus === 'active' || normalizedStatus === 'renewal_failed'
        ? sub.next_billing_time || null
        : null;
  const nextBillingLabel = formatSubscriptionDate(nextBillingRaw);

  let statusTone: SubscriptionStatusTone = 'muted';
  let statusLabel = 'Inactive';
  let statusDescription = 'No active auto-renewal.';

  switch (normalizedStatus) {
    case 'active':
      statusTone = 'active';
      statusLabel = 'Active · Auto-Renew On';
      statusDescription = nextBillingLabel
        ? `Your subscription is active. Next scheduled billing date is ${nextBillingLabel}.`
        : 'Your subscription is active with unlimited reading access.';
      break;
    case 'cancel_pending':
      statusTone = 'warning';
      statusLabel = 'Cancellation Pending';
      statusDescription = paidThroughLabel
        ? `Awaiting confirmation from ${providerLabel}. You keep full reading access through ${paidThroughLabel}.`
        : `Awaiting cancellation confirmation from ${providerLabel}.`;
      break;
    case 'canceled_with_paid_period':
      statusTone = 'muted';
      statusLabel = 'Auto-Renew Stopped';
      statusDescription = paidThroughLabel
        ? `Auto-renewal is off and you will not be charged again. Unlimited access remains active through ${paidThroughLabel}.`
        : 'Auto-renewal is off. Your remaining paid access continues until the end of the current period.';
      break;
    case 'renewal_failed':
      statusTone = 'danger';
      statusLabel = 'Renewal Payment Failed';
      statusDescription =
        'Your recent renewal charge could not be completed. Update your payment method to avoid interruption, or turn off auto-renewal.';
      break;
    case 'pending':
      statusTone = 'warning';
      statusLabel = 'Confirmation Pending';
      statusDescription = `Waiting for initial payment confirmation from ${providerLabel}.`;
      break;
    case 'canceled':
    case 'none':
    default:
      statusTone = 'muted';
      statusLabel = 'Ended';
      statusDescription = 'This subscription has ended and will not renew.';
      break;
  }

  // Strictly trust server-authorized capabilities; never grant unapproved actions client-side.
  const caps = sub.capabilities;
  const cancelPending = Boolean(caps?.cancel_pending || normalizedStatus === 'cancel_pending');
  const canCancelAutoRenew = Boolean(caps?.can_cancel_auto_renew) && !cancelPending;
  const canOpenBillingPortal = Boolean(caps?.can_open_billing_portal);
  const morLinkUrl =
    caps?.can_open_mor_link_portal && isSafeHttpsUrl(caps.mor_link_url) ? caps.mor_link_url! : null;
  const canOpenMoRLinkPortal = Boolean(morLinkUrl);

  return {
    subscriptionId: sub.subscription_id,
    provider,
    providerLabel,
    salesModeLabel,
    planName,
    priceLabel,
    normalizedStatus,
    statusTone,
    statusLabel,
    statusDescription,
    paidThroughRaw,
    paidThroughLabel,
    nextBillingRaw,
    nextBillingLabel,
    canCancelAutoRenew,
    cancelPending,
    canOpenBillingPortal,
    canOpenMoRLinkPortal,
    morLinkUrl,
  };
}

export function applyCancelResultToSubscription(
  sub: SubscriptionInfo,
  result: SubscriptionCancelResponse,
): SubscriptionInfo {
  const nextNormalized: SubscriptionNormalizedStatus = result.status || 'cancel_pending';
  const nextRawStatus =
    nextNormalized === 'canceled_with_paid_period' || nextNormalized === 'canceled'
      ? 'CANCELLED'
      : 'CANCEL_PENDING';
  const paidThrough = result.paid_through || sub.paid_through || sub.current_period_end;

  return {
    ...sub,
    status: nextRawStatus,
    normalized_status: nextNormalized,
    paid_through: paidThrough,
    current_period_end: paidThrough,
    next_billing_at: undefined,
    next_billing_time: undefined,
    capabilities: sub.capabilities
      ? {
          ...sub.capabilities,
          can_cancel_auto_renew: false,
          cancel_pending: nextNormalized === 'cancel_pending',
        }
      : undefined,
  };
}

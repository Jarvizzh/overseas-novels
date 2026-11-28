import type { TopUpDisplayStatus, TopUpHistoryItem } from './api';

export type TopUpStatusTone = 'active' | 'warning' | 'danger' | 'muted';

export interface TopUpStatusMeta {
  label: string;
  tone: TopUpStatusTone;
  description: string;
}

export interface TopUpAmountDisplay {
  primaryAmount: string;
  catalogUsdNote: string | null;
  refundedAmount: string | null;
}

const ZERO_DECIMAL_CURRENCIES = new Set([
  'BIF',
  'CLP',
  'DJF',
  'GNF',
  'JPY',
  'KMF',
  'KRW',
  'MGA',
  'PYG',
  'RWF',
  'UGX',
  'VND',
  'VUV',
  'XAF',
  'XOF',
  'XPF',
]);

const THREE_DECIMAL_CURRENCIES = new Set(['BHD', 'JOD', 'KWD', 'OMR', 'TND']);

export function currencyFractionDigits(currency: string): number {
  const code = (currency || 'USD').trim().toUpperCase();
  if (ZERO_DECIMAL_CURRENCIES.has(code)) return 0;
  if (THREE_DECIMAL_CURRENCIES.has(code)) return 3;
  return 2;
}

export function formatMinorCurrency(amountMinor: number, currency: string): string {
  const code = (currency || 'USD').trim().toUpperCase() || 'USD';
  const digits = currencyFractionDigits(code);
  const safeMinor = Number.isFinite(amountMinor) ? amountMinor : 0;
  const major = safeMinor / 10 ** digits;
  try {
    return new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency: code,
      minimumFractionDigits: digits,
      maximumFractionDigits: digits,
    }).format(major);
  } catch {
    return `${code} ${major.toFixed(digits)}`;
  }
}

export function resolveTopUpAmountDisplay(item: TopUpHistoryItem): TopUpAmountDisplay {
  const catalogCurrency = (item.currency || 'USD').trim().toUpperCase() || 'USD';
  const catalogFormatted = formatMinorCurrency(item.amount_minor, catalogCurrency);

  const hasPresentment =
    typeof item.presentment_amount_minor === 'number' &&
    item.presentment_amount_minor > 0 &&
    Boolean(item.presentment_currency && item.presentment_currency.trim());

  let primaryAmount = catalogFormatted;
  let catalogUsdNote: string | null = null;

  if (hasPresentment) {
    const presentmentCurrency = item.presentment_currency!.trim().toUpperCase();
    const presentmentFormatted = formatMinorCurrency(item.presentment_amount_minor!, presentmentCurrency);
    primaryAmount = presentmentFormatted;
    if (presentmentCurrency !== catalogCurrency || item.presentment_amount_minor !== item.amount_minor) {
      catalogUsdNote = `${catalogFormatted} ${catalogCurrency}`;
    }
  }

  let refundedAmount: string | null = null;
  if (typeof item.refunded_minor === 'number' && item.refunded_minor > 0) {
    const formattedRefund = formatMinorCurrency(item.refunded_minor, catalogCurrency);
    refundedAmount = catalogUsdNote ? `${formattedRefund} ${catalogCurrency}` : formattedRefund;
  }

  return {
    primaryAmount,
    catalogUsdNote,
    refundedAmount,
  };
}

export function getTopUpStatusMeta(status: TopUpDisplayStatus, refundedAmount?: string | null): TopUpStatusMeta {
  switch (status) {
    case 'completed':
      return {
        label: 'Completed',
        tone: 'active',
        description: 'Payment completed and your purchase has been delivered.',
      };
    case 'confirming':
      return {
        label: 'Confirming',
        tone: 'warning',
        description: 'Payment received. Your order is being confirmed and will be delivered shortly.',
      };
    case 'under_review':
      return {
        label: 'Under Review',
        tone: 'warning',
        description:
          'Payment received and currently under verification. Please do not pay again — contact support if this persists.',
      };
    case 'refund_pending':
      return {
        label: 'Refund Processing',
        tone: 'warning',
        description: 'A refund is currently being processed by the payment provider.',
      };
    case 'partially_refunded':
      return {
        label: 'Partially Refunded',
        tone: 'warning',
        description: refundedAmount
          ? `A partial refund of ${refundedAmount} was issued. Your purchase benefits remain active.`
          : 'A partial refund was issued. Your purchase benefits remain active.',
      };
    case 'refunded':
      return {
        label: 'Refunded',
        tone: 'muted',
        description: 'This order has been fully refunded.',
      };
    case 'revoked':
      return {
        label: 'Disputed',
        tone: 'danger',
        description: 'This payment was disputed or its entitlement has been revoked. Contact support for details.',
      };
    default:
      return {
        label: 'Completed',
        tone: 'muted',
        description: 'Order recorded.',
      };
  }
}

export function formatTopUpProvider(provider?: string): string {
  const normalized = (provider || '').trim().toLowerCase();
  if (normalized === 'paypal') return 'PayPal';
  if (normalized === 'stripe') return 'Card';
  return normalized ? normalized.toUpperCase() : 'Payment';
}

export function formatTopUpCycle(cycle?: string): string {
  switch ((cycle || '').trim().toLowerCase()) {
    case 'day':
      return 'Daily';
    case 'week':
      return 'Weekly';
    case 'month':
      return 'Monthly';
    case 'year':
      return 'Annual';
    default:
      return '';
  }
}

export function formatTopUpDateTime(iso?: string | null): string {
  if (!iso) return '—';
  const ts = Date.parse(iso);
  if (Number.isNaN(ts)) return '—';
  return new Date(ts).toLocaleString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function formatTopUpDateRange(startIso?: string | null, endIso?: string | null): string | null {
  if (!startIso || !endIso) return null;
  const startTs = Date.parse(startIso);
  const endTs = Date.parse(endIso);
  if (Number.isNaN(startTs) || Number.isNaN(endTs)) return null;
  const fmt = (ts: number) =>
    new Date(ts).toLocaleDateString('en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    });
  return `${fmt(startTs)} – ${fmt(endTs)}`;
}

export function buildTopUpBenefitSummary(item: TopUpHistoryItem): string {
  if (item.order_type === 'single') {
    const coins = item.coins ?? 0;
    const bonus = item.bonus_coins ?? 0;
    if (coins > 0 && bonus > 0) {
      return `+${coins.toLocaleString()} Coins · +${bonus.toLocaleString()} Bonus`;
    }
    if (coins > 0) {
      return `+${coins.toLocaleString()} Coins`;
    }
    return 'Coin Top-Up';
  }

  if (item.order_type === 'whole_book') {
    return 'Full Book Unlock · Forever access';
  }

  const parts: string[] = [];
  const cycleText = formatTopUpCycle(item.cycle);
  if (cycleText) parts.push(cycleText);
  parts.push(item.billing_kind === 'renewal' ? 'Auto-Renewal' : 'Subscription');
  const range = formatTopUpDateRange(item.period_start, item.period_end);
  if (range) parts.push(range);
  return parts.join(' · ');
}

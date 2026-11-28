export interface CheckoutPendingConflictError {
  status?: number;
  code?: string;
}

export interface PurchaseLookupIntentLike {
  intent_id: string;
  order_id?: string;
  status: string;
  confirmation_code?: string;
  confirmation_message?: string;
  order_type?: string;
  coins?: number;
}

export interface PurchaseLookupDecision {
  outcome: 'settled' | 'refunded' | 'waiting' | 'terminal_failure' | 'closed';
  shouldShowSuccess: boolean;
  shouldClearDraft: boolean;
  shouldRefreshWallet: boolean;
  shouldContinuePolling: boolean;
  nextPollDelayMs: number | null;
}

const TERMINAL_FAILURE_CODES = new Set([
  'payment_declined',
  'payment_blocked',
  'payment_action_required',
]);

export function isCheckoutPendingConflict(err: unknown): boolean {
  if (!err || typeof err !== 'object') return false;
  const candidate = err as CheckoutPendingConflictError;
  return candidate.status === 409 && candidate.code === 'checkout_pending';
}

export function buildCheckoutPendingLocator(intentId: string) {
  return {
    intent: intentId,
    token: '',
    vip: false,
    stripeVIP: false,
    cancelled: false,
  };
}

export interface PurchaseLocator {
  intent: string;
  token: string;
  vip: boolean;
  stripeVIP: boolean;
  cancelled: boolean;
}

export function parsePurchaseLocator(search: string): PurchaseLocator {
  const query = new URLSearchParams(search);
  const cancelled = query.get('payment') === 'cancel';
  const stripeVIP = query.get('stripe_vip');
  if (stripeVIP) {
    return { intent: stripeVIP, token: '', vip: true, stripeVIP: true, cancelled };
  }
  const intentParam = query.get('purchase') || '';
  const vipIntent = query.get('vip_purchase') || '';
  const rawToken = query.get('subscription_id') || query.get('token') || '';
  const token = cancelled && (vipIntent || intentParam) ? '' : rawToken;
  if (vipIntent || query.has('subscription_id') || rawToken.startsWith('I-')) {
    return { intent: vipIntent || intentParam, token, vip: true, stripeVIP: false, cancelled };
  }
  return { intent: intentParam, token, vip: false, stripeVIP: false, cancelled };
}

export function evaluatePurchaseLookupOutcome(
  intent: PurchaseLookupIntentLike,
  opts: {
    cancelled: boolean;
    elapsedMs: number;
    maxWaitMs?: number;
  },
): PurchaseLookupDecision {
  const maxWaitMs = opts.maxWaitMs ?? 75_000;
  const isTerminalFailure =
    TERMINAL_FAILURE_CODES.has(intent.confirmation_code || '') || intent.status === 'review';

  if (intent.status === 'completed') {
    return {
      outcome: 'settled',
      shouldShowSuccess: true,
      shouldClearDraft: true,
      shouldRefreshWallet: true,
      shouldContinuePolling: false,
      nextPollDelayMs: null,
    };
  }

  if (intent.status === 'refunded') {
    return {
      outcome: 'refunded',
      shouldShowSuccess: false,
      shouldClearDraft: true,
      shouldRefreshWallet: false,
      shouldContinuePolling: false,
      nextPollDelayMs: null,
    };
  }

  if (isTerminalFailure) {
    return {
      outcome: 'terminal_failure',
      shouldShowSuccess: false,
      shouldClearDraft: false,
      shouldRefreshWallet: false,
      shouldContinuePolling: false,
      nextPollDelayMs: null,
    };
  }

  const waiting = ['confirming', 'awaiting_approval'].includes(intent.status);
  if (waiting) {
    const canPoll = !opts.cancelled && opts.elapsedMs < maxWaitMs;
    return {
      outcome: 'waiting',
      shouldShowSuccess: false,
      shouldClearDraft: false,
      shouldRefreshWallet: false,
      shouldContinuePolling: canPoll,
      nextPollDelayMs: canPoll ? (opts.elapsedMs < 10_000 ? 1000 : 3000) : null,
    };
  }

  return {
    outcome: 'closed',
    shouldShowSuccess: false,
    shouldClearDraft: false,
    shouldRefreshWallet: false,
    shouldContinuePolling: false,
    nextPollDelayMs: null,
  };
}

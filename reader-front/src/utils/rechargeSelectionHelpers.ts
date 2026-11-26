import type { PurchaseIntent } from './api';
import type { RechargeSlotItem } from './rechargeCatalogHelpers';

const PENDING_PURCHASE_STATUSES = new Set(['ready', 'retryable', 'awaiting_approval', 'confirming']);

export function isPendingPurchaseStatus(status?: string): boolean {
  return Boolean(status && PENDING_PURCHASE_STATUSES.has(status));
}

export function isSubscriptionSlotItem(slot: RechargeSlotItem): boolean {
  return slot.type === 'subscription' || slot.type === 'vip' || slot.product_type === 'vip';
}

export function findMatchingSlotForIntent(
  visibleSlots: RechargeSlotItem[],
  intent: Pick<PurchaseIntent, 'slot_id' | 'product_id'>,
): RechargeSlotItem | undefined {
  if (!intent.slot_id || intent.slot_id <= 0) return undefined;
  return visibleSlots.find(
    (slot) =>
      slot.id === intent.slot_id &&
      (!intent.product_id || !slot.product_id || slot.product_id === intent.product_id),
  );
}

export type ReturnedLookupState = 'none' | 'loading' | 'resolved' | 'unresolved';

export interface SelectionResolutionInput {
  visibleSlots: RechargeSlotItem[];
  currentSelected: number | null;
  userManuallySelected: boolean;
  returnedLookupState: ReturnedLookupState;
  returnedPendingIntent: Pick<PurchaseIntent, 'intent_id' | 'slot_id' | 'product_id' | 'status'> | null;
}

export interface SelectionResolutionResult {
  selectedSlotId: number | null;
  staleReturnedIntent: boolean;
}

/**
 * Resolves which recharge slot should be selected across asynchronous template
 * loading and returned-purchase lookup without overwriting a manual choice or
 * silently substituting a default slot while the returned purchase is unknown
 * or when a returned unpaid offer disappeared.
 */
export function resolveRechargeSelection({
  visibleSlots,
  currentSelected,
  userManuallySelected,
  returnedLookupState,
  returnedPendingIntent,
}: SelectionResolutionInput): SelectionResolutionResult {
  if (visibleSlots.length === 0) {
    return { selectedSlotId: null, staleReturnedIntent: false };
  }

  const currentStillVisible = currentSelected !== null && visibleSlots.some((slot) => slot.id === currentSelected);

  if (userManuallySelected) {
    return {
      selectedSlotId: currentStillVisible ? currentSelected : null,
      staleReturnedIntent: false,
    };
  }

  if (returnedPendingIntent && isPendingPurchaseStatus(returnedPendingIntent.status) && returnedPendingIntent.slot_id > 0) {
    const matched = findMatchingSlotForIntent(visibleSlots, returnedPendingIntent);
    if (matched) {
      return { selectedSlotId: matched.id, staleReturnedIntent: false };
    }
    return { selectedSlotId: null, staleReturnedIntent: true };
  }

  if (returnedLookupState === 'loading' || returnedLookupState === 'unresolved') {
    return { selectedSlotId: null, staleReturnedIntent: false };
  }

  if (currentStillVisible) {
    return { selectedSlotId: currentSelected, staleReturnedIntent: false };
  }

  const highlightedSlot = visibleSlots.find((s) => s.is_highlighted);
  const subSlot = visibleSlots.find(isSubscriptionSlotItem);
  return {
    selectedSlotId: (highlightedSlot || subSlot || visibleSlots[0]).id,
    staleReturnedIntent: false,
  };
}

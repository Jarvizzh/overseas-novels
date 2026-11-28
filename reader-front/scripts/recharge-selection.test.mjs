import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';

async function loadSelectionHelpers() {
  const source = await readFile(new URL('../src/utils/rechargeSelectionHelpers.ts', import.meta.url), 'utf8');
  const transpiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ESNext,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(transpiled).toString('base64')}`);
}

const sampleSlots = [
  { id: 26, product_id: 5, type: 'subscription', product_type: 'vip', name: 'VIP Daily Pass', is_highlighted: false },
  { id: 27, product_id: 6, type: 'subscription', product_type: 'vip', name: 'VIP Weekly Pass', is_highlighted: true },
  { id: 30, product_id: 10, type: 'single', product_type: 'single', name: '499 Coins', is_highlighted: false },
];

test('#21 templates return before purchase lookup: restores slot 26 over highlighted slot 27', async () => {
  const { resolveRechargeSelection } = await loadSelectionHelpers();

  // Step 1: templates arrived while initial purchase lookup is still in flight.
  const waiting = resolveRechargeSelection({
    visibleSlots: sampleSlots,
    currentSelected: null,
    userManuallySelected: false,
    returnedLookupState: 'loading',
    returnedPendingIntent: null,
  });
  assert.deepEqual(waiting, { selectedSlotId: null, staleReturnedIntent: false });

  // Step 2: lookup resolves with unpaid Day VIP (slot 26, product 5).
  const restored = resolveRechargeSelection({
    visibleSlots: sampleSlots,
    currentSelected: waiting.selectedSlotId,
    userManuallySelected: false,
    returnedLookupState: 'resolved',
    returnedPendingIntent: { intent_id: 'day-1', slot_id: 26, product_id: 5, status: 'awaiting_approval' },
  });
  assert.deepEqual(restored, { selectedSlotId: 26, staleReturnedIntent: false });
});

test('#21 purchase lookup returns before templates: restores slot 26 once templates arrive', async () => {
  const { resolveRechargeSelection } = await loadSelectionHelpers();

  const restored = resolveRechargeSelection({
    visibleSlots: sampleSlots,
    currentSelected: null,
    userManuallySelected: false,
    returnedLookupState: 'resolved',
    returnedPendingIntent: { intent_id: 'day-1', slot_id: 26, product_id: 5, status: 'confirming' },
  });
  assert.deepEqual(restored, { selectedSlotId: 26, staleReturnedIntent: false });
});

test('#21 late lookup response never overwrites manual user selection', async () => {
  const { resolveRechargeSelection } = await loadSelectionHelpers();

  // User manually clicked Weekly Pass (slot 27); a subsequent poll returns slot 26 again.
  const kept = resolveRechargeSelection({
    visibleSlots: sampleSlots,
    currentSelected: 27,
    userManuallySelected: true,
    returnedLookupState: 'resolved',
    returnedPendingIntent: { intent_id: 'day-1', slot_id: 26, product_id: 5, status: 'awaiting_approval' },
  });
  assert.deepEqual(kept, { selectedSlotId: 27, staleReturnedIntent: false });
});

test('#21 removed slot or replaced product does not silently select default highlighted slot', async () => {
  const { resolveRechargeSelection } = await loadSelectionHelpers();

  // Case A: slot 26 removed from template.
  const removed = resolveRechargeSelection({
    visibleSlots: sampleSlots.filter((s) => s.id !== 26),
    currentSelected: null,
    userManuallySelected: false,
    returnedLookupState: 'resolved',
    returnedPendingIntent: { intent_id: 'day-1', slot_id: 26, product_id: 5, status: 'awaiting_approval' },
  });
  assert.deepEqual(removed, { selectedSlotId: null, staleReturnedIntent: true });

  // Case B: slot 26 still exists, but bound product changed from 5 to 99.
  const replaced = resolveRechargeSelection({
    visibleSlots: sampleSlots.map((s) => (s.id === 26 ? { ...s, product_id: 99 } : s)),
    currentSelected: null,
    userManuallySelected: false,
    returnedLookupState: 'resolved',
    returnedPendingIntent: { intent_id: 'day-1', slot_id: 26, product_id: 5, status: 'retryable' },
  });
  assert.deepEqual(replaced, { selectedSlotId: null, staleReturnedIntent: true });
});

test('#21 completed or closed returned intent falls back to default highlighted slot', async () => {
  const { resolveRechargeSelection } = await loadSelectionHelpers();

  const fallback = resolveRechargeSelection({
    visibleSlots: sampleSlots,
    currentSelected: null,
    userManuallySelected: false,
    returnedLookupState: 'resolved',
    returnedPendingIntent: null,
  });
  assert.deepEqual(fallback, { selectedSlotId: 27, staleReturnedIntent: false });

  // Default selection prefers subscription over a highlighted coin pack when no returned intent is pending.
  const coinHighlightedSlots = [
    { id: 26, product_id: 5, type: 'subscription', product_type: 'vip', name: 'VIP Daily Pass', is_highlighted: false },
    { id: 30, product_id: 10, type: 'single', product_type: 'single', name: '999 Coins', is_highlighted: true },
  ];
  const subDefault = resolveRechargeSelection({
    visibleSlots: coinHighlightedSlots,
    currentSelected: null,
    userManuallySelected: false,
    returnedLookupState: 'none',
    returnedPendingIntent: null,
  });
  assert.deepEqual(subDefault, { selectedSlotId: 26, staleReturnedIntent: false });
});

test('#21 initial lookup 503 failure keeps unselected while retrying, then restores slot 26', async () => {
  const { resolveRechargeSelection } = await loadSelectionHelpers();

  // First lookup failed with 503 and scheduled a retry: state stays 'loading', must NOT fall back to slot 27.
  const retrying = resolveRechargeSelection({
    visibleSlots: sampleSlots,
    currentSelected: null,
    userManuallySelected: false,
    returnedLookupState: 'loading',
    returnedPendingIntent: null,
  });
  assert.deepEqual(retrying, { selectedSlotId: null, staleReturnedIntent: false });

  // Retry succeeds with original Day VIP (slot 26, product 5).
  const restored = resolveRechargeSelection({
    visibleSlots: sampleSlots,
    currentSelected: retrying.selectedSlotId,
    userManuallySelected: false,
    returnedLookupState: 'resolved',
    returnedPendingIntent: { intent_id: 'day-1', slot_id: 26, product_id: 5, status: 'awaiting_approval' },
  });
  assert.deepEqual(restored, { selectedSlotId: 26, staleReturnedIntent: false });
});

test('#21 unresolved lookup without auto-retry stays unselected until user manually selects a slot', async () => {
  const { resolveRechargeSelection } = await loadSelectionHelpers();

  // E.g. payment=cancel return where first lookup returned 503 and does not auto-retry.
  const unresolved = resolveRechargeSelection({
    visibleSlots: sampleSlots,
    currentSelected: null,
    userManuallySelected: false,
    returnedLookupState: 'unresolved',
    returnedPendingIntent: null,
  });
  assert.deepEqual(unresolved, { selectedSlotId: null, staleReturnedIntent: false });

  // User explicitly clicks Weekly Pass (slot 27).
  const manual = resolveRechargeSelection({
    visibleSlots: sampleSlots,
    currentSelected: 27,
    userManuallySelected: true,
    returnedLookupState: 'unresolved',
    returnedPendingIntent: null,
  });
  assert.deepEqual(manual, { selectedSlotId: 27, staleReturnedIntent: false });
});

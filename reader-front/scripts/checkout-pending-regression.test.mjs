import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';

async function loadCheckoutPendingHelpers() {
  const source = await readFile(new URL('../src/utils/checkoutPendingHelpers.ts', import.meta.url), 'utf8');
  const transpiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ESNext,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(transpiled).toString('base64')}`);
}

test('P2-4 entering without callback params -> 409 checkout_pending -> queries original intent -> shows settled/credited', async () => {
  const {
    isCheckoutPendingConflict,
    buildCheckoutPendingLocator,
    evaluatePurchaseLookupOutcome,
  } = await loadCheckoutPendingHelpers();

  // 1. User enters /recharge without callback params and triggers checkout -> receives 409 checkout_pending.
  const conflictError = { status: 409, code: 'checkout_pending', message: 'Payment confirmation is already in progress.' };
  assert.equal(isCheckoutPendingConflict(conflictError), true);
  assert.equal(isCheckoutPendingConflict({ status: 409, code: 'subscription_conflict' }), false);
  assert.equal(isCheckoutPendingConflict({ status: 500, code: 'checkout_pending' }), false);

  // 2. Recovery switches purchase locator to the original intent ID.
  const locator = buildCheckoutPendingLocator('intent-409-orig');
  assert.deepEqual(locator, {
    intent: 'intent-409-orig',
    token: '',
    vip: false,
    stripeVIP: false,
    cancelled: false,
  });

  // 3. Querying original intent returns completed -> shows settled/credited, clears draft, refreshes wallet.
  const settled = evaluatePurchaseLookupOutcome(
    {
      intent_id: 'intent-409-orig',
      status: 'completed',
      order_type: 'single',
      coins: 500,
    },
    { cancelled: false, elapsedMs: 1200 },
  );
  assert.deepEqual(settled, {
    outcome: 'settled',
    shouldShowSuccess: true,
    shouldClearDraft: true,
    shouldRefreshWallet: true,
    shouldContinuePolling: false,
    nextPollDelayMs: null,
  });
});

test('P2-4 409 checkout_pending -> original intent still confirming -> continues waiting then settles', async () => {
  const { evaluatePurchaseLookupOutcome } = await loadCheckoutPendingHelpers();

  // Early poll (<10s) continues waiting with 1000ms backoff.
  const waitingEarly = evaluatePurchaseLookupOutcome(
    { intent_id: 'intent-409-wait', status: 'confirming' },
    { cancelled: false, elapsedMs: 2500 },
  );
  assert.deepEqual(waitingEarly, {
    outcome: 'waiting',
    shouldShowSuccess: false,
    shouldClearDraft: false,
    shouldRefreshWallet: false,
    shouldContinuePolling: true,
    nextPollDelayMs: 1000,
  });

  // Later poll (>=10s) continues waiting with 3000ms backoff.
  const waitingLater = evaluatePurchaseLookupOutcome(
    { intent_id: 'intent-409-wait', status: 'awaiting_approval' },
    { cancelled: false, elapsedMs: 15_000 },
  );
  assert.equal(waitingLater.outcome, 'waiting');
  assert.equal(waitingLater.shouldContinuePolling, true);
  assert.equal(waitingLater.nextPollDelayMs, 3000);

  // Poll past maxWaitMs stops auto-polling while remaining waiting.
  const timedOut = evaluatePurchaseLookupOutcome(
    { intent_id: 'intent-409-wait', status: 'confirming' },
    { cancelled: false, elapsedMs: 76_000 },
  );
  assert.equal(timedOut.outcome, 'waiting');
  assert.equal(timedOut.shouldContinuePolling, false);
  assert.equal(timedOut.nextPollDelayMs, null);

  // Subsequent poll settles VIP subscription.
  const settledVip = evaluatePurchaseLookupOutcome(
    { intent_id: 'intent-409-wait', status: 'completed', order_type: 'subscription', coins: 0 },
    { cancelled: false, elapsedMs: 18_000 },
  );
  assert.equal(settledVip.outcome, 'settled');
  assert.equal(settledVip.shouldShowSuccess, true);
  assert.equal(settledVip.shouldRefreshWallet, true);
});

test('P2-4 terminal payment failure or review stops polling without showing success', async () => {
  const { evaluatePurchaseLookupOutcome } = await loadCheckoutPendingHelpers();

  const declined = evaluatePurchaseLookupOutcome(
    {
      intent_id: 'intent-409-fail',
      status: 'confirming',
      confirmation_code: 'payment_declined',
    },
    { cancelled: false, elapsedMs: 1500 },
  );
  assert.equal(declined.outcome, 'terminal_failure');
  assert.equal(declined.shouldShowSuccess, false);
  assert.equal(declined.shouldContinuePolling, false);

  const review = evaluatePurchaseLookupOutcome(
    { intent_id: 'intent-409-review', status: 'review' },
    { cancelled: false, elapsedMs: 1500 },
  );
  assert.equal(review.outcome, 'terminal_failure');
  assert.equal(review.shouldContinuePolling, false);
});

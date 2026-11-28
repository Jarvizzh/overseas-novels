import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';

async function loadTopUpHistoryHelpers() {
  const source = await readFile(new URL('../src/utils/topUpHistoryHelpers.ts', import.meta.url), 'utf8');
  const transpiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ESNext,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(transpiled).toString('base64')}`);
}

test('resolveTopUpAmountDisplay prefers presentment currency and keeps USD catalog note', async () => {
  const { resolveTopUpAmountDisplay } = await loadTopUpHistoryHelpers();

  const eurOrder = resolveTopUpAmountDisplay({
    order_id: 1001,
    order_no: 'SN-1001',
    order_type: 'single',
    title: '500 Coins Pack',
    display_status: 'completed',
    amount_minor: 999,
    currency: 'USD',
    presentment_amount_minor: 920,
    presentment_currency: 'EUR',
    provider: 'stripe',
    created_at: '2026-11-28T10:00:00Z',
  });
  assert.equal(eurOrder.primaryAmount, '€9.20');
  assert.equal(eurOrder.catalogUsdNote, '$9.99 USD');
  assert.equal(eurOrder.refundedAmount, null);

  const jpyOrder = resolveTopUpAmountDisplay({
    order_id: 1002,
    order_no: 'SN-1002',
    order_type: 'single',
    title: '1000 Coins Pack',
    display_status: 'completed',
    amount_minor: 1999,
    currency: 'USD',
    presentment_amount_minor: 3000,
    presentment_currency: 'JPY',
    provider: 'stripe',
    created_at: '2026-11-28T10:00:00Z',
  });
  assert.equal(jpyOrder.primaryAmount, '¥3,000');
  assert.equal(jpyOrder.catalogUsdNote, '$19.99 USD');

  const usdOrder = resolveTopUpAmountDisplay({
    order_id: 1003,
    order_no: 'SN-1003',
    order_type: 'whole_book',
    title: 'Starlight Heir',
    display_status: 'partially_refunded',
    amount_minor: 1499,
    currency: 'USD',
    refunded_minor: 500,
    provider: 'paypal',
    created_at: '2026-11-28T10:00:00Z',
  });
  assert.equal(usdOrder.primaryAmount, '$14.99');
  assert.equal(usdOrder.catalogUsdNote, null);
  assert.equal(usdOrder.refundedAmount, '$5.00');

  const eurPartialRefund = resolveTopUpAmountDisplay({
    order_id: 1004,
    order_no: 'SN-1004',
    order_type: 'single',
    title: '500 Coins Pack',
    display_status: 'partially_refunded',
    amount_minor: 999,
    currency: 'USD',
    presentment_amount_minor: 920,
    presentment_currency: 'EUR',
    refunded_minor: 500,
    provider: 'stripe',
    created_at: '2026-11-28T10:00:00Z',
  });
  assert.equal(eurPartialRefund.primaryAmount, '€9.20');
  assert.equal(eurPartialRefund.catalogUsdNote, '$9.99 USD');
  assert.equal(eurPartialRefund.refundedAmount, '$5.00 USD');
});

test('getTopUpStatusMeta maps all 7 normalized statuses cleanly', async () => {
  const { getTopUpStatusMeta } = await loadTopUpHistoryHelpers();

  assert.equal(getTopUpStatusMeta('completed').tone, 'active');
  assert.equal(getTopUpStatusMeta('confirming').tone, 'warning');
  assert.equal(getTopUpStatusMeta('under_review').tone, 'warning');
  assert.equal(getTopUpStatusMeta('refund_pending').tone, 'warning');
  assert.equal(getTopUpStatusMeta('refunded').tone, 'muted');
  assert.equal(getTopUpStatusMeta('revoked').tone, 'danger');

  const partial = getTopUpStatusMeta('partially_refunded', '$5.00');
  assert.equal(partial.label, 'Partially Refunded');
  assert.match(partial.description, /\$5\.00/);
  assert.match(partial.description, /remain active/i);
});

test('buildTopUpBenefitSummary and formatTopUpProvider format all order types', async () => {
  const { buildTopUpBenefitSummary, formatTopUpProvider } = await loadTopUpHistoryHelpers();

  assert.equal(formatTopUpProvider('paypal'), 'PayPal');
  assert.equal(formatTopUpProvider('stripe'), 'Card');

  assert.equal(
    buildTopUpBenefitSummary({
      order_id: 1,
      order_no: 'SN-1',
      order_type: 'single',
      title: '500 Coins',
      display_status: 'completed',
      amount_minor: 999,
      currency: 'USD',
      coins: 500,
      bonus_coins: 100,
      provider: 'stripe',
      created_at: '2026-11-28T10:00:00Z',
    }),
    '+500 Coins · +100 Bonus',
  );

  assert.equal(
    buildTopUpBenefitSummary({
      order_id: 2,
      order_no: 'SN-2',
      order_type: 'whole_book',
      title: 'Starlight Heir',
      display_status: 'completed',
      amount_minor: 1499,
      currency: 'USD',
      provider: 'paypal',
      created_at: '2026-11-28T10:00:00Z',
    }),
    'Full Book Unlock · Forever access',
  );

  const subSummary = buildTopUpBenefitSummary({
    order_id: 3,
    order_no: 'SN-3',
    order_type: 'subscription',
    title: 'Weekly Unlimited',
    display_status: 'completed',
    amount_minor: 999,
    currency: 'USD',
    cycle: 'week',
    billing_kind: 'renewal',
    period_start: '2026-11-21T08:00:00Z',
    period_end: '2026-11-28T08:00:00Z',
    provider: 'paypal',
    created_at: '2026-11-21T08:00:05Z',
  });
  assert.match(subSummary, /Weekly · Auto-Renewal/);
  assert.match(subSummary, /2026/);
});

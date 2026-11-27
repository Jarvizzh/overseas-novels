import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';

async function loadSubscriptionHelpers() {
  const source = await readFile(new URL('../src/utils/subscriptionHelpers.ts', import.meta.url), 'utf8');
  const transpiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ESNext,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(transpiled).toString('base64')}`);
}

const FUTURE_ISO = '2026-12-15T00:00:00Z';
const NOW_MS = Date.parse('2026-11-27T00:00:00Z');

test('P1-4 PayPal active subscription exposes cancel only, no Stripe portals', async () => {
  const { buildSubscriptionViewModel } = await loadSubscriptionHelpers();
  const vm = buildSubscriptionViewModel(
    {
      id: 1,
      user_id: 10,
      subscription_id: 'I-PP-001',
      plan_id: 'P-1',
      plan_name: 'VIP Monthly Pass',
      slot_id: 1,
      template_id: 1,
      status: 'ACTIVE',
      normalized_status: 'active',
      cycle: 'month',
      price_cents: 1999,
      currency: 'USD',
      payment_method: 'paypal',
      provider: 'paypal',
      current_period_end: FUTURE_ISO,
      paid_through: FUTURE_ISO,
      next_billing_time: FUTURE_ISO,
      next_billing_at: FUTURE_ISO,
      capabilities: {
        can_cancel_auto_renew: true,
        cancel_pending: false,
        can_open_billing_portal: false,
        can_open_mor_link_portal: false,
      },
    },
    NOW_MS,
  );

  assert.equal(vm.providerLabel, 'PayPal');
  assert.equal(vm.normalizedStatus, 'active');
  assert.equal(vm.statusTone, 'active');
  assert.equal(vm.canCancelAutoRenew, true);
  assert.equal(vm.cancelPending, false);
  assert.equal(vm.canOpenBillingPortal, false);
  assert.equal(vm.canOpenMoRLinkPortal, false);
  assert.ok(vm.paidThroughLabel);
  assert.ok(vm.nextBillingLabel);
});

test('P1-4 Stripe Direct active authorizes Billing Portal only and hides MoR Link', async () => {
  const { buildSubscriptionViewModel } = await loadSubscriptionHelpers();
  const vm = buildSubscriptionViewModel(
    {
      id: 2,
      user_id: 10,
      subscription_id: 'sub_direct_1',
      plan_id: 'price_1',
      plan_name: 'VIP Direct Monthly',
      slot_id: 2,
      template_id: 1,
      status: 'ACTIVE',
      normalized_status: 'active',
      cycle: 'month',
      price_cents: 1999,
      currency: 'USD',
      payment_method: 'stripe',
      provider: 'stripe',
      sales_mode: 'direct',
      current_period_end: FUTURE_ISO,
      paid_through: FUTURE_ISO,
      next_billing_at: FUTURE_ISO,
      capabilities: {
        can_cancel_auto_renew: true,
        cancel_pending: false,
        can_open_billing_portal: true,
        billing_portal_endpoint: '/wallet/subscription/stripe/portal',
        can_open_mor_link_portal: false,
      },
    },
    NOW_MS,
  );

  assert.equal(vm.providerLabel, 'Stripe');
  assert.equal(vm.salesModeLabel, 'Card Billing');
  assert.equal(vm.canCancelAutoRenew, true);
  assert.equal(vm.canOpenBillingPortal, true);
  assert.equal(vm.canOpenMoRLinkPortal, false);
  assert.equal(vm.morLinkUrl, null);
});

test('P1-4 Stripe MoR active authorizes Link URL only and rejects unsafe URLs', async () => {
  const { buildSubscriptionViewModel } = await loadSubscriptionHelpers();
  const validVm = buildSubscriptionViewModel(
    {
      id: 3,
      user_id: 10,
      subscription_id: 'sub_mor_1',
      plan_id: 'price_2',
      slot_id: 3,
      template_id: 1,
      status: 'ACTIVE',
      normalized_status: 'active',
      cycle: 'week',
      price_cents: 999,
      currency: 'USD',
      payment_method: 'stripe',
      provider: 'stripe',
      sales_mode: 'merchant_of_record',
      current_period_end: FUTURE_ISO,
      paid_through: FUTURE_ISO,
      next_billing_at: FUTURE_ISO,
      capabilities: {
        can_cancel_auto_renew: true,
        cancel_pending: false,
        can_open_billing_portal: false,
        can_open_mor_link_portal: true,
        mor_link_url: 'https://app.link.com/login',
      },
    },
    NOW_MS,
  );

  assert.equal(validVm.canOpenBillingPortal, false);
  assert.equal(validVm.canOpenMoRLinkPortal, true);
  assert.equal(validVm.morLinkUrl, 'https://app.link.com/login');

  const unsafeVm = buildSubscriptionViewModel(
    {
      id: 4,
      user_id: 10,
      subscription_id: 'sub_mor_bad',
      plan_id: 'price_2',
      slot_id: 3,
      template_id: 1,
      status: 'ACTIVE',
      normalized_status: 'active',
      cycle: 'week',
      price_cents: 999,
      currency: 'USD',
      payment_method: 'stripe',
      provider: 'stripe',
      sales_mode: 'merchant_of_record',
      capabilities: {
        can_cancel_auto_renew: true,
        cancel_pending: false,
        can_open_billing_portal: false,
        can_open_mor_link_portal: true,
        mor_link_url: 'javascript:alert(1)',
      },
    },
    NOW_MS,
  );

  assert.equal(unsafeVm.canOpenMoRLinkPortal, false);
  assert.equal(unsafeVm.morLinkUrl, null);
});

test('P1-4 cancel_pending vs canceled_with_paid_period vs renewal_failed states and idempotent cancel update', async () => {
  const { buildSubscriptionViewModel, applyCancelResultToSubscription } = await loadSubscriptionHelpers();

  const activeSub = {
    id: 5,
    user_id: 10,
    subscription_id: 'sub_direct_2',
    plan_id: 'price_1',
    slot_id: 2,
    template_id: 1,
    status: 'ACTIVE',
    normalized_status: 'active',
    cycle: 'month',
    price_cents: 1999,
    currency: 'USD',
    payment_method: 'stripe',
    provider: 'stripe',
    sales_mode: 'direct',
    current_period_end: FUTURE_ISO,
    paid_through: FUTURE_ISO,
    next_billing_at: FUTURE_ISO,
    capabilities: {
      can_cancel_auto_renew: true,
      cancel_pending: false,
      can_open_billing_portal: true,
      can_open_mor_link_portal: false,
    },
  };

  // After cancel API returns cancel_pending, button disables, paid_through is preserved, next billing cleared.
  const pendingSub = applyCancelResultToSubscription(activeSub, {
    subscription_id: 'sub_direct_2',
    status: 'cancel_pending',
    already_requested: false,
    paid_through: FUTURE_ISO,
    message: 'Cancellation requested.',
  });
  const pendingVm = buildSubscriptionViewModel(pendingSub, NOW_MS);
  assert.equal(pendingVm.normalizedStatus, 'cancel_pending');
  assert.equal(pendingVm.statusLabel, 'Cancellation Pending');
  assert.equal(pendingVm.canCancelAutoRenew, false);
  assert.equal(pendingVm.cancelPending, true);
  assert.equal(pendingVm.paidThroughRaw, FUTURE_ISO);
  assert.equal(pendingVm.nextBillingRaw, null);

  // After channel confirms cancellation with remaining paid period, status distinguishes stopped from pending.
  const stoppedSub = applyCancelResultToSubscription(pendingSub, {
    subscription_id: 'sub_direct_2',
    status: 'canceled_with_paid_period',
    already_requested: true,
    paid_through: FUTURE_ISO,
    message: 'Auto-renewal is already stopped.',
  });
  const stoppedVm = buildSubscriptionViewModel(stoppedSub, NOW_MS);
  assert.equal(stoppedVm.normalizedStatus, 'canceled_with_paid_period');
  assert.equal(stoppedVm.statusLabel, 'Auto-Renew Stopped');
  assert.equal(stoppedVm.canCancelAutoRenew, false);
  assert.equal(stoppedVm.cancelPending, false);
  assert.equal(stoppedVm.paidThroughRaw, FUTURE_ISO);

  // Renewal failed state shows danger tone while allowing cancel/card update.
  const failedVm = buildSubscriptionViewModel(
    {
      ...activeSub,
      status: 'SUSPENDED',
      normalized_status: 'renewal_failed',
    },
    NOW_MS,
  );
  assert.equal(failedVm.normalizedStatus, 'renewal_failed');
  assert.equal(failedVm.statusTone, 'danger');
  assert.equal(failedVm.canCancelAutoRenew, true);
  assert.equal(failedVm.canOpenBillingPortal, true);
});

import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import ts from 'typescript';

async function loadTS(relPath, extraReplace = (s) => s) {
  let source = await readFile(new URL(relPath, import.meta.url), 'utf8');
  source = extraReplace(source);
  const output = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(output).toString('base64')}`);
}

const rechargeHelpers = await loadTS('../src/utils/rechargeCatalogHelpers.ts');

test('TC-FE-04: Recharge renders products.name for single and whole_book when display_alias is empty', () => {
  const slots = [
    {
      id: 101,
      template_id: 1,
      slot_index: 1,
      product_id: 11,
      product_type: 'single',
      type: 'single',
      name: '2026 Starter Pack',
      description: 'Best coin entry',
      coins: 1000,
      bonus: 150,
      vip_duration: '',
      vip_name: '',
      vip_desc: '',
      display_alias: '',
      price: '$9.99',
      price_cents: 999,
    },
    {
      id: 102,
      template_id: 1,
      slot_index: 2,
      product_id: 12,
      product_type: 'whole_book',
      type: 'whole_book',
      name: 'Full Novel Access',
      description: 'Unlock forever',
      coins: 0,
      bonus: 0,
      vip_duration: '',
      vip_name: '',
      vip_desc: '',
      display_alias: '',
      price: '$14.99',
      price_cents: 1499,
    },
    {
      id: 103,
      template_id: 1,
      slot_index: 3,
      product_id: 13,
      product_type: 'vip',
      type: 'subscription',
      name: 'VIP Gold Pass',
      description: 'Member exclusive',
      coins: 0,
      bonus: 0,
      vip_duration: 'month',
      subscription_cycle: 'month',
      vip_name: '',
      vip_desc: '',
      display_alias: '',
      price: '$19.99',
      price_cents: 1999,
    },
  ];

  const coinAndBookPacks = rechargeHelpers.buildCoinPacks(slots, 55);
  assert.equal(coinAndBookPacks.length, 2);
  assert.equal(coinAndBookPacks[0].name, '2026 Starter Pack');
  assert.equal(rechargeHelpers.formatCoinPackSubtitle(coinAndBookPacks[0]), '2026 Starter Pack · +150 bonus');
  assert.equal(coinAndBookPacks[1].name, 'Full Novel Access');

  const vipPacks = rechargeHelpers.buildSubscriptionPacks(slots);
  assert.equal(vipPacks.length, 1);
  assert.equal(vipPacks[0].name, 'VIP Gold Pass');
  assert.equal(vipPacks[0].desc, 'Member exclusive');
});

test('TC-FE-05 (Reader): App.tsx ignores ?template_id and api.ts never sends X-Recharge-Template-ID', async () => {
  const storageMap = new Map([['recharge_template_id', '999']]);
  globalThis.document = { cookie: '' };
  globalThis.window = {
    location: { href: 'https://reader.example.invalid/recharge?template_id=999' },
    localStorage: {
      getItem: (k) => storageMap.get(k) ?? null,
      setItem: (k, v) => storageMap.set(k, String(v)),
      removeItem: (k) => storageMap.delete(k),
    },
  };
  globalThis.localStorage = globalThis.window.localStorage;

  const readerStorageSrc = await readFile(new URL('../src/utils/readerStorage.ts', import.meta.url), 'utf8');
  const { api } = await loadTS('../src/utils/api.ts', (src) =>
    src
      .replace("import { readerStorage } from './readerStorage';", readerStorageSrc)
      .replace('import.meta.env.VITE_API_BASE_URL', "''"),
  );

  const originalFetch = globalThis.fetch;
  let capturedHeaders = null;
  globalThis.fetch = async (_url, init) => {
    capturedHeaders = new Headers(init.headers);
    return Response.json({ id: 1, name: 'Default', is_default: true, slots: [] });
  };
  try {
    await api.getRechargeTemplates(55);
    assert.ok(capturedHeaders);
    assert.equal(capturedHeaders.has('X-Recharge-Template-ID'), false);
  } finally {
    globalThis.fetch = originalFetch;
  }

  // Verify App.tsx neither tracks `template_id` in TRACKING_KEYS nor stores `recharge_template_id`.
  const appSource = await readFile(new URL('../src/App.tsx', import.meta.url), 'utf8');
  assert.equal(appSource.includes('recharge_template_id'), false);
  assert.equal(appSource.includes("'template_id'"), false);
  assert.equal(appSource.includes('X-Recharge-Template-ID'), false);
});

test('Case 1 helpers format North American plan names, equivalent prices, and bonus percentages', async () => {
  assert.equal(rechargeHelpers.vipCycleLabel('day'), 'Daily Plan');
  assert.equal(rechargeHelpers.vipCycleLabel('week'), 'Weekly Plan');
  assert.equal(rechargeHelpers.vipCycleLabel('month'), 'Monthly Plan');
  assert.equal(rechargeHelpers.vipCycleLabel('year'), 'Annual Plan');

  assert.equal(rechargeHelpers.formatSubscriptionEquivalent(99, 'day'), '$0.99/day');
  assert.equal(rechargeHelpers.formatSubscriptionEquivalent(499, 'week'), '≈ $0.71/day');
  assert.equal(rechargeHelpers.formatSubscriptionEquivalent(1499, 'month'), '≈ $0.50/day');
  assert.equal(rechargeHelpers.formatSubscriptionEquivalent(9999, 'year'), '≈ $8.33/mo');
  assert.equal(rechargeHelpers.formatSubscriptionEquivalent(0, 'month'), '');

  assert.equal(rechargeHelpers.calcCoinBonusPercent(499, 50), '+10%');
  assert.equal(rechargeHelpers.calcCoinBonusPercent(999, 150), '+15%');
  assert.equal(rechargeHelpers.calcCoinBonusPercent(1999, 400), '+20%');
  assert.equal(rechargeHelpers.calcCoinBonusPercent(4999, 1250), '+25%');
  assert.equal(rechargeHelpers.calcCoinBonusPercent(499, 0), '');

  const subPacks = [
    { id: 1, isHighlighted: false },
    { id: 2, isHighlighted: true },
    { id: 3, isHighlighted: false },
  ];
  assert.equal(rechargeHelpers.resolveInitialCarouselIndex(subPacks, 3), 2);
  assert.equal(rechargeHelpers.resolveInitialCarouselIndex(subPacks, 999), 1);
  assert.equal(rechargeHelpers.resolveInitialCarouselIndex(subPacks, null), 1);
  assert.equal(rechargeHelpers.resolveInitialCarouselIndex([], null), 0);

  assert.equal(rechargeHelpers.formatValidListPrice(2699, 1999, '$26.99'), '$26.99');
  assert.equal(rechargeHelpers.formatValidListPrice(2699, 1999), '$26.99');
  assert.equal(rechargeHelpers.formatValidListPrice(1999, 1999, '$19.99'), '');
  assert.equal(rechargeHelpers.formatValidListPrice(1499, 1999, '$14.99'), '');
  assert.equal(rechargeHelpers.formatValidListPrice(0, 1999, '$0.00'), '');
  assert.equal(rechargeHelpers.formatValidListPrice(undefined, 1999), '');

  const sessionMap = new Map();
  globalThis.sessionStorage = {
    getItem: (k) => sessionMap.get(k) ?? null,
    setItem: (k, v) => sessionMap.set(k, String(v)),
    removeItem: (k) => sessionMap.delete(k),
  };
  rechargeHelpers.saveCheckoutOrigin('intent-reader-1', { source: 'reader', path: '/content?novel_id=7&chapter_index=5' });
  assert.deepEqual(rechargeHelpers.readCheckoutOrigin('intent-reader-1'), {
    source: 'reader',
    path: '/content?novel_id=7&chapter_index=5',
  });
  rechargeHelpers.clearCheckoutOrigin('intent-reader-1');
  assert.equal(rechargeHelpers.readCheckoutOrigin('intent-reader-1'), null);

  rechargeHelpers.saveLastCheckoutSelection({ provider: 'stripe', slotId: 30, productId: 10 }, 1_000);
  assert.deepEqual(rechargeHelpers.readLastCheckoutSelection(undefined, 2_000), {
    provider: 'stripe',
    slotId: 30,
    productId: 10,
  });
  // Source isolation & 30-min TTL
  rechargeHelpers.saveLastCheckoutSelection(
    { provider: 'stripe', slotId: 31, productId: 11, source: 'reader' },
    10_000,
  );
  assert.equal(rechargeHelpers.readLastCheckoutSelection('recharge', 11_000), null);
  assert.deepEqual(rechargeHelpers.readLastCheckoutSelection('reader', 11_000), {
    provider: 'stripe',
    slotId: 31,
    productId: 11,
    source: 'reader',
  });
  assert.equal(rechargeHelpers.readLastCheckoutSelection('reader', 10_000 + 31 * 60 * 1000), null);
  rechargeHelpers.clearLastCheckoutSelection();
  assert.equal(rechargeHelpers.readLastCheckoutSelection(), null);

  // StrictMode-safe non-destructive read + TTL for pending payment success modal
  rechargeHelpers.savePendingPaymentSuccess(
    { intentId: 'intent-ok-1', coins: 500, isVip: false, isWholeBook: false },
    50_000,
  );
  assert.deepEqual(rechargeHelpers.readPendingPaymentSuccess(51_000), {
    intentId: 'intent-ok-1',
    coins: 500,
    isVip: false,
    isWholeBook: false,
    vipTitle: undefined,
  });
  // Second read still returns payload (StrictMode double-initializer safe)
  assert.equal(rechargeHelpers.readPendingPaymentSuccess(51_500)?.intentId, 'intent-ok-1');
  // Expired after 10 minutes
  assert.equal(rechargeHelpers.readPendingPaymentSuccess(50_000 + 11 * 60 * 1000), null);
  rechargeHelpers.savePendingPaymentSuccess(
    { intentId: 'intent-ok-2', coins: 0, isVip: true, isWholeBook: false, vipTitle: 'StarNovel Unlimited' },
    60_000,
  );
  rechargeHelpers.clearPendingPaymentSuccess();
  assert.equal(rechargeHelpers.readPendingPaymentSuccess(61_000), null);
});

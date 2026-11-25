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

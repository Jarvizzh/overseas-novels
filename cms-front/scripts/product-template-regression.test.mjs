import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import ts from 'typescript';

async function loadTS(relPath) {
  let source = await readFile(new URL(relPath, import.meta.url), 'utf8');
  source = source.replace(/import\s+type\s+[^;]+;\s*/g, '');
  if (relPath.includes('promotion.ts')) {
    source = source.replace("import { getCurrentAdminRole } from './promotion';", '');
    source = `const normalizeInline = true;\n${source}`;
  }
  const output = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(output).toString('base64')}`);
}

const prodHelpers = await loadTS('../src/utils/productCatalogHelpers.ts');
const tplHelpers = await loadTS('../src/utils/templateCatalogHelpers.ts');
const promoHelpers = await loadTS('../src/utils/promotion.ts');

test('TC-FE-01: ProductsPage Stripe MoR echo, published-only accounts, and per-account binding switch', () => {
  const echoed = prodHelpers.extractStripeCatalogTax({
    id: 'price_inclusive_1',
    tax_behavior: 'inclusive',
    product: { id: 'prod_1', tax_code: 'txcd_20030000' },
  });
  assert.deepEqual(echoed, { taxCode: 'txcd_20030000', taxBehavior: 'inclusive' });

  const accounts = [
    { id: 1, provider: 'stripe', environment: 'live', status: 'draft' },
    { id: 2, provider: 'stripe', environment: 'live', status: 'published' },
    { id: 3, provider: 'stripe', environment: 'live', status: 'disabled' },
    { id: 4, provider: 'stripe', environment: 'live', status: 'published' },
    { id: 5, provider: 'stripe', environment: 'sandbox', status: 'published' },
  ];
  const visibleLiveStripe = prodHelpers.filterPublishedChannelAccounts(accounts, 'stripe', 'live');
  assert.deepEqual(
    visibleLiveStripe.map((a) => a.id),
    [2, 4],
  );

  const productView = {
    stripe_binding: { account_id: 2, external_price_ref: 'price_primary_2', validation_error: '' },
    all_bindings: [
      { provider: 'stripe', account_id: 2, external_price_ref: 'price_primary_2', validation_error: '' },
      { provider: 'stripe', account_id: 4, external_price_ref: 'price_backup_4', validation_error: 'rate limit' },
    ],
  };
  assert.equal(prodHelpers.findBindingForAccount(productView, 'stripe', '4')?.external_price_ref, 'price_backup_4');
  assert.equal(prodHelpers.findBindingForAccount(productView, 'stripe', '4')?.validation_error, 'rate limit');
  assert.equal(prodHelpers.findBindingForAccount(productView, 'stripe', '99'), undefined);
});

test('TC-FE-02: CopyProduct nextCopyCode increments _V suffix and unchecking specific_novels never resurrects ID', () => {
  assert.equal(prodHelpers.nextCopyCode('BOOK_PACK_V2'), 'BOOK_PACK_V3');
  assert.equal(prodHelpers.nextCopyCode('BOOK_PACK'), 'BOOK_PACK_V2');

  // Unchecking 101 after copying [101, 102] submits [102] only even if 101 existed in manual_novel_ids.
  const toggled = prodHelpers.toggleSpecificNovelScope([101, 102], '101, 105', 101);
  assert.deepEqual(toggled.novel_scope_ids, [102]);
  assert.equal(toggled.manual_novel_ids, '105');

  const cleanToggled = prodHelpers.toggleSpecificNovelScope([101, 102], '', 101);
  assert.deepEqual(prodHelpers.parseSpecificNovelIds(cleanToggled.novel_scope_ids, cleanToggled.manual_novel_ids), [102]);
});

test('TC-FE-03: TemplatesTab enforces env/readiness filtering, promo reference delete block, and sole-default lock', () => {
  const readyAndUnready = [
    { id: 11, dual_channel_ready: true },
    { id: 12, dual_channel_ready: false },
    { id: 13, dual_channel_ready: true },
  ];
  assert.deepEqual(
    tplHelpers.filterEligibleTemplateProducts(readyAndUnready).map((p) => p.id),
    [11, 13],
  );

  assert.equal(tplHelpers.canDeleteTemplate({ is_default: false, linked_promotion_count: 2 }), false);
  assert.equal(tplHelpers.canDeleteTemplate({ is_default: false, linked_promotion_count: 0 }), true);
  assert.equal(tplHelpers.canDeleteTemplate({ is_default: true, linked_promotion_count: 0 }), false);

  // R-1: Only non-default live templates can become default or appear in promotion dropdowns.
  assert.equal(tplHelpers.canSetDefaultTemplate({ environment: 'sandbox', is_default: false }), false);
  assert.equal(tplHelpers.canSetDefaultTemplate({ environment: 'live', is_default: false }), true);
  assert.equal(tplHelpers.canSetDefaultTemplate({ environment: 'live', is_default: true }), false);
  assert.deepEqual(
    tplHelpers
      .filterLivePromotionTemplates([
        { id: 1, environment: 'live' },
        { id: 2, environment: 'sandbox' },
        { id: 3 },
      ])
      .map((t) => t.id),
    [1, 3],
  );

  const singleDefaultList = [
    { id: 1, is_default: true },
    { id: 2, is_default: false },
  ];
  assert.equal(tplHelpers.shouldLockDefaultTemplate(singleDefaultList, 1), true);
  assert.equal(tplHelpers.shouldLockDefaultTemplate(singleDefaultList, 2), false);

  const twoDefaultList = [
    { id: 1, is_default: true },
    { id: 2, is_default: true },
  ];
  assert.equal(tplHelpers.shouldLockDefaultTemplate(twoDefaultList, 1), false);
});

test('TC-FE-05 (CMS) & F-12: Promotion URL never appends template_id and legacy StripeOfferSettings is removed', () => {
  const built = promoHelpers.validateAndBuildPromotionPayload(
    {
      novelId: 42,
      name: 'Campaign X',
      chapterIndex: '3',
      utmSource: 'fb',
      utmCampaign: 'nov',
      domainId: '1',
      fbPixelId: '9',
      rechargeTemplateId: '77',
      coinCostPerThousand: '',
      startPayChapterIndex: '',
    },
    [{ id: 1, domain: 'reader.example.test', type: 'main', is_default: true, status: 1 }],
    [{ id: 9, pixel_id: 'PX-999' }],
  );
  assert.equal(built.error, undefined);
  assert.ok(built.finalUrl);
  assert.equal(built.finalUrl.includes('template_id='), false);
  assert.equal(built.payload.recharge_template_id, 77);

  assert.equal(existsSync(new URL('../src/pages/StripeOfferSettings.tsx', import.meta.url)), false);
});

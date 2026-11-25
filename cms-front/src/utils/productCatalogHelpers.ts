import type { ChannelAccount, ChannelBindingView, PaymentEnvironment, PaymentProvider, ProductView } from './paymentCatalogApi';

export function nextCopyCode(code: string): string {
  const match = code.match(/^(.*)_V(\d+)$/);
  const base = match ? match[1] : code;
  const nextVer = match ? Number(match[2]) + 1 : 2;
  const suffix = `_V${nextVer}`;
  return `${base.slice(0, Math.max(1, 64 - suffix.length))}${suffix}`;
}

export function extractStripeCatalogTax(validatedCatalog?: Record<string, unknown>): {
  taxCode: string;
  taxBehavior: 'exclusive' | 'inclusive';
} {
  const product =
    validatedCatalog && typeof validatedCatalog.product === 'object' && validatedCatalog.product !== null
      ? (validatedCatalog.product as Record<string, unknown>)
      : undefined;
  const taxCode = typeof product?.tax_code === 'string' && product.tax_code ? product.tax_code : 'txcd_10103001';
  const taxBehavior: 'exclusive' | 'inclusive' = validatedCatalog?.tax_behavior === 'inclusive' ? 'inclusive' : 'exclusive';
  return { taxCode, taxBehavior };
}

export function filterPublishedChannelAccounts(
  accounts: ChannelAccount[],
  provider: PaymentProvider,
  environment: PaymentEnvironment,
): ChannelAccount[] {
  return accounts.filter((a) => a.provider === provider && a.environment === environment && a.status === 'published');
}

export function findBindingForAccount(
  prod: ProductView | null | undefined,
  provider: 'paypal' | 'stripe',
  accountIdStr: string,
): ChannelBindingView | undefined {
  if (!prod) return undefined;
  const targetId = Number(accountIdStr);
  if (Number.isFinite(targetId) && targetId > 0 && Array.isArray(prod.all_bindings)) {
    const matched = prod.all_bindings.find((b) => b.provider === provider && b.account_id === targetId);
    if (matched) return matched;
    // When all_bindings is loaded, avoid showing another account's binding if the chosen backup account has none yet.
    return undefined;
  }
  const fallback = provider === 'paypal' ? prod.paypal_binding : prod.stripe_binding;
  if (!targetId || fallback?.account_id === targetId) return fallback;
  return undefined;
}

export function parseSpecificNovelIds(novelScopeIds: number[], manualNovelIds: string): number[] {
  const manual = manualNovelIds
    .split(/[\s,，]+/)
    .map((item) => Number(item))
    .filter((n) => Number.isInteger(n) && n > 0);
  return Array.from(new Set([...novelScopeIds, ...manual])).sort((a, b) => a - b);
}

export function toggleSpecificNovelScope(
  novelScopeIds: number[],
  manualNovelIds: string,
  novelId: number,
): { novel_scope_ids: number[]; manual_novel_ids: string } {
  const exists = novelScopeIds.includes(novelId);
  const nextIds = exists ? novelScopeIds.filter((id) => id !== novelId) : [...novelScopeIds, novelId];
  const nextManual = exists
    ? manualNovelIds
        .split(/[\s,，]+/)
        .map((item) => item.trim())
        .filter((item) => item !== '' && Number(item) !== novelId)
        .join(', ')
    : manualNovelIds;
  return {
    novel_scope_ids: nextIds,
    manual_novel_ids: nextManual,
  };
}

/**
 * Resolve the Reader API origin that serves `/api/v1/wallet/webhook/channel/:token`.
 * Falls back from CMS hostnames (`cms.<domain>`) to `h5.<domain>` and from localhost CMS to `:8080`.
 */
export function resolveWebhookOrigin(currentOrigin: string, configuredOrigin?: string): string {
  const configured = (configuredOrigin ?? '').trim().replace(/\/+$/, '');
  if (configured) {
    return configured;
  }
  const trimmedOrigin = currentOrigin.trim().replace(/\/+$/, '');
  if (!trimmedOrigin) {
    return '';
  }
  try {
    const parsed = new URL(trimmedOrigin);
    if (parsed.hostname === 'localhost' || parsed.hostname === '127.0.0.1') {
      return `${parsed.protocol}//${parsed.hostname}:8080`;
    }
    if (parsed.hostname.startsWith('cms.') && parsed.hostname.length > 4) {
      return `${parsed.protocol}//h5.${parsed.hostname.slice(4)}${parsed.port ? `:${parsed.port}` : ''}`;
    }
    return parsed.origin;
  } catch {
    return trimmedOrigin;
  }
}

/**
 * Build the full payment channel webhook URL from a relative `webhook_path` returned by the CMS API.
 */
export function buildChannelWebhookUrl(
  webhookPath: string,
  currentOrigin: string,
  configuredOrigin?: string,
): string {
  const path = webhookPath.trim();
  if (!path) return '';
  if (/^https?:\/\//i.test(path)) return path;
  const origin = resolveWebhookOrigin(currentOrigin, configuredOrigin);
  if (!origin) return path;
  return `${origin}${path.startsWith('/') ? '' : '/'}${path}`;
}

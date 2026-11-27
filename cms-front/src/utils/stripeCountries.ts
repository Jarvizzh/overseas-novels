import type { StripeSalesMode } from './paymentCatalogApi';

// Official ISO 3166-1 alpha-2 assigned country/territory codes (249 entries, sorted).
export const ALL_ISO2_COUNTRIES: readonly string[] = [
  'AD', 'AE', 'AF', 'AG', 'AI', 'AL', 'AM', 'AO', 'AQ', 'AR', 'AS', 'AT', 'AU', 'AW', 'AX', 'AZ',
  'BA', 'BB', 'BD', 'BE', 'BF', 'BG', 'BH', 'BI', 'BJ', 'BL', 'BM', 'BN', 'BO', 'BQ', 'BR', 'BS',
  'BT', 'BV', 'BW', 'BY', 'BZ', 'CA', 'CC', 'CD', 'CF', 'CG', 'CH', 'CI', 'CK', 'CL', 'CM', 'CN',
  'CO', 'CR', 'CU', 'CV', 'CW', 'CX', 'CY', 'CZ', 'DE', 'DJ', 'DK', 'DM', 'DO', 'DZ', 'EC', 'EE',
  'EG', 'EH', 'ER', 'ES', 'ET', 'FI', 'FJ', 'FK', 'FM', 'FO', 'FR', 'GA', 'GB', 'GD', 'GE', 'GF',
  'GG', 'GH', 'GI', 'GL', 'GM', 'GN', 'GP', 'GQ', 'GR', 'GS', 'GT', 'GU', 'GW', 'GY', 'HK', 'HM',
  'HN', 'HR', 'HT', 'HU', 'ID', 'IE', 'IL', 'IM', 'IN', 'IO', 'IQ', 'IR', 'IS', 'IT', 'JE', 'JM',
  'JO', 'JP', 'KE', 'KG', 'KH', 'KI', 'KM', 'KN', 'KP', 'KR', 'KW', 'KY', 'KZ', 'LA', 'LB', 'LC',
  'LI', 'LK', 'LR', 'LS', 'LT', 'LU', 'LV', 'LY', 'MA', 'MC', 'MD', 'ME', 'MF', 'MG', 'MH', 'MK',
  'ML', 'MM', 'MN', 'MO', 'MP', 'MQ', 'MR', 'MS', 'MT', 'MU', 'MV', 'MW', 'MX', 'MY', 'MZ', 'NA',
  'NC', 'NE', 'NF', 'NG', 'NI', 'NL', 'NO', 'NP', 'NR', 'NU', 'NZ', 'OM', 'PA', 'PE', 'PF', 'PG',
  'PH', 'PK', 'PL', 'PM', 'PN', 'PR', 'PS', 'PT', 'PW', 'PY', 'QA', 'RE', 'RO', 'RS', 'RU', 'RW',
  'SA', 'SB', 'SC', 'SD', 'SE', 'SG', 'SH', 'SI', 'SJ', 'SK', 'SL', 'SM', 'SN', 'SO', 'SR', 'SS',
  'ST', 'SV', 'SX', 'SY', 'SZ', 'TC', 'TD', 'TF', 'TG', 'TH', 'TJ', 'TK', 'TL', 'TM', 'TN', 'TO',
  'TR', 'TT', 'TV', 'TW', 'TZ', 'UA', 'UG', 'UM', 'US', 'UY', 'UZ', 'VA', 'VC', 'VE', 'VG', 'VI',
  'VN', 'VU', 'WF', 'WS', 'YE', 'YT', 'ZA', 'ZM', 'ZW',
];

// Restricted customer locations under Stripe Managed Payments (MoR) per Stripe official docs.
export const STRIPE_MOR_RESTRICTED_COUNTRIES: readonly string[] = ['CN', 'CU', 'IR', 'KP', 'RU', 'SH', 'SY'];

const MOR_RESTRICTED_SET = new Set<string>(STRIPE_MOR_RESTRICTED_COUNTRIES);

export const STRIPE_MOR_SUPPORTED_COUNTRIES: readonly string[] = ALL_ISO2_COUNTRIES.filter(
  (code) => !MOR_RESTRICTED_SET.has(code),
);

// Default supported markets when creating a Stripe product binding: US, CA, GB, AU.
export const DEFAULT_STRIPE_COUNTRIES: readonly string[] = ['US', 'CA', 'GB', 'AU'];

const ALL_ISO2_KEY = ALL_ISO2_COUNTRIES.join(',');
const MOR_ALL_KEY = STRIPE_MOR_SUPPORTED_COUNTRIES.join(',');

export function getDefaultStripeCountries(_salesMode?: StripeSalesMode): string[] {
  return [...DEFAULT_STRIPE_COUNTRIES];
}

export function formatDefaultStripeCountriesInput(salesMode?: StripeSalesMode): string {
  return getDefaultStripeCountries(salesMode).join(', ');
}

export function formatAllStripeCountriesInput(salesMode: StripeSalesMode): string {
  return (salesMode === 'merchant_of_record' ? STRIPE_MOR_SUPPORTED_COUNTRIES : ALL_ISO2_COUNTRIES).join(', ');
}

export function parseStripeCountriesInput(raw: string): string[] {
  return Array.from(
    new Set(
      raw
        .split(/[\s,，]+/)
        .map((c) => c.trim().toUpperCase())
        .filter(Boolean),
    ),
  );
}

export function detectStripeCountriesPreset(codes: readonly string[]): 'global' | 'mor_all' | 'custom' {
  if (codes.length === 0) return 'custom';
  const normalized = Array.from(new Set(codes.map((c) => c.trim().toUpperCase()).filter(Boolean))).sort();
  const key = normalized.join(',');
  if (key === ALL_ISO2_KEY) return 'global';
  if (key === MOR_ALL_KEY) return 'mor_all';
  return 'custom';
}

export function resolveCountriesOnSalesModeChange(
  currentRaw: string,
  prevMode: StripeSalesMode,
  nextMode: StripeSalesMode,
): string {
  if (prevMode === nextMode) return currentRaw;
  const parsed = parseStripeCountriesInput(currentRaw);
  if (parsed.length === 0) {
    return formatDefaultStripeCountriesInput(nextMode);
  }
  const preset = detectStripeCountriesPreset(parsed);
  if (prevMode === 'direct' && preset === 'global' && nextMode === 'merchant_of_record') {
    return formatAllStripeCountriesInput('merchant_of_record');
  }
  if (prevMode === 'merchant_of_record' && preset === 'mor_all' && nextMode === 'direct') {
    return formatAllStripeCountriesInput('direct');
  }
  return currentRaw;
}

export function formatAllowedCountriesSummary(codes?: readonly string[]): string {
  if (!codes || codes.length === 0) return '—';
  const preset = detectStripeCountriesPreset(codes);
  if (preset === 'global') return `全球（全部 ${ALL_ISO2_COUNTRIES.length} 个国家/地区）`;
  if (preset === 'mor_all') return `Stripe MoR 官方全量（${STRIPE_MOR_SUPPORTED_COUNTRIES.length} 个国家/地区）`;
  if (codes.length > 10) {
    return `${codes.slice(0, 8).join(', ')} 等共 ${codes.length} 个国家/地区`;
  }
  return codes.join(', ');
}

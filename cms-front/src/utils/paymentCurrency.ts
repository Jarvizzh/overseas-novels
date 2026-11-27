const ZERO_DECIMAL_CURRENCIES = new Set([
  'BIF', 'CLP', 'DJF', 'GNF', 'JPY', 'KMF', 'KRW', 'MGA',
  'PYG', 'RWF', 'UGX', 'VND', 'VUV', 'XAF', 'XOF', 'XPF',
]);

const THREE_DECIMAL_CURRENCIES = new Set(['BHD', 'JOD', 'KWD', 'OMR', 'TND']);

export function currencyDecimals(currency: string): number {
  const code = currency.trim().toUpperCase();
  if (ZERO_DECIMAL_CURRENCIES.has(code)) return 0;
  if (THREE_DECIMAL_CURRENCIES.has(code)) return 3;
  return 2;
}

export function paymentMoney(value?: string | number | null, currency = 'USD'): string {
  if (value == null) return '—';
  const code = currency.trim().toUpperCase() || 'USD';
  const amount = BigInt(value);
  const absolute = amount < 0n ? -amount : amount;
  const sign = amount < 0n ? '−' : '';
  const decimals = currencyDecimals(code);
  if (decimals === 0) {
    return `${sign}${absolute.toLocaleString('en-US')} ${code}`;
  }
  const divisor = 10n ** BigInt(decimals);
  return `${sign}${(absolute / divisor).toLocaleString('en-US')}.${String(absolute % divisor).padStart(decimals, '0')} ${code}`;
}

export function formatCaseAmount(item: {
  kind?: string;
  amount_minor?: string | null;
  original_currency?: string | null;
  original_amount_minor?: string | null;
}): string {
  const origCurrency = item.original_currency?.trim().toUpperCase() || '';
  if (origCurrency && item.original_amount_minor != null) {
    return paymentMoney(item.original_amount_minor, origCurrency);
  }
  if (item.amount_minor != null) {
    return paymentMoney(item.amount_minor, 'USD');
  }
  if (item.kind === 'dispute') {
    return '未知';
  }
  return '—';
}

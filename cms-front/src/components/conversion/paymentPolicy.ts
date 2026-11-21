export type PaymentRange = 'all_paid' | 'first_paid_on_promotion';
export type PaymentMode = 'all' | 'ratio' | 'cycle';
export type PolicySource = 'own' | 'inherited' | 'implicit_all';

export interface PolicyAction {
  mode: PaymentMode;
  rate_bps?: number;
  send_count?: number;
  skip_count?: number;
}

export interface BandInput extends PolicyAction {
  min_minor: string;
  max_minor: string | null;
}

export interface PolicyBand extends BandInput {
  currency: string;
  branch_id?: string;
}

export interface PolicyGroup {
  payment_kind: string;
  bands: PolicyBand[] | null;
  fallback?: PolicyAction;
}

export interface PolicyConfig {
  payment_range: PaymentRange;
  groups: PolicyGroup[];
}

interface PolicyVersion {
  id: string;
  number: string;
  config: PolicyConfig;
}

export interface PolicyBranch {
  branch_id?: string;
  payment_kind: string;
  currency?: string;
  min_minor?: string;
  next_sequence?: string;
}

export interface PaymentPolicyView {
  app_id: string;
  promotion_id?: string;
  promotion_name?: string;
  payment_kinds: string[];
  first_paid_on_promotion: boolean;
  first_paid_disabled_reason?: string;
  config_source: PolicySource;
  own_version: PolicyVersion | null;
  inherited_version: PolicyVersion | null;
  config: PolicyConfig;
  branches: PolicyBranch[];
}

export type PolicyOp =
  | { op: 'add_band'; payment_kind: string; currency: string; bands: [BandInput] }
  | { op: 'update_band'; branch_id: string; currency: string; bands: [BandInput] }
  | { op: 'split_band'; branch_id: string; bands: [BandInput, BandInput] }
  | { op: 'merge_bands'; branch_ids: [string, string]; bands: [BandInput] }
  | { op: 'remove_band'; branch_id: string }
  | { op: 'set_fallback'; payment_kind: string; action: PolicyAction }
  | { op: 'remove_fallback' | 'remove_group'; payment_kind: string };

export interface PolicyDraft {
  app_id: string;
  promotion_id?: string;
  expected_version: string | null;
  expected_inherited_version?: string | null;
  payment_range?: PaymentRange;
  deactivate?: boolean;
  operations: PolicyOp[];
}

export interface ActionFields {
  mode: PaymentMode;
  ratio: string;
  send: string;
  skip: string;
}

export const policySourceLabels: Record<PolicySource, string> = {
  own: '专属规则',
  inherited: '继承接入默认',
  implicit_all: '默认全传',
};

export function policyEndpoint(integration: string, app: string, promotion = '') {
  const params = new URLSearchParams({ app_id: app });
  if (promotion) params.set('promotion_id', promotion);
  return `/conversion/integrations/${encodeURIComponent(integration)}/payment-policy?${params}`;
}

export function paymentKindLabel(kind: string) {
  return (
    (
      {
        '': '所有付款类型',
        '1': 'VIP',
        '2': '代币',
        single: '代币',
        whole_book: '整本',
        subscription_initial: '订阅首付',
        subscription_renewal: '订阅续费',
      } as Record<string, string>
    )[kind] ?? kind
  );
}

function currencyDigits(currency: string) {
  if (!/^[A-Z]{3}$/.test(currency) || currency === 'XXX') return undefined;
  return new Intl.NumberFormat('en', { style: 'currency', currency }).resolvedOptions().maximumFractionDigits;
}

const maxMinor = 9223372036854775807n;

export function decimalToMinor(input: string, precision: number, limit = maxMinor): string | undefined {
  const parts = /^([0-9]{1,19})(?:\.([0-9]+))?$/.exec(input.trim());
  if (!parts || (parts[2]?.length ?? 0) > precision) return undefined;
  const value = BigInt(parts[1] + (parts[2] ?? '').padEnd(precision, '0'));
  return value <= limit ? String(value) : undefined;
}

export function amountToMinor(input: string, currency: string) {
  const precision = currencyDigits(currency);
  return precision === undefined ? undefined : decimalToMinor(input, precision);
}

export function showMinor(value: string, currency: string) {
  const precision = currencyDigits(currency) ?? 0;
  if (!precision) return value;
  const full = value.padStart(precision + 1, '0');
  return `${full.slice(0, -precision)}.${full.slice(-precision)}`;
}

export function actionFields(action?: PolicyAction): ActionFields {
  return {
    mode: action?.mode ?? 'all',
    ratio: action?.rate_bps === undefined ? '' : showMinor(String(action.rate_bps), 'USD'),
    send: action?.send_count === undefined ? '' : String(action.send_count),
    skip: action?.skip_count === undefined ? '' : String(action.skip_count),
  };
}

export function parseAction(fields: ActionFields): PolicyAction | undefined {
  switch (fields.mode) {
    case 'all':
      return { mode: 'all' };
    case 'ratio': {
      const value = decimalToMinor(fields.ratio, 2, 10000n);
      return value === undefined ? undefined : { mode: 'ratio', rate_bps: Number(value) };
    }
    case 'cycle': {
      const send = /^[1-9][0-9]{0,15}$/.test(fields.send.trim()) ? Number(fields.send) : 0;
      const skip = /^[1-9][0-9]{0,15}$/.test(fields.skip.trim()) ? Number(fields.skip) : 0;
      return send > 0 && skip > 0 && Number.isSafeInteger(send + skip)
        ? { mode: 'cycle', send_count: send, skip_count: skip }
        : undefined;
    }
  }
}

export function actionLabel(action: PolicyAction) {
  if (action.mode === 'ratio' && action.rate_bps !== undefined) return `按 ${action.rate_bps / 100}% 回传`;
  if (action.mode === 'cycle') return `传 ${action.send_count} 笔 · 漏 ${action.skip_count} 笔`;
  return '全传';
}

export function bandLabel(band: PolicyBand) {
  const upper = band.max_minor === null ? '无上限' : showMinor(band.max_minor, band.currency);
  return `${band.currency} [${showMinor(band.min_minor, band.currency)}, ${upper})`;
}

export function policyVersion(view: PaymentPolicyView) {
  return view.own_version ?? view.inherited_version;
}

export function defaultPolicySummary(view: PaymentPolicyView) {
  const common = view.config.groups.find((group) => group.payment_kind === '');
  if (view.config.groups.some((group) => group.payment_kind || group.bands?.length)) {
    return '继承接入默认 · 按付款类型或金额匹配';
  }
  return `继承接入默认 · ${common?.fallback ? actionLabel(common.fallback) : '全传'}`;
}

export interface BandRow {
  key: string;
  kind: string;
  band: PolicyBand;
}

export type BandChange = BandRow | { key: string; kind: string; branchId: string; band: null };

export function bandChangeOperation(change: BandChange): PolicyOp {
  if (change.band === null) {
    return { op: 'remove_band', branch_id: change.branchId };
  }
  const { currency, branch_id, ...band } = change.band;
  return branch_id
    ? { op: 'update_band', branch_id, currency, bands: [band] }
    : { op: 'add_band', payment_kind: change.kind, currency, bands: [band] };
}

export function paymentBandRows(config: PolicyConfig, changes: BandChange[]): BandRow[] {
  const changed = new Set(changes.map(({ key }) => key));
  const rows = config.groups
    .flatMap((group) =>
      (group.bands ?? []).map((band) => ({
        key: band.branch_id
          ? `branch-${band.branch_id}`
          : `band-${group.payment_kind}-${band.currency}-${band.min_minor}`,
        kind: group.payment_kind,
        band,
      })),
    )
    .filter(({ key }) => !changed.has(key));

  rows.push(...changes.filter((change): change is BandRow => change.band !== null));
  return rows.sort((a, b) => {
    const groupOrder = a.kind.localeCompare(b.kind) || a.band.currency.localeCompare(b.band.currency);
    if (groupOrder !== 0) return groupOrder;
    const difference = BigInt(a.band.min_minor) - BigInt(b.band.min_minor);
    return difference < 0n ? -1 : difference > 0n ? 1 : 0;
  });
}

export function overlappingBand(rows: BandRow[]): BandRow | undefined {
  for (let i = 1; i < rows.length; i++) {
    const a = rows[i - 1];
    const b = rows[i];
    if (
      a.kind === b.kind &&
      a.band.currency === b.band.currency &&
      (a.band.max_minor === null || BigInt(a.band.max_minor) > BigInt(b.band.min_minor))
    ) {
      return b;
    }
  }
  return undefined;
}

export function discardPaymentDraft(onDiscard: () => void) {
  if (window.showConfirm) window.showConfirm('存在未发布的回传规则草稿，确认放弃？', onDiscard);
  else if (window.confirm('存在未发布的回传规则草稿，确认放弃？')) onDiscard();
}

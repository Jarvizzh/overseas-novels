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

export type BandChange = (BandRow | { key: string; kind: string; branchId: string; band: null }) & {
  editGroup?: number;
  conflict?: string[];
};

function sameAction(a: PolicyAction | undefined, b: PolicyAction | undefined) {
  return (
    a?.mode === b?.mode &&
    a?.rate_bps === b?.rate_bps &&
    a?.send_count === b?.send_count &&
    a?.skip_count === b?.skip_count
  );
}

function sameBand(a: PolicyBand | undefined, b: PolicyBand | undefined) {
  return (
    a?.currency === b?.currency &&
    a?.min_minor === b?.min_minor &&
    a?.max_minor === b?.max_minor &&
    sameAction(a, b)
  );
}

export function samePolicyGroup(a: PolicyGroup | undefined, b: PolicyGroup) {
  return (
    a !== undefined &&
    sameAction(a.fallback, b.fallback) &&
    (a.bands?.length ?? 0) === (b.bands?.length ?? 0) &&
    (b.bands ?? []).every(
      (band, index) => band.branch_id === a.bands?.[index]?.branch_id && sameBand(a.bands?.[index], band),
    )
  );
}

function mergeField<T>(base: T, draft: T, latest: T, onConflict: () => void): T {
  if (draft === base) return latest;
  if (latest !== base && latest !== draft) onConflict();
  return draft;
}

function mergeAction(
  base: PolicyAction | undefined,
  draft: PolicyAction | undefined,
  latest: PolicyAction | undefined,
  conflicts: string[],
): PolicyAction | undefined {
  if (sameAction(draft, base)) return latest;
  if (sameAction(latest, base) || sameAction(latest, draft)) return draft;
  const describe = (action: PolicyAction | undefined) => (action ? actionLabel(action) : '无兜底');
  if (!base || !draft || !latest || base.mode !== draft.mode || base.mode !== latest.mode) {
    conflicts.push(`回传方式与参数：草稿 ${describe(draft)}，生效 ${describe(latest)}`);
    return draft;
  }
  const numeric = (
    field: 'rate_bps' | 'send_count' | 'skip_count',
    label: string,
    format: (value: number | undefined) => string = String,
  ) =>
    mergeField(base[field], draft[field], latest[field], () =>
      conflicts.push(`${label}：草稿 ${format(draft[field])}，生效 ${format(latest[field])}`),
    );
  if (draft.mode === 'ratio') {
    return { mode: 'ratio', rate_bps: numeric('rate_bps', '回传比例', (value) => `${Number(value) / 100}%`) };
  }
  const send = numeric('send_count', '回传笔数');
  const skip = numeric('skip_count', '跳过笔数');
  if (!Number.isSafeInteger(Number(send) + Number(skip))) {
    conflicts.push(`合并后的循环笔数超出范围：草稿 ${describe(draft)}，生效 ${describe(latest)}`);
    return draft;
  }
  return { mode: 'cycle', send_count: send, skip_count: skip };
}

export function reconcileFallback(
  base: PolicyAction | undefined,
  fields: ActionFields | null,
  latest: PolicyAction | undefined,
) {
  const conflicts: string[] = [];
  const draft = fields ? parseAction(fields) : undefined;
  if (fields && !draft) {
    if (!sameAction(base, latest)) conflicts.push('兜底规则已更新，当前草稿参数有误');
    return { fields, conflicts };
  }
  const merged = mergeAction(base, draft, latest, conflicts);
  if (!conflicts.length && sameAction(merged, latest)) return { fields: undefined, conflicts };
  return { fields: sameAction(merged, draft) ? fields : merged ? actionFields(merged) : null, conflicts };
}

function mergeBand(base: PolicyBand, draft: PolicyBand, latest: PolicyBand, conflicts: string[]): PolicyBand {
  const amounts = (band: PolicyBand) => [band.currency, band.min_minor, band.max_minor].join('/');
  const band = { ...draft };
  if (draft.currency !== base.currency || latest.currency !== base.currency) {
    const merged = mergeField(amounts(base), amounts(draft), amounts(latest), () =>
      conflicts.push(`币种与金额：草稿 ${bandLabel(draft)}，生效 ${bandLabel(latest)}`),
    );
    if (merged === amounts(latest)) {
      band.currency = latest.currency;
      band.min_minor = latest.min_minor;
      band.max_minor = latest.max_minor;
    }
  } else {
    const display = (amount: string | null) =>
      amount === null ? '无上限' : showMinor(amount, draft.currency);
    band.min_minor = mergeField(base.min_minor, draft.min_minor, latest.min_minor, () =>
      conflicts.push(`最小金额：草稿 ${display(draft.min_minor)}，生效 ${display(latest.min_minor)}`),
    );
    band.max_minor = mergeField(base.max_minor, draft.max_minor, latest.max_minor, () =>
      conflicts.push(`最大金额：草稿 ${display(draft.max_minor)}，生效 ${display(latest.max_minor)}`),
    );
    if (band.max_minor !== null && BigInt(band.max_minor) <= BigInt(band.min_minor)) {
      conflicts.push(`合并后的上下限无效：草稿 ${bandLabel(draft)}，生效 ${bandLabel(latest)}`);
      band.min_minor = draft.min_minor;
      band.max_minor = draft.max_minor;
    }
  }
  const action = mergeAction(base, draft, latest, conflicts) ?? draft;
  return {
    currency: band.currency,
    min_minor: band.min_minor,
    max_minor: band.max_minor,
    branch_id: band.branch_id,
    mode: action.mode,
    rate_bps: action.rate_bps,
    send_count: action.send_count,
    skip_count: action.skip_count,
  };
}

export function reconcileBandChanges(base: PolicyConfig, latest: PolicyConfig, changes: BandChange[]) {
  const byID = (config: PolicyConfig) =>
    new Map(
      config.groups.flatMap(({ bands }) => (bands ?? []).map((band) => [band.branch_id, band] as const)),
    );
  const oldBands = byID(base);
  const newBands = byID(latest);
  const structuralConflicts = new Map<number, string[]>();
  const target = (change: BandChange) => (change.band === null ? change.branchId : change.band.branch_id);
  for (const change of changes) {
    const id = target(change);
    if (!id || change.editGroup === undefined) continue;
    const before = oldBands.get(id);
    const current = newBands.get(id);
    if (sameBand(before, current)) continue;
    const messages = structuralConflicts.get(change.editGroup) ?? [];
    messages.push(
      current
        ? `关联区间已变更：${bandLabel(current)} · ${actionLabel(current)}`
        : `关联区间已移除：${before ? bandLabel(before) : `#${id}`}`,
    );
    structuralConflicts.set(change.editGroup, messages);
  }

  return changes.flatMap((change): BandChange[] => {
    const id = target(change);
    const before = id ? oldBands.get(id) : undefined;
    const current = id ? newBands.get(id) : undefined;
    const structural = change.editGroup === undefined ? undefined : structuralConflicts.get(change.editGroup);
    if (change.band === null && !current) return [];
    if (structural) return [{ ...change, conflict: structural }];
    if (change.band === null) {
      return sameBand(before, current) || !current
        ? [change]
        : [
            {
              ...change,
              conflict: [
                `草稿删除此区间，生效规则已将其改为 ${bandLabel(current)} · ${actionLabel(current)}`,
              ],
            },
          ];
    }
    if (!id) return [change];
    if (!current) return [{ ...change, conflict: ['该区间已不在生效规则中'] }];
    if (!before) return [{ ...change, conflict: ['区间起始版本不可用，请撤销冲突编辑'] }];
    const conflicts: string[] = [];
    const band = mergeBand(before, change.band, current, conflicts);
    if (!conflicts.length && change.editGroup === undefined && sameBand(band, current)) return [];
    return [{ ...change, band, conflict: conflicts.length ? conflicts : undefined }];
  });
}

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
  const changed = new Set(
    changes.filter(({ band, conflict }) => band !== null || !conflict).map(({ key }) => key),
  );
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

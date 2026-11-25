import { SettlementCommands, type SettlementOrigin } from './SettlementCommands';
import { Activity, FileSearch, History, ListChecks, SlidersHorizontal } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Pagination } from '../components/ui/Pagination';
import {
  FilterChipsBar,
  SearchInput,
  SkeletonRows,
  StatusDot,
  TableRegion,
  ToolbarPopover,
} from '../components/ui/Primitives';
import { CmsDialog } from '../components/ui/CmsDialog';
import CustomSelect from '../components/CustomSelect';
import { PaymentLoadError, PaymentScopeBar, PaymentTabs } from '../components/payments/PaymentWorkspace';
import { usePermission } from '../context/PermissionContext';
import {
  PAYMENT_PAGE_SIZE,
  paymentMoney,
  paymentParams,
  paymentScope,
  paymentScopeLabel,
  paymentTime,
  usePaymentQuery,
} from '../utils/paymentWorkspace';

const PAGE_SIZE = PAYMENT_PAGE_SIZE;
const views = {
  movements: '资金流水',
  payouts: '结算与银行入账',
  'report-issues': '报告待核对',
} as const;
type DataView = keyof typeof views;
type View = DataView | 'commands';
const tabs = [
  { value: 'movements', label: views.movements, icon: <Activity size={14} aria-hidden="true" /> },
  { value: 'payouts', label: views.payouts, icon: <ListChecks size={14} aria-hidden="true" /> },
  { value: 'report-issues', label: views['report-issues'], icon: <FileSearch size={14} aria-hidden="true" /> },
  { value: 'commands', label: '操作记录', icon: <History size={14} aria-hidden="true" /> },
] as const;

const components = {
  gross: '总额',
  fee: '渠道手续费',
  net: '净额',
  sales_tax: '销售税',
  withheld_tax: '代扣税',
  processing_fee: '处理费',
  customer_facing: '买家实付',
  fx_fee: '换汇手续费',
} as const;
type Amounts = Partial<Record<keyof typeof components, string>>;
const amountKeys = Object.keys(components) as (keyof typeof components)[];
const movementKinds: Record<string, string> = {
  payment: '收款',
  refund: '退款',
  reversal: '冲回',
  dispute_debit: '拒付划扣',
  dispute_credit: '争议返还',
  fee: '手续费',
  tax: '税费',
  fx: '换汇',
  payout: '划拨',
  hold: '冻结',
  release: '解冻',
  other: '其他',
};
const payoutStatuses: Record<string, string> = {
  pending: '待处理',
  in_transit: '划拨中',
  paid: '已划拨',
  failed: '划拨失败',
  canceled: '已取消',
  ready: '渠道核对完成',
  matched: '已匹配',
  review: '证据待核对',
  reversed: '划拨冲回',
  awaiting_channel: '等待渠道划拨',
  missing_transfer: '缺少划拨流水',
  transfer_amount_mismatch: '划拨金额不符',
  missing_movements: '缺少批次流水',
  allocation_amount_mismatch: '批次净额不符',
  awaiting_bank: '等待银行入账',
  bank_delayed: '入账延迟',
  amount_mismatch: '银行金额不符',
  reference_mismatch: '银行参考号不符',
  matched_requires_review: '已匹配，需复核',
};

type Account = { provider: string; environment: string; account_id: string };
type PayoutReference = { id: string; external_ref: string };
type LedgerMovement = Account & {
  id: string;
  movement_key: string;
  provider_code: string | null;
  kind: string;
  occurred_at: string | null;
  review_required: boolean;
  amounts: Amounts;
  refs: { type: string; id: string }[];
  order_ids: string[];
  payouts: PayoutReference[];
};
type Payout = Account &
  PayoutReference & {
    channel_status: string;
    expected_minor: string;
    expected_at: string | null;
    bank_reference: string | null;
    allocation_required: boolean;
    allocation_complete: boolean;
    row_count: number;
    net_total_minor: string | null;
    transfer_minor: string | null;
    channel_check: string;
    bank_amount_minor: string | null;
    booked_at: string | null;
    bank_transaction_id: string | null;
    difference_minor: string | null;
    status: string;
  };
type ReportIssue = Account & {
  import_id: string;
  imported_at: string;
  external_ref: string;
  source: string;
  row_number: number;
  source_file: string;
  review_reason: string;
  movement_id: string | null;
};
type Result =
  | { view: 'movements'; total: number; items: LedgerMovement[]; totals: Amounts }
  | { view: 'payouts'; total: number; items: Payout[] }
  | { view: 'report-issues'; total: number; items: ReportIssue[] };

const usd = paymentMoney;
const dateTime = paymentTime;

function reconciliationLink(view: DataView, account: Account, filters: Record<string, string>) {
  return (
    '/payments/reconciliation?' +
    paymentParams({
      view,
      environment: account.environment,
      provider: account.provider,
      account_id: account.account_id,
      ...filters,
    })
  );
}
type Entry =
  | { view: 'movements'; row: LedgerMovement }
  | { view: 'payouts'; row: Payout }
  | { view: 'report-issues'; row: ReportIssue };
const mainAmounts = ['gross', 'fee', 'net'] as const;

const numberCell = 'text-right tabular-nums whitespace-nowrap';

function ReconciliationTable({
  headers,
  loading,
  children,
  label,
  footer,
}: {
  headers: readonly (string | { amount: string })[];
  loading: boolean;
  children: ReactNode;
  label: string;
  footer?: ReactNode;
}) {
  return (
    <TableRegion ariaLabel={label} footer={footer}>
      <table className="cms-table whitespace-nowrap" data-density="compact" aria-label={label} aria-busy={loading}>
        <thead>
          <tr>
            {headers.map((column) => {
              const name = typeof column === 'string' ? column : column.amount;
              return (
                <th key={name} scope="col" className={typeof column === 'string' ? undefined : numberCell}>
                  {name}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>{loading ? <SkeletonRows rows={4} cols={headers.length} /> : children}</tbody>
      </table>
    </TableRegion>
  );
}

function MovementRows({
  rows,
  onOpen,
  loading,
  footer,
}: {
  rows: LedgerMovement[];
  onOpen: (entry: Entry) => void;
  loading: boolean;
  footer?: ReactNode;
}) {
  const headers = [
    '流水',
    '渠道账户',
    '资金动作',
    ...mainAmounts.map((key) => ({ amount: components[key] })),
    '发生时间 UTC',
    '操作',
  ];
  return (
    <ReconciliationTable headers={headers} loading={loading} footer={footer} label="USD 资金流水">
      {rows.map((row) => (
        <tr key={row.id}>
          <td className="font-mono">{row.movement_key}</td>
          <td>{paymentScopeLabel(row)}</td>
          <td>
            <StatusDot label={movementKinds[row.kind] ?? row.kind} tone={row.review_required ? 'warning' : 'muted'} />
            {row.review_required && <div>证据待核对</div>}
          </td>
          {mainAmounts.map((key) => (
            <td key={key} className={numberCell}>
              {usd(row.amounts[key])}
            </td>
          ))}
          <td>{dateTime(row.occurred_at)}</td>
          <td>
            <button type="button" className="btn-detail" onClick={() => onOpen({ view: 'movements', row })}>
              详情
            </button>
          </td>
        </tr>
      ))}
    </ReconciliationTable>
  );
}

function PayoutRows({
  rows,
  onOpen,
  loading,
  footer,
}: {
  rows: Payout[];
  onOpen: (entry: Entry) => void;
  loading: boolean;
  footer?: ReactNode;
}) {
  const headers = [
    '结算批次',
    '渠道账户',
    '核对状态',
    { amount: '渠道划拨' },
    { amount: '银行实收' },
    { amount: '差额' },
    '操作',
  ];
  return (
    <ReconciliationTable headers={headers} loading={loading} footer={footer} label="USD 结算批次">
      {rows.map((row) => (
        <tr key={row.id}>
          <td className="font-mono">{row.external_ref}</td>
          <td>{paymentScopeLabel(row)}</td>
          <td>
            <StatusDot
              tone={row.status === 'matched' ? 'success' : 'warning'}
              label={payoutStatuses[row.status] ?? row.status}
            />
          </td>
          <td className={numberCell}>{usd(row.transfer_minor)}</td>
          <td className={numberCell}>{usd(row.bank_amount_minor)}</td>
          <td className={numberCell}>{usd(row.difference_minor)}</td>
          <td>
            <button type="button" className="btn-detail" onClick={() => onOpen({ view: 'payouts', row })}>
              详情
            </button>
          </td>
        </tr>
      ))}
    </ReconciliationTable>
  );
}

function ReportRows({
  rows,
  onOpen,
  loading,
  footer,
}: {
  rows: ReportIssue[];
  onOpen: (entry: Entry) => void;
  loading: boolean;
  footer?: ReactNode;
}) {
  return (
    <ReconciliationTable
      headers={['报表编号', '渠道账户', '文件及行号', '核对原因', '导入时间 UTC', '操作']}
      loading={loading}
      label="报告待核对"
      footer={footer}
    >
      {rows.map((row) => (
        <tr key={`${row.import_id}/${row.source_file}/${row.row_number}`}>
          <td className="font-mono">{row.external_ref}</td>
          <td>{paymentScopeLabel(row)}</td>
          <td>
            {row.source_file} · 第 {row.row_number} 行
          </td>
          <td>{row.review_reason === 'conflicting_ledger_evidence' ? '资金凭证冲突' : row.review_reason}</td>
          <td>{dateTime(row.imported_at)}</td>
          <td>
            <button type="button" className="btn-detail" onClick={() => onOpen({ view: 'report-issues', row })}>
              详情
            </button>
          </td>
        </tr>
      ))}
    </ReconciliationTable>
  );
}

function DetailFields({ fields }: { fields: [string, string][] }) {
  return (
    <dl className="grid grid-cols-2 gap-3 sm:grid-cols-3">
      {fields.map(([label, value]) => (
        <div key={label}>
          <dt className="text-xs text-[var(--cms-muted-fg)]">{label}</dt>
          <dd className="mt-1 break-words tabular-nums">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function ReconciliationDetail({
  entry,
  canWrite,
  canReadOrders,
  canReadDisputes,
  onOperate,
}: {
  entry: Entry;
  canWrite: boolean;
  canReadOrders: boolean;
  canReadDisputes: boolean;
  onOperate: (value: SettlementOrigin) => void;
}) {
  switch (entry.view) {
    case 'movements': {
      const row = entry.row;
      return (
        <div className="space-y-4 text-sm">
          <DetailFields
            fields={[
              ['资金动作', movementKinds[row.kind] ?? row.kind],
              ['渠道事件代码', row.provider_code || '—'],
              ['发生时间（UTC）', dateTime(row.occurred_at)],
              ...amountKeys
                .filter((key) => key in row.amounts)
                .map((key): [string, string] => [components[key], usd(row.amounts[key])]),
            ]}
          />
          {row.review_required && <StatusDot tone="warning" label="证据待核对" />}
          <div className="flex flex-wrap gap-2">
            {canReadOrders &&
              row.order_ids.map((id) => (
                <Link
                  key={id}
                  className="btn-detail"
                  to={`/payments/orders?${paymentParams({ order_id: id, environment: row.environment, provider: row.provider })}`}
                >
                  订单 #{id}
                </Link>
              ))}
            {row.payouts.map((payout) => (
              <Link
                key={payout.id}
                to={reconciliationLink('payouts', row, { payout_id: payout.id, detail_id: payout.id })}
                className="btn-detail"
              >
                批次 {payout.external_ref}
              </Link>
            ))}
          </div>
          {row.refs.length > 0 && (
            <div className="border-t border-[var(--cms-border)] pt-3">
              <h3 className="font-semibold">渠道凭证</h3>
              <ul className="mt-2 space-y-1">
                {row.refs.map((ref) => (
                  <li key={`${ref.type}/${ref.id}`}>
                    {canReadDisputes && ['stripe_dispute', 'paypal_reversal'].includes(ref.type) ? (
                      <Link
                        className="btn-detail"
                        to={`/payments/disputes?${paymentParams({
                          provider: row.provider,
                          environment: row.environment,
                          account_id: row.account_id,
                          external_id: ref.id,
                          tab: ref.type === 'paypal_reversal' ? 'reversals' : 'cases',
                        })}`}
                      >
                        {ref.type} · {ref.id}
                      </Link>
                    ) : (
                      <span className="break-all">
                        {ref.type} · {ref.id}
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      );
    }
    case 'payouts': {
      const row = entry.row;
      return (
        <div className="space-y-4 text-sm">
          <StatusDot
            tone={row.status === 'matched' ? 'success' : 'warning'}
            label={payoutStatuses[row.status] ?? row.status}
          />
          <DetailFields
            fields={[
              ['渠道划拨状态', payoutStatuses[row.channel_status] ?? row.channel_status],
              ['渠道证据', payoutStatuses[row.channel_check] ?? row.channel_check],
              ['预计入账时间（UTC）', dateTime(row.expected_at)],
              ['预计入账', usd(row.expected_minor)],
              ['渠道划拨', usd(row.transfer_minor)],
              ['成员净额', usd(row.net_total_minor)],
              ['银行实收', usd(row.bank_amount_minor)],
              ['差额', usd(row.difference_minor)],
              ['入账时间（UTC）', dateTime(row.booked_at)],
              ['银行凭证', row.bank_transaction_id || '—'],
              ['预期参考号', row.bank_reference || '—'],
              [
                '批次明细',
                row.allocation_required
                  ? `${row.row_count} 条 · ${row.allocation_complete ? '已导入' : '待补齐'}`
                  : '—',
              ],
            ]}
          />
          <div className="flex flex-wrap gap-2">
            <Link className="btn-secondary" to={reconciliationLink('movements', row, { payout_id: row.id })}>
              查看批次流水
            </Link>
            {canWrite && row.status !== 'matched' && (
              <button
                type="button"
                className="btn-primary"
                onClick={() =>
                  onOperate({
                    kind: 'bank_match',
                    provider: row.provider,
                    environment: row.environment,
                    account_id: row.account_id,
                    reference: row.external_ref,
                  })
                }
              >
                匹配银行流水
              </button>
            )}
          </div>
        </div>
      );
    }
    case 'report-issues': {
      const row = entry.row;
      const kind =
        row.source === 'paypal_settlement' ? 'paypal_report' : row.source === 'stripe_report' ? 'stripe_report' : null;
      return (
        <div className="space-y-4 text-sm">
          <DetailFields
            fields={[
              ['来源', row.source],
              ['文件 / 行号', `${row.source_file} · 第 ${row.row_number} 行`],
              ['核对原因', row.review_reason === 'conflicting_ledger_evidence' ? '资金凭证冲突' : row.review_reason],
              ['导入时间（UTC）', dateTime(row.imported_at)],
            ]}
          />
          {row.movement_id && (
            <Link
              className="btn-secondary"
              to={reconciliationLink('movements', row, { movement_id: row.movement_id, detail_id: row.movement_id })}
            >
              查看关联流水
            </Link>
          )}
          {canWrite && kind && (
            <button
              className="btn-secondary"
              type="button"
              onClick={() =>
                onOperate({
                  kind,
                  provider: row.provider,
                  environment: row.environment,
                  account_id: row.account_id,
                  report: {
                    import_id: row.import_id,
                    source_file: row.source_file,
                    row_number: row.row_number,
                    external_ref: row.external_ref,
                  },
                })
              }
            >
              补充报表凭证
            </button>
          )}
        </div>
      );
    }
  }
}

function MovementDateFilter({
  from,
  through,
  onApply,
}: {
  from: string;
  through: string;
  onApply: (dates: { from: string; through: string }) => void;
}) {
  const [start, setStart] = useState(from);
  const [end, setEnd] = useState(through);
  const [error, setError] = useState('');
  useEffect(() => {
    setStart(from);
    setEnd(through);
    setError('');
  }, [from, through]);
  return (
    <ToolbarPopover
      align="start"
      label="流水日期"
      icon={<SlidersHorizontal size={13} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />}
      badgeCount={Number(Boolean(from)) + Number(Boolean(through))}
      panelClassName="w-80 p-4"
    >
      {({ close }) => (
        <form
          className="space-y-3.5"
          onSubmit={(event) => {
            event.preventDefault();
            if (start && end && start > end) {
              setError('开始日期不能晚于结束日期。');
              return;
            }
            onApply({ from: start, through: end });
            close();
          }}
        >
          <div className="grid grid-cols-2 gap-2.5">
            <label className="block space-y-1">
              <span className="block text-[var(--cms-muted-fg)]">起始日期 UTC</span>
              <input
                type="date"
                className="input-field"
                value={start}
                onChange={(event) => {
                  setError('');
                  setStart(event.target.value);
                }}
              />
            </label>
            <label className="block space-y-1">
              <span className="block text-[var(--cms-muted-fg)]">结束日期 UTC</span>
              <input
                type="date"
                className="input-field"
                value={end}
                onChange={(event) => {
                  setError('');
                  setEnd(event.target.value);
                }}
              />
            </label>
          </div>
          {error && (
            <p role="alert" className="text-xs text-[var(--cms-error)]">
              {error}
            </p>
          )}
          <div className="pt-2 border-t border-[var(--cms-border)] flex justify-end items-center gap-2">
            <button
              type="button"
              className="btn-ghost"
              onClick={() => {
                setStart('');
                setEnd('');
                setError('');
                onApply({ from: '', through: '' });
                close();
              }}
            >
              清空
            </button>
            <button className="btn-primary h-8 px-3 text-xs" type="submit">
              应用筛选
            </button>
          </div>
        </form>
      )}
    </ToolbarPopover>
  );
}

const reconciliationFilters = {
  order_id: '',
  payout_id: '',
  movement_id: '',
  movement_key: '',
  payout_ref: '',
  kind: '',
  from: '',
  through: '',
  review_required: '',
  differences_only: '',
};

export default function PaymentReconciliation({ actor }: { actor?: string }) {
  const { admin, canRead, canWrite } = usePermission();
  const effectiveActor = actor || admin?.id || '';
  const canWriteRecon = canWrite('payments.reconciliation');
  const canReadOrders = canRead('payments.orders');
  const canReadDisputes = canRead('payments.disputes');
  const [params, setParams] = useSearchParams();
  const view = tabs.find((tab) => tab.value === params.get('view'))?.value ?? 'movements';
  const scope = paymentScope(params);
  const page = Math.max(1, Number(params.get('page')) || 1);
  const order = view === 'movements' ? params.get('order_id') : null;
  const payout = view === 'movements' || view === 'payouts' ? params.get('payout_id') : null;
  const movement = view === 'movements' ? params.get('movement_id') : null;
  const movementKey = view === 'movements' ? params.get('movement_key') : null;
  const payoutRef = view === 'payouts' ? params.get('payout_ref') : null;
  const kind = view === 'movements' ? (params.get('kind') ?? '') : '';
  const from = view === 'movements' ? (params.get('from') ?? '') : '';
  const through = view === 'movements' ? (params.get('through') ?? '') : '';
  const detailId = view === 'movements' || view === 'payouts' ? params.get('detail_id') : null;
  const issueKey = view === 'movements' ? 'review_required' : 'differences_only';
  const onlyIssues = params.get(issueKey) === 'true';
  const [search, setSearch] = useState('');
  const [searchByInternalId, setSearchByInternalId] = useState(false);
  const searchKey =
    view === 'payouts'
      ? searchByInternalId
        ? 'payout_id'
        : 'payout_ref'
      : searchByInternalId
        ? 'order_id'
        : 'movement_key';
  const searchLabels = view === 'payouts' ? ['渠道批次号', '内部批次号'] : ['流水编号', '订单号'];
  const [searchError, setSearchError] = useState('');
  const [reportEntry, setReportEntry] = useState<Entry>();
  const [origin, setOrigin] = useState<SettlementOrigin>();
  const [retry, setRetry] = useState(0);

  const query = paymentParams({
    ...scope,
    page: String(page),
    order_id: order,
    payout_id: payout,
    movement_id: movement,
    movement_key: movementKey,
    payout_ref: payoutRef,
    kind,
    from,
    through,
    [issueKey]: onlyIssues && (view === 'movements' || view === 'payouts') ? 'true' : '',
  });
  const url = view === 'commands' ? '' : `/payments/reconciliation/${view}?${query}`;
  const current = usePaymentQuery<Omit<Result, 'view'>>(url, retry);
  const result = current?.data ? ({ ...current.data, view } as Result) : undefined;
  const loading = !current;
  const detail = usePaymentQuery<{ items: (LedgerMovement | Payout)[] }>(
    detailId
      ? `/payments/reconciliation/${view}?${paymentParams({
          ...scope,
          [view === 'movements' ? 'movement_id' : 'payout_id']: detailId,
        })}`
      : '',
    retry,
  );
  const exactRow = detail?.data?.items.find((row) => row.id === detailId);
  const entry: Entry | undefined =
    view === 'movements' && exactRow && 'movement_key' in exactRow
      ? { view, row: exactRow }
      : view === 'payouts' && exactRow && 'external_ref' in exactRow
        ? { view, row: exactRow }
        : view === 'report-issues'
          ? reportEntry
          : undefined;
  useEffect(() => {
    setReportEntry(undefined);
    setOrigin(undefined);
  }, [url, view]);

  function update(fields: Record<string, string>, reset = true) {
    const next = new URLSearchParams(params);
    if (reset) {
      next.delete('page');
      next.delete('detail_id');
    }
    for (const [key, value] of Object.entries(fields)) {
      if (value) next.set(key, value);
      else next.delete(key);
    }
    setParams(next);
  }

  function openEntry(row: Entry) {
    if (row.view === 'report-issues') setReportEntry(row);
    else update({ detail_id: row.row.id }, false);
  }
  const chips = (
    [
      ['order_id', order, '订单号'],
      ['payout_id', payout, '内部批次号'],
      ['movement_id', movement, '内部流水号'],
      ['movement_key', movementKey, '流水编号'],
      ['payout_ref', payoutRef, '渠道批次号'],
      ['kind', movementKinds[kind], '流水类型'],
      ['from', from, '开始日期 UTC'],
      ['through', through, '结束日期 UTC'],
      [issueKey, onlyIssues ? '是' : '', '仅显示异常'],
    ] as const
  ).flatMap(([key, value, label]) =>
    value ? [{ key, label: `${label} ${value}`, onClear: () => update({ [key]: '' }) }] : [],
  );
  const footer = result && (
    <Pagination
      page={page}
      pageSize={PAGE_SIZE}
      total={result.total}
      onPageChange={(next) => update({ page: String(next) }, false)}
    />
  );
  const title = entry?.view === 'movements' ? entry.row.movement_key : entry?.row.external_ref;

  return (
    <PaymentTabs
      tabs={tabs}
      value={view}
      label="资金对账视图"
      onChange={(next: View) => {
        setSearch('');
        setSearchByInternalId(false);
        setSearchError('');
        update({ ...reconciliationFilters, view: next });
      }}
    >
      <PaymentScopeBar
        scope={scope}
        onChange={(next) =>
          update({ ...next, payout_id: '', movement_id: '', order_id: '', movement_key: '', payout_ref: '' })
        }
      />
      <SettlementCommands
        key={`${scope.environment}/${scope.provider}/${scope.account_id}`}
        actor={effectiveActor}
        scope={scope}
        history={view === 'commands'}
        page={page}
        onPageChange={(value) => {
          if (view === 'commands') update({ page: String(value) }, false);
        }}
        origin={canWriteRecon ? origin : undefined}
        onOriginClose={() => setOrigin(undefined)}
        onSettled={() => setRetry((value) => value + 1)}
      />
      {view !== 'commands' && (
        <>
          {(view === 'movements' || view === 'payouts') && (
            <div className="flex flex-wrap items-center justify-between gap-2">
              <form
                className="flex flex-wrap items-center gap-2"
                onSubmit={(event) => {
                  event.preventDefault();
                  const value = search.trim();
                  if (searchByInternalId && value && (!/^\d+$/.test(value) || /^0+$/.test(value))) {
                    setSearchError(`请输入有效的${searchLabels[1]}。`);
                    return;
                  }
                  setSearchError('');
                  update({
                    movement_id: '',
                    payout_id: '',
                    order_id: '',
                    movement_key: '',
                    payout_ref: '',
                    [searchKey]: value,
                  });
                }}
              >
                <CustomSelect
                  aria-label="对账查询字段"
                  width="140px"
                  value={searchByInternalId ? 'internal' : 'channel'}
                  options={[
                    { value: 'channel', label: searchLabels[0] },
                    { value: 'internal', label: searchLabels[1] },
                  ]}
                  onChange={(value) => {
                    setSearchByInternalId(value === 'internal');
                    setSearch('');
                    setSearchError('');
                  }}
                />
                <SearchInput
                  value={search}
                  onChange={(value) => {
                    setSearch(value);
                    setSearchError('');
                  }}
                  placeholder={`输入${searchLabels[Number(searchByInternalId)]}`}
                />
                <button className="btn-secondary" type="submit">
                  查询
                </button>
              </form>
              <div className="flex flex-wrap items-center gap-2">
                {view === 'movements' && (
                  <>
                    <CustomSelect
                      aria-label="流水类型"
                      width="140px"
                      value={kind}
                      options={[
                        { value: '', label: '全部流水类型' },
                        ...Object.entries(movementKinds).map(([value, label]) => ({ value, label })),
                      ]}
                      onChange={(kind) => update({ kind })}
                    />
                    <MovementDateFilter from={from} through={through} onApply={update} />
                  </>
                )}
                <label className="flex items-center gap-1 text-xs">
                  <input
                    type="checkbox"
                    checked={onlyIssues}
                    onChange={(event) => update({ [issueKey]: event.target.checked ? 'true' : '' })}
                  />
                  {view === 'payouts' ? '仅显示异常批次' : '仅显示证据待核对'}
                </label>
              </div>
            </div>
          )}
          {searchError && (
            <p role="alert" className="text-sm text-[var(--cms-error)]">
              {searchError}
            </p>
          )}
          <FilterChipsBar chips={chips} onResetAll={() => update(reconciliationFilters)} />
          {result?.view === 'movements' && Object.keys(result.totals).length > 0 && (
            <section className="cms-card space-y-2 p-4">
              <h2 className="text-sm font-semibold">当前筛选的已确认流水汇总 · USD</h2>
              <DetailFields
                fields={amountKeys
                  .filter((key) => key in result.totals)
                  .map((key) => [components[key], usd(result.totals[key])])}
              />
            </section>
          )}
          {current?.error ? (
            <PaymentLoadError message={current.error} onRetry={() => setRetry((value) => value + 1)} />
          ) : (
            <>
              {view === 'movements' && (
                <MovementRows
                  loading={loading}
                  rows={result?.view === view ? result.items : []}
                  onOpen={openEntry}
                  footer={footer}
                />
              )}
              {view === 'payouts' && (
                <PayoutRows
                  loading={loading}
                  rows={result?.view === view ? result.items : []}
                  onOpen={openEntry}
                  footer={footer}
                />
              )}
              {view === 'report-issues' && (
                <ReportRows
                  loading={loading}
                  rows={result?.view === view ? result.items : []}
                  onOpen={openEntry}
                  footer={footer}
                />
              )}
              {result && !result.items.length && (
                <p role="status" className="text-[11px] text-[var(--cms-muted-fg)]">
                  {order ? '该订单暂无已验证关联的 USD 资金流水。' : '当前范围没有匹配记录。'}
                </p>
              )}
            </>
          )}
        </>
      )}
      <CmsDialog
        open={Boolean(entry || detailId)}
        onClose={() => {
          if (detailId) update({ detail_id: '' }, false);
          else setReportEntry(undefined);
        }}
        title={title || '对账详情'}
        subtitle={entry ? paymentScopeLabel(entry.row) : undefined}
        variant="drawer"
        size="lg"
      >
        {entry && (
          <ReconciliationDetail
            entry={entry}
            canWrite={canWriteRecon}
            canReadOrders={canReadOrders}
            canReadDisputes={canReadDisputes}
            onOperate={setOrigin}
          />
        )}
        {detailId &&
          !entry &&
          (detail?.error ? (
            <PaymentLoadError message={detail.error} onRetry={() => setRetry((value) => value + 1)} />
          ) : (
            <p role="status" className="text-sm">
              {detail ? '当前渠道范围内找不到该记录。' : '正在加载对账详情…'}
            </p>
          ))}
      </CmsDialog>
    </PaymentTabs>
  );
}

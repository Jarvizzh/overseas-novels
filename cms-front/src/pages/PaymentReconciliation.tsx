import { SettlementCommands } from './SettlementCommands';
import { useEffect, useState, type ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Pagination } from '../components/ui/Pagination';
import { SkeletonRows, TableRegion } from '../components/ui/Primitives';
import { ApiError, apiRequest } from '../utils/api';

const PAGE_SIZE = 20;
const views = {
  movements: '资金流水',
  payouts: '结算批次',
  'report-issues': '报告待核对',
} as const;
type View = keyof typeof views;

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
const providers: Record<string, string> = { paypal: 'PayPal', stripe: 'Stripe' };
const movementKinds: Record<string, string> = {
  payment: '收款', refund: '退款', reversal: '冲回', dispute_debit: '拒付划扣', dispute_credit: '争议返还',
  fee: '手续费', tax: '税费', fx: '换汇', payout: '划拨', hold: '冻结', release: '解冻', other: '其他',
};
const payoutStatuses: Record<string, string> = {
  pending: '待处理', in_transit: '划拨中', paid: '已划拨', failed: '划拨失败', canceled: '已取消',
  ready: '渠道核对完成', matched: '已匹配', review: '证据待核对', reversed: '划拨冲回',
  awaiting_channel: '等待渠道划拨', missing_transfer: '缺少划拨流水', transfer_amount_mismatch: '划拨金额不符',
  missing_movements: '缺少批次流水', allocation_amount_mismatch: '批次净额不符', awaiting_bank: '等待银行入账',
  bank_delayed: '入账延迟', amount_mismatch: '银行金额不符', reference_mismatch: '银行参考号不符',
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
type Payout = Account & PayoutReference & {
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

function usd(value: string | null | undefined) {
  if (value == null) return '—';
  const cents = BigInt(value);
  const unsigned = cents < 0n ? -cents : cents;
  return `${cents < 0n ? '−' : ''}$${(unsigned / 100n).toLocaleString('en-US')}.${String(unsigned % 100n).padStart(2, '0')}`;
}

function dateTime(value: string | null) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false, timeZone: 'UTC' }) : '—';
}

function AccountLabel({ account }: { account: Account }) {
  return <>{providers[account.provider] ?? account.provider} · #{account.account_id}</>;
}

function reconciliationLink(view: View, account: Account, filters: Record<string, string>) {
  const query = new URLSearchParams({ view, environment: account.environment, provider: account.provider, ...filters });
  return `/payments/reconciliation?${query}`;
}

const numberCell = 'text-right tabular-nums whitespace-nowrap';

function ReconciliationTable({ headers, loading, children, label }: {
  headers: readonly (string | { amount: string })[];
  loading: boolean;
  children: ReactNode;
  label: string;
}) {
  return (
    <TableRegion ariaLabel={label}>
      <table className="cms-table whitespace-nowrap" data-density="compact" aria-label={label} aria-busy={loading}>
        <thead><tr>{headers.map((column) => {
          const name = typeof column === 'string' ? column : column.amount;
          return <th key={name} scope="col" className={typeof column === 'string' ? undefined : numberCell}>{name}</th>;
        })}</tr></thead>
        <tbody>{loading ? <SkeletonRows rows={4} cols={headers.length} /> : children}</tbody>
      </table>
    </TableRegion>
  );
}

function Movements({ rows, loading }: { rows: LedgerMovement[]; loading: boolean }) {
  const headers = ['流水及引用', '渠道账户', '资金动作', ...amountKeys.map((key) => ({ amount: components[key] })),
    '发生时间 UTC', '关联订单', '结算批次'];
  return (
    <ReconciliationTable headers={headers} loading={loading} label="USD 资金流水">
      {rows.map((row) => (
        <tr key={row.id}>
          <td>
            <div className="font-medium">{row.movement_key}</div>
            {row.refs.length > 0 && <details>
              <summary className="cursor-pointer">渠道引用</summary>
              <ul>{row.refs.map((ref) => <li key={`${ref.type}:${ref.id}`}>
                {['stripe_dispute', 'paypal_reversal'].includes(ref.type)
                  ? <Link className="btn-detail" to={`/payments/disputes?${new URLSearchParams({
                    external_id: ref.id, environment: row.environment, provider: row.provider,
                  })}`}>{ref.type} · {ref.id}</Link>
                  : <>{ref.type} · {ref.id}</>}
              </li>)}</ul>
            </details>}
          </td>
          <td><AccountLabel account={row} /></td>
          <td>
            {movementKinds[row.kind] ?? row.kind}{row.provider_code && <div>{row.provider_code}</div>}
            {row.review_required && <div className="text-[var(--cms-warning)]">证据待核对</div>}
          </td>
          {amountKeys.map((key) => <td key={key} className={numberCell}>{usd(row.amounts[key])}</td>)}
          <td>{dateTime(row.occurred_at)}</td>
          <td>{row.order_ids.length ? row.order_ids.map((id) => (
            <div key={id}><Link to={`/payments/orders?${new URLSearchParams({ order_id: String(id), environment: row.environment })}`} className="btn-detail">#{id}</Link></div>
          )) : '—'}</td>
          <td>{row.payouts.length ? row.payouts.map((payout) => (
            <div key={payout.id}><Link to={reconciliationLink('payouts', row, { payout_id: payout.id })}
              className="btn-detail">{payout.external_ref}</Link></div>
          )) : '—'}</td>
        </tr>
      ))}
    </ReconciliationTable>
  );
}

function Payouts({ rows, loading }: { rows: Payout[]; loading: boolean }) {
  const headers = ['结算批次', '渠道账户', '核对状态', { amount: '预计入账' }, { amount: '渠道划拨' },
    { amount: '成员净额' }, { amount: '银行流水金额' }, { amount: '差额' }, '预计入账 UTC', '银行凭证'];
  return (
    <ReconciliationTable headers={headers} loading={loading} label="USD 结算批次">
      {rows.map((row) => (
        <tr key={row.id}>
          <td>
            <div className="font-medium">{row.external_ref}</div>
            <Link className="btn-detail" to={reconciliationLink('movements', row, { payout_id: row.id })}>查看批次流水</Link>
          </td>
          <td><AccountLabel account={row} /></td>
          <td>
            <div className="font-medium">{payoutStatuses[row.status] ?? row.status}</div>
            <div>{payoutStatuses[row.channel_status] ?? row.channel_status} · {payoutStatuses[row.channel_check] ?? row.channel_check}</div>
          </td>
          <td className={numberCell}>{usd(row.expected_minor)}</td>
          <td className={numberCell}>{usd(row.transfer_minor)}</td>
          <td className={numberCell}>
            {usd(row.net_total_minor)}
            {row.allocation_required && <div>{row.row_count} 条 · {row.allocation_complete ? '已导入明细' : '明细待补'}</div>}
          </td>
          <td className={numberCell}>{usd(row.bank_amount_minor)}</td>
          <td className={numberCell}>{usd(row.difference_minor)}</td>
          <td>{dateTime(row.expected_at)}</td>
          <td>
            {row.bank_transaction_id ?? '—'}
            {row.booked_at && <div>{dateTime(row.booked_at)} UTC</div>}
            {row.bank_reference && <div>预期参考号 {row.bank_reference}</div>}
          </td>
        </tr>
      ))}
    </ReconciliationTable>
  );
}

function ReportIssues({ rows, loading }: { rows: ReportIssue[]; loading: boolean }) {
  return (
    <ReconciliationTable headers={['报告', '渠道账户', '文件及行号', '核对原因', '资金流水', '导入时间 UTC']}
      loading={loading} label="报告待核对">
      {rows.map((row) => (
        <tr key={`${row.import_id}/${row.source_file}/${row.row_number}`}>
          <td>{row.external_ref}<div>{row.source} · #{row.import_id}</div></td>
          <td><AccountLabel account={row} /></td>
          <td>{row.source_file} · 第 {row.row_number} 行</td>
          <td>{row.review_reason === 'conflicting_ledger_evidence' ? '资金凭证冲突' : row.review_reason}</td>
          <td>{row.movement_id ? <Link className="btn-detail" to={reconciliationLink('movements', row,
            { movement_id: row.movement_id })}>#{row.movement_id}</Link> : '—'}</td>
          <td>{dateTime(row.imported_at)}</td>
        </tr>
      ))}
    </ReconciliationTable>
  );
}

export default function PaymentReconciliation() {
  const [params, setParams] = useSearchParams();
  const selected = params.get('view');
  const view: View = selected === 'payouts' || selected === 'report-issues' ? selected : 'movements';
  const provider = params.get('provider') ?? '';
  const environment = params.get('environment') ?? 'live';
  const page = Math.max(1, Number(params.get('page')) || 1);
  const order = view === 'movements' ? params.get('order_id') : null;
  const payout = view !== 'report-issues' ? params.get('payout_id') : null;
  const movement = view === 'movements' ? params.get('movement_id') : null;
  const issueKey = view === 'movements' ? 'review_required' : 'differences_only';
  const onlyIssues = params.get(issueKey) === 'true';
  const [request, setRequest] = useState<{ url: string; result?: Result; error?: string }>();
  const [retry, setRetry] = useState(0);

  const query = new URLSearchParams({ environment, provider, page: String(page) });
  if (order) query.set('order_id', order);
  if (payout) query.set('payout_id', payout);
  if (movement) query.set('movement_id', movement);
  if (view !== 'report-issues' && onlyIssues) query.set(issueKey, 'true');
  const url = `/payments/reconciliation/${view}?${query}`;
  const current = request?.url === url ? request : undefined;
  const result = current?.result;
  const loading = !current;
  useEffect(() => {
    const controller = new AbortController();
    void apiRequest('GET', url, undefined, false, controller.signal).then((data) => {
      if (!controller.signal.aborted) setRequest({ url, result: { ...data, view } });
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setRequest({
        url,
        error: error instanceof ApiError && error.status === 403 ? '当前角色没有资金对账权限。' : '资金对账加载失败。',
      });
    });
    return () => controller.abort();
  }, [url, view, retry]);

  function update(key: string, value: string) {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value); else next.delete(key);
    if (key !== 'page') next.delete('page');
    if (key === 'environment' || key === 'provider') {
      next.delete('payout_id');
      next.delete('movement_id');
    }
    setParams(next);
  }

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">资金对账</h1>
      <SettlementCommands key={environment} environment={environment} onSettled={() => setRetry((value) => value + 1)} />
      <nav aria-label="资金对账视图" className="flex flex-wrap gap-2">
        {(Object.entries(views) as [View, string][]).map(([key, label]) => (
          <Link key={key} to={`?${new URLSearchParams({ view: key, provider, environment })}`}
            aria-current={view === key ? 'page' : undefined} className={view === key ? 'btn-primary' : 'btn-secondary'}>{label}</Link>
        ))}
      </nav>
      <div className="flex flex-wrap items-end gap-3">
        <label className="grid gap-1 text-sm">环境
          <select className="input-field" value={environment} onChange={(event) => update('environment', event.target.value)}>
            <option value="live">正式</option><option value="sandbox">沙箱</option>
          </select>
        </label>
        <label className="grid gap-1 text-sm">渠道
          <select className="input-field" value={provider} onChange={(event) => update('provider', event.target.value)}>
            <option value="">全部</option><option value="paypal">PayPal</option><option value="stripe">Stripe</option>
          </select>
        </label>
        {view !== 'report-issues' && <label className="flex items-center gap-1 self-center text-sm">
          <input type="checkbox" checked={onlyIssues} onChange={(event) => update(issueKey, event.target.checked ? 'true' : '')} />
          {view === 'payouts' ? '仅显示异常批次' : '仅显示证据待核对'}
        </label>}
        {([['order_id', order, '订单'], ['payout_id', payout, '批次'], ['movement_id', movement, '流水']] as const)
          .map(([key, value, label]) => value && <button key={key} type="button" className="btn-secondary"
            onClick={() => update(key, '')}>清除{label}筛选 #{value}</button>)}
      </div>
      {view === 'movements' && <p className="text-sm text-[var(--cms-muted-fg)]">
        仅展示有渠道流水证据的 USD 金额。历史订单可能只有本地金额，订单关联以同账户的已验证凭证为准。
      </p>}
      {view === 'payouts' && <p className="text-sm text-[var(--cms-muted-fg)]">
        USD 差额为银行流水金额减去预计入账。只有标记为已匹配的批次已完成核对。PayPal 批次显示已识别的划拨流水。
      </p>}
      {result?.view === 'movements' && Object.keys(result.totals).length > 0 && <section className="cms-card p-4 space-y-2">
        <h2 className="text-sm font-semibold">当前筛选的已确认流水汇总 · USD</h2>
        <dl className="flex flex-wrap gap-x-6 gap-y-2">
          {amountKeys.filter((key) => key in result.totals).map((key) => <div key={key}>
            <dt className="text-xs text-[var(--cms-muted-fg)]">{components[key]}</dt>
            <dd className="font-medium tabular-nums">{usd(result.totals[key])}</dd>
          </div>)}
        </dl>
      </section>}
      {current?.error ? <div className="flex items-center gap-2">
        <p role="alert">{current.error}</p>
        <button type="button" className="btn-secondary" onClick={() => { setRequest(undefined); setRetry(retry + 1); }}>重试</button>
      </div> : <>
        {view === 'movements' && <Movements loading={loading} rows={result?.view === 'movements' ? result.items : []} />}
        {view === 'payouts' && <Payouts loading={loading} rows={result?.view === 'payouts' ? result.items : []} />}
        {view === 'report-issues' && <ReportIssues loading={loading} rows={result?.view === 'report-issues' ? result.items : []} />}
        {result && result.items.length === 0 && <p role="status">
          {result.total > 0 ? '当前页无记录，可返回前一页。' : order ? '该订单暂无已验证关联的 USD 资金流水。' : '没有符合条件的记录。'}
        </p>}
        {result && <Pagination page={page} pageSize={PAGE_SIZE} total={result.total}
          onPageChange={(next) => update('page', String(next))} />}
      </>}
    </div>
  );
}

import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Activity, FileSearch, ListChecks, SlidersHorizontal } from 'lucide-react';
import CustomSelect from '../components/CustomSelect';
import { CmsDialog } from '../components/ui/CmsDialog';
import { CursorPagination } from '../components/ui/Pagination';
import {
  type FilterChip,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ToolbarPopover,
  ViewOptionsPopover,
} from '../components/ui/Primitives';
import {
  PaymentFilterBar,
  PaymentLoadError,
  PaymentScopeBar,
  PaymentTabs,
} from '../components/payments/PaymentWorkspace';
import { usePermission } from '../context/PermissionContext';
import { apiRequest, paymentCaseOwnersPath, type PaymentCaseOwnerScope } from '../utils/api';
import { copyTextToClipboard } from '../utils/clipboard';
import {
  formatCaseAmount,
  paymentError,
  paymentMoney,
  paymentParams,
  paymentScope,
  paymentScopeLabel,
  paymentTime,
  providerLabels,
  usePaymentQuery,
} from '../utils/paymentWorkspace';

type View = 'risk' | 'disputes';
type CaseRow = {
  id: string;
  provider: string;
  environment: string;
  account_id: string;
  kind: string;
  external_id: string;
  provider_status: string;
  stage: string;
  reason: string;
  outcome: string;
  is_open: boolean;
  work_state: string;
  owner: string | null;
  due_at: string | null;
  work_due_at: string | null;
  escalated_at: string | null;
  amount_minor: string | null;
  original_currency: string | null;
  original_amount_minor: string | null;
  sales_mode: 'direct' | 'merchant_of_record' | null;
  receipt_refs: string[];
  order_ids: string[];
  reminder: boolean;
};
type Receipt = {
  id: string;
  receipt: string;
  order_id: string;
  type: string;
  state: string;
  paid_at: string | null;
  fulfilled_at: string | null;
  paid_period_start: string | null;
  paid_period_end: string | null;
  usage_count: number;
  refunds: { id: string; amount_minor: string }[];
  loss_minor: string | null;
  blocked: boolean;
  evidence_key: string | null;
  adjustment_pending: boolean;
  entitlement_action: 'apply_loss' | 'restore' | null;
  entitlement_result: {
    id: string;
    status: 'pending' | 'applied' | 'stale';
    blocked: boolean;
    finished_at: string | null;
  } | null;
};
type Detail = CaseRow & {
  available_actions: ('assign' | 'approve' | 'reject')[];
  receipts: Receipt[];
  audit: { actor: string; action: string; reason: string; recorded_at: string }[];
  sources: { source: string; source_ref: string; observed_at: string; codes?: object }[];
};
type Decision = {
  id: string;
  order_id: string | null;
  checkout_environment: string;
  provider: string | null;
  stage: string;
  reason: string;
  outcome: string;
  rule_version: number;
  evaluated_at: string;
};
type Owner = { id: string; name: string };
type Action = 'assign' | 'approve' | 'reject' | 'apply_loss' | 'restore';
const entitlementStatus = {
  pending: { tone: 'warning', label: '等待执行' },
  applied: { tone: 'success', label: '执行完成' },
  stale: { tone: 'warning', label: '凭证或状态已变化' },
} as const;
const providerResponseStates = new Set([
  'WAITING_FOR_SELLER_RESPONSE',
  'needs_response',
  'warning_needs_response',
  'appeal_needs_response',
]);
const labels: Record<string, string> = {
  risk_review: '渠道复核',
  risk_signal: '渠道风险信号',
  dispute: '争议',
  reversal: '独立冲回',
  observed: '已记录',
  pending: '待复核',
  approved: '已批准，待核验',
  refund_due: '财务退款待办',
  dispute_due: '跟踪关联争议',
  closed: '已处置',
  allow: '放行',
  block: '拦截',
  verify: '待确认',
  review: '待复核',
  assign: '分派负责人',
  approve: '批准重新核验',
  reject: '拒绝并转财务',
  apply_loss: '确认权益冲回',
  restore: '确认权益恢复',
};

const formatDate = paymentTime;
const usd = paymentMoney;
const workTone = (state: string) =>
  ['pending', 'refund_due', 'dispute_due', 'review', 'verify'].includes(state)
    ? 'warning'
    : ['closed', 'allow'].includes(state)
      ? 'success'
      : state === 'block'
        ? 'error'
        : 'muted';

function CaseActionForm({
  detail,
  action,
  roleScope,
  target,
  onClose,
  onDone,
}: {
  detail: Detail;
  action: Action;
  roleScope: PaymentCaseOwnerScope;
  target?: Receipt;
  onClose: () => void;
  onDone: () => void;
}) {
  const id = useId();
  const [owner, setOwner] = useState('');
  const [reason, setReason] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [ownersRevision, setOwnersRevision] = useState(0);
  const request = useRef<{ body: string; id: string } | undefined>(undefined);
  const owners = usePaymentQuery<Owner[]>(
    action === 'assign' ? paymentCaseOwnersPath(roleScope) : '',
    ownersRevision,
  );

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending || !reason.trim()) return;
    const body = { action, reason: reason.trim(), owner, evidence_key: target?.evidence_key };
    const serialized = JSON.stringify(body);
    if (request.current?.body !== serialized) request.current = { body: serialized, id: crypto.randomUUID() };
    setError('');
    setPending(true);
    try {
      await apiRequest('POST', `/payments/cases/${detail.id}/actions`, { ...body, request_id: request.current.id });
      onDone();
    } catch (failure) {
      setError(paymentError(failure));
    } finally {
      setPending(false);
    }
  }

  return (
    <CmsDialog
      open
      onClose={onClose}
      title={labels[action]}
      subtitle={paymentScopeLabel(detail)}
      closedBy="closerequest"
      closeDisabled={pending}
    >
      <form className="space-y-3 text-sm" onSubmit={(event) => void submit(event)}>
        <p>
          案件 {detail.external_id}
          {target
            ? ` · 原收款 ${target.receipt} · 已确认本金损失 ${usd(target.loss_minor)}`
            : ` · ${formatCaseAmount(detail)}`}
        </p>
        {action === 'approve' && <p>批准后会重新核验原收款和售后状态，再决定是否继续履约。</p>}
        {action === 'reject' && <p>确认后转交财务，并按案件状态生成退款或争议待办。</p>}
        {target && (
          <>
            <p>
              作用订单 #{target.order_id} · 证据版本 <span className="font-mono">{target.evidence_key}</span>
            </p>
            <p>
              {action === 'apply_loss'
                ? '执行前会重新核验本金损失。执行完成后调整该订单发放的权益。'
                : '执行前会重新核验资金状态。执行完成后恢复该订单此前冲回的权益。'}
            </p>
          </>
        )}
        {action === 'assign' && (
          <div className="grid gap-1">
            <label htmlFor={`${id}-owner`}>负责人（必填）</label>
            <CustomSelect
              id={`${id}-owner`}
              width="100%"
              value={owner}
              onChange={setOwner}
              options={[
                { value: '', label: '选择负责人' },
                ...(owners?.data ?? []).map((entry) => ({ value: entry.id, label: entry.name })),
              ]}
            />
            {owners?.error && (
              <div>
                <span role="alert">{owners.error}</span>{' '}
                <button type="button" onClick={() => setOwnersRevision((value) => value + 1)}>
                  重试
                </button>
              </div>
            )}
          </div>
        )}
        <div className="grid gap-1">
          <label htmlFor={`${id}-reason`}>处理依据（必填）</label>
          <textarea
            id={`${id}-reason`}
            className="input-field"
            required
            maxLength={500}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
        </div>
        <div className="flex justify-end gap-2">
          <button
            className="btn-secondary"
            type="button"
            data-dialog-initial-focus
            disabled={pending}
            onClick={onClose}
          >
            取消
          </button>
          <button
            className="btn-primary"
            type="submit"
            disabled={pending || !reason.trim() || (action === 'assign' && !owner)}
          >
            {pending ? '提交中…' : '确认提交'}
          </button>
        </div>
        {error && <p role="alert">{error}</p>}
      </form>
    </CmsDialog>
  );
}

function providerConsole(row: CaseRow) {
  if (row.provider === 'paypal' && row.kind === 'dispute') {
    return row.environment === 'live' ? 'https://www.paypal.com/disputes/' : 'https://www.sandbox.paypal.com/disputes/';
  }
  if (row.provider !== 'stripe' || row.kind !== 'dispute') return '';
  return `https://dashboard.stripe.com/${row.environment === 'sandbox' ? 'test/' : ''}disputes/${encodeURIComponent(row.external_id)}`;
}

function CaseDetail({
  detail,
  roleScope,
  canWriteCases,
  canReadOrders,
  canReadReconciliation,
  canReadRefunds,
  canReadDisputes,
  refreshing,
  onRefresh,
  onDone,
}: {
  detail: Detail;
  roleScope: PaymentCaseOwnerScope;
  canWriteCases: boolean;
  canReadOrders: boolean;
  canReadReconciliation: boolean;
  canReadRefunds: boolean;
  canReadDisputes: boolean;
  refreshing: boolean;
  onRefresh: () => void;
  onDone: () => void;
}) {
  const [action, setAction] = useState<{ name: Action; target?: Receipt }>();
  const scope = { provider: detail.provider, environment: detail.environment, account_id: detail.account_id };
  const consoleURL = providerConsole(detail);
  const managed = detail.provider === 'stripe' && detail.sales_mode === 'merchant_of_record';
  const merchant = detail.provider === 'paypal' || detail.sales_mode === 'direct';
  const responsibility = managed ? 'Stripe 负责渠道处置' : merchant ? '商户负责渠道处置' : '销售模式待核实';
  const responseHint = managed ? '跟踪 Stripe 处置进度' : merchant ? '等待商户响应' : '核实处理责任后响应';
  const confirm = () => {
    setAction(undefined);
    onDone();
  };
  return (
    <section className="space-y-4 text-sm">
      <StatusDot tone={workTone(detail.work_state)} label={labels[detail.work_state] ?? detail.work_state} />
      {detail.kind === 'dispute' && <p>{responsibility}</p>}
      <p>
        {detail.stage || '—'} · {detail.provider_status || '—'} · {detail.outcome || '裁决待确认'}
        {detail.kind === 'dispute' &&
          detail.is_open &&
          providerResponseStates.has(detail.provider_status) &&
          ` · ${responseHint}`}
      </p>
      <p>
        渠道原因 {detail.reason || '未提供'} · 争议原币金额 {formatCaseAmount(detail)}
        {detail.original_currency &&
          detail.original_currency.toUpperCase() !== 'USD' &&
          detail.amount_minor != null &&
          ` · USD 结算 ${usd(detail.amount_minor)}`}
      </p>
      <p>
        举证截止（UTC） {formatDate(detail.due_at)} · 处置期限 {formatDate(detail.work_due_at)} ·{' '}
        {detail.owner || '未分派'}
      </p>
      {consoleURL && (
        <div className="flex flex-wrap items-center gap-2">
          <a href={consoleURL} target="_blank" rel="noreferrer" className="btn-detail">
            打开渠道后台
          </a>
          <button
            type="button"
            className="btn-detail"
            onClick={async () => {
              const copied = await copyTextToClipboard(detail.external_id);
              window.showToast?.(
                copied ? '案件编号已复制' : '复制失败，请选中标题中的案件编号',
                copied ? 'success' : 'error',
              );
            }}
          >
            复制渠道案件编号
          </button>
        </div>
      )}
      <div className="flex flex-wrap gap-2">
        <button type="button" className="btn-secondary" disabled={refreshing} onClick={onRefresh}>
          {refreshing ? '刷新中…' : '刷新案件'}
        </button>
        {canWriteCases && detail.available_actions.map((name) => (
          <button
            key={name}
            type="button"
            className="btn-secondary"
            disabled={refreshing}
            onClick={() => setAction({ name })}
          >
            {labels[name]}
          </button>
        ))}
      </div>
      {detail.receipts.map((receipt) => (
        <article key={receipt.id} className="rounded border border-[var(--cms-border)] p-3 space-y-2">
          <h3 className="font-medium">原收款 {receipt.receipt}</h3>
          <p>
            订单 {receipt.state} · 收款 {formatDate(receipt.paid_at)} · 履约 {formatDate(receipt.fulfilled_at)}
          </p>
          {receipt.paid_period_end && (
            <p>
              原账期 {formatDate(receipt.paid_period_start)} 至 {formatDate(receipt.paid_period_end)}
            </p>
          )}
          <p>
            相关章节解锁 {receipt.usage_count} 次 · 已确认本金损失 {usd(receipt.loss_minor)}
            {receipt.blocked && ' · 权益已冲回'}
          </p>
          {(canReadOrders || canReadReconciliation || canReadRefunds || canReadDisputes) && (
            <div className="flex flex-wrap gap-2">
              {canReadOrders && (
                <Link className="btn-detail" to={`/payments/orders?order_id=${receipt.order_id}`}>
                  订单 #{receipt.order_id}
                </Link>
              )}
              {canReadReconciliation && (
                <Link
                  className="btn-detail"
                  to={`/payments/reconciliation?${paymentParams({ ...scope, order_id: receipt.order_id })}`}
                >
                  流水与结算
                </Link>
              )}
              {canReadRefunds && (
                <Link
                  className="btn-detail"
                  to={`/payments/refunds?${paymentParams({ ...scope, order_id: receipt.order_id, receipt_id: receipt.id })}`}
                >
                  退款
                </Link>
              )}
              {canReadDisputes && (
                <Link
                  className="btn-detail"
                  to={`/payments/disputes?${paymentParams({ ...scope, receipt: receipt.receipt })}`}
                >
                  关联争议和冲回
                </Link>
              )}
            </div>
          )}
          {receipt.refunds.length > 0 && (
            <ul>
              {receipt.refunds.map((refund) => (
                <li key={refund.id}>
                  {refund.id} · {usd(refund.amount_minor)}
                </li>
              ))}
            </ul>
          )}
          {receipt.entitlement_result && (
            <div role="status" className="flex flex-wrap items-center gap-2">
              <span>{receipt.entitlement_result.blocked ? '权益冲回' : '权益恢复'}</span>
              <StatusDot {...entitlementStatus[receipt.entitlement_result.status]} />
              {receipt.entitlement_result.finished_at && (
                <span>{formatDate(receipt.entitlement_result.finished_at)} UTC</span>
              )}
              <span className="font-mono break-all">{receipt.entitlement_result.id}</span>
            </div>
          )}
          {receipt.adjustment_pending && receipt.entitlement_result?.status !== 'pending' && (
            <p role="status">关联案件已有权益处置等待执行</p>
          )}
          {canWriteCases && receipt.entitlement_action && (
            <button
              type="button"
              className="btn-secondary"
              disabled={refreshing}
              onClick={() => {
                if (receipt.entitlement_action) setAction({ name: receipt.entitlement_action, target: receipt });
              }}
            >
              {labels[receipt.entitlement_action]}
            </button>
          )}
        </article>
      ))}
      {canReadOrders &&
        detail.receipts.length === 0 &&
        detail.order_ids.map((order) => (
          <Link key={order} className="btn-detail" to={`/payments/orders?order_id=${order}`}>
            订单 #{order}
          </Link>
        ))}
      {detail.receipts.length === 0 && <p>暂无同账户已验证原收款。</p>}
      {canWriteCases && action && (
        <CaseActionForm
          key={`${action.name}/${action.target?.id ?? ''}`}
          detail={detail}
          action={action.name}
          roleScope={roleScope}
          target={action.target}
          onClose={() => setAction(undefined)}
          onDone={confirm}
        />
      )}
      {detail.sources.length > 0 && (
        <details>
          <summary>渠道信号与来源</summary>
          {detail.sources.map((source, index) => (
            <article key={`${source.source_ref}/${index}`}>
              <p>
                {source.source} · {source.source_ref} · {formatDate(source.observed_at)}
              </p>
              <pre className="overflow-x-auto">{JSON.stringify(source.codes, null, 2)}</pre>
            </article>
          ))}
        </details>
      )}
      {detail.audit.length > 0 && (
        <details>
          <summary>操作审计</summary>
          <ul>
            {detail.audit.map((entry, index) => (
              <li key={index}>
                {formatDate(entry.recorded_at)} · {entry.actor} · {labels[entry.action] ?? entry.action} ·{' '}
                {entry.reason}
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  );
}

const riskTabs = [
  { value: 'review', label: '履约复核', icon: <ListChecks size={14} aria-hidden="true" /> },
  { value: 'signals', label: '渠道信号', icon: <Activity size={14} aria-hidden="true" /> },
  { value: 'decisions', label: '结账决策', icon: <FileSearch size={14} aria-hidden="true" /> },
] as const;
const disputeTabs = [
  { value: 'cases', label: '争议案件', icon: <FileSearch size={14} aria-hidden="true" /> },
  { value: 'reversals', label: '独立冲回', icon: <ListChecks size={14} aria-hidden="true" /> },
] as const;
const kinds: Record<string, string> = {
  review: 'risk_review',
  signals: 'risk_signal',
  cases: 'dispute',
  reversals: 'reversal',
};
const searchFields = [
  { value: 'order_id', label: '订单号' },
  { value: 'receipt', label: '原收款编号' },
  { value: 'external_id', label: '渠道案件编号' },
];
const caseStates = ['observed', 'pending', 'approved', 'refund_due', 'dispute_due', 'closed'];
const deadlineOptions = [
  { value: '', label: '全部期限' },
  { value: 'overdue', label: '已逾期' },
  { value: 'upcoming', label: '24 小时内到期' },
];
type CursorPage<T> = { items: T[]; next_cursor?: string | null };
const signalEmptyStates = {
  unconfigured: '当前范围没有已启用渠道信号接收的账户。',
  awaiting_signals: '当前范围的渠道账户已配置，尚未采集到风险信号。',
  collected: '当前筛选没有匹配的风险信号。',
};
type CasePage = CursorPage<CaseRow> & { signal_state?: keyof typeof signalEmptyStates | null };

function CaseAdvancedFilterPopover({
  ownerId,
  deadline,
  attention,
  showDeadline,
  owners,
  ownersError,
  onRetryOwners,
  onApply,
}: {
  ownerId: string;
  deadline: string;
  attention: boolean;
  showDeadline: boolean;
  owners: Owner[];
  ownersError?: string;
  onRetryOwners: () => void;
  onApply: (fields: Record<string, string>) => void;
}) {
  const [draftOwner, setDraftOwner] = useState(ownerId);
  const [draftDeadline, setDraftDeadline] = useState(deadline);
  const [draftAttention, setDraftAttention] = useState(attention);

  useEffect(() => {
    setDraftOwner(ownerId);
    setDraftDeadline(deadline);
    setDraftAttention(attention);
  }, [ownerId, deadline, attention]);

  const ownerOptions = [
    { value: '', label: '全部负责人' },
    ...owners.map((owner) => ({ value: owner.id, label: owner.name })),
  ];
  if (draftOwner && !ownerOptions.some((item) => item.value === draftOwner)) {
    ownerOptions.push({ value: draftOwner, label: `负责人 #${draftOwner}` });
  }

  const badgeCount =
    (ownerId ? 1 : 0) + (showDeadline && deadline ? 1 : 0) + (attention ? 1 : 0);

  return (
    <ToolbarPopover
      align="start"
      label="更多筛选"
      icon={<SlidersHorizontal size={13} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />}
      badgeCount={badgeCount}
      panelClassName="w-72 p-4"
      onOpen={() => {
        setDraftOwner(ownerId);
        setDraftDeadline(deadline);
        setDraftAttention(attention);
      }}
    >
      {({ close }) => (
        <form
          className="space-y-3"
          onSubmit={(event) => {
            event.preventDefault();
            onApply({
              owner_id: draftOwner,
              deadline: showDeadline ? draftDeadline : '',
              attention: draftAttention ? 'true' : '',
            });
            close();
          }}
        >
          <div className="font-semibold text-[var(--cms-fg)]">负责人与期限筛选</div>
          <label className="block space-y-1">
            <span className="block text-[var(--cms-muted-fg)]">负责人</span>
            <CustomSelect
              aria-label="负责人"
              width="100%"
              value={draftOwner}
              options={ownerOptions}
              onChange={setDraftOwner}
            />
          </label>
          {ownersError && (
            <div role="alert" className="flex items-center justify-between gap-2 text-xs text-[var(--cms-error)]">
              <span className="truncate">{ownersError}</span>
              <button type="button" className="btn-detail shrink-0" onClick={onRetryOwners}>
                重试
              </button>
            </div>
          )}
          {showDeadline && (
            <label className="block space-y-1">
              <span className="block text-[var(--cms-muted-fg)]">案件期限</span>
              <CustomSelect
                aria-label="案件期限"
                width="100%"
                value={draftDeadline}
                options={deadlineOptions}
                onChange={setDraftDeadline}
              />
            </label>
          )}
          <label className="flex items-center gap-2 px-1 py-1.5 rounded-lg hover:bg-[var(--cms-secondary)] cursor-pointer text-[var(--cms-fg)]">
            <input
              type="checkbox"
              className="w-3.5 h-3.5 cursor-pointer rounded"
              checked={draftAttention}
              onChange={(event) => setDraftAttention(event.target.checked)}
            />
            <span>仅看待处理与期限提醒</span>
          </label>
          <div className="pt-2 border-t border-[var(--cms-border)] flex items-center justify-end gap-2">
            <button
              type="button"
              className="btn-ghost text-[var(--cms-muted-fg)]"
              onClick={() => {
                setDraftOwner('');
                setDraftDeadline('');
                setDraftAttention(false);
                onApply({ owner_id: '', deadline: '', attention: '' });
                close();
              }}
            >
              清空
            </button>
            <button type="submit" className="btn-primary h-8 px-3 text-xs">
              应用筛选
            </button>
          </div>
        </form>
      )}
    </ToolbarPopover>
  );
}

export default function PaymentCases({ view }: { view: View }) {
  const { canRead, canWrite } = usePermission();
  const searchFormId = useId();
  const roleScope: PaymentCaseOwnerScope = view === 'risk' ? 'risk' : 'disputes';
  const canWriteCases = view === 'risk' ? canWrite('payments.risk') : canWrite('payments.disputes');
  const canReadOrders = canRead('payments.orders');
  const canReadReconciliation = canRead('payments.reconciliation');
  const canReadRefunds = canRead('payments.refunds');
  const canReadDisputes = canRead('payments.disputes');
  const [params, setParams] = useSearchParams();
  const [revision, setRevision] = useState(0);
  const [ownersRevision, setOwnersRevision] = useState(0);
  const [detailRevision, setDetailRevision] = useState(0);
  const [notice, setNotice] = useState('');
  const [searchField, setSearchField] = useState('order_id');
  const [search, setSearch] = useState('');
  const [searchError, setSearchError] = useState('');
  const [density, setDensity] = useState<TableDensity>('comfortable');
  const trigger = useRef<HTMLButtonElement | null>(null);
  const pollAttempts = useRef(0);
  const selected = params.get('case_id');
  const scope = paymentScope(params);
  const tabs = view === 'risk' ? riskTabs : disputeTabs;
  const tab = tabs.find((item) => item.value === params.get('tab'))?.value ?? tabs[0].value;
  const decisions = tab === 'decisions';
  const linked = searchFields.some((field) => params.has(field.value));
  const kind = !params.has('tab') && linked ? '' : kinds[tab];
  const before = params.get('before') ?? '';
  const trail = (params.get('trail') ?? '').split(',').filter((item) => /^\d+$/.test(item));
  const query = decisions
    ? paymentParams({
        order_id: params.get('order_id'),
        before,
        provider: scope.provider,
        checkout_environment: scope.environment === 'live' ? 'production' : scope.environment,
      })
    : paymentParams({
        ...scope,
        view,
        kind,
        before,
        order_id: params.get('order_id'),
        receipt: params.get('receipt'),
        external_id: params.get('external_id'),
        attention: params.get('attention'),
        owner_id: params.get('owner_id'),
        work_state: params.get('work_state'),
        deadline: params.get('deadline'),
      });
  const cases = usePaymentQuery<CasePage>(decisions ? '' : `/payments/cases?${query}`, revision);
  const decisionsPage = usePaymentQuery<CursorPage<Decision>>(
    decisions ? `/payments/cases/decisions?${query}` : '',
    revision,
  );
  const detail = usePaymentQuery<Detail>(
    selected && !decisions ? `/payments/cases/${encodeURIComponent(selected)}` : '',
    detailRevision,
    true,
  );
  const waitingForExecution =
    detail?.data?.work_state === 'approved' || detail?.data?.receipts.some((receipt) => receipt.adjustment_pending);
  const matchingDetail =
    detail?.data &&
    detail.data.id === selected &&
    (!params.has('tab') || detail.data.kind === kind) &&
    (view === 'risk' ? ['risk_review', 'risk_signal'] : ['dispute', 'reversal']).includes(detail.data.kind) &&
    (['provider', 'environment', 'account_id'] as const).every(
      (key) => !params.get(key) || detail.data?.[key] === params.get(key),
    );
  const owners = usePaymentQuery<Owner[]>(decisions ? '' : paymentCaseOwnersPath(roleScope), ownersRevision);
  const data = decisions ? decisionsPage : cases;
  const signalState = cases?.data?.signal_state;
  const headers = decisions
    ? ['阶段', '决定', '原因', '渠道 / 环境 / 版本', '订单', '时间 UTC']
    : [
        '案件',
        '渠道 / 商户',
        view === 'disputes' ? '责任 / 状态' : '处理状态',
        '负责人',
        '截止时间 UTC',
        '金额',
        '操作',
      ];

  useEffect(() => {
    if (!selected) trigger.current?.focus();
    pollAttempts.current = 0;
  }, [selected]);

  useEffect(() => {
    if (!matchingDetail || !waitingForExecution || detail?.refreshing || pollAttempts.current >= 15) return;
    const timer = window.setTimeout(() => {
      pollAttempts.current += 1;
      setDetailRevision((value) => value + 1);
    }, 4000);
    return () => window.clearTimeout(timer);
  }, [matchingDetail, waitingForExecution, detail?.refreshing, detailRevision]);

  function refreshDetail() {
    pollAttempts.current = 0;
    setDetailRevision((value) => value + 1);
  }

  function update(fields: Record<string, string>, reset = true) {
    const next = new URLSearchParams(params);
    if (reset) {
      next.delete('before');
      next.delete('trail');
      next.delete('case_id');
    }
    for (const [key, value] of Object.entries(fields)) {
      if (value) next.set(key, value);
      else next.delete(key);
    }
    setParams(next);
  }

  function updateSearch(event: FormEvent) {
    event.preventDefault();
    const field = decisions ? 'order_id' : searchField;
    const value = search.trim();
    if (field === 'order_id' && value && (!/^\d+$/.test(value) || /^0+$/.test(value))) {
      setSearchError('请输入有效的订单号。');
      return;
    }
    setSearchError('');
    update({ tab, order_id: '', receipt: '', external_id: '', [field]: value });
  }

  const ownerId = params.get('owner_id') ?? '';
  const workState = params.get('work_state') ?? '';
  const deadline = params.get('deadline') ?? '';
  const attention = params.get('attention') === 'true';
  const showDeadline = tab === 'review' || tab === 'cases';
  const hasAnyFilter = Boolean(
    search ||
      params.get('order_id') ||
      (!decisions &&
        (params.get('receipt') ||
          params.get('external_id') ||
          ownerId ||
          workState ||
          (showDeadline && deadline) ||
          attention)),
  );

  const chips: FilterChip[] = [
    ...searchFields.flatMap(({ value, label }) =>
      params.get(value) && (!decisions || value === 'order_id')
        ? [
            {
              key: value,
              label: `${label}: ${params.get(value)}`,
              onClear: () => {
                if (searchField === value || decisions) {
                  setSearch('');
                  setSearchError('');
                }
                update({ [value]: '' });
              },
            },
          ]
        : [],
    ),
    ...(!decisions && workState
      ? [
          {
            key: 'work_state',
            label: `状态: ${labels[workState] ?? workState}`,
            onClear: () => update({ work_state: '' }),
          },
        ]
      : []),
    ...(!decisions && ownerId
      ? [
          {
            key: 'owner_id',
            label: `负责人: ${owners?.data?.find((owner) => owner.id === ownerId)?.name ?? ownerId}`,
            onClear: () => update({ owner_id: '' }),
          },
        ]
      : []),
    ...(!decisions && showDeadline && deadline
      ? [
          {
            key: 'deadline',
            label: `期限: ${deadlineOptions.find((option) => option.value === deadline)?.label ?? deadline}`,
            onClear: () => update({ deadline: '' }),
          },
        ]
      : []),
    ...(!decisions && attention
      ? [
          {
            key: 'attention',
            label: '仅看: 待处理与期限提醒',
            onClear: () => update({ attention: '' }),
          },
        ]
      : []),
  ];

  function resetFilters() {
    setSearch('');
    setSearchError('');
    update({
      order_id: '',
      receipt: '',
      external_id: '',
      owner_id: '',
      work_state: '',
      deadline: '',
      attention: '',
    });
  }

  const cursorFooter = data?.data && (
    <CursorPagination
      page={before ? trail.length + 2 : 1}
      count={data.data.items.length}
      hasNext={Boolean(data.data.next_cursor)}
      onFirst={() => update({ before: '', trail: '' })}
      onPrevious={() => update({ before: trail.at(-1) || '', trail: trail.slice(0, -1).join(',') }, false)}
      onNext={() =>
        update({ before: data.data?.next_cursor || '', trail: [...trail, before].filter(Boolean).join(',') }, false)
      }
    />
  );

  return (
    <PaymentTabs
      tabs={tabs}
      value={tab}
      label={view === 'risk' ? '风控视图' : '争议视图'}
      onChange={(value) => {
        setSearch('');
        setSearchError('');
        setNotice('');
        update({ tab: value, owner_id: '', attention: '', work_state: '', deadline: '' });
      }}
    >
      <PaymentScopeBar scope={scope} showAccount={!decisions} onChange={(next) => update(next)} />
      <PaymentFilterBar
        error={searchError}
        chips={chips}
        onResetAll={resetFilters}
        right={
          <>
            {data?.data && (
              <span className="tabular-nums">
                本页 {data.data.items.length} {decisions ? '条' : '件'}
              </span>
            )}
            <ViewOptionsPopover density={density} onDensityChange={setDensity} />
          </>
        }
      >
        <form
          id={searchFormId}
          className={decisions ? 'w-full sm:w-56' : 'flex items-center gap-1.5 w-full sm:w-auto'}
          onSubmit={updateSearch}
        >
          {!decisions && (
            <CustomSelect
              aria-label="查询字段"
              width="136px"
              options={searchFields}
              value={searchField}
              onChange={(value) => {
                setSearchField(value);
                setSearch('');
                setSearchError('');
              }}
            />
          )}
          <SearchInput
            className={decisions ? 'w-full' : 'flex-1 sm:w-56'}
            value={search}
            placeholder={`搜索${decisions ? '订单号' : searchFields.find((field) => field.value === searchField)?.label}...`}
            onChange={(value) => {
              setSearch(value);
              setSearchError('');
            }}
            onClear={() => {
              setSearch('');
              setSearchError('');
              const activeField = decisions ? 'order_id' : searchField;
              if (params.get(activeField)) {
                update({ [activeField]: '' });
              }
            }}
          />
        </form>
        {!decisions && (
          <>
            <CustomSelect
              aria-label="处理状态"
              width="140px"
              value={workState}
              options={[
                { value: '', label: '全部处理状态' },
                ...caseStates.map((value) => ({ value, label: labels[value] })),
              ]}
              onChange={(value) => update({ work_state: value })}
            />
            <CaseAdvancedFilterPopover
              ownerId={ownerId}
              deadline={deadline}
              attention={attention}
              showDeadline={showDeadline}
              owners={owners?.data ?? []}
              ownersError={owners?.error}
              onRetryOwners={() => setOwnersRevision((value) => value + 1)}
              onApply={(fields) => update(fields)}
            />
          </>
        )}
        <button type="submit" form={searchFormId} className="btn-secondary h-9 px-3 text-xs font-normal whitespace-nowrap">
          查询
        </button>
        {hasAnyFilter && (
          <button type="button" className="btn-ghost h-9 text-xs text-[var(--cms-muted-fg)]" onClick={resetFilters}>
            重置
          </button>
        )}
      </PaymentFilterBar>
      {notice && (
        <p role="status" className="text-xs text-[var(--cms-muted-fg)]">
          {notice}
        </p>
      )}
      {data?.error ? (
        <PaymentLoadError message={data.error} onRetry={() => setRevision((value) => value + 1)} />
      ) : (
        <TableRegion ariaLabel={tabs.find((item) => item.value === tab)?.label || '支付案件'} footer={cursorFooter}>
          <table className="cms-table" data-density={density} aria-busy={!data}>
            <thead>
              <tr>
                {headers.map((header) => (
                  <th scope="col" key={header} className={header === '金额' ? 'text-right' : ''}>
                    {header}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {!data && <SkeletonRows rows={4} cols={headers.length} />}
              {decisions
                ? decisionsPage?.data?.items.map((row) => (
                    <tr key={row.id}>
                      <td>{row.stage}</td>
                      <td>
                        <StatusDot tone={workTone(row.outcome)} label={labels[row.outcome] ?? row.outcome} />
                      </td>
                      <td>{row.reason}</td>
                      <td>
                        {providerLabels[row.provider ?? ''] ?? (row.provider || '尚未选渠道')} ·{' '}
                        {row.checkout_environment === 'production'
                          ? '正式'
                          : row.checkout_environment === 'sandbox'
                            ? '沙箱'
                            : row.checkout_environment}{' '}
                        · v{row.rule_version}
                      </td>
                      <td>
                        {row.order_id ? (
                          canReadOrders ? (
                            <Link className="btn-detail" to={`/payments/orders?order_id=${row.order_id}`}>
                              #{row.order_id}
                            </Link>
                          ) : (
                            `#${row.order_id}`
                          )
                        ) : (
                          '—'
                        )}
                      </td>
                      <td>{formatDate(row.evaluated_at)}</td>
                    </tr>
                  ))
                : cases?.data?.items.map((row) => (
                    <tr key={row.id}>
                      <td>
                        <div className="font-mono">{row.external_id}</div>
                        <div className="text-[var(--cms-muted-fg)]">{labels[row.kind] ?? row.kind}</div>
                      </td>
                      <td>{paymentScopeLabel(row)}</td>
                      <td>
                        {row.kind === 'dispute' && (
                          <div>
                            {row.sales_mode === 'merchant_of_record'
                              ? 'Stripe 负责'
                              : row.provider === 'paypal' || row.sales_mode === 'direct'
                                ? '商户负责'
                                : '责任待核实'}
                          </div>
                        )}
                        <StatusDot tone={workTone(row.work_state)} label={labels[row.work_state] ?? row.work_state} />
                        {row.escalated_at && <div>已升级</div>}
                        {row.reminder && row.is_open && <div>举证提醒</div>}
                      </td>
                      <td>{row.owner || '未分派'}</td>
                      <td>{formatDate(row.due_at ?? row.work_due_at)}</td>
                      <td className="text-right tabular-nums">{formatCaseAmount(row)}</td>
                      <td>
                        <button
                          type="button"
                          className="btn-detail"
                          aria-label={`查看 ${row.external_id}`}
                          onClick={(event) => {
                            trigger.current = event.currentTarget;
                            update({ case_id: row.id }, false);
                          }}
                        >
                          详情
                        </button>
                      </td>
                    </tr>
                  ))}
              {data?.data?.items.length === 0 && (
                <tr>
                  <td colSpan={headers.length} className="text-center">
                    <p role="status" className="text-[11px] text-[var(--cms-muted-fg)]">
                      {tab === 'signals' && signalState ? signalEmptyStates[signalState] : '当前范围没有匹配记录。'}
                    </p>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </TableRegion>
      )}
      <CmsDialog
        open={Boolean(selected) && !decisions}
        onClose={() => update({ case_id: '' }, false)}
        variant="drawer"
        size="lg"
        title={detail?.data && matchingDetail ? detail.data.external_id : '案件详情'}
        subtitle={detail?.data && matchingDetail ? paymentScopeLabel(detail.data) : undefined}
      >
        {detail?.error ? (
          <PaymentLoadError message={detail.error} onRetry={refreshDetail} />
        ) : !detail?.data ? (
          <p role="status" className="text-sm">
            正在加载案件详情…
          </p>
        ) : !matchingDetail ? (
          <p role="alert" className="text-sm text-[var(--cms-error)]">
            指定案件与当前视图或渠道范围不匹配。
          </p>
        ) : (
          <CaseDetail
            key={detail.data.id}
            detail={detail.data}
            roleScope={roleScope}
            canWriteCases={canWriteCases}
            canReadOrders={canReadOrders}
            canReadReconciliation={canReadReconciliation}
            canReadRefunds={canReadRefunds}
            canReadDisputes={canReadDisputes}
            refreshing={detail.refreshing}
            onRefresh={refreshDetail}
            onDone={() => {
              refreshDetail();
              setRevision((value) => value + 1);
              setNotice('操作已受理。执行结果以案件状态为准。');
              window.showToast?.('操作已受理', 'info');
            }}
          />
        )}
      </CmsDialog>
    </PaymentTabs>
  );
}

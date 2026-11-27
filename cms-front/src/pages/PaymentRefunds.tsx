import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { History, ListChecks } from 'lucide-react';
import { Link, useSearchParams } from 'react-router-dom';
import CustomSelect from '../components/CustomSelect';
import { CmsDialog } from '../components/ui/CmsDialog';
import { Pagination } from '../components/ui/Pagination';
import {
  type FilterChip,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../components/ui/Primitives';
import { PaymentFilterBar, PaymentLoadError, PaymentScopeBar, PaymentTabs } from '../components/payments/PaymentWorkspace';
import { usePermission } from '../context/PermissionContext';
import { ApiError, apiRequest } from '../utils/api';
import {
  PAYMENT_PAGE_SIZE,
  paymentError,
  paymentGet,
  paymentMoney,
  paymentParams,
  paymentScope,
  paymentScopeLabel,
  paymentTime,
  usePaymentQuery,
  type PaymentScope,
} from '../utils/paymentWorkspace';

type Receipt = PaymentScope & {
  id: number;
  receipt_ref: string;
  currency: string;
  amount_minor: number;
  available_minor: number;
  refunded_minor: number;
  reserved_minor: number;
  refund_facts: { ref: string; amount_minor: string }[];
  funds_entitlement: boolean;
  refundable: boolean;
  note: string;
};

type Refund = {
  id: string;
  status: keyof typeof statuses;
  amount_minor?: number | string;
  currency: string;
  result_note?: string;
  can_reconcile?: boolean;
  created_at: string;
  updated_at: string;
};
type QueueItem = Refund &
  PaymentScope & {
    order_id: string;
    receipt_id: string | null;
    receipt_ref: string | null;
    actor: string;
    provider_refund_id: string | null;
  };
type RefundDraft = { receipt_id: number; amount_minor: number; currency: string; request_key: string };
type RefundTab = 'queue' | 'history';

const tabs = [
  { value: 'queue', label: '退款待办', icon: <ListChecks size={14} aria-hidden="true" /> },
  { value: 'history', label: '退款记录', icon: <History size={14} aria-hidden="true" /> },
] as const;
const statuses = {
  pending: { label: '等待处理', tone: 'muted' },
  confirming: { label: '渠道确认中', tone: 'warning' },
  review: { label: '待财务核对', tone: 'warning' },
  succeeded: { label: '退款成功', tone: 'success' },
  failed: { label: '退款失败', tone: 'error' },
} as const;
const inProgress = new Set(['pending', 'confirming', 'review']);

function amountInMinorUnits(text: string) {
  if (!/^\d+(\.\d{1,2})?$/.test(text)) return 0;
  const [whole, fraction = ''] = text.split('.');
  const minor = Number(whole) * 100 + Number(fraction.padEnd(2, '0'));
  return Number.isSafeInteger(minor) ? minor : 0;
}

function RefundReceipt({
  actor,
  order,
  receipt,
  canWriteRefunds,
  canReadDisputes,
  balanceRefreshing,
  onUpdated,
}: {
  actor: string;
  order: string;
  receipt: Receipt;
  canWriteRefunds: boolean;
  canReadDisputes: boolean;
  balanceRefreshing: boolean;
  onUpdated: () => void;
}) {
  const [amount, setAmount] = useState('');
  const [confirm, setConfirm] = useState<RefundDraft>();
  const [reconciliation, setReconciliation] = useState<{ id: string; ref: string; reason: string }>();
  const [submission, setSubmission] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);
  const lock = useRef(false);
  const storageKey = `cms-refund:${actor}:${receipt.id}`;
  const [draft, setDraft] = useState<RefundDraft | undefined>(() => {
    try {
      const saved: RefundDraft = JSON.parse(localStorage.getItem(storageKey) || 'null');
      if (
        saved?.receipt_id === receipt.id &&
        saved.currency === receipt.currency &&
        typeof saved.request_key === 'string'
      )
        return saved;
    } catch {
      /* Server history remains available. */
    }
    return undefined;
  });
  const history = usePaymentQuery<{ refunds: Refund[] }>(`/payments/refunds?receipt_id=${receipt.id}`, revision);
  const operation = usePaymentQuery<Refund>(
    draft ? `/payments/refunds/request/${encodeURIComponent(draft.request_key)}` : '',
    revision,
  );
  const tracked = operation?.data;
  const activeRefund = history?.data?.refunds.some((item) => inProgress.has(item.status));

  function forgetDraft() {
    setDraft(undefined);
    try {
      localStorage.removeItem(storageKey);
    } catch {
      /* Server history remains available. */
    }
  }

  useEffect(() => {
    if (!activeRefund && (!tracked || !inProgress.has(tracked.status))) return;
    const timer = window.setTimeout(() => {
      setRevision((value) => value + 1);
      onUpdated();
    }, 5000);
    return () => window.clearTimeout(timer);
  }, [activeRefund, tracked, onUpdated]);

  function refresh() {
    setRevision((value) => value + 1);
    onUpdated();
  }

  async function submit(request: RefundDraft, retry: boolean) {
    if (!canWriteRefunds || lock.current) return;
    lock.current = true;
    setBusy(true);
    setError('');
    try {
      if (retry) {
        try {
          const existing = await paymentGet<Refund>(
            `/payments/refunds/request/${encodeURIComponent(request.request_key)}`,
          );
          setSubmission(statuses[existing.status].label);
          refresh();
          return;
        } catch (failure) {
          if (!(failure instanceof ApiError && failure.status === 404)) throw failure;
        }
      }
      setDraft(request);
      try {
        localStorage.setItem(storageKey, JSON.stringify(request));
      } catch {
        /* Retain the same key in this session. */
      }
      const command: Refund = await apiRequest('POST', '/payments/refunds', request);
      setSubmission(`操作 ${command.id} · ${statuses[command.status].label}`);
      if (!inProgress.has(command.status)) forgetDraft();
      setAmount('');
      refresh();
    } catch (failure) {
      if (!retry && failure instanceof ApiError && [400, 403, 409].includes(failure.status)) {
        forgetDraft();
        setError(paymentError(failure));
        refresh();
      } else {
        setSubmission('提交结果待确认。请查询原命令，或按原参数重试。');
      }
    } finally {
      setConfirm(undefined);
      setBusy(false);
      lock.current = false;
    }
  }

  async function reconcile() {
    if (!canWriteRefunds || !reconciliation || lock.current) return;
    lock.current = true;
    setBusy(true);
    setError('');
    try {
      await apiRequest('POST', `/payments/refunds/${encodeURIComponent(reconciliation.id)}/reconcile-paypal`, {
        refund_ref: reconciliation.ref.trim(),
        reason: reconciliation.reason.trim(),
      });
      setReconciliation(undefined);
      setSubmission('已关联 PayPal 退款凭证。');
      refresh();
    } catch (failure) {
      setError(paymentError(failure));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }

  const requestedMinor = amountInMinorUnits(amount);
  const terminal = tracked && !inProgress.has(tracked.status);
  const canRefund =
    Number.isSafeInteger(receipt.id) &&
    receipt.refundable &&
    ['paypal', 'stripe'].includes(receipt.provider) &&
    ['live', 'sandbox'].includes(receipt.environment) &&
    requestedMinor > 0 &&
    requestedMinor <= receipt.available_minor &&
    Boolean(history?.data) &&
    !balanceRefreshing &&
    !draft &&
    !activeRefund;
  const scopeLabel = paymentScopeLabel(receipt);
  return (
    <div className="space-y-4 text-sm">
      <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {(
          [
            ['原收款', receipt.amount_minor],
            ['已退', receipt.refunded_minor],
            ['预留', receipt.reserved_minor],
            ['当前可退', receipt.available_minor],
          ] as const
        ).map(([label, value]) => (
          <div key={label}>
            <dt className="text-xs text-[var(--cms-muted-fg)]">{label}</dt>
            <dd className="mt-1 font-medium tabular-nums">{paymentMoney(value, receipt.currency)}</dd>
          </div>
        ))}
      </dl>
      <p className="text-xs text-[var(--cms-muted-fg)]">
        {receipt.funds_entitlement ? '此收款用于履约' : '此收款未用于履约'}
        {receipt.note ? ` · ${receipt.note}` : ''}
      </p>
      {canReadDisputes && receipt.environment && (
        <Link
          className="btn-detail"
          to={`/payments/disputes?${paymentParams({ provider: receipt.provider, environment: receipt.environment, account_id: receipt.account_id, order_id: order, receipt: receipt.receipt_ref })}`}
        >
          查看关联争议与冲回
        </Link>
      )}
      {canWriteRefunds ? (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (canRefund && !busy)
              setConfirm({
                receipt_id: receipt.id,
                amount_minor: requestedMinor,
                currency: receipt.currency,
                request_key: crypto.randomUUID(),
              });
          }}
        >
          <label className="grid gap-1 text-xs">
            退款金额（{receipt.currency}）
            <input
              className="input-field"
              inputMode="decimal"
              value={amount}
              disabled={!!draft || busy}
              onChange={(event) => setAmount(event.target.value)}
            />
          </label>
          <button type="submit" className="btn-primary" disabled={!canRefund || busy}>
            发起退款
          </button>
          <button type="button" className="btn-secondary" disabled={busy} onClick={refresh}>
            刷新状态
          </button>
          {draft && !tracked && (
            <button
              type="button"
              className="btn-secondary"
              disabled={busy || !operation}
              onClick={() => void submit(draft, true)}
            >
              重试同一请求
            </button>
          )}
          {terminal && (
            <button type="button" className="btn-secondary" onClick={forgetDraft}>
              结束跟踪
            </button>
          )}
        </form>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <button type="button" className="btn-secondary" disabled={busy} onClick={refresh}>
            刷新状态
          </button>
        </div>
      )}
      {error && !reconciliation && (
        <p role="alert" className="text-[var(--cms-error)]">
          {error}
        </p>
      )}
      <p role="status" className="text-xs text-[var(--cms-muted-fg)]">
        {tracked
          ? `操作 ${tracked.id} · ${statuses[tracked.status].label}`
          : submission ||
            (draft ? '正在核对原退款请求，确认前暂停新申请。' : activeRefund ? '原款有在途或待核对的退款。' : '')}
      </p>
      {history?.error ? (
        <PaymentLoadError message={history.error} onRetry={refresh} />
      ) : (
        <TableRegion ariaLabel="退款操作记录">
          <table className="cms-table" aria-busy={!history}>
            <thead>
              <tr>
                <th scope="col">操作编号 / 时间 UTC</th>
                <th scope="col" className="text-right">
                  金额
                </th>
                <th scope="col">状态</th>
                <th scope="col">操作</th>
              </tr>
            </thead>
            <tbody>
              {!history ? (
                <SkeletonRows rows={2} cols={4} />
              ) : history.data?.refunds.length ? (
                history.data.refunds.map((refund) => (
                  <tr key={refund.id}>
                    <td>
                      <span className="font-mono">{refund.id}</span>
                      <div className="text-[var(--cms-muted-fg)]">{paymentTime(refund.created_at)}</div>
                    </td>
                    <td className="text-right tabular-nums">{paymentMoney(refund.amount_minor, refund.currency)}</td>
                    <td>
                      <StatusDot {...statuses[refund.status]} />
                      {refund.result_note && <div>{refund.result_note}</div>}
                    </td>
                    <td>
                      {canWriteRefunds && receipt.provider === 'paypal' && refund.can_reconcile && (
                        <button
                          type="button"
                          className="btn-detail"
                          disabled={busy}
                          onClick={() => {
                            setError('');
                            setReconciliation({ id: refund.id, ref: '', reason: '' });
                          }}
                        >
                          关联已确认退款
                        </button>
                      )}
                    </td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={4} className="text-center">
                    <p role="status" className="text-[11px] text-[var(--cms-muted-fg)]">
                      暂无退款操作记录
                    </p>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </TableRegion>
      )}
      {!!receipt.refund_facts?.length && (
        <div>
          <h3 className="mb-2 text-sm font-semibold">已确认渠道退款</h3>
          <TableRegion ariaLabel="已确认渠道退款">
            <table className="cms-table">
              <thead>
                <tr>
                  <th scope="col">渠道退款编号</th>
                  <th scope="col" className="text-right">
                    金额
                  </th>
                </tr>
              </thead>
              <tbody>
                {receipt.refund_facts.map((item) => (
                  <tr key={item.ref}>
                    <td className="font-mono">{item.ref}</td>
                    <td className="text-right tabular-nums">{paymentMoney(item.amount_minor, receipt.currency)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableRegion>
        </div>
      )}
      <CmsDialog
        open={Boolean(confirm)}
        onClose={() => setConfirm(undefined)}
        title="确认退款"
        subtitle={scopeLabel}
        closedBy="closerequest"
        closeDisabled={busy}
        footer={
          <>
            <button
              type="button"
              className="btn-secondary"
              data-dialog-initial-focus
              disabled={busy}
              onClick={() => setConfirm(undefined)}
            >
              取消
            </button>
            <button
              type="button"
              className="btn-primary"
              disabled={busy}
              onClick={() => confirm && void submit(confirm, false)}
            >
              {busy ? '提交中…' : '确认提交'}
            </button>
          </>
        }
      >
        {confirm && (
          <p className="text-sm">
            订单 #{order} · 原收款 {receipt.receipt_ref || `#${receipt.id}`} · 申请退款{' '}
            {paymentMoney(confirm.amount_minor, confirm.currency)}
          </p>
        )}
      </CmsDialog>
      <CmsDialog
        open={Boolean(reconciliation)}
        onClose={() => setReconciliation(undefined)}
        title="核对 PayPal 退款"
        subtitle={scopeLabel}
        closedBy="closerequest"
        closeDisabled={busy}
      >
        {reconciliation && (
          <form
            className="space-y-3 text-sm"
            onSubmit={(event) => {
              event.preventDefault();
              void reconcile();
            }}
          >
            <p>原收款 {receipt.receipt_ref || `#${receipt.id}`}。提交会关联已确认凭证。</p>
            <label className="grid gap-1">
              PayPal 退款编号
              <input
                required
                maxLength={100}
                className="input-field"
                value={reconciliation.ref}
                onChange={(event) => setReconciliation({ ...reconciliation, ref: event.target.value })}
              />
            </label>
            <label className="grid gap-1">
              核对依据
              <textarea
                required
                maxLength={500}
                className="input-field"
                value={reconciliation.reason}
                onChange={(event) => setReconciliation({ ...reconciliation, reason: event.target.value })}
              />
            </label>
            {error && (
              <p role="alert" className="text-[var(--cms-error)]">
                {error}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <button
                type="button"
                className="btn-secondary"
                data-dialog-initial-focus
                disabled={busy}
                onClick={() => setReconciliation(undefined)}
              >
                取消
              </button>
              <button
                type="submit"
                className="btn-primary"
                disabled={busy || !reconciliation.ref.trim() || !reconciliation.reason.trim()}
              >
                确认关联
              </button>
            </div>
          </form>
        )}
      </CmsDialog>
    </div>
  );
}

function RefundDrawer({
  actor,
  order,
  params,
  canWriteRefunds,
  canReadDisputes,
  onClose,
  onUpdated,
  onSelect,
}: {
  actor: string;
  order: string;
  params: URLSearchParams;
  canWriteRefunds: boolean;
  canReadDisputes: boolean;
  onClose: () => void;
  onUpdated: () => void;
  onSelect: (receipt: string) => void;
}) {
  const [revision, setRevision] = useState(0);
  const pin = params.get('receipt_id');
  const legacy = params.get('legacy_command');
  const source = usePaymentQuery<{ receipts: Receipt[] }>(
    legacy ? '' : `/payments/orders/${encodeURIComponent(order)}/receipts?${paymentParams({ receipt_id: pin })}`,
    revision,
    true,
  );
  const candidates =
    source?.data?.receipts.filter((receipt) =>
      (['provider', 'environment', 'account_id'] as const).every((key) => {
        const expected = params.get('target_' + key) ?? params.get(key);
        return !expected || String(receipt[key] ?? '') === expected;
      }),
    ) ?? [];
  const receipt = pin
    ? candidates.find((item) => String(item.id) === pin)
    : candidates.length === 1
      ? candidates[0]
      : undefined;

  const refreshReceipt = useCallback(() => {
    setRevision((value) => value + 1);
    onUpdated();
  }, [onUpdated]);

  return (
    <CmsDialog
      open
      onClose={onClose}
      title={`订单 #${order} · 退款`}
      subtitle={receipt ? paymentScopeLabel(receipt) : undefined}
      variant="drawer"
      size="lg"
    >
      <div className="space-y-4">
        {legacy ? (
          <p className="text-sm" role="status">
            操作 {legacy} 尚未关联原收款。请先核实旧退款凭证。
          </p>
        ) : source?.error ? (
          <PaymentLoadError message={source.error} onRetry={() => setRevision((value) => value + 1)} />
        ) : !source?.data ? (
          <p role="status" className="text-sm">
            正在查询原收款…
          </p>
        ) : pin && !receipt ? (
          <p role="alert" className="text-sm text-[var(--cms-error)]">
            指定原收款不存在或与链接的渠道、环境、商户不匹配。
          </p>
        ) : !candidates.length ? (
          <p role="status" className="text-sm">
            当前范围没有可查的原收款。
          </p>
        ) : (
          <>
            {pin ? (
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-sm">
                  原收款 <span className="font-mono">{receipt?.receipt_ref || `#${pin}`}</span>
                </p>
                <button type="button" className="btn-secondary" onClick={() => onSelect('')}>
                  查看本单其他收款
                </button>
              </div>
            ) : (
              <CustomSelect
                aria-label="原收款"
                width="100%"
                value={receipt ? String(receipt.id) : ''}
                options={[
                  { value: '', label: '请选择原收款' },
                  ...candidates.map((item) => ({
                    value: String(item.id),
                    label: `${item.receipt_ref || `#${item.id}`} · ${paymentScopeLabel(item)} · ${paymentMoney(item.amount_minor, item.currency)}`,
                  })),
                ]}
                onChange={onSelect}
              />
            )}
            {receipt && (
              <RefundReceipt
                key={receipt.id}
                actor={actor}
                order={order}
                receipt={receipt}
                canWriteRefunds={canWriteRefunds}
                canReadDisputes={canReadDisputes}
                balanceRefreshing={Boolean(source.refreshing)}
                onUpdated={refreshReceipt}
              />
            )}
          </>
        )}
      </div>
    </CmsDialog>
  );
}

export function PaymentRefunds({ actor }: { actor?: string }) {
  const { admin, canRead, canWrite } = usePermission();
  const effectiveActor = actor || admin?.id || '';
  const canWriteRefunds = canWrite('payments.refunds');
  const canReadDisputes = canRead('payments.disputes');
  const searchFormId = useId();
  const [params, setParams] = useSearchParams();
  const [search, setSearch] = useState('');
  const [searchField, setSearchField] = useState<'order' | 'command'>('order');
  const [error, setError] = useState('');
  const [density, setDensity] = useState<TableDensity>('comfortable');
  const [lookupDialogOpen, setLookupDialogOpen] = useState(false);
  const [lookupOrderInput, setLookupOrderInput] = useState('');
  const [lookupOrderError, setLookupOrderError] = useState('');
  const [revision, setRevision] = useState(0);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  const triggerRef = useRef<HTMLElement | null>(null);
  const scope = paymentScope(params);
  const page = Math.max(1, Number(params.get('page')) || 1);
  const order = params.get('order_id') ?? '';
  const queueOrder = params.get('queue_order_id') ?? '';
  const command = params.get('command_id') ?? '';
  const view: RefundTab = command || params.get('tab') === 'history' ? 'history' : 'queue';
  const status = params.get('status') ?? '';
  useEffect(() => {
    if (!order) triggerRef.current?.focus();
  }, [order]);
  const result = usePaymentQuery<{ total: number; items: QueueItem[] }>(
    `/payments/refunds/queue?${paymentParams({
      ...scope,
      page: String(page),
      attention: view === 'queue' ? 'true' : '',
      status,
      order_id: queueOrder,
      command_id: command,
    })}`,
    revision,
  );

  function update(fields: Record<string, string | undefined>) {
    const next = new URLSearchParams(params);
    for (const [key, value] of Object.entries(fields)) {
      if (value) next.set(key, value);
      else next.delete(key);
    }
    setParams(next);
  }

  function openOrderReceipts(orderID: string) {
    update({
      ...scope,
      order_id: orderID,
      receipt_id: '',
      legacy_command: '',
      target_provider: '',
      target_environment: '',
      target_account_id: '',
    });
  }

  function startLookup() {
    const value = search.trim();
    if (!value) {
      setError('');
      update({ queue_order_id: '', command_id: '', page: '' });
      return;
    }
    if (searchField === 'command') {
      if (!/^[\da-f]{8}(?:-[\da-f]{4}){3}-[\da-f]{12}$/i.test(value)) {
        setError('请输入完整的退款操作号。');
        return;
      }
      setError('');
      update({
        command_id: value.toLowerCase(),
        queue_order_id: '',
        tab: 'history',
        status: '',
        page: '',
        order_id: '',
        receipt_id: '',
        legacy_command: '',
      });
      return;
    }
    if (!/^\d+$/.test(value) || /^0+$/.test(value)) {
      setError('请输入有效的订单号。');
      return;
    }
    setError('');
    const orderID = value.replace(/^0+/, '');
    update({ queue_order_id: orderID, command_id: '', page: '' });
  }

  const hasAnyFilter = Boolean(search || queueOrder || command || status);
  const chips: FilterChip[] = [
    ...(queueOrder
      ? [
          {
            key: 'queue_order_id',
            label: `订单号: ${queueOrder}`,
            onClear: () => {
              if (searchField === 'order') {
                setSearch('');
                setError('');
              }
              update({ queue_order_id: '', page: '' });
            },
          },
        ]
      : []),
    ...(status && statuses[status as keyof typeof statuses]
      ? [
          {
            key: 'status',
            label: `状态: ${statuses[status as keyof typeof statuses].label}`,
            onClear: () => update({ status: '', page: '' }),
          },
        ]
      : []),
    ...(command
      ? [
          {
            key: 'command_id',
            label: `退款操作号: ${command}`,
            onClear: () => {
              if (searchField === 'command') {
                setSearch('');
                setError('');
              }
              update({ command_id: '', page: '' });
            },
          },
        ]
      : []),
  ];

  function resetFilters() {
    setSearch('');
    setError('');
    update({ status: '', command_id: '', queue_order_id: '', page: '' });
  }

  return (
    <PaymentTabs
      tabs={tabs}
      value={view}
      label="退款视图"
      onChange={(tab) => {
        setSearch('');
        setError('');
        update({ tab, status: '', command_id: '', page: '' });
      }}
    >
      <PaymentScopeBar
        scope={scope}
        onChange={(next) => update({ ...next, page: '', order_id: '', receipt_id: '', legacy_command: '' })}
      />
      <PaymentFilterBar
        error={error}
        chips={chips}
        onResetAll={resetFilters}
        right={
          <>
            {result?.data && <span className="tabular-nums">共 {result.data.total} 笔</span>}
            <ViewOptionsPopover density={density} onDensityChange={setDensity} />
            <button
              type="button"
              className="btn-primary h-9 px-3 text-xs"
              onClick={(event) => {
                triggerRef.current = event.currentTarget;
                const candidate = (searchField === 'order' ? search.trim() : '') || queueOrder;
                if (candidate && /^\d+$/.test(candidate) && !/^0+$/.test(candidate)) {
                  openOrderReceipts(candidate.replace(/^0+/, ''));
                  return;
                }
                setLookupOrderInput(candidate || '');
                setLookupOrderError('');
                setLookupDialogOpen(true);
              }}
            >
              查原收款
            </button>
          </>
        }
      >
        <form
          id={searchFormId}
          aria-label="查询退款"
          className="flex items-center gap-1.5 w-full sm:w-auto"
          onSubmit={(event) => {
            event.preventDefault();
            startLookup();
          }}
        >
          <CustomSelect
            aria-label="退款查询字段"
            width="128px"
            value={searchField}
            options={[
              { value: 'order', label: '订单号' },
              { value: 'command', label: '退款操作号' },
            ]}
            onChange={(value) => {
              setSearchField(value === 'command' ? 'command' : 'order');
              setSearch('');
              setError('');
            }}
          />
          <SearchInput
            className="flex-1 sm:w-56"
            value={search}
            onChange={(value) => {
              setSearch(value);
              setError('');
            }}
            onClear={() => {
              setSearch('');
              setError('');
              if (searchField === 'order' && queueOrder) update({ queue_order_id: '', page: '' });
              else if (searchField === 'command' && command) update({ command_id: '', page: '' });
            }}
            placeholder={searchField === 'order' ? '输入订单号...' : '输入退款操作号...'}
          />
        </form>
        <CustomSelect
          aria-label="退款状态"
          width="140px"
          value={status}
          options={[
            { value: '', label: '全部状态' },
            ...Object.entries(statuses)
              .filter(([value]) => view === 'history' || inProgress.has(value))
              .map(([value, entry]) => ({ value, label: entry.label })),
          ]}
          onChange={(value) => update({ status: value, page: '' })}
        />
        <button type="submit" form={searchFormId} className="btn-secondary h-9 px-3 text-xs font-normal whitespace-nowrap">
          查询
        </button>
        {hasAnyFilter && (
          <button type="button" className="btn-ghost h-9 text-xs text-[var(--cms-muted-fg)]" onClick={resetFilters}>
            重置
          </button>
        )}
      </PaymentFilterBar>
      {result?.error ? (
        <PaymentLoadError message={result.error} onRetry={() => setRevision((value) => value + 1)} />
      ) : (
        <TableRegion
          ariaLabel="退款队列"
          footer={
            result?.data && (
              <Pagination
                page={page}
                pageSize={PAYMENT_PAGE_SIZE}
                total={result.data.total}
                onPageChange={(value) => update({ page: String(value) })}
              />
            )
          }
        >
          <table className="cms-table" data-density={density} aria-busy={!result}>
            <thead>
              <tr>
                <th scope="col">订单 / 原收款</th>
                <th scope="col">渠道账户</th>
                <th scope="col">退款操作 / 状态</th>
                <th scope="col" className="text-right">
                  退款金额
                </th>
                <th scope="col">更新时间 UTC</th>
                <th scope="col">操作</th>
              </tr>
            </thead>
            <tbody>
              {!result ? (
                <SkeletonRows rows={4} cols={6} />
              ) : result.data?.items.length ? (
                result.data.items.map((item) => (
                  <tr key={item.id}>
                    <td>
                      <strong>#{item.order_id}</strong>
                      <div className="font-mono">{item.receipt_ref || '原收款待核对'}</div>
                    </td>
                    <td>{paymentScopeLabel(item)}</td>
                    <td>
                      <StatusDot {...statuses[item.status]} />
                      <div className="font-mono break-all">{item.id}</div>
                      {item.provider_refund_id && (
                        <div className="text-[var(--cms-muted-fg)] break-all">渠道退款 {item.provider_refund_id}</div>
                      )}
                    </td>
                    <td className="text-right tabular-nums">{paymentMoney(item.amount_minor, item.currency)}</td>
                    <td>
                      {paymentTime(item.updated_at)}
                      {item.actor && <div className="text-[var(--cms-muted-fg)]">{item.actor}</div>}
                    </td>
                    <td>
                      <button
                        type="button"
                        className="btn-detail"
                        onClick={(event) => {
                          triggerRef.current = event.currentTarget;
                          update({
                            target_provider: item.provider || scope.provider,
                            target_environment: item.environment,
                            target_account_id: item.account_id || '',
                            order_id: item.order_id,
                            receipt_id: item.receipt_id || '',
                            legacy_command: item.receipt_id ? '' : item.id,
                          });
                        }}
                      >
                        详情
                      </button>
                    </td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={6} className="text-center">
                    <p role="status" className="text-[11px] text-[var(--cms-muted-fg)]">
                      {view === 'queue' ? '当前范围没有匹配的退款待办。' : '当前范围没有匹配的退款记录。'}
                    </p>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </TableRegion>
      )}
      {order && (
        <RefundDrawer
          key={`${order}/${params.get('receipt_id') ?? ''}/${scope.provider}/${scope.environment}/${scope.account_id}`}
          actor={effectiveActor}
          order={order}
          params={params}
          canWriteRefunds={canWriteRefunds}
          canReadDisputes={canReadDisputes}
          onSelect={(receipt_id) => update({ receipt_id })}
          onClose={() =>
            update({
              order_id: '',
              receipt_id: '',
              legacy_command: '',
              target_provider: '',
              target_environment: '',
              target_account_id: '',
            })
          }
          onUpdated={refresh}
        />
      )}
      <CmsDialog
        open={lookupDialogOpen}
        onClose={() => setLookupDialogOpen(false)}
        title="按订单查原收款"
        subtitle={canWriteRefunds ? '输入数字订单号以查看原收款并发起退款' : '输入数字订单号以查看原收款与退款记录'}
        size="sm"
      >
        <form
          className="space-y-3 text-sm"
          onSubmit={(event) => {
            event.preventDefault();
            const value = lookupOrderInput.trim();
            if (!/^\d+$/.test(value) || /^0+$/.test(value)) {
              setLookupOrderError('请输入有效的订单号。');
              return;
            }
            setLookupOrderError('');
            setLookupDialogOpen(false);
            openOrderReceipts(value.replace(/^0+/, ''));
          }}
        >
          <label className="grid gap-1 text-xs">
            <span className="text-[var(--cms-muted-fg)]">订单号</span>
            <input
              type="text"
              inputMode="numeric"
              className="input-field tabular-nums"
              placeholder="输入数字订单号..."
              data-dialog-initial-focus
              value={lookupOrderInput}
              onChange={(event) => {
                setLookupOrderInput(event.target.value);
                setLookupOrderError('');
              }}
            />
          </label>
          {lookupOrderError && (
            <p role="alert" className="text-xs text-[var(--cms-error)] m-0">
              {lookupOrderError}
            </p>
          )}
          <div className="flex justify-end gap-2 pt-1">
            <button type="button" className="btn-secondary" onClick={() => setLookupDialogOpen(false)}>
              取消
            </button>
            <button type="submit" className="btn-primary">
              查看原收款
            </button>
          </div>
        </form>
      </CmsDialog>
    </PaymentTabs>
  );
}

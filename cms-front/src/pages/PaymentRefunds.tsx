import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { ApiError, apiRequest } from '../utils/api';
import { CmsDialog } from '../components/ui/CmsDialog';
import { SkeletonRows, StatusDot, TableRegion } from '../components/ui/Primitives';

type Receipt = {
  id: number;
  provider: string;
  environment?: string;
  currency: string;
  amount_minor: number;
  available_minor: number;
  funds_entitlement: boolean;
  refundable: boolean;
  note: string;
};

type Refund = {
  id: string;
  status: 'pending' | 'confirming' | 'review' | 'succeeded' | 'failed';
  amount_minor?: number;
  currency: string;
  result_note?: string;
  can_reconcile?: boolean;
};

type RefundDraft = { receipt_id: number; amount_minor: number; currency: string; request_key: string };

const statuses = {
  pending: { label: '等待处理', tone: 'muted' },
  confirming: { label: '渠道确认中', tone: 'warning' },
  review: { label: '待财务核对', tone: 'warning' },
  succeeded: { label: '退款成功', tone: 'success' },
  failed: { label: '退款失败', tone: 'error' },
} as const;

function amountInMinorUnits(text: string) {
  if (!/^\d+(\.\d{1,2})?$/.test(text)) return 0;
  const [whole, fraction = ''] = text.split('.');
  const minor = Number(whole) * 100 + Number(fraction.padEnd(2, '0'));
  return Number.isSafeInteger(minor) ? minor : 0;
}

const money = (minor: number, currency: string) => `${(minor / 100).toFixed(2)} ${currency}`;
const errorText = (error: unknown) => error instanceof Error ? error.message : '退款查询失败，请重试。';

export function PaymentRefunds({ actor }: { actor: string }) {
  const [params, setParams] = useSearchParams();
  const order = params.get('order_id') || '';
  const selected = params.get('receipt_id') || '';
  const [orderInput, setOrderInput] = useState(order);
  const [receipts, setReceipts] = useState<Receipt[] | null>(null);
  const [refunds, setRefunds] = useState<Refund[] | null>(null);
  const [selectedReceiptID, setSelectedReceiptID] = useState<number | null>(null);
  const [amount, setAmount] = useState('');
  const [confirm, setConfirm] = useState<RefundDraft | null>(null);
  const [reconciliation, setReconciliation] = useState<{ id: string; refundRef: string; reason: string } | null>(null);
  const [draft, setDraft] = useState<RefundDraft | null>(null);
  const [submission, setSubmission] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const inFlight = useRef(false);

  const activeReceipt = receipts?.find((item) => item.id === selectedReceiptID);
  const environment = activeReceipt?.environment || '';
  const draftKey = activeReceipt ? `cms-refund:${actor}:${activeReceipt.id}` : '';
  const activeReceiptID = activeReceipt?.id;
  const activeRefund = refunds?.some((item) => ['pending', 'confirming', 'review'].includes(item.status));
  const loadReceipts = useCallback(async (signal?: AbortSignal) => {
    if (!order) return;
    const result = await apiRequest('GET', `/payments/orders/${encodeURIComponent(order)}/receipts`, undefined, false, signal);
    if (signal?.aborted) return;
    const items: Receipt[] = result.receipts || [];
    setReceipts(items);
    setSelectedReceiptID((current) => items.find((item) => item.id === current)?.id ??
      items.find((item) => String(item.id) === selected)?.id ??
      items.find((item) => item.funds_entitlement)?.id ?? items[0]?.id ?? null);
  }, [order, selected]);

  useEffect(() => {
    const controller = new AbortController();
    setReceipts(null);
    setError('');
    void loadReceipts(controller.signal).catch((failure) => {
      if (!controller.signal.aborted) setError(errorText(failure));
    });
    return () => controller.abort();
  }, [loadReceipts]);

  const dropDraft = useCallback(() => {
    setDraft(null);
    try { localStorage.removeItem(draftKey); } catch { /* Continue with server history. */ }
  }, [draftKey]);

  const readOperation = useCallback(async (request: RefundDraft, signal?: AbortSignal) => {
    try {
      const command: Refund = await apiRequest('GET', `/payments/refunds/request/${request.request_key}`, undefined, false, signal);
      if (signal?.aborted) return undefined;
      if (command.status === 'succeeded' || command.status === 'failed') dropDraft();
      setSubmission(statuses[command.status].label);
      return command;
    } catch (failure) {
      if (signal?.aborted) return undefined;
      if (!(failure instanceof ApiError && failure.status === 404)) throw failure;
      setSubmission('暂未查到原命令。如需重试，将使用原请求和金额。');
      return undefined;
    }
  }, [dropDraft]);

  const loadRefunds = useCallback(async (signal?: AbortSignal) => {
    if (!activeReceiptID) return;
    const result = await apiRequest('GET', `/payments/refunds?receipt_id=${activeReceiptID}`, undefined, false, signal);
    if (!signal?.aborted) setRefunds(result.refunds || []);
  }, [activeReceiptID]);

  useEffect(() => {
    if (!activeReceiptID || !draftKey) return;
    const controller = new AbortController();
    setAmount('');
    setSubmission('');
    setRefunds(null);
    setDraft(null);
    let saved: RefundDraft | null = null;
    try {
      const value = JSON.parse(localStorage.getItem(draftKey) || 'null');
      if (value?.receipt_id === activeReceiptID && typeof value.request_key === 'string') saved = value;
    } catch { /* Continue with server history. */ }
    if (saved) setDraft(saved);
    void loadRefunds(controller.signal).then(() => {
      if (saved && !controller.signal.aborted) return readOperation(saved, controller.signal);
    }).catch((failure) => {
      if (!controller.signal.aborted) setError(errorText(failure));
    });
    return () => controller.abort();
  }, [activeReceiptID, draftKey, loadRefunds, readOperation]);

  const refresh = async () => {
    try {
      await Promise.all([loadReceipts(), loadRefunds(), draft ? readOperation(draft) : undefined]);
    } catch (failure) {
      setError(errorText(failure));
    }
  };

  const submit = async (request: RefundDraft, retry: boolean) => {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError('');
    try {
      if (retry) {
        const existing = await readOperation(request);
        if (existing) {
          await loadRefunds();
          return;
        }
      }
      setDraft(request);
      try { localStorage.setItem(draftKey, JSON.stringify(request)); } catch { /* The current form still has the original key. */ }
      const command: Refund = await apiRequest('POST', '/payments/refunds', request);
      setSubmission(statuses[command.status].label);
      if (command.status === 'succeeded' || command.status === 'failed') dropDraft();
      setConfirm(null);
      await Promise.all([loadReceipts(), loadRefunds()]);
    } catch (failure) {
      setConfirm(null);
      if (failure instanceof ApiError && [400, 403, 409].includes(failure.status) && !retry) {
        dropDraft();
        setError(errorText(failure));
      } else {
        setSubmission('提交结果待确认。请查询原命令或重试同一请求。');
      }
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };

  const requestedMinor = amountInMinorUnits(amount);
  const reconcilePayPal = async () => {
    if (!reconciliation || inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError('');
    try {
      await apiRequest('POST', `/payments/refunds/${encodeURIComponent(reconciliation.id)}/reconcile-paypal`, {
        refund_ref: reconciliation.refundRef.trim(), reason: reconciliation.reason.trim(),
      });
      setReconciliation(null);
      setSubmission('已关联 PayPal 退款凭证。');
      await refresh();
    } catch (failure) {
      setError(errorText(failure));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };

  const canRequest = !!activeReceipt?.refundable && requestedMinor > 0 && requestedMinor <= activeReceipt.available_minor &&
    refunds !== null && !draft && !activeRefund;

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">退款</h1>
      <form className="flex flex-wrap items-end gap-2" onSubmit={(event) => {
        event.preventDefault();
        setParams({ order_id: orderInput.trim() });
      }}>
        <label className="grid gap-1 text-sm">原订单号
          <input className="input-field" value={orderInput} onChange={(event) => setOrderInput(event.target.value)}
            inputMode="numeric" pattern="[0-9]+" required />
        </label>
        <button type="submit" className="btn-primary" disabled={busy}>查收款</button>
      </form>
      {order && environment && <Link className="btn-detail" to={`/payments/disputes?${new URLSearchParams({ order_id: order, environment })}`}>关联争议与冲回</Link>}
      {activeReceipt && !environment && <p>收款环境待核对。</p>}
      {error && <p role="alert" className="text-[var(--cms-error)]">{error}</p>}
      {order && !receipts && !error && <p role="status">正在查询收款…</p>}
      {receipts?.length === 0 && <p role="status">该订单没有可查的收款凭证。</p>}
      {!!receipts?.length && (
        <label className="grid gap-1 max-w-md text-sm">原收款
          <select className="input-field" disabled={busy} value={selectedReceiptID || ''} onChange={(event) => {
            const id = Number(event.target.value);
            setSelectedReceiptID(id);
            setParams({ order_id: order, receipt_id: String(id) }, { replace: true });
          }}>
            {receipts.map((receipt) => <option key={receipt.id} value={receipt.id}>
              #{receipt.id} · {receipt.provider} · {money(receipt.amount_minor, receipt.currency)}
              {receipt.funds_entitlement ? ' · 履约收款' : ''}
            </option>)}
          </select>
        </label>
      )}
      {activeReceipt && (
        <>
          <p className="text-sm">当前可退 {money(activeReceipt.available_minor, activeReceipt.currency)}</p>
          {activeReceipt.note && <p className="text-sm text-[var(--cms-muted-fg)]">{activeReceipt.note}</p>}
          <div className="flex flex-wrap items-end gap-2">
            <label className="grid gap-1 text-sm">退款金额（{activeReceipt.currency}）
              <input className="input-field" inputMode="decimal" value={amount} disabled={!!draft || busy}
                onChange={(event) => setAmount(event.target.value)} />
            </label>
            <button type="button" className="btn-primary" disabled={!canRequest || busy} onClick={() => setConfirm({
              receipt_id: activeReceipt.id, amount_minor: requestedMinor, currency: activeReceipt.currency, request_key: crypto.randomUUID(),
            })}>发起退款</button>
            <button type="button" className="btn-secondary" disabled={busy} onClick={() => void refresh()}>刷新退款记录</button>
            {draft && <button type="button" className="btn-secondary" disabled={busy} onClick={() => void submit(draft, true)}>重试同一请求</button>}
          </div>
          <p role="status" className="text-sm">{submission || (activeRefund ? '原款有在途或待核对的退款。' : '')}</p>
          <TableRegion ariaLabel="退款操作记录">
            <table className="cms-table">
              <thead><tr><th>操作编号</th><th className="text-right">退款金额</th><th>状态</th><th>处理结果</th></tr></thead>
              <tbody>
                {!refunds ? <SkeletonRows rows={2} cols={4} /> : refunds.map((refund) => (
                  <tr key={refund.id}>
                    <td className="font-mono">{refund.id}</td><td className="text-right">{refund.amount_minor ? money(refund.amount_minor, refund.currency) : '—'}</td>
                    <td><StatusDot {...statuses[refund.status]} /></td>
                    <td>{refund.result_note || '—'}
                      {activeReceipt.provider === 'paypal' && refund.can_reconcile && (
                        <button type="button" className="btn-detail" disabled={busy}
                          onClick={() => setReconciliation({ id: refund.id, refundRef: '', reason: '' })}>关联已确认退款</button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableRegion>
          {refunds?.length === 0 && <p role="status">暂无退款记录。</p>}
        </>
      )}
      <CmsDialog open={reconciliation !== null} onClose={() => { if (!busy) setReconciliation(null); }}
        title="核对 PayPal 退款" closedBy="closerequest" closeDisabled={busy}>
        {reconciliation && (
          <form className="grid gap-3" onSubmit={(event) => { event.preventDefault(); void reconcilePayPal(); }}>
            <p>提交前，请在 PayPal 核实该退款编号对应此退款操作。存在同额退款时，请核对发起记录。</p>
            <label>PayPal 退款编号
              <input className="input-field" required maxLength={100} value={reconciliation.refundRef}
                onChange={(event) => setReconciliation({ ...reconciliation, refundRef: event.target.value })} />
            </label>
            <label>核对依据
              <textarea className="input-field" required maxLength={500} value={reconciliation.reason}
                onChange={(event) => setReconciliation({ ...reconciliation, reason: event.target.value })} />
            </label>
            {error && <p role="alert">{error}</p>}
            <button type="submit" className="btn-primary" disabled={busy || !reconciliation.refundRef.trim() || !reconciliation.reason.trim()}>
              确认关联
            </button>
          </form>
        )}
      </CmsDialog>
      <CmsDialog open={confirm !== null} onClose={() => { if (!busy) setConfirm(null); }} title="确认退款"
        closedBy="closerequest" closeDisabled={busy}
        footer={<>
          <button type="button" data-dialog-initial-focus className="btn-secondary" onClick={() => setConfirm(null)} disabled={busy}>取消</button>
          <button type="button" className="btn-primary" disabled={busy} onClick={() => confirm && void submit(confirm, false)}>
            {busy ? '提交中…' : '确认提交'}
          </button>
        </>}>
        {confirm && <p>订单 #{order} 的收款 #{confirm.receipt_id} 将申请退款 {money(confirm.amount_minor, confirm.currency)}。</p>}
      </CmsDialog>
    </div>
  );
}

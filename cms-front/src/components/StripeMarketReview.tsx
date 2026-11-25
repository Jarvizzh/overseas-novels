import { useEffect, useState } from 'react';
import { usePermission } from '../context/PermissionContext';
import { apiRequest } from '../utils/api';

type Review = {
  evidence_key: string;
  evidence: { reference: string; sales_mode: string; country: string; expected_country: string; tax_account: string; customer_tax_status: string };
  note?: string;
  approved_at?: string;
  approval_reason?: string;
  can_approve: boolean;
};

export function StripeMarketReview({ orderID }: { orderID: string | number }) {
  const { canWrite } = usePermission();
  const canApproveReview = canWrite('payments.orders');
  const [review, setReview] = useState<Review | null>(null);
  const [reason, setReason] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const path = '/payments/orders/' + encodeURIComponent(orderID) + '/stripe-market-review';

  useEffect(() => {
    const controller = new AbortController();
    void apiRequest('GET', path, undefined, false, controller.signal)
      .then((response) => { if (!controller.signal.aborted) setReview(response.review ?? null); })
      .catch(() => { if (!controller.signal.aborted) setError('Stripe 交易核对记录读取失败。'); });
    return () => controller.abort();
  }, [path]);

  if (!review) return error ? <p role="alert">{error}</p> : null;

  const approve = async () => {
    if (!canApproveReview || saving || !reason.trim()) return;
    setSaving(true);
    setError('');
    try {
      await apiRequest('POST', path, { evidence_key: review.evidence_key, reason: reason.trim() });
      const updated = await apiRequest('GET', path);
      setReview(updated.review ?? null);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : '处理失败。');
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="my-2 space-y-2 rounded border border-[var(--border-color)] p-3" aria-label="Stripe 市场与税务核对">
      <h3 className="font-medium">Stripe 市场与税务核对</h3>
      {review.note && <p>{review.note}</p>}
      <p>渠道凭证 {review.evidence.reference} · 实际地区 {review.evidence.country || '待确认'} · 预计地区 {review.evidence.expected_country || '待确认'}</p>
      <p>税务责任账户 {review.evidence.tax_account || '待确认'} · 客户税务状态 {review.evidence.customer_tax_status || '待确认'}</p>
      {review.approved_at && <p role="status">复核已批准。Reader 将重新核验款项及履约条件。</p>}
      {canApproveReview && review.can_approve && <form className="space-y-2" onSubmit={(event) => { event.preventDefault(); void approve(); }}>
        <label className="grid gap-1">核实依据、税务责任以及批准原因
          <textarea className="input-field" required maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} />
        </label>
        <button className="btn-primary" type="submit" disabled={saving}>{saving ? '提交中…' : '批准重新核验与履约'}</button>
      </form>}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

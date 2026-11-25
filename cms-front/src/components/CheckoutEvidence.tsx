import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { usePermission } from '../context/PermissionContext';
import { apiRequest } from '../utils/api';

type Checkout = {
  amount_minor: number;
  currency: string;
  environment?: 'live' | 'sandbox';
  fulfilled_at?: string;
  status: string;
  note?: string;
  attempts: { id: string; provider: string; sales_mode: string; environment: string; status: string; provider_order_id?: string }[];
  receipts: { id: string; provider: string; capture_id: string; status: string; currency: string; amount_minor: number; note?: string; funds_entitlement: boolean }[];
};

function formatAmount(minor: number, currency: string) {
  return `${(minor / 100).toFixed(2)} ${currency}`;
}

export function CheckoutEvidence({ orderID }: { orderID: string | number }) {
  const { canRead } = usePermission();
  const canReadRefunds = canRead('payments.refunds');
  const canReadDisputes = canRead('payments.disputes');
  const canReadReconciliation = canRead('payments.reconciliation');
  const [opened, setOpened] = useState(false);
  const [checkout, setCheckout] = useState<Checkout>();
  const [error, setError] = useState(false);
  const order = encodeURIComponent(orderID);

  useEffect(() => {
    if (!opened) return;
    const controller = new AbortController();
    void apiRequest('GET', `/payments/orders/${order}/checkout-evidence`, undefined, false, controller.signal)
      .then((response) => { if (!controller.signal.aborted) setCheckout(response.checkout); })
      .catch(() => { if (!controller.signal.aborted) setError(true); });
    return () => controller.abort();
  }, [opened, order]);

  return (
    <details className="my-3 rounded-xl border border-[var(--cms-border)] p-3 text-xs" onToggle={(event) => {
      setError(false);
      setOpened(event.currentTarget.open);
    }}>
      <summary className="cursor-pointer font-semibold">结账尝试与收款记录</summary>
      {opened && error && <p className="mt-2" role="alert">读取失败。展开面板可重新读取。</p>}
      {opened && !error && !checkout && <p className="mt-2" role="status">正在读取…</p>}
      {checkout && (
        <div className="mt-3 space-y-3">
          <p>{checkout.status} · {formatAmount(checkout.amount_minor, checkout.currency)} · {checkout.fulfilled_at ? '已履约' : '待履约'}</p>
          {checkout.note && <p>{checkout.note}</p>}
          <div>
            <h3 className="font-semibold">支付尝试</h3>
            {checkout.attempts.length === 0 ? <p>暂无尝试记录</p> : (
              <ul className="mt-1 space-y-1">
                {checkout.attempts.map((attempt) => (
                  <li className="break-all" key={attempt.id}>
                    {attempt.provider} · {attempt.sales_mode} · {attempt.environment} · {attempt.status}
                    {attempt.provider_order_id && ` · ${attempt.provider_order_id}`}
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div>
            <h3 className="font-semibold">收款凭证</h3>
            {checkout.receipts.length === 0 ? <p>暂无已登记收款</p> : (
              <ul className="mt-1 space-y-2">
                {checkout.receipts.map((receipt) => (
                  <li key={receipt.id} className="space-y-1 rounded-lg bg-[var(--cms-content)] p-2">
                    <p className="break-all">
                      {receipt.provider} · {receipt.capture_id} · {formatAmount(receipt.amount_minor, receipt.currency)} · {receipt.status}
                      {receipt.funds_entitlement && ' · 履约收款'}
                    </p>
                    {receipt.note && <p>{receipt.note}</p>}
                    {canReadRefunds && (
                      <Link className="btn-detail" to={`/payments/refunds?order_id=${order}&receipt_id=${encodeURIComponent(receipt.id)}${checkout.environment ? "&environment=" + checkout.environment : ""}`}>退款记录</Link>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </div>
          {(canReadDisputes || canReadReconciliation) && (
            <div className="flex flex-wrap gap-2">
              {checkout.environment ? <>
                {canReadDisputes && <Link className="btn-detail" to={`/payments/disputes?order_id=${order}&environment=${checkout.environment}`}>争议与冲回</Link>}
                {canReadReconciliation && <Link className="btn-detail" to={`/payments/reconciliation?order_id=${order}&environment=${checkout.environment}`}>流水与结算</Link>}
              </> : <span>渠道环境待核对</span>}
            </div>
          )}
        </div>
      )}
    </details>
  );
}

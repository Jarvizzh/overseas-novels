import { StripeMarketReview } from './StripeMarketReview';
import { CheckoutEvidence } from './CheckoutEvidence';
import React, { useState } from 'react';
import {
  AlertCircle,
  Check,
  Copy,
  CreditCard,
  DollarSign,
  Hash,
  RefreshCw,
  ShieldCheck,
  UserCheck,
} from 'lucide-react';
import { CmsDialog } from './ui/CmsDialog';
import { StatusDot } from './ui/Primitives';
import { copyTextToClipboard } from '../utils/clipboard';

export interface ThirdPartyPaymentDetail {
  id: number;
  order_id: number;
  payment_provider: string;
  external_order_id: string;
  capture_id: string;
  payer_id: string;
  payer_email: string;
  payer_name: string;
  payer_country: string;
  currency: string;
  gross_amount: number;
  fee_amount: number | null;
  net_amount: number | null;
  status: string;
  seller_protection_status: string;
  created_at: string;
  updated_at: string;
}

interface Props {
  isOpen: boolean;
  onClose: () => void;
  orderId: number | string | null;
  loading: boolean;
  details: ThirdPartyPaymentDetail | null;
  onRefresh?: () => void;
}

export const ThirdPartyPaymentModal: React.FC<Props> = ({
  isOpen,
  onClose,
  orderId,
  loading,
  details,
  onRefresh,
}) => {
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  const handleCopy = async (text: string, key: string) => {
    if (!text) return;
    const ok = await copyTextToClipboard(text);
    if (!ok) {
      window.showToast?.('复制失败，请手动选中复制', 'error');
      return;
    }
    setCopiedKey(key);
    window.setTimeout(() => setCopiedKey(null), 2000);
  };

  const providerLower = details?.payment_provider?.toLowerCase();
  const isPayPal = providerLower === 'paypal';
  const isStripe = providerLower === 'stripe';
  const isSuccess =
    details?.status === 'VERIFIED' ||
    details?.status === 'COMPLETED' ||
    details?.status === 'SUCCESS' ||
    details?.status === 'PAID' ||
    details?.status === 'CAPTURED';
  const isRefunded = details?.status === 'REFUNDED' || details?.status === 'PARTIALLY_REFUNDED';

  return (
    <CmsDialog
      open={isOpen}
      onClose={onClose}
      variant="drawer"
      title="第三方支付凭据详情"
      subtitle={orderId !== null ? `关联系统订单 ID：#${orderId}` : undefined}
      size="lg"
      headerExtra={
        onRefresh && (
          <button type="button" onClick={onRefresh} className="btn-secondary h-8 px-3 text-xs" title="重新拉取网关状态">
            <RefreshCw size={13} className={loading ? 'animate-spin' : ''} aria-hidden="true" />
            <span>刷新</span>
          </button>
        )
      }
      footer={
        <button type="button" onClick={onClose} className="btn-secondary">
          关闭
        </button>
      }
    >
      {isOpen && orderId !== null && <StripeMarketReview key={orderId} orderID={orderId} />}
      {isOpen && orderId !== null && <CheckoutEvidence key={`checkout-${orderId}`} orderID={orderId} />}
      {loading ? (
        <div className="py-16 text-center text-xs text-[var(--cms-muted-fg)]">正在请求第三方支付网关数据...</div>
      ) : !details ? (
        <div className="py-14 flex flex-col items-center gap-3 text-center">
          <div className="w-12 h-12 rounded-full bg-[var(--cms-content)] flex items-center justify-center text-[var(--cms-muted-fg)]">
            <AlertCircle size={24} aria-hidden="true" />
          </div>
          <div className="text-sm font-medium text-[var(--cms-fg)]">暂无第三方支付凭据</div>
        </div>
      ) : (
        <div className="space-y-5 text-xs">
          {/* Provider & Status Banner */}
          <div className="p-4 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-4">
            <div className="flex items-center gap-3">
              <div
                className="px-3 py-1.5 rounded-lg text-white font-bold tracking-wider inline-flex items-center gap-1.5"
                style={{
                  backgroundColor: isPayPal ? '#003087' : isStripe ? '#635bff' : 'var(--cms-primary)',
                }}
              >
                <CreditCard size={14} aria-hidden="true" />
                <span>{details.payment_provider.toUpperCase()}</span>
              </div>
              <div>
                <div className="text-xs text-[var(--cms-muted-fg)]">支付接入渠道</div>
                <div className="text-sm font-semibold text-[var(--cms-fg)] mt-0.5">
                  {isPayPal ? 'PayPal Subscriptions / Orders' : isStripe ? 'Stripe Direct Charges' : `${details.payment_provider} Gateway`}
                </div>
              </div>
            </div>

            <div className="text-right space-y-1">
              <div className="text-xs text-[var(--cms-muted-fg)]">交易扣款状态</div>
              <StatusDot
                tone={isSuccess ? 'success' : isRefunded ? 'error' : 'warning'}
                label={details.status}
              />
            </div>
          </div>

          {/* Financial Breakdown */}
          <div className="p-4 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-card)] space-y-3">
            <div className="flex items-center gap-2 text-sm font-semibold text-[var(--cms-fg)]">
              <DollarSign size={15} className="text-[var(--cms-primary)]" aria-hidden="true" />
              <span>财务结算明细 (Financial Breakdown)</span>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              <div className="p-3.5 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] text-center space-y-1">
                <div className="text-xs text-[var(--cms-muted-fg)]">用户支付总额 (Gross)</div>
                <div className="text-lg font-bold tabular-nums text-[var(--cms-fg)]">
                  ${details.gross_amount.toFixed(2)}{' '}
                  <span className="text-xs font-medium text-[var(--cms-muted-fg)]">{details.currency}</span>
                </div>
              </div>
              <div className="p-3.5 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] text-center space-y-1">
                <div className="text-xs text-[var(--cms-muted-fg)]">渠道扣取手续费 (Fee)</div>
                <div className={`text-lg font-bold tabular-nums ${details.fee_amount == null ? 'text-[var(--cms-muted-fg)]' : 'text-[var(--cms-error)]'}`}>
                  {details.fee_amount == null ? '待渠道凭证' : `${(-details.fee_amount).toFixed(2)} ${details.currency}`}
                </div>
              </div>
              <div className="p-3.5 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] text-center space-y-1">
                <div className="text-xs text-[var(--cms-muted-fg)]">商户实收净额 (Net)</div>
                <div className={`text-lg font-bold tabular-nums ${details.net_amount == null ? 'text-[var(--cms-muted-fg)]' : 'text-[var(--cms-success)]'}`}>
                  {details.net_amount == null ? '待渠道凭证' : `${details.net_amount.toFixed(2)} ${details.currency}`}
                </div>
              </div>
            </div>
          </div>

          {/* IDs */}
          <div className="p-4 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-card)] space-y-3">
            <div className="flex items-center gap-2 text-sm font-semibold text-[var(--cms-fg)]">
              <Hash size={15} className="text-[var(--cms-primary)]" aria-hidden="true" />
              <span>通道流水与单号凭证</span>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              {[
                { label: '第三方订单/订阅号 (External Order ID)', value: details.external_order_id, key: 'ext' },
                { label: '扣款凭证流水号 (Capture / Charge ID)', value: details.capture_id, key: 'capture' },
              ].map((row) => (
                <div key={row.key} className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] space-y-1.5">
                  <div className="text-xs text-[var(--cms-muted-fg)]">{row.label}</div>
                  <div className="flex items-center justify-between gap-2">
                    <code className="font-mono-code text-xs text-[var(--cms-fg)] break-all font-medium">
                      {row.value || '-'}
                    </code>
                    {row.value && (
                      <button
                        type="button"
                        onClick={() => handleCopy(row.value, row.key)}
                        className="btn-secondary h-7 px-2.5 text-xs shrink-0 rounded-lg"
                      >
                        {copiedKey === row.key ? <Check size={12} className="text-[var(--cms-success)]" /> : <Copy size={12} />}
                        <span>{copiedKey === row.key ? '已复制' : '复制'}</span>
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          </div>

          {/* Payer Profile */}
          <div className="p-4 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-card)] space-y-3">
            <div className="flex items-center gap-2 text-sm font-semibold text-[var(--cms-fg)]">
              <UserCheck size={15} className="text-[var(--cms-primary)]" aria-hidden="true" />
              <span>买家付款画像与风控状态</span>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-3">
              <div className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] space-y-1">
                <div className="text-xs text-[var(--cms-muted-fg)]">买家第三方账号 ID</div>
                <div className="font-semibold text-[var(--cms-fg)] break-all">{details.payer_id || '-'}</div>
              </div>
              <div className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] space-y-1">
                <div className="text-xs text-[var(--cms-muted-fg)]">买家支付邮箱</div>
                <div className="font-semibold text-[var(--cms-fg)] break-all">{details.payer_email || '-'}</div>
              </div>
              <div className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] space-y-1">
                <div className="text-xs text-[var(--cms-muted-fg)]">买家登记姓名</div>
                <div className="font-semibold text-[var(--cms-fg)]">{details.payer_name || '-'}</div>
              </div>
              <div className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] space-y-1">
                <div className="text-xs text-[var(--cms-muted-fg)]">国家 / 地区代码</div>
                <div className="font-semibold text-[var(--cms-fg)]">{details.payer_country || '-'}</div>
              </div>
              <div className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] space-y-1 sm:col-span-2">
                <div className="text-xs text-[var(--cms-muted-fg)]">卖家保障风控状态</div>
                <div className="inline-flex items-center gap-1.5 font-semibold text-[var(--cms-fg)]">
                  <ShieldCheck size={13} className="text-[var(--cms-success)]" aria-hidden="true" />
                  <span>{details.seller_protection_status || '待渠道凭证'}</span>
                </div>
              </div>
            </div>
          </div>


        </div>
      )}
    </CmsDialog>
  );
};

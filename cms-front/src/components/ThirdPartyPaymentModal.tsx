import React, { useState } from 'react';
import { createPortal } from 'react-dom';
import { X, Copy, Check, FileText, CreditCard, AlertCircle, RefreshCw, ShieldCheck, DollarSign, UserCheck, Hash } from 'lucide-react';

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
  fee_amount: number;
  net_amount: number;
  status: string;
  seller_protection_status: string;
  raw_payload?: string;
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
  onRefresh
}) => {
  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const [showRaw, setShowRaw] = useState(false);

  if (!isOpen) return null;

  const handleCopy = (text: string, key: string) => {
    if (!text) return;
    navigator.clipboard.writeText(text);
    setCopiedKey(key);
    setTimeout(() => setCopiedKey(null), 2000);
  };

  let formattedRaw = '';
  if (details?.raw_payload) {
    try {
      const parsed = typeof details.raw_payload === 'string' ? JSON.parse(details.raw_payload) : details.raw_payload;
      formattedRaw = JSON.stringify(parsed, null, 2);
    } catch (_) {
      formattedRaw = String(details.raw_payload);
    }
  }

  const isPayPal = details?.payment_provider?.toLowerCase() === 'paypal';
  const isStripe = details?.payment_provider?.toLowerCase() === 'stripe';
  const isSuccess = details?.status === 'COMPLETED' || details?.status === 'SUCCESS' || details?.status === 'PAID' || details?.status === 'CAPTURED';
  const isRefunded = details?.status === 'REFUNDED' || details?.status === 'PARTIALLY_REFUNDED';

  return createPortal(
    <div
      style={{
        position: 'fixed',
        inset: 0,
        backgroundColor: 'rgba(15, 23, 42, 0.35)',
        backdropFilter: 'blur(2.5px)',
        WebkitBackdropFilter: 'blur(2.5px)',
        zIndex: 10000,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: '16px'
      }}
      onClick={onClose}
    >
      <div
        className="animate-scale-in"
        style={{
          width: '100%',
          maxWidth: '680px',
          maxHeight: '88vh',
          display: 'flex',
          flexDirection: 'column',
          backgroundColor: 'hsl(var(--bg-surface))',
          borderRadius: '16px',
          border: '1px solid hsl(var(--border))',
          boxShadow: 'var(--shadow-lg)',
          overflow: 'hidden'
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div
          style={{
            padding: '18px 24px',
            borderBottom: '1px solid hsl(var(--border))',
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            backgroundColor: 'hsl(var(--bg-surface))'
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
            <div
              style={{
                width: '36px',
                height: '36px',
                borderRadius: '10px',
                backgroundColor: 'hsl(var(--primary) / 0.1)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                color: 'hsl(var(--primary))'
              }}
            >
              <CreditCard size={18} />
            </div>
            <div>
              <h3 style={{ margin: 0, fontSize: '1.05rem', fontWeight: 600, color: 'hsl(var(--text-primary))' }}>
                第三方支付凭据详情
              </h3>
              <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginTop: '2px' }}>
                关联系统订单 ID: <span style={{ fontWeight: 600, color: 'hsl(var(--text-primary))' }}>#{orderId}</span>
              </div>
            </div>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            {onRefresh && (
              <button
                className="btn-secondary"
                onClick={onRefresh}
                style={{
                  padding: '6px 12px',
                  fontSize: '0.8rem',
                  borderRadius: '8px',
                  cursor: 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '6px'
                }}
                title="重新拉取网关状态"
              >
                <RefreshCw size={13} className={loading ? 'animate-spin' : ''} />
                <span>刷新</span>
              </button>
            )}
            <button
              onClick={onClose}
              style={{
                background: 'transparent',
                border: 'none',
                cursor: 'pointer',
                color: 'hsl(var(--text-secondary))',
                padding: '6px',
                borderRadius: '6px',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center'
              }}
              aria-label="Close modal"
            >
              <X size={20} />
            </button>
          </div>
        </div>

        {/* Content Body */}
        <div style={{ padding: '24px', overflowY: 'auto', flex: 1 }}>
          {loading ? (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: '60px 0' }}>
              <div className="spinner" style={{ width: '36px', height: '36px', marginBottom: '16px' }} />
              <p style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.9rem' }}>正在请求第三方支付网关数据...</p>
            </div>
          ) : !details ? (
            <div style={{ textAlign: 'center', padding: '48px 16px' }}>
              <div
                style={{
                  width: '56px',
                  height: '56px',
                  borderRadius: '50%',
                  backgroundColor: 'hsl(var(--bg-base))',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  margin: '0 auto 16px auto',
                  color: 'hsl(var(--text-secondary))'
                }}
              >
                <AlertCircle size={28} />
              </div>
              <h4 style={{ fontSize: '1.05rem', color: 'hsl(var(--text-primary))', marginBottom: '8px', fontWeight: 600 }}>暂无第三方支付凭据</h4>
            </div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '18px' }}>
              {/* Channel & Status Banner */}
              <div
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  padding: '16px 20px',
                  backgroundColor: 'hsl(var(--bg-base))',
                  borderRadius: '12px',
                  border: '1px solid hsl(var(--border))'
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: '14px' }}>
                  <div
                    style={{
                      padding: '6px 12px',
                      borderRadius: '8px',
                      backgroundColor: isPayPal ? '#003087' : isStripe ? '#635bff' : 'hsl(var(--primary))',
                      color: '#ffffff',
                      fontWeight: 700,
                      fontSize: '0.85rem',
                      letterSpacing: '0.5px',
                      boxShadow: '0 2px 4px rgba(0,0,0,0.1)'
                    }}
                  >
                    {details.payment_provider.toUpperCase()}
                  </div>
                  <div>
                    <div style={{ fontSize: '0.7rem', textTransform: 'uppercase', letterSpacing: '0.5px', color: 'hsl(var(--text-secondary))', fontWeight: 500 }}>
                      支付接入渠道
                    </div>
                    <div style={{ fontSize: '0.95rem', fontWeight: 600, color: 'hsl(var(--text-primary))', marginTop: '1px' }}>
                      {isPayPal ? 'PayPal Subscriptions / Orders' : isStripe ? 'Stripe Direct Charges' : `${details.payment_provider} Gateway`}
                    </div>
                  </div>
                </div>

                <div style={{ textAlign: 'right' }}>
                  <div style={{ fontSize: '0.7rem', textTransform: 'uppercase', letterSpacing: '0.5px', color: 'hsl(var(--text-secondary))', marginBottom: '4px', fontWeight: 500 }}>
                    交易扣款状态
                  </div>
                  <span
                    className={`badge ${isSuccess
                        ? 'badge-green'
                        : isRefunded
                          ? 'badge-red'
                          : 'badge-orange'
                      }`}
                    style={{ fontSize: '0.8rem', fontWeight: 600, padding: '4px 10px' }}
                  >
                    {details.status}
                  </span>
                </div>
              </div>

              {/* Financial Metrics Cards */}
              <div
                style={{
                  backgroundColor: 'hsl(var(--bg-surface))',
                  borderRadius: '12px',
                  border: '1px solid hsl(var(--border))',
                  padding: '16px 20px',
                  boxShadow: 'var(--shadow-sm)'
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '14px' }}>
                  <DollarSign size={16} style={{ color: 'hsl(var(--primary))' }} />
                  <h4 style={{ margin: 0, fontSize: '0.9rem', color: 'hsl(var(--text-primary))', fontWeight: 600 }}>
                    财务结算明细 (Financial Breakdown)
                  </h4>
                </div>

                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '12px' }}>
                  <div
                    style={{
                      padding: '14px 16px',
                      backgroundColor: 'hsl(var(--bg-base))',
                      borderRadius: '10px',
                      border: '1px solid hsl(var(--border))',
                      textAlign: 'center'
                    }}
                  >
                    <div style={{ fontSize: '0.72rem', color: 'hsl(var(--text-secondary))', fontWeight: 500, marginBottom: '6px' }}>
                      用户支付总额 (Gross)
                    </div>
                    <div style={{ fontSize: '1.25rem', fontWeight: 700, color: 'hsl(var(--text-primary))' }}>
                      ${details.gross_amount.toFixed(2)}
                      <span style={{ fontSize: '0.75rem', fontWeight: 500, color: 'hsl(var(--text-secondary))', marginLeft: '4px' }}>
                        {details.currency}
                      </span>
                    </div>
                  </div>

                  <div
                    style={{
                      padding: '14px 16px',
                      backgroundColor: 'hsl(var(--bg-base))',
                      borderRadius: '10px',
                      border: '1px solid hsl(var(--border))',
                      textAlign: 'center'
                    }}
                  >
                    <div style={{ fontSize: '0.72rem', color: 'hsl(var(--text-secondary))', fontWeight: 500, marginBottom: '6px' }}>
                      渠道扣取手续费 (Fee)
                    </div>
                    <div style={{ fontSize: '1.25rem', fontWeight: 700, color: '#dc2626' }}>
                      -${details.fee_amount.toFixed(2)}
                      <span style={{ fontSize: '0.75rem', fontWeight: 500, color: 'hsl(var(--text-secondary))', marginLeft: '4px' }}>
                        {details.currency}
                      </span>
                    </div>
                  </div>

                  <div
                    style={{
                      padding: '14px 16px',
                      backgroundColor: 'hsl(var(--bg-base))',
                      borderRadius: '10px',
                      border: '1px solid hsl(var(--border))',
                      textAlign: 'center'
                    }}
                  >
                    <div style={{ fontSize: '0.72rem', color: 'hsl(var(--text-secondary))', fontWeight: 500, marginBottom: '6px' }}>
                      商户实收净额 (Net)
                    </div>
                    <div style={{ fontSize: '1.25rem', fontWeight: 700, color: '#16a34a' }}>
                      ${details.net_amount.toFixed(2)}
                      <span style={{ fontSize: '0.75rem', fontWeight: 500, color: 'hsl(var(--text-secondary))', marginLeft: '4px' }}>
                        {details.currency}
                      </span>
                    </div>
                  </div>
                </div>
              </div>

              {/* Transaction IDs */}
              <div
                style={{
                  backgroundColor: 'hsl(var(--bg-surface))',
                  borderRadius: '12px',
                  border: '1px solid hsl(var(--border))',
                  padding: '16px 20px',
                  boxShadow: 'var(--shadow-sm)'
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '14px' }}>
                  <Hash size={16} style={{ color: 'hsl(var(--primary))' }} />
                  <h4 style={{ margin: 0, fontSize: '0.9rem', color: 'hsl(var(--text-primary))', fontWeight: 600 }}>
                    通道流水与单号凭证
                  </h4>
                </div>

                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: '14px' }}>
                  <div style={{ padding: '12px 14px', backgroundColor: 'hsl(var(--bg-base))', borderRadius: '8px', border: '1px solid hsl(var(--border))' }}>
                    <span style={{ fontSize: '0.72rem', fontWeight: 500, color: 'hsl(var(--text-secondary))', display: 'block', marginBottom: '6px' }}>
                      第三方订单/订阅号 (External Order ID)
                    </span>
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '8px' }}>
                      <code style={{ fontSize: '0.82rem', fontFamily: 'monospace', color: 'hsl(var(--text-primary))', wordBreak: 'break-all', fontWeight: 500 }}>
                        {details.external_order_id || '-'}
                      </code>
                      {details.external_order_id && (
                        <button
                          onClick={() => handleCopy(details.external_order_id, 'ext')}
                          style={{
                            background: 'hsl(var(--bg-surface))',
                            border: '1px solid hsl(var(--border))',
                            borderRadius: '6px',
                            cursor: 'pointer',
                            color: 'hsl(var(--text-secondary))',
                            padding: '4px 8px',
                            display: 'flex',
                            alignItems: 'center',
                            gap: '4px',
                            fontSize: '0.75rem',
                            flexShrink: 0
                          }}
                          title="复制单号"
                        >
                          {copiedKey === 'ext' ? <Check size={13} color="#16a34a" /> : <Copy size={13} />}
                          <span>{copiedKey === 'ext' ? '已复制' : '复制'}</span>
                        </button>
                      )}
                    </div>
                  </div>

                  <div style={{ padding: '12px 14px', backgroundColor: 'hsl(var(--bg-base))', borderRadius: '8px', border: '1px solid hsl(var(--border))' }}>
                    <span style={{ fontSize: '0.72rem', fontWeight: 500, color: 'hsl(var(--text-secondary))', display: 'block', marginBottom: '6px' }}>
                      扣款凭证流水号 (Capture / Charge ID)
                    </span>
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '8px' }}>
                      <code style={{ fontSize: '0.82rem', fontFamily: 'monospace', color: 'hsl(var(--text-primary))', wordBreak: 'break-all', fontWeight: 500 }}>
                        {details.capture_id || '-'}
                      </code>
                      {details.capture_id && (
                        <button
                          onClick={() => handleCopy(details.capture_id, 'capture')}
                          style={{
                            background: 'hsl(var(--bg-surface))',
                            border: '1px solid hsl(var(--border))',
                            borderRadius: '6px',
                            cursor: 'pointer',
                            color: 'hsl(var(--text-secondary))',
                            padding: '4px 8px',
                            display: 'flex',
                            alignItems: 'center',
                            gap: '4px',
                            fontSize: '0.75rem',
                            flexShrink: 0
                          }}
                          title="复制扣款凭证号"
                        >
                          {copiedKey === 'capture' ? <Check size={13} color="#16a34a" /> : <Copy size={13} />}
                          <span>{copiedKey === 'capture' ? '已复制' : '复制'}</span>
                        </button>
                      )}
                    </div>
                  </div>
                </div>
              </div>

              {/* Payer Information & Risk Profile */}
              <div
                style={{
                  backgroundColor: 'hsl(var(--bg-surface))',
                  borderRadius: '12px',
                  border: '1px solid hsl(var(--border))',
                  padding: '16px 20px',
                  boxShadow: 'var(--shadow-sm)'
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '14px' }}>
                  <UserCheck size={16} style={{ color: 'hsl(var(--primary))' }} />
                  <h4 style={{ margin: 0, fontSize: '0.9rem', color: 'hsl(var(--text-primary))', fontWeight: 600 }}>
                    买家付款画像与风控状态
                  </h4>
                </div>

                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: '14px', fontSize: '0.85rem' }}>
                  <div style={{ padding: '10px 12px', backgroundColor: 'hsl(var(--bg-base))', borderRadius: '8px', border: '1px solid hsl(var(--border))' }}>
                    <span style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.72rem', fontWeight: 500, display: 'block', marginBottom: '4px' }}>
                      买家第三方账号 ID
                    </span>
                    <div style={{ fontWeight: 600, color: 'hsl(var(--text-primary))', wordBreak: 'break-all' }}>
                      {details.payer_id || '-'}
                    </div>
                  </div>

                  <div style={{ padding: '10px 12px', backgroundColor: 'hsl(var(--bg-base))', borderRadius: '8px', border: '1px solid hsl(var(--border))' }}>
                    <span style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.72rem', fontWeight: 500, display: 'block', marginBottom: '4px' }}>
                      买家支付邮箱
                    </span>
                    <div style={{ fontWeight: 600, color: 'hsl(var(--text-primary))', wordBreak: 'break-all' }}>
                      {details.payer_email || '-'}
                    </div>
                  </div>

                  <div style={{ padding: '10px 12px', backgroundColor: 'hsl(var(--bg-base))', borderRadius: '8px', border: '1px solid hsl(var(--border))' }}>
                    <span style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.72rem', fontWeight: 500, display: 'block', marginBottom: '4px' }}>
                      买家登记姓名
                    </span>
                    <div style={{ fontWeight: 600, color: 'hsl(var(--text-primary))' }}>
                      {details.payer_name || '-'}
                    </div>
                  </div>

                  <div style={{ padding: '10px 12px', backgroundColor: 'hsl(var(--bg-base))', borderRadius: '8px', border: '1px solid hsl(var(--border))' }}>
                    <span style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.72rem', fontWeight: 500, display: 'block', marginBottom: '4px' }}>
                      买家国家 / 地区代码
                    </span>
                    <div style={{ fontWeight: 600, color: 'hsl(var(--text-primary))' }}>
                      {details.payer_country || '-'}
                    </div>
                  </div>

                  <div style={{ padding: '10px 12px', backgroundColor: 'hsl(var(--bg-base))', borderRadius: '8px', border: '1px solid hsl(var(--border))' }}>
                    <span style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.72rem', fontWeight: 500, display: 'block', marginBottom: '4px' }}>
                      卖家保障风控状态
                    </span>
                    <div style={{ marginTop: '2px' }}>
                      <span className="badge badge-gray" style={{ fontSize: '0.75rem', fontWeight: 600 }}>
                        <ShieldCheck size={12} style={{ display: 'inline', verticalAlign: 'text-bottom', marginRight: '3px' }} />
                        {details.seller_protection_status || 'ELIGIBLE'}
                      </span>
                    </div>
                  </div>
                </div>
              </div>

              {/* Raw JSON Payload */}
              {formattedRaw && (
                <div
                  style={{
                    backgroundColor: 'hsl(var(--bg-surface))',
                    borderRadius: '12px',
                    border: '1px solid hsl(var(--border))',
                    padding: '14px 20px',
                    boxShadow: 'var(--shadow-sm)'
                  }}
                >
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                    <button
                      className="btn-secondary"
                      onClick={() => setShowRaw(!showRaw)}
                      style={{
                        padding: '6px 12px',
                        fontSize: '0.78rem',
                        borderRadius: '6px',
                        display: 'inline-flex',
                        alignItems: 'center',
                        gap: '6px',
                        cursor: 'pointer'
                      }}
                    >
                      <FileText size={13} />
                      <span>{showRaw ? '收起第三方原始网关报文' : '展开第三方原始 JSON 报文'}</span>
                    </button>
                    {showRaw && (
                      <button
                        className="btn-secondary"
                        onClick={() => handleCopy(formattedRaw, 'raw')}
                        style={{
                          padding: '6px 12px',
                          fontSize: '0.78rem',
                          borderRadius: '6px',
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: '6px',
                          cursor: 'pointer'
                        }}
                      >
                        {copiedKey === 'raw' ? <Check size={13} color="#16a34a" /> : <Copy size={13} />}
                        <span>{copiedKey === 'raw' ? '已复制报文' : '复制 JSON 报文'}</span>
                      </button>
                    )}
                  </div>

                  {showRaw && (
                    <pre
                      style={{
                        marginTop: '12px',
                        padding: '14px',
                        backgroundColor: 'hsl(var(--bg-base))',
                        borderRadius: '8px',
                        fontSize: '0.75rem',
                        maxHeight: '260px',
                        overflowY: 'auto',
                        color: 'hsl(var(--text-primary))',
                        fontFamily: 'SFMono-Regular, Menlo, Monaco, Consolas, monospace',
                        lineHeight: 1.5,
                        whiteSpace: 'pre-wrap',
                        wordBreak: 'break-all',
                        border: '1px solid hsl(var(--border))'
                      }}
                    >
                      {formattedRaw}
                    </pre>
                  )}
                </div>
              )}
            </div>
          )}
        </div>

        {/* Footer */}
        <div
          style={{
            padding: '14px 24px',
            borderTop: '1px solid hsl(var(--border))',
            display: 'flex',
            justifyContent: 'flex-end',
            backgroundColor: 'hsl(var(--bg-surface))'
          }}
        >
          <button className="btn-secondary" onClick={onClose} style={{ padding: '8px 22px', fontSize: '0.85rem', cursor: 'pointer', borderRadius: '8px' }}>
            关闭
          </button>
        </div>
      </div>
    </div>,
    document.body
  );
};

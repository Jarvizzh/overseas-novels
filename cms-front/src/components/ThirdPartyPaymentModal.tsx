import React, { useState } from 'react';
import { X, Copy, Check, FileText, CreditCard, AlertCircle, RefreshCw } from 'lucide-react';

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

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        backgroundColor: 'rgba(0,0,0,0.6)',
        backdropFilter: 'blur(4px)',
        zIndex: 1000,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: '16px'
      }}
      onClick={onClose}
    >
      <div
        className="glass-panel"
        style={{
          width: '100%',
          maxWidth: '680px',
          maxHeight: '90vh',
          display: 'flex',
          flexDirection: 'column',
          padding: 0,
          overflow: 'hidden',
          borderRadius: '16px',
          boxShadow: '0 25px 50px -12px rgba(0, 0, 0, 0.35)',
          backgroundColor: 'hsl(var(--card))'
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div
          style={{
            padding: '16px 20px',
            borderBottom: '1px solid hsl(var(--border))',
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            backgroundColor: 'hsl(var(--card) / 0.5)'
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            <CreditCard size={18} style={{ color: 'hsl(var(--primary))' }} />
            <h3 style={{ margin: 0, fontSize: '1.05rem', fontWeight: 600, color: 'hsl(var(--text-primary))' }}>
              第三方支付凭据详情 <span style={{ fontSize: '0.85rem', color: 'hsl(var(--text-secondary))', fontWeight: 400 }}>(系统订单 #{orderId})</span>
            </h3>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            {onRefresh && (
              <button
                className="btn-secondary"
                onClick={onRefresh}
                style={{ padding: '6px 10px', fontSize: '0.8rem', borderRadius: '6px', cursor: 'pointer' }}
                title="重新查询详情"
              >
                <RefreshCw size={13} className={loading ? 'animate-spin' : ''} />
              </button>
            )}
            <button
              onClick={onClose}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'hsl(var(--text-secondary))' }}
            >
              <X size={20} />
            </button>
          </div>
        </div>

        {/* Content Body */}
        <div style={{ padding: '20px', overflowY: 'auto', flex: 1 }}>
          {loading ? (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: '48px 0' }}>
              <div className="spinner" style={{ width: '32px', height: '32px', marginBottom: '16px' }} />
              <p style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.9rem' }}>正在请求第三方支付网关数据...</p>
            </div>
          ) : !details ? (
            <div style={{ textAlign: 'center', padding: '40px 16px' }}>
              <AlertCircle size={40} style={{ color: 'hsl(var(--text-secondary))', marginBottom: '12px', opacity: 0.7 }} />
              <h4 style={{ fontSize: '1rem', color: 'hsl(var(--text-primary))', marginBottom: '6px' }}>暂无第三方支付详细凭据</h4>
              <p style={{ fontSize: '0.85rem', color: 'hsl(var(--text-secondary))', maxWidth: '420px', margin: '0 auto' }}>
                该订单可能为早期历史订单、管理员手工发放金币或本地测试模拟订单，未留存第三方支付网关的即时回调流水。
              </p>
            </div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
              {/* Channel & Status Banner */}
              <div
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  padding: '14px 16px',
                  backgroundColor: 'hsl(var(--background) / 0.6)',
                  borderRadius: '10px',
                  border: '1px solid hsl(var(--border) / 0.6)'
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                  <div
                    style={{
                      padding: '4px 10px',
                      borderRadius: '6px',
                      backgroundColor: details.payment_provider.toLowerCase() === 'paypal' ? '#003087' : '#635bff',
                      color: '#ffffff',
                      fontWeight: 700,
                      fontSize: '0.85rem',
                      letterSpacing: '0.5px'
                    }}
                  >
                    {details.payment_provider.toUpperCase()}
                  </div>
                  <div>
                    <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))' }}>支付通道</div>
                    <div style={{ fontSize: '0.9rem', fontWeight: 600, color: 'hsl(var(--text-primary))' }}>
                      {details.payment_provider.toLowerCase() === 'paypal' ? 'PayPal Checkout' : 'Stripe Payments'}
                    </div>
                  </div>
                </div>

                <div style={{ textAlign: 'right' }}>
                  <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '2px' }}>扣款状态</div>
                  <span
                    className={`badge ${
                      details.status === 'COMPLETED' || details.status === 'SUCCESS' || details.status === 'PAID'
                        ? 'badge-green'
                        : details.status === 'REFUNDED'
                        ? 'badge-red'
                        : 'badge-orange'
                    }`}
                    style={{ fontSize: '0.8rem', fontWeight: 600, padding: '3px 8px' }}
                  >
                    {details.status}
                  </span>
                </div>
              </div>

              {/* Transaction IDs */}
              <div
                style={{
                  backgroundColor: 'hsl(var(--card))',
                  borderRadius: '10px',
                  border: '1px solid hsl(var(--border) / 0.6)',
                  padding: '14px 16px'
                }}
              >
                <h4 style={{ fontSize: '0.85rem', color: 'hsl(var(--text-primary))', marginBottom: '12px', fontWeight: 600 }}>
                  流水单号凭据
                </h4>
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))', gap: '12px' }}>
                  <div>
                    <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', display: 'block', marginBottom: '2px' }}>
                      第三方订单号 (External Order ID)
                    </span>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                      <code style={{ fontSize: '0.8rem', backgroundColor: 'hsl(var(--background))', padding: '3px 6px', borderRadius: '4px', wordBreak: 'break-all' }}>
                        {details.external_order_id || '-'}
                      </code>
                      {details.external_order_id && (
                        <button
                          onClick={() => handleCopy(details.external_order_id, 'ext')}
                          style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'hsl(var(--text-secondary))', padding: '2px' }}
                          title="复制单号"
                        >
                          {copiedKey === 'ext' ? <Check size={13} color="#22c55e" /> : <Copy size={13} />}
                        </button>
                      )}
                    </div>
                  </div>

                  <div>
                    <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', display: 'block', marginBottom: '2px' }}>
                      扣款凭证号 (Capture / Charge ID)
                    </span>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                      <code style={{ fontSize: '0.8rem', backgroundColor: 'hsl(var(--background))', padding: '3px 6px', borderRadius: '4px', wordBreak: 'break-all' }}>
                        {details.capture_id || '-'}
                      </code>
                      {details.capture_id && (
                        <button
                          onClick={() => handleCopy(details.capture_id, 'capture')}
                          style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'hsl(var(--text-secondary))', padding: '2px' }}
                          title="复制扣款凭证号"
                        >
                          {copiedKey === 'capture' ? <Check size={13} color="#22c55e" /> : <Copy size={13} />}
                        </button>
                      )}
                    </div>
                  </div>
                </div>
              </div>

              {/* Financial Breakdown */}
              <div
                style={{
                  backgroundColor: 'hsl(var(--card))',
                  borderRadius: '10px',
                  border: '1px solid hsl(var(--border) / 0.6)',
                  padding: '14px 16px'
                }}
              >
                <h4 style={{ fontSize: '0.85rem', color: 'hsl(var(--text-primary))', marginBottom: '12px', fontWeight: 600 }}>
                  财务结算与手续费
                </h4>
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '12px', textAlign: 'center' }}>
                  <div style={{ padding: '10px', backgroundColor: 'hsl(var(--background) / 0.5)', borderRadius: '8px' }}>
                    <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>交易总金额 (Gross)</div>
                    <div style={{ fontSize: '1.05rem', fontWeight: 700, color: 'hsl(var(--text-primary))' }}>
                      ${details.gross_amount.toFixed(2)} <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))' }}>{details.currency}</span>
                    </div>
                  </div>
                  <div style={{ padding: '10px', backgroundColor: 'hsl(var(--background) / 0.5)', borderRadius: '8px' }}>
                    <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>渠道手续费 (Fee)</div>
                    <div style={{ fontSize: '1.05rem', fontWeight: 700, color: '#ef4444' }}>
                      -${details.fee_amount.toFixed(2)} <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))' }}>{details.currency}</span>
                    </div>
                  </div>
                  <div style={{ padding: '10px', backgroundColor: 'hsl(var(--background) / 0.5)', borderRadius: '8px' }}>
                    <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>净到账金额 (Net)</div>
                    <div style={{ fontSize: '1.05rem', fontWeight: 700, color: '#22c55e' }}>
                      ${details.net_amount.toFixed(2)} <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))' }}>{details.currency}</span>
                    </div>
                  </div>
                </div>
              </div>

              {/* Payer Information */}
              <div
                style={{
                  backgroundColor: 'hsl(var(--card))',
                  borderRadius: '10px',
                  border: '1px solid hsl(var(--border) / 0.6)',
                  padding: '14px 16px'
                }}
              >
                <h4 style={{ fontSize: '0.85rem', color: 'hsl(var(--text-primary))', marginBottom: '12px', fontWeight: 600 }}>
                  买家身份与风控画像
                </h4>
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: '10px', fontSize: '0.85rem' }}>
                  <div>
                    <span style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.75rem' }}>买家第三方 ID:</span>
                    <div style={{ fontWeight: 500, color: 'hsl(var(--text-primary))' }}>{details.payer_id || '-'}</div>
                  </div>
                  <div>
                    <span style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.75rem' }}>买家邮箱:</span>
                    <div style={{ fontWeight: 500, color: 'hsl(var(--text-primary))' }}>{details.payer_email || '-'}</div>
                  </div>
                  <div>
                    <span style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.75rem' }}>买家姓名:</span>
                    <div style={{ fontWeight: 500, color: 'hsl(var(--text-primary))' }}>{details.payer_name || '-'}</div>
                  </div>
                  <div>
                    <span style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.75rem' }}>国家 / 地区:</span>
                    <div style={{ fontWeight: 500, color: 'hsl(var(--text-primary))' }}>{details.payer_country || '-'}</div>
                  </div>
                  <div>
                    <span style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.75rem' }}>卖家保障状态:</span>
                    <div>
                      <span className="badge badge-gray" style={{ fontSize: '0.75rem' }}>
                        {details.seller_protection_status || '-'}
                      </span>
                    </div>
                  </div>
                </div>
              </div>

              {/* Raw JSON Payload */}
              {formattedRaw && (
                <div
                  style={{
                    backgroundColor: 'hsl(var(--card))',
                    borderRadius: '10px',
                    border: '1px solid hsl(var(--border) / 0.6)',
                    padding: '12px 16px'
                  }}
                >
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                    <button
                      className="btn-secondary"
                      onClick={() => setShowRaw(!showRaw)}
                      style={{ padding: '4px 8px', fontSize: '0.75rem', borderRadius: '4px', display: 'inline-flex', alignItems: 'center', gap: '4px', cursor: 'pointer' }}
                    >
                      <FileText size={12} />
                      <span>{showRaw ? '收起第三方原始报文' : '展开第三方原始 JSON 报文'}</span>
                    </button>
                    {showRaw && (
                      <button
                        className="btn-secondary"
                        onClick={() => handleCopy(formattedRaw, 'raw')}
                        style={{ padding: '4px 8px', fontSize: '0.75rem', borderRadius: '4px', display: 'inline-flex', alignItems: 'center', gap: '4px', cursor: 'pointer' }}
                      >
                        {copiedKey === 'raw' ? <Check size={12} color="#22c55e" /> : <Copy size={12} />}
                        <span>{copiedKey === 'raw' ? '已复制' : '复制 JSON'}</span>
                      </button>
                    )}
                  </div>

                  {showRaw && (
                    <pre
                      style={{
                        marginTop: '10px',
                        padding: '12px',
                        backgroundColor: 'hsl(var(--background))',
                        borderRadius: '8px',
                        fontSize: '0.75rem',
                        maxHeight: '220px',
                        overflowY: 'auto',
                        color: 'hsl(var(--text-secondary))',
                        fontFamily: 'monospace',
                        lineHeight: 1.4,
                        whiteSpace: 'pre-wrap',
                        wordBreak: 'break-all'
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
            padding: '12px 20px',
            borderTop: '1px solid hsl(var(--border))',
            display: 'flex',
            justifyContent: 'flex-end',
            backgroundColor: 'hsl(var(--card) / 0.5)'
          }}
        >
          <button className="btn-secondary" onClick={onClose} style={{ padding: '8px 18px', fontSize: '0.85rem', cursor: 'pointer' }}>
            关闭
          </button>
        </div>
      </div>
    </div>
  );
};

import { useState, useEffect } from 'react';
import { apiRequest } from '../utils/api';
import { CreditCard, Save, Eye, EyeOff, CheckCircle2, AlertCircle } from 'lucide-react';

export default function PaymentTab() {
  const [paypalForm, setPaypalForm] = useState({
    paypal_mode: 'sandbox', // 'sandbox' | 'live'
    paypal_client_id: '',
    paypal_secret_key: '',
  });

  const [stripeForm, setStripeForm] = useState({
    stripe_mode: 'sandbox',
    stripe_publishable_key: '',
    stripe_secret_key: '',
  });

  const [showPaypalSecret, setShowPaypalSecret] = useState(false);
  const [showStripeSecret, setShowStripeSecret] = useState(false);

  const [paypalSaveMsg, setPaypalSaveMsg] = useState('');
  const [paypalSaveErr, setPaypalSaveErr] = useState('');
  const [paypalSaving, setPaypalSaving] = useState(false);

  const [stripeSaveMsg, setStripeSaveMsg] = useState('');
  const [stripeSaveErr, setStripeSaveErr] = useState('');
  const [stripeSaving, setStripeSaving] = useState(false);

  const fetchConfigs = async () => {
    try {
      const data: Record<string, string> = await apiRequest('GET', '/settings');
      
      // Determine PayPal mode
      let ppMode = data.paypal_mode;
      if (!ppMode) {
        ppMode = data.payment_sandbox_mode === 'false' ? 'live' : 'sandbox';
      }

      setPaypalForm({
        paypal_mode: ppMode,
        paypal_client_id: data.paypal_client_id || '',
        paypal_secret_key: data.paypal_secret_key || '',
      });

      setStripeForm({
        stripe_mode: data.payment_sandbox_mode === 'false' ? 'live' : 'sandbox',
        stripe_publishable_key: data.stripe_publishable_key || '',
        stripe_secret_key: data.stripe_secret_key || '',
      });
    } catch (err: any) {
      console.error('Failed to load operational configurations:', err);
    }
  };

  useEffect(() => {
    fetchConfigs();
  }, []);

  const handleSavePayPal = async (e: React.FormEvent) => {
    e.preventDefault();
    setPaypalSaveMsg('');
    setPaypalSaveErr('');
    setPaypalSaving(true);
    try {
      await apiRequest('POST', '/settings', {
        paypal_mode: paypalForm.paypal_mode,
        paypal_client_id: paypalForm.paypal_client_id.trim(),
        paypal_secret_key: paypalForm.paypal_secret_key.trim(),
        // Keep payment_sandbox_mode in sync for legacy compatibility
        payment_sandbox_mode: paypalForm.paypal_mode === 'live' ? 'false' : 'true',
      });
      setPaypalSaveMsg('PayPal 网关配置已成功保存并即时生效！');
      setTimeout(() => setPaypalSaveMsg(''), 4000);
    } catch (err: any) {
      setPaypalSaveErr(err.message || '保存 PayPal 配置失败');
    } finally {
      setPaypalSaving(false);
    }
  };

  const handleSaveStripe = async (e: React.FormEvent) => {
    e.preventDefault();
    setStripeSaveMsg('');
    setStripeSaveErr('');
    setStripeSaving(true);
    try {
      await apiRequest('POST', '/settings', {
        stripe_publishable_key: stripeForm.stripe_publishable_key.trim(),
        stripe_secret_key: stripeForm.stripe_secret_key.trim(),
      });
      setStripeSaveMsg('Stripe 网关配置已成功保存！');
      setTimeout(() => setStripeSaveMsg(''), 4000);
    } catch (err: any) {
      setStripeSaveErr(err.message || '保存 Stripe 配置失败');
    } finally {
      setStripeSaving(false);
    }
  };

  return (
    <div className="animate-fade-in" style={{ padding: '16px 24px 24px 24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">
            支付管理
          </h1>
          <p style={{ fontSize: '0.85rem', color: 'hsl(var(--text-secondary))', marginTop: '4px' }}>
            管理多通道在线支付网关凭证与沙盒/生产运行环境。后台配置具备最高优先级并自动缓存加速。
          </p>
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(440px, 1fr))', gap: '24px', maxWidth: '1100px' }}>
        
        {/* PayPal Configuration Card */}
        <form onSubmit={handleSavePayPal} className="glass-panel animate-fade-in" style={{
          padding: '24px',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'space-between',
          border: '1px solid hsl(var(--border))',
          backgroundColor: 'hsl(var(--bg-surface))',
          borderRadius: '16px',
        }}>
          <div>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '20px' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                <div style={{
                  width: '36px', height: '36px', borderRadius: '10px',
                  backgroundColor: 'rgba(0, 112, 224, 0.12)',
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                  color: '#0070e0', fontWeight: 700
                }}>
                  PP
                </div>
                <div>
                  <h3 style={{ fontSize: '1.1rem', fontWeight: 600, color: 'hsl(var(--text-primary))' }}>
                    PayPal 支付网关
                  </h3>
                  <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>
                    全球主流电子钱包与信用卡收单
                  </div>
                </div>
              </div>

              <span style={{
                fontSize: '0.72rem',
                fontWeight: 600,
                padding: '3px 8px',
                borderRadius: '6px',
                backgroundColor: paypalForm.paypal_mode === 'live' ? 'rgba(16, 185, 129, 0.12)' : 'rgba(245, 158, 11, 0.12)',
                color: paypalForm.paypal_mode === 'live' ? '#10b981' : '#f59e0b',
                border: `1px solid ${paypalForm.paypal_mode === 'live' ? 'rgba(16, 185, 129, 0.25)' : 'rgba(245, 158, 11, 0.25)'}`
              }}>
                {paypalForm.paypal_mode === 'live' ? '● 正式生产环境 (Live)' : '● 沙盒测试环境 (Sandbox)'}
              </span>
            </div>

            {paypalSaveMsg && (
              <div style={{
                backgroundColor: 'rgba(16, 185, 129, 0.1)',
                border: '1px solid rgba(16, 185, 129, 0.25)',
                color: '#10b981',
                padding: '10px 14px',
                borderRadius: '8px',
                fontSize: '0.82rem',
                marginBottom: '16px',
                display: 'flex',
                alignItems: 'center',
                gap: '8px'
              }}>
                <CheckCircle2 size={16} /> {paypalSaveMsg}
              </div>
            )}

            {paypalSaveErr && (
              <div style={{
                backgroundColor: 'rgba(239, 68, 68, 0.1)',
                border: '1px solid rgba(239, 68, 68, 0.25)',
                color: '#f87171',
                padding: '10px 14px',
                borderRadius: '8px',
                fontSize: '0.82rem',
                marginBottom: '16px',
                display: 'flex',
                alignItems: 'center',
                gap: '8px'
              }}>
                <AlertCircle size={16} /> {paypalSaveErr}
              </div>
            )}

            {/* Mode Selection */}
            <div style={{ marginBottom: '18px' }}>
              <label style={{ display: 'block', fontSize: '0.78rem', fontWeight: 600, color: 'hsl(var(--text-secondary))', marginBottom: '8px' }}>
                运行环境 (MODE)
              </label>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px' }}>
                <button
                  type="button"
                  onClick={() => setPaypalForm({ ...paypalForm, paypal_mode: 'sandbox' })}
                  style={{
                    padding: '10px 14px',
                    borderRadius: '8px',
                    fontSize: '0.82rem',
                    fontWeight: 500,
                    cursor: 'pointer',
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'flex-start',
                    gap: '2px',
                    border: paypalForm.paypal_mode === 'sandbox' 
                      ? '2px solid #f59e0b' 
                      : '1px solid hsl(var(--border))',
                    backgroundColor: paypalForm.paypal_mode === 'sandbox' 
                      ? 'rgba(245, 158, 11, 0.08)' 
                      : 'hsl(var(--bg-base))',
                    color: 'hsl(var(--text-primary))',
                    transition: 'all 0.15s ease',
                  }}
                >
                  <span style={{ fontWeight: 600, color: paypalForm.paypal_mode === 'sandbox' ? '#f59e0b' : 'inherit' }}>
                    Sandbox (沙盒模式)
                  </span>
                  <span style={{ fontSize: '0.7rem', color: 'hsl(var(--text-muted))' }}>
                    用于开发调试与模拟扣款
                  </span>
                </button>

                <button
                  type="button"
                  onClick={() => setPaypalForm({ ...paypalForm, paypal_mode: 'live' })}
                  style={{
                    padding: '10px 14px',
                    borderRadius: '8px',
                    fontSize: '0.82rem',
                    fontWeight: 500,
                    cursor: 'pointer',
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'flex-start',
                    gap: '2px',
                    border: paypalForm.paypal_mode === 'live' 
                      ? '2px solid #10b981' 
                      : '1px solid hsl(var(--border))',
                    backgroundColor: paypalForm.paypal_mode === 'live' 
                      ? 'rgba(16, 185, 129, 0.08)' 
                      : 'hsl(var(--bg-base))',
                    color: 'hsl(var(--text-primary))',
                    transition: 'all 0.15s ease',
                  }}
                >
                  <span style={{ fontWeight: 600, color: paypalForm.paypal_mode === 'live' ? '#10b981' : 'inherit' }}>
                    Live (正式生产)
                  </span>
                  <span style={{ fontSize: '0.7rem', color: 'hsl(var(--text-muted))' }}>
                    真实向海外用户收取款项
                  </span>
                </button>
              </div>
            </div>

            {/* Client ID */}
            <div style={{ marginBottom: '16px' }}>
              <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 600, color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                PAYPAL CLIENT ID
              </label>
              <input
                type="text"
                className="input-field"
                placeholder="例如: Acdw76..."
                value={paypalForm.paypal_client_id}
                onChange={(e) => setPaypalForm({ ...paypalForm, paypal_client_id: e.target.value })}
                style={{ width: '100%', fontFamily: 'monospace', fontSize: '0.82rem' }}
              />
            </div>

            {/* Secret Key */}
            <div style={{ marginBottom: '22px' }}>
              <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 600, color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                PAYPAL SECRET KEY
              </label>
              <div style={{ position: 'relative' }}>
                <input
                  type={showPaypalSecret ? 'text' : 'password'}
                  className="input-field"
                  placeholder="例如: EM89_..."
                  value={paypalForm.paypal_secret_key}
                  onChange={(e) => setPaypalForm({ ...paypalForm, paypal_secret_key: e.target.value })}
                  style={{ width: '100%', paddingRight: '40px', fontFamily: 'monospace', fontSize: '0.82rem' }}
                />
                <button
                  type="button"
                  onClick={() => setShowPaypalSecret(!showPaypalSecret)}
                  style={{
                    position: 'absolute',
                    right: '10px',
                    top: '50%',
                    transform: 'translateY(-50%)',
                    background: 'none',
                    border: 'none',
                    cursor: 'pointer',
                    color: 'hsl(var(--text-muted))',
                    display: 'flex',
                    alignItems: 'center',
                    padding: '4px',
                  }}
                  title={showPaypalSecret ? '隐藏密钥' : '显示密钥'}
                >
                  {showPaypalSecret ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </div>
            </div>
          </div>

          <button
            type="submit"
            disabled={paypalSaving}
            className="btn-primary"
            style={{
              width: '100%',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              gap: '8px',
              padding: '10px',
              fontSize: '0.88rem',
            }}
          >
            <Save size={16} /> {paypalSaving ? '正在保存...' : '保存 PayPal 配置'}
          </button>
        </form>

        {/* Stripe Configuration Card */}
        <form onSubmit={handleSaveStripe} className="glass-panel animate-fade-in" style={{
          padding: '24px',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'space-between',
          border: '1px solid hsl(var(--border))',
          backgroundColor: 'hsl(var(--bg-surface))',
          borderRadius: '16px',
        }}>
          <div>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '20px' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                <div style={{
                  width: '36px', height: '36px', borderRadius: '10px',
                  backgroundColor: 'rgba(99, 91, 255, 0.12)',
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                  color: '#635bff', fontWeight: 700
                }}>
                  <CreditCard size={18} />
                </div>
                <div>
                  <h3 style={{ fontSize: '1.1rem', fontWeight: 600, color: 'hsl(var(--text-primary))' }}>
                    Stripe 支付网关
                  </h3>
                  <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>
                    支持全球 Visa / MasterCard / Apple Pay 等快捷通道
                  </div>
                </div>
              </div>

              <span style={{
                fontSize: '0.72rem',
                fontWeight: 600,
                padding: '3px 8px',
                borderRadius: '6px',
                backgroundColor: 'rgba(99, 91, 255, 0.12)',
                color: '#635bff',
                border: '1px solid rgba(99, 91, 255, 0.25)'
              }}>
                Stripe Gateway
              </span>
            </div>

            {stripeSaveMsg && (
              <div style={{
                backgroundColor: 'rgba(16, 185, 129, 0.1)',
                border: '1px solid rgba(16, 185, 129, 0.25)',
                color: '#10b981',
                padding: '10px 14px',
                borderRadius: '8px',
                fontSize: '0.82rem',
                marginBottom: '16px',
                display: 'flex',
                alignItems: 'center',
                gap: '8px'
              }}>
                <CheckCircle2 size={16} /> {stripeSaveMsg}
              </div>
            )}

            {stripeSaveErr && (
              <div style={{
                backgroundColor: 'rgba(239, 68, 68, 0.1)',
                border: '1px solid rgba(239, 68, 68, 0.25)',
                color: '#f87171',
                padding: '10px 14px',
                borderRadius: '8px',
                fontSize: '0.82rem',
                marginBottom: '16px',
                display: 'flex',
                alignItems: 'center',
                gap: '8px'
              }}>
                <AlertCircle size={16} /> {stripeSaveErr}
              </div>
            )}

            {/* Publishable Key */}
            <div style={{ marginBottom: '16px' }}>
              <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 600, color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                STRIPE PUBLISHABLE KEY
              </label>
              <input
                type="text"
                className="input-field"
                placeholder="例如: pk_live_... 或 pk_test_..."
                value={stripeForm.stripe_publishable_key}
                onChange={(e) => setStripeForm({ ...stripeForm, stripe_publishable_key: e.target.value })}
                style={{ width: '100%', fontFamily: 'monospace', fontSize: '0.82rem' }}
              />
            </div>

            {/* Secret Key */}
            <div style={{ marginBottom: '22px' }}>
              <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 600, color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                STRIPE SECRET KEY
              </label>
              <div style={{ position: 'relative' }}>
                <input
                  type={showStripeSecret ? 'text' : 'password'}
                  className="input-field"
                  placeholder="例如: sk_live_... 或 sk_test_..."
                  value={stripeForm.stripe_secret_key}
                  onChange={(e) => setStripeForm({ ...stripeForm, stripe_secret_key: e.target.value })}
                  style={{ width: '100%', paddingRight: '40px', fontFamily: 'monospace', fontSize: '0.82rem' }}
                />
                <button
                  type="button"
                  onClick={() => setShowStripeSecret(!showStripeSecret)}
                  style={{
                    position: 'absolute',
                    right: '10px',
                    top: '50%',
                    transform: 'translateY(-50%)',
                    background: 'none',
                    border: 'none',
                    cursor: 'pointer',
                    color: 'hsl(var(--text-muted))',
                    display: 'flex',
                    alignItems: 'center',
                    padding: '4px',
                  }}
                  title={showStripeSecret ? '隐藏密钥' : '显示密钥'}
                >
                  {showStripeSecret ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </div>
            </div>
          </div>

          <button
            type="submit"
            disabled={stripeSaving}
            className="btn-primary"
            style={{
              width: '100%',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              gap: '8px',
              padding: '10px',
              fontSize: '0.88rem',
            }}
          >
            <Save size={16} /> {stripeSaving ? '正在保存...' : '保存 Stripe 配置'}
          </button>
        </form>

      </div>
    </div>
  );
}


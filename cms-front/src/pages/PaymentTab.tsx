import { useEffect, useState } from 'react';
import { CreditCard, Eye, EyeOff, Save } from 'lucide-react';
import { apiRequest } from '../utils/api';
import { StatusDot } from '../components/ui/Primitives';

export default function PaymentTab() {
  const [paypalForm, setPaypalForm] = useState({
    paypal_mode: 'sandbox',
    paypal_client_id: '',
    paypal_secret_key: '',
  });

  const [stripeForm, setStripeForm] = useState({
    stripe_publishable_key: '',
    stripe_secret_key: '',
  });

  const [showPaypalSecret, setShowPaypalSecret] = useState(false);
  const [showStripeSecret, setShowStripeSecret] = useState(false);
  const [configLoaded, setConfigLoaded] = useState(false);
  const [configLoadFailed, setConfigLoadFailed] = useState(false);
  const [paypalSaving, setPaypalSaving] = useState(false);
  const [stripeSaving, setStripeSaving] = useState(false);

  useEffect(() => {
    void apiRequest('GET', '/settings')
      .then((data: Record<string, string>) => {
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
          stripe_publishable_key: data.stripe_publishable_key || '',
          stripe_secret_key: data.stripe_secret_key || '',
        });
        setConfigLoaded(true);
      })
      .catch((err: unknown) => {
        setConfigLoadFailed(true);
        window.showToast?.((err instanceof Error ? err.message : '') || '加载支付网关配置失败，请刷新页面', 'error');
      });
  }, []);

  const handleSavePayPal = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!configLoaded || configLoadFailed || paypalSaving) return;
    setPaypalSaving(true);
    try {
      await apiRequest('POST', '/settings', {
        paypal_mode: paypalForm.paypal_mode,
        paypal_client_id: paypalForm.paypal_client_id.trim(),
        paypal_secret_key: paypalForm.paypal_secret_key.trim(),
        payment_sandbox_mode: paypalForm.paypal_mode === 'live' ? 'false' : 'true',
      });
      window.showToast?.('PayPal 网关配置已成功保存并即时生效！', 'success');
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '保存 PayPal 配置失败', 'error');
    } finally {
      setPaypalSaving(false);
    }
  };

  const handleSaveStripe = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!configLoaded || configLoadFailed || stripeSaving) return;
    setStripeSaving(true);
    try {
      await apiRequest('POST', '/settings', {
        stripe_publishable_key: stripeForm.stripe_publishable_key.trim(),
        stripe_secret_key: stripeForm.stripe_secret_key.trim(),
      });
      window.showToast?.('Stripe 网关配置已成功保存！', 'success');
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '保存 Stripe 配置失败', 'error');
    } finally {
      setStripeSaving(false);
    }
  };

  return (
    <div className="space-y-4 animate-fade-in">
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 items-stretch max-w-6xl">
        {/* PayPal Card */}
        <form onSubmit={handleSavePayPal} className="cms-card p-6 flex flex-col justify-between gap-6">
          <div className="space-y-5">
            <div className="flex items-start justify-between gap-3">
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-xl nav-active-item font-bold text-sm flex items-center justify-center shrink-0">
                  PP
                </div>
                <div>
                  <h2 className="text-base font-semibold text-[var(--cms-fg)] m-0">PayPal 支付网关</h2>
                  <p className="text-xs text-[var(--cms-muted-fg)] mt-0.5 m-0">全球主流电子钱包与信用卡收单</p>
                </div>
              </div>
              <StatusDot
                tone={paypalForm.paypal_mode === 'live' ? 'success' : 'warning'}
                label={paypalForm.paypal_mode === 'live' ? '正式生产 (Live)' : '沙盒环境 (Sandbox)'}
              />
            </div>

            <div className="space-y-2 text-xs">
              <span className="block font-medium text-[var(--cms-muted-fg)]">运行环境 (MODE)</span>
              <div className="grid grid-cols-2 gap-2.5">
                <button
                  type="button"
                  onClick={() => setPaypalForm({ ...paypalForm, paypal_mode: 'sandbox' })}
                  className={`focus-ring p-3.5 rounded-xl border text-left cursor-pointer transition-colors ${
                    paypalForm.paypal_mode === 'sandbox'
                      ? 'border-[var(--cms-warning)] bg-[color-mix(in_srgb,var(--cms-warning)_8%,transparent)]'
                      : 'border-[var(--cms-border)] bg-[var(--cms-content)]'
                  }`}
                >
                  <div className="font-semibold text-xs text-[var(--cms-fg)]">Sandbox（沙盒）</div>
                  <div className="text-xs text-[var(--cms-muted-fg)] mt-0.5">用于开发与模拟扣款</div>
                </button>
                <button
                  type="button"
                  onClick={() => setPaypalForm({ ...paypalForm, paypal_mode: 'live' })}
                  className={`focus-ring p-3.5 rounded-xl border text-left cursor-pointer transition-colors ${
                    paypalForm.paypal_mode === 'live'
                      ? 'border-[var(--cms-success)] bg-[color-mix(in_srgb,var(--cms-success)_8%,transparent)]'
                      : 'border-[var(--cms-border)] bg-[var(--cms-content)]'
                  }`}
                >
                  <div className="font-semibold text-xs text-[var(--cms-fg)]">Live（生产）</div>
                  <div className="text-xs text-[var(--cms-muted-fg)] mt-0.5">真实向海外用户收款</div>
                </button>
              </div>
            </div>

            <label className="block space-y-1.5 text-xs">
              <span className="block font-medium text-[var(--cms-muted-fg)]">PAYPAL CLIENT ID</span>
              <input
                type="text"
                className="input-field font-mono-code"
                placeholder="例如: Acdw76..."
                value={paypalForm.paypal_client_id}
                onChange={(e) => setPaypalForm({ ...paypalForm, paypal_client_id: e.target.value })}
              />
            </label>

            <label className="block space-y-1.5 text-xs">
              <span className="block font-medium text-[var(--cms-muted-fg)]">PAYPAL SECRET KEY</span>
              <div className="relative">
                <input
                  type={showPaypalSecret ? 'text' : 'password'}
                  className="input-field font-mono-code"
                  style={{ paddingRight: '40px' }}
                  placeholder="例如: EM89_..."
                  value={paypalForm.paypal_secret_key}
                  onChange={(e) => setPaypalForm({ ...paypalForm, paypal_secret_key: e.target.value })}
                />
                <button
                  type="button"
                  onClick={() => setShowPaypalSecret((s) => !s)}
                  aria-label={showPaypalSecret ? '隐藏密钥' : '显示密钥'}
                  className="focus-ring absolute right-2.5 top-1/2 -translate-y-1/2 p-1 rounded text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] cursor-pointer"
                >
                  {showPaypalSecret ? <EyeOff size={15} /> : <Eye size={15} />}
                </button>
              </div>
            </label>
          </div>

          <button type="submit" disabled={!configLoaded || configLoadFailed || paypalSaving} className="btn-primary w-full h-10">
            <Save size={15} aria-hidden="true" />
            <span>{paypalSaving ? '正在保存...' : '保存 PayPal 配置'}</span>
          </button>
        </form>

        {/* Stripe Card */}
        <form onSubmit={handleSaveStripe} className="cms-card p-6 flex flex-col justify-between gap-6">
          <div className="space-y-5">
            <div className="flex items-start justify-between gap-3">
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-xl nav-active-item flex items-center justify-center shrink-0">
                  <CreditCard size={18} aria-hidden="true" />
                </div>
                <div>
                  <h2 className="text-base font-semibold text-[var(--cms-fg)] m-0">Stripe 支付网关</h2>
                  <p className="text-xs text-[var(--cms-muted-fg)] mt-0.5 m-0">支持 Visa / MasterCard / Apple Pay 等通道</p>
                </div>
              </div>
              <StatusDot tone="primary" label="Stripe Gateway" />
            </div>

            <label className="block space-y-1.5 text-xs">
              <span className="block font-medium text-[var(--cms-muted-fg)]">STRIPE PUBLISHABLE KEY</span>
              <input
                type="text"
                className="input-field font-mono-code"
                placeholder="例如: pk_live_... 或 pk_test_..."
                value={stripeForm.stripe_publishable_key}
                onChange={(e) => setStripeForm({ ...stripeForm, stripe_publishable_key: e.target.value })}
              />
            </label>

            <label className="block space-y-1.5 text-xs">
              <span className="block font-medium text-[var(--cms-muted-fg)]">STRIPE SECRET KEY</span>
              <div className="relative">
                <input
                  type={showStripeSecret ? 'text' : 'password'}
                  className="input-field font-mono-code"
                  style={{ paddingRight: '40px' }}
                  placeholder="例如: sk_live_... 或 sk_test_..."
                  value={stripeForm.stripe_secret_key}
                  onChange={(e) => setStripeForm({ ...stripeForm, stripe_secret_key: e.target.value })}
                />
                <button
                  type="button"
                  onClick={() => setShowStripeSecret((s) => !s)}
                  aria-label={showStripeSecret ? '隐藏密钥' : '显示密钥'}
                  className="focus-ring absolute right-2.5 top-1/2 -translate-y-1/2 p-1 rounded text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] cursor-pointer"
                >
                  {showStripeSecret ? <EyeOff size={15} /> : <Eye size={15} />}
                </button>
              </div>
            </label>
          </div>

          <button type="submit" disabled={!configLoaded || configLoadFailed || stripeSaving} className="btn-primary w-full h-10">
            <Save size={15} aria-hidden="true" />
            <span>{stripeSaving ? '正在保存...' : '保存 Stripe 配置'}</span>
          </button>
        </form>
      </div>
    </div>
  );
}

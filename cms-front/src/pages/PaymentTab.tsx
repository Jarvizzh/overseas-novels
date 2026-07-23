import { useState, useEffect } from 'react';
import { apiRequest } from '../utils/api';
import { CreditCard, Save } from 'lucide-react';

export default function PaymentTab() {
  const [opsForm, setOpsForm] = useState({
    stripe_publishable_key: '',
    stripe_secret_key: '',
    paypal_client_id: '',
    paypal_secret_key: '',
    payment_sandbox_mode: 'true',
    checkin_reward_gradient: ''
  });
  const [saveMessage, setSaveMessage] = useState('');
  const [saveError, setSaveError] = useState('');

  const fetchConfigs = async () => {
    try {
      const data = await apiRequest('GET', '/settings');
      setOpsForm(prev => ({ ...prev, ...data }));
    } catch (err: any) {
      console.error('Failed to load operational configurations:', err);
    }
  };

  useEffect(() => {
    fetchConfigs();
  }, []);

  const handleSaveOps = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaveMessage('');
    setSaveError('');
    try {
      await apiRequest('POST', '/settings', opsForm);
      setSaveMessage('配置已成功保存！');
    } catch (err: any) {
      setSaveError(err.message || '保存配置失败');
    }
  };

  return (
    <div className="animate-fade-in" style={{ padding: '16px 24px 24px 24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">
          支付管理
        </h1>
      </div>

      <div className="animate-fade-in" style={{ display: 'flex', flexDirection: 'column', gap: '20px', maxWidth: '900px' }}>
        {saveMessage && (
          <div style={{ backgroundColor: 'rgba(16, 185, 129, 0.1)', border: '1px solid rgba(16, 185, 129, 0.25)', color: '#10b981', padding: '12px', borderRadius: '8px', fontSize: '0.85rem' }}>
            {saveMessage}
          </div>
        )}

        {saveError && (
          <div style={{ backgroundColor: 'rgba(239, 68, 68, 0.1)', border: '1px solid rgba(239, 68, 68, 0.25)', color: '#f87171', padding: '12px', borderRadius: '8px', fontSize: '0.85rem' }}>
            {saveError}
          </div>
        )}

        <form onSubmit={handleSaveOps} className="glass-panel animate-fade-in" style={{ padding: '24px' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '18px' }}>
            <h3 style={{ fontSize: '1.2rem', display: 'flex', alignItems: 'center', gap: '8px' }}>
              <CreditCard size={18} style={{ color: 'hsl(var(--primary))' }} />
              支付配置 (Stripe & PayPal)
            </h3>
            <label style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.8rem', cursor: 'pointer' }}>
              <input 
                type="checkbox" 
                checked={opsForm.payment_sandbox_mode === 'true'} 
                onChange={(e) => setOpsForm({...opsForm, payment_sandbox_mode: e.target.checked ? 'true' : 'false'})}
                style={{ cursor: 'pointer' }}
              />
              开启沙盒测试模式 (Sandbox Mode)
            </label>
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: '16px', marginBottom: '24px' }}>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px' }}>
              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>STRIPE PUBLISHABLE KEY</label>
                <input type="text" className="input-field" placeholder="pk_test_..." value={opsForm.stripe_publishable_key} onChange={(e) => setOpsForm({...opsForm, stripe_publishable_key: e.target.value})} />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>STRIPE SECRET KEY</label>
                <input type="password" className="input-field" placeholder="sk_test_..." value={opsForm.stripe_secret_key} onChange={(e) => setOpsForm({...opsForm, stripe_secret_key: e.target.value})} />
              </div>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px' }}>
              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>PAYPAL CLIENT ID</label>
                <input type="text" className="input-field" placeholder="Client ID" value={opsForm.paypal_client_id} onChange={(e) => setOpsForm({...opsForm, paypal_client_id: e.target.value})} />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>PAYPAL SECRET KEY</label>
                <input type="password" className="input-field" placeholder="Secret Key" value={opsForm.paypal_secret_key} onChange={(e) => setOpsForm({...opsForm, paypal_secret_key: e.target.value})} />
              </div>
            </div>
          </div>

          <button type="submit" className="btn-primary" style={{ width: '100%', display: 'flex', gap: '8px' }}>
            <Save size={16} /> 保存支付网关配置
          </button>
        </form>
      </div>
    </div>
  );
}

import { useState, useEffect } from 'react';
import { apiRequest } from '../utils/api';
import { Award, Save } from 'lucide-react';

export default function CampaignTab() {
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
    <div className="animate-fade-in" style={{ padding: '24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">
          活动配置
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
          <h3 style={{ fontSize: '1.2rem', marginBottom: '18px', display: 'flex', alignItems: 'center', gap: '8px' }}>
            <Award size={18} style={{ color: 'hsl(var(--primary))' }} />
            活动配置 (签到福利)
          </h3>

          <div style={{ marginBottom: '24px' }}>
            <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>连续签到福利金币奖励表 (第1至第7天)</label>
            <input type="text" className="input-field" placeholder="e.g. 第1天: 10, 第2天: 15..." value={opsForm.checkin_reward_gradient} onChange={(e) => setOpsForm({...opsForm, checkin_reward_gradient: e.target.value})} />
            <span style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-muted))', marginTop: '4px' }}>签到奖励福利参数修改后，移动端签到代币梯度分发会即时生效。</span>
          </div>

          <button type="submit" className="btn-primary" style={{ width: '100%', display: 'flex', gap: '8px' }}>
            <Save size={16} /> 保存签到福利配置
          </button>
        </form>
      </div>
    </div>
  );
}

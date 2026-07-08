import { useEffect, useState } from 'react';
import { apiRequest } from '../utils/api';
import { CreditCard, Activity, ChevronLeft, ChevronRight } from 'lucide-react';
import CustomSelect from '../components/CustomSelect';

const paymentMethodOptions = [
  { value: 'stripe', label: 'Stripe 支付网关' },
  { value: 'paypal', label: 'PayPal 支付网关' }
];

const actionTypeOptions = [
  { value: 'Paid', label: 'payment_intent.succeeded (支付成功/发放金币)' },
  { value: 'Refunded', label: 'charge.refunded / 发生拒付 (退款扣减金币)' }
];

const statusFilterOptions = [
  { value: '', label: '所有订单状态' },
  { value: 'Success', label: '交易成功 (Paid)' },
  { value: 'Refunded', label: '已退款 (Refunded)' },
  { value: 'Pending', label: '待付款 (Pending)' },
  { value: 'Failed', label: '支付失败 (Failed)' }
];

interface Order {
  id: number | string;
  user_id: string;
  external_ref_id: string;
  amount_cents: number;
  currency: string;
  coins: number;
  bonus_coins_credited: number;
  payment_method: string;
  status: string; // Success, Refunded, Pending, Failed
  utm_source: string;
  utm_campaign: string;
  paid_at?: string;
  order_type?: string;
  promotion_link_id?: number | null;
  novel_id?: number | null;
  created_at: string;
}

export default function OrdersTab() {
  const [orders, setOrders] = useState<Order[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState('');
  const [searchUserID, setSearchUserID] = useState('');
  const [orderTypeFilter, setOrderTypeFilter] = useState('');
  const [promotionLinkFilter, setPromotionLinkFilter] = useState('');
  const [paidStartFilter, setPaidStartFilter] = useState('');
  const [paidEndFilter, setPaidEndFilter] = useState('');
  const [loading, setLoading] = useState(true);

  // Mock webhook form state
  const [showMockForm, setShowMockForm] = useState(false);
  const [mockUserID, setMockUserID] = useState('');
  const [mockAmount, setMockAmount] = useState('9.99');
  const [mockCoins, setMockCoins] = useState(990);
  const [mockBonusCoins, setMockBonusCoins] = useState(100);
  const [mockMethod, setMockMethod] = useState<'stripe' | 'paypal'>('stripe');
  const [mockStatus, setMockStatus] = useState<'Paid' | 'Refunded'>('Paid');
  const [mockUtmSource, setMockUtmSource] = useState('facebook');
  const [mockUtmCampaign, setMockUtmCampaign] = useState('romance_novels_fb_ad');
  const [mockSuccessMsg, setMockSuccessMsg] = useState('');

  useEffect(() => {
    fetchOrders();
  }, [page, statusFilter, searchUserID, orderTypeFilter, promotionLinkFilter, paidStartFilter, paidEndFilter]);

  const fetchOrders = async () => {
    setLoading(true);
    try {
      let path = `/orders?page=${page}`;
      if (statusFilter) path += `&status=${statusFilter}`;
      if (searchUserID) path += `&user_id=${searchUserID}`;
      if (orderTypeFilter) path += `&order_type=${orderTypeFilter}`;
      if (promotionLinkFilter) path += `&promotion_link_id=${promotionLinkFilter}`;
      if (paidStartFilter) path += `&paid_start=${paidStartFilter}T00:00:00Z`;
      if (paidEndFilter) path += `&paid_end=${paidEndFilter}T23:59:59Z`;

      const data = await apiRequest('GET', path);
      setOrders(data.orders || []);
      setTotal(data.total || 0);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  const handleMockWebhookSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!mockUserID) {
      window.showToast?.('请输入接收金币的用户 ID', 'error');
      return;
    }
    setMockSuccessMsg('');
    try {
      const cents = Math.round(parseFloat(mockAmount) * 100);
      const data = await apiRequest('POST', '/orders/mock-webhook', {
        user_id: mockUserID,
        amount_cents: cents,
        charged_coins: mockCoins,
        bonus_coins: mockBonusCoins,
        payment_method: mockMethod,
        status: mockStatus,
        utm_source: mockUtmSource,
        utm_campaign: mockUtmCampaign
      });

      setMockSuccessMsg(`已发送模拟网关 Webhook 信号！处理结果: ${data.message} (${reqMethodDesc()})`);
      fetchOrders();
    } catch (err: any) {
      window.showToast?.(err.message || '分发模拟 Webhook 失败', 'error');
    }
  };

  const reqMethodDesc = () => {
    return mockStatus === 'Paid'
      ? `增加账户本金 +${mockCoins} 币 / 赠送金币 +${mockBonusCoins} 币`
      : `扣减用户钱包中已退款的小说代币`;
  };

  const autofillFirstUser = async () => {
    try {
      const data = await apiRequest('GET', '/users?page_size=1');
      if (data && data.users && data.users.length > 0) {
        setMockUserID(data.users[0].id);
      } else {
        window.showToast?.('当前没有已注册用户，请先在 H5 端创建一个游客账号。', 'info');
      }
    } catch (e) {
      console.error(e);
    }
  };

  if (loading && orders.length === 0) {
    return <div style={{ color: 'hsl(var(--text-secondary))', padding: '20px' }}>正在加载充值交易记录...</div>;
  }

  return (
    <div className="animate-fade-in" style={{ padding: '24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">订单管理</h1>

        </div>
        <button onClick={() => setShowMockForm(!showMockForm)} className="btn-primary" style={{ display: 'flex', gap: '6px' }}>
          <Activity size={16} /> Webhook 支付网关模拟器
        </button>
      </div>

      {/* Simulator Section */}
      {showMockForm && (
        <form onSubmit={handleMockWebhookSubmit} className="glass-panel" style={{ padding: '24px', marginBottom: '30px' }}>
          <h3 style={{ marginBottom: '18px', display: 'flex', alignItems: 'center', gap: '8px' }}>
            <Activity size={18} color="hsl(var(--accent-pink))" />
            模拟 Stripe & PayPal 支付网关回调分发
          </h3>

          {mockSuccessMsg && (
            <div style={{ backgroundColor: 'rgba(16, 185, 129, 0.1)', border: '1px solid rgba(16, 185, 129, 0.25)', color: '#34d399', padding: '12px', borderRadius: '8px', fontSize: '0.85rem', marginBottom: '18px' }}>
              {mockSuccessMsg}
            </div>
          )}

          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: '16px', marginBottom: '18px' }}>
            <div>
              <label style={{ display: 'flex', justifyContent: 'space-between', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                购买用户 ID
                <span onClick={autofillFirstUser} style={{ color: 'hsl(var(--primary))', cursor: 'pointer', textDecoration: 'underline' }}>自动填充首个用户</span>
              </label>
              <input type="text" className="input-field" placeholder="e.g. guest_uuid" value={mockUserID} onChange={(e) => setMockUserID(e.target.value)} required />
            </div>
            <div>
              <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>充值金额 (USD)</label>
              <input type="text" className="input-field" value={mockAmount} onChange={(e) => setMockAmount(e.target.value)} required />
            </div>
            <div>
              <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>发放充值本金数</label>
              <input type="number" className="input-field" value={mockCoins} onChange={(e) => setMockCoins(Number(e.target.value))} required />
            </div>
            <div>
              <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>赠送金币数</label>
              <input type="number" className="input-field" value={mockBonusCoins} onChange={(e) => setMockBonusCoins(Number(e.target.value))} required />
            </div>
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: '16px', marginBottom: '24px' }}>
            <div>
              <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>支付渠道</label>
              <CustomSelect
                options={paymentMethodOptions}
                value={mockMethod}
                onChange={(val) => setMockMethod(val as any)}
                width="100%"
              />
            </div>
            <div>
              <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>网关动作类型</label>
              <CustomSelect
                options={actionTypeOptions}
                value={mockStatus}
                onChange={(val) => setMockStatus(val as any)}
                width="100%"
              />
            </div>
            <div>
              <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>媒体渠道来源 (UTM Source)</label>
              <input type="text" className="input-field" value={mockUtmSource} onChange={(e) => setMockUtmSource(e.target.value)} />
            </div>
            <div>
              <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>广告活动名称 (UTM Campaign)</label>
              <input type="text" className="input-field" value={mockUtmCampaign} onChange={(e) => setMockUtmCampaign(e.target.value)} />
            </div>
          </div>

          <div style={{ display: 'flex', gap: '12px', justifyContent: 'flex-end' }}>
            <button type="button" className="btn-secondary" onClick={() => setShowMockForm(false)}>关闭</button>
            <button type="submit" className="btn-primary">发送模拟 Webhook 信号</button>
          </div>
        </form>
      )}

      {/* Filter and Search Bar */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: '16px', marginBottom: '24px', position: 'relative', zIndex: 10 }}>
        {/* User ID */}
        <div>
          <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>用户 ID</label>
          <input
            type="text"
            className="input-field"
            placeholder="输入精确用户 ID"
            value={searchUserID}
            onChange={(e) => {
              setSearchUserID(e.target.value);
              setPage(1);
            }}
          />
        </div>

        {/* Order Status */}
        <div>
          <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>订单状态</label>
          <CustomSelect
            options={statusFilterOptions}
            value={statusFilter}
            onChange={(val) => {
              setStatusFilter(val);
              setPage(1);
            }}
            width="100%"
            placeholder="所有订单状态"
          />
        </div>

        {/* Order Type */}
        <div>
          <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>订单类型</label>
          <CustomSelect
            options={[
              { value: '', label: '所有订单类型' },
              { value: 'single', label: '金币充值 (single)' },
              { value: 'vip', label: 'VIP订阅 (vip)' },
              { value: 'whole_book', label: '整本购买 (whole_book)' }
            ]}
            value={orderTypeFilter}
            onChange={(val) => {
              setOrderTypeFilter(val);
              setPage(1);
            }}
            width="100%"
            placeholder="所有订单类型"
          />
        </div>

        {/* Promotion Link ID */}
        <div>
          <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>推广链接 ID</label>
          <input
            type="text"
            className="input-field"
            placeholder="输入推广链接 ID"
            value={promotionLinkFilter}
            onChange={(e) => {
              setPromotionLinkFilter(e.target.value);
              setPage(1);
            }}
          />
        </div>

        {/* Paid Start Time */}
        <div>
          <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>支付时间 (起)</label>
          <input
            type="date"
            className="input-field"
            value={paidStartFilter}
            onChange={(e) => {
              setPaidStartFilter(e.target.value);
              setPage(1);
            }}
          />
        </div>

        {/* Paid End Time */}
        <div>
          <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>支付时间 (止)</label>
          <input
            type="date"
            className="input-field"
            value={paidEndFilter}
            onChange={(e) => {
              setPaidEndFilter(e.target.value);
              setPage(1);
            }}
          />
        </div>
      </div>

      {/* Orders Table */}
      <div className="glass-panel" style={{ padding: '8px', overflowX: 'auto' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left' }}>
          <thead>
            <tr style={{ borderBottom: '1px solid hsl(var(--border))', color: 'hsl(var(--text-secondary))', fontSize: '0.8rem' }}>
              <th style={{ padding: '12px 16px' }}>订单 ID</th>
              <th style={{ padding: '12px 16px' }}>用户 ID</th>
              <th style={{ padding: '12px 16px' }}>流水凭证</th>
              <th style={{ padding: '12px 16px' }}>订单类型</th>
              <th style={{ padding: '12px 16px' }}>所得金币</th>
              <th style={{ padding: '12px 16px' }}>支付金额</th>
              <th style={{ padding: '12px 16px' }}>推广/书籍</th>
              <th style={{ padding: '12px 16px' }}>创建时间</th>
              <th style={{ padding: '12px 16px' }}>支付时间</th>
              <th style={{ padding: '12px 16px' }}>订单状态</th>
            </tr>
          </thead>
          <tbody>
            {orders.length === 0 ? (
              <tr>
                <td colSpan={10} style={{ padding: '20px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>暂无充值交易订单记录。</td>
              </tr>
            ) : (
              orders.map((o) => (
                <tr key={o.id} style={{ borderBottom: '1px solid hsl(var(--border) / 0.5)', fontSize: '0.9rem', color: 'hsl(var(--text-secondary))' }}>
                  <td style={{ padding: '14px 16px', fontFamily: 'monospace', fontSize: '0.8rem' }} title={String(o.id)}>
                    {o.id}
                  </td>
                  <td style={{ padding: '14px 16px', fontFamily: 'monospace', fontSize: '0.8rem' }}>{o.user_id}</td>
                  <td style={{ padding: '14px 16px', color: 'hsl(var(--text-secondary))' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                      <CreditCard size={14} color="hsl(var(--text-muted))" />
                      <span style={{ fontSize: '0.8rem' }}>{o.payment_method.toUpperCase()}: {o.external_ref_id || '无'}</span>
                    </div>
                  </td>
                  <td style={{ padding: '14px 16px' }}>
                    <span className={`badge ${o.order_type === 'whole_book' ? 'badge-blue' : 'badge-gray'}`} style={{ fontSize: '0.75rem' }}>
                      {o.order_type === 'vip' ? 'VIP订阅' : o.order_type === 'whole_book' ? '整本购买' : '金币充值'}
                    </span>
                  </td>
                  <td style={{ padding: '14px 16px', fontSize: '0.8rem' }}>
                    <span>
                      {o.coins}
                      {o.bonus_coins_credited > 0 && `+${o.bonus_coins_credited}`}
                    </span>
                  </td>
                  <td style={{ padding: '14px 16px', fontSize: '0.8rem', fontWeight: 500 }}>
                    ${(o.amount_cents / 100).toFixed(2)}
                  </td>
                  <td style={{ padding: '14px 16px' }}>
                    {o.promotion_link_id || o.novel_id ? (
                      <div style={{ fontSize: '0.8rem' }}>
                        {o.promotion_link_id && <div>推广: #{o.promotion_link_id}</div>}
                        {o.novel_id && <div style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.75rem' }}>书籍: #{o.novel_id}</div>}
                      </div>
                    ) : (
                      <span style={{ color: 'hsl(var(--text-muted))' }}>-</span>
                    )}
                  </td>
                  <td style={{ padding: '14px 16px', fontSize: '0.8rem' }}>
                    {new Date(o.created_at).toLocaleString('zh-CN', { hour12: false })}
                  </td>
                  <td style={{ padding: '14px 16px', fontSize: '0.8rem' }}>
                    {o.paid_at ? (
                      <span>
                        {new Date(o.paid_at).toLocaleString('zh-CN', { hour12: false })}
                      </span>
                    ) : (
                      <span style={{ color: 'hsl(var(--text-muted))' }}>未支付</span>
                    )}
                  </td>
                  <td style={{ padding: '14px 16px' }}>
                    <span className={`badge ${o.status === 'Success' || o.status === 'Paid' ? 'badge-green' :
                      o.status === 'Refunded' ? 'badge-red' :
                        o.status === 'Pending' ? 'badge-orange' : 'badge-gray'
                      }`} style={{ fontSize: '0.7rem' }}>
                      {o.status === 'Success' || o.status === 'Paid' ? '支付成功' :
                        o.status === 'Refunded' ? '已退款' :
                          o.status === 'Pending' ? '待付款' : o.status}
                    </span>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>

        {/* Pagination Footer */}
        {!loading && orders.length > 0 && (
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px 16px', borderTop: '1px solid hsl(var(--border) / 0.5)' }}>
            <span style={{ fontSize: '0.8rem', color: 'hsl(var(--text-secondary))' }}>
              共 {total} 个订单，第 {page} / {Math.ceil(total / 10) || 1} 页
            </span>
            <div style={{ display: 'flex', gap: '8px' }}>
              <button
                className="btn-secondary"
                disabled={page <= 1}
                onClick={() => setPage(page - 1)}
                style={{ padding: '6px 12px', fontSize: '0.8rem', borderRadius: '6px', display: 'inline-flex', alignItems: 'center', gap: '4px' }}
              >
                <ChevronLeft size={14} /> 上一页
              </button>
              <button
                className="btn-secondary"
                disabled={page >= (Math.ceil(total / 10) || 1)}
                onClick={() => setPage(page + 1)}
                style={{ padding: '6px 12px', fontSize: '0.8rem', borderRadius: '6px', display: 'inline-flex', alignItems: 'center', gap: '4px' }}
              >
                下一页 <ChevronRight size={14} />
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

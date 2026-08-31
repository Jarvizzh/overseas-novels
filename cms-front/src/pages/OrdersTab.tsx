import { useEffect, useState } from 'react';
import { apiRequest } from '../utils/api';
import { CreditCard, ChevronLeft, ChevronRight, Eye } from 'lucide-react';
import CustomSelect from '../components/CustomSelect';
import { ThirdPartyPaymentModal, type ThirdPartyPaymentDetail } from '../components/ThirdPartyPaymentModal';

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

  // Third-party payment details modal state
  const [detailModalOpen, setDetailModalOpen] = useState(false);
  const [selectedOrderForDetail, setSelectedOrderForDetail] = useState<number | string | null>(null);
  const [paymentDetails, setPaymentDetails] = useState<ThirdPartyPaymentDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const handleOpenPaymentDetails = async (orderId: number | string) => {
    setSelectedOrderForDetail(orderId);
    setDetailModalOpen(true);
    setDetailLoading(true);
    try {
      const res = await apiRequest('GET', `/orders/${orderId}/payment-details`);
      setPaymentDetails(res.details || null);
    } catch (err: any) {
      console.error("Failed to fetch payment details:", err);
      setPaymentDetails(null);
    } finally {
      setDetailLoading(false);
    }
  };

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

  if (loading && orders.length === 0) {
    return <div style={{ color: 'hsl(var(--text-secondary))', padding: '20px' }}>正在加载充值交易记录...</div>;
  }

  return (
    <div className="animate-fade-in" style={{ padding: '16px 24px 24px 24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">订单管理</h1>
        </div>
      </div>

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
              <th style={{ padding: '12px 16px', textAlign: 'center' }}>操作</th>
            </tr>
          </thead>
          <tbody>
            {orders.length === 0 ? (
              <tr>
                <td colSpan={11} style={{ padding: '20px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>暂无充值交易订单记录。</td>
              </tr>
            ) : (
              orders.map((o) => (
                <tr key={o.id} style={{ borderBottom: '1px solid hsl(var(--border) / 0.5)', fontSize: '0.9rem', color: 'hsl(var(--text-secondary))' }}>
                  <td style={{ padding: '14px 16px', fontFamily: 'monospace', fontSize: '0.8rem' }} title={String(o.id)}>
                    {o.id}
                  </td>
                  <td style={{ padding: '14px 16px', fontFamily: 'monospace', fontSize: '0.8rem' }}>{o.user_id}</td>
                  <td style={{ padding: '14px 16px', color: 'hsl(var(--text-secondary))' }}>
                    <div
                      style={{ display: 'flex', alignItems: 'center', gap: '6px', cursor: 'pointer' }}
                      onClick={() => handleOpenPaymentDetails(o.id)}
                      title="点击查看第三方支付凭据"
                    >
                      <CreditCard size={14} style={{ color: 'hsl(var(--primary))' }} />
                      <span style={{ fontSize: '0.8rem', textDecoration: 'underline', textUnderlineOffset: '2px' }}>
                        {o.payment_method.toUpperCase()}: {o.external_ref_id || '无'}
                      </span>
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
                  <td style={{ padding: '14px 16px', textAlign: 'center' }}>
                    <button
                      className="btn-secondary"
                      onClick={() => handleOpenPaymentDetails(o.id)}
                      style={{
                        padding: '4px 10px',
                        fontSize: '0.75rem',
                        borderRadius: '6px',
                        display: 'inline-flex',
                        alignItems: 'center',
                        gap: '4px',
                        cursor: 'pointer'
                      }}
                      title="查看第三方支付凭据明细"
                    >
                      <Eye size={13} style={{ color: 'hsl(var(--primary))' }} />
                      <span>明细</span>
                    </button>
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

      {/* Third Party Payment Details Modal */}
      <ThirdPartyPaymentModal
        isOpen={detailModalOpen}
        onClose={() => {
          setDetailModalOpen(false);
          setPaymentDetails(null);
        }}
        orderId={selectedOrderForDetail}
        loading={detailLoading}
        details={paymentDetails}
        onRefresh={() => {
          if (selectedOrderForDetail) {
            handleOpenPaymentDetails(selectedOrderForDetail);
          }
        }}
      />
    </div>
  );
}

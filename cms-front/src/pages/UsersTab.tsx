import { useEffect, useState } from 'react';
import { apiRequest } from '../utils/api';
import { Search, ArrowLeft, Ban, DollarSign, Wallet, BookOpen, ChevronLeft, ChevronRight, Crown, CheckCircle2, AlertCircle } from 'lucide-react';
import CustomSelect from '../components/CustomSelect';

export interface UserSubscriptionItem {
  id: number;
  subscription_id: string;
  plan_id: string;
  status: string; // ACTIVE, CANCELLED, EXPIRED, PENDING, SUSPENDED
  cycle: string; // day, week, month
  price_cents: number;
  currency: string;
  payment_method: string;
  current_period_start?: string;
  current_period_end?: string;
  next_billing_time?: string;
  last_payment_time?: string;
  cancelled_at?: string;
  created_at: string;
}

interface User {
  id: number;
  email: string;
  nickname: string;
  avatar_url: string;
  status: number; // 1-Normal, 2-Banned
  device?: string;
  ip_address?: string;
  utm_source?: string;
  utm_campaign?: string;
  total_recharge?: number;
  balance?: number;
  total_spent?: number;
  recently_read_book?: string;
  is_vip?: boolean;
  vip_cycle?: string;
  vip_expire_at?: string;
  vip_status?: string;
  created_at: string;
}

interface BookshelfItem {
  novel_id: number;
  novel_title: string;
  cover_url: string;
  chapter_index: number;
  scroll_offset_percentage: number;
  in_shelf: boolean;
  updated_at: string;
}

interface UserDetail {
  id: number;
  email: string;
  nickname: string;
  avatar_url: string;
  status: number;
  device?: string;
  ip_address?: string;
  utm_source?: string;
  utm_campaign?: string;
  total_recharge?: number;
  balance?: number;
  total_spent?: number;
  recently_read_book?: string;
  is_vip?: boolean;
  vip_cycle?: string;
  vip_expire_at?: string;
  vip_status?: string;
  created_at: string;
  charged_coins: number;
  bonus_coins: number;
  bookshelf: BookshelfItem[];
  subscriptions?: UserSubscriptionItem[];
  active_subscription?: UserSubscriptionItem;
}

export default function UsersTab() {
  const [users, setUsers] = useState<User[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [loading, setLoading] = useState(true);
  const [selectedUser, setSelectedUser] = useState<UserDetail | null>(null);

  // Bookshelf pagination states
  const [shelfPage, setShelfPage] = useState(1);
  const shelfPageSize = 5;

  const totalShelfPages = (selectedUser && selectedUser.bookshelf) ? Math.ceil(selectedUser.bookshelf.length / shelfPageSize) : 0;
  const startShelfIndex = (shelfPage - 1) * shelfPageSize;
  const paginatedBookshelf = (selectedUser && selectedUser.bookshelf) ? selectedUser.bookshelf.slice(startShelfIndex, startShelfIndex + shelfPageSize) : [];

  // Coin Adjustment Form State
  const [adjustAmount, setAdjustAmount] = useState(100);
  const [adjustType, setAdjustType] = useState<'bonus' | 'charged'>('bonus');
  const [adjustReason, setAdjustReason] = useState('每日签到异常补偿发放');
  const [adjustSuccess, setAdjustSuccess] = useState('');

  useEffect(() => {
    fetchUsers();
  }, [page, search]);

  const fetchUsers = async () => {
    setLoading(true);
    try {
      const data = await apiRequest('GET', `/users?page=${page}&search=${search}`);
      setUsers(data.users || []);
      setTotal(data.total || 0);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  const handleUserClick = async (userID: number) => {
    setLoading(true);
    setAdjustSuccess('');
    try {
      const data = await apiRequest('GET', `/users/${userID}`);
      setSelectedUser(data);
      setShelfPage(1); // Reset shelf page index
    } catch (e) {
      window.showToast?.('加载用户详细信息失败: ' + e, 'error');
    } finally {
      setLoading(false);
    }
  };

  const handleToggleStatus = async (user: UserDetail) => {
    const nextStatus = user.status === 1 ? 2 : 1;
    const confirmMsg = nextStatus === 2
      ? '您确定要封禁该用户账号吗？封禁后该用户将无法再次登录 H5 客户端或阅读任何小说章节！'
      : '您确定要解除该用户的封禁状态吗？';

    window.showConfirm?.(confirmMsg, async () => {
      try {
        await apiRequest('PUT', `/users/${user.id}/status`, { status: nextStatus });
        setSelectedUser({ ...user, status: nextStatus });
        fetchUsers();
      } catch (err: any) {
        window.showToast?.(err.message, 'error');
      }
    });
  };

  const handleAdjustWallet = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedUser) return;
    try {
      const amountToSend = adjustAmount;
      await apiRequest('POST', `/users/${selectedUser.id}/adjust-wallet`, {
        amount: amountToSend,
        is_bonus: adjustType === 'bonus',
        reason: adjustReason
      });

      setAdjustSuccess('钱包金币余额调整成功！已录入系统日志。');
      setAdjustReason('');

      const data = await apiRequest('GET', `/users/${selectedUser.id}`);
      setSelectedUser(data);
    } catch (err: any) {
      window.showToast?.(err.message || '调整钱包余额失败', 'error');
    }
  };

  if (loading && users.length === 0) {
    return <div style={{ color: 'hsl(var(--text-secondary))', padding: '20px' }}>正在载入读者账户档案库...</div>;
  }

  // --- User Detail View ---
  if (selectedUser) {
    return (
      <div className="animate-fade-in" style={{ padding: '24px' }}>
        <button onClick={() => setSelectedUser(null)} className="btn-secondary" style={{ marginBottom: '24px', display: 'inline-flex', gap: '8px' }}>
          <ArrowLeft size={16} /> 返回用户列表
        </button>

        {/* Profile Card Header */}
        <div className="glass-panel" style={{ padding: '24px', display: 'grid', gridTemplateColumns: '80px 1fr auto', gap: '20px', alignItems: 'center', marginBottom: '24px' }}>
          <div style={{
            width: '80px', height: '80px', borderRadius: '40px', backgroundColor: selectedUser.is_vip ? 'rgba(234, 179, 8, 0.15)' : 'hsl(var(--primary) / 0.1)',
            display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: '2rem', fontWeight: 600, color: selectedUser.is_vip ? '#eab308' : 'hsl(var(--primary))',
            border: selectedUser.is_vip ? '2px solid rgba(234, 179, 8, 0.4)' : 'none'
          }}>
            {selectedUser.is_vip ? '👑' : (selectedUser.nickname ? selectedUser.nickname.charAt(0).toUpperCase() : 'U')}
          </div>
          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '6px' }}>
              <h2 style={{ fontSize: '1.5rem', margin: 0 }}>{selectedUser.nickname || '匿名读者'}</h2>
              {selectedUser.is_vip ? (
                <span className="badge badge-yellow" style={{ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: '0.75rem', fontWeight: 800, padding: '3px 8px' }}>
                  <Crown size={13} /> VIP 无限畅读会员 ({selectedUser.vip_cycle === 'month' ? '月卡' : selectedUser.vip_cycle === 'week' ? '周卡' : selectedUser.vip_cycle === 'day' ? '日卡' : selectedUser.vip_cycle})
                </span>
              ) : selectedUser.vip_status === 'CANCELLED' ? (
                <span className="badge badge-secondary" style={{ fontSize: '0.75rem' }}>已取消订阅</span>
              ) : selectedUser.vip_status === 'EXPIRED' ? (
                <span className="badge badge-secondary" style={{ fontSize: '0.75rem' }}>VIP 已过期</span>
              ) : (
                <span className="badge badge-secondary" style={{ fontSize: '0.75rem' }}>普通读者</span>
              )}
            </div>
            <p style={{ color: 'hsl(var(--text-secondary))', fontSize: '0.9rem', marginBottom: '4px' }}>用户 ID: {selectedUser.id}</p>
            <p style={{ color: 'hsl(var(--text-muted))', fontSize: '0.85rem', marginBottom: '4px' }}>绑定邮箱: {selectedUser.email}</p>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '8px', marginTop: '8px' }}>
              <span className="badge badge-violet" style={{ fontSize: '0.7rem' }}>IP: {selectedUser.ip_address || '未知'}</span>
              <span className="badge badge-secondary" style={{ fontSize: '0.7rem', maxWidth: '350px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={selectedUser.device}>设备: {selectedUser.device || '未知'}</span>
              {selectedUser.utm_source && (
                <span className="badge badge-blue" style={{ fontSize: '0.7rem' }}>
                  渠道: {selectedUser.utm_source} {selectedUser.utm_campaign ? `(${selectedUser.utm_campaign})` : ''}
                </span>
              )}
              {selectedUser.is_vip && selectedUser.vip_expire_at && (
                <span className="badge badge-yellow" style={{ fontSize: '0.7rem' }}>
                  VIP 有效期至: {new Date(selectedUser.vip_expire_at).toLocaleString()}
                </span>
              )}
            </div>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
            <span className={`badge ${selectedUser.status === 1 ? 'badge-green' : 'badge-red'}`} style={{ textAlign: 'center', width: 'fit-content', alignSelf: 'flex-end' }}>
              {selectedUser.status === 1 ? '账号状态：正常' : '账号状态：已封禁'}
            </span>
            <button onClick={() => handleToggleStatus(selectedUser)} className="btn-secondary" style={{
              display: 'flex', gap: '6px', fontSize: '0.85rem', color: selectedUser.status === 1 ? '#f87171' : '#10b981',
              borderColor: selectedUser.status === 1 ? 'rgba(239,68,68,0.2)' : 'rgba(16,185,129,0.2)'
            }}>
              <Ban size={14} /> {selectedUser.status === 1 ? '封禁此账号' : '解封此账号'}
            </button>
          </div>
        </div>

        {/* Dedicated VIP Subscription & Billing Info Panel */}
        <div className="glass-panel" style={{ padding: '24px', marginBottom: '24px' }}>
          <h3 style={{ fontSize: '1.2rem', marginBottom: '16px', display: 'flex', alignItems: 'center', gap: '8px' }}>
            <Crown size={18} style={{ color: '#eab308' }} />
            VIP 会员周期订阅与续费档案
          </h3>

          {selectedUser.active_subscription ? (
            <div style={{
              background: 'linear-gradient(135deg, rgba(234, 179, 8, 0.1) 0%, rgba(202, 138, 4, 0.05) 100%)',
              border: '1px solid rgba(234, 179, 8, 0.3)',
              borderRadius: '12px',
              padding: '16px 20px',
              marginBottom: '20px',
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))',
              gap: '16px'
            }}>
              <div>
                <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))', display: 'block' }}>当前活跃订阅</span>
                <span style={{ fontSize: '1.1rem', fontWeight: 700, color: '#eab308', display: 'flex', alignItems: 'center', gap: '6px', marginTop: '2px' }}>
                  <CheckCircle2 size={16} /> VIP {selectedUser.active_subscription.cycle === 'month' ? '月度订阅' : selectedUser.active_subscription.cycle === 'week' ? '周度订阅' : '日卡订阅'}
                </span>
                <span style={{ fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginTop: '2px', display: 'block' }}>
                  ${(selectedUser.active_subscription.price_cents / 100).toFixed(2)} {selectedUser.active_subscription.currency} / {selectedUser.active_subscription.cycle}
                </span>
              </div>

              <div>
                <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))', display: 'block' }}>PayPal 订阅流水号</span>
                <span style={{ fontSize: '0.95rem', fontWeight: 600, fontFamily: 'monospace', color: 'hsl(var(--text-primary))', display: 'block', marginTop: '4px' }}>
                  {selectedUser.active_subscription.subscription_id}
                </span>
                <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>渠道: {selectedUser.active_subscription.payment_method.toUpperCase()}</span>
              </div>

              <div>
                <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))', display: 'block' }}>当前计费周期起止</span>
                <span style={{ fontSize: '0.85rem', fontWeight: 600, color: 'hsl(var(--text-primary))', display: 'block', marginTop: '4px' }}>
                  {selectedUser.active_subscription.current_period_start ? new Date(selectedUser.active_subscription.current_period_start).toLocaleDateString() : '-'}
                  {' ~ '}
                  {selectedUser.active_subscription.current_period_end ? new Date(selectedUser.active_subscription.current_period_end).toLocaleDateString() : '-'}
                </span>
                {selectedUser.active_subscription.next_billing_time && (
                  <span style={{ fontSize: '0.75rem', color: '#10b981', display: 'block', marginTop: '2px' }}>
                    下次续费扣款: {new Date(selectedUser.active_subscription.next_billing_time).toLocaleString()}
                  </span>
                )}
              </div>
            </div>
          ) : (
            <div style={{
              backgroundColor: 'hsl(var(--bg-card))',
              border: '1px solid hsl(var(--border))',
              borderRadius: '8px',
              padding: '14px 18px',
              color: 'hsl(var(--text-muted))',
              fontSize: '0.85rem',
              marginBottom: '16px',
              display: 'flex',
              alignItems: 'center',
              gap: '8px'
            }}>
              <AlertCircle size={16} />
              <span>该用户当前无生效中的 VIP 周期自动扣款订阅。</span>
            </div>
          )}

          {/* All Subscriptions History Table */}
          {selectedUser.subscriptions && selectedUser.subscriptions.length > 0 && (
            <div style={{ overflowX: 'auto', border: '1px solid hsl(var(--border))', borderRadius: '8px' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', fontSize: '0.82rem' }}>
                <thead>
                  <tr style={{ backgroundColor: 'hsl(var(--bg-card))', borderBottom: '1px solid hsl(var(--border))', color: 'hsl(var(--text-secondary))' }}>
                    <th style={{ padding: '10px 14px' }}>PayPal Subscription ID</th>
                    <th style={{ padding: '10px 14px' }}>订阅周期</th>
                    <th style={{ padding: '10px 14px' }}>单期金额</th>
                    <th style={{ padding: '10px 14px' }}>订阅状态</th>
                    <th style={{ padding: '10px 14px' }}>当前周期起止</th>
                    <th style={{ padding: '10px 14px' }}>下次续费时间</th>
                    <th style={{ padding: '10px 14px' }}>签约开通时间</th>
                  </tr>
                </thead>
                <tbody>
                  {selectedUser.subscriptions.map((sub) => (
                    <tr key={sub.id} style={{ borderBottom: '1px solid hsl(var(--border) / 0.5)' }}>
                      <td style={{ padding: '10px 14px', fontFamily: 'monospace', color: 'hsl(var(--text-primary))' }}>
                        {sub.subscription_id}
                      </td>
                      <td style={{ padding: '10px 14px', textTransform: 'capitalize' }}>
                        {sub.cycle === 'month' ? '月度订阅' : sub.cycle === 'week' ? '周度订阅' : '日度卡'}
                      </td>
                      <td style={{ padding: '10px 14px', fontWeight: 600 }}>
                        ${(sub.price_cents / 100).toFixed(2)} {sub.currency}
                      </td>
                      <td style={{ padding: '10px 14px' }}>
                        <span className={`badge ${
                          sub.status === 'ACTIVE' ? 'badge-green' : sub.status === 'CANCELLED' ? 'badge-orange' : 'badge-secondary'
                        }`} style={{ fontSize: '0.7rem' }}>
                          {sub.status === 'ACTIVE' ? '生效中 (ACTIVE)' : sub.status === 'CANCELLED' ? '已取消 (CANCELLED)' : sub.status}
                        </span>
                      </td>
                      <td style={{ padding: '10px 14px', color: 'hsl(var(--text-secondary))' }}>
                        {sub.current_period_start ? new Date(sub.current_period_start).toLocaleDateString() : '-'}
                        {' ~ '}
                        {sub.current_period_end ? new Date(sub.current_period_end).toLocaleDateString() : '-'}
                      </td>
                      <td style={{ padding: '10px 14px', color: 'hsl(var(--text-secondary))' }}>
                        {sub.next_billing_time ? new Date(sub.next_billing_time).toLocaleString() : '-'}
                      </td>
                      <td style={{ padding: '10px 14px', color: 'hsl(var(--text-muted))' }}>
                        {new Date(sub.created_at).toLocaleString()}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        {/* Sync Status / Manual Adjustment splits */}
        <div style={{ display: 'grid', gridTemplateColumns: '1.2fr 1fr', gap: '24px', alignItems: 'start' }}>

          {/* Bookshelf syncing logs */}
          <div className="glass-panel" style={{ padding: '24px' }}>
            <h3 style={{ fontSize: '1.2rem', marginBottom: '18px', display: 'flex', alignItems: 'center' }}>
              <BookOpen size={18} style={{ marginRight: '8px', color: 'hsl(var(--primary))' }} />
              同步书架与阅读进度
            </h3>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
              {(!selectedUser.bookshelf || selectedUser.bookshelf.length === 0) ? (
                <p style={{ color: 'hsl(var(--text-muted))', fontSize: '0.9rem' }}>该用户书架为空，暂无同步阅读记录。</p>
              ) : (
                paginatedBookshelf.map((item) => (
                  <div key={item.novel_id} style={{
                    display: 'flex', gap: '14px', padding: '12px',
                    backgroundColor: 'hsl(var(--bg-card))', border: '1px solid hsl(var(--border))', borderRadius: '8px'
                  }}>
                    <img src={item.cover_url} alt={item.novel_title} style={{ width: '48px', height: '64px', objectFit: 'cover', borderRadius: '4px' }} />
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <h4 style={{ fontSize: '0.95rem', marginBottom: '4px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{item.novel_title}</h4>
                      <p style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))' }}>
                        阅读进度: <span style={{ color: 'hsl(var(--text-primary))', fontWeight: 500 }}>第 {item.chapter_index} 章</span> (滑动位置: {Math.round(item.scroll_offset_percentage * 100)}%)
                      </p>
                      <p style={{ fontSize: '0.7rem', color: 'hsl(var(--text-muted))', marginTop: '4px' }}>
                        最后同步: {new Date(item.updated_at).toLocaleString()}
                      </p>
                    </div>
                    <div>
                      {item.in_shelf ? (
                        <span className="badge badge-green">书架中</span>
                      ) : (
                        <span className="badge badge-orange" title="用户已从书架移出，但仍保留阅读记录">已移出书架</span>
                      )}
                    </div>
                  </div>
                ))
              )}
            </div>

            {totalShelfPages > 1 && (
              <div style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                marginTop: '14px',
                fontSize: '0.8rem',
                color: 'hsl(var(--text-secondary))'
              }}>
                <button
                  className="btn-secondary"
                  onClick={() => setShelfPage(p => Math.max(1, p - 1))}
                  disabled={shelfPage === 1}
                  style={{ padding: '4px 8px', fontSize: '0.75rem', borderRadius: '4px' }}
                >
                  上一页
                </button>
                <span>
                  第 {shelfPage} / {totalShelfPages} 页 (共 {selectedUser.bookshelf?.length || 0} 本)
                </span>
                <button
                  className="btn-secondary"
                  onClick={() => setShelfPage(p => Math.min(totalShelfPages, p + 1))}
                  disabled={shelfPage === totalShelfPages}
                  style={{ padding: '4px 8px', fontSize: '0.75rem', borderRadius: '4px' }}
                >
                  下一页
                </button>
              </div>
            )}
          </div>

          {/* Support Manual wallet coin adjustment */}
          <div className="glass-panel" style={{ padding: '24px' }}>
            <h3 style={{ fontSize: '1.2rem', marginBottom: '18px', display: 'flex', alignItems: 'center' }}>
              <Wallet size={18} style={{ marginRight: '8px', color: 'hsl(var(--accent-pink))' }} />
              运营调币与补偿发放面板
            </h3>

            {/* Wallet balances */}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px', marginBottom: '16px' }}>
              <div style={{ padding: '16px', backgroundColor: 'hsl(var(--bg-card))', borderRadius: '8px', border: '1px solid hsl(var(--border))' }}>
                <span style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>充值本金余额 (Charged Coins)</span>
                <span style={{ fontSize: '1.6rem', fontWeight: 700 }}>
                  {selectedUser.charged_coins}
                </span>
              </div>
              <div style={{ padding: '16px', backgroundColor: 'hsl(var(--bg-card))', borderRadius: '8px', border: '1px solid hsl(var(--border))' }}>
                <span style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>赠送金币余额 (Bonus Coins)</span>
                <span style={{ fontSize: '1.6rem', fontWeight: 700 }}>
                  {selectedUser.bonus_coins}
                </span>
              </div>
            </div>

            {/* Detailed financial KPIs */}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '12px', marginBottom: '24px' }}>
              <div style={{ padding: '12px', backgroundColor: 'hsl(var(--bg-card))', borderRadius: '8px', border: '1px solid hsl(var(--border))', textAlign: 'center' }}>
                <span style={{ display: 'block', fontSize: '0.72rem', color: 'hsl(var(--text-muted))' }}>累计充值</span>
                <span style={{ fontSize: '1.15rem', fontWeight: 600, color: 'hsl(var(--primary))' }}>
                  {selectedUser.total_recharge || 0} 金币
                </span>
              </div>
              <div style={{ padding: '12px', backgroundColor: 'hsl(var(--bg-card))', borderRadius: '8px', border: '1px solid hsl(var(--border))', textAlign: 'center' }}>
                <span style={{ display: 'block', fontSize: '0.72rem', color: 'hsl(var(--text-muted))' }}>金币总余额</span>
                <span style={{ fontSize: '1.15rem', fontWeight: 600, color: 'hsl(var(--accent-pink))' }}>
                  {selectedUser.balance || 0} 金币
                </span>
              </div>
              <div style={{ padding: '12px', backgroundColor: 'hsl(var(--bg-card))', borderRadius: '8px', border: '1px solid hsl(var(--border))', textAlign: 'center' }}>
                <span style={{ display: 'block', fontSize: '0.72rem', color: 'hsl(var(--text-muted))' }}>累计消费</span>
                <span style={{ fontSize: '1.15rem', fontWeight: 600, color: 'hsl(var(--text-secondary))' }}>
                  {selectedUser.total_spent || 0} 金币
                </span>
              </div>
            </div>

            {adjustSuccess && (
              <div style={{ backgroundColor: 'rgba(16, 185, 129, 0.1)', border: '1px solid rgba(16, 185, 129, 0.25)', color: '#10b981', padding: '12px', borderRadius: '8px', fontSize: '0.85rem', marginBottom: '18px' }}>
                {adjustSuccess}
              </div>
            )}

            <form onSubmit={handleAdjustWallet}>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px', marginBottom: '16px' }}>
                <div>
                  <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>调整数量 (可为负数)</label>
                  <input
                    type="number"
                    className="input-field"
                    value={adjustAmount}
                    onChange={(e) => setAdjustAmount(Number(e.target.value))}
                    required
                  />
                  <span style={{ fontSize: '0.7rem', color: 'hsl(var(--text-muted))' }}>输入负数（如 -200）表示扣减余额。</span>
                </div>
                <div>
                  <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>调整金币类型</label>
                  <CustomSelect
                    options={[
                      { value: 'bonus', label: '赠送金币 (Bonus Coins)' },
                      { value: 'charged', label: '充值代币 (Charged Coins)' }
                    ]}
                    value={adjustType}
                    onChange={(val) => setAdjustType(val as any)}
                    width="100%"
                  />
                </div>
              </div>

              <div style={{ marginBottom: '20px' }}>
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>调整原因及备注</label>
                <input
                  type="text"
                  className="input-field"
                  placeholder="请输入发放缘由或扣减退款事由说明"
                  value={adjustReason}
                  onChange={(e) => setAdjustReason(e.target.value)}
                  required
                />
              </div>

              <button type="submit" className="btn-primary" style={{ width: '100%', display: 'flex', gap: '8px' }}>
                <DollarSign size={16} /> 确认并执行金币余额调整
              </button>
            </form>
          </div>
        </div>
      </div>
    );
  }

  // --- Users Table Listing ---
  return (
    <div className="animate-fade-in" style={{ padding: '16px 24px 24px 24px' }}>
      <div style={{ marginBottom: '16px' }}>
        <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">用户管理</h1>
      </div>

      {/* Filter and Search Bar */}
      <div style={{ display: 'flex', gap: '16px', marginBottom: '24px' }}>
        <div style={{ position: 'relative', flex: 1 }}>
          <span style={{ position: 'absolute', left: '12px', top: '12px', color: 'hsl(var(--text-muted))' }}>
            <Search size={16} />
          </span>
          <input
            type="text"
            className="input-field"
            style={{ paddingLeft: '38px' }}
            placeholder="输入用户 ID、邮箱地址或昵称进行搜索..."
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setPage(1);
            }}
          />
        </div>
      </div>

      {/* Users table */}
      <div className="glass-panel" style={{ padding: '8px', overflowX: 'auto' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left' }}>
          <thead>
            <tr style={{ borderBottom: '1px solid hsl(var(--border))', color: 'hsl(var(--text-secondary))', fontSize: '0.8rem' }}>
              <th style={{ padding: '12px 16px' }}>用户 ID</th>
              <th style={{ padding: '12px 16px' }}>账号类型</th>
              <th style={{ padding: '12px 16px' }}>读者属性 & 登录设备</th>
              <th style={{ padding: '12px 16px' }}>VIP 订阅信息</th>
              <th style={{ padding: '12px 16px' }}>推广来源/广告渠道</th>
              <th style={{ padding: '12px 16px' }}>财务指标 (累计充值/余额/消费)</th>
              <th style={{ padding: '12px 16px' }}>最近阅读小说</th>
              <th style={{ padding: '12px 16px' }}>账号状态</th>
              <th style={{ padding: '12px 16px' }}>加入时间</th>
            </tr>
          </thead>
          <tbody>
            {users.length === 0 ? (
              <tr>
                <td colSpan={9} style={{ padding: '20px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>未检索到符合条件的读者档案。</td>
              </tr>
            ) : (
              users.map((u) => (
                <tr
                  key={u.id}
                  onClick={() => handleUserClick(u.id)}
                  style={{ borderBottom: '1px solid hsl(var(--border) / 0.5)', fontSize: '0.9rem', cursor: 'pointer', transition: 'background-color 0.2s', color: 'hsl(var(--text-secondary))' }}
                  onMouseEnter={(e) => e.currentTarget.style.backgroundColor = 'hsl(var(--bg-card))'}
                  onMouseLeave={(e) => e.currentTarget.style.backgroundColor = 'transparent'}
                >
                  <td style={{ padding: '14px 16px', fontFamily: 'monospace' }}>{u.id}</td>
                  <td style={{ padding: '14px 16px', color: 'hsl(var(--text-secondary))' }}>
                    {u.email === 'Guest Account' ? (
                      <span className="badge badge-violet" style={{ fontSize: '0.7rem' }}>游客账号</span>
                    ) : (
                      u.email
                    )}
                  </td>
                  <td style={{ padding: '14px 16px' }}>
                    <div style={{}}>{u.nickname || '新读者'}</div>
                    <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))', marginTop: '2px', maxWidth: '240px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={u.device}>
                      {u.ip_address || '未知 IP'} | {u.device || '未知设备'}
                    </div>
                  </td>
                  <td style={{ padding: '14px 16px' }}>
                    {u.is_vip ? (
                      <div>
                        <span className="badge badge-yellow" style={{ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: '0.72rem', fontWeight: 700 }}>
                          <Crown size={12} /> VIP {u.vip_cycle === 'month' ? '月卡' : u.vip_cycle === 'week' ? '周卡' : u.vip_cycle === 'day' ? '日卡' : u.vip_cycle}
                        </span>
                        {u.vip_expire_at && (
                          <div style={{ fontSize: '0.72rem', color: '#eab308', marginTop: '3px' }}>
                            至 {new Date(u.vip_expire_at).toLocaleDateString()}
                          </div>
                        )}
                      </div>
                    ) : u.vip_status === 'CANCELLED' ? (
                      <div>
                        <span className="badge badge-orange" style={{ fontSize: '0.7rem' }}>已取消订阅</span>
                        {u.vip_expire_at && (
                          <div style={{ fontSize: '0.7rem', color: 'hsl(var(--text-muted))', marginTop: '2px' }}>
                            期满: {new Date(u.vip_expire_at).toLocaleDateString()}
                          </div>
                        )}
                      </div>
                    ) : u.vip_status === 'EXPIRED' ? (
                      <span className="badge badge-secondary" style={{ fontSize: '0.7rem' }}>VIP 已过期</span>
                    ) : (
                      <span style={{ color: 'hsl(var(--text-muted))', fontSize: '0.82rem' }}>普通读者</span>
                    )}
                  </td>
                  <td style={{ padding: '14px 16px' }}>
                    {u.utm_source ? (
                      <div>
                        <span className="badge badge-blue" style={{ fontSize: '0.75rem' }}>{u.utm_source}</span>
                        {u.utm_campaign && (
                          <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))', marginTop: '2px' }}>
                            Campaign: {u.utm_campaign}
                          </div>
                        )}
                      </div>
                    ) : (
                      <span style={{ color: 'hsl(var(--text-muted))', fontSize: '0.85rem' }}>自然流量</span>
                    )}
                  </td>
                  <td style={{ padding: '14px 16px' }}>
                    <div style={{ fontSize: '0.82rem' }}>充值: <span style={{ color: 'hsl(var(--primary))', fontWeight: 600 }}>{u.total_recharge || 0}</span></div>
                    <div style={{ fontSize: '0.82rem', marginTop: '2px' }}>余额: <span style={{ color: 'hsl(var(--accent-pink))', fontWeight: 600 }}>{u.balance || 0}</span></div>
                    <div style={{ fontSize: '0.82rem', marginTop: '2px' }}>消费: <span style={{ color: 'hsl(var(--text-secondary))' }}>{u.total_spent || 0}</span></div>
                  </td>
                  <td style={{ padding: '14px 16px', maxWidth: '180px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {u.recently_read_book ? (
                      <div style={{ display: 'flex', alignItems: 'center', gap: '4px', fontSize: '0.85rem' }}>
                        <BookOpen size={12} style={{ color: 'hsl(var(--primary))', flexShrink: 0 }} />
                        <span title={u.recently_read_book} style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{u.recently_read_book}</span>
                      </div>
                    ) : (
                      <span style={{ color: 'hsl(var(--text-muted))', fontSize: '0.85rem' }}>-</span>
                    )}
                  </td>
                  <td style={{ padding: '14px 16px' }}>
                    <span className={`badge ${u.status === 1 ? 'badge-green' : 'badge-red'}`} style={{ fontSize: '0.7rem' }}>
                      {u.status === 1 ? '正常' : '已封禁'}
                    </span>
                  </td>
                  <td style={{ padding: '14px 16px', color: 'hsl(var(--text-muted))', fontSize: '0.8rem' }}>
                    {new Date(u.created_at).toLocaleDateString()}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>

        {/* Pagination Footer */}
        {!loading && users.length > 0 && (
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px 16px', borderTop: '1px solid hsl(var(--border) / 0.5)' }}>
            <span style={{ fontSize: '0.8rem', color: 'hsl(var(--text-secondary))' }}>
              共 {total} 个用户，第 {page} / {Math.ceil(total / 10) || 1} 页
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

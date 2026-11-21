import { useCallback, useEffect, useRef, useState } from 'react';
import {
  AlertCircle,
  Ban,
  CheckCircle2,
  Crown,
  DollarSign,
  Wallet,
} from 'lucide-react';
import { apiRequest } from '../utils/api';
import CustomSelect from '../components/CustomSelect';
import { FALLBACK_COVER } from '../components/operations/operationsUtils';
import { CmsDialog } from '../components/ui/CmsDialog';
import { Pagination } from '../components/ui/Pagination';
import {
  type FilterChip,
  FilterChipsBar,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../components/ui/Primitives';

export interface UserSubscriptionItem {
  id: number;
  subscription_id: string;
  plan_id: string;
  status: string;
  cycle: string;
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

interface UserDetail extends User {
  charged_coins: number;
  bonus_coins: number;
  bookshelf: BookshelfItem[];
  subscriptions?: UserSubscriptionItem[];
  active_subscription?: UserSubscriptionItem;
}

function formatVipCycleShort(cycle?: string): string {
  if (cycle === 'month') return '月卡';
  if (cycle === 'week') return '周卡';
  if (cycle === 'day') return '日卡';
  return cycle || '会员';
}

export default function UsersTab() {
  const [users, setUsers] = useState<User[]>([]);
  const [total, setTotal] = useState(0);
  const [searchDraft, setSearchDraft] = useState('');
  const [queryState, setQueryState] = useState({ page: 1, pageSize: 10, search: '', nonce: 0 });
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [density, setDensity] = useState<TableDensity>('comfortable');

  const [selectedUser, setSelectedUser] = useState<UserDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailTab, setDetailTab] = useState<'profile' | 'vip_shelf' | 'adjust'>('profile');
  const [shelfPage, setShelfPage] = useState(1);
  const [shelfPageSize, setShelfPageSize] = useState(5);

  const [adjustAmount, setAdjustAmount] = useState('100');
  const [adjustType, setAdjustType] = useState<'bonus' | 'charged'>('bonus');
  const [adjustReason, setAdjustReason] = useState('每日签到异常补偿发放');
  const [adjustSuccess, setAdjustSuccess] = useState('');
  const [adjustSubmitting, setAdjustSubmitting] = useState(false);

  const fetchSeqRef = useRef(0);
  const detailSeqRef = useRef(0);

  const fetchUsers = useCallback(async () => {
    const seq = ++fetchSeqRef.current;
    setLoading(true);
    try {
      const params = new URLSearchParams({
        page: String(queryState.page),
        page_size: String(queryState.pageSize),
        search: queryState.search,
      });
      const data = await apiRequest('GET', `/users?${params.toString()}`);
      if (seq !== fetchSeqRef.current) return;
      setUsers(data.users || []);
      setTotal(data.total || 0);
      setHasLoadedOnce(true);
    } catch (e) {
      if (seq !== fetchSeqRef.current) return;
      console.error(e);
      setHasLoadedOnce(true);
    } finally {
      if (seq === fetchSeqRef.current) {
        setLoading(false);
      }
    }
  }, [queryState]);

  useEffect(() => {
    void fetchUsers();
  }, [fetchUsers]);

  const handleUserClick = async (userID: number) => {
    const seq = ++detailSeqRef.current;
    setDetailLoading(true);
    setAdjustSuccess('');
    setDetailTab('profile');
    try {
      const data = await apiRequest('GET', `/users/${userID}`);
      if (seq !== detailSeqRef.current) return;
      setSelectedUser(data);
      setShelfPage(1);
    } catch (e) {
      if (seq !== detailSeqRef.current) return;
      window.showToast?.(`加载用户详细信息失败: ${e}`, 'error');
    } finally {
      if (seq === detailSeqRef.current) {
        setDetailLoading(false);
      }
    }
  };

  const closeDetailDrawer = () => {
    detailSeqRef.current += 1;
    setDetailLoading(false);
    setSelectedUser(null);
    setAdjustSuccess('');
  };

  const handleToggleStatus = (user: UserDetail) => {
    const nextStatus = user.status === 1 ? 2 : 1;
    const confirmMsg =
      nextStatus === 2
        ? '您确定要封禁该用户账号吗？封禁后该用户将无法再次登录 H5 客户端或阅读任何小说章节！'
        : '您确定要解除该用户的封禁状态吗？';

    window.showConfirm?.(confirmMsg, async () => {
      try {
        await apiRequest('PUT', `/users/${user.id}/status`, { status: nextStatus });
        setSelectedUser((prev) => (prev && prev.id === user.id ? { ...prev, status: nextStatus } : prev));
        window.showToast?.(nextStatus === 2 ? '账号已封禁' : '账号已解封', 'success');
        void fetchUsers();
      } catch (err: unknown) {
        window.showToast?.(err instanceof Error ? err.message : '操作失败', 'error');
      }
    });
  };

  const handleAdjustWallet = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedUser || adjustSubmitting) return;
    const trimmedAmount = adjustAmount.trim();
    const parsedAmount = Number(trimmedAmount);
    if (!trimmedAmount || !Number.isInteger(parsedAmount) || parsedAmount === 0) {
      window.showToast?.('请输入非零整数的调整数量（正数增加，负数扣减）', 'error');
      return;
    }

    const targetUserId = selectedUser.id;
    const seq = detailSeqRef.current;
    setAdjustSubmitting(true);
    try {
      await apiRequest('POST', `/users/${targetUserId}/adjust-wallet`, {
        amount: parsedAmount,
        is_bonus: adjustType === 'bonus',
        reason: adjustReason,
      });

      const data = await apiRequest('GET', `/users/${targetUserId}`);
      if (seq !== detailSeqRef.current) return;
      setAdjustSuccess('钱包金币余额调整成功！已录入系统日志。');
      setAdjustReason('');
      setSelectedUser(data);
      void fetchUsers();
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '调整钱包余额失败', 'error');
    } finally {
      setAdjustSubmitting(false);
    }
  };

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setQueryState((prev) => ({
      ...prev,
      page: 1,
      search: searchDraft.trim(),
      nonce: prev.nonce + 1,
    }));
  };

  const handleReset = () => {
    setSearchDraft('');
    setQueryState((prev) => ({
      ...prev,
      page: 1,
      search: '',
      nonce: prev.nonce + 1,
    }));
  };

  const filterChips: FilterChip[] = queryState.search
    ? [
        {
          key: 'search',
          label: `搜索: ${queryState.search}`,
          onClear: handleReset,
        },
      ]
    : [];

  const startShelfIndex = (shelfPage - 1) * shelfPageSize;
  const paginatedBookshelf = (selectedUser?.bookshelf || []).slice(startShelfIndex, startShelfIndex + shelfPageSize);

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Unified Filter Strip */}
      <search className="block space-y-2">
        <form onSubmit={handleSearchSubmit} className="flex flex-wrap items-center justify-between gap-2.5">
          <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
            <SearchInput
              className="w-full sm:w-80"
              ariaLabel="搜索用户 ID、邮箱或昵称"
              placeholder="搜索用户 ID、邮箱或昵称..."
              value={searchDraft}
              onChange={setSearchDraft}
              onClear={() => {
                if (queryState.search) handleReset();
              }}
            />
            <button type="submit" className="btn-secondary h-9 px-3 text-xs font-normal whitespace-nowrap">
              查询
            </button>
            {(searchDraft || queryState.search) && (
              <button
                type="button"
                onClick={handleReset}
                className="btn-ghost h-9 text-xs text-[var(--cms-muted-fg)]"
              >
                重置
              </button>
            )}
          </div>

          <div className="flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
            <span className="tabular-nums">共 {total} 位用户</span>
            <ViewOptionsPopover density={density} onDensityChange={setDensity} />
          </div>
        </form>

        <FilterChipsBar chips={filterChips} />
      </search>

      {/* Airy Table + External Pagination */}
      <TableRegion
        ariaLabel="读者账户列表"
        footer={
          <Pagination
            page={queryState.page}
            pageSize={queryState.pageSize}
            total={total}
            disabled={loading}
            onPageChange={(nextPage) => setQueryState((prev) => ({ ...prev, page: nextPage }))}
            onPageSizeChange={(nextSize) => setQueryState((prev) => ({ ...prev, page: 1, pageSize: nextSize }))}
            itemLabel="位用户"
          />
        }
      >
        <table className="cms-table min-w-[980px]" data-density={density}>
          <thead>
            <tr>
              <th scope="col">用户</th>
              <th scope="col">读者属性与设备</th>
              <th scope="col">VIP 订阅</th>
              <th scope="col">推广渠道</th>
              <th scope="col" className="text-right">
                充 / 余 / 消 (币)
              </th>
              <th scope="col">最近阅读</th>
              <th scope="col">状态</th>
              <th scope="col">加入时间</th>
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={6} cols={9} />
            ) : users.length === 0 ? (
              <tr>
                <td colSpan={9} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  未检索到符合条件的读者档案
                </td>
              </tr>
            ) : (
              users.map((u) => {
                const isViewing = selectedUser?.id === u.id;
                return (
                  <tr
                    key={u.id}
                    className={isViewing ? 'row-selected' : undefined}
                  >
                    <td>
                      <div className="space-y-0.5">
                        <div className="text-[var(--cms-fg)] tabular-nums">#{u.id}</div>
                        <div className="text-[var(--cms-muted-fg)] truncate max-w-[180px]">
                          {u.email === 'Guest Account' ? '游客账号' : u.email}
                        </div>
                      </div>
                    </td>
                    <td>
                      <div className="text-[var(--cms-fg)]">{u.nickname || '新读者'}</div>
                      <div
                        className="text-[var(--cms-muted-fg)] mt-0.5 truncate max-w-[200px]"
                        title={`${u.ip_address || '未知 IP'} | ${u.device || '未知设备'}`}
                      >
                        {u.ip_address || '未知 IP'} · {u.device || '未知设备'}
                      </div>
                    </td>
                    <td>
                      {u.is_vip ? (
                        <div className="space-y-0.5">
                          <div className="inline-flex items-center gap-1.5 text-[var(--cms-warning)]">
                            <Crown size={12} aria-hidden="true" />
                            <span>VIP {formatVipCycleShort(u.vip_cycle)}</span>
                          </div>
                          {u.vip_expire_at && (
                            <div className="text-[var(--cms-muted-fg)] tabular-nums">
                              至 {new Date(u.vip_expire_at).toLocaleDateString()}
                            </div>
                          )}
                        </div>
                      ) : u.vip_status === 'CANCELLED' ? (
                        <div className="space-y-0.5">
                          <div className="text-[var(--cms-muted-fg)]">已取消订阅</div>
                          {u.vip_expire_at && (
                            <div className="text-[var(--cms-muted-fg)] tabular-nums">
                              期满 {new Date(u.vip_expire_at).toLocaleDateString()}
                            </div>
                          )}
                        </div>
                      ) : u.vip_status === 'EXPIRED' ? (
                        <span className="text-[var(--cms-muted-fg)]">VIP 已过期</span>
                      ) : (
                        <span className="text-[var(--cms-muted-fg)]">普通读者</span>
                      )}
                    </td>
                    <td>
                      {u.utm_source ? (
                        <div className="space-y-0.5">
                          <div className="text-[var(--cms-fg)]">{u.utm_source}</div>
                          {u.utm_campaign && (
                            <div className="text-[var(--cms-muted-fg)] truncate max-w-[140px]">
                              {u.utm_campaign}
                            </div>
                          )}
                        </div>
                      ) : (
                        <span className="text-[var(--cms-muted-fg)]">自然流量</span>
                      )}
                    </td>
                    <td className="text-right tabular-nums whitespace-nowrap">
                      <div className="text-[var(--cms-fg)]">余 {(u.balance || 0).toLocaleString()}</div>
                      <div className="text-[var(--cms-muted-fg)] mt-0.5">
                        充 {u.total_recharge || 0} · 消 {u.total_spent || 0}
                      </div>
                    </td>
                    <td className="max-w-[160px] truncate text-[var(--cms-fg)]" title={u.recently_read_book}>
                      {u.recently_read_book || <span className="text-[var(--cms-muted-fg)]">-</span>}
                    </td>
                    <td>
                      <StatusDot tone={u.status === 1 ? 'success' : 'error'} label={u.status === 1 ? '正常' : '已封禁'} />
                    </td>
                    <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                      {new Date(u.created_at).toLocaleDateString()}
                    </td>
                    <td className="text-right">
                      <button
                        type="button"
                        onClick={() => void handleUserClick(u.id)}
                        className="btn-detail"
                      >
                        详情
                      </button>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </TableRegion>

      {/* Right Slide-Over User Detail & Wallet Drawer */}
      <CmsDialog
        open={Boolean(selectedUser || detailLoading)}
        onClose={closeDetailDrawer}
        closedBy={adjustSubmitting ? 'none' : 'any'}
        closeDisabled={adjustSubmitting}
        variant="drawer"
        size="xl"
        title={selectedUser ? selectedUser.nickname || '匿名读者' : '正在加载用户档案...'}
        subtitle={
          selectedUser
            ? `UID #${selectedUser.id} · ${selectedUser.email === 'Guest Account' ? '游客账号' : selectedUser.email}`
            : undefined
        }
        bodyClassName="p-0 flex-1 overflow-hidden flex flex-col"
        headerExtra={
          selectedUser && (
            <div className="flex items-center gap-2">
              <StatusDot
                tone={selectedUser.status === 1 ? 'success' : 'error'}
                label={selectedUser.status === 1 ? '账号正常' : '已封禁'}
                className="hidden sm:inline-flex mr-1"
              />
              <button
                type="button"
                onClick={() => handleToggleStatus(selectedUser)}
                className="btn-secondary h-8 px-3 text-xs"
                style={{ color: selectedUser.status === 1 ? 'var(--cms-error)' : 'var(--cms-success)' }}
              >
                <Ban size={13} aria-hidden="true" />
                <span>{selectedUser.status === 1 ? '封禁账号' : '解封账号'}</span>
              </button>
            </div>
          )
        }
      >
        {detailLoading && !selectedUser ? (
          <div className="p-6 space-y-4">
            <div className="skeleton-bar h-20 rounded-2xl" />
            <div className="skeleton-bar h-32 rounded-2xl" />
            <div className="skeleton-bar h-48 rounded-2xl" />
          </div>
        ) : (
          selectedUser && (
            <>
              {/* Drawer Internal Sub-Tabs */}
              <div className="px-6 border-b border-[var(--cms-border)] bg-[var(--cms-card)] flex items-center justify-between gap-4 shrink-0">
                <div role="tablist" aria-label="用户档案视图切换" className="flex items-center gap-6 overflow-x-auto">
                  <button
                    type="button"
                    role="tab"
                    aria-selected={detailTab === 'profile'}
                    onClick={() => setDetailTab('profile')}
                    className={`focus-ring py-2.5 text-xs font-medium whitespace-nowrap cursor-pointer transition-colors ${
                      detailTab === 'profile'
                        ? 'text-[var(--cms-fg)]'
                        : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                    }`}
                  >
                    基础档案
                  </button>
                  <button
                    type="button"
                    role="tab"
                    aria-selected={detailTab === 'vip_shelf'}
                    onClick={() => setDetailTab('vip_shelf')}
                    className={`focus-ring py-2.5 text-xs font-medium whitespace-nowrap cursor-pointer transition-colors ${
                      detailTab === 'vip_shelf'
                        ? 'text-[var(--cms-fg)]'
                        : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                    }`}
                  >
                    VIP 订阅与书架 ({selectedUser.bookshelf?.length || 0})
                  </button>
                  <button
                    type="button"
                    role="tab"
                    aria-selected={detailTab === 'adjust'}
                    onClick={() => setDetailTab('adjust')}
                    className={`focus-ring py-2.5 text-xs font-medium whitespace-nowrap cursor-pointer transition-colors ${
                      detailTab === 'adjust'
                        ? 'text-[var(--cms-fg)]'
                        : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                    }`}
                  >
                    资产调币
                  </button>
                </div>

                <div className="hidden sm:flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
                  {selectedUser.is_vip ? (
                    <span className="inline-flex items-center gap-1 text-[var(--cms-warning)] font-medium">
                      <Crown size={12} aria-hidden="true" />
                      VIP {formatVipCycleShort(selectedUser.vip_cycle)}
                    </span>
                  ) : (
                    <span>普通读者</span>
                  )}
                  {selectedUser.utm_source && (
                    <>
                      <span>·</span>
                      <span className="text-[var(--cms-primary)]">{selectedUser.utm_source}</span>
                    </>
                  )}
                </div>
              </div>

              {detailTab === 'profile' ? (
                <div className="flex-1 overflow-y-auto p-6 space-y-5 text-xs">
                  {/* Profile Meta Grid */}
                  <div className="p-4 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] grid grid-cols-1 sm:grid-cols-2 gap-3">
                    <div>
                      <div className="text-[var(--cms-muted-fg)]">最近 IP</div>
                      <div className="font-mono-code font-medium text-[var(--cms-fg)] mt-0.5 break-all">
                        {selectedUser.ip_address || '未知'}
                      </div>
                    </div>
                    <div>
                      <div className="text-[var(--cms-muted-fg)]">推广来源</div>
                      <div className="text-[var(--cms-fg)] mt-0.5 break-words">
                        {selectedUser.utm_source
                          ? `${selectedUser.utm_source}${selectedUser.utm_campaign ? ` · ${selectedUser.utm_campaign}` : ''}`
                          : '自然流量'}
                      </div>
                    </div>
                    <div className="sm:col-span-2">
                      <div className="text-[var(--cms-muted-fg)]">终端设备</div>
                      <div className="text-[var(--cms-fg)] mt-0.5 break-all whitespace-normal leading-relaxed">
                        {selectedUser.device || '未知'}
                      </div>
                    </div>
                  </div>

                  {/* Balance Metrics */}
                  <div className="grid grid-cols-2 sm:grid-cols-5 gap-3">
                    <div className="cms-card p-3.5 space-y-1">
                      <div className="text-[var(--cms-muted-fg)]">充值本金</div>
                      <div className="text-lg font-bold tabular-nums text-[var(--cms-fg)]">
                        {selectedUser.charged_coins}
                      </div>
                    </div>
                    <div className="cms-card p-3.5 space-y-1">
                      <div className="text-[var(--cms-muted-fg)]">赠送金币</div>
                      <div className="text-lg font-bold tabular-nums text-[var(--cms-fg)]">
                        {selectedUser.bonus_coins}
                      </div>
                    </div>
                    <div className="cms-card p-3.5 space-y-1">
                      <div className="text-[var(--cms-muted-fg)]">金币总余额</div>
                      <div className="text-lg font-bold tabular-nums text-[var(--cms-primary)]">
                        {selectedUser.balance || 0}
                      </div>
                    </div>
                    <div className="cms-card p-3.5 space-y-1">
                      <div className="text-[var(--cms-muted-fg)]">累计充值</div>
                      <div className="text-lg font-bold tabular-nums text-[var(--cms-success)]">
                        {selectedUser.total_recharge || 0}
                      </div>
                    </div>
                    <div className="cms-card p-3.5 space-y-1">
                      <div className="text-[var(--cms-muted-fg)]">累计消费</div>
                      <div className="text-lg font-bold tabular-nums text-[var(--cms-muted-fg)]">
                        {selectedUser.total_spent || 0}
                      </div>
                    </div>
                  </div>
                </div>
              ) : detailTab === 'adjust' ? (
                <div className="flex-1 overflow-y-auto p-6 space-y-5 text-xs">
                  {/* Live Balance Snapshot for Reference */}
                  <div className="grid grid-cols-3 gap-3">
                    <div className="cms-card p-3.5 space-y-1">
                      <div className="text-[var(--cms-muted-fg)]">当前充值本金</div>
                      <div className="text-base font-semibold tabular-nums text-[var(--cms-fg)]">
                        {selectedUser.charged_coins}
                      </div>
                    </div>
                    <div className="cms-card p-3.5 space-y-1">
                      <div className="text-[var(--cms-muted-fg)]">当前赠送金币</div>
                      <div className="text-base font-semibold tabular-nums text-[var(--cms-fg)]">
                        {selectedUser.bonus_coins}
                      </div>
                    </div>
                    <div className="cms-card p-3.5 space-y-1">
                      <div className="text-[var(--cms-muted-fg)]">当前总余额</div>
                      <div className="text-base font-semibold tabular-nums text-[var(--cms-primary)]">
                        {selectedUser.balance || 0}
                      </div>
                    </div>
                  </div>

                  {/* Manual Wallet Adjustment */}
                  <div className="cms-card p-5 flex flex-col gap-4">
                    <h2 className="text-sm font-semibold text-[var(--cms-fg)] flex items-center gap-2">
                      <Wallet size={16} className="text-[var(--cms-primary)]" aria-hidden="true" />
                      <span>运营调币与补偿发放</span>
                    </h2>

                    {adjustSuccess && (
                      <div
                        className="p-3 rounded-xl border text-xs font-medium"
                        style={{
                          backgroundColor: 'color-mix(in srgb, var(--cms-success) 10%, transparent)',
                          borderColor: 'color-mix(in srgb, var(--cms-success) 25%, transparent)',
                          color: 'var(--cms-success)',
                        }}
                      >
                        {adjustSuccess}
                      </div>
                    )}

                    <form onSubmit={handleAdjustWallet} className="space-y-4">
                      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                        <label className="space-y-1.5">
                          <span className="block font-medium text-[var(--cms-muted-fg)]">调整数量（支持负数扣减）</span>
                          <input
                            type="number"
                            step={1}
                            className="input-field tabular-nums"
                            value={adjustAmount}
                            onChange={(e) => setAdjustAmount(e.target.value)}
                            required
                          />
                        </label>

                        <label className="space-y-1.5">
                          <span className="block font-medium text-[var(--cms-muted-fg)]">调整金币类型</span>
                          <CustomSelect
                            aria-label="调整金币类型"
                            width="100%"
                            value={adjustType}
                            onChange={(val) => setAdjustType(val as 'bonus' | 'charged')}
                            options={[
                              { value: 'bonus', label: '赠送金币 (Bonus Coins)' },
                              { value: 'charged', label: '充值代币 (Charged Coins)' },
                            ]}
                          />
                        </label>
                      </div>

                      <label className="block space-y-1.5">
                        <span className="block font-medium text-[var(--cms-muted-fg)]">调整原因及备注</span>
                        <input
                          type="text"
                          className="input-field"
                          placeholder="请输入发放缘由或扣减退款事由说明"
                          value={adjustReason}
                          onChange={(e) => setAdjustReason(e.target.value)}
                          required
                        />
                      </label>

                      <button type="submit" disabled={adjustSubmitting} className="btn-primary w-full h-9">
                        <DollarSign size={14} aria-hidden="true" />
                        <span>{adjustSubmitting ? '执行中...' : '确认并执行金币余额调整'}</span>
                      </button>
                    </form>
                  </div>
                </div>
              ) : (
                <div className="flex-1 overflow-y-auto p-6 space-y-5 text-xs">
                  {/* VIP Subscriptions */}
                  <div className="cms-card p-5 flex flex-col gap-4">
                    <h2 className="text-sm font-semibold text-[var(--cms-fg)] flex items-center gap-2">
                      <Crown size={15} className="text-[var(--cms-warning)]" aria-hidden="true" />
                      <span>VIP 周期订阅记录</span>
                    </h2>

                    {selectedUser.active_subscription ? (
                      <div className="p-4 rounded-xl border border-[var(--cms-warning)]/40 bg-[color-mix(in_srgb,var(--cms-warning)_8%,transparent)] grid grid-cols-1 sm:grid-cols-3 gap-4">
                        <div className="space-y-1">
                          <div className="text-[var(--cms-muted-fg)]">当前活跃订阅</div>
                          <div className="text-sm font-bold text-[var(--cms-warning)] flex items-center gap-1.5">
                            <CheckCircle2 size={14} aria-hidden="true" />
                            <span>
                              VIP{' '}
                              {selectedUser.active_subscription.cycle === 'month'
                                ? '月度订阅'
                                : selectedUser.active_subscription.cycle === 'week'
                                  ? '周度订阅'
                                  : '日卡订阅'}
                            </span>
                          </div>
                          <div className="text-[var(--cms-muted-fg)] tabular-nums">
                            ${(selectedUser.active_subscription.price_cents / 100).toFixed(2)}{' '}
                            {selectedUser.active_subscription.currency} / {selectedUser.active_subscription.cycle}
                          </div>
                        </div>

                        <div className="space-y-1">
                          <div className="text-[var(--cms-muted-fg)]">PayPal 订阅流水号</div>
                          <div className="font-mono-code font-semibold text-[var(--cms-fg)] break-all">
                            {selectedUser.active_subscription.subscription_id}
                          </div>
                          <div className="text-[var(--cms-muted-fg)]">
                            渠道：{selectedUser.active_subscription.payment_method.toUpperCase()}
                          </div>
                        </div>

                        <div className="space-y-1 tabular-nums">
                          <div className="text-[var(--cms-muted-fg)]">当前计费周期</div>
                          <div className="font-semibold text-[var(--cms-fg)]">
                            {selectedUser.active_subscription.current_period_start
                              ? new Date(selectedUser.active_subscription.current_period_start).toLocaleDateString()
                              : '-'}{' '}
                            ~{' '}
                            {selectedUser.active_subscription.current_period_end
                              ? new Date(selectedUser.active_subscription.current_period_end).toLocaleDateString()
                              : '-'}
                          </div>
                          {selectedUser.active_subscription.next_billing_time && (
                            <div className="text-[var(--cms-success)]">
                              下次续费：{new Date(selectedUser.active_subscription.next_billing_time).toLocaleString()}
                            </div>
                          )}
                        </div>
                      </div>
                    ) : (
                      <div className="px-4 py-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] text-[var(--cms-muted-fg)] flex items-center gap-2">
                        <AlertCircle size={14} aria-hidden="true" />
                        <span>该用户当前无生效中的 VIP 周期自动扣款订阅。</span>
                      </div>
                    )}

                    {selectedUser.subscriptions && selectedUser.subscriptions.length > 0 && (
                      <TableRegion ariaLabel="历史订阅记录">
                        <table className="cms-table" data-density="compact">
                          <thead>
                            <tr>
                              <th scope="col">Subscription ID</th>
                              <th scope="col">周期</th>
                              <th scope="col" className="text-right">
                                单期金额
                              </th>
                              <th scope="col">状态</th>
                              <th scope="col">当前周期起止</th>
                            </tr>
                          </thead>
                          <tbody>
                            {selectedUser.subscriptions.map((sub) => (
                              <tr key={sub.id}>
                                <td className="tabular-nums text-[var(--cms-fg)]">{sub.subscription_id}</td>
                                <td>
                                  {sub.cycle === 'month' ? '月度' : sub.cycle === 'week' ? '周度' : '日卡'}
                                </td>
                                <td className="text-right tabular-nums text-[var(--cms-fg)]">
                                  ${(sub.price_cents / 100).toFixed(2)} {sub.currency}
                                </td>
                                <td>
                                  <StatusDot
                                    tone={sub.status === 'ACTIVE' ? 'success' : sub.status === 'CANCELLED' ? 'warning' : 'muted'}
                                    label={
                                      sub.status === 'ACTIVE'
                                        ? '生效中'
                                        : sub.status === 'CANCELLED'
                                          ? '已取消'
                                          : sub.status
                                    }
                                  />
                                </td>
                                <td className="text-[var(--cms-muted-fg)] tabular-nums">
                                  {sub.current_period_start ? new Date(sub.current_period_start).toLocaleDateString() : '-'} ~{' '}
                                  {sub.current_period_end ? new Date(sub.current_period_end).toLocaleDateString() : '-'}
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </TableRegion>
                    )}
                  </div>

                  {/* Synchronized Bookshelf */}
                  <div className="cms-card overflow-hidden">
                    <div className="px-5 py-3.5 border-b border-[var(--cms-border)] flex items-center justify-between">
                      <h2 className="text-sm font-semibold text-[var(--cms-fg)] m-0">同步书架与阅读进度</h2>
                      <span className="text-xs text-[var(--cms-muted-fg)] tabular-nums">
                        共 {selectedUser.bookshelf?.length || 0} 本
                      </span>
                    </div>

                    {!selectedUser.bookshelf || selectedUser.bookshelf.length === 0 ? (
                      <div className="p-10 text-center text-xs text-[var(--cms-muted-fg)]">
                        该用户书架为空，暂无同步阅读记录。
                      </div>
                    ) : (
                      <div className="p-4 space-y-2">
                        {paginatedBookshelf.map((item) => (
                          <div
                            key={item.novel_id}
                            className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] flex items-center gap-3.5"
                          >
                            <img
                              src={item.cover_url || FALLBACK_COVER}
                              alt={item.novel_title}
                              onError={(e) => {
                                const img = e.currentTarget;
                                if (img.dataset.fb) return;
                                img.dataset.fb = '1';
                                img.src = FALLBACK_COVER;
                              }}
                              className="w-10 h-13 object-cover rounded-lg border border-[var(--cms-border)] shrink-0"
                            />
                            <div className="flex-1 min-w-0 space-y-0.5 text-xs">
                              <div className="text-[var(--cms-fg)] truncate">{item.novel_title}</div>
                              <div className="text-[var(--cms-muted-fg)] tabular-nums">
                                阅读进度：第 {item.chapter_index} 章（{Math.round(item.scroll_offset_percentage * 100)}%） · 同步于{' '}
                                {new Date(item.updated_at).toLocaleString()}
                              </div>
                            </div>
                            <StatusDot
                              tone={item.in_shelf ? 'success' : 'warning'}
                              label={item.in_shelf ? '书架中' : '已移出'}
                            />
                          </div>
                        ))}
                      </div>
                    )}

                    {(selectedUser.bookshelf?.length || 0) > 5 && (
                      <Pagination
                        insideCard
                        page={shelfPage}
                        pageSize={shelfPageSize}
                        pageSizeOptions={[5, 10, 20, 50]}
                        total={selectedUser.bookshelf?.length || 0}
                        onPageChange={setShelfPage}
                        onPageSizeChange={(nextSize) => {
                          setShelfPageSize(nextSize);
                          setShelfPage(1);
                        }}
                        itemLabel="本书"
                      />
                    )}
                  </div>
                </div>
              )}
            </>
          )
        )}
      </CmsDialog>
    </div>
  );
}

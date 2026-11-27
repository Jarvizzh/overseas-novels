import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { CreditCard, SlidersHorizontal } from 'lucide-react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { usePermission } from '../context/PermissionContext';
import { apiRequest } from '../utils/api';
import CustomSelect from '../components/CustomSelect';
import { ThirdPartyPaymentModal, type ThirdPartyPaymentDetail } from '../components/ThirdPartyPaymentModal';
import { Pagination } from '../components/ui/Pagination';
import { PaymentLoadError, PaymentScopeBar } from '../components/payments/PaymentWorkspace';
import { paymentError, paymentParams, paymentScope, providerLabels, environmentLabels } from '../utils/paymentWorkspace';
import {
  type ColumnOption,
  type FilterChip,
  FilterChipsBar,
  type RowActionItem,
  RowActionMenu,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ToolbarPopover,
  ViewOptionsPopover,
} from '../components/ui/Primitives';

const statusFilterOptions = [
  { value: '', label: '全部订单状态' },
  { value: 'Success', label: '交易成功 (Paid)' },
  { value: 'RefundPending', label: '退款处理中' },
  { value: 'Refunded', label: '已退款 (Refunded)' },
  { value: 'Pending', label: '待付款 (Pending)' },
  { value: 'Failed', label: '支付失败 (Failed)' },
];

const orderTypeFilterOptions = [
  { value: '', label: '全部订单类型' },
  { value: 'single', label: '金币充值' },
  { value: 'subscription', label: '订阅' },
  { value: 'whole_book', label: '整本购买' },
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
  payment_environment?: "live" | "sandbox";
  payment_provider?: string;
  payment_account_id?: string;
  status: string;
  utm_source: string;
  utm_campaign: string;
  paid_at?: string;
  order_type?: string;
  subscription_cycle?: string;
  promotion_link_id?: number | null;
  novel_id?: number | null;
  created_at: string;
}

interface OrderFilterState {
  userId: string;
  status: string;
  orderType: string;
  promotionLinkId: string;
  paidStart: string;
  paidEnd: string;
}

const EMPTY_FILTER: OrderFilterState = {
  userId: '',
  status: '',
  orderType: '',
  promotionLinkId: '',
  paidStart: '',
  paidEnd: '',
};

type OrderColKey = 'attribution' | 'created_at' | 'paid_at';

function getOrderStatusMeta(status: string): {
  label: string;
  tone: 'success' | 'warning' | 'error' | 'muted';
} {
  if (status === 'Success' || status === 'Paid') return { label: '支付成功', tone: 'success' };
  if (status === 'Refunded') return { label: '已退款', tone: 'error' };
  if (status === 'RefundPending') return { label: '退款处理中', tone: 'warning' };
  if (status === 'Pending') return { label: '待付款', tone: 'warning' };
  return { label: status, tone: 'muted' };
}

const subscriptionCycleLabels: Record<string, string> = {
  day: '日',
  week: '周',
  month: '月',
  year: '年',
};

function getOrderTypeLabel(orderType?: string, subscriptionCycle?: string): string {
  if (orderType === 'subscription' || orderType === 'vip') {
    const cycleLabel = subscriptionCycle ? subscriptionCycleLabels[subscriptionCycle] : '';
    return cycleLabel ? `订阅（${cycleLabel}）` : '订阅';
  }
  if (orderType === 'whole_book') return '整本购买';
  if (!orderType || orderType === 'single') return '金币充值';
  return orderType;
}

export default function OrdersTab() {
  const { canRead, canWrite } = usePermission();
  const canReadReconciliation = canRead('payments.reconciliation');
  const canReadRisk = canRead('payments.risk');
  const canReadDisputes = canRead('payments.disputes');
  const canWriteRefunds = canWrite('payments.refunds');
  const searchFormId = useId();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const { provider, environment, account_id } = paymentScope(params, '');
  const linkedOrder = params.get('order_id');
  const linkedUser = params.get('user_id') ?? '';
  const [orders, setOrders] = useState<Order[]>([]);
  const [total, setTotal] = useState(0);
  const [draft, setDraft] = useState<OrderFilterState>({ ...EMPTY_FILTER, userId: linkedUser });
  const [queryState, setQueryState] = useState<Omit<OrderFilterState, 'userId'> & { page: number; pageSize: number; nonce: number }>({
    page: 1,
    pageSize: 10,
    nonce: 0,
    ...EMPTY_FILTER,
  });
  const [dateError, setDateError] = useState('');
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [loadError, setLoadError] = useState('');

  const [density, setDensity] = useState<TableDensity>('comfortable');
  const [visibleCols, setVisibleCols] = useState<Record<OrderColKey, boolean>>({
    attribution: true,
    created_at: true,
    paid_at: true,
  });

  const [detailModalOpen, setDetailModalOpen] = useState(false);
  const [selectedOrderForDetail, setSelectedOrderForDetail] = useState<number | string | null>(null);
  const [paymentDetails, setPaymentDetails] = useState<ThirdPartyPaymentDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const fetchSeqRef = useRef(0);
  const detailSeqRef = useRef(0);

  useEffect(() => {
    setDraft((previous) => ({ ...previous, userId: linkedUser }));
  }, [linkedUser]);

  const clearNumber = useCallback(
    (field: 'user_id' | 'order_id') => {
      setParams((current) => {
        const next = new URLSearchParams(current);
        next.delete(field);
        return next;
      });
      setQueryState((previous) => ({ ...previous, page: 1 }));
    },
    [setParams],
  );

  const fetchOrders = useCallback(async () => {
    const seq = ++fetchSeqRef.current;
    setLoading(true);
    setLoadError('');
    try {
      const params = new URLSearchParams({
        page: String(queryState.page),
        page_size: String(queryState.pageSize),
      });
      if (linkedOrder) params.set('order_id', linkedOrder);
      if (provider) params.set('provider', provider);
      if (environment) params.set('environment', environment);
      if (account_id) params.set('account_id', account_id);
      if (queryState.status) params.set('status', queryState.status);
      if (linkedUser) params.set('user_id', linkedUser);
      if (queryState.orderType) params.set('order_type', queryState.orderType);
      if (queryState.promotionLinkId) params.set('promotion_link_id', queryState.promotionLinkId);
      if (queryState.paidStart) params.set('paid_start', `${queryState.paidStart}T00:00:00Z`);
      if (queryState.paidEnd) params.set('paid_end', `${queryState.paidEnd}T23:59:59Z`);

      const data = await apiRequest('GET', `/payments/orders?${params.toString()}`);
      if (seq !== fetchSeqRef.current) return;
      setOrders(data.orders || []);
      setTotal(data.total || 0);
      setHasLoadedOnce(true);
    } catch (e) {
      if (seq !== fetchSeqRef.current) return;
      setLoadError(paymentError(e));
      setHasLoadedOnce(true);
    } finally {
      if (seq === fetchSeqRef.current) {
        setLoading(false);
      }
    }
  }, [queryState, linkedOrder, linkedUser, provider, environment, account_id]);

  useEffect(() => {
    void fetchOrders();
  }, [fetchOrders]);

  const handleOpenPaymentDetails = async (orderId: number | string) => {
    const seq = ++detailSeqRef.current;
    setSelectedOrderForDetail(orderId);
    setDetailModalOpen(true);
    setDetailLoading(true);
    try {
      const res = await apiRequest('GET', `/payments/orders/${orderId}/payment-details`);
      if (seq !== detailSeqRef.current) return;
      setPaymentDetails(res.details || null);
    } catch (err) {
      if (seq !== detailSeqRef.current) return;
      console.error('Failed to fetch payment details:', err);
      setPaymentDetails(null);
      window.showToast?.(
        `加载三方支付明细失败：${err instanceof Error ? err.message : ''}`,
        'error',
      );
    } finally {
      if (seq === detailSeqRef.current) {
        setDetailLoading(false);
      }
    }
  };

  const commitQuery = (nextDraft: OrderFilterState) => {
    if (nextDraft.paidStart && nextDraft.paidEnd && nextDraft.paidStart > nextDraft.paidEnd) {
      setDateError('支付开始日期不能晚于结束日期');
      return false;
    }
    setDateError('');
    const userId = nextDraft.userId.trim();
    if (userId !== linkedUser) {
      const next = new URLSearchParams(params);
      if (userId) next.set('user_id', userId);
      else next.delete('user_id');
      setParams(next);
    }
    setQueryState((prev) => ({
      ...prev,
      page: 1,
      nonce: prev.nonce + 1,
      status: nextDraft.status,
      orderType: nextDraft.orderType,
      promotionLinkId: nextDraft.promotionLinkId.trim(),
      paidStart: nextDraft.paidStart,
      paidEnd: nextDraft.paidEnd,
    }));
    return true;
  };

  const resetFilters = () => {
    setDateError('');
    setDraft(EMPTY_FILTER);
    if (linkedUser || linkedOrder) setParams(paymentParams({ provider, environment, account_id }));
    setQueryState((prev) => ({
      ...prev,
      page: 1,
      nonce: prev.nonce + 1,
      ...EMPTY_FILTER,
    }));
  };

  const advFilterCount =
    (queryState.promotionLinkId ? 1 : 0) + (queryState.paidStart || queryState.paidEnd ? 1 : 0);

  const hasAnyFilter = Boolean(
    draft.userId ||
      draft.status ||
      draft.orderType ||
      draft.promotionLinkId ||
      draft.paidStart ||
      draft.paidEnd ||
      linkedUser ||
      linkedOrder ||
      queryState.status ||
      queryState.orderType ||
      queryState.promotionLinkId ||
      queryState.paidStart ||
      queryState.paidEnd,
  );

  const filterChips = useMemo<FilterChip[]>(() => {
    const chips: FilterChip[] = [];
    if (linkedOrder) {
      chips.push({ key: 'orderId', label: `订单 #${linkedOrder}`, onClear: () => clearNumber('order_id') });
    }
    if (linkedUser) {
      chips.push({
        key: 'userId',
        label: `UID ${linkedUser}`,
        onClear: () => clearNumber('user_id'),
      });
    }
    if (queryState.status) {
      const label = statusFilterOptions.find((o) => o.value === queryState.status)?.label || queryState.status;
      chips.push({
        key: 'status',
        label: `状态: ${label}`,
        onClear: () => {
          setDraft((prev) => ({ ...prev, status: '' }));
          setQueryState((prev) => ({ ...prev, page: 1, status: '' }));
        },
      });
    }
    if (queryState.orderType) {
      const label = orderTypeFilterOptions.find((o) => o.value === queryState.orderType)?.label || queryState.orderType;
      chips.push({
        key: 'orderType',
        label: `类型: ${label}`,
        onClear: () => {
          setDraft((prev) => ({ ...prev, orderType: '' }));
          setQueryState((prev) => ({ ...prev, page: 1, orderType: '' }));
        },
      });
    }
    if (queryState.promotionLinkId) {
      chips.push({
        key: 'promo',
        label: `推广链接 #${queryState.promotionLinkId}`,
        onClear: () => {
          setDraft((prev) => ({ ...prev, promotionLinkId: '' }));
          setQueryState((prev) => ({ ...prev, page: 1, promotionLinkId: '' }));
        },
      });
    }
    if (queryState.paidStart || queryState.paidEnd) {
      chips.push({
        key: 'paidDate',
        label: `支付时间: ${queryState.paidStart || '不限'} ~ ${queryState.paidEnd || '至今'}`,
        onClear: () => {
          setDateError('');
          setDraft((prev) => ({ ...prev, paidStart: '', paidEnd: '' }));
          setQueryState((prev) => ({ ...prev, page: 1, paidStart: '', paidEnd: '' }));
        },
      });
    }
    return chips;
  }, [linkedOrder, linkedUser, clearNumber, queryState]);

  const columnOptions: ColumnOption[] = [
    { key: 'attribution', label: '推广 / 书籍归因', visible: visibleCols.attribution },
    { key: 'paid_at', label: '支付时间', visible: visibleCols.paid_at },
    { key: 'created_at', label: '创建时间', visible: visibleCols.created_at },
  ];

  const colSpan =
    6 + (visibleCols.attribution ? 1 : 0) + (visibleCols.paid_at ? 1 : 0) + (visibleCols.created_at ? 1 : 0);

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Unified Borderless Filter Strip */}
      <PaymentScopeBar scope={{ provider, environment, account_id }} allEnvironments onChange={(scope) => {
        setQueryState((previous) => ({ ...previous, page: 1 }));
        setParams(paymentParams({ ...scope, order_id: linkedOrder, user_id: linkedUser }));
      }} />
      <search className="block space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2.5">
          <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
            <form
              id={searchFormId}
              onSubmit={(e) => {
                e.preventDefault();
                commitQuery(draft);
              }}
              className="w-full sm:w-56"
            >
              <SearchInput
                className="w-full"
                ariaLabel="精确用户 ID"
                placeholder="精确用户 ID..."
                value={draft.userId}
                onChange={(val) => setDraft((prev) => ({ ...prev, userId: val }))}
                onClear={() => {
                  const next = { ...draft, userId: '' };
                  setDraft(next);
                  if (linkedUser) clearNumber('user_id');
                }}
              />
            </form>

            <CustomSelect
              aria-label="筛选订单状态"
              options={statusFilterOptions}
              value={queryState.status}
              onChange={(val) => {
                setDraft((prev) => ({ ...prev, status: val }));
                setQueryState((prev) => ({ ...prev, page: 1, status: val }));
              }}
              width="148px"
            />

            <CustomSelect
              aria-label="筛选订单类型"
              options={orderTypeFilterOptions}
              value={queryState.orderType}
              onChange={(val) => {
                setDraft((prev) => ({ ...prev, orderType: val }));
                setQueryState((prev) => ({ ...prev, page: 1, orderType: val }));
              }}
              width="140px"
            />

            <ToolbarPopover
              align="start"
              label="更多筛选"
              icon={<SlidersHorizontal size={13} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />}
              badgeCount={advFilterCount}
              panelClassName="w-80 p-4"
              onOpen={() => {
                setDateError('');
                setDraft((prev) => ({
                  ...prev,
                  promotionLinkId: queryState.promotionLinkId,
                  paidStart: queryState.paidStart,
                  paidEnd: queryState.paidEnd,
                }));
              }}
            >
              {({ close }) => (
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    if (commitQuery(draft)) close();
                  }}
                  className="space-y-3.5"
                >
                  <div className="font-semibold text-[var(--cms-fg)]">归因与支付日期筛选</div>
                  <label className="block space-y-1">
                    <span className="block text-[var(--cms-muted-fg)]">推广链接 ID</span>
                    <input
                      type="text"
                      className="input-field tabular-nums"
                      placeholder="输入推广链接 ID..."
                      value={draft.promotionLinkId}
                      onChange={(e) => setDraft((prev) => ({ ...prev, promotionLinkId: e.target.value }))}
                    />
                  </label>

                  <div className="grid grid-cols-2 gap-2.5">
                    <label className="block space-y-1">
                      <span className="block text-[var(--cms-muted-fg)]">支付起始</span>
                      <input
                        type="date"
                        aria-label="支付起始日期"
                        className="input-field"
                        value={draft.paidStart}
                        onChange={(e) => {
                          setDateError('');
                          setDraft((prev) => ({ ...prev, paidStart: e.target.value }));
                        }}
                      />
                    </label>
                    <label className="block space-y-1">
                      <span className="block text-[var(--cms-muted-fg)]">支付结束</span>
                      <input
                        type="date"
                        aria-label="支付结束日期"
                        className="input-field"
                        value={draft.paidEnd}
                        onChange={(e) => {
                          setDateError('');
                          setDraft((prev) => ({ ...prev, paidEnd: e.target.value }));
                        }}
                      />
                    </label>
                  </div>

                  {dateError && (
                    <p role="status" className="text-xs text-[var(--cms-error)] m-0">
                      {dateError}
                    </p>
                  )}

                  <div className="pt-2 border-t border-[var(--cms-border)] flex items-center justify-end gap-2">
                    <button
                      type="button"
                      onClick={() => {
                        const cleared = { ...draft, promotionLinkId: '', paidStart: '', paidEnd: '' };
                        setDateError('');
                        setDraft(cleared);
                        commitQuery(cleared);
                        close();
                      }}
                      className="btn-ghost text-[var(--cms-muted-fg)]"
                    >
                      清空
                    </button>
                    <button type="submit" className="btn-primary h-8 px-3 text-xs">
                      应用筛选
                    </button>
                  </div>
                </form>
              )}
            </ToolbarPopover>

            <button
              type="submit"
              form={searchFormId}
              className="btn-secondary h-9 px-3 text-xs font-normal whitespace-nowrap"
            >
              查询
            </button>

            {hasAnyFilter && (
              <button type="button" onClick={resetFilters} className="btn-ghost h-9 text-xs text-[var(--cms-muted-fg)]">
                重置
              </button>
            )}
          </div>

          <div className="flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
            <span className="tabular-nums">共 {total} 笔</span>
            <ViewOptionsPopover
              density={density}
              onDensityChange={setDensity}
              columns={columnOptions}
              onToggleColumn={(k) =>
                setVisibleCols((prev) => ({ ...prev, [k as OrderColKey]: !prev[k as OrderColKey] }))
              }
            />
          </div>
        </div>

        <FilterChipsBar chips={filterChips} onResetAll={resetFilters} />
      </search>

      {/* Airy Orders Table + External Pagination */}
      {linkedOrder && <Link to={`/payments/orders?${paymentParams({ provider, environment, account_id })}`} className="btn-secondary">清除关联订单筛选 #{linkedOrder}</Link>}
      {loadError ? <PaymentLoadError message={loadError} onRetry={() => void fetchOrders()} /> : <TableRegion
        ariaLabel="充值交易订单列表"
        footer={
          <Pagination
            page={queryState.page}
            pageSize={queryState.pageSize}
            total={total}
            disabled={loading}
            onPageChange={(nextPage) => setQueryState((prev) => ({ ...prev, page: nextPage }))}
            onPageSizeChange={(nextSize) => setQueryState((prev) => ({ ...prev, page: 1, pageSize: nextSize }))}
            itemLabel="笔订单"
          />
        }
      >
        <table className="cms-table min-w-[920px]" data-density={density}>
          <thead>
            <tr>
              <th scope="col">订单 / 用户</th>
              <th scope="col">支付凭证</th>
              <th scope="col">类型</th>
              <th scope="col" className="text-right">
                发放金币
              </th>
              <th scope="col" className="text-right">
                实付金额
              </th>
              {visibleCols.attribution && <th scope="col">推广 / 书籍归因</th>}
              {visibleCols.paid_at && <th scope="col">支付时间</th>}
              {visibleCols.created_at && <th scope="col">创建时间</th>}
              <th scope="col">状态</th>
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={6} cols={colSpan} />
            ) : orders.length === 0 ? (
              <tr>
                <td colSpan={colSpan} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  <p role="status" className="text-[11px] text-[var(--cms-muted-fg)]">暂无充值交易订单记录</p>
                </td>
              </tr>
            ) : (
              orders.map((o) => {
                const statusMeta = getOrderStatusMeta(o.status);
                const providerLabel = providerLabels[o.payment_provider || ''] || '渠道待核对';
                const credentialText = `${providerLabel} · ${o.external_ref_id || '凭证待确认'}`;
                const hasMorePermissions =
                  canReadReconciliation || canReadRisk || canReadDisputes || canWriteRefunds;
                const moreActionItems: RowActionItem[] = hasMorePermissions
                  ? o.payment_environment
                    ? ([
                        ['reconciliation', '流水', canReadReconciliation],
                        ['risk', '风控', canReadRisk],
                        ['disputes', '争议', canReadDisputes],
                        ['refunds', '退款', canWriteRefunds],
                      ] as const)
                        .filter(([, , allowed]) => allowed)
                        .map(([view, label]) => ({
                          label,
                          onClick: () =>
                            navigate(
                              `/payments/${view}?${paymentParams({
                                order_id: String(o.id),
                                environment: o.payment_environment,
                                provider: providerLabels[o.payment_provider ?? '']
                                  ? o.payment_provider
                                  : undefined,
                                account_id: o.payment_account_id,
                              })}`,
                            ),
                        }))
                    : [{ label: '渠道环境待核对', onClick: () => {}, disabled: true }]
                  : [];

                return (
                  <tr
                    key={o.id}
                    className={selectedOrderForDetail === o.id && detailModalOpen ? 'row-selected' : undefined}
                  >
                    <td className="tabular-nums">
                      <div className="text-[var(--cms-fg)]" title={String(o.id)}>
                        #{o.id}
                      </div>
                      <div className="text-[var(--cms-muted-fg)] mt-0.5">UID {o.user_id}</div>
                    </td>
                    <td>
                      <button
                        type="button"
                        onClick={() => void handleOpenPaymentDetails(o.id)}
                        className="focus-ring inline-flex items-center gap-1.5 max-w-[260px] text-[var(--cms-fg)] hover:underline cursor-pointer tabular-nums"
                        title={
                          o.external_ref_id
                            ? `${credentialText}（点击查看第三方支付凭据）`
                            : '点击查看第三方支付凭据'
                        }
                      >
                        <CreditCard size={13} className="text-[var(--cms-muted-fg)] shrink-0" aria-hidden="true" />
                        <span className="truncate min-w-0">{credentialText}</span>
                      </button>
                      <div className="mt-0.5 text-[var(--cms-muted-fg)]">
                        {environmentLabels[o.payment_environment || ''] || '环境待核对'}
                        {o.payment_account_id && ` · 商户 #${o.payment_account_id}`}
                      </div>
                    </td>
                    <td className="text-[var(--cms-muted-fg)] whitespace-nowrap">
                      {getOrderTypeLabel(o.order_type, o.subscription_cycle)}
                    </td>
                    <td className="text-right tabular-nums text-[var(--cms-fg)] whitespace-nowrap">
                      {o.coins}
                      {o.bonus_coins_credited > 0 && (
                        <span className="text-[var(--cms-muted-fg)]"> +{o.bonus_coins_credited}</span>
                      )}
                    </td>
                    <td className="text-right tabular-nums text-[var(--cms-fg)] whitespace-nowrap">
                      ${(o.amount_cents / 100).toFixed(2)}
                    </td>
                    {visibleCols.attribution && (
                      <td className="text-[var(--cms-muted-fg)] tabular-nums">
                        {o.promotion_link_id || o.novel_id ? (
                          <div>
                            {o.promotion_link_id && <span>推广 #{o.promotion_link_id}</span>}
                            {o.promotion_link_id && o.novel_id && <span className="mx-1">·</span>}
                            {o.novel_id && <span>书籍 #{o.novel_id}</span>}
                          </div>
                        ) : (
                          '-'
                        )}
                      </td>
                    )}
                    {visibleCols.paid_at && (
                      <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                        {o.paid_at ? new Date(o.paid_at).toLocaleString('zh-CN', { hour12: false }) : '未支付'}
                      </td>
                    )}
                    {visibleCols.created_at && (
                      <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                        {new Date(o.created_at).toLocaleString('zh-CN', { hour12: false })}
                      </td>
                    )}
                    <td>
                      <StatusDot tone={statusMeta.tone} label={statusMeta.label} />
                    </td>
                    <td className="text-right whitespace-nowrap">
                      <div className="inline-flex items-center justify-end gap-0.5 whitespace-nowrap">
                        <button type="button" onClick={() => void handleOpenPaymentDetails(o.id)} className="btn-detail">
                          详情
                        </button>
                        <RowActionMenu ariaLabel={`订单 #${o.id} 更多操作`} items={moreActionItems} />
                      </div>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </TableRegion>}

      <ThirdPartyPaymentModal
        isOpen={detailModalOpen}
        onClose={() => {
          detailSeqRef.current += 1;
          setDetailLoading(false);
          setDetailModalOpen(false);
          setPaymentDetails(null);
        }}
        orderId={selectedOrderForDetail}
        loading={detailLoading}
        details={paymentDetails}
        onRefresh={() => {
          if (selectedOrderForDetail) {
            void handleOpenPaymentDetails(selectedOrderForDetail);
          }
        }}
      />
    </div>
  );
}

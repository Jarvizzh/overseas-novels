import { StripeMarketReview } from '../components/StripeMarketReview';
import { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { PaymentChannelsPanel } from './PaymentChannelsPanel';
import { CmsDialog } from '../components/ui/CmsDialog';
import { SearchInput, SkeletonRows, StatusDot, TableRegion } from '../components/ui/Primitives';
import { Pagination } from '../components/ui/Pagination';
import CustomSelect from '../components/CustomSelect';
import { PaymentLoadError, PaymentScopeBar } from '../components/payments/PaymentWorkspace';
import {
  PAYMENT_PAGE_SIZE,
  environmentLabels,
  paymentParams,
  paymentScope,
  paymentScopeLabel,
  paymentTime,
  paymentMoney,
  usePaymentQuery,
} from '../utils/paymentWorkspace';

type SummaryOrder = {
  id: number;
  user_id: string;
  order_type: string;
  status: string;
  refund_status?: string;
  novel_id?: number;
  coins: number;
  bonus_coins_credited: number;
};

export function SupportPayments() {
  const [params, setParams] = useSearchParams();
  const orderID = params.get('order_id') ?? '';
  const userID = params.get('user_id') ?? '';
  const queryField = userID && !orderID ? 'user_id' : 'order_id';
  const queryValue = orderID || userID;
  const [field, setField] = useState(queryField);
  const [value, setValue] = useState(queryValue);
  const [revision, setRevision] = useState(0);
  const page = Math.max(1, Number(params.get('page')) || 1);
  const list = usePaymentQuery<{ orders: SummaryOrder[]; total: number }>(
    queryValue
      ? `/payments/orders?${paymentParams({ ...paymentScope(params, ''), order_id: orderID, user_id: userID, page: String(page), page_size: String(PAYMENT_PAGE_SIZE) })}`
      : '',
    revision,
  );
  const loading = Boolean(queryValue) && !list;

  useEffect(() => {
    setField(queryField);
    setValue(queryValue);
  }, [queryField, queryValue]);

  return (
    <div className="space-y-4">
      <search>
        <form
          aria-label="查询客服订单"
          className="flex flex-wrap items-center gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            const next = new URLSearchParams(params);
            next.delete('order_id');
            next.delete('user_id');
            next.delete('page');
            next.set(field, value.trim());
            if (next.toString() === params.toString()) setRevision((current) => current + 1);
            else setParams(next);
          }}
        >
          <CustomSelect
            aria-label="订单查询方式"
            width="142px"
            value={field}
            options={[
              { value: 'order_id', label: '订单号' },
              { value: 'user_id', label: '用户 ID' },
            ]}
            onChange={(next) => {
              setField(next === 'user_id' ? 'user_id' : 'order_id');
              setValue('');
            }}
          />
          <input
            aria-label={field === 'order_id' ? '订单号' : '用户 ID'}
            placeholder={field === 'order_id' ? '输入订单号' : '输入用户 ID'}
            className="input-field w-full sm:w-56"
            inputMode="numeric"
            pattern="0*[1-9][0-9]*"
            required
            value={value}
            onChange={(event) => setValue(event.target.value)}
          />
          <button type="submit" className="btn-secondary">
            查询
          </button>
        </form>
      </search>
      {list?.error ? (
        <PaymentLoadError message={list.error} onRetry={() => setRevision((current) => current + 1)} />
      ) : (
        <TableRegion
          ariaLabel="客服订单列表"
          footer={
            list?.data && (
              <Pagination
                page={page}
                pageSize={PAYMENT_PAGE_SIZE}
                total={list.data.total}
                onPageChange={(nextPage) => {
                  const next = new URLSearchParams(params);
                  next.set('page', String(nextPage));
                  setParams(next);
                }}
              />
            )
          }
        >
          <table className="cms-table" aria-busy={loading}>
            <thead>
              <tr>
                <th scope="col">订单</th>
                <th scope="col">用户</th>
                <th scope="col">状态</th>
                <th scope="col">退款进度</th>
                <th scope="col">权益</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <SkeletonRows rows={3} cols={5} />
              ) : list?.data?.orders.length ? (
                list.data.orders.map((order) => (
                  <tr key={order.id}>
                    <td>#{order.id}</td>
                    <td>{order.user_id}</td>
                    <td>{order.status}</td>
                    <td>{order.refund_status || '—'}</td>
                    <td>
                      {order.order_type === 'whole_book'
                        ? `整本阅读 #${order.novel_id}`
                        : ['vip', 'subscription'].includes(order.order_type)
                          ? 'VIP'
                          : `${order.coins + order.bonus_coins_credited} 金币`}
                    </td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={5} className="text-center">
                    {queryValue ? (
                      <p role="status" className="text-[11px] text-[var(--cms-muted-fg)]">
                        未查询到订单。
                      </p>
                    ) : (
                      <Link className="btn-detail" to="/users">
                        从用户档案查看订单
                      </Link>
                    )}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </TableRegion>
      )}
    </div>
  );
}

type Subscription = {
  id: string;
  user_id: string;
  provider: string;
  status: string;
  cycle: string;
  paid_until: string;
  environment: string;
  account_id?: string;
};

type StripePeriod = {
  invoice_ref: string;
  order_id: string;
  service_start?: string;
  service_end?: string;
  billed_minor: number;
  billing_reason: string;
  review_reason?: string;
  admitted: boolean;
  allocation_count: number;
  status: string;
  sales_mode: string;
  environment: 'live' | 'sandbox' | 'unknown';
  country?: string;
  observed_tax_liability_account?: string;
  observed_customer_tax_status?: string;
  provider_status?: string;
  revises_invoice_ref?: string;
  revision_verified?: boolean;
};

const subscriptionStates = {
  ACTIVE: { label: '有效', tone: 'success' },
  PENDING: { label: '待确认', tone: 'warning' },
  CANCEL_PENDING: { label: '取消处理中', tone: 'warning' },
  CANCELLED: { label: '已取消', tone: 'muted' },
  CANCELED: { label: '已取消', tone: 'muted' },
  EXPIRED: { label: '已到期', tone: 'muted' },
  SUSPENDED: { label: '已暂停', tone: 'warning' },
  APPROVAL_PENDING: { label: '待确认', tone: 'warning' },
} as const;

export function PaymentSubscriptions({ support, finance }: { support: boolean; finance: boolean }) {
  const [params, setParams] = useSearchParams();
  const userID = params.get('user_id') ?? '';
  const [user, setUser] = useState(userID);
  useEffect(() => setUser(userID), [userID]);
  const [selected, setSelected] = useState<Subscription>();
  const [inputError, setInputError] = useState('');
  const [revision, setRevision] = useState(0);
  const [periodRevision, setPeriodRevision] = useState(0);
  const scope = paymentScope(params, '');
  const status = params.get('status') ?? '';
  const page = Math.max(1, Number(params.get('page')) || 1);
  const pageSize = Number(params.get('page_size')) || PAYMENT_PAGE_SIZE;
  const shouldLoad = !support || Boolean(userID);
  const list = usePaymentQuery<{ subscriptions: Subscription[]; total: number }>(
    shouldLoad
      ? `/payments/subscriptions?${paymentParams({ ...scope, user_id: userID, status, page: String(page), page_size: String(pageSize) })}`
      : '',
    revision,
  );
  const periodQuery = usePaymentQuery<{ periods: StripePeriod[] }>(
    selected ? `/payments/subscriptions/${encodeURIComponent(selected.id)}/${selected.provider}-periods` : '',
    periodRevision,
  );
  const periods = periodQuery?.data?.periods;
  const periodError = periodQuery?.error;

  function update(fields: Record<string, string>, reset = true) {
    const next = new URLSearchParams(params);
    if (reset) next.delete('page');
    for (const [key, value] of Object.entries(fields)) {
      if (value) next.set(key, value);
      else next.delete(key);
    }
    setParams(next);
  }

  return (
    <div className="space-y-4">
      {!support && <PaymentScopeBar allEnvironments scope={scope} onChange={(value) => update(value)} />}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <form
          className="flex flex-wrap items-center gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            const value = user.trim();
            if ((support || value) && (!/^\d+$/.test(value) || /^0+$/.test(value))) {
              setInputError('请输入有效的用户 ID。');
              return;
            }
            update({ user_id: value });
          }}
        >
          <SearchInput
            value={user}
            placeholder="输入用户 ID"
            onChange={(value) => {
              setUser(value);
              setInputError('');
            }}
          />
          <button type="submit" className="btn-secondary">
            查询
          </button>
          {userID && (
            <button
              type="button"
              className="btn-ghost"
              onClick={() => {
                setUser('');
                setInputError('');
                update({ user_id: '' });
              }}
            >
              清除用户筛选
            </button>
          )}
        </form>
        {!support && (
          <CustomSelect
            aria-label="订阅状态"
            value={status}
            options={[
              { value: '', label: '全部订阅状态' },
              ...Object.entries(subscriptionStates).map(([value, entry]) => ({ value, label: entry.label })),
            ]}
            onChange={(value) => update({ status: value })}
          />
        )}
      </div>
      {inputError && (
        <p role="alert" className="text-sm text-[var(--cms-error)]">
          {inputError}
        </p>
      )}
      {list?.error ? (
        <PaymentLoadError message={list.error} onRetry={() => setRevision((value) => value + 1)} />
      ) : (
        <TableRegion
          ariaLabel="订阅与账期"
          footer={
            list?.data && (
              <Pagination
                page={page}
                pageSize={pageSize}
                total={list.data.total}
                onPageChange={(value) => update({ page: String(value) }, false)}
                onPageSizeChange={(value) => update({ page_size: String(value) })}
              />
            )
          }
        >
          <table className="cms-table" aria-busy={shouldLoad && !list}>
            <thead>
              <tr>
                <th scope="col">订阅编号</th>
                <th scope="col">用户</th>
                <th scope="col">渠道 / 环境</th>
                <th scope="col">协议状态</th>
                <th scope="col">周期</th>
                <th scope="col">已付至 UTC</th>
                {finance && <th scope="col">账期明细</th>}
              </tr>
            </thead>
            <tbody>
              {shouldLoad && !list ? (
                <SkeletonRows rows={3} cols={finance ? 7 : 6} />
              ) : list?.data?.subscriptions.length ? (
                list.data.subscriptions.map((item) => {
                  const channelScope = {
                    provider: item.provider,
                    environment: item.environment === 'production' ? 'live' : item.environment,
                    account_id: item.account_id || '',
                  };
                  const state = subscriptionStates[item.status as keyof typeof subscriptionStates];
                  return (
                    <tr key={item.id}>
                      <td>#{item.id}</td>
                      <td>{item.user_id}</td>
                      <td>
                        {item.account_id
                          ? paymentScopeLabel(channelScope)
                          : [
                              item.provider === 'paypal'
                                ? 'PayPal'
                                : item.provider === 'stripe'
                                  ? 'Stripe'
                                  : item.provider,
                              environmentLabels[channelScope.environment] ?? '环境待核对',
                            ]
                              .filter(Boolean)
                              .join(' · ')}
                      </td>
                      <td>
                        <StatusDot tone={state?.tone ?? 'muted'} label={state?.label ?? item.status} />
                      </td>
                      <td>{item.cycle || '—'}</td>
                      <td>{paymentTime(item.paid_until)}</td>
                      {finance && (
                        <td>
                          {['stripe', 'paypal'].includes(item.provider) && (
                            <button type="button" className="btn-detail" onClick={() => setSelected(item)}>
                              账期 #{item.id}
                            </button>
                          )}
                        </td>
                      )}
                    </tr>
                  );
                })
              ) : (
                <tr>
                  <td colSpan={finance ? 7 : 6} className="text-center">
                    {shouldLoad ? (
                      <p role="status" className="text-[11px] text-[var(--cms-muted-fg)]">
                        当前范围没有匹配的订阅。
                      </p>
                    ) : (
                      <Link className="btn-detail" to="/users">
                        从用户档案查看订阅
                      </Link>
                    )}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </TableRegion>
      )}
      <CmsDialog
        open={Boolean(selected)}
        onClose={() => setSelected(undefined)}
        variant="drawer"
        size="lg"
        title={selected?.provider === 'paypal' ? 'PayPal VIP 账期' : 'Stripe VIP 账期'}
        subtitle={selected ? `订阅 #${selected.id}` : undefined}
      >
        {periodError && (
          <PaymentLoadError message={periodError} onRetry={() => setPeriodRevision((value) => value + 1)} />
        )}
        {!periodQuery && (
          <p role="status" className="text-sm">
            正在查询账期…
          </p>
        )}
        {periods?.length === 0 && (
          <p role="status" className="text-sm">
            暂无已记录的账期。
          </p>
        )}
        <ul className="space-y-3 text-sm">
          {periods?.map((period) => (
            <li key={period.invoice_ref + '/' + period.order_id} className="cms-card space-y-1 p-3">
              <div className="flex flex-wrap items-center gap-2">
                {selected?.provider === 'stripe' && period.environment !== 'unknown' ? (
                  <a
                    className="btn-detail"
                    href={`https://dashboard.stripe.com/${period.environment === 'sandbox' ? 'test/' : ''}invoices/${encodeURIComponent(period.invoice_ref)}`}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {period.invoice_ref}
                  </a>
                ) : (
                  <span>{period.invoice_ref || '原收款凭证待确认'}</span>
                )}
                <Link
                  className="btn-detail"
                  to={`/payments/orders?${paymentParams({ order_id: period.order_id, provider: selected?.provider, environment: period.environment === 'unknown' ? '' : period.environment })}`}
                >
                  订单 #{period.order_id}
                </Link>
                {period.environment !== 'unknown' ? (
                  <>
                    <Link
                      className="btn-detail"
                      to={`/payments/refunds?${paymentParams({ order_id: period.order_id, provider: selected?.provider, environment: period.environment })}`}
                    >
                      退款
                    </Link>
                    <Link
                      className="btn-detail"
                      to={`/payments/disputes?${paymentParams({ order_id: period.order_id, provider: selected?.provider, environment: period.environment })}`}
                    >
                      争议
                    </Link>
                  </>
                ) : (
                  <span>渠道环境待核对</span>
                )}
              </div>
              <p>
                {paymentMoney(period.billed_minor)} ·{' '}
                {period.sales_mode === 'merchant_of_record'
                  ? 'Managed Payments'
                  : period.sales_mode === 'direct'
                    ? '直收'
                    : '销售模式待核对'}{' '}
                · {period.allocation_count} 笔收款分配
              </p>
              {period.service_start && period.service_end && (
                <p>
                  {new Date(period.service_start).toLocaleString()} – {new Date(period.service_end).toLocaleString()}
                </p>
              )}
              <p>
                {period.admitted ? '账期已确认' : '账期未确认'} · {period.status}
              </p>
              {period.provider_status && <p>渠道账单状态 {period.provider_status}</p>}
              {period.revises_invoice_ref && (
                <p>
                  原账单 {period.revises_invoice_ref} · {period.revision_verified ? '关联已核实' : '关联待核实'}
                </p>
              )}
              {period.country && <p>账单地区 {period.country}</p>}
              {period.observed_tax_liability_account && <p>税务责任账户 {period.observed_tax_liability_account}</p>}
              {period.observed_customer_tax_status && <p>客户税务状态 {period.observed_customer_tax_status}</p>}
              {period.review_reason && <p>{period.review_reason}</p>}
              {selected?.provider === 'stripe' && period.review_reason && (
                <StripeMarketReview key={period.order_id} orderID={period.order_id} />
              )}
            </li>
          ))}
        </ul>
      </CmsDialog>
    </div>
  );
}

export function PaymentChannels() {
  return <PaymentChannelsPanel />;
}

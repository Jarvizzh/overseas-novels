import { StripeMarketReview } from '../components/StripeMarketReview';
import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { apiRequest } from '../utils/api';
import { StripeOfferSettings } from './StripeOfferSettings';
import { CmsDialog } from '../components/ui/CmsDialog';
import { SkeletonRows, StatusDot, TableRegion } from '../components/ui/Primitives';

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

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : '加载失败，请重试。';
}

export function SupportPayments() {
  const [scope, setScope] = useState('order_id');
  const [value, setValue] = useState('');
  const [orders, setOrders] = useState<SummaryOrder[] | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">交易与订单</h1>
      <form className="flex flex-wrap items-end gap-2" onSubmit={async (event) => {
        event.preventDefault();
        setLoading(true);
        setError('');
        try {
          const response = await apiRequest('GET', `/payments/orders?${new URLSearchParams({ [scope]: value.trim() })}`);
          setOrders(response.orders || []);
        } catch (failure) {
          setOrders(null);
          setError(errorMessage(failure));
        } finally {
          setLoading(false);
        }
      }}>
        <label className="grid gap-1 text-sm">查询方式
          <select className="input-field" value={scope} onChange={(event) => setScope(event.target.value)}>
            <option value="order_id">订单号</option>
            <option value="user_id">用户 ID</option>
          </select>
        </label>
        <label className="grid gap-1 text-sm">编号
          <input className="input-field" inputMode="numeric" pattern="[0-9]+" required value={value} onChange={(event) => setValue(event.target.value)} />
        </label>
        <button type="submit" className="btn-primary" disabled={loading}>查询</button>
      </form>
      {error && <p role="alert">{error}</p>}
      <TableRegion ariaLabel="客服订单查询结果">
        <table className="cms-table">
          <thead><tr><th>订单</th><th>用户</th><th>状态</th><th>退款进度</th><th>权益</th></tr></thead>
          <tbody>
            {loading ? <SkeletonRows rows={3} cols={5} /> : orders?.map((order) => (
              <tr key={order.id}>
                <td>#{order.id}</td><td>{order.user_id}</td><td>{order.status}</td><td>{order.refund_status || '—'}</td>
                <td>{order.order_type === 'whole_book' ? `整本阅读 #${order.novel_id}` :
                  order.order_type === 'subscription' ? 'VIP' : `${order.coins + order.bonus_coins_credited} 金币`}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
      {orders?.length === 0 && !loading && <p role="status">未查询到订单。</p>}
    </div>
  );
}

type Subscription = { id: string; user_id: string; provider: string; status: string; cycle: string; paid_until: string; environment: string };

type StripePeriod = {
  invoice_ref: string; order_id: string; service_start?: string; service_end?: string; billed_minor: number;
  billing_reason: string; review_reason?: string; admitted: boolean; allocation_count: number; status: string; sales_mode: string;
  environment: 'live' | 'sandbox' | 'unknown'; country?: string; observed_tax_liability_account?: string; observed_customer_tax_status?: string;
  provider_status?: string; revises_invoice_ref?: string; revision_verified?: boolean;
};

export function PaymentSubscriptions({ support, finance }: { support: boolean; finance: boolean }) {
  const [user, setUser] = useState('');
  const [selected, setSelected] = useState<Subscription | null>(null);
  const [periods, setPeriods] = useState<StripePeriod[] | null>(null);
  const [periodError, setPeriodError] = useState('');
  const select = (item: Subscription) => {
    setPeriods(null);
    setPeriodError('');
    setSelected(item);
  };
  useEffect(() => {
    if (!selected) return;
    const controller = new AbortController();
    void apiRequest('GET', `/payments/subscriptions/${selected.id}/${selected.provider}-periods`, undefined, false, controller.signal)
      .then((result) => { if (!controller.signal.aborted) setPeriods(result.periods ?? []); })
      .catch((failure) => { if (!controller.signal.aborted) setPeriodError(errorMessage(failure)); });
    return () => controller.abort();
  }, [selected]);
  const [results, setResults] = useState<Subscription[] | null>(null);
  const [loading, setLoading] = useState(!support);
  const [error, setError] = useState('');
  const load = useCallback(async (id: string, signal?: AbortSignal) => {
    setLoading(true);
    setError('');
    try {
      const response = await apiRequest('GET', `/payments/subscriptions?${new URLSearchParams({ user_id: id })}`, undefined, false, signal);
      if (!signal?.aborted) setResults(response.subscriptions || []);
    } catch (failure) {
      if (!signal?.aborted) setError(errorMessage(failure));
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, []);
  useEffect(() => {
    if (support) return;
    const controller = new AbortController();
    void load('', controller.signal);
    return () => controller.abort();
  }, [load, support]);
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">订阅与账期</h1>
      <form className="flex flex-wrap items-end gap-2" onSubmit={(event) => { event.preventDefault(); void load(user.trim()); }}>
        <label className="grid gap-1 text-sm">用户 ID
          <input className="input-field" inputMode="numeric" pattern="[0-9]+" required={support} value={user} onChange={(event) => setUser(event.target.value)} />
        </label>
        <button type="submit" className="btn-primary" disabled={loading}>查询</button>
      </form>
      {error && <p role="alert">{error}</p>}
      <TableRegion ariaLabel="订阅与账期">
        <table className="cms-table">
          <thead><tr><th>订阅编号</th><th>用户</th><th>渠道</th><th>协议状态</th><th>周期</th><th>已付至</th>{finance && <th scope="col">账期明细</th>}</tr></thead>
          <tbody>
            {loading ? <SkeletonRows rows={3} cols={finance ? 7 : 6} /> : results?.map((item) => (
              <tr key={item.id}>
                <td>#{item.id}</td><td>{item.user_id}</td><td>{item.provider} · {item.environment === 'unknown' ? '环境待核对' : item.environment}</td><td>{item.status}</td>
                <td>{item.cycle}</td><td>{item.paid_until ? new Date(item.paid_until).toLocaleString() : '—'}</td>
                {finance && <td>{['stripe', 'paypal'].includes(item.provider) && (
                  <button type="button" className="btn-detail" onClick={() => select(item)}>账期 #{item.id}</button>
                )}</td>}
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
      {results?.length === 0 && !loading && <p role="status">未查询到订阅。</p>}
      <CmsDialog open={selected !== null} onClose={() => setSelected(null)} title={selected?.provider === 'paypal' ? 'PayPal VIP 账期' : 'Stripe VIP 账期'}
        subtitle={selected ? `订阅 #${selected.id}` : undefined}>
        {periodError && <p role="alert">{periodError}</p>}
        {!periods && !periodError && <p role="status">正在查询账期…</p>}
        {periods?.length === 0 && <p role="status">暂无已记录的账期。</p>}
        <ul className="space-y-3">
          {periods?.map((period) => (
            <li key={period.order_id} className="rounded border border-[var(--border-color)] p-3">
              <div className="flex flex-wrap items-center gap-2">
                {selected?.provider === 'stripe' ?
<a className="btn-detail" href={`https://dashboard.stripe.com/${period.environment === 'sandbox' ? 'test/' : ''}invoices/${encodeURIComponent(period.invoice_ref)}`}
                  target="_blank" rel="noreferrer">{period.invoice_ref}</a> : <span>{period.invoice_ref || '原收款凭证待确认'}</span>}
                <Link className="btn-detail" to={`/payments/orders?order_id=${period.order_id}`}>订单 #{period.order_id}</Link>
                {period.environment !== 'unknown' ? <>
                  <Link className="btn-detail" to={`/payments/refunds?order_id=${period.order_id}&environment=${period.environment}`}>退款</Link>
                  <Link className="btn-detail" to={`/payments/disputes?order_id=${period.order_id}&environment=${period.environment}`}>争议</Link>
                </> : <span>渠道环境待核对</span>}
              </div>
              <p>USD {(period.billed_minor / 100).toFixed(2)} · {period.sales_mode === 'direct' ? '直收' : 'Managed Payments'} · {period.allocation_count} 笔收款分配</p>
              {period.service_start && period.service_end && <p>{new Date(period.service_start).toLocaleString()} – {new Date(period.service_end).toLocaleString()}</p>}
              <p>{period.admitted ? '账期已确认' : '账期未确认'} · {period.status}</p>
              {period.provider_status && <p>渠道账单状态 {period.provider_status}</p>}
              {period.revises_invoice_ref && <p>原账单 {period.revises_invoice_ref} · {period.revision_verified ? '关联已核实' : '关联待核实'}</p>}
              {period.country && <p>账单地区 {period.country}</p>}
              {period.observed_tax_liability_account && <p>税务责任账户 {period.observed_tax_liability_account}</p>}
              {period.observed_customer_tax_status && <p>客户税务状态 {period.observed_customer_tax_status}</p>}
              {period.review_reason && <p>{period.review_reason}</p>}
              {selected?.provider === 'stripe' && period.review_reason && <StripeMarketReview key={period.order_id} orderID={period.order_id} />}
            </li>
          ))}
        </ul>
      </CmsDialog>
    </div>
  );
}

type Channel = {
  id: number;
  provider: string;
  environment: string;
  account_label: string;
  credentials_ref: string;
  webhook_ref: string;
  new_orders_enabled: boolean;
  new_subscriptions_enabled: boolean;
};

const offerStates = {
  no_account: { label: '待绑定账户', tone: 'warning' },
  queued: { label: '等待发布', tone: 'muted' },
  publishing: { label: '发布中', tone: 'primary' },
  review: { label: '待核对', tone: 'warning' },
  ready: { label: '已绑定', tone: 'success' },
} as const;

type VIPOfferPublication = {
  account_id?: number;
  account_label?: string;
  environment?: string;
  version_id: number;
  template: string;
  name: string;
  amount_minor: number;
  currency: string;
  cycle: 'day' | 'week' | 'month';
  plan_id?: string;
  request_key?: string;
  state: keyof typeof offerStates;
};

const cycleLabels = { day: '每日', week: '每周', month: '每月' };

export function PaymentChannels() {
  const [channels, setChannels] = useState<Channel[] | null>(null);
  const [offers, setOffers] = useState<VIPOfferPublication[] | null>(null);
  const [offerError, setOfferError] = useState('');
  const [editing, setEditing] = useState<Channel | null>(null);
  const [stripeAccount, setStripeAccount] = useState<{ channel: Channel; kind: 'one-time' | 'vip' } | null>(null);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const load = useCallback(async (signal?: AbortSignal) => {
    setError('');
    try {
      const response = await apiRequest('GET', '/payments/channels', undefined, false, signal);
      if (!signal?.aborted) setChannels(response.channels || []);
    } catch (failure) {
      if (!signal?.aborted) setError(errorMessage(failure));
    }
  }, []);
  const loadOffers = useCallback(async (signal?: AbortSignal) => {
    setOfferError('');
    try {
      const response = await apiRequest('GET', '/payments/channels/vip-offers', undefined, false, signal);
      if (!signal?.aborted) setOffers(response.offers);
    } catch (failure) {
      if (!signal?.aborted) setOfferError(errorMessage(failure));
    }
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    void loadOffers(controller.signal);
    return () => controller.abort();
  }, [load, loadOffers]);
  const save = async () => {
    if (!editing || saving) return;
    setSaving(true);
    setError('');
    try {
      const { credentials_ref, new_orders_enabled, new_subscriptions_enabled } = editing;
      await apiRequest('PUT', `/payments/channels/${editing.id}`, { credentials_ref, new_orders_enabled, new_subscriptions_enabled });
      setEditing(null);
      await Promise.all([load(), loadOffers()]);
    } catch (failure) {
      setError(errorMessage(failure));
    } finally {
      setSaving(false);
    }
  };
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">渠道与商户</h1>
      <button type="button" className="btn-secondary" onClick={() => void Promise.all([load(), loadOffers()])}>刷新</button>
      {error && !editing && <p role="alert">{error}</p>}
      <TableRegion ariaLabel="支付渠道与商户">
        <table className="cms-table">
          <thead><tr><th>渠道</th><th>账户</th><th>环境</th><th>凭证来源</th><th>Webhook 配置</th><th>单次购买</th><th>新订阅</th><th>操作</th></tr></thead>
          <tbody>
            {!channels && !error ? <SkeletonRows rows={2} cols={8} /> : channels?.map((channel) => (
              <tr key={channel.id}>
                <td>{channel.provider}</td><td>{channel.account_label}</td><td>{channel.environment}</td>
                <td>{channel.credentials_ref}</td><td>{channel.webhook_ref}</td>
                <td>{channel.new_orders_enabled ? '开放' : '暂停'}</td>
                <td>{channel.new_subscriptions_enabled ? '开放' : '暂停'}</td>
                <td>
                  {['paypal', 'stripe'].includes(channel.provider) &&
                    <button type="button" className="btn-detail" onClick={() => setEditing(channel)}>配置</button>}
                  {channel.provider === 'stripe' &&
                    <>
                      <button type="button" className="btn-detail" onClick={() => setStripeAccount({ channel, kind: 'one-time' })}>整本套餐</button>
                      <button type="button" className="btn-detail" onClick={() => setStripeAccount({ channel, kind: 'vip' })}>VIP 套餐</button>
                    </>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
      {channels?.length === 0 && <p role="status">暂无已绑定的渠道账户。</p>}
      {stripeAccount && <StripeOfferSettings key={`${stripeAccount.channel.id}:${stripeAccount.kind}`}
        accountID={stripeAccount.channel.id} accountLabel={stripeAccount.channel.account_label} environment={stripeAccount.channel.environment} kind={stripeAccount.kind} />}
      <h2 className="text-base font-semibold">PayPal VIP 套餐版本</h2>
      {offerError && <p role="alert">{offerError}</p>}
      <TableRegion ariaLabel="PayPal VIP 套餐版本">
        <table className="cms-table">
          <caption className="sr-only">PayPal VIP 套餐与计划绑定状态</caption>
          <thead><tr>
            <th scope="col">套餐</th><th scope="col">模板</th><th scope="col">版本</th><th scope="col">账户与环境</th>
            <th scope="col">价格</th><th scope="col">周期</th><th scope="col">发布状态</th><th scope="col">计划／核对编号</th>
          </tr></thead>
          <tbody>
            {!offers && !offerError ? <SkeletonRows rows={2} cols={8} /> : offers?.map((offer) => (
              <tr key={`${offer.version_id}:${offer.account_id ?? 0}`}>
                <th scope="row" className="text-left">{offer.name}</th>
                <td>{offer.template}</td><td>#{offer.version_id}</td>
                <td>{offer.account_label ? `${offer.account_label} · ${offer.environment}` : '—'}</td>
                <td>{(offer.amount_minor / 100).toFixed(2)} {offer.currency}</td>
                <td>{cycleLabels[offer.cycle]}</td>
                <td><StatusDot {...offerStates[offer.state]} /></td>
                <td className="font-mono">{offer.plan_id || (offer.state === 'review' && offer.request_key) || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
      {offers?.length === 0 && <p role="status">暂无 PayPal VIP 套餐。</p>}
      <CmsDialog open={editing !== null} onClose={() => { if (!saving) setEditing(null); }} title="修改渠道配置"
        subtitle={editing ? `${editing.provider} · ${editing.account_label} · ${editing.environment}` : undefined}
        closedBy="closerequest" closeDisabled={saving}
        footer={<>
          <button type="button" data-dialog-initial-focus className="btn-secondary" disabled={saving} onClick={() => setEditing(null)}>取消</button>
          <button type="button" className="btn-primary" disabled={saving} onClick={() => void save()}>{saving ? '保存中…' : '保存配置'}</button>
        </>}>
        {editing && <div className="grid gap-3">
          {editing.provider === 'paypal' && <label className="grid gap-1">凭证来源
            <select className="input-field" value={editing.credentials_ref}
              onChange={(event) => setEditing({ ...editing, credentials_ref: event.target.value })}>
              <option value="legacy:paypal">现有服务端 PayPal 配置</option>
              <option value="env:paypal">服务端环境变量</option>
            </select>
          </label>}
          <label><input type="checkbox" checked={editing.new_orders_enabled}
            onChange={(event) => setEditing({ ...editing, new_orders_enabled: event.target.checked })} /> 允许新单次购买</label>
          <label><input type="checkbox" checked={editing.new_subscriptions_enabled}
            onChange={(event) => setEditing({ ...editing, new_subscriptions_enabled: event.target.checked })} /> 允许新 VIP 订阅</label>
          {error && <p role="alert">{error}</p>}
        </div>}
      </CmsDialog>
    </div>
  );
}

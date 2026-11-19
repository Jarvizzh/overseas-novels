import { type FormEvent, useEffect, useId, useRef, useState } from 'react';
import { TableRegion } from '../components/ui/Primitives';
import { apiRequest } from '../utils/api';
import { sourceEventLabel, status, timestamp } from '../utils/conversionStatus';

export interface Delivery {
  id: string;
  pixel_id: string;
  role: 'original' | 'additional';
  state: string;
  reason?: string;
  meta_event_id: string;
  attempts: number;
  last_attempt_at?: string;
  next_attempt_at?: string;
  deadline_at?: string;
  confirmed_at?: string;
  last_attempt?: {
    http_status?: number;
    graph_code?: number;
    graph_subcode?: number;
    fbtrace_id?: string;
    reason?: string;
  };
}

interface OrderEvent {
  id?: string;
  business_key: string;
  source_event_type?: string;
  event_name: string;
  state: string;
  reason?: string;
  occurred_at?: string;
  enqueued_at?: string;
  deliveries: Delivery[];
  deliveries_truncated: boolean;
  legacy?: { state: string; confirmed_at?: string; environment_verified?: boolean };
  partner_report?: { state: string; status: number; reason?: string };
}

interface OrderReport {
  order_id: string;
  source: string;
  environment: string;
  integration_id?: string;
  order_type?: string;
  order_status?: string;
  eligibility?: {
    state: string;
    reason?: string;
    pixel_id?: string;
    occurred_at?: string;
    time_source?: string;
    fulfilled_at?: string;
  };
  events: OrderEvent[];
  events_truncated?: boolean;
  inbox_truncated?: boolean;
  inbox?: {
    id: string;
    source_event_id?: string;
    source_event_type: string;
    state: string;
    reason?: string;
    received_at: string;
  }[];
}

const identifierInput = {
  autoComplete: 'off',
  autoCorrect: 'off',
  autoCapitalize: 'none',
  spellCheck: false,
};

export function TargetResults({ deliveries }: { deliveries: Delivery[] }) {
  return (
    <TableRegion ariaLabel="Pixel 投递结果">
      <table className="cms-table min-w-[760px]" data-density="compact">
        <caption className="sr-only">Pixel 投递结果</caption>
        <thead className="sticky top-0">
          <tr>
            <th scope="col">Pixel</th>
            <th scope="col">结果</th>
            <th scope="col">Meta 事件 ID / 尝试</th>
            <th scope="col">投递时间</th>
            <th scope="col">最近响应</th>
          </tr>
        </thead>
        <tbody>
          {deliveries.map((delivery) => (
            <tr key={delivery.id}>
              <th scope="row" className="whitespace-nowrap text-left font-medium">
                {delivery.pixel_id}
                <span className="block font-normal text-[var(--cms-muted-fg)]">
                  {delivery.role === 'original' ? '原目标' : '附加目标'}
                </span>
              </th>
              <td>
                {status(delivery.state)}
                {delivery.reason && (
                  <span className="block text-[var(--cms-muted-fg)]">{delivery.reason}</span>
                )}
              </td>
              <td className="break-all">
                {delivery.meta_event_id}
                <span className="block text-[var(--cms-muted-fg)]">{delivery.attempts} 次</span>
              </td>
              <td className="whitespace-nowrap">
                <div>上次 {timestamp(delivery.last_attempt_at)}</div>
                {delivery.next_attempt_at && <div>下次 {timestamp(delivery.next_attempt_at)}</div>}
                {delivery.deadline_at && <div>截止 {timestamp(delivery.deadline_at)}</div>}
                {delivery.confirmed_at && <div>确认 {timestamp(delivery.confirmed_at)}</div>}
              </td>
              <td>
                {delivery.last_attempt ? (
                  <>
                    {delivery.last_attempt.http_status && <div>HTTP {delivery.last_attempt.http_status}</div>}
                    {delivery.last_attempt.graph_code && (
                      <div>
                        Graph {delivery.last_attempt.graph_code} /{' '}
                        {delivery.last_attempt.graph_subcode ?? '—'}
                      </div>
                    )}
                    {delivery.last_attempt.fbtrace_id && (
                      <div className="break-all">{delivery.last_attempt.fbtrace_id}</div>
                    )}
                    {delivery.last_attempt.reason && <div>{delivery.last_attempt.reason}</div>}
                  </>
                ) : (
                  '—'
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableRegion>
  );
}

export default function ConversionOrdersPanel() {
  const id = useId();
  const [order, setOrder] = useState('');
  const [integration, setIntegration] = useState('');
  const [report, setReport] = useState<OrderReport>();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const request = useRef<AbortController | null>(null);

  useEffect(() => () => request.current?.abort(), []);

  async function search(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (request.current) return;
    const controller = new AbortController();
    request.current = controller;
    setError('');
    setReport(undefined);
    setLoading(true);
    const query = integration.trim() ? `?${new URLSearchParams({ integration_id: integration.trim() })}` : '';
    try {
      const result: OrderReport = await apiRequest(
        'GET',
        `/conversion/orders/${encodeURIComponent(order.trim())}${query}`,
        undefined,
        false,
        controller.signal,
      );
      if (!controller.signal.aborted) setReport(result);
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '查询失败');
    } finally {
      request.current = null;
      if (!controller.signal.aborted) setLoading(false);
    }
  }

  return (
    <div className="space-y-4">
      <search aria-labelledby={`${id}-heading`} className="block">
        <h2 id={`${id}-heading`} className="mb-3 text-sm font-semibold">
          逐单对账
        </h2>
        <form
          method="get"
          action="/ad-config/logs"
          aria-labelledby={`${id}-heading`}
          onSubmit={search}
          className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-start"
        >
          <div className="grid gap-1 text-sm">
            <label htmlFor={`${id}-order`}>订单号（必填）</label>
            <input
              {...identifierInput}
              id={`${id}-order`}
              name="order_id"
              type="text"
              required
              maxLength={128}
              inputMode={integration ? 'text' : 'numeric'}
              value={order}
              onChange={(event) => setOrder(event.target.value)}
              className="input-field min-h-12 text-base"
            />
          </div>
          <div className="grid gap-1 text-sm">
            <label htmlFor={`${id}-integration`}>接入 ID（选填）</label>
            <input
              {...identifierInput}
              id={`${id}-integration`}
              name="integration_id"
              type="text"
              inputMode="numeric"
              pattern="[1-9][0-9]{0,18}"
              maxLength={19}
              value={integration}
              aria-describedby={`${id}-hint`}
              onChange={(event) => setIntegration(event.target.value)}
              className="input-field min-h-12 text-base"
            />
            <span id={`${id}-hint`} className="text-xs text-[var(--cms-muted-fg)]">
              分销订单填写接入 ID，本地订单留空。
            </span>
          </div>
          <button type="submit" disabled={loading} className="btn-primary min-h-12 sm:mt-6">
            {loading ? '查询中…' : '查询订单'}
          </button>
        </form>
      </search>
      <div role="status" className="text-sm text-[var(--cms-muted-fg)]">
        {loading
          ? '正在查询订单'
          : report
            ? `订单 ${report.order_id}，找到 ${report.events.length} 条事件`
            : ''}
      </div>
      {error && (
        <p role="alert" className="text-sm text-[var(--cms-error)]">
          {error}
        </p>
      )}
      {report && (
        <div className="space-y-4">
          <dl className="grid gap-3 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-card)] p-4 text-sm sm:grid-cols-3">
            <div>
              <dt>订单号</dt>
              <dd className="break-all">{report.order_id}</dd>
            </div>
            <div>
              <dt>来源 / 环境</dt>
              <dd>
                {report.source} / {report.environment}
              </dd>
            </div>
            {report.integration_id && (
              <div>
                <dt>接入 ID</dt>
                <dd>{report.integration_id}</dd>
              </div>
            )}
            {report.order_type && (
              <div>
                <dt>订单类型 / 状态</dt>
                <dd>
                  {report.order_type} / {report.order_status}
                </dd>
              </div>
            )}
            {report.eligibility && (
              <>
                <div>
                  <dt>回传资格</dt>
                  <dd>
                    {status(report.eligibility.state)} {report.eligibility.reason}
                  </dd>
                </div>
                <div>
                  <dt>原 Pixel</dt>
                  <dd>{report.eligibility.pixel_id ?? '—'}</dd>
                </div>
                <div>
                  <dt>业务时间</dt>
                  <dd>
                    {timestamp(report.eligibility.occurred_at)} {report.eligibility.time_source}
                  </dd>
                </div>
                <div>
                  <dt>履约时间</dt>
                  <dd>{timestamp(report.eligibility.fulfilled_at)}</dd>
                </div>
              </>
            )}
          </dl>
          {Boolean(report.inbox?.length) && (
            <div className="space-y-2">
              <h3 className="text-sm font-semibold">分销回调</h3>
              <ul className="list-disc space-y-1 pl-5 text-sm">
                {report.inbox?.map((item) => (
                  <li key={item.id} className="break-all">
                    {sourceEventLabel(report.source, item.source_event_type)} ·{' '}
                    {item.source_event_id ?? '缺少事件 ID'} · {timestamp(item.received_at)} ·{' '}
                    {status(item.state)} {item.reason}
                  </li>
                ))}
              </ul>
            </div>
          )}
          {report.events.length === 0 && (
            <p className="text-sm text-[var(--cms-muted-fg)]">该订单暂无转化事件。</p>
          )}
          {report.events.map((event) => (
            <div key={event.id ?? event.business_key} className="space-y-2">
              <h3 className="text-sm font-semibold">
                {event.source_event_type &&
                  `${sourceEventLabel(report.source, event.source_event_type)} · `}
                {event.event_name} · {status(event.state)}
              </h3>
              <p className="break-all text-xs text-[var(--cms-muted-fg)]">
                {event.business_key} {event.id && `· 内部 ID ${event.id}`}{' '}
                {event.reason && `· ${event.reason}`}
              </p>
              {(event.occurred_at || event.enqueued_at) && (
                <p className="text-xs text-[var(--cms-muted-fg)]">
                  发生 {timestamp(event.occurred_at)} · 入队 {timestamp(event.enqueued_at)}
                </p>
              )}
              {event.legacy && (
                <p className="text-xs text-[var(--cms-muted-fg)]">
                  历史记录 · {status(event.legacy.state)}
                  {event.legacy.confirmed_at && ` · 确认 ${timestamp(event.legacy.confirmed_at)}`}
                  {event.legacy.environment_verified === false && ' · 环境未核实'}
                </p>
              )}
              {event.deliveries.length > 0 && <TargetResults deliveries={event.deliveries} />}
              {event.deliveries_truncated && <p className="text-xs">显示前 50 个目标，原目标优先。</p>}
              {event.partner_report && (
                <p className="text-xs">
                  结果回告 · {status(event.partner_report.state)} · {event.partner_report.status}{' '}
                  {event.partner_report.reason && status(event.partner_report.reason)}
                </p>
              )}
            </div>
          ))}
          {(report.events_truncated || report.inbox_truncated) && (
            <p className="text-xs text-[var(--cms-muted-fg)]">事件与回调各最多显示最近 100 条。</p>
          )}
        </div>
      )}
    </div>
  );
}

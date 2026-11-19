import { type FormEvent, useEffect, useId, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { apiRequest } from '../utils/api';
import { TableRegion } from '../components/ui/Primitives';
import { type Delivery, TargetResults } from './ConversionOrdersPanel';
import { sourceEventLabel, status, timestamp } from '../utils/conversionStatus';

interface PartnerReport {
  id: string;
  state: string;
  status: number;
  reason?: string;
  next_attempt_at?: string;
  attempt_count: number;
  attempts?: {
    number: number;
    outcome?: string;
    reason?: string;
    http_status?: number;
    business_code?: string;
    started_at: string;
    sending_at?: string;
    responded_at?: string;
    recorded_at?: string;
    retry_at?: string;
  }[];
}

function PartnerReportAttempts({ report }: { report: PartnerReport }) {
  const attempts = report.attempts ?? [];
  if (!attempts.length) return null;

  return (
    <TableRegion ariaLabel="司南回告尝试">
      <table className="cms-table min-w-[900px]" data-density="compact">
        <caption className="sr-only">司南回告尝试</caption>
        <thead className="sticky top-0">
          <tr>
            <th scope="col">次数</th>
            <th scope="col">结果</th>
            <th scope="col">HTTP</th>
            <th scope="col">司南业务码</th>
            <th scope="col">领取时间</th>
            <th scope="col">发送时间</th>
            <th scope="col">响应时间</th>
            <th scope="col">记录时间</th>
            <th scope="col">计划重试时间</th>
          </tr>
        </thead>
        <tbody>
          {attempts.map((attempt) => (
            <tr key={attempt.number}>
              <th scope="row">{attempt.number}</th>
              <td>
                {attempt.outcome ? status(attempt.outcome) : '进行中'}
                {attempt.reason && <div>{status(attempt.reason)}</div>}
              </td>
              <td>{attempt.http_status ?? '—'}</td>
              <td>{attempt.business_code ?? '—'}</td>
              <td>{timestamp(attempt.started_at)}</td>
              <td>{timestamp(attempt.sending_at)}</td>
              <td>{timestamp(attempt.responded_at)}</td>
              <td>{timestamp(attempt.recorded_at)}</td>
              <td>{timestamp(attempt.retry_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {report.attempt_count > attempts.length && (
        <p className="text-xs text-[var(--cms-muted-fg)]">已显示最近 {attempts.length} 次</p>
      )}
    </TableRegion>
  );
}

interface ConversionEvent {
  id: string;
  integration_id: string;
  source: string;
  source_event_id: string;
  source_event_type: string;
  event_name: string;
  order_id?: string;
  attribution_state: string;
  occurred_at?: string;
  created_at: string;
  deliveries: Delivery[];
  deliveries_truncated: boolean;
  partner_reports?: PartnerReport[];
}

interface EventPage {
  events: ConversionEvent[];
  next_cursor: string | null;
}

interface Target {
  id: string;
  pixel_id: string;
  display_name: string;
}

type Action =
  | { kind: 'append'; event: ConversionEvent }
  | { kind: 'retry'; delivery: Delivery }
  | { kind: 'report'; id: string };

function errorText(error: unknown) {
  return error instanceof Error ? error.message : '请求失败';
}

function ActionForm({
  action,
  onDone,
  onCancel,
}: {
  action: Action;
  onDone: () => void;
  onCancel: () => void;
}) {
  const id = useId();
  const [targets, setTargets] = useState<Target[]>();
  const [reason, setReason] = useState('');
  const [target, setTarget] = useState('');
  const [acknowledged, setAcknowledged] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [invalidReason, setInvalidReason] = useState(false);
  const reasonField = useRef<HTMLTextAreaElement>(null);
  const inFlight = useRef(false);
  const eventID = action.kind === 'append' ? action.event.id : '';

  useEffect(() => {
    if (!eventID) return;
    const controller = new AbortController();
    apiRequest('GET', `/conversion/events/${eventID}/destinations`, undefined, false, controller.signal)
      .then(setTargets)
      .catch((err) => {
        if (!controller.signal.aborted) setError(errorText(err));
      });
    return () => controller.abort();
  }, [eventID]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (inFlight.current) return;
    if (!reason.trim()) {
      reasonField.current?.setCustomValidity('请输入操作原因。');
      reasonField.current?.reportValidity();
      return;
    }
    inFlight.current = true;
    setBusy(true);
    setError('');
    const path =
      action.kind === 'append'
        ? `/conversion/events/${action.event.id}/deliveries`
        : action.kind === 'retry'
          ? `/conversion/deliveries/${action.delivery.id}/retry`
          : `/conversion/partner-reports/${action.id}/retry`;
    const data =
      action.kind === 'append'
        ? { reason: reason.trim(), destination_id: target }
        : action.kind === 'retry'
          ? {
              reason: reason.trim(),
              expected_attempts: action.delivery.attempts,
              acknowledge_duplicate_risk: acknowledged,
            }
          : { reason: reason.trim() };
    try {
      await apiRequest('POST', path, data);
      onDone();
    } catch (err) {
      setError(errorText(err));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  const title = action.kind === 'append' ? '追加 Pixel' : action.kind === 'retry' ? '重排投递' : '重排回告';
  return (
    <form
      onSubmit={submit}
      aria-labelledby={`${id}-title`}
      className="space-y-3 rounded-xl border border-[var(--cms-border)] p-4"
    >
      <h4 id={`${id}-title`} className="text-sm font-semibold">
        {title}
      </h4>
      {action.kind === 'append' && (
        <div className="grid gap-1 text-sm">
          <label htmlFor={`${id}-target`}>授权目标 Pixel</label>
          <select
            id={`${id}-target`}
            name="destination_id"
            required
            value={target}
            onChange={(event) => setTarget(event.target.value)}
            disabled={!targets?.length || busy}
            className="input-field"
          >
            <option value="">
              {targets ? (targets.length ? '请选择目标' : '没有可追加的目标') : '正在加载目标…'}
            </option>
            {targets?.map((item) => (
              <option key={item.id} value={item.id}>
                {item.pixel_id} {item.display_name}
              </option>
            ))}
          </select>
        </div>
      )}
      <div className="grid gap-1 text-sm">
        <label htmlFor={`${id}-reason`}>操作原因（必填）</label>
        <textarea
          ref={reasonField}
          id={`${id}-reason`}
          name="reason"
          required
          maxLength={500}
          rows={2}
          value={reason}
          aria-invalid={invalidReason}
          aria-describedby={invalidReason ? `${id}-reason-error` : undefined}
          onInvalid={() => setInvalidReason(true)}
          onChange={(event) => {
            event.target.setCustomValidity('');
            if (event.target.validity.valid) setInvalidReason(false);
            setReason(event.target.value);
          }}
          className="input-field user-invalid:border-[var(--cms-error)]"
        />
        {invalidReason && (
          <p id={`${id}-reason-error`} className="text-xs text-[var(--cms-error)]">
            请输入 1 至 500 字的操作原因。
          </p>
        )}
      </div>
      {action.kind === 'retry' && action.delivery.state === 'outcome_unknown' && (
        <label className="flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            name="acknowledge_duplicate_risk"
            required
            checked={acknowledged}
            onChange={(event) => setAcknowledged(event.target.checked)}
          />
          我确认 Meta 可能已接收该事件，重排可能导致重复计数。
        </label>
      )}
      {error && (
        <p role="alert" className="text-sm text-[var(--cms-error)]">
          {error}
        </p>
      )}
      <div className="flex gap-2">
        <button
          type="submit"
          className="btn-primary"
          disabled={busy || (action.kind === 'append' && !targets?.length)}
        >
          {busy ? '正在提交…' : title}
        </button>
        <button type="button" className="btn-secondary" disabled={busy} onClick={onCancel}>
          取消
        </button>
      </div>
    </form>
  );
}

interface Attempt {
  number: number;
  outcome?: string;
  reason?: string;
  started_at: string;
  http_status?: number;
  fbtrace_id?: string;
}

function EventDetail({
  event,
  canWrite,
  onChanged,
}: {
  event: ConversionEvent;
  canWrite: boolean;
  onChanged: () => void;
}) {
  const [detail, setDetail] = useState(event);
  const [action, setAction] = useState<Action>();
  const [message, setMessage] = useState('');
  const [attempts, setAttempts] = useState<Attempt[]>([]);
  const [selected, setSelected] = useState('');
  const [attemptBusy, setAttemptBusy] = useState(false);
  const [more, setMore] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    apiRequest('GET', `/conversion/events/${event.id}`, undefined, false, controller.signal)
      .then(setDetail)
      .catch((error) => {
        if (!controller.signal.aborted) setMessage(errorText(error));
      });
    return () => controller.abort();
  }, [event.id]);

  async function showAttempts(id: string, before?: number) {
    setAttemptBusy(true);
    setSelected(id);
    if (!before) setAttempts([]);
    try {
      const query = new URLSearchParams({ delivery_id: id });
      if (before) query.set('before_attempt', String(before));
      const result: { attempts: Attempt[] } = await apiRequest(
        'GET',
        `/conversion/events/${event.id}?${query}`,
      );
      setAttempts((current) => (before ? [...current, ...result.attempts] : result.attempts));
      setMore(result.attempts.length === 50);
    } catch (error) {
      setMessage(errorText(error));
    } finally {
      setAttemptBusy(false);
    }
  }

  async function reload() {
    setAction(undefined);
    setMessage('操作已写入审计记录。');
    try {
      setDetail(await apiRequest('GET', `/conversion/events/${event.id}`));
      onChanged();
    } catch (error) {
      setMessage(`操作已提交，刷新详情失败：${errorText(error)}`);
    }
  }

  return (
    <div className="space-y-3">
      <p className="break-all text-xs text-[var(--cms-muted-fg)]">
        {detail.source_event_id} · {timestamp(detail.occurred_at)}
      </p>
      {detail.deliveries.length > 0 && <TargetResults deliveries={detail.deliveries} />}
      {detail.deliveries_truncated && <p className="text-xs">显示前 50 个目标，原目标优先。</p>}
      <div className="flex flex-wrap gap-2">
        {canWrite && (
          <button
            type="button"
            className="btn-secondary"
            onClick={() => setAction({ kind: 'append', event })}
          >
            追加 Pixel
          </button>
        )}
        {detail.deliveries.map((delivery) => (
          <span key={delivery.id} className="inline-flex flex-wrap gap-1">
            {delivery.attempts > 0 && (
              <button
                type="button"
                className="btn-secondary"
                disabled={attemptBusy}
                onClick={() => void showAttempts(delivery.id)}
              >
                尝试记录 · {delivery.pixel_id}
              </button>
            )}
            {canWrite && ['pending', 'blocked', 'outcome_unknown'].includes(delivery.state) && (
              <button
                type="button"
                className="btn-secondary"
                onClick={() => setAction({ kind: 'retry', delivery })}
              >
                重排 · {delivery.pixel_id}
              </button>
            )}
          </span>
        ))}
      </div>
      {detail.partner_reports?.map((report) => (
        <div key={report.id} className="space-y-2">
          <p className="flex flex-wrap items-center gap-2 text-xs">
            回告 · {status(report.state)} · {report.status} {report.reason && status(report.reason)}
            {report.next_attempt_at && ` · 下次尝试 ${timestamp(report.next_attempt_at)}`}
            {canWrite && report.state !== 'confirmed' && (
              <button
                type="button"
                className="btn-secondary"
                onClick={() => setAction({ kind: 'report', id: report.id })}
              >
                重排回告
              </button>
            )}
          </p>
          <PartnerReportAttempts report={report} />
        </div>
      ))}
      {action && (
        <ActionForm
          key={JSON.stringify(action)}
          action={action}
          onDone={() => void reload()}
          onCancel={() => setAction(undefined)}
        />
      )}
      {selected && (
        <div className="space-y-1 text-xs" aria-busy={attemptBusy}>
          {attempts.length === 0 && !attemptBusy && <p>该目标暂无结构化尝试记录。</p>}
          <ul className="list-disc pl-5">
            {attempts.map((attempt) => (
              <li key={attempt.number}>
                第 {attempt.number} 次 · {timestamp(attempt.started_at)} · {attempt.outcome ?? '进行中'}{' '}
                {attempt.reason}
                {attempt.http_status && ` · HTTP ${attempt.http_status}`}
                {attempt.fbtrace_id && ` · ${attempt.fbtrace_id}`}
              </li>
            ))}
          </ul>
          {more && (
            <button
              type="button"
              className="btn-secondary"
              disabled={attemptBusy}
              onClick={() => void showAttempts(selected, attempts.at(-1)?.number)}
            >
              更多尝试
            </button>
          )}
        </div>
      )}
      <p role="status" className="text-xs text-[var(--cms-muted-fg)]">
        {message}
      </p>
    </div>
  );
}

export default function ConversionEventsPanel({ canWrite }: { canWrite: boolean }) {
  const [routeParams, setRouteParams] = useSearchParams();
  const requestedEvent = routeParams.get('event_id');
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [order, setOrder] = useState('');
  const [filters, setFilters] = useState({ orderID: '', eventName: '' });
  const [eventName, setEventName] = useState('');
  const [cursor, setCursor] = useState('');
  const [page, setPage] = useState<EventPage>({ events: [], next_cursor: null });
  const [selected, setSelected] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(true);
  const [refresh, setRefresh] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    const query = new URLSearchParams();
    if (requestedEvent) {
      query.set('event_id', requestedEvent);
    } else {
      if (filters.orderID) query.set('order_id', filters.orderID);
      if (filters.eventName) query.set('event_name', filters.eventName);
      if (cursor) query.set('cursor', cursor);
    }
    apiRequest('GET', `/conversion/events?${query}`, undefined, false, controller.signal)
      .then((result: EventPage) => {
        setPage(result);
        setSelected((current) => {
          const id = requestedEvent ?? current;
          return result.events.some((item) => item.id === id) ? id : '';
        });
        setError('');
        if (requestedEvent && result.events.some((item) => item.id === requestedEvent)) {
          headingRef.current?.focus();
        }
      })
      .catch((err) => {
        if (!controller.signal.aborted) setError(errorText(err));
      })
      .finally(() => {
        if (!controller.signal.aborted) setBusy(false);
      });
    return () => controller.abort();
  }, [filters, cursor, refresh, requestedEvent]);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setCursor('');
    if (requestedEvent) {
      const params = new URLSearchParams(routeParams);
      params.delete('event_id');
      setRouteParams(params, { replace: true });
    }
    setFilters({ orderID: order.trim(), eventName });
  }

  const active = page.events.find((item) => item.id === selected);
  return (
    <div className="space-y-3">
      <h2 ref={headingRef} id="conversion-events" tabIndex={-1} className="text-sm font-semibold">
        回传事件
      </h2>
      <search>
        <form onSubmit={submit} className="flex flex-wrap items-end gap-2">
          <label className="grid gap-1 text-xs">
            订单号
            <input
              className="input-field"
              name="order_id"
              value={order}
              onChange={(event) => setOrder(event.target.value)}
              maxLength={128}
            />
          </label>
          <label className="grid gap-1 text-xs">
            事件类型
            <select
              className="input-field"
              name="event_name"
              value={eventName}
              onChange={(event) => setEventName(event.target.value)}
            >
              <option value="">全部</option>
              {['CompleteRegistration', 'InitiateCheckout', 'Purchase', 'Subscribe'].map((name) => (
                <option key={name}>{name}</option>
              ))}
            </select>
          </label>
          <button type="submit" className="btn-secondary" disabled={busy}>
            筛选
          </button>
        </form>
      </search>
      {error && (
        <p role="alert" className="text-sm text-[var(--cms-error)]">
          {error}
        </p>
      )}
      <TableRegion ariaLabel="回传事件列表">
        <table className="cms-table min-w-[620px]" data-density="compact">
          <thead>
            <tr>
              <th scope="col">事件</th>
              <th scope="col">来源</th>
              <th scope="col">订单</th>
              <th scope="col">归因</th>
              <th scope="col">发生时间</th>
              <th scope="col">详情</th>
            </tr>
          </thead>
          <tbody>
            {page.events.length === 0 && (
              <tr>
                <td colSpan={6}>{busy ? '正在加载…' : '没有匹配的事件'}</td>
              </tr>
            )}
            {page.events.map((item) => (
              <tr key={item.id}>
                <th scope="row" className="text-left">
                  {item.source === 'sinan' && (
                    <span className="block">
                      {sourceEventLabel(item.source, item.source_event_type)}
                    </span>
                  )}
                  {item.event_name}
                </th>
                <td>{item.source}</td>
                <td>{item.order_id ?? '—'}</td>
                <td>{item.attribution_state}</td>
                <td>{timestamp(item.occurred_at)}</td>
                <td>
                  <button
                    type="button"
                    aria-pressed={selected === item.id}
                    className="btn-detail"
                    onClick={() => setSelected(selected === item.id ? '' : item.id)}
                  >
                    查看 #{item.id}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
      {!requestedEvent && (page.next_cursor || cursor) && (
        <div className="flex gap-2">
          {cursor && (
            <button
              type="button"
              className="btn-secondary"
              disabled={busy}
              onClick={() => {
                setBusy(true);
                setCursor('');
              }}
            >
              返回首页
            </button>
          )}
          {page.next_cursor && (
            <button
              type="button"
              className="btn-secondary"
              disabled={busy}
              onClick={() => {
                setBusy(true);
                setCursor(page.next_cursor ?? '');
              }}
            >
              下一页
            </button>
          )}
        </div>
      )}
      {active && (
        <EventDetail
          key={active.id}
          event={active}
          canWrite={canWrite}
          onChanged={() => setRefresh((value) => value + 1)}
        />
      )}
    </div>
  );
}

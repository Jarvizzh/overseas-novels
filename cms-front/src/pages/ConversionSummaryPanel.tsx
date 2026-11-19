import { type FormEvent, useState } from 'react';
import { Link } from 'react-router-dom';
import { apiRequest } from '../utils/api';
import { TableRegion } from '../components/ui/Primitives';
import { status, timestamp } from '../utils/conversionStatus';

interface Summary {
  population: {
    source: string;
    integration_id: string | null;
    event_name: string;
    eligibility: string;
    events: number;
    confirmed: number;
    missing_outbox: number;
    missing_target: number;
    missing_time: number;
  }[];
  targets: {
    source: string;
    integration_id: string;
    event_name: string;
    destination_id: string;
    pixel_id: string;
    role: string;
    state: string;
    reason?: string;
    deliveries: number;
    oldest_ready_at?: string;
    queue_wait_seconds?: number;
    business_confirmation_seconds?: number;
    queue_confirmation_seconds?: number;
  }[];
  reports: {
    integration_id: string;
    destination_id: string;
    event_name: string;
    state: string;
    reason?: string;
    reports: number;
  }[];
  alerts: {
    kind: string;
    subject: string;
    detail: { count?: number; oldest_at?: string; sample_event_id?: string };
    opened_at: string;
  }[];
  heartbeats: { worker_type: string; instance_id: string; heartbeat_at: string; paused: boolean }[];
}

const alertLabels: Record<string, string> = {
  meta_queue_delayed: 'Meta 待发超时',
  meta_credentials: 'Meta 凭据或绑定异常',
  meta_outcome_unknown: 'Meta 接收结果未知',
  worker_heartbeat_missing: 'Worker 心跳缺失',
  worker_stalled: 'Worker 停滞',
  purchase_missing_outbox: '已履约购买缺少入队',
  missing_original_target: '已归一化，缺少原 Pixel 投递',
  reader_normalization_delayed: 'Reader 归一化积压或隔离',
  inbox_delayed: '分销回调积压或隔离',
  partner_report_delayed: '分销回告异常',
};

function seconds(value?: number) {
  return value === undefined || value === null ? '—' : `${Math.round(value)} 秒`;
}

export default function ConversionSummaryPanel() {
  const [data, setData] = useState<Summary>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function refresh(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
    if (busy) return;
    const params = new URLSearchParams();
    if (event) {
      for (const [key, value] of new FormData(event.currentTarget)) {
        if (typeof value === 'string' && value.trim()) params.set(key, value.trim());
      }
    }
    setBusy(true);
    setError('');
    try {
      setData(await apiRequest('GET', `/conversion/summary?${params}`));
    } catch (err) {
      setError(err instanceof Error ? err.message : '汇总暂不可用');
    } finally {
      setBusy(false);
    }
  }

  return (
    <details
      className="space-y-3"
      onToggle={(event) => {
        if (event.currentTarget.open && !data) void refresh();
      }}
    >
      <summary className="cursor-pointer text-sm font-semibold">投递汇总与告警</summary>
      <form onSubmit={refresh} className="flex flex-wrap items-end gap-2">
        <label className="grid gap-1 text-xs">
          最近小时数
          <input
            type="number"
            name="hours"
            min={1}
            max={168}
            defaultValue={24}
            className="input-field w-24"
          />
        </label>
        <label className="grid gap-1 text-xs">
          来源
          <input name="source" maxLength={100} className="input-field w-32" />
        </label>
        <label className="grid gap-1 text-xs">
          接入 ID
          <input
            name="integration_id"
            inputMode="numeric"
            pattern="[1-9][0-9]{0,18}"
            className="input-field w-32"
          />
        </label>
        <label className="grid gap-1 text-xs">
          事件
          <input name="event_name" maxLength={100} className="input-field w-40" />
        </label>
        <label className="grid gap-1 text-xs">
          目标 ID
          <input
            name="destination_id"
            inputMode="numeric"
            pattern="[1-9][0-9]{0,18}"
            className="input-field w-32"
          />
        </label>
        <button type="submit" className="btn-secondary" disabled={busy}>
          {busy ? '查询中…' : '查询汇总'}
        </button>
      </form>
      {error && (
        <p role="alert" className="text-sm text-[var(--cms-error)]">
          {error}
        </p>
      )}
      {data && (
        <div className="space-y-3 text-xs">
          <h3 className="font-semibold">告警与 Worker 心跳</h3>
          {data.alerts.length === 0 && <p>当前没有活动告警。</p>}
          <ul className="list-disc pl-5">
            {data.alerts.map((alert) => (
              <li key={`${alert.kind}:${alert.subject}`}>
                {alertLabels[alert.kind] ?? alert.kind} · {alert.subject} · {alert.detail.count ?? '—'} ·{' '}
                {timestamp(alert.opened_at)}
                {alert.detail.oldest_at && ` · 最早 ${timestamp(alert.detail.oldest_at)}`}
                {alert.detail.sample_event_id && (
                  <Link
                    className="ml-2 underline"
                    to={`/ad-config/logs?${new URLSearchParams({ event_id: alert.detail.sample_event_id })}#conversion-events`}
                  >
                    查看事件 #{alert.detail.sample_event_id}
                  </Link>
                )}
              </li>
            ))}
          </ul>
          <ul className="list-disc pl-5">
            {data.heartbeats.map((item) => (
              <li key={`${item.worker_type}:${item.instance_id}`}>
                {item.worker_type} · {item.instance_id} · {timestamp(item.heartbeat_at)}
                {item.paused && ' · 维护暂停'}
              </li>
            ))}
          </ul>
          {data.population.length > 0 && (
            <TableRegion ariaLabel="原目标回传统计">
              <table className="cms-table min-w-[650px]">
                <thead>
                  <tr>
                    <th scope="col">来源 / 事件</th>
                    <th scope="col">资格</th>
                    <th scope="col">事件数</th>
                    <th scope="col">原目标确认率</th>
                    <th scope="col">缺入队 / 目标 / 时间</th>
                  </tr>
                </thead>
                <tbody>
                  {data.population.map((row) => (
                    <tr key={[row.source, row.integration_id, row.event_name, row.eligibility].join(':')}>
                      <th scope="row" className="text-left">
                        {row.source} / {row.integration_id ?? '—'} / {row.event_name}
                      </th>
                      <td>{status(row.eligibility)}</td>
                      <td>{row.events}</td>
                      <td>
                        {row.eligibility === 'expected'
                          ? `${row.confirmed} / ${row.events}（${Math.round((row.confirmed * 1000) / row.events) / 10}%）`
                          : '—'}
                      </td>
                      <td>
                        {row.missing_outbox} / {row.missing_target} / {row.missing_time}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableRegion>
          )}
          {data.targets.length > 0 && (
            <TableRegion ariaLabel="Pixel 投递统计">
              <table className="cms-table min-w-[800px]">
                <thead>
                  <tr>
                    <th scope="col">Pixel / 事件</th>
                    <th scope="col">角色 / 状态</th>
                    <th scope="col">任务数</th>
                    <th scope="col">最早可发送</th>
                    <th scope="col">平均等待</th>
                    <th scope="col">平均确认（发生 / 入队）</th>
                  </tr>
                </thead>
                <tbody>
                  {data.targets.map((row) => (
                    <tr
                      key={[
                        row.integration_id,
                        row.destination_id,
                        row.event_name,
                        row.role,
                        row.state,
                        row.reason,
                      ].join(':')}
                    >
                      <th scope="row" className="text-left">
                        {row.pixel_id} / {row.event_name}
                      </th>
                      <td>
                        {row.role === 'original' ? '原目标' : '附加'} / {status(row.state)} {row.reason}
                      </td>
                      <td>{row.deliveries}</td>
                      <td>{timestamp(row.oldest_ready_at)}</td>
                      <td>{seconds(row.queue_wait_seconds)}</td>
                      <td>
                        {seconds(row.business_confirmation_seconds)} /{' '}
                        {seconds(row.queue_confirmation_seconds)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableRegion>
          )}
          {data.reports.length > 0 && (
            <ul className="list-disc pl-5">
              {data.reports.map((row) => (
                <li
                  key={[row.integration_id, row.destination_id, row.event_name, row.state, row.reason].join(
                    ':',
                  )}
                >
                  分销回告 · 接入 {row.integration_id} · 目标 {row.destination_id} · {row.event_name} ·{' '}
                  {status(row.state)} {row.reason} · {row.reports} 条
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </details>
  );
}

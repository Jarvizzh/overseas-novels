import { type FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Activity, AlertTriangle, CheckCircle2, ExternalLink, PauseCircle, RefreshCw } from 'lucide-react';
import { apiRequest } from '../../utils/api';
import CustomSelect from '../CustomSelect';
import { TableRegion } from '../ui/Primitives';
import {
  alertKindLabel,
  formatDurationSeconds,
  getErrorMessage,
  getReasonLabel,
  isServiceUnavailableError,
  sourceLabel,
  timestamp,
  workerTypeMeta,
} from '../../utils/conversionStatus';
import {
  ConversionStatusDot,
  InlineErrorBanner,
  ServiceUnavailableState,
} from './ConversionShared';
import type {
  ConversionDestination,
  ConversionIntegration,
  ConversionSummaryData,
  SummaryAlert,
  WorkerHeartbeat,
} from './types';

const HOURS_OPTIONS = [
  { value: '1', label: '最近 1 小时' },
  { value: '6', label: '最近 6 小时' },
  { value: '24', label: '最近 24 小时' },
  { value: '72', label: '最近 3 天 (72h)' },
  { value: '168', label: '最近 7 天 (168h)' },
];

const SOURCE_OPTIONS = [
  { value: '', label: '全部来源' },
  { value: 'starnovel', label: 'StarNovel 自建' },
  { value: 'sinan', label: '番茄司南' },
];

const EVENT_NAME_OPTIONS = [
  { value: '', label: '全部事件类型' },
  { value: 'CompleteRegistration', label: 'CompleteRegistration' },
  { value: 'InitiateCheckout', label: 'InitiateCheckout' },
  { value: 'Purchase', label: 'Purchase' },
  { value: 'Subscribe', label: 'Subscribe' },
];

const WORKER_TYPES = ['normalize', 'normalize_inbox', 'meta', 'partner_report'] as const;
const WORKER_HEARTBEAT_ACTIVE_MS = 90_000;

export function ConversionSummaryTab({
  integrations,
  destinations,
  integrationMap,
  destinationById,
  serviceUnavailable,
  retryingService = false,
  onServiceUnavailable,
  onRetryService,
  onOpenEventDrawer,
  onAlertsCountChange,
}: {
  integrations: ConversionIntegration[];
  destinations: ConversionDestination[];
  integrationMap: Map<string, ConversionIntegration>;
  destinationById: Map<string, ConversionDestination>;
  serviceUnavailable: boolean;
  retryingService?: boolean;
  onServiceUnavailable: () => void;
  onRetryService: () => void;
  onOpenEventDrawer: (eventId: string) => void;
  onAlertsCountChange: (count: number) => void;
}) {
  const [filters, setFilters] = useState({
    hours: '24',
    source: '',
    integrationId: '',
    eventName: '',
    destinationId: '',
  });
  const [data, setData] = useState<ConversionSummaryData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [lastUpdated, setLastUpdated] = useState<string>('');
  const [refreshNonce, setRefreshNonce] = useState(0);

  const reqSeqRef = useRef(0);

  const fetchSummary = useCallback(
    async (signal?: AbortSignal) => {
      if (serviceUnavailable) {
        setLoading(false);
        return;
      }
      const seq = ++reqSeqRef.current;
      setLoading(true);
      setError('');

      const params = new URLSearchParams();
      params.set('hours', filters.hours || '24');
      if (filters.source) params.set('source', filters.source);
      if (filters.integrationId) params.set('integration_id', filters.integrationId);
      if (filters.eventName) params.set('event_name', filters.eventName);
      if (filters.destinationId) params.set('destination_id', filters.destinationId);

      try {
        const res: ConversionSummaryData = await apiRequest(
          'GET',
          `/conversion/summary?${params.toString()}`,
          undefined,
          false,
          signal,
        );
        if (seq !== reqSeqRef.current || signal?.aborted) return;
        setData(res);
        setLastUpdated(new Date().toLocaleTimeString());
        onAlertsCountChange(res.alerts?.length ?? 0);
      } catch (err) {
        if (seq !== reqSeqRef.current || signal?.aborted) return;
        if (isServiceUnavailableError(err)) {
          onServiceUnavailable();
        } else {
          setError(getErrorMessage(err, '加载转化汇总监控失败'));
        }
      } finally {
        if (seq === reqSeqRef.current && !signal?.aborted) {
          setLoading(false);
        }
      }
    },
    [filters, serviceUnavailable, onServiceUnavailable, onAlertsCountChange],
  );

  useEffect(() => {
    const controller = new AbortController();
    void fetchSummary(controller.signal);
    return () => controller.abort();
  }, [fetchSummary, refreshNonce]);

  const workerSummaries = useMemo(() => {
    const grouped = new Map<string, WorkerHeartbeat[]>();
    for (const item of data?.heartbeats ?? []) {
      const list = grouped.get(item.worker_type) ?? [];
      list.push(item);
      grouped.set(item.worker_type, list);
    }

    const nowMs = Date.now();
    return WORKER_TYPES.map((type) => {
      const instances = grouped.get(type) ?? [];
      const latest = instances
        .slice()
        .sort((a, b) => Date.parse(b.heartbeat_at) - Date.parse(a.heartbeat_at))[0];
      const activeInstances = instances.filter((i) => {
        const hbMs = Date.parse(i.heartbeat_at);
        return Number.isFinite(hbMs) && nowMs - hbMs <= WORKER_HEARTBEAT_ACTIVE_MS;
      });
      const anyPaused = activeInstances.length > 0 && activeInstances.every((i) => i.paused);
      const stale = activeInstances.length === 0;
      return {
        type,
        meta: workerTypeMeta(type),
        instanceCount: activeInstances.length,
        latest,
        anyPaused,
        stale,
      };
    });
  }, [data?.heartbeats]);

  const resolveAlertSubject = (alert: SummaryAlert): string => {
    if (
      alert.kind === 'meta_queue_delayed' ||
      alert.kind === 'meta_credentials' ||
      alert.kind === 'meta_outcome_unknown'
    ) {
      const dest = destinationById.get(alert.subject);
      const pixel = alert.detail.pixel_id || dest?.pixel_id;
      if (dest?.name && pixel) return `${pixel} (${dest.name})`;
      if (pixel) return `Pixel ${pixel}`;
      return `目标 #${alert.subject}`;
    }
    if (
      alert.kind === 'inbox_delayed' ||
      alert.kind === 'rule_fact_missing' ||
      alert.kind === 'missing_original_target' ||
      alert.kind === 'partner_report_delayed'
    ) {
      const integ = integrationMap.get(alert.subject);
      return integ ? `${sourceLabel(integ.source)} · ${integ.account_key} (#${integ.id})` : `接入 #${alert.subject}`;
    }
    if (alert.kind === 'worker_heartbeat_missing' || alert.kind === 'worker_stalled') {
      return workerTypeMeta(alert.subject).label;
    }
    return alert.subject;
  };

  if (serviceUnavailable) {
    return (
      <ServiceUnavailableState
        retrying={retryingService}
        onRetry={onRetryService}
      />
    );
  }

  const integrationOptions = [
    { value: '', label: '全部接入账户' },
    ...integrations.map((item) => ({
      value: item.id,
      label: `${sourceLabel(item.source)} · ${item.account_key} (#${item.id})`,
    })),
  ];

  const destinationOptions = [
    { value: '', label: '全部目标 Pixel' },
    ...destinations.map((item) => ({
      value: item.id,
      label: `${item.pixel_id}${item.name ? ` · ${item.name}` : ''}`,
    })),
  ];

  const handleFilterSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setRefreshNonce((n) => n + 1);
  };

  return (
    <div className="space-y-5 animate-fade-in">
      {/* Filter Strip */}
      <search className="block">
        <form onSubmit={handleFilterSubmit} className="flex flex-wrap items-center justify-between gap-2.5">
          <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
            <CustomSelect
              aria-label="选择统计时间窗口"
              options={HOURS_OPTIONS}
              value={filters.hours}
              onChange={(val) => setFilters((prev) => ({ ...prev, hours: val }))}
              width="148px"
            />
            <CustomSelect
              aria-label="筛选来源"
              options={SOURCE_OPTIONS}
              value={filters.source}
              onChange={(val) => setFilters((prev) => ({ ...prev, source: val }))}
              width="142px"
            />
            <CustomSelect
              aria-label="筛选接入账户"
              options={integrationOptions}
              value={filters.integrationId}
              onChange={(val) => setFilters((prev) => ({ ...prev, integrationId: val }))}
              width="188px"
            />
            <CustomSelect
              aria-label="筛选事件类型"
              options={EVENT_NAME_OPTIONS}
              value={filters.eventName}
              onChange={(val) => setFilters((prev) => ({ ...prev, eventName: val }))}
              width="174px"
            />
            <CustomSelect
              aria-label="筛选目标 Pixel"
              options={destinationOptions}
              value={filters.destinationId}
              onChange={(val) => setFilters((prev) => ({ ...prev, destinationId: val }))}
              width="184px"
            />
          </div>

          <div className="flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
            {lastUpdated && <span className="tabular-nums">更新于 {lastUpdated}</span>}
            <button type="submit" disabled={loading} className="btn-secondary h-9 px-3 text-xs">
              <RefreshCw size={13} className={loading ? 'animate-spin' : ''} aria-hidden="true" />
              <span>刷新统计</span>
            </button>
          </div>
        </form>
      </search>

      {error && <InlineErrorBanner message={error} onRetry={() => setRefreshNonce((n) => n + 1)} />}

      {loading && !data ? (
        <div className="space-y-4">
          <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-3">
            {[1, 2, 3, 4].map((i) => (
              <div key={i} className="cms-card h-24 skeleton-bar" />
            ))}
          </div>
          <div className="skeleton-bar h-36 rounded-2xl" />
          <div className="skeleton-bar h-48 rounded-2xl" />
        </div>
      ) : (
        data && (
          <div className={`space-y-5 ${loading ? 'opacity-70 transition-opacity' : ''}`}>
            {/* Worker Heartbeats */}
            <section className="flex flex-col gap-3.5">
              <div className="flex items-center justify-between gap-2">
                <h2 className="text-sm font-semibold text-[var(--cms-fg)] flex items-center gap-2 m-0">
                  <Activity size={15} className="text-[var(--cms-primary)]" aria-hidden="true" />
                  <span>Worker 节点心跳</span>
                </h2>
                {data.environment && (
                  <span className="text-xs text-[var(--cms-muted-fg)]">
                    运行环境：<strong className="text-[var(--cms-fg)]">{data.environment}</strong>
                  </span>
                )}
              </div>

              <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-3">
                {workerSummaries.map((w) => (
                  <div key={w.type} className="cms-card p-4 space-y-2 text-xs">
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-semibold text-[var(--cms-fg)]">{w.meta.label}</span>
                      {w.anyPaused ? (
                        <span className="badge badge-amber">
                          <PauseCircle size={11} aria-hidden="true" />
                          <span>维护暂停</span>
                        </span>
                      ) : !w.latest ? (
                        <span className="badge badge-gray">近 24h 无心跳</span>
                      ) : w.stale ? (
                        <span className="badge badge-red">心跳超时</span>
                      ) : (
                        <span className="badge badge-green">运行中 ({w.instanceCount})</span>
                      )}
                    </div>
                    <div className="text-[11px] text-[var(--cms-muted-fg)]">{w.meta.description}</div>
                    <div className="pt-1 border-t border-[var(--cms-border)] text-[11px] tabular-nums text-[var(--cms-muted-fg)] space-y-0.5">
                      <div>最近心跳：{w.latest ? timestamp(w.latest.heartbeat_at) : '—'}</div>
                      <div>最近推进：{w.latest?.last_progress_at ? timestamp(w.latest.last_progress_at) : '—'}</div>
                    </div>
                  </div>
                ))}
              </div>
            </section>

            {/* Active Alerts */}
            <section className="flex flex-col gap-3.5">
              <h2 className="text-sm font-semibold text-[var(--cms-fg)] flex items-center gap-2">
                <AlertTriangle
                  size={15}
                  className={data.alerts.length > 0 ? 'text-[var(--cms-warning)]' : 'text-[var(--cms-success)]'}
                  aria-hidden="true"
                />
                <span>活动告警 ({data.alerts.length})</span>
              </h2>

              {data.alerts.length === 0 ? (
                <div
                  className="p-4 rounded-2xl border text-xs flex items-center gap-2.5"
                  style={{
                    backgroundColor: 'color-mix(in srgb, var(--cms-success) 8%, transparent)',
                    borderColor: 'color-mix(in srgb, var(--cms-success) 24%, transparent)',
                    color: 'var(--cms-success)',
                  }}
                >
                  <CheckCircle2 size={16} aria-hidden="true" />
                  <span className="font-medium">当前环境无未恢复的活动告警，投递与回告链路健康。</span>
                </div>
              ) : (
                <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                  {data.alerts.map((alert) => (
                    <div
                      key={`${alert.kind}:${alert.subject}`}
                      className="cms-card p-4 space-y-2 text-xs border-l-4"
                      style={{ borderLeftColor: 'var(--cms-warning)' }}
                    >
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="font-semibold text-[var(--cms-fg)]">
                          {alertKindLabel(alert.kind)}
                        </span>
                        {alert.detail.count !== undefined && (
                          <span className="badge badge-amber tabular-nums">
                            影响 {alert.detail.count} 条
                          </span>
                        )}
                      </div>

                      <div className="text-[var(--cms-fg)]">
                        对象：<span className="font-medium">{resolveAlertSubject(alert)}</span>
                      </div>

                      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-[var(--cms-muted-fg)] tabular-nums">
                        <span>
                          触发 {timestamp(alert.opened_at)}
                          {alert.detail.oldest_at ? ` · 最早积压 ${timestamp(alert.detail.oldest_at)}` : ''}
                        </span>
                        {alert.detail.sample_event_id && (
                          <button
                            type="button"
                            onClick={() => onOpenEventDrawer(alert.detail.sample_event_id!)}
                            className="focus-ring inline-flex items-center gap-1 text-[var(--cms-primary)] hover:underline cursor-pointer font-medium"
                          >
                            <span>排查样例事件 #{alert.detail.sample_event_id}</span>
                            <ExternalLink size={11} aria-hidden="true" />
                          </button>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </section>

            <section className="flex flex-col gap-3.5">
              <h2 className="text-sm font-semibold text-[var(--cms-fg)]">付款回传规则统计</h2>
              <TableRegion ariaLabel="付款覆盖率与选中后原目标确认率">
                <table className="cms-table min-w-[540px]" data-density="comfortable">
                  <thead><tr>
                    <th scope="col">指标 / 对象</th><th scope="col" className="text-right">已选中或已确认</th>
                    <th scope="col" className="text-right">已判定或应投递</th><th scope="col" className="text-right">比率</th>
                  </tr></thead>
                  <tbody>
                    <PolicyMetric label="订单回传覆盖率 · 按付款" counted={data.payment_coverage?.selected ?? 0}
                      total={data.payment_coverage?.evaluated ?? 0} />
                    {(data.selected_confirmation?.length ? data.selected_confirmation : [{ event_name: '', confirmed: 0, expected: 0 }])
                      .map((row) => (
                        <PolicyMetric key={row.event_name} label={`选中后原目标确认率 · ${row.event_name || '按事件'}`}
                          counted={row.confirmed} total={row.expected} />
                      ))}
                  </tbody>
                </table>
              </TableRegion>
              <p className="text-xs m-0 text-[var(--cms-muted-fg)]">以付款判定时间为统计窗口。按 Pixel 筛选时匹配付款的原目标，追加目标见投递分布。</p>
            </section>

            {/* Population Confirmation Rate */}
            <section className="flex flex-col gap-3.5">
              <h2 className="text-sm font-semibold text-[var(--cms-fg)]">原目标回传确认率统计</h2>
              <TableRegion ariaLabel="原目标回传确认率统计">
                <table className="cms-table min-w-[760px]" data-density="comfortable">
                  <thead>
                    <tr>
                      <th scope="col">来源 / 接入账户</th>
                      <th scope="col">标准事件</th>
                      <th scope="col">资格分组</th>
                      <th scope="col" className="text-right">
                        事件总数
                      </th>
                      <th scope="col" className="w-56">
                        原目标确认率
                      </th>
                      <th scope="col" className="text-right">
                        缺入队 / 缺目标 / 缺时间
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.population.length === 0 ? (
                      <tr>
                        <td colSpan={6} className="py-10 text-center text-[var(--cms-muted-fg)]">
                          所选时间窗内暂无回传口径统计数据
                        </td>
                      </tr>
                    ) : (
                      data.population.map((row) => {
                        const integ = row.integration_id ? integrationMap.get(row.integration_id) : undefined;
                        const pct =
                          row.eligibility === 'expected' && row.events > 0
                            ? Math.round((row.confirmed * 1000) / row.events) / 10
                            : null;
                        return (
                          <tr
                            key={[
                              row.source,
                              row.integration_id ?? 'null',
                              row.event_name,
                              row.eligibility,
                            ].join(':')}
                          >
                            <th scope="row">
                              <span className="font-medium text-[var(--cms-fg)]">
                                {sourceLabel(row.source)}
                              </span>
                              <span className="text-[var(--cms-muted-fg)]">
                                {' '}
                                ·{' '}
                                {integ
                                  ? `${integ.account_key} (#${integ.id})`
                                  : row.integration_id
                                    ? `#${row.integration_id}`
                                    : '未绑定接入'}
                              </span>
                            </th>
                            <td className="font-medium text-[var(--cms-fg)]">{row.event_name}</td>
                            <td>
                              <ConversionStatusDot code={row.eligibility} />
                            </td>
                            <td className="text-right tabular-nums font-medium">{row.events}</td>
                            <td>
                              {pct !== null ? (
                                <div className="space-y-1">
                                  <div className="flex items-center justify-between text-[11px] tabular-nums">
                                    <span className="text-[var(--cms-fg)] font-medium">
                                      {row.confirmed} / {row.events}
                                    </span>
                                    <span className="text-[var(--cms-muted-fg)]">{pct}%</span>
                                  </div>
                                  <div className="h-1.5 rounded-full bg-[var(--cms-content)] overflow-hidden">
                                    <div
                                      className="h-full rounded-full"
                                      style={{
                                        width: `${Math.min(100, pct)}%`,
                                        backgroundColor:
                                          pct >= 95
                                            ? 'var(--cms-success)'
                                            : pct >= 80
                                              ? 'var(--cms-primary)'
                                              : 'var(--cms-warning)',
                                      }}
                                    />
                                  </div>
                                </div>
                              ) : (
                                <span className="text-[var(--cms-muted-fg)]">—</span>
                              )}
                            </td>
                            <td className="text-right tabular-nums">
                              <span
                                className={
                                  row.missing_outbox > 0
                                    ? 'text-[var(--cms-error)] font-semibold'
                                    : 'text-[var(--cms-muted-fg)]'
                                }
                              >
                                {row.missing_outbox}
                              </span>
                              <span className="text-[var(--cms-muted-fg)]"> / </span>
                              <span
                                className={
                                  row.missing_target > 0
                                    ? 'text-[var(--cms-warning)] font-semibold'
                                    : 'text-[var(--cms-muted-fg)]'
                                }
                              >
                                {row.missing_target}
                              </span>
                              <span className="text-[var(--cms-muted-fg)]"> / </span>
                              <span
                                className={
                                  row.missing_time > 0
                                    ? 'text-[var(--cms-warning)] font-semibold'
                                    : 'text-[var(--cms-muted-fg)]'
                                }
                              >
                                {row.missing_time}
                              </span>
                            </td>
                          </tr>
                        );
                      })
                    )}
                  </tbody>
                </table>
              </TableRegion>
              <p className="text-xs m-0 text-[var(--cms-muted-fg)]">
                按事件业务时间统计。缺业务时间的 Reader
                订单按履约时间归入窗口。确认率仅统计应投递的事件。
              </p>
            </section>

            {/* Target Delivery Metrics */}
            <section className="flex flex-col gap-3.5">
              <h2 className="text-sm font-semibold text-[var(--cms-fg)]">Pixel 投递与延迟分布</h2>
              <TableRegion ariaLabel="Pixel 投递与延迟分布">
                <table className="cms-table min-w-[900px]" data-density="compact">
                  <thead>
                    <tr>
                      <th scope="col">目标 Pixel</th>
                      <th scope="col">事件</th>
                      <th scope="col">角色</th>
                      <th scope="col">投递状态 / 原因</th>
                      <th scope="col" className="text-right">
                        任务数
                      </th>
                      <th scope="col">最早可发时间</th>
                      <th scope="col" className="text-right">
                        平均队列等待
                      </th>
                      <th scope="col" className="text-right">
                        平均确认延迟（发生 / 入队）
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.targets.length === 0 ? (
                      <tr>
                        <td colSpan={8} className="py-10 text-center text-[var(--cms-muted-fg)]">
                          暂无 Pixel 投递统计数据
                        </td>
                      </tr>
                    ) : (
                      data.targets.map((row) => {
                        const dest = destinationById.get(row.destination_id);
                        return (
                          <tr
                            key={[
                              row.integration_id,
                              row.destination_id,
                              row.event_name,
                              row.role,
                              row.state,
                              row.reason ?? '',
                            ].join(':')}
                          >
                            <td>
                              <div className="font-mono-code font-medium text-[var(--cms-fg)]">
                                {row.pixel_id}
                              </div>
                              {dest?.name && (
                                <div className="text-[11px] text-[var(--cms-muted-fg)]">{dest.name}</div>
                              )}
                            </td>
                            <td className="text-[var(--cms-fg)]">{row.event_name}</td>
                            <td>
                              <span
                                className={`badge ${
                                  row.role === 'original' ? 'badge-blue' : 'badge-gray'
                                }`}
                              >
                                {row.role === 'original' ? '原目标' : '附加'}
                              </span>
                            </td>
                            <td>
                              <ConversionStatusDot code={row.state} />
                              {row.reason && (
                                <div className="text-[11px] text-[var(--cms-muted-fg)] mt-0.5">
                                  {getReasonLabel(row.reason)}
                                </div>
                              )}
                            </td>
                            <td className="text-right tabular-nums font-medium">{row.deliveries}</td>
                            <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                              {timestamp(row.oldest_ready_at)}
                            </td>
                            <td className="text-right tabular-nums text-[var(--cms-fg)] whitespace-nowrap">
                              {formatDurationSeconds(row.queue_wait_seconds)}
                            </td>
                            <td className="text-right tabular-nums text-[var(--cms-fg)] whitespace-nowrap">
                              {formatDurationSeconds(row.business_confirmation_seconds)} /{' '}
                              {formatDurationSeconds(row.queue_confirmation_seconds)}
                            </td>
                          </tr>
                        );
                      })
                    )}
                  </tbody>
                </table>
              </TableRegion>
            </section>

            {/* Partner Reports Metrics */}
            {data.reports.length > 0 && (
              <section className="flex flex-col gap-3.5">
                <h2 className="text-sm font-semibold text-[var(--cms-fg)]">分销结果回告汇总</h2>
                <TableRegion ariaLabel="分销结果回告汇总">
                  <table className="cms-table min-w-[760px]" data-density="compact">
                    <thead>
                      <tr>
                        <th scope="col">接入账户</th>
                        <th scope="col">原目标 Pixel</th>
                        <th scope="col">事件</th>
                        <th scope="col">回告状态 / 原因</th>
                        <th scope="col">最早待回告</th>
                        <th scope="col" className="text-right">
                          任务数
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.reports.map((row) => {
                        const integ = integrationMap.get(row.integration_id);
                        const dest = destinationById.get(row.destination_id);
                        return (
                          <tr
                            key={[
                              row.integration_id,
                              row.destination_id,
                              row.event_name,
                              row.state,
                              row.reason ?? '',
                            ].join(':')}
                          >
                            <td>
                              {integ
                                ? `${sourceLabel(integ.source)} · ${integ.account_key} (#${integ.id})`
                                : `接入 #${row.integration_id}`}
                            </td>
                            <td className="font-mono-code">
                              {dest ? `${dest.pixel_id}${dest.name ? ` (${dest.name})` : ''}` : `目标 #${row.destination_id}`}
                            </td>
                            <td>{row.event_name}</td>
                            <td>
                              <ConversionStatusDot code={row.state} />
                              {row.reason && (
                                <div className="text-[11px] text-[var(--cms-muted-fg)] mt-0.5">
                                  {getReasonLabel(row.reason)}
                                </div>
                              )}
                            </td>
                            <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                              {timestamp(row.oldest_pending_at)}
                            </td>
                            <td className="text-right tabular-nums font-medium">{row.reports}</td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </TableRegion>
              </section>
            )}
          </div>
        )
      )}
    </div>
  );
}

function PolicyMetric({ label, counted, total }: { label: string; counted: number; total: number }) {
  return <tr>
    <th scope="row" className="text-left font-medium">{label}</th>
    <td className="text-right tabular-nums">{counted}</td>
    <td className="text-right tabular-nums">{total}</td>
    <td className="text-right tabular-nums font-medium">{total ? `${((counted / total) * 100).toFixed(1)}%` : '—'}</td>
  </tr>;
}

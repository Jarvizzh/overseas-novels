import { type FormEvent, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { ArrowRight, Search } from 'lucide-react';
import { ApiError, apiRequest } from '../../utils/api';
import CustomSelect from '../CustomSelect';
import { SearchInput, TableRegion } from '../ui/Primitives';
import {
  getErrorMessage,
  getReasonLabel,
  partnerReportStatusLabel,
  sourceEventLabel,
  sourceLabel,
  status,
  timestamp,
} from '../../utils/conversionStatus';
import { ConversionStatusDot, CopyableText, InlineErrorBanner } from './ConversionShared';
import { PaymentDecisionSummary } from './PaymentDecisionSummary';
import { TargetResultsTable } from './TargetResultsTable';
import type {
  ConversionDestination,
  ConversionIntegration,
  OrderReconciliationReport,
} from './types';

export function ConversionOrdersTab({
  integrations,
  destinationByPixel,
  initialQuery,
  onOpenEventDrawer,
}: {
  integrations: ConversionIntegration[];
  destinationByPixel: Map<string, ConversionDestination>;
  initialQuery: { orderId: string; integrationId: string; nonce: number } | null;
  onOpenEventDrawer: (eventId: string) => void;
}) {
  const headingId = useId();
  const [orderInput, setOrderInput] = useState(initialQuery?.orderId ?? '');
  const [integrationId, setIntegrationId] = useState(initialQuery?.integrationId ?? '');
  const [appId, setAppId] = useState('');
  const [appChoices, setAppChoices] = useState<string[]>([]);
  const [report, setReport] = useState<OrderReconciliationReport | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [lastUpdated, setLastUpdated] = useState('');
  const lastQueryRef = useRef<{ order: string; integration: string; app: string } | null>(null);

  const abortRef = useRef<AbortController | null>(null);
  const lastAutoNonceRef = useRef(-1);

  const runOrderLookup = useCallback(
    (
      targetOrderId: string,
      targetIntegrationId: string,
      targetAppId = '',
    ): AbortController | null => {
      const trimmedOrder = targetOrderId.trim();
      if (!trimmedOrder) {
        setError('请输入本地订单 ID 或分销外部订单号');
        return null;
      }

      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;

      setLoading(true);
      setError('');
      const lastQuery = lastQueryRef.current;
      if (
        lastQuery?.order !== trimmedOrder ||
        lastQuery.integration !== targetIntegrationId ||
        lastQuery.app !== targetAppId
      ) {
        setReport(null);
        setLastUpdated('');
      }
      lastQueryRef.current = {
        order: trimmedOrder,
        integration: targetIntegrationId,
        app: targetAppId,
      };

      const params = new URLSearchParams();
      if (targetIntegrationId) params.set('integration_id', targetIntegrationId.trim());
      if (targetIntegrationId && targetAppId) params.set('app_id', targetAppId);
      const query = params.size ? `?${params}` : '';

      void (async () => {
        try {
          const res: OrderReconciliationReport = await apiRequest(
            'GET',
            `/conversion/orders/${encodeURIComponent(trimmedOrder)}${query}`,
            undefined,
            false,
            controller.signal,
          );
          if (!controller.signal.aborted) {
            setReport(res);
            setLastUpdated(new Date().toLocaleTimeString());
          }
        } catch (err) {
          if (!controller.signal.aborted) {
            if (err instanceof ApiError && err.code === 'order_app_ambiguous')
              setAppChoices(err.appIds ?? []);
            setError(getErrorMessage(err, '逐单对账查询失败'));
          }
        } finally {
          if (abortRef.current === controller) {
            abortRef.current = null;
          }
          if (!controller.signal.aborted) {
            setLoading(false);
          }
        }
      })();

      return controller;
    },
    [],
  );

  useEffect(() => () => abortRef.current?.abort(), []);

  useEffect(() => {
    if (
      !initialQuery ||
      !initialQuery.orderId.trim() ||
      initialQuery.nonce === lastAutoNonceRef.current
    ) {
      return;
    }
    const prevNonce = lastAutoNonceRef.current;
    lastAutoNonceRef.current = initialQuery.nonce;
    setOrderInput(initialQuery.orderId);
    setIntegrationId(initialQuery.integrationId);
    setAppId('');
    setAppChoices([]);
    const controller = runOrderLookup(initialQuery.orderId, initialQuery.integrationId);
    return () => {
      // If React StrictMode unmounts/remounts before this auto-lookup settles, abort and
      // restore lastAutoNonceRef so the remount performs the lookup cleanly.
      if (controller && abortRef.current === controller) {
        controller.abort();
        if (lastAutoNonceRef.current === initialQuery.nonce) {
          lastAutoNonceRef.current = prevNonce;
        }
      }
    };
  }, [initialQuery, runOrderLookup]);

  const partnerAccountOptions = useMemo(() => {
    const partnerIntegrations = integrations.filter((item) => item.source !== 'starnovel');
    const options = [{ value: '', label: 'StarNovel 本地订单（默认）' }];
    for (const item of partnerIntegrations) {
      options.push({
        value: item.id,
        label: `${sourceLabel(item.source)} · ${item.account_key} (#${item.id})`,
      });
    }
    if (
      integrationId &&
      !options.some((opt) => opt.value === integrationId)
    ) {
      options.push({ value: integrationId, label: `接入账户 #${integrationId}` });
    }
    return options;
  }, [integrations, integrationId]);

  const handleSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    runOrderLookup(orderInput, integrationId, appId);
  };

  return (
    <div className="space-y-4 animate-fade-in">
      <search aria-labelledby={headingId} className="block space-y-2">
        <h2 id={headingId} className="sr-only">
          逐单转化对账查询
        </h2>
        <form onSubmit={handleSubmit} className="flex flex-wrap items-center gap-2.5">
          <CustomSelect
            aria-label="选择订单来源账户"
            options={partnerAccountOptions}
            value={integrationId}
            onChange={(value) => { setIntegrationId(value); setAppId(''); setAppChoices([]); }}
            width="240px"
          />

          {integrationId && (
            <CustomSelect aria-label="应用" width="200px" value={appId} onChange={setAppId}
              options={[{ value: '', label: '自动匹配应用' }, ...Array.from(new Set([
                ...(integrations.find((item) => item.id === integrationId)?.allowed_apps ?? []), ...appChoices,
              ])).map((app) => ({ value: app, label: `应用 ${app}` }))]} />
          )}
          <SearchInput
            className="w-full sm:w-80"
            ariaLabel="订单号"
            placeholder={
              integrationId
                ? '输入分销外部订单号 (source_order_id)...'
                : '输入本地充值订单 ID...'
            }
            value={orderInput}
            onChange={setOrderInput}
            onClear={() => {
              abortRef.current?.abort();
              lastQueryRef.current = null;
              setReport(null);
              setLastUpdated('');
              setLoading(false);
              setError('');
            }}
          />

          <button type="submit" disabled={loading} className="btn-primary h-9 px-4 text-xs">
            <Search size={13} aria-hidden="true" />
            <span>{loading ? '查询中…' : '查询对账'}</span>
          </button>
        </form>
        <p className="text-[11px] text-[var(--cms-muted-fg)] m-0">
          本地充值订单保持「StarNovel 本地订单」直接输入数字订单 ID；司南等分销订单请先在左侧选择对应的分销接入账户。
        </p>
      </search>

      {error && (
        <InlineErrorBanner
          message={error}
          onRetry={
            lastQueryRef.current
              ? () => {
                  const query = lastQueryRef.current;
                  if (query) runOrderLookup(query.order, query.integration, query.app);
                }
              : undefined
          }
        />
      )}

      {loading && (
        <div className="space-y-3">
          <div className="skeleton-bar h-28 rounded-2xl" />
          <div className="skeleton-bar h-44 rounded-2xl" />
        </div>
      )}

      {!loading && !report && !error && (
        <div className="cms-card p-10 text-center space-y-1.5">
          <div className="text-sm font-medium text-[var(--cms-fg)]">输入订单号开始全链路对账</div>
          <p className="text-xs text-[var(--cms-muted-fg)] m-0">
            可同时核查订单回传资格（capi_expectation）、Outbox/回调 Inbox 状态、统一事件、各 Pixel 投递回执及分销结果回告。
          </p>
        </div>
      )}

      {report && (
        <div className="space-y-4">
          {/* Order Overview Card */}
          <div className="cms-card p-5 space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-2 pb-3 border-b border-[var(--cms-border)]">
              <div className="flex flex-wrap items-center gap-2 text-sm font-semibold text-[var(--cms-fg)]">
                <span>订单对账单</span>
                <CopyableText value={report.order_id} label="订单号" className="text-xs font-medium" />
              </div>
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <span className="badge badge-blue">{sourceLabel(report.source)}</span>
                <span className="badge badge-secondary">
                  环境：
                  {report.environment === 'unknown'
                    ? '待核实'
                    : report.environment === 'conflict'
                      ? '证据冲突'
                      : report.environment}
                </span>
                {lastUpdated && (
                  <span className="text-[var(--cms-muted-fg)]">更新于 {lastUpdated}</span>
                )}
                {report.integration_id && (
                  <span className="badge badge-gray">接入 #{report.integration_id}</span>
                )}
              </div>
            </div>

            <dl className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 text-xs m-0">
              {report.order_type && (
                <div className="space-y-1">
                  <dt className="text-[var(--cms-muted-fg)]">订单类型 / 业务状态</dt>
                  <dd className="m-0 font-medium text-[var(--cms-fg)]">
                    {report.order_type} / {report.order_status ?? '—'}
                  </dd>
                </div>
              )}

              {report.eligibility && (
                <>
                  <div className="space-y-1">
                    <dt className="text-[var(--cms-muted-fg)]">回传资格判定</dt>
                    <dd className="m-0 space-y-0.5">
                      <ConversionStatusDot code={report.eligibility.state} />
                      {report.eligibility.reason && (
                        <div className="text-[11px] text-[var(--cms-muted-fg)]">
                          {getReasonLabel(report.eligibility.reason)}
                        </div>
                      )}
                    </dd>
                  </div>

                  <div className="space-y-1">
                    <dt className="text-[var(--cms-muted-fg)]">归属原 Pixel</dt>
                    <dd className="m-0 font-mono-code text-[var(--cms-fg)]">
                      {report.eligibility.pixel_id || '—'}
                    </dd>
                  </div>

                  <div className="space-y-1">
                    <dt className="text-[var(--cms-muted-fg)]">业务时间 / 履约时间</dt>
                    <dd className="m-0 tabular-nums text-[var(--cms-fg)] space-y-0.5">
                      <div>
                        发生：{timestamp(report.eligibility.occurred_at)}
                        {report.eligibility.time_source ? ` (${report.eligibility.time_source})` : ''}
                      </div>
                      <div className="text-[var(--cms-muted-fg)]">
                        履约：{timestamp(report.eligibility.fulfilled_at)}
                      </div>
                    </dd>
                  </div>
                </>
              )}
            </dl>
          </div>

          {report.payment_policy && <PaymentDecisionSummary decision={report.payment_policy} />}

          {/* Partner Inbox Section */}
          {report.inbox && report.inbox.length > 0 && (
            <div className="flex flex-col gap-3">
              <h3 className="text-sm font-semibold text-[var(--cms-fg)]">
                分销入站回调 Inbox ({report.inbox.length})
              </h3>
              <TableRegion ariaLabel="分销入站回调记录">
                <table className="cms-table min-w-[760px]" data-density="compact">
                  <thead>
                    <tr>
                      <th scope="col">回调 ID</th>
                      <th scope="col">来源事件类型</th>
                      <th scope="col">原始事件 ID</th>
                      <th scope="col">应用 / 关联事件</th>
                      <th scope="col">解析状态</th>
                      <th scope="col">原因</th>
                      <th scope="col">接收时间</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.inbox.map((item) => (
                      <tr key={item.id}>
                        <td className="tabular-nums text-[var(--cms-muted-fg)]">#{item.id}</td>
                        <td className="font-medium text-[var(--cms-fg)]">
                          {sourceEventLabel(report.source, item.source_event_type)}
                        </td>
                        <td>
                          {item.source_event_id ? (
                            <CopyableText value={item.source_event_id} label="原始事件 ID" />
                          ) : (
                            <span className="text-[var(--cms-error)]">缺少事件 ID</span>
                          )}
                        </td>
                        <td className="tabular-nums">
                          {item.app_id ?? '—'}{item.normalized_event_id && (
                            <button type="button" className="btn-ghost ml-1" onClick={() => onOpenEventDrawer(item.normalized_event_id!)}>
                              事件 #{item.normalized_event_id}
                            </button>
                          )}
                        </td>
                        <td>
                          <ConversionStatusDot code={item.state} />
                        </td>
                        <td className="text-[var(--cms-muted-fg)]">
                          {item.reason ? getReasonLabel(item.reason) : '—'}
                        </td>
                        <td className="tabular-nums whitespace-nowrap text-[var(--cms-muted-fg)]">
                          {timestamp(item.received_at)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableRegion>
            </div>
          )}

          {/* Reconciled Events */}
          <div className="flex flex-col gap-3">
            <h3 className="text-sm font-semibold text-[var(--cms-fg)]">
              关联转化事件 ({report.events.length})
            </h3>

            {report.events.length === 0 ? (
              <div className="cms-card p-6 text-center text-xs text-[var(--cms-muted-fg)]">
                该订单暂无转化事件记录。
              </div>
            ) : (
              report.events.map((ev) => {
                const reasonText = getReasonLabel(ev.reason);
                return (
                  <div key={ev.id ?? ev.business_key} className="cms-card p-5 space-y-3.5 text-xs">
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="space-y-1 min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="text-sm font-semibold text-[var(--cms-fg)]">
                            {ev.source_event_type
                              ? `${sourceEventLabel(report.source, ev.source_event_type)} · `
                              : ''}
                            {ev.event_name}
                          </span>
                          <ConversionStatusDot code={ev.state} />
                          {ev.attribution_state && (
                            <span className="badge badge-secondary">
                              归因：{status(ev.attribution_state)}
                            </span>
                          )}
                        </div>
                        <div className="flex flex-wrap items-center gap-2 text-[var(--cms-muted-fg)]">
                          <CopyableText value={ev.business_key} label="业务键" />
                          {ev.id && <span>· 内部事件 #{ev.id}</span>}
                          <span>· 发生 {timestamp(ev.occurred_at)}</span>
                          <span>· 入队 {timestamp(ev.enqueued_at)}</span>
                        </div>
                        {reasonText && (
                          <div className="text-[var(--cms-muted-fg)]">原因：{reasonText}</div>
                        )}
                      </div>

                      {ev.id && (
                        <button
                          type="button"
                          onClick={() => onOpenEventDrawer(ev.id!)}
                          className="btn-secondary h-8 px-3 text-xs shrink-0"
                        >
                          <span>打开事件详情</span>
                          <ArrowRight size={13} aria-hidden="true" />
                        </button>
                      )}
                    </div>

                    {ev.legacy && (
                      <div className="p-2.5 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] flex flex-wrap items-center gap-2 text-[11px] text-[var(--cms-muted-fg)]">
                        <span className="font-medium text-[var(--cms-fg)]">历史队列迁移记录：</span>
                        <ConversionStatusDot code={ev.legacy.state} />
                        {ev.legacy.confirmed_at && <span>· 确认 {timestamp(ev.legacy.confirmed_at)}</span>}
                        {ev.legacy.environment_verified === false && (
                          <span className="badge badge-amber">环境未核实</span>
                        )}
                      </div>
                    )}

                    {ev.deliveries.length > 0 && (
                      <TargetResultsTable
                        deliveries={ev.deliveries}
                        destinationByPixel={destinationByPixel}
                      />
                    )}
                    {ev.deliveries_truncated && (
                      <button
                        type="button"
                        className="btn-secondary text-xs"
                        onClick={() => ev.id && onOpenEventDrawer(ev.id)}
                      >
                        查看全部投递目标
                      </button>
                    )}

                    {ev.partner_report && (
                      <div className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] flex flex-wrap items-center gap-2">
                        <span className="font-medium text-[var(--cms-fg)]">分销最终结果回告：</span>
                        <span
                          className={`badge ${
                            ev.partner_report.status === 1
                              ? 'badge-green'
                              : ev.partner_report.status === -1
                                ? 'badge-red'
                                : 'badge-gray'
                          }`}
                        >
                          {partnerReportStatusLabel(ev.partner_report.status)}
                        </span>
                        <ConversionStatusDot code={ev.partner_report.state} />
                        {ev.partner_report.reason && (
                          <span className="text-[var(--cms-muted-fg)]">
                            · {getReasonLabel(ev.partner_report.reason)}
                          </span>
                        )}
                      </div>
                    )}
                  </div>
                );
              })
            )}
          </div>

          {(report.events_truncated || report.inbox_truncated) && (
            <p className="text-xs text-[var(--cms-muted-fg)] m-0">
              事件与回调记录各最多显示最近 100 条。
            </p>
          )}
        </div>
      )}
    </div>
  );
}

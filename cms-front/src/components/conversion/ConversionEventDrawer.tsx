import { type FormEvent, useCallback, useEffect, useId, useRef, useState } from 'react';
import { AlertTriangle, ExternalLink, Plus, RefreshCw, RotateCcw } from 'lucide-react';
import { apiRequest } from '../../utils/api';
import CustomSelect from '../CustomSelect';
import { CmsDialog } from '../ui/CmsDialog';
import { TableRegion } from '../ui/Primitives';
import {
  formatAmount,
  getErrorMessage,
  getReasonLabel,
  partnerReportStatusLabel,
  sourceEventLabel,
  sourceLabel,
  timestamp,
} from '../../utils/conversionStatus';
import { ConversionStatusDot, CopyableText, InlineErrorBanner } from './ConversionShared';
import { PaymentDecisionSummary } from './PaymentDecisionSummary';
import { TargetResultsTable } from './TargetResultsTable';
import type {
  AllowedDestinationTarget,
  ConversionDelivery,
  ConversionDeliveryAttempt,
  ConversionDestination,
  ConversionEvent,
  ConversionEventDetail,
  ConversionIntegration,
  PartnerReportDetail,
} from './types';

type AuditAction =
  | { kind: 'append'; eventId: string }
  | { kind: 'retry'; delivery: ConversionDelivery }
  | { kind: 'report'; report: PartnerReportDetail };

function PartnerReportCard({
  report,
  canWrite,
  onRetryReport,
}: {
  report: PartnerReportDetail;
  canWrite: boolean;
  onRetryReport: (report: PartnerReportDetail) => void;
}) {
  const attempts = report.attempts ?? [];
  const reasonLabel = getReasonLabel(report.reason);
  const contractBlocked = ['no_trace_contract_unconfirmed', 'missing_promotion_contract_unconfirmed'].includes(report.reason ?? '');

  return (
    <div className="cms-card p-4 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="font-semibold text-[var(--cms-fg)]">分销结果回告 #{report.id}</span>
          <span
            className={`badge ${
              report.status === 1
                ? 'badge-green'
                : report.status === -1
                  ? 'badge-red'
                  : 'badge-gray'
            }`}
          >
            {partnerReportStatusLabel(report.status)}
          </span>
          <ConversionStatusDot code={report.state} />
          {report.next_attempt_at && (
            <span className="text-[var(--cms-muted-fg)] tabular-nums">
              下次调度：{timestamp(report.next_attempt_at)}
            </span>
          )}
        </div>

        {canWrite && report.state !== 'confirmed' && (
          <button
            type="button"
            className="btn-secondary h-8 px-3 text-xs"
            onClick={() => onRetryReport(report)}
          >
            <RotateCcw size={13} aria-hidden="true" />
            <span>重排回告</span>
          </button>
        )}
      </div>

      {reasonLabel && (
        <div
          className={`p-2.5 rounded-lg text-xs flex items-center gap-2 ${
            contractBlocked
              ? 'border border-[color-mix(in_srgb,var(--cms-warning)_30%,transparent)] bg-[color-mix(in_srgb,var(--cms-warning)_10%,transparent)] text-[var(--cms-warning)]'
              : 'bg-[var(--cms-content)] text-[var(--cms-muted-fg)]'
          }`}
        >
          {contractBlocked && <AlertTriangle size={14} aria-hidden="true" />}
          <span>
            {reasonLabel}
            {report.reason && reasonLabel !== report.reason ? ` (${report.reason})` : ''}
          </span>
        </div>
      )}

      {attempts.length > 0 && (
        <div className="space-y-1.5">
          <TableRegion ariaLabel="分销回告尝试明细">
            <table className="cms-table min-w-[860px]" data-density="compact">
              <caption className="sr-only">分销回告尝试明细</caption>
              <thead>
                <tr>
                  <th scope="col">次数</th>
                  <th scope="col">结果与原因</th>
                  <th scope="col">HTTP</th>
                  <th scope="col">业务码</th>
                  <th scope="col">领取时间</th>
                  <th scope="col">发送时间</th>
                  <th scope="col">响应时间</th>
                  <th scope="col">记录时间</th>
                  <th scope="col">计划重试时间</th>
                </tr>
              </thead>
              <tbody>
                {attempts.map((attempt) => {
                  const attemptReason = getReasonLabel(attempt.reason);
                  return (
                    <tr key={attempt.number}>
                      <th scope="row" className="tabular-nums font-medium">
                        #{attempt.number}
                      </th>
                      <td>
                        {attempt.outcome ? (
                          <ConversionStatusDot code={attempt.outcome} />
                        ) : (
                          <span className="text-[var(--cms-primary)]">进行中</span>
                        )}
                        {attemptReason && (
                          <div className="text-[11px] text-[var(--cms-muted-fg)] mt-0.5">
                            {attemptReason}
                          </div>
                        )}
                      </td>
                      <td className="tabular-nums">{attempt.http_status ?? '—'}</td>
                      <td className="font-mono-code">{attempt.business_code ?? '—'}</td>
                      <td className="tabular-nums whitespace-nowrap">{timestamp(attempt.started_at)}</td>
                      <td className="tabular-nums whitespace-nowrap">{timestamp(attempt.sending_at)}</td>
                      <td className="tabular-nums whitespace-nowrap">{timestamp(attempt.responded_at)}</td>
                      <td className="tabular-nums whitespace-nowrap">{timestamp(attempt.recorded_at)}</td>
                      <td className="tabular-nums whitespace-nowrap">{timestamp(attempt.retry_at)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </TableRegion>
          {report.attempt_count > attempts.length && (
            <p className="text-[11px] text-[var(--cms-muted-fg)] m-0">
              共 {report.attempt_count} 次尝试，当前已显示最近 {attempts.length} 次。
            </p>
          )}
        </div>
      )}
    </div>
  );
}

function AuditActionModal({
  action,
  onClose,
  onSuccess,
}: {
  action: AuditAction | null;
  onClose: () => void;
  onSuccess: (message: string, feedback?: 'status') => Promise<void> | void;
}) {
  const formId = useId();
  const reasonErrorId = useId();
  const reasonRef = useRef<HTMLTextAreaElement>(null);
  const inFlightRef = useRef(false);

  const [targets, setTargets] = useState<AllowedDestinationTarget[]>();
  const [targetsError, setTargetsError] = useState('');
  const [selectedTarget, setSelectedTarget] = useState('');
  const [reason, setReason] = useState('');
  const [reasonError, setReasonError] = useState('');
  const [acknowledged, setAcknowledged] = useState(false);
  const [busy, setBusy] = useState(false);
  const [submitError, setSubmitError] = useState('');

  const appendEventId = action?.kind === 'append' ? action.eventId : '';

  useEffect(() => {
    setTargets(undefined);
    setTargetsError('');
    setSelectedTarget('');
    setReason('');
    setReasonError('');
    setAcknowledged(false);
    setSubmitError('');

    if (!appendEventId) return;
    const controller = new AbortController();
    apiRequest('GET', `/conversion/events/${appendEventId}/destinations`, undefined, false, controller.signal)
      .then((list: AllowedDestinationTarget[]) => {
        if (!controller.signal.aborted) {
          setTargets(list || []);
        }
      })
      .catch((err) => {
        if (!controller.signal.aborted) {
          setTargetsError(getErrorMessage(err, '加载可追加目标失败'));
        }
      });
    return () => controller.abort();
  }, [action, appendEventId]);

  const validateReason = (raw: string): string => {
    const trimmed = raw.trim();
    if (!trimmed) return '请填写操作原因（1 至 500 字）';
    if (trimmed.length > 500) return '操作原因不能超过 500 字';
    return '';
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!action || inFlightRef.current) return;

    const rErr = validateReason(reason);
    if (rErr) {
      setReasonError(rErr);
      reasonRef.current?.focus();
      return;
    }

    if (action.kind === 'append' && !selectedTarget) {
      setSubmitError('请选择要追加的授权 Pixel 目标');
      return;
    }

    if (action.kind === 'retry' && action.delivery.state === 'outcome_unknown' && !acknowledged) {
      setSubmitError('结果未知任务重排前，必须先勾选确认重复计数风险');
      return;
    }

    inFlightRef.current = true;
    setBusy(true);
    setSubmitError('');
    try {
      const trimmedReason = reason.trim();
      if (action.kind === 'append') {
        await apiRequest('POST', `/conversion/events/${action.eventId}/deliveries`, {
          destination_id: selectedTarget,
          reason: trimmedReason,
        });
        await onSuccess('已追加授权 Pixel 投递任务并记录审计日志');
      } else if (action.kind === 'retry') {
        const result: { state?: string } = await apiRequest(
          'POST',
          `/conversion/deliveries/${action.delivery.id}/retry`,
          {
            reason: trimmedReason,
            expected_attempts: action.delivery.attempts,
            acknowledge_duplicate_risk: acknowledged,
          },
        );
        const pixel = `Pixel ${action.delivery.pixel_id}`;
        if (result.state === 'blocked') {
          await onSuccess(`${pixel} 的付款事实仍待核查，投递继续暂停。`, 'status');
        } else if (result.state === 'skipped') {
          await onSuccess(`${pixel} 已按付款规则跳过。`, 'status');
        } else {
          await onSuccess(`已提交 ${pixel} 重排调度并记录审计日志`);
        }
      } else {
        await apiRequest('POST', `/conversion/partner-reports/${action.report.id}/retry`, {
          reason: trimmedReason,
        });
        await onSuccess('已提交分销结果回告重排并记录审计日志');
      }
    } catch (err) {
      setSubmitError(getErrorMessage(err, '操作提交失败'));
    } finally {
      inFlightRef.current = false;
      setBusy(false);
    }
  };

  const title =
    action?.kind === 'append'
      ? '追加授权 Pixel 目标'
      : action?.kind === 'retry'
        ? `重排 Pixel ${action.delivery.pixel_id} 投递`
        : action?.kind === 'report'
          ? `重排分销结果回告 #${action.report.id}`
          : '';

  return (
    <CmsDialog
      open={Boolean(action)}
      onClose={() => {
        if (!busy) onClose();
      }}
      closedBy={busy ? 'none' : 'any'}
      closeDisabled={busy}
      variant="modal"
      size="md"
      title={title}
      subtitle="所有操作将实时复核账号权限并写入审计日志"
      footer={
        <>
          <button type="button" className="btn-secondary" disabled={busy} onClick={onClose}>
            取消
          </button>
          <button
            type="submit"
            form={formId}
            disabled={busy || (action?.kind === 'append' && (!targets || targets.length === 0))}
            className="btn-primary"
          >
            {busy ? '正在提交…' : '确认提交'}
          </button>
        </>
      }
    >
      {action && (
        <form id={formId} onSubmit={handleSubmit} noValidate className="space-y-4 text-xs">
          {action.kind === 'append' && (
            <div className="space-y-1.5">
              <label htmlFor={`${formId}-target`} className="block font-medium text-[var(--cms-muted-fg)]">
                授权目标 Pixel（必选）
              </label>
              {targetsError ? (
                <InlineErrorBanner message={targetsError} />
              ) : (
                <CustomSelect
                  id={`${formId}-target`}
                  width="100%"
                  disabled={!targets?.length || busy}
                  value={selectedTarget}
                  onChange={(val) => {
                    setSelectedTarget(val);
                    setSubmitError('');
                  }}
                  options={[
                    {
                      value: '',
                      label: targets
                        ? targets.length > 0
                          ? '请选择要追加的授权 Pixel…'
                          : '暂无可追加的已授权活跃目标'
                        : '正在加载可用目标…',
                    },
                    ...(targets?.map((item) => ({
                      value: item.id,
                      label: `${item.pixel_id}${item.display_name ? ` · ${item.display_name}` : ''}`,
                    })) ?? []),
                  ]}
                />
              )}
              {targets && targets.length === 0 && !targetsError && (
                <p className="text-[11px] text-[var(--cms-muted-fg)] m-0">
                  所有已授权且可用的 Pixel 均已存在投递任务，或当前事件路由受接入配置限制。如需授权新 Pixel，请前往「像素管理 → 接入与授权绑定」。
                </p>
              )}
            </div>
          )}

          {action.kind === 'retry' && (
            <div className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] space-y-1">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="font-medium text-[var(--cms-fg)]">
                  目标 Pixel：{action.delivery.pixel_id}
                </span>
                <ConversionStatusDot code={action.delivery.state} />
              </div>
              <div className="text-[11px] text-[var(--cms-muted-fg)] tabular-nums">
                当前已尝试 {action.delivery.attempts} 次 · 截止期限 {timestamp(action.delivery.deadline_at)}
              </div>
            </div>
          )}

          {action.kind === 'retry' && action.delivery.state === 'outcome_unknown' && (
            <label
              className="p-3 rounded-xl border flex items-start gap-2.5 cursor-pointer"
              style={{
                backgroundColor: 'color-mix(in srgb, var(--cms-warning) 10%, transparent)',
                borderColor: 'color-mix(in srgb, var(--cms-warning) 30%, transparent)',
              }}
            >
              <input
                type="checkbox"
                checked={acknowledged}
                onChange={(e) => {
                  setAcknowledged(e.target.checked);
                  setSubmitError('');
                }}
                className="mt-0.5 cursor-pointer"
              />
              <span className="text-[var(--cms-fg)] leading-relaxed">
                <strong>重复计数风险确认：</strong>
                该次请求此前可能已送达 Meta（缺少结构化确认回执）。我确认已完成核对并知晓重排可能导致 Meta 统计重复计数（仅允许授权一次）。
              </span>
            </label>
          )}

          {action.kind === 'report' && (
            <div className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-2">
              <span className="font-medium text-[var(--cms-fg)]">
                回告结果：{partnerReportStatusLabel(action.report.status)}
              </span>
              <ConversionStatusDot code={action.report.state} />
            </div>
          )}

          <div className="space-y-1.5">
            <label htmlFor={`${formId}-reason`} className="block font-medium text-[var(--cms-muted-fg)]">
              操作原因（必填，1-500 字）
            </label>
            <textarea
              ref={reasonRef}
              id={`${formId}-reason`}
              required
              maxLength={500}
              rows={3}
              disabled={busy}
              value={reason}
              placeholder="例如：已刷新目标 Pixel Token 权限，申请重排投递"
              aria-invalid={reasonError ? 'true' : 'false'}
              aria-describedby={reasonError ? reasonErrorId : undefined}
              onBlur={() => {
                if (reason.length > 0) {
                  setReasonError(validateReason(reason));
                }
              }}
              onChange={(e) => {
                const next = e.target.value;
                setReason(next);
                if (reasonError) {
                  setReasonError(validateReason(next));
                }
              }}
              className="input-field"
              style={reasonError ? { borderColor: 'var(--cms-error)' } : undefined}
            />
            <div className="flex items-center justify-between text-[11px]">
              {reasonError ? (
                <span id={reasonErrorId} className="text-[var(--cms-error)]">
                  {reasonError}
                </span>
              ) : (
                <span className="text-[var(--cms-muted-fg)]">操作原因将写入永久审计日志</span>
              )}
              <span className="tabular-nums text-[var(--cms-muted-fg)]">{reason.trim().length}/500</span>
            </div>
          </div>

          {submitError && <InlineErrorBanner message={submitError} />}
        </form>
      )}
    </CmsDialog>
  );
}

export function ConversionEventDrawer({
  eventId,
  initialSummary,
  canWrite,
  integrationMap,
  destinationByPixel,
  onClose,
  onChanged,
  onJumpToOrder,
}: {
  eventId: string | null;
  initialSummary?: ConversionEvent | null;
  canWrite: boolean;
  integrationMap: Map<string, ConversionIntegration>;
  destinationByPixel: Map<string, ConversionDestination>;
  onClose: () => void;
  onChanged: () => void;
  onJumpToOrder: (orderId: string, integrationId?: string) => void;
}) {
  const [detail, setDetail] = useState<ConversionEventDetail | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [auditAction, setAuditAction] = useState<AuditAction | null>(null);
  const [retryStatus, setRetryStatus] = useState('');
  useEffect(() => {
    setRetryStatus('');
  }, [eventId]);

  const [attemptsDeliveryId, setAttemptsDeliveryId] = useState('');
  const [attempts, setAttempts] = useState<ConversionDeliveryAttempt[]>([]);
  const [attemptBusy, setAttemptBusy] = useState(false);
  const [hasMoreAttempts, setHasMoreAttempts] = useState(false);
  const [attemptError, setAttemptError] = useState('');

  const loadDetail = useCallback(
    async (targetId: string, signal?: AbortSignal) => {
      setLoading(true);
      setError('');
      try {
        const res: ConversionEventDetail = await apiRequest(
          'GET',
          `/conversion/events/${targetId}`,
          undefined,
          false,
          signal,
        );
        if (!signal?.aborted) {
          setDetail(res);
        }
      } catch (err) {
        if (!signal?.aborted) {
          setError(getErrorMessage(err, '加载事件详情失败'));
        }
      } finally {
        if (!signal?.aborted) {
          setLoading(false);
        }
      }
    },
    [],
  );

  useEffect(() => {
    setAttemptsDeliveryId('');
    setAttempts([]);
    setHasMoreAttempts(false);
    setAttemptError('');
    setAuditAction(null);

    if (!eventId) {
      setDetail(null);
      setError('');
      return;
    }

    if (initialSummary && initialSummary.id === eventId) {
      setDetail(initialSummary);
    } else {
      setDetail(null);
    }

    const controller = new AbortController();
    void loadDetail(eventId, controller.signal);
    return () => controller.abort();
  }, [eventId, initialSummary, loadDetail]);

  const handleToggleAttempts = async (delivery: ConversionDelivery, beforeAttempt?: number) => {
    if (!eventId) return;
    if (!beforeAttempt && attemptsDeliveryId === delivery.id) {
      setAttemptsDeliveryId('');
      setAttempts([]);
      setHasMoreAttempts(false);
      setAttemptError('');
      return;
    }

    setAttemptBusy(true);
    setAttemptsDeliveryId(delivery.id);
    setAttemptError('');
    if (!beforeAttempt) {
      setAttempts([]);
    }

    try {
      const query = new URLSearchParams({ delivery_id: delivery.id });
      if (beforeAttempt) query.set('before_attempt', String(beforeAttempt));
      const res: { attempts?: ConversionDeliveryAttempt[] } = await apiRequest(
        'GET',
        `/conversion/events/${eventId}?${query.toString()}`,
      );
      const batch = res.attempts ?? [];
      setAttempts((prev) => (beforeAttempt ? [...prev, ...batch] : batch));
      setHasMoreAttempts(batch.length === 50);
    } catch (err) {
      setAttemptError(getErrorMessage(err, '加载尝试记录失败'));
    } finally {
      setAttemptBusy(false);
    }
  };

  const active = detail;
  const integration = active ? integrationMap.get(active.integration_id) : undefined;
  const activeAttemptsDelivery = active?.deliveries.find((d) => d.id === attemptsDeliveryId);
  const amountLabel = active ? formatAmount(active.amount, active.currency) : '';

  const eventDisplayTitle = active
    ? active.source === 'sinan'
      ? `事件 #${active.id} · ${sourceEventLabel(active.source, active.source_event_type)} (${active.event_name})`
      : `事件 #${active.id} · ${active.event_name}`
    : eventId
      ? `事件 #${eventId}`
      : '';

  return (
    <>
      <CmsDialog
        open={Boolean(eventId)}
        onClose={onClose}
        variant="drawer"
        size="xl"
        title={eventDisplayTitle}
        subtitle={
          active
            ? `${sourceLabel(active.source)}${integration ? ` (${integration.account_key})` : ''} · 原始事件 ${active.source_event_id}`
            : '正在加载统一转化事件详情…'
        }
        headerExtra={
          active && (
            <div className="flex items-center gap-2">
              <ConversionStatusDot code={active.attribution_state} className="hidden sm:inline-flex" />
              <button
                type="button"
                disabled={loading}
                onClick={() => void loadDetail(active.id)}
                className="btn-secondary h-8 px-2.5 text-xs"
                title="刷新事件详情"
              >
                <RefreshCw size={13} aria-hidden="true" />
                <span className="hidden sm:inline">刷新</span>
              </button>
              {canWrite && (
                <button
                  type="button"
                  onClick={() => setAuditAction({ kind: 'append', eventId: active.id })}
                  className="btn-primary h-8 px-3 text-xs"
                >
                  <Plus size={13} aria-hidden="true" />
                  <span>追加 Pixel</span>
                </button>
              )}
            </div>
          )
        }
        footer={
          <button type="button" className="btn-secondary" onClick={onClose}>
            关闭
          </button>
        }
      >
        <div role="status">
          {retryStatus && <p className="mt-0 mb-3 text-xs text-[var(--cms-muted-fg)]">{retryStatus}</p>}
        </div>
        {loading && !active ? (
          <div className="space-y-4">
            <div className="skeleton-bar h-24 rounded-2xl" />
            <div className="skeleton-bar h-40 rounded-2xl" />
            <div className="skeleton-bar h-32 rounded-2xl" />
          </div>
        ) : error && !active ? (
          <InlineErrorBanner
            message={error}
            onRetry={eventId ? () => void loadDetail(eventId) : undefined}
          />
        ) : (
          active && (
            <div className="space-y-5 text-xs">
              {error && (
                <InlineErrorBanner
                  message={error}
                  onRetry={() => void loadDetail(active.id)}
                />
              )}

              {/* Event Metadata Grid */}
              <div className="p-4 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3.5">
                <div className="space-y-0.5 min-w-0">
                  <div className="text-[var(--cms-muted-fg)]">内部事件 ID</div>
                  <div className="font-medium text-[var(--cms-fg)]">
                    <CopyableText value={active.id} label="内部事件 ID" />
                  </div>
                </div>

                <div className="space-y-0.5 min-w-0 sm:col-span-2">
                  <div className="text-[var(--cms-muted-fg)]">原始事件 ID（去重键）</div>
                  <div className="font-medium text-[var(--cms-fg)]">
                    <CopyableText value={active.source_event_id} label="原始事件 ID" />
                  </div>
                </div>

                <div className="space-y-0.5 min-w-0">
                  <div className="text-[var(--cms-muted-fg)]">归因状态</div>
                  <div className="flex flex-wrap items-center gap-1.5">
                    <ConversionStatusDot code={active.attribution_state} />
                    {active.reason && (
                      <span className="text-[11px] text-[var(--cms-muted-fg)]">
                        · {getReasonLabel(active.reason)}
                      </span>
                    )}
                  </div>
                </div>

                <div className="space-y-0.5 min-w-0">
                  <div className="text-[var(--cms-muted-fg)]">关联订单</div>
                  <div>
                    {active.order_id ? (
                      <button
                        type="button"
                        onClick={() => {
                          onClose();
                          onJumpToOrder(
                            active.order_id!,
                            active.source === 'starnovel' ? '' : active.integration_id,
                          );
                        }}
                        className="focus-ring inline-flex items-center gap-1 text-[var(--cms-primary)] hover:underline cursor-pointer font-mono-code"
                      >
                        <span>{active.order_id}</span>
                        <ExternalLink size={11} aria-hidden="true" />
                      </button>
                    ) : (
                      <span className="text-[var(--cms-muted-fg)]">—</span>
                    )}
                  </div>
                </div>

                <div className="space-y-0.5 min-w-0">
                  <div className="text-[var(--cms-muted-fg)]">金额 / 币种</div>
                  <div className="tabular-nums font-medium text-[var(--cms-fg)]">
                    {amountLabel || <span className="text-[var(--cms-muted-fg)] font-normal">—</span>}
                  </div>
                </div>

                <div className="space-y-0.5 min-w-0">
                  <div className="text-[var(--cms-muted-fg)]">推广链 ID</div>
                  <div className="font-mono-code text-[var(--cms-fg)]">
                    {active.promotion_id || <span className="text-[var(--cms-muted-fg)]">—</span>}
                  </div>
                </div>

                <div className="space-y-0.5 min-w-0">
                  <div className="text-[var(--cms-muted-fg)]">接入账户</div>
                  <div className="text-[var(--cms-fg)] truncate">
                    {sourceLabel(active.source)} ·{' '}
                    {integration ? `${integration.account_key} (#${active.integration_id})` : `#${active.integration_id}`}
                  </div>
                </div>

                <div className="space-y-0.5 min-w-0">
                  <div className="text-[var(--cms-muted-fg)]">业务发生时间</div>
                  <div className="tabular-nums text-[var(--cms-fg)]">{timestamp(active.occurred_at)}</div>
                </div>

                <div className="space-y-0.5 min-w-0">
                  <div className="text-[var(--cms-muted-fg)]">入队 / 接收时间</div>
                  <div className="tabular-nums text-[var(--cms-fg)]">{timestamp(active.enqueued_at)}</div>
                </div>

                <div className="space-y-0.5 min-w-0">
                  <div className="text-[var(--cms-muted-fg)]">事件入库时间</div>
                  <div className="tabular-nums text-[var(--cms-fg)]">{timestamp(active.created_at)}</div>
                </div>
              </div>

              {active.payment_policy && <PaymentDecisionSummary decision={active.payment_policy} />}

              {/* Meta Pixel Deliveries */}
              <div className="space-y-2.5">
                <div className="flex items-center justify-between gap-2">
                  <h3 className="text-sm font-semibold text-[var(--cms-fg)] m-0">
                    Meta Pixel 投递任务 ({active.deliveries.length})
                  </h3>
                  {active.deliveries_truncated && (
                    <span className="text-[11px] text-[var(--cms-warning)]">
                      仅展示前 50 个目标（原目标优先）
                    </span>
                  )}
                </div>

                {active.deliveries.length === 0 ? (
                  <div className="cms-card p-6 text-center text-[var(--cms-muted-fg)]">
                    当前事件未生成 Pixel 投递任务
                    {active.reason ? `（原因：${getReasonLabel(active.reason)}）` : ''}。
                  </div>
                ) : (
                  <TargetResultsTable
                    deliveries={active.deliveries}
                    destinationByPixel={destinationByPixel}
                    canWrite={canWrite}
                    activeAttemptsDeliveryId={attemptsDeliveryId}
                    attemptBusy={attemptBusy}
                    onViewAttempts={(d) => void handleToggleAttempts(d)}
                    onRetry={(d) => setAuditAction({ kind: 'retry', delivery: d })}
                  />
                )}
              </div>

              {/* Delivery Attempts Timeline */}
              {attemptsDeliveryId && (
                <div className="cms-card p-4 space-y-3">
                  <div className="flex items-center justify-between gap-2">
                    <h4 className="text-xs font-semibold text-[var(--cms-fg)] m-0">
                      投递尝试时间线 · Pixel {activeAttemptsDelivery?.pixel_id ?? attemptsDeliveryId}
                    </h4>
                    <button
                      type="button"
                      onClick={() => {
                        setAttemptsDeliveryId('');
                        setAttempts([]);
                      }}
                      className="btn-ghost h-7 px-2 text-xs text-[var(--cms-muted-fg)]"
                    >
                      收起
                    </button>
                  </div>

                  {attemptError && <InlineErrorBanner message={attemptError} />}

                  {attemptBusy && attempts.length === 0 ? (
                    <div className="skeleton-bar h-20 rounded-xl" />
                  ) : attempts.length === 0 ? (
                    <p className="text-[var(--cms-muted-fg)] m-0">该目标暂无结构化尝试记录。</p>
                  ) : (
                    <TableRegion ariaLabel="Meta 投递尝试记录">
                      <table className="cms-table min-w-[780px]" data-density="compact">
                        <thead>
                          <tr>
                            <th scope="col">次数</th>
                            <th scope="col">结果 / 原因</th>
                            <th scope="col">HTTP</th>
                            <th scope="col">Graph 错误 / 接收</th>
                            <th scope="col">fbtrace_id</th>
                            <th scope="col">领取时间</th>
                            <th scope="col">记录时间</th>
                          </tr>
                        </thead>
                        <tbody>
                          {attempts.map((att) => (
                            <tr key={att.number}>
                              <th scope="row" className="tabular-nums font-medium">
                                #{att.number}
                              </th>
                              <td>
                                {att.outcome ? (
                                  <ConversionStatusDot code={att.outcome} />
                                ) : (
                                  <span className="text-[var(--cms-primary)]">进行中</span>
                                )}
                                {att.reason && (
                                  <div className="text-[11px] text-[var(--cms-muted-fg)] mt-0.5">
                                    {getReasonLabel(att.reason)}
                                  </div>
                                )}
                              </td>
                              <td className="tabular-nums">{att.http_status ?? '—'}</td>
                              <td className="tabular-nums">
                                {att.graph_code !== undefined
                                  ? `Graph ${att.graph_code}/${att.graph_subcode ?? '—'}`
                                  : att.events_received !== undefined
                                    ? `接收 ${att.events_received}`
                                    : '—'}
                              </td>
                              <td>
                                {att.fbtrace_id ? (
                                  <div className="max-w-[160px] min-w-0">
                                    <CopyableText value={att.fbtrace_id} label="fbtrace_id" />
                                  </div>
                                ) : (
                                  '—'
                                )}
                              </td>
                              <td className="tabular-nums whitespace-nowrap">
                                {timestamp(att.started_at)}
                              </td>
                              <td className="tabular-nums whitespace-nowrap">
                                {timestamp(att.recorded_at)}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </TableRegion>
                  )}

                  {hasMoreAttempts && activeAttemptsDelivery && (
                    <div>
                      <button
                        type="button"
                        disabled={attemptBusy}
                        onClick={() =>
                          void handleToggleAttempts(activeAttemptsDelivery, attempts.at(-1)?.number)
                        }
                        className="btn-secondary h-8 px-3 text-xs"
                      >
                        {attemptBusy ? '加载中…' : '加载更早尝试'}
                      </button>
                    </div>
                  )}
                </div>
              )}

              {/* Partner Reports */}
              {active.partner_reports && active.partner_reports.length > 0 && (
                <div className="flex flex-col gap-3">
                  <h3 className="text-sm font-semibold text-[var(--cms-fg)]">
                    分销最终结果回告 ({active.partner_reports.length})
                  </h3>
                  {active.partner_reports.map((report) => (
                    <PartnerReportCard
                      key={report.id}
                      report={report}
                      canWrite={canWrite}
                      onRetryReport={(r) => setAuditAction({ kind: 'report', report: r })}
                    />
                  ))}
                </div>
              )}
            </div>
          )
        )}
      </CmsDialog>

      <AuditActionModal
        action={auditAction}
        onClose={() => setAuditAction(null)}
        onSuccess={async (msg, feedback) => {
          setAuditAction(null);
          setRetryStatus(feedback === 'status' ? msg : '');
          if (feedback !== 'status') {
            window.showToast?.(msg, 'success');
          }
          if (eventId) {
            await loadDetail(eventId);
          }
          onChanged();
        }}
      />
    </>
  );
}

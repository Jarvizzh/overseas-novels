import { TableRegion, type TableDensity } from '../ui/Primitives';
import {
  getDeliveryRetryEligibility,
  getReasonLabel,
  timestamp,
} from '../../utils/conversionStatus';
import { ConversionStatusDot, CopyableText } from './ConversionShared';
import type { ConversionDelivery, ConversionDestination } from './types';

export function TargetResultsTable({
  deliveries,
  destinationByPixel,
  canWrite = false,
  activeAttemptsDeliveryId = '',
  attemptBusy = false,
  density = 'compact',
  onViewAttempts,
  onRetry,
}: {
  deliveries: ConversionDelivery[];
  destinationByPixel?: Map<string, ConversionDestination>;
  canWrite?: boolean;
  activeAttemptsDeliveryId?: string;
  attemptBusy?: boolean;
  density?: TableDensity;
  onViewAttempts?: (delivery: ConversionDelivery) => void;
  onRetry?: (delivery: ConversionDelivery) => void;
}) {
  const showActionCol = Boolean(onViewAttempts || (canWrite && onRetry));

  return (
    <TableRegion ariaLabel="Pixel 投递任务结果">
      <table className="cms-table min-w-[620px] [&_th]:px-2.5 [&_td]:px-2.5" data-density={density}>
        <caption className="sr-only">Pixel 投递任务结果</caption>
        <thead>
          <tr>
            <th scope="col">目标 Pixel</th>
            <th scope="col">投递状态 / 原因</th>
            <th scope="col">Meta 事件 ID / 尝试</th>
            <th scope="col">调度与确认时间</th>
            <th scope="col">最近响应回执</th>
            {showActionCol && (
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            )}
          </tr>
        </thead>
        <tbody>
          {deliveries.map((delivery) => {
            const destination = destinationByPixel?.get(delivery.pixel_id);
            const reasonText = getReasonLabel(delivery.reason);
            const lastAttemptReason = getReasonLabel(delivery.last_attempt?.reason);
            const retryCheck = getDeliveryRetryEligibility(delivery);

            return (
              <tr
                key={delivery.id}
                className={activeAttemptsDeliveryId === delivery.id ? 'row-selected' : ''}
              >
                <th scope="row" className="text-left font-normal align-top">
                  <div className="space-y-1 min-w-0 max-w-[164px]">
                    <CopyableText
                      value={delivery.pixel_id}
                      label="Pixel ID"
                      className="font-medium text-[var(--cms-fg)]"
                    />
                    <div className="flex items-center gap-1.5 min-w-0">
                      <span
                        className={`badge shrink-0 ${
                          delivery.role === 'original' ? 'badge-blue' : 'badge-gray'
                        }`}
                      >
                        {delivery.role === 'original' ? '原目标' : '附加目标'}
                      </span>
                      {destination?.name && (
                        <span
                          className="text-[11px] text-[var(--cms-muted-fg)] truncate min-w-0"
                          title={destination.name}
                        >
                          {destination.name}
                        </span>
                      )}
                    </div>
                  </div>
                </th>

                <td className="align-top">
                  <div className="space-y-1 min-w-0 max-w-[148px]">
                    <ConversionStatusDot code={delivery.state} />
                    {reasonText && (
                      <div
                        className="text-[11px] text-[var(--cms-muted-fg)] break-words"
                        title={delivery.reason}
                      >
                        {reasonText}
                      </div>
                    )}
                  </div>
                </td>

                <td className="align-top">
                  <div className="space-y-1 min-w-0 max-w-[152px]">
                    <CopyableText
                      value={delivery.meta_event_id}
                      label="Meta 事件 ID"
                      className="text-[var(--cms-fg)]"
                    />
                    <div className="text-[11px] text-[var(--cms-muted-fg)] tabular-nums">
                      已尝试 {delivery.attempts} 次
                    </div>
                  </div>
                </td>

                <td className="align-top whitespace-nowrap tabular-nums text-[11px] text-[var(--cms-muted-fg)] space-y-0.5">
                  <div>上次：{timestamp(delivery.last_attempt_at)}</div>
                  {delivery.next_attempt_at && (
                    <div className="text-[var(--cms-primary)]">
                      下次：{timestamp(delivery.next_attempt_at)}
                    </div>
                  )}
                  {delivery.deadline_at && <div>截止：{timestamp(delivery.deadline_at)}</div>}
                  {delivery.confirmed_at && (
                    <div className="text-[var(--cms-success)]">
                      确认：{timestamp(delivery.confirmed_at)}
                    </div>
                  )}
                </td>

                <td className="align-top text-[11px]">
                  <div className="min-w-0 max-w-[136px] space-y-0.5">
                    {delivery.last_attempt ? (
                      <>
                        <div className="flex flex-wrap items-center gap-1.5 text-[var(--cms-fg)] tabular-nums">
                          {delivery.last_attempt.http_status !== undefined && (
                            <span className="badge badge-secondary">
                              HTTP {delivery.last_attempt.http_status}
                            </span>
                          )}
                          {delivery.last_attempt.graph_code !== undefined && (
                            <span className="badge badge-amber">
                              Graph {delivery.last_attempt.graph_code}
                              {delivery.last_attempt.graph_subcode !== undefined
                                ? `/${delivery.last_attempt.graph_subcode}`
                                : ''}
                            </span>
                          )}
                        </div>
                        {delivery.last_attempt.fbtrace_id && (
                          <CopyableText
                            value={delivery.last_attempt.fbtrace_id}
                            label="fbtrace_id"
                            className="text-[var(--cms-muted-fg)]"
                          />
                        )}
                        {lastAttemptReason && (
                          <div
                            className="text-[var(--cms-muted-fg)] break-words"
                            title={delivery.last_attempt.reason}
                          >
                            {lastAttemptReason}
                          </div>
                        )}
                      </>
                    ) : (
                      <span className="text-[var(--cms-muted-fg)]">—</span>
                    )}
                  </div>
                </td>

                {showActionCol && (
                  <td className="text-right align-top whitespace-nowrap">
                    <div className="inline-flex items-center justify-end gap-1">
                      {onViewAttempts && delivery.attempts > 0 && (
                        <button
                          type="button"
                          disabled={attemptBusy}
                          onClick={() => onViewAttempts(delivery)}
                          className={`btn-detail ${
                            activeAttemptsDeliveryId === delivery.id
                              ? 'bg-[var(--cms-accent)] font-medium'
                              : ''
                          }`}
                        >
                          {activeAttemptsDeliveryId === delivery.id ? '收起记录' : '尝试记录'}
                        </button>
                      )}
                      {canWrite && onRetry && !retryCheck.isTerminal && (
                        <button
                          type="button"
                          disabled={!retryCheck.allowed}
                          title={retryCheck.hint}
                          onClick={() => onRetry(delivery)}
                          className="btn-detail"
                        >
                          重排
                        </button>
                      )}
                    </div>
                  </td>
                )}
              </tr>
            );
          })}
        </tbody>
      </table>
    </TableRegion>
  );
}

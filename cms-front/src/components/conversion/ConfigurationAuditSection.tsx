import { useCallback, useEffect, useState } from 'react';
import { History, RefreshCw } from 'lucide-react';
import { ROLE_LABELS } from '../../config/navigation';
import { apiRequest } from '../../utils/api';
import {
  auditActionLabel,
  formatAuditHighlights,
  getErrorMessage,
  timestamp,
} from '../../utils/conversionStatus';
import { InlineErrorBanner } from './ConversionShared';
import type {
  ConversionAuditPage,
  ConversionAuditRecord,
  ConversionDestination,
} from './types';

function formatSnapshotJson(value: unknown): string {
  if (value === undefined || value === null) return 'null';
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function ConfigurationAuditSection({
  subjectType,
  subjectId,
  refreshKey = 0,
  destinationById,
}: {
  subjectType: 'destination' | 'integration';
  subjectId: string;
  refreshKey?: number;
  destinationById?: Map<string, ConversionDestination>;
}) {
  const [records, setRecords] = useState<ConversionAuditRecord[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState('');

  const fetchAudit = useCallback(
    async (beforeId?: string, signal?: AbortSignal) => {
      if (!subjectId) return;
      if (beforeId) {
        setLoadingMore(true);
      } else {
        setLoading(true);
      }
      setError('');

      try {
        const params = new URLSearchParams({
          subject_type: subjectType,
          subject_id: subjectId,
          limit: '20',
        });
        if (beforeId) {
          params.set('before_id', beforeId);
        }
        const page: ConversionAuditPage = await apiRequest(
          'GET',
          `/conversion/audit?${params.toString()}`,
          undefined,
          false,
          signal,
        );
        if (signal?.aborted) return;
        const nextBatch = page.records ?? [];
        setRecords((prev) => (beforeId ? [...prev, ...nextBatch] : nextBatch));
        setHasMore(Boolean(page.has_more));
      } catch (err) {
        if (!signal?.aborted) {
          setError(getErrorMessage(err, '加载配置变更审计记录失败'));
        }
      } finally {
        if (!signal?.aborted) {
          setLoading(false);
          setLoadingMore(false);
        }
      }
    },
    [subjectType, subjectId],
  );

  useEffect(() => {
    setRecords([]);
    setHasMore(false);
    const controller = new AbortController();
    void fetchAudit(undefined, controller.signal);
    return () => controller.abort();
  }, [fetchAudit, refreshKey]);

  return (
    <div className="space-y-3 text-xs">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 text-[var(--cms-muted-fg)]">
          <History size={14} aria-hidden="true" />
          <span>记录最近的配置创建、编辑、密钥轮换与路由绑定审计（只读不可篡改）</span>
        </div>
        <button
          type="button"
          disabled={loading || loadingMore}
          onClick={() => void fetchAudit()}
          className="btn-secondary h-7 px-2.5 text-xs shrink-0"
        >
          <RefreshCw size={12} aria-hidden="true" />
          <span>刷新</span>
        </button>
      </div>

      {error && (
        <InlineErrorBanner
          message={error}
          onRetry={() => void fetchAudit()}
        />
      )}

      {loading ? (
        <div className="space-y-2.5" aria-busy="true" aria-label="正在加载审计记录">
          {[0, 1, 2].map((idx) => (
            <div key={idx} className="cms-card p-3.5 space-y-2">
              <div className="skeleton-bar h-4 w-48" />
              <div className="skeleton-bar h-3.5 w-full" />
              <div className="skeleton-bar h-3.5 w-2/3" />
            </div>
          ))}
        </div>
      ) : records.length === 0 ? (
        error ? null : (
          <div className="cms-card p-8 text-center text-[var(--cms-muted-fg)]">
            暂无历史配置变更审计记录（后续修改将在保存后自动留痕）。
          </div>
        )
      ) : (
        <div className="space-y-2.5">
          {records.map((record) => {
            const highlights = formatAuditHighlights(
              record.before_state,
              record.after_state,
              destinationById,
            );
            const roleText = record.operator_role
              ? ROLE_LABELS[record.operator_role] || record.operator_role
              : '';
            const hasSnapshots =
              record.before_state !== undefined || record.after_state !== undefined;

            return (
              <div
                key={record.id}
                className="cms-card p-3.5 space-y-2"
              >
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="badge badge-blue">{auditActionLabel(record.action)}</span>
                    <span className="font-mono-code text-[11px] text-[var(--cms-muted-fg)]">
                      #{record.id}
                    </span>
                  </div>
                  <span className="tabular-nums text-[11px] text-[var(--cms-muted-fg)]">
                    {timestamp(record.created_at)}
                  </span>
                </div>

                <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[var(--cms-fg)]">
                  <span>
                    <span className="text-[var(--cms-muted-fg)]">操作人：</span>
                    <span className="font-mono-code">{record.operator_id || '系统'}</span>
                    {roleText && (
                      <span className="text-[var(--cms-muted-fg)]"> ({roleText})</span>
                    )}
                  </span>
                  {record.evidence_reference && (
                    <span className="text-[var(--cms-muted-fg)] font-mono-code">
                      证据：{record.evidence_reference}
                    </span>
                  )}
                </div>

                {record.reason && (
                  <div className="p-2 rounded-lg bg-[var(--cms-content)] text-[var(--cms-fg)] leading-relaxed">
                    <span className="text-[var(--cms-muted-fg)]">变更原因：</span>
                    {record.reason}
                  </div>
                )}

                {highlights.length > 0 && (
                  <ul className="m-0 pl-4 space-y-0.5 text-[11px] text-[var(--cms-muted-fg)]">
                    {highlights.map((line, i) => (
                      <li key={i}>{line}</li>
                    ))}
                  </ul>
                )}

                {hasSnapshots && (
                  <details className="pt-1">
                    <summary className="cursor-pointer select-none text-[11px] text-[var(--cms-primary)] hover:underline">
                      查看变更前后状态快照 (JSON)
                    </summary>
                    <div className="mt-2 grid grid-cols-1 md:grid-cols-2 gap-2">
                      <div className="space-y-1">
                        <div className="text-[11px] text-[var(--cms-muted-fg)]">变更前 (Before)</div>
                        <pre className="m-0 p-2 rounded-lg bg-[var(--cms-content)] border border-[var(--cms-border)] font-mono-code text-[11px] overflow-x-auto max-h-48 text-[var(--cms-fg)]">
                          {formatSnapshotJson(record.before_state)}
                        </pre>
                      </div>
                      <div className="space-y-1">
                        <div className="text-[11px] text-[var(--cms-muted-fg)]">变更后 (After)</div>
                        <pre className="m-0 p-2 rounded-lg bg-[var(--cms-content)] border border-[var(--cms-border)] font-mono-code text-[11px] overflow-x-auto max-h-48 text-[var(--cms-fg)]">
                          {formatSnapshotJson(record.after_state)}
                        </pre>
                      </div>
                    </div>
                  </details>
                )}
              </div>
            );
          })}

          {hasMore && (
            <div className="pt-1 flex justify-center">
              <button
                type="button"
                disabled={loadingMore}
                onClick={() => void fetchAudit(records.at(-1)?.id)}
                className="btn-secondary h-8 px-4 text-xs"
              >
                {loadingMore ? '正在加载…' : '加载更早审计记录'}
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

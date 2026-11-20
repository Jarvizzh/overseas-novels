import { type FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { History } from 'lucide-react';
import { apiRequest } from '../../utils/api';
import CustomSelect from '../CustomSelect';
import { CmsDialog } from '../ui/CmsDialog';
import { Pagination } from '../ui/Pagination';
import {
  type FilterChip,
  FilterChipsBar,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../ui/Primitives';
import type { CAPILog } from './types';

const eventOptions = [
  { value: '', label: '全部事件' },
  { value: 'CompleteRegistration', label: 'CompleteRegistration' },
  { value: 'InitiateCheckout', label: 'InitiateCheckout' },
  { value: 'Purchase', label: 'Purchase' },
];

const statusOptions = [
  { value: '', label: '全部状态' },
  { value: '200', label: '200 OK (成功)' },
  { value: '0', label: '0 (空跑/测试)' },
  { value: '-1', label: '-1 (参数缺失)' },
  { value: '-2', label: '-2 (Token配置缺失)' },
  { value: '-4', label: '-4 (网络请求失败)' },
];

function formatJSON(jsonStr?: string) {
  if (!jsonStr) return '（无内容）';
  try {
    return JSON.stringify(JSON.parse(jsonStr), null, 2);
  } catch {
    return jsonStr;
  }
}

export function LegacyTrackingLogsTab() {
  const [logs, setLogs] = useState<CAPILog[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [density, setDensity] = useState<TableDensity>('comfortable');
  const [selectedLog, setSelectedLog] = useState<CAPILog | null>(null);

  const [pixelFilter, setPixelFilter] = useState('');
  const [queryState, setQueryState] = useState({
    page: 1,
    pageSize: 10,
    pixelId: '',
    eventName: '',
    statusCode: '',
    nonce: 0,
  });
  const fetchSeqRef = useRef(0);

  const fetchLogs = useCallback(async () => {
    const seq = ++fetchSeqRef.current;
    setLoading(true);
    try {
      const params = new URLSearchParams();
      params.set('page', String(queryState.page));
      params.set('page_size', String(queryState.pageSize));
      if (queryState.pixelId) params.set('pixel_id', queryState.pixelId);
      if (queryState.eventName) params.set('event_name', queryState.eventName);
      if (queryState.statusCode) params.set('status_code', queryState.statusCode);

      const data = await apiRequest('GET', `/tracking-logs?${params.toString()}`);
      if (seq !== fetchSeqRef.current) return;
      setLogs(data.logs || []);
      setTotal(data.total || 0);
      setHasLoadedOnce(true);
    } catch (e) {
      if (seq !== fetchSeqRef.current) return;
      window.showToast?.(`加载历史回传日志失败：${e}`, 'error');
      setHasLoadedOnce(true);
    } finally {
      if (seq === fetchSeqRef.current) {
        setLoading(false);
      }
    }
  }, [queryState]);

  useEffect(() => {
    void fetchLogs();
  }, [fetchLogs]);

  const handleSearchSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setQueryState((prev) => ({
      ...prev,
      page: 1,
      pixelId: pixelFilter.trim(),
      nonce: prev.nonce + 1,
    }));
  };

  const handleReset = useCallback(() => {
    setPixelFilter('');
    setQueryState((prev) => ({
      ...prev,
      page: 1,
      pixelId: '',
      eventName: '',
      statusCode: '',
      nonce: prev.nonce + 1,
    }));
  }, []);

  const filterChips = useMemo<FilterChip[]>(() => {
    const chips: FilterChip[] = [];
    if (queryState.pixelId) {
      chips.push({
        key: 'pixelId',
        label: `像素 ID: ${queryState.pixelId}`,
        onClear: () => {
          setPixelFilter('');
          setQueryState((prev) => ({ ...prev, page: 1, pixelId: '' }));
        },
      });
    }
    if (queryState.eventName) {
      chips.push({
        key: 'event',
        label: `事件: ${queryState.eventName}`,
        onClear: () => setQueryState((prev) => ({ ...prev, page: 1, eventName: '' })),
      });
    }
    if (queryState.statusCode) {
      chips.push({
        key: 'statusCode',
        label: `状态码: ${queryState.statusCode}`,
        onClear: () => setQueryState((prev) => ({ ...prev, page: 1, statusCode: '' })),
      });
    }
    return chips;
  }, [queryState]);

  const hasFilter = Boolean(
    pixelFilter || queryState.pixelId || queryState.eventName || queryState.statusCode,
  );

  return (
    <div className="space-y-4 animate-fade-in">
      <div className="px-4 py-2.5 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
        <History size={14} className="shrink-0 text-[var(--cms-primary)]" aria-hidden="true" />
        <span>
          此视图展示统一回传架构升级前的旧 <code className="font-mono-code">capi_logs</code> 历史记录（只读，仅 SuperAdmin 可访问），点击任意行或「详情」可查看完整请求载荷与响应包体。
        </span>
      </div>

      {/* Unified Borderless Filter Strip */}
      <search className="block space-y-2">
        <form onSubmit={handleSearchSubmit} className="flex flex-wrap items-center justify-between gap-2.5">
          <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
            <SearchInput
              className="w-full sm:w-64"
              ariaLabel="像素 ID"
              placeholder="搜索像素 ID..."
              value={pixelFilter}
              onChange={setPixelFilter}
              onClear={() => {
                if (queryState.pixelId) {
                  setQueryState((prev) => ({ ...prev, page: 1, pixelId: '' }));
                }
              }}
            />

            <CustomSelect
              aria-label="筛选转化事件类型"
              options={eventOptions}
              value={queryState.eventName}
              onChange={(val) => {
                setQueryState((prev) => ({ ...prev, eventName: val, page: 1 }));
              }}
              width="178px"
            />

            <CustomSelect
              aria-label="筛选回传状态码"
              options={statusOptions}
              value={queryState.statusCode}
              onChange={(val) => {
                setQueryState((prev) => ({ ...prev, statusCode: val, page: 1 }));
              }}
              width="160px"
            />

            <button type="submit" className="btn-secondary h-9 px-3 text-xs font-normal whitespace-nowrap">
              筛选
            </button>

            {hasFilter && (
              <button
                type="button"
                className="btn-ghost h-9 text-xs text-[var(--cms-muted-fg)]"
                onClick={handleReset}
              >
                重置
              </button>
            )}
          </div>

          <div className="flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
            <span className="tabular-nums">共 {total} 条</span>
            <ViewOptionsPopover density={density} onDensityChange={setDensity} />
          </div>
        </form>

        <FilterChipsBar chips={filterChips} onResetAll={handleReset} />
      </search>

      {/* Table + External Pagination */}
      <TableRegion
        ariaLabel="CAPI 历史回传审计日志"
        footer={
          <Pagination
            page={queryState.page}
            pageSize={queryState.pageSize}
            total={total}
            disabled={loading}
            onPageChange={(nextPage) => setQueryState((prev) => ({ ...prev, page: nextPage }))}
            onPageSizeChange={(nextSize) =>
              setQueryState((prev) => ({ ...prev, page: 1, pageSize: nextSize }))
            }
            itemLabel="条日志"
          />
        }
      >
        <table className="cms-table min-w-[900px]" data-density={density}>
          <thead>
            <tr>
              <th scope="col">ID</th>
              <th scope="col">像素 ID</th>
              <th scope="col">事件类型</th>
              <th scope="col">关联用户</th>
              <th scope="col" className="text-right">
                回传金额
              </th>
              <th scope="col">测试代码</th>
              <th scope="col">状态码</th>
              <th scope="col">回传时间</th>
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={6} cols={9} />
            ) : logs.length === 0 ? (
              <tr>
                <td colSpan={9} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  暂无历史回传日志记录
                </td>
              </tr>
            ) : (
              logs.map((log) => (
                <tr
                  key={log.id}
                  className={selectedLog?.id === log.id ? 'row-selected' : undefined}
                >
                  <td className="tabular-nums text-[var(--cms-muted-fg)]">#{log.id}</td>
                  <td className="tabular-nums font-mono-code text-[var(--cms-fg)]">{log.pixel_id}</td>
                  <td className="text-[var(--cms-fg)]">{log.event_name}</td>
                  <td className="text-[var(--cms-muted-fg)] tabular-nums">
                    {log.user_id ? `UID ${log.user_id}` : '—'}
                  </td>
                  <td className="text-right tabular-nums text-[var(--cms-fg)] whitespace-nowrap">
                    {log.value ? (
                      `${log.value}${log.currency ? ` ${log.currency}` : ''}`
                    ) : (
                      <span className="text-[var(--cms-muted-fg)]">—</span>
                    )}
                  </td>
                  <td className="text-[var(--cms-muted-fg)] font-mono-code">
                    {log.test_event_code || (log.test ? '测试' : '—')}
                  </td>
                  <td>
                    <StatusDot
                      tone={
                        log.status_code === 200
                          ? 'success'
                          : log.status_code === 0
                            ? 'warning'
                            : 'error'
                      }
                      label={`${log.status_code} ${
                        log.status_code === 200 ? 'OK' : log.status_code === 0 ? 'Dry Run' : 'Error'
                      }`}
                    />
                  </td>
                  <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                    {new Date(log.created_at).toLocaleString()}
                  </td>
                  <td className="text-right">
                    <button
                      type="button"
                      onClick={() => setSelectedLog(log)}
                      className="btn-detail"
                    >
                      详情
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableRegion>

      {/* Right Slide-Over Log Inspector Drawer */}
      <CmsDialog
        open={Boolean(selectedLog)}
        onClose={() => setSelectedLog(null)}
        variant="drawer"
        title={selectedLog ? `回传日志 #${selectedLog.id}` : ''}
        subtitle={
          selectedLog
            ? `${selectedLog.event_name} · Pixel ${selectedLog.pixel_id} · ${new Date(selectedLog.created_at).toLocaleString()}`
            : undefined
        }
        size="lg"
        footer={
          <button type="button" className="btn-secondary" onClick={() => setSelectedLog(null)}>
            关闭
          </button>
        }
      >
        {selectedLog && (
          <div className="space-y-4 text-xs">
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-2.5 p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)]">
              <div>
                <div className="text-[11px] text-[var(--cms-muted-fg)]">关联用户</div>
                <div className="mt-0.5 font-medium tabular-nums text-[var(--cms-fg)]">
                  {selectedLog.user_id ? `UID ${selectedLog.user_id}` : '—'}
                </div>
              </div>
              <div>
                <div className="text-[11px] text-[var(--cms-muted-fg)]">回传金额</div>
                <div className="mt-0.5 font-medium tabular-nums text-[var(--cms-fg)]">
                  {selectedLog.value
                    ? `${selectedLog.value}${selectedLog.currency ? ` ${selectedLog.currency}` : ''}`
                    : '—'}
                </div>
              </div>
              <div>
                <div className="text-[11px] text-[var(--cms-muted-fg)]">测试代码</div>
                <div className="mt-0.5 font-medium font-mono-code text-[var(--cms-fg)]">
                  {selectedLog.test_event_code || (selectedLog.test ? '测试' : '—')}
                </div>
              </div>
              <div>
                <div className="text-[11px] text-[var(--cms-muted-fg)]">状态码</div>
                <div
                  className="mt-0.5 font-medium tabular-nums"
                  style={{
                    color:
                      selectedLog.status_code === 200
                        ? 'var(--cms-success)'
                        : selectedLog.status_code === 0
                          ? 'var(--cms-warning)'
                          : 'var(--cms-error)',
                  }}
                >
                  {selectedLog.status_code}
                </div>
              </div>
            </div>
            <div className="space-y-1.5">
              <div className="font-medium text-[var(--cms-muted-fg)]">Request Payload (请求载荷)</div>
              <pre className="p-4 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] font-mono-code text-xs max-h-72 overflow-auto text-[var(--cms-fg)] m-0 leading-relaxed">
                {formatJSON(selectedLog.payload)}
              </pre>
            </div>
            <div className="space-y-1.5">
              <div className="font-medium text-[var(--cms-muted-fg)]">Response Body (响应包体)</div>
              <pre
                className="p-4 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] font-mono-code text-xs max-h-72 overflow-auto m-0 leading-relaxed"
                style={{
                  color:
                    selectedLog.status_code === 200 ? 'var(--cms-success)' : 'var(--cms-error)',
                }}
              >
                {formatJSON(selectedLog.response)}
              </pre>
            </div>
          </div>
        )}
      </CmsDialog>
    </div>
  );
}

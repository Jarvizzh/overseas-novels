import { type FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ExternalLink, RefreshCw, SlidersHorizontal } from 'lucide-react';
import { apiRequest } from '../../utils/api';
import CustomSelect from '../CustomSelect';
import {
  type ColumnOption,
  type FilterChip,
  FilterChipsBar,
  SearchInput,
  SkeletonRows,
  type TableDensity,
  TableRegion,
  ToolbarPopover,
  ViewOptionsPopover,
} from '../ui/Primitives';
import {
  formatAmount,
  getErrorMessage,
  getReasonLabel,
  isServiceUnavailableError,
  partnerReportStatusLabel,
  sourceEventLabel,
  sourceLabel,
  status,
  timestamp,
} from '../../utils/conversionStatus';
import {
  ConversionStatusDot,
  CopyableText,
  InlineErrorBanner,
  ServiceUnavailableState,
} from './ConversionShared';
import type {
  ConversionDestination,
  ConversionEvent,
  ConversionEventPage,
  ConversionIntegration,
} from './types';

type SearchMode = 'order_id' | 'event_id' | 'source_event_id';
type TimePreset = '24h' | '72h' | '168h' | 'custom';
type EventOptionalCol = 'promotion_id' | 'source_event_id' | 'enqueued_at';

interface EventFilterState {
  searchMode: SearchMode;
  searchKeyword: string;
  source: string;
  eventName: string;
  deliveryState: string;
  integrationId: string;
  pixelId: string;
  attributionState: string;
  partnerReportState: string;
  promotionId: string;
  timePreset: TimePreset;
  fromLocal: string;
  toLocal: string;
}

const EMPTY_FILTER: EventFilterState = {
  searchMode: 'order_id',
  searchKeyword: '',
  source: '',
  eventName: '',
  deliveryState: '',
  integrationId: '',
  pixelId: '',
  attributionState: '',
  partnerReportState: '',
  promotionId: '',
  timePreset: '24h',
  fromLocal: '',
  toLocal: '',
};

const SEARCH_MODE_OPTIONS = [
  { value: 'order_id', label: '按订单号' },
  { value: 'event_id', label: '按事件 ID' },
  { value: 'source_event_id', label: '按原始事件 ID' },
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

const DELIVERY_STATE_OPTIONS = [
  { value: '', label: '全部投递状态' },
  { value: 'pending', label: '待处理 (pending)' },
  { value: 'blocked', label: '阻塞 (blocked)' },
  { value: 'outcome_unknown', label: '结果未知 (unknown)' },
  { value: 'succeeded', label: '已确认接收 (succeeded)' },
  { value: 'failed', label: '失败 (failed)' },
  { value: 'skipped', label: '已跳过 (skipped)' },
];

const ATTRIBUTION_OPTIONS = [
  { value: '', label: '全部归因状态' },
  { value: 'matched', label: '已归因 (matched)' },
  { value: 'missing_pixel', label: '缺 Pixel (missing_pixel)' },
  { value: 'organic', label: '自然流量 (organic)' },
  { value: 'unknown', label: '历史未知 (unknown)' },
];

const PARTNER_REPORT_STATE_OPTIONS = [
  { value: '', label: '全部回告状态' },
  { value: 'pending', label: '待发送 (pending)' },
  { value: 'blocked', label: '回告阻塞 (blocked)' },
  { value: 'outcome_unknown', label: '回告结果未知' },
  { value: 'confirmed', label: '分销已确认 (confirmed)' },
];

const TIME_PRESET_OPTIONS = [
  { value: '24h', label: '最近 24 小时（默认）' },
  { value: '72h', label: '最近 3 天 (72h)' },
  { value: '168h', label: '最近 7 天 (168h)' },
  { value: 'custom', label: '自定义起止时间' },
];

function toRfc3339Utc(date: Date): string | null {
  if (Number.isNaN(date.getTime())) return null;
  return date.toISOString().replace(/\.\d{3}Z$/, 'Z');
}

function summarizeDeliveries(event: ConversionEvent) {
  const original = event.deliveries.find((d) => d.role === 'original');
  const additional = event.deliveries.filter((d) => d.role === 'additional');
  let blockedOrFailed = 0;
  let pending = 0;
  let succeeded = 0;
  for (const item of additional) {
    if (item.state === 'succeeded') succeeded += 1;
    else if (item.state === 'blocked' || item.state === 'failed' || item.state === 'outcome_unknown') {
      blockedOrFailed += 1;
    } else if (item.state === 'pending') {
      pending += 1;
    }
  }
  return { original, additionalCount: additional.length, blockedOrFailed, pending, succeeded };
}

export function ConversionEventsTab({
  integrations,
  destinations,
  integrationMap,
  destinationByPixel,
  selectedEventId,
  refreshToken,
  initialPixelId = '',
  onClearUrlPixelId,
  serviceUnavailable,
  retryingService = false,
  showLegacyHint,
  onServiceUnavailable,
  onRetryService,
  onSwitchLegacy,
  onSelectEvent,
  onJumpToOrder,
}: {
  integrations: ConversionIntegration[];
  destinations: ConversionDestination[];
  integrationMap: Map<string, ConversionIntegration>;
  destinationByPixel: Map<string, ConversionDestination>;
  selectedEventId: string | null;
  refreshToken: number;
  initialPixelId?: string;
  onClearUrlPixelId?: () => void;
  serviceUnavailable: boolean;
  retryingService?: boolean;
  showLegacyHint: boolean;
  onServiceUnavailable: () => void;
  onRetryService: () => void;
  onSwitchLegacy: () => void;
  onSelectEvent: (ev: ConversionEvent) => void;
  onJumpToOrder: (orderId: string, integrationId?: string) => void;
}) {
  const [draft, setDraft] = useState<EventFilterState>(() => ({
    ...EMPTY_FILTER,
    pixelId: initialPixelId,
  }));
  const [committed, setCommitted] = useState<EventFilterState>(() => ({
    ...EMPTY_FILTER,
    pixelId: initialPixelId,
  }));
  const [limit, setLimit] = useState(50);
  const [cursorStack, setCursorStack] = useState<string[]>([]);
  const lastUrlPixelRef = useRef(initialPixelId);

  useEffect(() => {
    if (initialPixelId !== lastUrlPixelRef.current) {
      lastUrlPixelRef.current = initialPixelId;
      setDraft((prev) => ({ ...prev, pixelId: initialPixelId }));
      setCommitted((prev) => ({ ...prev, pixelId: initialPixelId }));
      setCursorStack([]);
    }
  }, [initialPixelId]);

  useEffect(() => {
    if (!committed.pixelId && initialPixelId) {
      lastUrlPixelRef.current = '';
      onClearUrlPixelId?.();
    }
  }, [committed.pixelId, initialPixelId, onClearUrlPixelId]);
  const [manualNonce, setManualNonce] = useState(0);
  const [timeError, setTimeError] = useState('');

  const [pageData, setPageData] = useState<ConversionEventPage>({ events: [], next_cursor: null });
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [error, setError] = useState('');

  const [density, setDensity] = useState<TableDensity>('comfortable');
  const [visibleCols, setVisibleCols] = useState<Record<EventOptionalCol, boolean>>({
    promotion_id: false,
    source_event_id: false,
    enqueued_at: false,
  });

  const requestSeqRef = useRef(0);
  const currentCursor = cursorStack.at(-1) ?? '';

  useEffect(() => {
    if (serviceUnavailable) {
      setLoading(false);
      return;
    }

    const seq = ++requestSeqRef.current;
    const controller = new AbortController();
    setLoading(true);
    setError('');

    const params = new URLSearchParams();
    params.set('limit', String(limit));
    if (currentCursor) params.set('cursor', currentCursor);

    const kw = committed.searchKeyword.trim();
    if (kw) {
      params.set(committed.searchMode, kw);
    }
    if (committed.source) params.set('source', committed.source);
    if (committed.eventName) params.set('event_name', committed.eventName);
    if (committed.deliveryState) params.set('delivery_state', committed.deliveryState);
    if (committed.integrationId) params.set('integration_id', committed.integrationId);
    if (committed.pixelId.trim()) params.set('pixel_id', committed.pixelId.trim());
    if (committed.attributionState) params.set('attribution_state', committed.attributionState);
    if (committed.partnerReportState) params.set('partner_report_state', committed.partnerReportState);
    if (committed.promotionId.trim()) params.set('promotion_id', committed.promotionId.trim());

    if (committed.timePreset === '72h') {
      const from = toRfc3339Utc(new Date(Date.now() - 72 * 3600 * 1000));
      if (from) params.set('from', from);
    } else if (committed.timePreset === '168h') {
      const from = toRfc3339Utc(new Date(Date.now() - 168 * 3600 * 1000));
      if (from) params.set('from', from);
    } else if (committed.timePreset === 'custom') {
      if (committed.fromLocal) {
        const from = toRfc3339Utc(new Date(committed.fromLocal));
        if (from) params.set('from', from);
      }
      if (committed.toLocal) {
        const to = toRfc3339Utc(new Date(committed.toLocal));
        if (to) params.set('to', to);
      }
    }

    apiRequest('GET', `/conversion/events?${params.toString()}`, undefined, false, controller.signal)
      .then((res: ConversionEventPage) => {
        if (seq !== requestSeqRef.current || controller.signal.aborted) return;
        setPageData({
          events: res.events || [],
          next_cursor: res.next_cursor ?? null,
        });
        setHasLoadedOnce(true);
      })
      .catch((err) => {
        if (seq !== requestSeqRef.current || controller.signal.aborted) return;
        if (isServiceUnavailableError(err)) {
          onServiceUnavailable();
        } else {
          setError(getErrorMessage(err, '加载回传事件列表失败'));
        }
        setHasLoadedOnce(true);
      })
      .finally(() => {
        if (seq === requestSeqRef.current && !controller.signal.aborted) {
          setLoading(false);
        }
      });

    return () => controller.abort();
  }, [committed, currentCursor, limit, refreshToken, manualNonce, serviceUnavailable, onServiceUnavailable]);

  const commitFilters = useCallback((next: EventFilterState): boolean => {
    if (next.timePreset === 'custom' && next.fromLocal && next.toLocal) {
      if (new Date(next.fromLocal).getTime() > new Date(next.toLocal).getTime()) {
        setTimeError('开始时间不能晚于结束时间');
        return false;
      }
    }
    setTimeError('');
    setCursorStack([]);
    setCommitted(next);
    return true;
  }, []);

  const handleSubmitSearch = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (commitFilters(draft)) {
      setManualNonce((n) => n + 1);
    }
  };

  const handleResetAll = useCallback(() => {
    setTimeError('');
    setDraft(EMPTY_FILTER);
    setCursorStack([]);
    setCommitted(EMPTY_FILTER);
  }, []);

  const advFilterCount =
    (committed.integrationId ? 1 : 0) +
    (committed.pixelId ? 1 : 0) +
    (committed.attributionState ? 1 : 0) +
    (committed.partnerReportState ? 1 : 0) +
    (committed.promotionId ? 1 : 0) +
    (committed.timePreset !== '24h' ? 1 : 0);

  const hasAnyFilter = Boolean(
    draft.searchKeyword ||
      committed.searchKeyword ||
      committed.source ||
      committed.eventName ||
      committed.deliveryState ||
      advFilterCount > 0,
  );

  const filterChips = useMemo<FilterChip[]>(() => {
    const chips: FilterChip[] = [];
    if (committed.searchKeyword.trim()) {
      const modeLabel =
        SEARCH_MODE_OPTIONS.find((o) => o.value === committed.searchMode)?.label ?? '精确查询';
      chips.push({
        key: 'search',
        label: `${modeLabel}: ${committed.searchKeyword.trim()}`,
        onClear: () => {
          const next = { ...draft, searchKeyword: '' };
          setDraft(next);
          commitFilters({ ...committed, searchKeyword: '' });
        },
      });
    }
    if (committed.source) {
      chips.push({
        key: 'source',
        label: `来源: ${sourceLabel(committed.source)}`,
        onClear: () => {
          setDraft((prev) => ({ ...prev, source: '' }));
          commitFilters({ ...committed, source: '' });
        },
      });
    }
    if (committed.eventName) {
      chips.push({
        key: 'eventName',
        label: `事件: ${committed.eventName}`,
        onClear: () => {
          setDraft((prev) => ({ ...prev, eventName: '' }));
          commitFilters({ ...committed, eventName: '' });
        },
      });
    }
    if (committed.deliveryState) {
      chips.push({
        key: 'deliveryState',
        label: `投递: ${status(committed.deliveryState)}`,
        onClear: () => {
          setDraft((prev) => ({ ...prev, deliveryState: '' }));
          commitFilters({ ...committed, deliveryState: '' });
        },
      });
    }
    if (committed.integrationId) {
      const integ = integrationMap.get(committed.integrationId);
      chips.push({
        key: 'integrationId',
        label: `接入: ${integ ? `${sourceLabel(integ.source)} · ${integ.account_key}` : `#${committed.integrationId}`}`,
        onClear: () => {
          setDraft((prev) => ({ ...prev, integrationId: '' }));
          commitFilters({ ...committed, integrationId: '' });
        },
      });
    }
    if (committed.pixelId.trim()) {
      chips.push({
        key: 'pixelId',
        label: `Pixel: ${committed.pixelId.trim()}`,
        onClear: () => {
          setDraft((prev) => ({ ...prev, pixelId: '' }));
          commitFilters({ ...committed, pixelId: '' });
        },
      });
    }
    if (committed.attributionState) {
      chips.push({
        key: 'attributionState',
        label: `归因: ${status(committed.attributionState)}`,
        onClear: () => {
          setDraft((prev) => ({ ...prev, attributionState: '' }));
          commitFilters({ ...committed, attributionState: '' });
        },
      });
    }
    if (committed.partnerReportState) {
      chips.push({
        key: 'partnerReportState',
        label: `分销回告: ${status(committed.partnerReportState)}`,
        onClear: () => {
          setDraft((prev) => ({ ...prev, partnerReportState: '' }));
          commitFilters({ ...committed, partnerReportState: '' });
        },
      });
    }
    if (committed.promotionId.trim()) {
      chips.push({
        key: 'promotionId',
        label: `推广链: ${committed.promotionId.trim()}`,
        onClear: () => {
          setDraft((prev) => ({ ...prev, promotionId: '' }));
          commitFilters({ ...committed, promotionId: '' });
        },
      });
    }
    if (committed.timePreset !== '24h') {
      const label =
        committed.timePreset === 'custom'
          ? `时间: ${committed.fromLocal || '不限'} ~ ${committed.toLocal || '至今'}`
          : TIME_PRESET_OPTIONS.find((o) => o.value === committed.timePreset)?.label ?? committed.timePreset;
      chips.push({
        key: 'timePreset',
        label,
        onClear: () => {
          setTimeError('');
          setDraft((prev) => ({ ...prev, timePreset: '24h', fromLocal: '', toLocal: '' }));
          commitFilters({ ...committed, timePreset: '24h', fromLocal: '', toLocal: '' });
        },
      });
    }
    return chips;
  }, [committed, draft, integrationMap, commitFilters]);

  const columnOptions: ColumnOption[] = [
    { key: 'promotion_id', label: '推广链 ID', visible: visibleCols.promotion_id },
    { key: 'source_event_id', label: '原始事件 ID', visible: visibleCols.source_event_id },
    { key: 'enqueued_at', label: '入队 / 接收时间', visible: visibleCols.enqueued_at },
  ];

  const colSpan =
    7 +
    (visibleCols.promotion_id ? 1 : 0) +
    (visibleCols.source_event_id ? 1 : 0) +
    (visibleCols.enqueued_at ? 1 : 0);

  if (serviceUnavailable) {
    return (
      <ServiceUnavailableState
        showLegacyHint={showLegacyHint}
        retrying={retryingService}
        onRetry={onRetryService}
        onSwitchLegacy={onSwitchLegacy}
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
      value: item.pixel_id,
      label: `${item.pixel_id}${item.name ? ` · ${item.name}` : ''}`,
    })),
  ];

  const isExactCrossWindowSearch = Boolean(committed.searchKeyword.trim()) && committed.timePreset === '24h';

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Unified Borderless Filter Strip */}
      <search className="block space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2.5">
          <form onSubmit={handleSubmitSearch} className="flex flex-wrap items-center gap-2 flex-1 min-w-[280px]">
            <div className="flex items-center gap-1.5 w-full sm:w-auto">
              <CustomSelect
                aria-label="选择精确检索字段"
                options={SEARCH_MODE_OPTIONS}
                value={draft.searchMode}
                onChange={(val) =>
                  setDraft((prev) => ({ ...prev, searchMode: val as SearchMode }))
                }
                width="136px"
              />
              <SearchInput
                className="flex-1 sm:w-56"
                ariaLabel="精确检索编号"
                placeholder={
                  draft.searchMode === 'order_id'
                    ? '输入订单号...'
                    : draft.searchMode === 'event_id'
                      ? '输入内部事件 ID...'
                      : '输入原始事件 ID...'
                }
                value={draft.searchKeyword}
                onChange={(val) => setDraft((prev) => ({ ...prev, searchKeyword: val }))}
                onClear={() => {
                  const next = { ...draft, searchKeyword: '' };
                  setDraft(next);
                  if (committed.searchKeyword) {
                    commitFilters(next);
                  }
                }}
              />
            </div>

            <CustomSelect
              aria-label="筛选来源平台"
              options={SOURCE_OPTIONS}
              value={committed.source}
              onChange={(val) => {
                const next = { ...draft, source: val };
                setDraft(next);
                commitFilters(next);
              }}
              width="142px"
            />

            <CustomSelect
              aria-label="筛选标准事件类型"
              options={EVENT_NAME_OPTIONS}
              value={committed.eventName}
              onChange={(val) => {
                const next = { ...draft, eventName: val };
                setDraft(next);
                commitFilters(next);
              }}
              width="176px"
            />

            <CustomSelect
              aria-label="筛选 Pixel 投递状态"
              options={DELIVERY_STATE_OPTIONS}
              value={committed.deliveryState}
              onChange={(val) => {
                const next = { ...draft, deliveryState: val };
                setDraft(next);
                commitFilters(next);
              }}
              width="168px"
            />

            <ToolbarPopover
              align="start"
              label="更多筛选"
              icon={<SlidersHorizontal size={13} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />}
              badgeCount={advFilterCount}
              panelClassName="w-[340px] p-4"
            >
              {({ close }) => (
                <div className="space-y-3">
                  <div className="font-semibold text-[var(--cms-fg)]">归因、路由与时间筛选</div>

                  <label className="block space-y-1">
                    <span className="block text-[var(--cms-muted-fg)]">接入账户</span>
                    <CustomSelect
                      aria-label="筛选接入账户"
                      width="100%"
                      options={integrationOptions}
                      value={draft.integrationId}
                      onChange={(val) => setDraft((prev) => ({ ...prev, integrationId: val }))}
                    />
                  </label>

                  <label className="block space-y-1">
                    <span className="block text-[var(--cms-muted-fg)]">目标 Pixel</span>
                    <CustomSelect
                      aria-label="筛选目标 Pixel"
                      width="100%"
                      options={destinationOptions}
                      value={draft.pixelId}
                      onChange={(val) => setDraft((prev) => ({ ...prev, pixelId: val }))}
                    />
                  </label>

                  <div className="grid grid-cols-2 gap-2.5">
                    <label className="block space-y-1">
                      <span className="block text-[var(--cms-muted-fg)]">归因状态</span>
                      <CustomSelect
                        aria-label="筛选归因状态"
                        width="100%"
                        options={ATTRIBUTION_OPTIONS}
                        value={draft.attributionState}
                        onChange={(val) => setDraft((prev) => ({ ...prev, attributionState: val }))}
                      />
                    </label>
                    <label className="block space-y-1">
                      <span className="block text-[var(--cms-muted-fg)]">分销回告</span>
                      <CustomSelect
                        aria-label="筛选分销回告状态"
                        width="100%"
                        options={PARTNER_REPORT_STATE_OPTIONS}
                        value={draft.partnerReportState}
                        onChange={(val) =>
                          setDraft((prev) => ({ ...prev, partnerReportState: val }))
                        }
                      />
                    </label>
                  </div>

                  <label className="block space-y-1">
                    <span className="block text-[var(--cms-muted-fg)]">推广链 ID</span>
                    <input
                      type="text"
                      className="input-field font-mono-code"
                      placeholder="输入 promotion_id..."
                      value={draft.promotionId}
                      onChange={(e) => setDraft((prev) => ({ ...prev, promotionId: e.target.value }))}
                    />
                  </label>

                  <label className="block space-y-1">
                    <span className="block text-[var(--cms-muted-fg)]">时间范围</span>
                    <CustomSelect
                      aria-label="选择时间范围"
                      width="100%"
                      options={TIME_PRESET_OPTIONS}
                      value={draft.timePreset}
                      onChange={(val) => {
                        setTimeError('');
                        setDraft((prev) => ({ ...prev, timePreset: val as TimePreset }));
                      }}
                    />
                  </label>

                  {draft.timePreset === 'custom' && (
                    <div className="grid grid-cols-1 gap-2">
                      <label className="block space-y-1">
                        <span className="block text-[var(--cms-muted-fg)]">起始时间</span>
                        <input
                          type="datetime-local"
                          className="input-field"
                          value={draft.fromLocal}
                          onChange={(e) => {
                            setTimeError('');
                            setDraft((prev) => ({ ...prev, fromLocal: e.target.value }));
                          }}
                        />
                      </label>
                      <label className="block space-y-1">
                        <span className="block text-[var(--cms-muted-fg)]">结束时间</span>
                        <input
                          type="datetime-local"
                          className="input-field"
                          value={draft.toLocal}
                          onChange={(e) => {
                            setTimeError('');
                            setDraft((prev) => ({ ...prev, toLocal: e.target.value }));
                          }}
                        />
                      </label>
                    </div>
                  )}

                  {timeError && (
                    <p role="alert" className="text-xs text-[var(--cms-error)] m-0">
                      {timeError}
                    </p>
                  )}

                  <div className="pt-2 border-t border-[var(--cms-border)] flex items-center justify-end gap-2">
                    <button
                      type="button"
                      onClick={() => {
                        const cleared: EventFilterState = {
                          ...draft,
                          integrationId: '',
                          pixelId: '',
                          attributionState: '',
                          partnerReportState: '',
                          promotionId: '',
                          timePreset: '24h',
                          fromLocal: '',
                          toLocal: '',
                        };
                        setTimeError('');
                        setDraft(cleared);
                        commitFilters(cleared);
                        close();
                      }}
                      className="btn-ghost text-[var(--cms-muted-fg)]"
                    >
                      清空高级项
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        if (commitFilters(draft)) {
                          close();
                        }
                      }}
                      className="btn-primary h-8 px-3 text-xs"
                    >
                      应用筛选
                    </button>
                  </div>
                </div>
              )}
            </ToolbarPopover>

            <button type="submit" disabled={loading} className="btn-secondary h-9 px-3 text-xs font-normal">
              查询
            </button>

            {hasAnyFilter && (
              <button
                type="button"
                onClick={handleResetAll}
                className="btn-ghost h-9 text-xs text-[var(--cms-muted-fg)]"
              >
                重置
              </button>
            )}
          </form>

          <div className="flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
            <button
              type="button"
              disabled={loading}
              onClick={() => setManualNonce((n) => n + 1)}
              className="btn-ghost h-9 px-2.5 text-xs"
              title="刷新列表"
            >
              <RefreshCw size={13} className={loading ? 'animate-spin' : ''} aria-hidden="true" />
              <span>刷新</span>
            </button>
            <ViewOptionsPopover
              density={density}
              onDensityChange={setDensity}
              columns={columnOptions}
              onToggleColumn={(key) =>
                setVisibleCols((prev) => ({
                  ...prev,
                  [key as EventOptionalCol]: !prev[key as EventOptionalCol],
                }))
              }
            />
          </div>
        </div>

        <FilterChipsBar chips={filterChips} onResetAll={handleResetAll} />
      </search>

      {error && (
        <InlineErrorBanner message={error} onRetry={() => setManualNonce((n) => n + 1)} />
      )}

      {/* Main Events Table + External Cursor Pagination */}
      <TableRegion
        ariaLabel="统一转化回传事件列表"
        footer={
          <div className="pt-0.5 flex flex-wrap items-center justify-between gap-3 text-xs text-[var(--cms-muted-fg)]">
            <div className="flex flex-wrap items-center gap-2 tabular-nums">
              <span>
                第 <strong className="text-[var(--cms-fg)] font-medium">{cursorStack.length + 1}</strong> 页 · 本页{' '}
                <strong className="text-[var(--cms-fg)] font-medium">{pageData.events.length}</strong> 条
              </span>
              <span className="opacity-50">·</span>
              <span>
                {isExactCrossWindowSearch
                  ? '精确编号检索（不限时间窗口）'
                  : committed.timePreset === '24h'
                    ? '默认展示最近 24 小时事件'
                    : '按所选时间窗口过滤'}
              </span>
            </div>

            <div className="flex flex-wrap items-center gap-2.5">
              <label className="inline-flex items-center gap-1.5">
                <span>每页</span>
                <select
                  aria-label="每页显示条数"
                  disabled={loading}
                  value={limit}
                  onChange={(e) => {
                    const nextLimit = Number(e.target.value);
                    if (nextLimit > 0 && nextLimit <= 100) {
                      setCursorStack([]);
                      setLimit(nextLimit);
                    }
                  }}
                  className="focus-ring h-7 px-2 rounded-lg border border-[var(--cms-border)] bg-[var(--cms-card)] text-xs text-[var(--cms-fg)] tabular-nums cursor-pointer disabled:opacity-40"
                >
                  {[20, 50, 100].map((size) => (
                    <option key={size} value={size}>
                      {size} 条
                    </option>
                  ))}
                </select>
              </label>

              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  disabled={loading || cursorStack.length === 0}
                  onClick={() => setCursorStack([])}
                  className="btn-secondary h-7 px-2.5 text-xs"
                >
                  首页
                </button>
                <button
                  type="button"
                  disabled={loading || cursorStack.length === 0}
                  onClick={() => setCursorStack((prev) => prev.slice(0, -1))}
                  className="btn-secondary h-7 px-2.5 text-xs"
                >
                  上一页
                </button>
                <button
                  type="button"
                  disabled={loading || !pageData.next_cursor}
                  onClick={() => {
                    if (pageData.next_cursor) {
                      setCursorStack((prev) => [...prev, pageData.next_cursor!]);
                    }
                  }}
                  className="btn-secondary h-7 px-2.5 text-xs"
                >
                  下一页
                </button>
              </div>
            </div>
          </div>
        }
      >
        <table className="cms-table min-w-[1040px]" data-density={density}>
          <thead>
            <tr>
              <th scope="col">事件 / ID</th>
              <th scope="col">来源 / 接入账户</th>
              <th scope="col">订单 / 金额</th>
              <th scope="col">归因状态</th>
              <th scope="col">Pixel 投递与回告概览</th>
              {visibleCols.promotion_id && <th scope="col">推广链 ID</th>}
              {visibleCols.source_event_id && <th scope="col">原始事件 ID</th>}
              {visibleCols.enqueued_at && <th scope="col">入队时间</th>}
              <th scope="col">发生 / 创建时间</th>
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={6} cols={colSpan} />
            ) : pageData.events.length === 0 ? (
              <tr>
                <td colSpan={colSpan} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  {error
                    ? '加载回传事件列表失败，请点击上方「重试」按钮重新加载'
                    : '未检索到符合条件的回传事件（当前默认仅查询最近 24 小时，可展开「更多筛选」调整时间范围）'}
                </td>
              </tr>
            ) : (
              pageData.events.map((item) => {
                const integ = integrationMap.get(item.integration_id);
                const summary = summarizeDeliveries(item);
                const amountText = formatAmount(item.amount, item.currency);
                const eventReason = getReasonLabel(item.reason);
                const origDest = summary.original
                  ? destinationByPixel.get(summary.original.pixel_id)
                  : undefined;

                return (
                  <tr
                    key={item.id}
                    onClick={() => onSelectEvent(item)}
                    className={`cursor-pointer ${selectedEventId === item.id ? 'row-selected' : ''}`}
                  >
                    <td>
                      <div className="space-y-0.5">
                        <div className="flex items-center gap-1.5">
                          <CopyableText
                            value={item.id}
                            displayValue={`#${item.id}`}
                            label="事件 ID"
                            mono={false}
                            className="text-[var(--cms-muted-fg)]"
                          />
                          <span className="font-medium text-[var(--cms-fg)]">
                            {item.source === 'sinan'
                              ? sourceEventLabel(item.source, item.source_event_type)
                              : item.event_name}
                          </span>
                        </div>
                        {item.source === 'sinan' && (
                          <div className="text-[11px] text-[var(--cms-muted-fg)]">{item.event_name}</div>
                        )}
                      </div>
                    </td>

                    <td>
                      <div className="space-y-1">
                        <span
                          className={`badge ${
                            item.source === 'sinan' ? 'badge-purple' : 'badge-blue'
                          }`}
                        >
                          {sourceLabel(item.source)}
                        </span>
                        <div className="text-[11px] text-[var(--cms-muted-fg)] truncate max-w-[140px]">
                          {integ ? `${integ.account_key} (#${item.integration_id})` : `#${item.integration_id}`}
                        </div>
                      </div>
                    </td>

                    <td>
                      <div className="space-y-0.5">
                        {item.order_id ? (
                          <button
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              onJumpToOrder(
                                item.order_id!,
                                item.source === 'starnovel' ? '' : item.integration_id,
                              );
                            }}
                            className="focus-ring inline-flex items-center gap-1 text-[var(--cms-primary)] hover:underline cursor-pointer font-mono-code"
                            title="前往逐单对账"
                          >
                            <span className="truncate max-w-[140px]">{item.order_id}</span>
                            <ExternalLink size={10} aria-hidden="true" />
                          </button>
                        ) : (
                          <span className="text-[var(--cms-muted-fg)]">—</span>
                        )}
                        {amountText && (
                          <div className="text-[11px] font-medium tabular-nums text-[var(--cms-fg)]">
                            {amountText}
                          </div>
                        )}
                      </div>
                    </td>

                    <td>
                      <div className="space-y-0.5">
                        <ConversionStatusDot code={item.attribution_state} />
                        {eventReason && (
                          <div
                            className="text-[11px] text-[var(--cms-muted-fg)] truncate max-w-[170px]"
                            title={eventReason}
                          >
                            {eventReason}
                          </div>
                        )}
                      </div>
                    </td>

                    <td>
                      <div className="space-y-1">
                        {item.deliveries.length === 0 ? (
                          <span className="text-[var(--cms-muted-fg)]">无投递任务</span>
                        ) : (
                          <>
                            {summary.original && (
                              <div className="flex flex-wrap items-center gap-1.5">
                                <span
                                  className="font-mono-code text-[var(--cms-fg)]"
                                  title={origDest?.name}
                                >
                                  {summary.original.pixel_id}
                                </span>
                                <ConversionStatusDot code={summary.original.state} />
                                {summary.original.reason && (
                                  <span
                                    className="text-[11px] text-[var(--cms-muted-fg)] truncate max-w-[150px]"
                                    title={getReasonLabel(summary.original.reason)}
                                  >
                                    · {getReasonLabel(summary.original.reason)}
                                  </span>
                                )}
                              </div>
                            )}

                            {summary.additionalCount > 0 && (
                              <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
                                <span className="badge badge-gray">
                                  +{summary.additionalCount} 附加目标
                                </span>
                                {summary.blockedOrFailed > 0 ? (
                                  <span className="text-[var(--cms-warning)]">
                                    {summary.blockedOrFailed} 个异常
                                  </span>
                                ) : summary.pending > 0 ? (
                                  <span className="text-[var(--cms-primary)]">
                                    {summary.pending} 个待发
                                  </span>
                                ) : (
                                  <span className="text-[var(--cms-success)]">
                                    {summary.succeeded} 个已确认
                                  </span>
                                )}
                              </div>
                            )}
                          </>
                        )}

                        {item.partner_report && (
                          <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
                            <span className="text-[var(--cms-muted-fg)]">回告:</span>
                            <span
                              className={`badge ${
                                item.partner_report.state === 'confirmed'
                                  ? 'badge-green'
                                  : item.partner_report.state === 'blocked' ||
                                      item.partner_report.state === 'outcome_unknown'
                                    ? 'badge-amber'
                                    : 'badge-secondary'
                              }`}
                            >
                              {partnerReportStatusLabel(item.partner_report.status)} ·{' '}
                              {status(item.partner_report.state)}
                            </span>
                          </div>
                        )}
                      </div>
                    </td>

                    {visibleCols.promotion_id && (
                      <td className="font-mono-code text-[var(--cms-muted-fg)] whitespace-nowrap">
                        {item.promotion_id || '—'}
                      </td>
                    )}

                    {visibleCols.source_event_id && (
                      <td className="max-w-[180px]">
                        <CopyableText value={item.source_event_id} label="原始事件 ID" />
                      </td>
                    )}

                    {visibleCols.enqueued_at && (
                      <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                        {timestamp(item.enqueued_at)}
                      </td>
                    )}

                    <td className="tabular-nums whitespace-nowrap">
                      <div className="text-[var(--cms-fg)]">{timestamp(item.occurred_at)}</div>
                      <div className="text-[11px] text-[var(--cms-muted-fg)]">
                        入库 {timestamp(item.created_at)}
                      </div>
                    </td>

                    <td className="text-right whitespace-nowrap" onClick={(e) => e.stopPropagation()}>
                      <button
                        type="button"
                        onClick={() => onSelectEvent(item)}
                        className="btn-detail"
                      >
                        详情
                      </button>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </TableRegion>
    </div>
  );
}

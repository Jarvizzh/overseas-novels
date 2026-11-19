import { type KeyboardEvent, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Activity, FileSearch, History, ListChecks } from 'lucide-react';
import { apiRequest } from '../utils/api';
import { getErrorMessage, isServiceUnavailableError } from '../utils/conversionStatus';
import { InlineErrorBanner } from '../components/conversion/ConversionShared';
import { ConversionEventDrawer } from '../components/conversion/ConversionEventDrawer';
import { ConversionEventsTab } from '../components/conversion/ConversionEventsTab';
import { ConversionOrdersTab } from '../components/conversion/ConversionOrdersTab';
import { ConversionSummaryTab } from '../components/conversion/ConversionSummaryTab';
import { LegacyTrackingLogsTab } from '../components/conversion/LegacyTrackingLogsTab';
import type {
  ConversionDestination,
  ConversionEvent,
  ConversionIntegration,
  ConversionSummaryData,
  ConversionTabKey,
} from '../components/conversion/types';

function resolveTabKey(raw: string | null, showHistory: boolean): ConversionTabKey {
  if (raw === 'orders' || raw === 'summary') return raw;
  if (raw === 'legacy' && showHistory) return 'legacy';
  return 'events';
}

export default function ConversionCenterTab({
  showHistory,
  canWrite,
}: {
  showHistory: boolean;
  canWrite: boolean;
}) {
  const baseId = useId();
  const [searchParams, setSearchParams] = useSearchParams();
  const tabListRef = useRef<HTMLDivElement>(null);
  const tabButtonRefs = useRef<Partial<Record<ConversionTabKey, HTMLButtonElement | null>>>({});

  const activeTab = resolveTabKey(searchParams.get('tab'), showHistory);
  const urlEventId = searchParams.get('event_id');
  const urlOrderId = searchParams.get('order_id');
  const urlIntegrationId = searchParams.get('integration_id') ?? '';
  const urlPixelId = searchParams.get('pixel_id') ?? '';

  const [integrations, setIntegrations] = useState<ConversionIntegration[]>([]);
  const [destinations, setDestinations] = useState<ConversionDestination[]>([]);
  const [alertsCount, setAlertsCount] = useState<number>(0);
  const [serviceUnavailable, setServiceUnavailable] = useState(false);
  const [metaLoading, setMetaLoading] = useState(true);
  const [metaError, setMetaError] = useState('');
  const [metaNonce, setMetaNonce] = useState(0);

  const [drawerEventId, setDrawerEventId] = useState<string | null>(urlEventId);
  const [drawerInitialSummary, setDrawerInitialSummary] = useState<ConversionEvent | null>(null);
  const [eventsRefreshToken, setEventsRefreshToken] = useState(0);

  const lastOrderUrlRef = useRef<{ orderId: string; integrationId: string } | null>(
    urlOrderId ? { orderId: urlOrderId, integrationId: urlIntegrationId } : null,
  );
  const [orderLookupTrigger, setOrderLookupTrigger] = useState<{
    orderId: string;
    integrationId: string;
    nonce: number;
  } | null>(() =>
    urlOrderId ? { orderId: urlOrderId, integrationId: urlIntegrationId, nonce: 1 } : null,
  );

  // Sync URL ?event_id= and ?order_id= changes into view state
  useEffect(() => {
    if (urlEventId) {
      setDrawerEventId(urlEventId);
    }
  }, [urlEventId]);

  useEffect(() => {
    if (!urlOrderId) {
      lastOrderUrlRef.current = null;
      return;
    }
    if (
      lastOrderUrlRef.current?.orderId === urlOrderId &&
      lastOrderUrlRef.current?.integrationId === urlIntegrationId
    ) {
      return;
    }
    lastOrderUrlRef.current = { orderId: urlOrderId, integrationId: urlIntegrationId };
    setOrderLookupTrigger((prev) => ({
      orderId: urlOrderId,
      integrationId: urlIntegrationId,
      nonce: (prev?.nonce ?? 0) + 1,
    }));
  }, [urlOrderId, urlIntegrationId]);

  // Normalize unauthorized ?tab=legacy back to events
  useEffect(() => {
    if (searchParams.get('tab') === 'legacy' && !showHistory) {
      const next = new URLSearchParams(searchParams);
      next.delete('tab');
      setSearchParams(next, { replace: true });
    }
  }, [searchParams, setSearchParams, showHistory]);

  // Load shared lookup metadata (integrations, destinations) and initial active alert badge count
  useEffect(() => {
    const controller = new AbortController();
    setMetaLoading(true);
    setMetaError('');
    Promise.allSettled([
      apiRequest('GET', '/conversion/integrations', undefined, false, controller.signal),
      apiRequest('GET', '/conversion/destinations', undefined, false, controller.signal),
      apiRequest('GET', '/conversion/summary?hours=24', undefined, false, controller.signal),
    ]).then(([integRes, destRes, sumRes]) => {
      if (controller.signal.aborted) return;
      setMetaLoading(false);

      let unavailableCount = 0;
      const nonServiceErrors: string[] = [];

      if (integRes.status === 'fulfilled') {
        setIntegrations(integRes.value || []);
      } else if (isServiceUnavailableError(integRes.reason)) {
        unavailableCount += 1;
      } else {
        nonServiceErrors.push(getErrorMessage(integRes.reason, '接入配置'));
      }

      if (destRes.status === 'fulfilled') {
        setDestinations(destRes.value || []);
      } else if (isServiceUnavailableError(destRes.reason)) {
        unavailableCount += 1;
      } else {
        nonServiceErrors.push(getErrorMessage(destRes.reason, 'Pixel 目标列表'));
      }

      if (sumRes.status === 'fulfilled') {
        const sumData = sumRes.value as ConversionSummaryData;
        setAlertsCount(sumData?.alerts?.length ?? 0);
      } else if (isServiceUnavailableError(sumRes.reason)) {
        unavailableCount += 1;
      }

      if (unavailableCount > 0) {
        setServiceUnavailable(true);
        setMetaError('');
      } else {
        setServiceUnavailable(false);
        if (nonServiceErrors.length > 0) {
          setMetaError(`基础配置元数据加载不完整：${nonServiceErrors.join('；')}`);
        }
      }
    });

    return () => controller.abort();
  }, [metaNonce]);

  const integrationMap = useMemo(() => {
    const map = new Map<string, ConversionIntegration>();
    for (const item of integrations) {
      map.set(item.id, item);
    }
    return map;
  }, [integrations]);

  const destinationByPixel = useMemo(() => {
    const map = new Map<string, ConversionDestination>();
    for (const item of destinations) {
      map.set(item.pixel_id, item);
    }
    return map;
  }, [destinations]);

  const destinationById = useMemo(() => {
    const map = new Map<string, ConversionDestination>();
    for (const item of destinations) {
      map.set(item.id, item);
    }
    return map;
  }, [destinations]);

  const switchTab = useCallback(
    (nextTab: ConversionTabKey) => {
      const next = new URLSearchParams(searchParams);
      if (nextTab === 'events') {
        next.delete('tab');
      } else {
        next.set('tab', nextTab);
      }
      if (nextTab !== 'orders') {
        next.delete('order_id');
        next.delete('integration_id');
      }
      setSearchParams(next, { replace: true });
      tabButtonRefs.current[nextTab]?.scrollIntoView({
        block: 'nearest',
        inline: 'nearest',
      });
    },
    [searchParams, setSearchParams],
  );

  const handleOpenDrawerFromList = useCallback((ev: ConversionEvent) => {
    setDrawerInitialSummary(ev);
    setDrawerEventId(ev.id);
  }, []);

  const handleOpenDrawerById = useCallback((eventId: string) => {
    setDrawerInitialSummary(null);
    setDrawerEventId(eventId);
  }, []);

  const handleCloseDrawer = useCallback(() => {
    setDrawerEventId(null);
    setDrawerInitialSummary(null);
    if (searchParams.has('event_id')) {
      const next = new URLSearchParams(searchParams);
      next.delete('event_id');
      setSearchParams(next, { replace: true });
    }
  }, [searchParams, setSearchParams]);

  const handleJumpToOrder = useCallback(
    (orderId: string, integrationId = '') => {
      const next = new URLSearchParams();
      next.set('tab', 'orders');
      next.set('order_id', orderId);
      if (integrationId) {
        next.set('integration_id', integrationId);
      }
      lastOrderUrlRef.current = { orderId, integrationId };
      setOrderLookupTrigger((prev) => ({
        orderId,
        integrationId,
        nonce: (prev?.nonce ?? 0) + 1,
      }));
      setSearchParams(next, { replace: true });
    },
    [setSearchParams],
  );

  const handleRetryService = useCallback(() => {
    setServiceUnavailable(false);
    setMetaError('');
    setMetaNonce((n) => n + 1);
  }, []);

  const handleClearUrlPixelId = useCallback(() => {
    if (!searchParams.has('pixel_id')) return;
    const next = new URLSearchParams(searchParams);
    next.delete('pixel_id');
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams]);

  const visibleTabs = useMemo(() => {
    const base: Array<{ key: ConversionTabKey; label: string }> = [
      { key: 'events', label: '回传事件' },
      { key: 'orders', label: '逐单对账' },
      { key: 'summary', label: '监控与告警' },
    ];
    if (showHistory) {
      base.push({ key: 'legacy', label: '回传日志' });
    }
    return base;
  }, [showHistory]);

  const handleTabListKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const keys = visibleTabs.map((t) => t.key);
    const focusedIdx = keys.findIndex((k) => tabButtonRefs.current[k] === document.activeElement);
    if (focusedIdx < 0) return;

    let nextIdx = -1;
    if (e.key === 'ArrowRight') nextIdx = (focusedIdx + 1) % keys.length;
    else if (e.key === 'ArrowLeft') nextIdx = (focusedIdx - 1 + keys.length) % keys.length;
    else if (e.key === 'Home') nextIdx = 0;
    else if (e.key === 'End') nextIdx = keys.length - 1;

    if (nextIdx >= 0) {
      e.preventDefault();
      const targetBtn = tabButtonRefs.current[keys[nextIdx]];
      targetBtn?.focus();
      targetBtn?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    }
  };

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Top Underlined Navigation Tabs */}
      <div className="border-b border-[var(--cms-border)] flex items-center justify-between gap-3">
        <div
          ref={tabListRef}
          role="tablist"
          aria-label="回传中心功能视图"
          onKeyDown={handleTabListKeyDown}
          className="flex items-center gap-6 overflow-x-auto h5-no-scrollbar"
        >
          {visibleTabs.map((tab) => {
            const isSelected = activeTab === tab.key;
            return (
              <button
                key={tab.key}
                ref={(el) => {
                  tabButtonRefs.current[tab.key] = el;
                }}
                id={`${baseId}-tab-${tab.key}`}
                type="button"
                role="tab"
                tabIndex={isSelected ? 0 : -1}
                aria-selected={isSelected}
                onClick={() => switchTab(tab.key)}
                className={`focus-ring shrink-0 pb-2.5 text-sm font-medium whitespace-nowrap cursor-pointer inline-flex items-center gap-2 transition-colors ${
                  isSelected
                    ? 'text-[var(--cms-fg)]'
                    : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                }`}
              >
                {tab.key === 'events' && <ListChecks size={14} aria-hidden="true" />}
                {tab.key === 'orders' && <FileSearch size={14} aria-hidden="true" />}
                {tab.key === 'summary' && <Activity size={14} aria-hidden="true" />}
                {tab.key === 'legacy' && <History size={14} aria-hidden="true" />}
                <span>{tab.label}</span>
                {tab.key === 'summary' && alertsCount > 0 && !serviceUnavailable && (
                  <span
                    className="min-w-[18px] h-[18px] px-1.5 rounded-full text-[11px] font-semibold tabular-nums inline-flex items-center justify-center leading-none"
                    style={{
                      backgroundColor: 'color-mix(in srgb, var(--cms-warning) 16%, transparent)',
                      color: 'var(--cms-warning)',
                    }}
                  >
                    {alertsCount}
                  </span>
                )}
              </button>
            );
          })}
        </div>
      </div>

      {/* Active Tab Panel */}
      <div role="tabpanel" aria-labelledby={`${baseId}-tab-${activeTab}`} className="space-y-4">
        {metaError && !serviceUnavailable && (
          <InlineErrorBanner message={metaError} onRetry={handleRetryService} />
        )}

        {activeTab === 'events' && (
          <ConversionEventsTab
            integrations={integrations}
            destinations={destinations}
            integrationMap={integrationMap}
            destinationByPixel={destinationByPixel}
            initialPixelId={urlPixelId}
            onClearUrlPixelId={handleClearUrlPixelId}
            selectedEventId={drawerEventId}
            refreshToken={eventsRefreshToken}
            serviceUnavailable={serviceUnavailable}
            retryingService={metaLoading}
            showLegacyHint={showHistory}
            onServiceUnavailable={() => setServiceUnavailable(true)}
            onRetryService={handleRetryService}
            onSwitchLegacy={() => switchTab('legacy')}
            onSelectEvent={handleOpenDrawerFromList}
            onJumpToOrder={handleJumpToOrder}
          />
        )}

        {activeTab === 'orders' && (
          <ConversionOrdersTab
            integrations={integrations}
            destinationByPixel={destinationByPixel}
            initialQuery={orderLookupTrigger}
            serviceUnavailable={serviceUnavailable}
            retryingService={metaLoading}
            showLegacyHint={showHistory}
            onServiceUnavailable={() => setServiceUnavailable(true)}
            onRetryService={handleRetryService}
            onSwitchLegacy={() => switchTab('legacy')}
            onOpenEventDrawer={handleOpenDrawerById}
          />
        )}

        {activeTab === 'summary' && (
          <ConversionSummaryTab
            integrations={integrations}
            destinations={destinations}
            integrationMap={integrationMap}
            destinationById={destinationById}
            serviceUnavailable={serviceUnavailable}
            retryingService={metaLoading}
            showLegacyHint={showHistory}
            onServiceUnavailable={() => setServiceUnavailable(true)}
            onRetryService={handleRetryService}
            onSwitchLegacy={() => switchTab('legacy')}
            onOpenEventDrawer={handleOpenDrawerById}
            onAlertsCountChange={setAlertsCount}
          />
        )}

        {activeTab === 'legacy' && showHistory && <LegacyTrackingLogsTab />}
      </div>

      {/* Shared Event Detail Drawer */}
      <ConversionEventDrawer
        eventId={drawerEventId}
        initialSummary={drawerInitialSummary}
        canWrite={canWrite}
        integrationMap={integrationMap}
        destinationByPixel={destinationByPixel}
        onClose={handleCloseDrawer}
        onChanged={() => setEventsRefreshToken((n) => n + 1)}
        onJumpToOrder={handleJumpToOrder}
      />
    </div>
  );
}

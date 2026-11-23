import { type KeyboardEvent, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Activity, FileSearch, ListChecks } from 'lucide-react';
import { apiRequest } from '../utils/api';
import { getErrorMessage } from '../utils/conversionStatus';
import { InlineErrorBanner } from '../components/conversion/ConversionShared';
import { ConversionEventDrawer } from '../components/conversion/ConversionEventDrawer';
import { ConversionEventsTab } from '../components/conversion/ConversionEventsTab';
import { ConversionOrdersTab } from '../components/conversion/ConversionOrdersTab';
import { ConversionSummaryTab } from '../components/conversion/ConversionSummaryTab';
import type {
  ConversionDestination,
  ConversionEvent,
  ConversionIntegration,
  ConversionSummaryData,
  ConversionTabKey,
} from '../components/conversion/types';

const conversionTabs: Array<{ key: ConversionTabKey; label: string }> = [
  { key: 'events', label: '回传事件' },
  { key: 'orders', label: '逐单对账' },
  { key: 'summary', label: '监控与告警' },
];

function resolveTabKey(raw: string | null): ConversionTabKey {
  if (raw === 'orders' || raw === 'summary') return raw;
  return 'events';
}

export default function ConversionCenterTab({ canWrite }: { canWrite: boolean }) {
  const baseId = useId();
  const [searchParams, setSearchParams] = useSearchParams();
  const tabListRef = useRef<HTMLDivElement>(null);
  const tabButtonRefs = useRef<Partial<Record<ConversionTabKey, HTMLButtonElement | null>>>({});

  const activeTab = resolveTabKey(searchParams.get('tab'));
  const urlEventId = searchParams.get('event_id');
  const urlOrderId = searchParams.get('order_id');
  const urlIntegrationId = searchParams.get('integration_id') ?? '';
  const urlPixelId = searchParams.get('pixel_id') ?? '';

  const [integrations, setIntegrations] = useState<ConversionIntegration[]>([]);
  const [destinations, setDestinations] = useState<ConversionDestination[]>([]);
  const [alertsCount, setAlertsCount] = useState<number | null>(0);
  const [metaError, setMetaError] = useState('');
  const [metaNonce, setMetaNonce] = useState(0);

  const [drawerEventId, setDrawerEventId] = useState<string | null>(urlEventId);
  const [drawerPixel, setDrawerPixel] = useState(urlEventId ? urlPixelId : '');
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

  useEffect(() => {
    const controller = new AbortController();
    setMetaError('');
    Promise.allSettled([
      apiRequest('GET', '/conversion/integrations', undefined, false, controller.signal),
      apiRequest('GET', '/conversion/destinations', undefined, false, controller.signal),
    ]).then(([integrationResult, destinationResult]) => {
      if (controller.signal.aborted) return;
      const errors: string[] = [];
      if (integrationResult.status === 'fulfilled') {
        setIntegrations(integrationResult.value || []);
      } else {
        errors.push(getErrorMessage(integrationResult.reason, '加载接入配置'));
      }
      if (destinationResult.status === 'fulfilled') {
        setDestinations(destinationResult.value || []);
      } else {
        errors.push(getErrorMessage(destinationResult.reason, '加载 Pixel 目标列表'));
      }
      setMetaError(errors.join('；'));
    });
    return () => controller.abort();
  }, [metaNonce]);

  useEffect(() => {
    if (activeTab === 'summary') return;
    const controller = new AbortController();
    apiRequest('GET', '/conversion/summary?hours=24', undefined, false, controller.signal)
      .then((summary: ConversionSummaryData) => {
        if (!controller.signal.aborted) {
          setAlertsCount(summary.collector?.state === 'fresh' ? summary.alerts.length : null);
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) setAlertsCount(null);
      });
    return () => controller.abort();
  }, [activeTab]);

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

  const handleOpenDrawerFromList = useCallback((ev: ConversionEvent, pixel: string) => {
    setDrawerPixel(pixel);
    setDrawerInitialSummary(ev);
    setDrawerEventId(ev.id);
  }, []);

  const handleOpenDrawerById = useCallback((eventId: string) => {
    setDrawerPixel('');
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
    setMetaError('');
    setMetaNonce((n) => n + 1);
  }, []);

  const handleClearUrlPixelId = useCallback(() => {
    if (!searchParams.has('pixel_id')) return;
    const next = new URLSearchParams(searchParams);
    next.delete('pixel_id');
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams]);

  const handleTabListKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const keys = conversionTabs.map((t) => t.key);
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
          {conversionTabs.map((tab) => {
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
                <span>{tab.label}</span>
                {tab.key === 'summary' && alertsCount === null && (
                  <span className="text-[11px]">告警数未知</span>
                )}
                {tab.key === 'summary' && alertsCount !== null && alertsCount > 0 && (
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
        {metaError && <InlineErrorBanner message={metaError} onRetry={handleRetryService} />}

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
            onSelectEvent={handleOpenDrawerFromList}
            onJumpToOrder={handleJumpToOrder}
          />
        )}

        {activeTab === 'orders' && (
          <ConversionOrdersTab
            integrations={integrations}
            destinationByPixel={destinationByPixel}
            initialQuery={orderLookupTrigger}
            onOpenEventDrawer={handleOpenDrawerById}
          />
        )}

        {activeTab === 'summary' && (
          <ConversionSummaryTab
            integrations={integrations}
            destinations={destinations}
            integrationMap={integrationMap}
            destinationById={destinationById}
            onOpenEventDrawer={handleOpenDrawerById}
            onAlertsCountChange={setAlertsCount}
          />
        )}

      </div>

      {/* Shared Event Detail Drawer */}
      <ConversionEventDrawer
        eventId={drawerEventId}
        initialSummary={drawerInitialSummary}
        initialPixelId={drawerPixel}
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

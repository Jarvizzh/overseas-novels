import { type KeyboardEvent, useCallback, useEffect, useId, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { ExternalLink, Layers, Radio } from 'lucide-react';
import { usePermission } from '../context/PermissionContext';
import { apiRequest } from '../utils/api';
import { getErrorMessage, isServiceUnavailableError } from '../utils/conversionStatus';
import { ConversionIntegrationsPanel } from '../components/conversion/ConversionIntegrationsPanel';
import { ConversionPixelsPanel } from '../components/conversion/ConversionPixelsPanel';
import { InlineErrorBanner, ServiceUnavailableState } from '../components/conversion/ConversionShared';
import type {
  ConversionDestination,
  ConversionIntegration,
  PixelsTabKey,
} from '../components/conversion/types';

function resolveTabKey(raw: string | null): PixelsTabKey {
  return raw === 'integrations' ? 'integrations' : 'pixels';
}

export default function PixelsTab() {
  const { canRead, canWrite } = usePermission();
  const canWriteIntegration = canWrite('ads.pixels');
  const canReadPolicy = canRead('ads.conversions');
  const canWritePolicy = canWrite('ads.conversions');
  const canReadAudit = canRead('ads.conversions');

  const headingId = useId();
  const tabsId = useId();
  const [searchParams, setSearchParams] = useSearchParams();
  const activeTab = resolveTabKey(searchParams.get('tab'));

  const [destinations, setDestinations] = useState<ConversionDestination[]>([]);
  const [integrations, setIntegrations] = useState<ConversionIntegration[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [error, setError] = useState('');
  const [serviceUnavailable, setServiceUnavailable] = useState(false);

  const tabRefs = useRef<Partial<Record<PixelsTabKey, HTMLButtonElement | null>>>({});
  const reqSeqRef = useRef(0);
  const activeAbortRef = useRef<AbortController | null>(null);

  const loadData = useCallback(async () => {
    activeAbortRef.current?.abort();
    const controller = new AbortController();
    activeAbortRef.current = controller;
    const seq = ++reqSeqRef.current;

    setLoading(true);
    setError('');

    try {
      const [destRes, integRes] = await Promise.all([
        apiRequest('GET', '/conversion/destinations', undefined, false, controller.signal),
        apiRequest('GET', '/conversion/integrations', undefined, false, controller.signal),
      ]);
      if (controller.signal.aborted || seq !== reqSeqRef.current) return;

      setDestinations(Array.isArray(destRes) ? destRes : []);
      setIntegrations(Array.isArray(integRes) ? integRes : []);
      setServiceUnavailable(false);
      setHasLoadedOnce(true);
    } catch (err) {
      if (controller.signal.aborted || seq !== reqSeqRef.current) return;
      if (isServiceUnavailableError(err)) {
        setServiceUnavailable(true);
      } else {
        setError(getErrorMessage(err, '加载像素与接入配置失败'));
      }
    } finally {
      if (!controller.signal.aborted && seq === reqSeqRef.current) {
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    void loadData();
    return () => {
      activeAbortRef.current?.abort();
    };
  }, [loadData]);

  const switchTab = (next: PixelsTabKey) => {
    const nextParams = new URLSearchParams(searchParams);
    if (next === 'pixels') {
      nextParams.delete('tab');
    } else {
      nextParams.set('tab', next);
    }
    setSearchParams(nextParams, { replace: true });
  };

  const handleTabKeyDown = (e: KeyboardEvent<HTMLButtonElement>, current: PixelsTabKey) => {
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight' || e.key === 'Home' || e.key === 'End') {
      e.preventDefault();
      const next: PixelsTabKey =
        e.key === 'Home'
          ? 'pixels'
          : e.key === 'End'
            ? 'integrations'
            : current === 'pixels'
              ? 'integrations'
              : 'pixels';
      switchTab(next);
      tabRefs.current[next]?.focus();
    }
  };

  const tabItems: Array<{
    key: PixelsTabKey;
    label: string;
    count: number;
    icon: typeof Radio;
  }> = [
      { key: 'pixels', label: 'Pixel 目标管理', count: destinations.length, icon: Radio },
      { key: 'integrations', label: '接入与授权绑定', count: integrations.length, icon: Layers },
    ];

  return (
    <section aria-labelledby={headingId} className="space-y-4 animate-fade-in">
      <h2 id={headingId} className="sr-only">
        像素管理与渠道接入授权工作台
      </h2>

      {/* Primary Underlined Navigation Tabs */}
      <div className="border-b border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-3">
        <div role="tablist" aria-label="像素管理工作区标签页" className="flex items-center gap-6 overflow-x-auto">
          {tabItems.map((tab) => {
            const active = activeTab === tab.key;
            const Icon = tab.icon;
            return (
              <button
                key={tab.key}
                ref={(el) => {
                  tabRefs.current[tab.key] = el;
                }}
                type="button"
                role="tab"
                id={`${tabsId}-tab-${tab.key}`}
                aria-selected={active}
                onKeyDown={(e) => handleTabKeyDown(e, tab.key)}
                onClick={() => switchTab(tab.key)}
                className={`focus-ring pb-2.5 text-sm font-medium whitespace-nowrap cursor-pointer transition-colors inline-flex items-center gap-2 ${active ? 'text-[var(--cms-fg)]' : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                  }`}
              >
                <Icon size={14} aria-hidden="true" />
                <span>{tab.label}</span>
                {hasLoadedOnce && (
                  <span className="badge badge-gray tabular-nums">{tab.count}</span>
                )}
              </button>
            );
          })}
        </div>

        {canReadPolicy && (
          <div className="pb-1.5 flex items-center gap-2">
            <Link
              to="/conversions"
              className="btn-ghost text-xs whitespace-nowrap no-underline inline-flex items-center gap-1 text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]"
            >
              <span>前往回传中心</span>
              <ExternalLink size={12} aria-hidden="true" />
            </Link>
          </div>
        )}
      </div>

      {serviceUnavailable ? (
        <ServiceUnavailableState
          retrying={loading}
          onRetry={() => void loadData()}
        />
      ) : (
        <>
          {error && (
            <InlineErrorBanner
              message={error}
              onRetry={() => void loadData()}
            />
          )}

          {activeTab === 'pixels' && (
            <div
              role="tabpanel"
              id={`${tabsId}-panel-pixels`}
              aria-labelledby={`${tabsId}-tab-pixels`}
            >
              <ConversionPixelsPanel
                destinations={destinations}
                integrations={integrations}
                loading={loading}
                hasLoadedOnce={hasLoadedOnce}
                loadError={error}
                canWrite={canWriteIntegration}
                canReadAudit={canReadAudit}
                canReadConversions={canReadPolicy}
                onRefresh={loadData}
              />
            </div>
          )}

          {activeTab === 'integrations' && (
            <div
              role="tabpanel"
              id={`${tabsId}-panel-integrations`}
              aria-labelledby={`${tabsId}-tab-integrations`}
            >
              <ConversionIntegrationsPanel
                integrations={integrations}
                destinations={destinations}
                loading={loading}
                hasLoadedOnce={hasLoadedOnce}
                loadError={error}
                canWriteIntegration={canWriteIntegration}
                canReadPolicy={canReadPolicy}
                canWritePolicy={canWritePolicy}
                canReadAudit={canReadAudit}
                onRefresh={loadData}
              />
            </div>
          )}
        </>
      )}
    </section>
  );
}

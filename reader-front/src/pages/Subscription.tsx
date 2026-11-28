import React, { useState, useEffect, useCallback, useRef } from 'react';
import {
  Crown,
  CreditCard,
  ExternalLink,
  RefreshCw,
  AlertTriangle,
  Clock,
  CheckCircle2,
  XCircle,
} from 'lucide-react';
import { api, type SubscriptionInfo } from '../utils/api';
import { useToast } from '../context/toastStore';
import { useConfirm } from '../context/confirmStore';
import {
  applyCancelResultToSubscription,
  buildSubscriptionViewModel,
  type SubscriptionViewModel,
} from '../utils/subscriptionHelpers';
import { sanitizeSubscriptionCopy } from '../utils/rechargeCatalogHelpers';

interface SubscriptionProps {
  authReady: boolean;
  onRefreshStatus?: () => Promise<void>;
  onNavigate: (page: string, params?: Record<string, unknown>) => void;
}

export const Subscription: React.FC<SubscriptionProps> = ({
  authReady,
  onRefreshStatus,
  onNavigate,
}) => {
  const { showToast } = useToast();
  const { showConfirm } = useConfirm();

  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isVip, setIsVip] = useState(false);
  const [subscriptions, setSubscriptions] = useState<SubscriptionInfo[]>([]);
  const [cancelingId, setCancelingId] = useState<string | null>(null);
  const [portalLoadingId, setPortalLoadingId] = useState<string | null>(null);

  const requestRef = useRef<AbortController | null>(null);

  const loadStatus = useCallback(async (isManualRefresh = false) => {
    requestRef.current?.abort();
    const controller = new AbortController();
    requestRef.current = controller;

    if (isManualRefresh) {
      setRefreshing(true);
    } else {
      setLoading(true);
    }
    setError(null);

    try {
      const data = await api.getSubscriptionStatus(controller.signal);
      if (controller.signal.aborted) return;
      setIsVip(Boolean(data.is_vip));
      setSubscriptions(Array.isArray(data.subscriptions) ? data.subscriptions : data.subscription ? [data.subscription] : []);
    } catch (err) {
      if (controller.signal.aborted) return;
      const msg = err instanceof Error ? err.message : 'Unable to load subscription status.';
      setError(msg);
    } finally {
      if (!controller.signal.aborted) {
        setLoading(false);
        setRefreshing(false);
      }
    }
  }, []);

  useEffect(() => {
    if (!authReady) return;
    void loadStatus(false);
    return () => requestRef.current?.abort();
  }, [authReady, loadStatus]);

  const handleCancelClick = (vm: SubscriptionViewModel) => {
    if (!vm.canCancelAutoRenew || cancelingId === vm.subscriptionId) return;

    const keepAccessNote = vm.paidThroughLabel
      ? ` You will keep full subscription reading access through ${vm.paidThroughLabel}.`
      : ' Your remaining paid period will not be interrupted.';

    showConfirm({
      title: 'Cancel Auto-Renewal?',
      message: `Turn off auto-renewal for ${vm.planName} (${vm.providerLabel})?${keepAccessNote}`,
      confirmText: 'Stop Auto-Renew',
      cancelText: 'Keep Plan',
      destructive: true,
      onConfirm: () => {
        void executeCancel(vm.subscriptionId);
      },
    });
  };

  const executeCancel = async (subscriptionId: string) => {
    if (cancelingId) return;
    setCancelingId(subscriptionId);
    try {
      const result = await api.cancelSubscription(subscriptionId);
      setSubscriptions((prev) =>
        prev.map((item) =>
          item.subscription_id === subscriptionId
            ? applyCancelResultToSubscription(item, result)
            : item,
        ),
      );
      showToast(
        result.message || 'Cancellation requested. Access continues through the paid period.',
        'success',
      );
      void onRefreshStatus?.();
      void loadStatus(true);
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Failed to request cancellation. Please try again.';
      showToast(msg, 'error');
    } finally {
      setCancelingId(null);
    }
  };

  const handleOpenBillingPortal = async (subscriptionId: string) => {
    if (portalLoadingId) return;
    setPortalLoadingId(subscriptionId);
    try {
      const res = await api.openStripeVIPPortal(subscriptionId, '/subscription');
      if (res.portal_url) {
        window.location.assign(res.portal_url);
      } else {
        showToast('Billing portal is temporarily unavailable.', 'error');
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Unable to open Stripe billing portal.';
      showToast(msg, 'error');
    } finally {
      setPortalLoadingId(null);
    }
  };

  const renderStatusIcon = (tone: SubscriptionViewModel['statusTone']) => {
    switch (tone) {
      case 'active':
        return <CheckCircle2 size={15} strokeWidth={2} aria-hidden="true" />;
      case 'warning':
        return <Clock size={15} strokeWidth={2} aria-hidden="true" />;
      case 'danger':
        return <AlertTriangle size={15} strokeWidth={2} aria-hidden="true" />;
      default:
        return <XCircle size={15} strokeWidth={2} aria-hidden="true" />;
    }
  };

  return (
    <div className="scroll-container animate-fade-in">
      <div className="sn-narrow-container sub-mgmt-container">
        <div className="sub-mgmt-header-card">
          <div className="sub-mgmt-header-left">
            <div className="sub-mgmt-crown-badge">
              <Crown size={20} strokeWidth={1.9} aria-hidden="true" />
            </div>
            <div style={{ minWidth: 0 }}>
              <h2 className="sub-mgmt-title">
                {isVip ? 'Subscription Active' : 'Subscription'}
              </h2>
              <p className="sub-mgmt-subtitle">
                {isVip
                  ? 'Manage your auto-renewal, billing cycle, and payment methods.'
                  : 'View and manage your StarNovel subscriptions.'}
              </p>
            </div>
          </div>
          <button
            type="button"
            className="btn-cta-secondary sub-mgmt-refresh-btn"
            onClick={() => void loadStatus(true)}
            disabled={loading || refreshing}
            aria-label="Refresh subscription status"
          >
            <RefreshCw size={14} className={refreshing ? 'spin-icon' : ''} aria-hidden="true" />
            <span>{refreshing ? 'Refreshing' : 'Refresh'}</span>
          </button>
        </div>

        {loading ? (
          <div className="sub-mgmt-card sub-mgmt-state-card" role="status" aria-live="polite">
            <RefreshCw size={22} className="spin-icon" style={{ color: 'var(--accent-color)' }} aria-hidden="true" />
            <p className="sub-mgmt-state-title">Loading subscription details…</p>
          </div>
        ) : error ? (
          <div className="sub-mgmt-card sub-mgmt-state-card" role="alert">
            <AlertTriangle size={24} style={{ color: '#DC2626' }} aria-hidden="true" />
            <p className="sub-mgmt-state-title">Unable to load subscriptions</p>
            <p className="sub-mgmt-state-desc">{error}</p>
            <button
              type="button"
              className="btn-cta-primary"
              style={{ borderRadius: '999px', padding: '10px 22px', marginTop: '8px' }}
              onClick={() => void loadStatus(false)}
            >
              Try Again
            </button>
          </div>
        ) : subscriptions.length === 0 ? (
          <div className="sub-mgmt-card sub-mgmt-state-card">
            <Crown size={28} style={{ color: 'var(--accent-color)' }} aria-hidden="true" />
            <p className="sub-mgmt-state-title">No Active Subscription</p>
            <p className="sub-mgmt-state-desc">
              Unlock unlimited access to all chapters with a flexible StarNovel Subscription.
            </p>
            <button
              type="button"
              className="btn-cta-primary"
              style={{ borderRadius: '999px', padding: '10px 24px', marginTop: '8px' }}
              onClick={() => onNavigate('recharge')}
            >
              Explore Subscription Plans
            </button>
          </div>
        ) : (
          <div className="sub-mgmt-list">
            {subscriptions.map((sub) => {
              const vm = buildSubscriptionViewModel(sub);
              const isCanceling = cancelingId === vm.subscriptionId;
              const isOpeningPortal = portalLoadingId === vm.subscriptionId;

              const displayPlanName = sanitizeSubscriptionCopy(vm.planName);

              return (
                <section
                  key={vm.subscriptionId}
                  className="sub-mgmt-card"
                  aria-label={`${displayPlanName} subscription`}
                >
                  <div className="sub-mgmt-card-top">
                    <div style={{ minWidth: 0 }}>
                      <h3 className="sub-mgmt-plan-name">{displayPlanName}</h3>
                      {vm.priceLabel && <p className="sub-mgmt-plan-price">{vm.priceLabel}</p>}
                    </div>
                    <span className={`sub-status-badge sub-status-${vm.statusTone}`}>
                      {renderStatusIcon(vm.statusTone)}
                      <span>{vm.statusLabel}</span>
                    </span>
                  </div>

                  <p className={`sub-status-note sub-note-${vm.statusTone}`}>
                    {vm.statusDescription}
                  </p>

                  <dl className="sub-meta-grid">
                    <div className="sub-meta-item">
                      <dt>Payment Channel</dt>
                      <dd>
                        {vm.providerLabel}
                        {vm.salesModeLabel ? ` · ${vm.salesModeLabel}` : ''}
                      </dd>
                    </div>
                    <div className="sub-meta-item">
                      <dt>Paid Through</dt>
                      <dd className="tabular-nums">{vm.paidThroughLabel || 'Pending confirmation'}</dd>
                    </div>
                    {vm.nextBillingLabel && (
                      <div className="sub-meta-item">
                        <dt>Next Billing Date</dt>
                        <dd className="tabular-nums">{vm.nextBillingLabel}</dd>
                      </div>
                    )}
                    <div className="sub-meta-item">
                      <dt>Agreement ID</dt>
                      <dd className="sub-meta-mono">{vm.subscriptionId}</dd>
                    </div>
                  </dl>

                  <div className="sub-actions">
                    {vm.canOpenBillingPortal && (
                      <button
                        type="button"
                        className="btn-cta-primary sub-action-btn"
                        disabled={isOpeningPortal || isCanceling}
                        onClick={() => void handleOpenBillingPortal(vm.subscriptionId)}
                      >
                        <CreditCard size={15} aria-hidden="true" />
                        <span>{isOpeningPortal ? 'Opening Billing Portal…' : 'Update Card & Billing'}</span>
                      </button>
                    )}

                    {vm.canOpenMoRLinkPortal && vm.morLinkUrl && (
                      <a
                        href={vm.morLinkUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="btn-cta-primary sub-action-btn"
                      >
                        <ExternalLink size={15} aria-hidden="true" />
                        <span>Manage via Stripe Link</span>
                      </a>
                    )}

                    {vm.canCancelAutoRenew && (
                      <button
                        type="button"
                        className="btn-cta-secondary sub-action-btn sub-cancel-btn"
                        disabled={isCanceling || isOpeningPortal}
                        onClick={() => handleCancelClick(vm)}
                      >
                        <span>{isCanceling ? 'Submitting Cancellation…' : 'Cancel Auto-Renew'}</span>
                      </button>
                    )}

                    {vm.cancelPending && (
                      <button
                        type="button"
                        className="btn-cta-secondary sub-action-btn"
                        disabled
                      >
                        <Clock size={14} aria-hidden="true" />
                        <span>Awaiting Provider Confirmation</span>
                      </button>
                    )}
                  </div>
                </section>
              );
            })}

            <div className="sub-mgmt-footer">
              <button
                type="button"
                className="btn-cta-secondary"
                style={{ borderRadius: '999px', padding: '10px 20px', fontSize: '0.8125rem' }}
                onClick={() => onNavigate('topup-history')}
              >
                Top Up History
              </button>
              <button
                type="button"
                className="btn-cta-secondary"
                style={{ borderRadius: '999px', padding: '10px 20px', fontSize: '0.8125rem' }}
                onClick={() => onNavigate('recharge')}
              >
                View All Packages
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
};

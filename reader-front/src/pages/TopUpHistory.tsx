import React, { useState, useEffect, useCallback, useRef, useId } from 'react';
import {
  Receipt,
  RefreshCw,
  AlertTriangle,
  Crown,
  BookOpen,
  CheckCircle2,
  Clock,
  XCircle,
  Copy,
  Check,
  X,
  ChevronRight,
  HelpCircle,
} from 'lucide-react';
import { api, type TopUpHistoryItem, type TopUpOrderType } from '../utils/api';
import { GoldCoin } from '../components/GoldCoin';
import { LegalModal } from '../components/LegalModal';
import { useToast } from '../context/toastStore';
import {
  buildTopUpBenefitSummary,
  formatTopUpDateRange,
  formatTopUpDateTime,
  formatTopUpProvider,
  getTopUpStatusMeta,
  resolveTopUpAmountDisplay,
  type TopUpStatusTone,
} from '../utils/topUpHistoryHelpers';

type FilterType = 'all' | TopUpOrderType;

const FILTER_OPTIONS: Array<{ id: FilterType; label: string }> = [
  { id: 'all', label: 'All' },
  { id: 'single', label: 'Coins' },
  { id: 'subscription', label: 'Subscription' },
  { id: 'whole_book', label: 'Full Book' },
];

const PAGE_SIZE = 20;

interface TopUpHistoryProps {
  authReady: boolean;
  isGuest: boolean;
  userEmail?: string;
  onRefreshWallet?: () => Promise<void>;
  onNavigate: (page: string, params?: Record<string, unknown>) => void;
}

export const TopUpHistory: React.FC<TopUpHistoryProps> = ({
  authReady,
  isGuest,
  userEmail = '',
  onRefreshWallet,
  onNavigate,
}) => {
  const { showToast } = useToast();
  const [filter, setFilter] = useState<FilterType>('all');
  const [items, setItems] = useState<TopUpHistoryItem[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [selectedOrder, setSelectedOrder] = useState<TopUpHistoryItem | null>(null);
  const [copiedOrderNo, setCopiedOrderNo] = useState<string | null>(null);
  const [supportOpen, setSupportOpen] = useState(false);
  const [supportOrderNo, setSupportOrderNo] = useState('');

  const requestRef = useRef<AbortController | null>(null);
  const copyTimerRef = useRef<number | undefined>(undefined);
  const detailDialogRef = useRef<HTMLDialogElement>(null);
  const pressedOutsideRef = useRef(false);
  const detailTitleId = useId();

  const loadPage = useCallback(
    async (mode: 'initial' | 'refresh' | 'more', activeFilter: FilterType, cursor?: string) => {
      if (mode !== 'more') {
        requestRef.current?.abort();
        setLoadingMore(false);
      } else if (requestRef.current) {
        return;
      }

      const controller = new AbortController();
      requestRef.current = controller;

      if (mode === 'initial') {
        setLoading(true);
        setError(null);
      } else if (mode === 'refresh') {
        setRefreshing(true);
        setError(null);
      } else {
        setLoadingMore(true);
      }

      try {
        const page = await api.getTopUpHistory({
          limit: PAGE_SIZE,
          cursor,
          type: activeFilter,
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        const pageItems = Array.isArray(page.items) ? page.items : [];
        if (mode === 'more') {
          setItems((prev) => {
            const seen = new Set(prev.map((entry) => entry.order_id));
            const appended = pageItems.filter((entry) => !seen.has(entry.order_id));
            return [...prev, ...appended];
          });
        } else {
          setItems(pageItems);
        }
        setNextCursor(page.next_cursor || undefined);
      } catch (err) {
        if (controller.signal.aborted) return;
        const msg = err instanceof Error ? err.message : 'Unable to load top-up history.';
        if (mode === 'more') {
          showToast(msg, 'error');
        } else {
          setError(msg);
        }
      } finally {
        if (requestRef.current === controller) {
          requestRef.current = null;
        }
        if (!controller.signal.aborted) {
          setLoading(false);
          setRefreshing(false);
          setLoadingMore(false);
        }
      }
    },
    [showToast],
  );

  useEffect(() => {
    if (!authReady) return;
    setItems([]);
    setNextCursor(undefined);
    void loadPage('initial', filter);
    return () => requestRef.current?.abort();
  }, [authReady, filter, loadPage]);

  useEffect(() => {
    return () => {
      window.clearTimeout(copyTimerRef.current);
    };
  }, []);

  useEffect(() => {
    const dialog = detailDialogRef.current;
    if (!dialog) return;
    if (selectedOrder && !dialog.open) {
      dialog.showModal();
    } else if (!selectedOrder && dialog.open) {
      dialog.close();
    }
  }, [selectedOrder]);

  const handleCopyOrderNo = async (orderNo: string) => {
    try {
      await navigator.clipboard.writeText(orderNo);
      setCopiedOrderNo(orderNo);
      showToast(`Copied ${orderNo}`, 'success');
      window.clearTimeout(copyTimerRef.current);
      copyTimerRef.current = window.setTimeout(() => setCopiedOrderNo(null), 2000);
    } catch {
      showToast('Unable to copy order number.', 'error');
    }
  };

  const renderStatusIcon = (tone: TopUpStatusTone) => {
    switch (tone) {
      case 'active':
        return <CheckCircle2 size={13} strokeWidth={2.1} aria-hidden="true" />;
      case 'warning':
        return <Clock size={13} strokeWidth={2.1} aria-hidden="true" />;
      case 'danger':
        return <AlertTriangle size={13} strokeWidth={2.1} aria-hidden="true" />;
      default:
        return <XCircle size={13} strokeWidth={2.1} aria-hidden="true" />;
    }
  };

  const renderTypeIcon = (type: TopUpOrderType) => {
    if (type === 'subscription') {
      return (
        <div className="history-type-badge history-type-vip" aria-hidden="true">
          <Crown size={18} strokeWidth={1.9} />
        </div>
      );
    }
    if (type === 'whole_book') {
      return (
        <div className="history-type-badge history-type-book" aria-hidden="true">
          <BookOpen size={18} strokeWidth={1.9} />
        </div>
      );
    }
    return (
      <div className="history-type-badge history-type-coin" aria-hidden="true">
        <GoldCoin size={18} />
      </div>
    );
  };

  const isPointerOutside = (e: React.PointerEvent<HTMLDialogElement>, dialog: HTMLDialogElement) => {
    const r = dialog.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  };

  const selectedAmount = selectedOrder ? resolveTopUpAmountDisplay(selectedOrder) : null;
  const selectedStatusMeta = selectedOrder
    ? getTopUpStatusMeta(selectedOrder.display_status, selectedAmount?.refundedAmount)
    : null;
  const selectedPeriodRange = selectedOrder
    ? formatTopUpDateRange(selectedOrder.period_start, selectedOrder.period_end)
    : null;

  return (
    <>
      <div className="scroll-container animate-fade-in">
        <div className="sn-narrow-container sub-mgmt-container">
          <div className="sub-mgmt-header-card">
            <div className="sub-mgmt-header-left">
              <div className="sub-mgmt-crown-badge">
                <Receipt size={20} strokeWidth={1.9} aria-hidden="true" />
              </div>
              <div style={{ minWidth: 0 }}>
                <h2 className="sub-mgmt-title">Top Up History</h2>
                <p className="sub-mgmt-subtitle">
                  View your coin purchases, subscriptions, and full-book unlocks.
                </p>
              </div>
            </div>
            <button
              type="button"
              className="btn-cta-secondary sub-mgmt-refresh-btn"
              onClick={() => {
                void loadPage('refresh', filter);
                void onRefreshWallet?.();
              }}
              disabled={loading || refreshing}
              aria-label="Refresh top-up history"
            >
              <RefreshCw size={14} className={refreshing ? 'spin-icon' : ''} aria-hidden="true" />
              <span>{refreshing ? 'Refreshing' : 'Refresh'}</span>
            </button>
          </div>

          <div className="history-filter-bar" role="tablist" aria-label="Filter purchases by type">
            {FILTER_OPTIONS.map((opt) => {
              const active = filter === opt.id;
              return (
                <button
                  key={opt.id}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  className={`history-filter-pill ${active ? 'active' : ''}`}
                  onClick={() => setFilter(opt.id)}
                >
                  {opt.label}
                </button>
              );
            })}
          </div>

          {isGuest && items.length > 0 && (
            <div className="history-guest-notice">
              <p>Sign up with an email to keep your purchase history and coins synced across devices.</p>
              <button
                type="button"
                className="btn-cta-secondary history-guest-btn"
                onClick={() => onNavigate('profile')}
              >
                Sign Up
              </button>
            </div>
          )}

          {loading ? (
            <div className="sub-mgmt-card sub-mgmt-state-card" role="status" aria-live="polite">
              <RefreshCw size={22} className="spin-icon" style={{ color: 'var(--accent-color)' }} aria-hidden="true" />
              <p className="sub-mgmt-state-title">Loading top-up history…</p>
            </div>
          ) : error ? (
            <div className="sub-mgmt-card sub-mgmt-state-card" role="alert">
              <AlertTriangle size={24} style={{ color: '#DC2626' }} aria-hidden="true" />
              <p className="sub-mgmt-state-title">Unable to load top-up history</p>
              <p className="sub-mgmt-state-desc">{error}</p>
              <button
                type="button"
                className="btn-cta-primary"
                style={{ borderRadius: '999px', padding: '10px 22px', marginTop: '8px' }}
                onClick={() => void loadPage('initial', filter)}
              >
                Try Again
              </button>
            </div>
          ) : items.length === 0 ? (
            <div className="sub-mgmt-card sub-mgmt-state-card">
              <Receipt size={28} style={{ color: 'var(--accent-color)' }} aria-hidden="true" />
              <p className="sub-mgmt-state-title">No top-up records yet</p>
              <p className="sub-mgmt-state-desc">
                {filter === 'all'
                  ? 'Your completed coin packs, subscriptions, and full-book purchases will appear here.'
                  : 'No purchases found under this category.'}
              </p>
              <button
                type="button"
                className="btn-cta-primary"
                style={{ borderRadius: '999px', padding: '10px 24px', marginTop: '8px' }}
                onClick={() => onNavigate('recharge')}
              >
                Top Up Now
              </button>
            </div>
          ) : (
            <div className="history-list">
              {items.map((item) => {
                const amount = resolveTopUpAmountDisplay(item);
                const statusMeta = getTopUpStatusMeta(item.display_status, amount.refundedAmount);
                const benefitLine = buildTopUpBenefitSummary(item);
                const timestamp = formatTopUpDateTime(item.paid_at || item.created_at);

                return (
                  <button
                    key={item.order_id}
                    type="button"
                    className="history-order-card"
                    onClick={() => setSelectedOrder(item)}
                  >
                    <div className="history-order-top">
                      <div className="history-order-title-group">
                        {renderTypeIcon(item.order_type)}
                        <div className="history-order-title-col">
                          <div className="history-order-title-row">
                            <span className="history-order-title">{item.title}</span>
                          </div>
                          <span className="history-order-benefit">{benefitLine}</span>
                        </div>
                      </div>
                      <div className="history-order-amount-col">
                        <span className="history-order-amount tabular-nums">{amount.primaryAmount}</span>
                        {amount.catalogUsdNote && (
                          <span className="history-order-usd-sub tabular-nums">{amount.catalogUsdNote}</span>
                        )}
                      </div>
                    </div>

                    {amount.refundedAmount && item.display_status === 'partially_refunded' && (
                      <div className="history-order-inline-note">
                        Refunded {amount.refundedAmount} · Benefits kept
                      </div>
                    )}

                    <div className="history-order-bottom">
                      <div className="history-order-meta">
                        <span className={`sub-status-badge history-status-pill sub-status-${statusMeta.tone}`}>
                          {renderStatusIcon(statusMeta.tone)}
                          <span>{statusMeta.label}</span>
                        </span>
                        <span className="history-meta-dot" aria-hidden="true">·</span>
                        <span>{formatTopUpProvider(item.provider)}</span>
                        <span className="history-meta-dot" aria-hidden="true">·</span>
                        <span className="tabular-nums">{timestamp}</span>
                      </div>
                      <div className="history-order-no-wrap">
                        <span className="history-order-no tabular-nums">{item.order_no}</span>
                        <ChevronRight size={14} strokeWidth={1.8} aria-hidden="true" />
                      </div>
                    </div>
                  </button>
                );
              })}

              {nextCursor && (
                <div className="sub-mgmt-footer">
                  <button
                    type="button"
                    className="btn-cta-secondary"
                    style={{ borderRadius: '999px', padding: '10px 22px', fontSize: '0.8125rem' }}
                    disabled={loadingMore}
                    onClick={() => void loadPage('more', filter, nextCursor)}
                  >
                    {loadingMore ? 'Loading...' : 'Load More History'}
                  </button>
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      {/* Order Detail Dialog */}
      <dialog
        ref={detailDialogRef}
        className="sn-dialog"
        aria-labelledby={detailTitleId}
        onCancel={(e) => {
          e.preventDefault();
          setSelectedOrder(null);
        }}
        onPointerDown={(e) => {
          if (detailDialogRef.current) {
            pressedOutsideRef.current = isPointerOutside(e, detailDialogRef.current);
          }
        }}
        onPointerUp={(e) => {
          if (detailDialogRef.current && pressedOutsideRef.current && isPointerOutside(e, detailDialogRef.current)) {
            setSelectedOrder(null);
          }
          pressedOutsideRef.current = false;
        }}
        style={{ width: '100%', maxWidth: '420px', padding: '20px' }}
      >
        {selectedOrder && selectedAmount && selectedStatusMeta && (
          <>
            <div className="history-detail-header">
              <h2 id={detailTitleId} className="history-detail-title">Order Details</h2>
              <button
                type="button"
                className="header-btn"
                onClick={() => setSelectedOrder(null)}
                aria-label="Close order details"
              >
                <X size={18} strokeWidth={1.8} aria-hidden="true" />
              </button>
            </div>

            <div className="history-detail-amount-box">
              <span className={`sub-status-badge sub-status-${selectedStatusMeta.tone}`}>
                {renderStatusIcon(selectedStatusMeta.tone)}
                <span>{selectedStatusMeta.label}</span>
              </span>
              <div className="history-detail-amount tabular-nums">{selectedAmount.primaryAmount}</div>
              {selectedAmount.catalogUsdNote && (
                <div className="history-detail-usd tabular-nums">
                  Catalog price: {selectedAmount.catalogUsdNote}
                </div>
              )}
              <p className="history-detail-status-desc">{selectedStatusMeta.description}</p>
            </div>

            <dl className="history-detail-list">
              <div className="history-detail-row">
                <dt>Order No.</dt>
                <dd className="history-detail-order-no">
                  <span className="sub-meta-mono tabular-nums">{selectedOrder.order_no}</span>
                  <button
                    type="button"
                    className="history-copy-btn"
                    onClick={() => void handleCopyOrderNo(selectedOrder.order_no)}
                    aria-label={`Copy order number ${selectedOrder.order_no}`}
                  >
                    {copiedOrderNo === selectedOrder.order_no ? (
                      <>
                        <Check size={13} strokeWidth={2.1} aria-hidden="true" />
                        <span>Copied</span>
                      </>
                    ) : (
                      <>
                        <Copy size={13} strokeWidth={2} aria-hidden="true" />
                        <span>Copy</span>
                      </>
                    )}
                  </button>
                </dd>
              </div>

              <div className="history-detail-row">
                <dt>Item</dt>
                <dd>{selectedOrder.title}</dd>
              </div>

              <div className="history-detail-row">
                <dt>Details</dt>
                <dd>{buildTopUpBenefitSummary(selectedOrder)}</dd>
              </div>

              <div className="history-detail-row">
                <dt>Payment Method</dt>
                <dd>{formatTopUpProvider(selectedOrder.provider)}</dd>
              </div>

              <div className="history-detail-row">
                <dt>Date</dt>
                <dd className="tabular-nums">
                  {formatTopUpDateTime(selectedOrder.paid_at || selectedOrder.created_at)}
                </dd>
              </div>

              {selectedPeriodRange && (
                <div className="history-detail-row">
                  <dt>Service Period</dt>
                  <dd className="tabular-nums">{selectedPeriodRange}</dd>
                </div>
              )}

              {selectedAmount.refundedAmount && (
                <div className="history-detail-row">
                  <dt>Refunded</dt>
                  <dd className="tabular-nums">{selectedAmount.refundedAmount}</dd>
                </div>
              )}
            </dl>

            <div className="history-detail-actions">
              {selectedOrder.order_type === 'whole_book' && selectedOrder.novel_id ? (
                <button
                  type="button"
                  className="btn-cta-primary sub-action-btn"
                  onClick={() => {
                    const targetNovelId = selectedOrder.novel_id!;
                    setSelectedOrder(null);
                    onNavigate('detail', { id: targetNovelId });
                  }}
                >
                  <BookOpen size={15} aria-hidden="true" />
                  <span>Read Book</span>
                </button>
              ) : null}

              {selectedOrder.order_type === 'subscription' ? (
                <button
                  type="button"
                  className="btn-cta-primary sub-action-btn"
                  onClick={() => {
                    setSelectedOrder(null);
                    onNavigate('subscription');
                  }}
                >
                  <Crown size={15} aria-hidden="true" />
                  <span>Manage Subscription</span>
                </button>
              ) : null}

              <button
                type="button"
                className="btn-cta-secondary sub-action-btn"
                onClick={() => {
                  const orderNo = selectedOrder.order_no;
                  setSelectedOrder(null);
                  setSupportOrderNo(orderNo);
                  setSupportOpen(true);
                }}
              >
                <HelpCircle size={15} aria-hidden="true" />
                <span>Contact Support</span>
              </button>
            </div>
          </>
        )}
      </dialog>

      <LegalModal
        type="contact"
        isOpen={supportOpen}
        onClose={() => setSupportOpen(false)}
        defaultEmail={userEmail}
        defaultSubject={supportOrderNo ? `Order Inquiry: ${supportOrderNo}` : 'Payment / Recharge Issue'}
      />
    </>
  );
};

import React, { useState, useEffect, useCallback, useRef } from 'react';
import { RefreshCw, AlertTriangle } from 'lucide-react';
import { api, type Transaction } from '../utils/api';
import { GoldCoin } from '../components/GoldCoin';
import { useToast } from '../context/toastStore';

const TX_PAGE_SIZE = 30;

interface CoinHistoryProps {
  authReady: boolean;
  userCoins: number;
  chargedCoins: number;
  bonusCoins: number;
  onRefreshWallet?: () => Promise<void>;
  onNavigate: (page: string, params?: Record<string, unknown>) => void;
}

function formatBizTypeLabel(bizType: Transaction['biz_type']): string {
  switch ((bizType || '').toLowerCase()) {
    case 'unlock':
      return 'Chapter Unlock';
    case 'recharge':
      return 'Top Up';
    case 'checkin':
      return 'Daily Check-in';
    case 'reward_task':
    case 'welcome':
    case 'gift_code':
      return 'Reward';
    case 'refund':
      return 'Refund';
    case 'dispute':
    case 'chargeback':
    case 'loss':
      return 'Adjustment';
    default:
      return 'Coins';
  }
}

export const CoinHistory: React.FC<CoinHistoryProps> = ({
  authReady,
  userCoins,
  chargedCoins,
  bonusCoins,
  onRefreshWallet,
  onNavigate,
}) => {
  const { showToast } = useToast();
  const [items, setItems] = useState<Transaction[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const requestRef = useRef<AbortController | null>(null);

  const loadPage = useCallback(
    async (mode: 'initial' | 'refresh' | 'more', cursor?: string) => {
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
        const page = await api.getTransactionHistory(TX_PAGE_SIZE, cursor, controller.signal);
        if (controller.signal.aborted) return;
        const pageItems = Array.isArray(page.items) ? page.items : [];
        if (mode === 'more') {
          setItems((prev) => {
            const seen = new Set(prev.map((entry) => entry.id));
            return [...prev, ...pageItems.filter((entry) => !seen.has(entry.id))];
          });
        } else {
          setItems(pageItems);
        }
        setNextCursor(page.next_cursor || undefined);
      } catch (err) {
        if (controller.signal.aborted) return;
        const msg = err instanceof Error ? err.message : 'Unable to load coin history.';
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
    void loadPage('initial');
    return () => requestRef.current?.abort();
  }, [authReady, loadPage]);

  return (
    <div className="scroll-container animate-fade-in">
      <div className="sn-narrow-container sub-mgmt-container">
        <div className="sub-mgmt-header-card">
          <div className="sub-mgmt-header-left">
            <div className="sub-mgmt-crown-badge">
              <GoldCoin size={20} />
            </div>
            <div style={{ minWidth: 0 }}>
              <h2 className="sub-mgmt-title tabular-nums">{userCoins.toLocaleString()} Coins</h2>
              <p className="sub-mgmt-subtitle tabular-nums">
                {chargedCoins.toLocaleString()} Purchased · {bonusCoins.toLocaleString()} Bonus
              </p>
            </div>
          </div>
          <button
            type="button"
            className="btn-cta-secondary sub-mgmt-refresh-btn"
            onClick={() => {
              void loadPage('refresh');
              void onRefreshWallet?.();
            }}
            disabled={loading || refreshing}
            aria-label="Refresh coin history"
          >
            <RefreshCw size={14} className={refreshing ? 'spin-icon' : ''} aria-hidden="true" />
            <span>{refreshing ? 'Refreshing' : 'Refresh'}</span>
          </button>
        </div>

        {loading ? (
          <div className="sub-mgmt-card sub-mgmt-state-card" role="status" aria-live="polite">
            <RefreshCw size={22} className="spin-icon" style={{ color: 'var(--accent-color)' }} aria-hidden="true" />
            <p className="sub-mgmt-state-title">Loading coin history…</p>
          </div>
        ) : error ? (
          <div className="sub-mgmt-card sub-mgmt-state-card" role="alert">
            <AlertTriangle size={24} style={{ color: '#DC2626' }} aria-hidden="true" />
            <p className="sub-mgmt-state-title">Unable to load coin history</p>
            <p className="sub-mgmt-state-desc">{error}</p>
            <button
              type="button"
              className="btn-cta-primary"
              style={{ borderRadius: '999px', padding: '10px 22px', marginTop: '8px' }}
              onClick={() => void loadPage('initial')}
            >
              Try Again
            </button>
          </div>
        ) : items.length === 0 ? (
          <div className="sub-mgmt-card sub-mgmt-state-card">
            <GoldCoin size={28} />
            <p className="sub-mgmt-state-title">No coin history yet</p>
            <p className="sub-mgmt-state-desc">
              Top up coins or claim daily rewards to unlock paid chapters.
            </p>
            <button
              type="button"
              className="btn-cta-primary"
              style={{ borderRadius: '999px', padding: '10px 24px', marginTop: '8px' }}
              onClick={() => onNavigate('recharge')}
            >
              Top Up Coins
            </button>
          </div>
        ) : (
          <div className="history-list">
            {items.map((item) => (
              <div key={item.id} className="coin-history-card">
                <div style={{ minWidth: 0 }}>
                  <div className="coin-history-desc">{item.desc}</div>
                  <div className="coin-history-meta">
                    <span className="coin-history-biz-tag">{formatBizTypeLabel(item.biz_type)}</span>
                    <span className="history-meta-dot" aria-hidden="true">·</span>
                    <span className="tabular-nums">{new Date(item.date).toLocaleString()}</span>
                  </div>
                </div>
                <span
                  className="tabular-nums coin-history-amount"
                  style={{ color: item.type === 'credit' ? '#16a34a' : '#dc2626' }}
                >
                  {item.type === 'credit' ? '+' : '-'}
                  {item.amount.toLocaleString()}
                </span>
              </div>
            ))}

            {nextCursor && (
              <div className="sub-mgmt-footer">
                <button
                  type="button"
                  className="btn-cta-secondary"
                  style={{ borderRadius: '999px', padding: '10px 22px', fontSize: '0.8125rem' }}
                  disabled={loadingMore}
                  onClick={() => void loadPage('more', nextCursor)}
                >
                  {loadingMore ? 'Loading...' : 'Load More History'}
                </button>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
};

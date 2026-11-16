import React, { useState, useEffect, useRef, useId } from 'react';
import { Calendar, Check, Sparkles } from 'lucide-react';
import { useToast } from '../context/toastStore';
import { api } from '../utils/api';
import { GoldCoin } from '../components/GoldCoin';

interface RewardsProps {
  authReady?: boolean;
  onAddCoins: (amount: number, reason: string) => void;
}

const DEFAULT_REWARDS = [10, 15, 20, 25, 30, 35, 50];

export const Rewards: React.FC<RewardsProps> = ({ authReady = true, onAddCoins }) => {
  const { showToast } = useToast();
  const [rewardsByDay, setRewardsByDay] = useState<number[]>(DEFAULT_REWARDS);
  const [streakDay, setStreakDay] = useState<number>(0);
  const [hasCheckedInToday, setHasCheckedInToday] = useState<boolean>(false);
  const [claiming, setClaiming] = useState<boolean>(false);
  const [loading, setLoading] = useState(true);
  const [showSuccessModal, setShowSuccessModal] = useState<{ visible: boolean; coins: number; message: string }>({
    visible: false,
    coins: 0,
    message: '',
  });

  const dialogRef = useRef<HTMLDialogElement>(null);
  const pressedOutsideRef = useRef(false);
  const modalTitleId = useId();

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (showSuccessModal.visible && !dialog.open) {
      dialog.showModal();
    } else if (!showSuccessModal.visible && dialog.open) {
      dialog.close();
    }
  }, [showSuccessModal.visible]);

  useEffect(() => {
    if (!authReady) return;
    let cancelled = false;
    const fetchCheckInStatus = async () => {
      setLoading(true);
      try {
        const state = await api.getCheckInState();
        if (cancelled) return;
        setHasCheckedInToday(Boolean(state.checked_in));
        setStreakDay(state.day || 0);
        if (Array.isArray(state.rewards) && state.rewards.length > 0) {
          setRewardsByDay(state.rewards);
        }
      } catch (err) {
        console.error('Failed to load check-in status:', err);
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    fetchCheckInStatus();
    return () => {
      cancelled = true;
    };
  }, [authReady]);

  const handleCheckIn = async () => {
    if (hasCheckedInToday || claiming) return;
    setClaiming(true);
    try {
      const state = await api.dailyCheckIn();
      setHasCheckedInToday(Boolean(state.checked_in));
      setStreakDay(state.day);
      if (Array.isArray(state.rewards) && state.rewards.length > 0) {
        setRewardsByDay(state.rewards);
      }

      const earnedCoins = state.coins_awarded || rewardsByDay[(state.day - 1) % rewardsByDay.length] || 0;
      onAddCoins(earnedCoins, `Daily Check-in (Day ${state.day})`);
      setShowSuccessModal({
        visible: true,
        coins: earnedCoins,
        message: `Day ${state.day} reward claimed. ${earnedCoins} Coins have been added to your wallet.`,
      });
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Failed to check in today. Please try again.';
      showToast(msg, 'error');
    } finally {
      setClaiming(false);
    }
  };

  const isPointerOutside = (e: React.PointerEvent<HTMLDialogElement>, dialog: HTMLDialogElement) => {
    const r = dialog.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  };

  if (loading) {
    return (
      <div className="scroll-container animate-fade-in" style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: '12px', color: 'var(--text-secondary)' }}>
        <div className="sn-spinner" />
        <p style={{ fontSize: '0.875rem' }}>Loading daily rewards...</p>
      </div>
    );
  }

  const cycleLen = rewardsByDay.length || 7;
  const completedCount = hasCheckedInToday
    ? streakDay
    : streakDay >= cycleLen
      ? 0
      : streakDay;
  const nextClaimDay = !hasCheckedInToday ? (streakDay % cycleLen) + 1 : 0;

  return (
    <div className="scroll-container animate-fade-in">
      <div className="sn-narrow-container">
        <div
          style={{
            backgroundColor: 'var(--bg-secondary)',
            borderRadius: '20px',
            padding: '24px 20px',
            border: '1px solid var(--border-color)',
            boxShadow: 'var(--card-shadow)',
          }}
        >
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: '16px', marginBottom: '20px' }}>
            <div>
              <div className="section-eyebrow" style={{ marginBottom: '4px' }}>Reading Streak</div>
              <h2 style={{ fontFamily: 'var(--font-display)', fontSize: '1.375rem', fontWeight: 600, color: 'var(--text-primary)' }}>
                Daily Check-In
              </h2>
              <p style={{ fontSize: '0.8125rem', color: 'var(--text-secondary)', marginTop: '4px', lineHeight: 1.5 }}>
                {hasCheckedInToday
                  ? `Checked in today · Current streak: Day ${streakDay}`
                  : 'Check in daily to earn bonus coins for unlocking chapters.'}
              </p>
            </div>
            <div
              style={{
                width: '44px',
                height: '44px',
                borderRadius: '12px',
                backgroundColor: 'var(--accent-light)',
                color: 'var(--accent-text)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                flexShrink: 0,
              }}
            >
              <Calendar size={20} strokeWidth={1.8} aria-hidden="true" />
            </div>
          </div>

          {/* 7-Day Streak Grid */}
          <div style={{ display: 'grid', gridTemplateColumns: `repeat(${cycleLen}, minmax(0, 1fr))`, gap: '6px', marginBottom: '22px' }}>
            {rewardsByDay.map((coins, index) => {
              const dayNum = index + 1;
              const isCompleted = dayNum <= completedCount;
              const isCurrent = dayNum === nextClaimDay;

              return (
                <div
                  key={dayNum}
                  aria-label={`Day ${dayNum}: +${coins} coins`}
                  style={{
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    justifyContent: 'center',
                    gap: '5px',
                    padding: '10px 2px',
                    borderRadius: '12px',
                    backgroundColor: isCompleted
                      ? 'var(--cta-bg)'
                      : isCurrent
                        ? 'var(--accent-light)'
                        : 'var(--bg-primary)',
                    border: isCurrent
                      ? '1.5px solid var(--accent-color)'
                      : '1px solid var(--border-color)',
                    color: isCompleted ? 'var(--cta-text)' : 'var(--text-primary)',
                    transition: 'var(--transition-fast)',
                  }}
                >
                  <span
                    style={{
                      fontSize: '0.625rem',
                      fontWeight: 600,
                      color: isCompleted ? 'var(--cta-text)' : 'var(--text-tertiary)',
                      opacity: isCompleted ? 0.8 : 1,
                    }}
                  >
                    D{dayNum}
                  </span>
                  <span className="tabular-nums" style={{ fontSize: '0.8125rem', fontWeight: 700 }}>
                    +{coins}
                  </span>
                  {isCompleted ? (
                    <Check size={13} strokeWidth={2.5} aria-label="Claimed" />
                  ) : (
                    <GoldCoin size={13} />
                  )}
                </div>
              );
            })}
          </div>

          <button
            type="button"
            className="btn-cta-primary"
            disabled={hasCheckedInToday || claiming}
            onClick={handleCheckIn}
            style={{ width: '100%', borderRadius: '999px' }}
          >
            {hasCheckedInToday ? (
              <>
                <Check size={16} strokeWidth={2.2} aria-hidden="true" />
                <span>Checked In Today</span>
              </>
            ) : claiming ? (
              <span>Claiming...</span>
            ) : (
              <span>Claim Daily Reward</span>
            )}
          </button>
        </div>
      </div>

      {/* Native <dialog> Success Modal */}
      <dialog
        ref={dialogRef}
        className="sn-dialog"
        aria-labelledby={modalTitleId}
        onCancel={(e) => {
          e.preventDefault();
          setShowSuccessModal({ visible: false, coins: 0, message: '' });
        }}
        onPointerDown={(e) => {
          if (dialogRef.current) {
            pressedOutsideRef.current = isPointerOutside(e, dialogRef.current);
          }
        }}
        onPointerUp={(e) => {
          if (dialogRef.current && pressedOutsideRef.current && isPointerOutside(e, dialogRef.current)) {
            setShowSuccessModal({ visible: false, coins: 0, message: '' });
          }
          pressedOutsideRef.current = false;
        }}
        style={{ width: '100%', maxWidth: '340px', padding: '26px 24px', textAlign: 'center' }}
      >
        <div
          style={{
            width: '52px',
            height: '52px',
            borderRadius: '50%',
            backgroundColor: 'var(--accent-light)',
            color: 'var(--accent-text)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            margin: '0 auto 14px',
          }}
        >
          <Sparkles size={24} strokeWidth={1.8} aria-hidden="true" />
        </div>
        <h2 id={modalTitleId} style={{ fontFamily: 'var(--font-display)', fontSize: '1.25rem', fontWeight: 600, marginBottom: '8px' }}>
          Reward Claimed
        </h2>
        <p style={{ fontSize: '0.875rem', color: 'var(--text-secondary)', marginBottom: '18px', lineHeight: 1.5 }}>
          {showSuccessModal.message}
        </p>
        <div
          className="tabular-nums"
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '6px',
            backgroundColor: 'var(--accent-light)',
            color: 'var(--accent-text)',
            fontWeight: 700,
            padding: '8px 18px',
            borderRadius: '999px',
            margin: '0 auto 20px',
            fontSize: '0.9375rem',
          }}
        >
          <GoldCoin size={16} />
          <span>+{showSuccessModal.coins} Coins</span>
        </div>
        <button
          type="button"
          autoFocus
          className="btn-cta-primary"
          style={{ width: '100%', borderRadius: '999px' }}
          onClick={() => setShowSuccessModal({ visible: false, coins: 0, message: '' })}
        >
          Continue
        </button>
      </dialog>
    </div>
  );
};

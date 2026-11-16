import React, { useState, useEffect, useRef, useId } from 'react';
import { Calendar, Check, Sparkles } from 'lucide-react';
import { useToast } from '../context/toastStore';
import { api } from '../utils/api';
import { GoldCoin } from '../components/GoldCoin';

interface RewardsProps {
  onAddCoins: (amount: number, reason: string) => void;
}

export const Rewards: React.FC<RewardsProps> = ({ onAddCoins }) => {
  const { showToast } = useToast();
  const [streak, setStreak] = useState<number>(0);
  const [lastCheckIn, setLastCheckIn] = useState<string>('');
  const [loading, setLoading] = useState(true);
  const [showSuccessModal, setShowSuccessModal] = useState<{ visible: boolean; coins: number; message: string }>({
    visible: false,
    coins: 0,
    message: '',
  });

  const dialogRef = useRef<HTMLDialogElement>(null);
  const pressedOutsideRef = useRef(false);
  const modalTitleId = useId();

  const rewardsByDay = [10, 15, 20, 25, 30, 35, 50];
  const todayStr = new Date().toDateString();
  const hasCheckedInToday = lastCheckIn === todayStr;

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
    const fetchCheckInStatus = async () => {
      setLoading(true);
      try {
        const txs = (await api.getTransactionHistory(1, 20)) || [];
        const checkinTxs = txs.filter((tx) => tx.biz_type === 'checkin');

        const currentDayStr = new Date().toDateString();
        const checkedIn = checkinTxs.some((tx) => new Date(tx.date).toDateString() === currentDayStr);
        setLastCheckIn(checkedIn ? currentDayStr : '');

        let currentStreak = 0;
        const checkDate = new Date();

        for (let i = 0; i < 7; i++) {
          const dateStr = checkDate.toDateString();
          const found = checkinTxs.some((tx) => new Date(tx.date).toDateString() === dateStr);
          if (found) {
            currentStreak++;
            checkDate.setDate(checkDate.getDate() - 1);
          } else {
            if (i === 0) {
              checkDate.setDate(checkDate.getDate() - 1);
              const yesterdayStr = checkDate.toDateString();
              const foundYesterday = checkinTxs.some((tx) => new Date(tx.date).toDateString() === yesterdayStr);
              if (foundYesterday) {
                continue;
              }
            }
            break;
          }
        }
        setStreak(currentStreak === 0 ? 0 : ((currentStreak - 1) % 7) + 1);
      } catch (err) {
        console.error('Failed to load checkin status:', err);
      } finally {
        setLoading(false);
      }
    };

    fetchCheckInStatus();
  }, []);

  const handleCheckIn = async () => {
    const currentDayStr = new Date().toDateString();
    if (lastCheckIn === currentDayStr) return;

    let nextStreak = streak + 1;
    if (nextStreak > 7) {
      nextStreak = 1;
    }

    const earnedCoins = rewardsByDay[nextStreak - 1];

    try {
      await api.dailyCheckIn(nextStreak, earnedCoins);
      setStreak(nextStreak);
      setLastCheckIn(currentDayStr);

      onAddCoins(earnedCoins, `Daily Check-in (Day ${nextStreak})`);
      setShowSuccessModal({
        visible: true,
        coins: earnedCoins,
        message: `Day ${nextStreak} reward claimed. ${earnedCoins} Coins have been added to your wallet.`,
      });
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Failed to check in today. Please try again.';
      showToast(msg, 'error');
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
                  ? `Checked in today · Current streak: ${streak} ${streak === 1 ? 'day' : 'days'}`
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
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7, minmax(0, 1fr))', gap: '6px', marginBottom: '22px' }}>
            {rewardsByDay.map((coins, index) => {
              const dayNum = index + 1;
              const isCompleted = dayNum <= streak;
              const isCurrent = dayNum === streak + 1 && !hasCheckedInToday;

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
            disabled={hasCheckedInToday}
            onClick={handleCheckIn}
            style={{ width: '100%', borderRadius: '999px' }}
          >
            {hasCheckedInToday ? (
              <>
                <Check size={16} strokeWidth={2.2} aria-hidden="true" />
                <span>Checked In Today</span>
              </>
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

import React, { useState, useEffect } from 'react';
import { api } from '../utils/api';
import { GoldCoin } from '../components/GoldCoin';

interface RewardsProps {
  onAddCoins: (amount: number, reason: string) => void;
}

export const Rewards: React.FC<RewardsProps> = ({ onAddCoins }) => {
  const [streak, setStreak] = useState<number>(0);
  const [lastCheckIn, setLastCheckIn] = useState<string>('');
  const [loading, setLoading] = useState(true);
  const [showSuccessModal, setShowSuccessModal] = useState<{ visible: boolean; coins: number; message: string }>({
    visible: false,
    coins: 0,
    message: ''
  });

  const rewardsByDay = [10, 15, 20, 25, 30, 35, 50];
  const todayStr = new Date().toDateString();
  const hasCheckedInToday = lastCheckIn === todayStr;

  // Load check-in history from transaction ledger on mount
  useEffect(() => {
    const fetchCheckInStatus = async () => {
      setLoading(true);
      try {
        const txs = await api.getTransactionHistory(1, 20);
        const checkinTxs = txs.filter(tx => tx.biz_type === 'checkin');
        
        // Check if checked in today
        const checkedIn = checkinTxs.some(tx => new Date(tx.date).toDateString() === todayStr);
        setLastCheckIn(checkedIn ? todayStr : '');
        
        // Calculate consecutive streak
        let currentStreak = 0;
        let checkDate = new Date();
        
        for (let i = 0; i < 7; i++) {
          const dateStr = checkDate.toDateString();
          const found = checkinTxs.some(tx => new Date(tx.date).toDateString() === dateStr);
          if (found) {
            currentStreak++;
            checkDate.setDate(checkDate.getDate() - 1);
          } else {
            // If not found and we are evaluating today, streak might continue from yesterday
            if (i === 0) {
              checkDate.setDate(checkDate.getDate() - 1);
              const yesterdayStr = checkDate.toDateString();
              const foundYesterday = checkinTxs.some(tx => new Date(tx.date).toDateString() === yesterdayStr);
              if (foundYesterday) {
                continue;
              }
            }
            break;
          }
        }
        setStreak(currentStreak === 0 ? 0 : ((currentStreak - 1) % 7) + 1);
      } catch (err) {
        console.error("Failed to load checkin status:", err);
      } finally {
        setLoading(false);
      }
    };
    
    fetchCheckInStatus();
  }, []);

  // Handle Daily Check-In via API
  const handleCheckIn = async () => {
    if (hasCheckedInToday) return;

    let nextStreak = streak + 1;
    if (nextStreak > 7) {
      nextStreak = 1;
    }

    const earnedCoins = rewardsByDay[nextStreak - 1];

    try {
      await api.dailyCheckIn(nextStreak, earnedCoins);
      setStreak(nextStreak);
      setLastCheckIn(todayStr);

      onAddCoins(earnedCoins, `Daily Check-in (Day ${nextStreak})`);
      setShowSuccessModal({
        visible: true,
        coins: earnedCoins,
        message: `Checked in successfully! You earned ${earnedCoins} Coins.`
      });
    } catch (err: any) {
      alert(err.message || "Failed to check in today. Please try again.");
    }
  };

  if (loading) {
    return (
      <div className="scroll-container animate-fade-in" style={{ textAlign: 'center', paddingTop: '100px', color: 'var(--text-secondary)' }}>
        <p>Loading reward details...</p>
      </div>
    );
  }

  return (
    <div className="scroll-container animate-fade-in" style={{ paddingBottom: '32px' }}>
      {/* Daily Check-In Section */}
      <div className="checkin-card" style={{
        background: 'linear-gradient(135deg, #1e1b4b 0%, #312e81 100%)',
        color: 'white',
        borderRadius: '16px',
        padding: '20px',
        marginBottom: '20px',
        boxShadow: '0 8px 30px rgba(0,0,0,0.15)',
        border: '1px solid rgba(255,255,255,0.08)'
      }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
          <div>
            <h2 style={{ fontSize: '18px', fontWeight: 800 }}>Daily Check-In</h2>
            <p style={{ fontSize: '11px', color: '#94a3b8', marginTop: '2px' }}>
              {hasCheckedInToday 
                ? `You checked in today! Streak: ${streak} days`
                : 'Check in daily to earn coins for free chapters!'
              }
            </p>
          </div>
          <span style={{ fontSize: '24px' }}>📅</span>
        </div>

        {/* Days Streak Map */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7, 1fr)', gap: '6px', marginBottom: '16px' }}>
          {rewardsByDay.map((coins, index) => {
            const dayNum = index + 1;
            const isCompleted = dayNum <= streak;
            const isCurrent = dayNum === streak + 1 && !hasCheckedInToday;

            return (
              <div 
                key={index} 
                style={{
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                  padding: '8px 2px',
                  borderRadius: '10px',
                  background: isCompleted 
                    ? 'rgba(79, 70, 229, 0.4)' 
                    : isCurrent 
                      ? 'rgba(255,255,255,0.15)' 
                      : 'rgba(255,255,255,0.03)',
                  border: isCurrent 
                    ? '1.5px solid var(--accent-color)' 
                    : '1px solid transparent',
                  opacity: isCompleted ? 0.9 : 1
                }}
              >
                <span style={{ fontSize: '10px', color: '#94a3b8', marginBottom: '4px' }}>D{dayNum}</span>
                <span style={{ fontSize: '12px', fontWeight: 700, color: isCompleted ? '#818cf8' : 'white' }}>
                  +{coins}
                </span>
                {isCompleted ? (
                  <span style={{ fontSize: '10px', color: '#818cf8', marginTop: '4px' }}>✓</span>
                ) : (
                  <GoldCoin size={10} style={{ marginTop: '4px' }} />
                )}
              </div>
            );
          })}
        </div>

        <button 
          className="btn-cta-primary" 
          disabled={hasCheckedInToday}
          onClick={handleCheckIn}
          style={{ 
            width: '100%', 
            padding: '12px',
            backgroundColor: hasCheckedInToday ? 'rgba(255,255,255,0.1)' : 'var(--accent-color)',
            color: hasCheckedInToday ? '#64748b' : 'white',
            boxShadow: hasCheckedInToday ? 'none' : '0 4px 14px rgba(79,70,229,0.4)',
            cursor: hasCheckedInToday ? 'not-allowed' : 'pointer'
          }}
        >
          {hasCheckedInToday ? 'Already Checked In' : 'Check In Now'}
        </button>
      </div>

      {/* Success Dialog Modal */}
      {showSuccessModal.visible && (
        <div style={{
          position: 'fixed',
          inset: 0,
          backgroundColor: 'rgba(0,0,0,0.6)',
          backdropFilter: 'blur(4px)',
          zIndex: 9999,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '24px'
        }}>
          <div className="animate-fade-in" style={{
            backgroundColor: 'var(--bg-secondary)',
            borderRadius: '16px',
            padding: '24px',
            width: '100%',
            maxWidth: '320px',
            textAlign: 'center',
            boxShadow: '0 20px 25px -5px rgba(0,0,0,0.3)',
            border: '1px solid var(--border-color)'
          }}>
            <span style={{ fontSize: '48px', display: 'block', marginBottom: '12px' }}>🎉</span>
            <h3 style={{ fontSize: '18px', fontWeight: 800, marginBottom: '8px', color: 'var(--text-primary)' }}>Congratulations!</h3>
            <p style={{ fontSize: '13px', color: 'var(--text-secondary)', marginBottom: '20px' }}>
              {showSuccessModal.message}
            </p>
            <div style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '6px',
              backgroundColor: 'var(--accent-light)',
              color: 'var(--accent-color)',
              fontWeight: 700,
              padding: '6px 14px',
              borderRadius: '99px',
              marginBottom: '20px',
              fontSize: '14px'
            }}>
              <GoldCoin size={14} />
              <span>+{showSuccessModal.coins} Coins</span>
            </div>
            <button 
              className="btn-cta-primary" 
              style={{ width: '100%', padding: '10px 0' }}
              onClick={() => setShowSuccessModal({ visible: false, coins: 0, message: '' })}
            >
              Get It
            </button>
          </div>
        </div>
      )}
    </div>
  );
};

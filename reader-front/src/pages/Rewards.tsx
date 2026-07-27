import React, { useState, useEffect } from 'react';
import { useToast } from '../context/ToastContext';
import { api } from '../utils/api';
import { GoldCoin } from '../components/GoldCoin';

interface RewardsProps {
  onAddCoins: (amount: number, reason: string) => void;
  globalTheme?: 'light' | 'dark';
}

export const Rewards: React.FC<RewardsProps> = ({ onAddCoins, globalTheme = 'light' }) => {
  const { showToast } = useToast();
  const [streak, setStreak] = useState<number>(0);
  const [lastCheckIn, setLastCheckIn] = useState<string>('');
  const [loading, setLoading] = useState(true);
  const [showSuccessModal, setShowSuccessModal] = useState<{ visible: boolean; coins: number; message: string }>({
    visible: false,
    coins: 0,
    message: ''
  });

  const isDark = globalTheme === 'dark';

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
  }, [todayStr]);

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
      showToast(err.message || "Failed to check in today. Please try again.", "error");
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
      {/* Daily Check-In Card Section */}
      <div 
        className="checkin-card" 
        style={{
          background: isDark
            ? 'linear-gradient(135deg, #0f172a 0%, #1e1b4b 100%)'
            : 'linear-gradient(135deg, #eef2ff 0%, #f8fafc 100%)',
          color: isDark ? '#ffffff' : '#1e1b4b',
          borderRadius: '16px',
          padding: '22px 20px',
          marginBottom: '24px',
          boxShadow: isDark
            ? '0 10px 30px rgba(0,0,0,0.35)'
            : '0 10px 25px rgba(99, 102, 241, 0.12)',
          border: isDark ? '1px solid rgba(255,255,255,0.1)' : '1px solid #c7d2fe',
          transition: 'all 0.3s ease'
        }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '18px' }}>
          <div>
            <h2 style={{ fontSize: '20px', fontWeight: 800, color: isDark ? '#ffffff' : '#1e1b4b' }}>
              Daily Check-In
            </h2>
            <p style={{ fontSize: '12px', color: isDark ? '#a5b4fc' : '#4f46e5', marginTop: '3px', fontWeight: 500 }}>
              {hasCheckedInToday 
                ? `You checked in today! Consecutive Streak: ${streak} Days`
                : 'Check in daily to claim free reading coins!'
              }
            </p>
          </div>
          <div style={{
            fontSize: '28px',
            background: isDark ? 'rgba(255,255,255,0.1)' : 'rgba(99, 102, 241, 0.1)',
            padding: '8px',
            borderRadius: '12px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center'
          }}>
            📅
          </div>
        </div>

        {/* Days Streak Map Grid */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7, 1fr)', gap: '6px', marginBottom: '20px' }}>
          {rewardsByDay.map((coins, index) => {
            const dayNum = index + 1;
            const isCompleted = dayNum <= streak;
            const isCurrent = dayNum === streak + 1 && !hasCheckedInToday;

            // Theme colors per day item
            let bgColor = isDark ? 'rgba(255,255,255,0.04)' : '#ffffff';
            let borderColor = isDark ? 'rgba(255,255,255,0.08)' : '#e2e8f0';
            let textColor = isDark ? '#cbd5e1' : '#475569';
            let dayColor = isDark ? '#94a3b8' : '#94a3b8';
            let boxShadow = 'none';

            if (isCompleted) {
              bgColor = isDark 
                ? 'linear-gradient(135deg, #4f46e5 0%, #3730a3 100%)' 
                : 'linear-gradient(135deg, #6366f1 0%, #4f46e5 100%)';
              borderColor = 'transparent';
              textColor = '#ffffff';
              dayColor = 'rgba(255,255,255,0.8)';
            } else if (isCurrent) {
              bgColor = isDark ? 'rgba(255,255,255,0.12)' : '#ffffff';
              borderColor = '#4f46e5';
              textColor = isDark ? '#818cf8' : '#4f46e5';
              dayColor = isDark ? '#a5b4fc' : '#6366f1';
              boxShadow = isDark 
                ? '0 0 12px rgba(129,140,248,0.3)' 
                : '0 4px 12px rgba(79,70,229,0.18)';
            }

            return (
              <div 
                key={index} 
                style={{
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                  padding: '10px 2px',
                  borderRadius: '12px',
                  background: bgColor,
                  border: `1.5px solid ${borderColor}`,
                  boxShadow: boxShadow,
                  transition: 'all 0.2s ease',
                  opacity: isCompleted ? 0.95 : 1
                }}
              >
                <span style={{ fontSize: '10px', fontWeight: 600, color: dayColor, marginBottom: '4px' }}>
                  D{dayNum}
                </span>
                <span style={{ fontSize: '13px', fontWeight: 800, color: textColor }}>
                  +{coins}
                </span>
                {isCompleted ? (
                  <span style={{ fontSize: '11px', fontWeight: 800, color: '#ffffff', marginTop: '4px' }}>✓</span>
                ) : (
                  <GoldCoin size={11} style={{ marginTop: '4px' }} />
                )}
              </div>
            );
          })}
        </div>

        {/* Check-In CTA Button */}
        <button 
          disabled={hasCheckedInToday}
          onClick={handleCheckIn}
          style={{ 
            width: '100%', 
            padding: '13px',
            borderRadius: '99px',
            fontSize: '14px',
            fontWeight: 800,
            border: 'none',
            background: hasCheckedInToday 
              ? (isDark ? 'rgba(255,255,255,0.08)' : '#e2e8f0')
              : 'linear-gradient(135deg, #6366f1 0%, #4338ca 100%)',
            color: hasCheckedInToday 
              ? (isDark ? '#64748b' : '#94a3b8') 
              : '#ffffff',
            boxShadow: hasCheckedInToday 
              ? 'none' 
              : '0 6px 18px rgba(79,70,229,0.35)',
            cursor: hasCheckedInToday ? 'not-allowed' : 'pointer',
            transition: 'all 0.25s ease'
          }}
        >
          {hasCheckedInToday ? '✓ Already Checked In Today' : 'Claim Daily Reward Now'}
        </button>
      </div>

      {/* Success Dialog Modal */}
      {showSuccessModal.visible && (
        <div style={{
          position: 'fixed',
          inset: 0,
          backgroundColor: 'rgba(0,0,0,0.6)',
          backdropFilter: 'blur(5px)',
          zIndex: 9999,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '24px'
        }}>
          <div className="animate-fade-in" style={{
            backgroundColor: 'var(--bg-secondary)',
            borderRadius: '20px',
            padding: '28px 24px',
            width: '100%',
            maxWidth: '320px',
            textAlign: 'center',
            boxShadow: '0 25px 30px -5px rgba(0,0,0,0.35)',
            border: '1px solid var(--border-color)'
          }}>
            <span style={{ fontSize: '52px', display: 'block', marginBottom: '12px' }}>🎉</span>
            <h3 style={{ fontSize: '20px', fontWeight: 800, marginBottom: '8px', color: 'var(--text-primary)' }}>Congratulations!</h3>
            <p style={{ fontSize: '13px', color: 'var(--text-secondary)', marginBottom: '20px', lineHeight: 1.4 }}>
              {showSuccessModal.message}
            </p>
            <div style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '6px',
              backgroundColor: 'var(--accent-light)',
              color: 'var(--accent-color)',
              fontWeight: 800,
              padding: '8px 18px',
              borderRadius: '99px',
              marginBottom: '22px',
              fontSize: '16px'
            }}>
              <GoldCoin size={18} />
              <span>+{showSuccessModal.coins} Coins</span>
            </div>
            <button 
              className="btn-cta-primary" 
              style={{ width: '100%', padding: '12px 0', borderRadius: '99px', fontWeight: 800 }}
              onClick={() => setShowSuccessModal({ visible: false, coins: 0, message: '' })}
            >
              Awesome!
            </button>
          </div>
        </div>
      )}
    </div>
  );
};

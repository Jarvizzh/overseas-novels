import React, { useId, useState } from 'react';
import { BookOpen, Calendar, Check, Compass, Gift, User } from 'lucide-react';

interface H5RewardsPreviewProps {
  rewards: number[];
  h5Theme: 'light' | 'dark';
  onThemeChange: (theme: 'light' | 'dark') => void;
}

const PreviewGoldCoin: React.FC<{ size?: number }> = ({ size = 13 }) => {
  const gradId = useId();
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      aria-hidden="true"
      style={{ width: `${size}px`, height: `${size}px`, display: 'inline-block', flexShrink: 0 }}
    >
      <circle cx="12" cy="12" r="10" fill={`url(#${gradId})`} stroke="#9A7132" strokeWidth="1.25" />
      <circle cx="12" cy="12" r="7.5" fill="none" stroke="rgba(255, 248, 225, 0.55)" strokeWidth="0.75" />
      <path
        d="M12 6.5l1.35 3.65L17 11.5l-3.65 1.35L12 16.5l-1.35-3.65L7 11.5l3.65-1.35L12 6.5z"
        fill="#6B4A16"
      />
      <defs>
        <linearGradient id={gradId} x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" stopColor="#F5E1A4" />
          <stop offset="50%" stopColor="#D4AE68" />
          <stop offset="100%" stopColor="#A67C37" />
        </linearGradient>
      </defs>
    </svg>
  );
};

export const H5RewardsPreview: React.FC<H5RewardsPreviewProps> = ({
  rewards,
  h5Theme,
  onThemeChange,
}) => {
  // Simulate reader streak state: 0 = fresh Day 1 claimable, 2 = Day 3 claimable, 7 = full cycle checked in
  const [simState, setSimState] = useState<'day1' | 'day3' | 'day7_done'>('day3');

  const safeRewards = rewards.length === 7 ? rewards : [10, 15, 20, 25, 30, 35, 50];
  const completedCount = simState === 'day1' ? 0 : simState === 'day3' ? 2 : 7;
  const nextClaimDay = simState === 'day1' ? 1 : simState === 'day3' ? 3 : 0;
  const hasCheckedInToday = simState === 'day7_done';

  return (
    <div className="xl:sticky xl:top-16 space-y-2.5">
      <div className="flex flex-wrap items-center justify-between gap-2 px-0.5">
        <span className="text-xs font-medium text-[var(--cms-fg)]">H5 签到活动页实时预览</span>
        <div className="flex items-center rounded-lg border border-[var(--cms-border)] bg-[var(--cms-card)] p-0.5 text-xs shrink-0">
          <button
            type="button"
            onClick={() => onThemeChange('light')}
            className={`focus-ring px-2 py-0.5 rounded-md cursor-pointer transition-colors ${
              h5Theme === 'light'
                ? 'bg-[var(--cms-secondary)] text-[var(--cms-fg)] font-medium'
                : 'text-[var(--cms-muted-fg)]'
            }`}
          >
            日间
          </button>
          <button
            type="button"
            onClick={() => onThemeChange('dark')}
            className={`focus-ring px-2 py-0.5 rounded-md cursor-pointer transition-colors ${
              h5Theme === 'dark'
                ? 'bg-[var(--cms-secondary)] text-[var(--cms-fg)] font-medium'
                : 'text-[var(--cms-muted-fg)]'
            }`}
          >
            夜间
          </button>
        </div>
      </div>

      {/* Interactive Reader State Simulator Strip */}
      <div className="cms-card p-2.5 flex items-center justify-between gap-2 text-xs">
        <span className="text-[var(--cms-muted-fg)] font-medium">模拟读者态</span>
        <div className="flex items-center gap-1">
          {(
            [
              { key: 'day1', label: '首日待签' },
              { key: 'day3', label: '第3天待签' },
              { key: 'day7_done', label: '满签已领' },
            ] as const
          ).map((item) => (
            <button
              key={item.key}
              type="button"
              onClick={() => setSimState(item.key)}
              className={`focus-ring px-2 py-0.5 rounded-md text-[11px] cursor-pointer transition-colors ${
                simState === item.key
                  ? 'nav-active-item font-medium'
                  : 'text-[var(--cms-muted-fg)] hover:bg-[var(--cms-secondary)]'
              }`}
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>

      {/* H5 Rewards Phone Shell */}
      <div
        className={`rounded-[26px] border border-[var(--cms-border)] overflow-hidden transition-colors flex flex-col h-[660px] ${
          h5Theme === 'dark' ? 'h5-preview-dark' : 'h5-preview-light'
        }`}
        style={{ boxShadow: 'var(--shadow-sm)', backgroundColor: 'var(--h5-bg-primary)' }}
      >
        {/* Mobile Header mirroring reader-front Header on /rewards */}
        <div
          className="px-4 py-3 flex items-center justify-between shrink-0"
          style={{
            borderBottom: '1px solid var(--h5-border)',
            backgroundColor: 'var(--h5-bg-primary)',
          }}
        >
          <span
            style={{
              fontFamily: "'Iowan Old Style', Georgia, serif",
              fontSize: '1.05rem',
              fontWeight: 600,
              letterSpacing: '-0.015em',
              color: 'var(--h5-text-primary)',
            }}
          >
            Daily Rewards
          </span>
          <span
            className="px-2.5 py-1 rounded-full tabular-nums flex items-center gap-1"
            style={{
              backgroundColor: 'var(--h5-bg-secondary)',
              border: '1px solid var(--h5-border)',
              color: 'var(--h5-text-primary)',
              fontSize: '0.6875rem',
              fontWeight: 600,
            }}
          >
            <PreviewGoldCoin size={11} />
            <span>1,280</span>
          </span>
        </div>

        {/* Scrollable Mobile Viewport Body */}
        <div className="flex-1 overflow-y-auto p-3.5" style={{ backgroundColor: 'var(--h5-bg-primary)' }}>
          <div
            style={{
              backgroundColor: 'var(--h5-bg-secondary)',
              borderRadius: '18px',
              padding: '18px 14px',
              border: '1px solid var(--h5-border)',
            }}
          >
            <div className="flex items-start justify-between gap-3 mb-4">
              <div>
                <div
                  style={{
                    fontSize: '0.625rem',
                    fontWeight: 600,
                    textTransform: 'uppercase',
                    letterSpacing: '0.1em',
                    color: 'var(--h5-accent-text)',
                    marginBottom: '2px',
                  }}
                >
                  Reading Streak
                </div>
                <h4
                  className="m-0"
                  style={{
                    fontFamily: "'Iowan Old Style', Georgia, serif",
                    fontSize: '1.125rem',
                    fontWeight: 600,
                    color: 'var(--h5-text-primary)',
                  }}
                >
                  Daily Check-In
                </h4>
                <p
                  className="m-0 mt-1"
                  style={{
                    fontSize: '0.6875rem',
                    color: 'var(--h5-text-secondary)',
                    lineHeight: 1.45,
                  }}
                >
                  {hasCheckedInToday
                    ? 'Checked in today · Current streak: Day 7'
                    : 'Check in daily to earn bonus coins for unlocking chapters.'}
                </p>
              </div>
              <div
                className="w-9 h-9 rounded-xl flex items-center justify-center shrink-0"
                style={{
                  backgroundColor: 'var(--h5-accent-light)',
                  color: 'var(--h5-accent-text)',
                }}
              >
                <Calendar size={16} strokeWidth={1.8} aria-hidden="true" />
              </div>
            </div>

            {/* 7-Day Streak Grid mirroring reader-front/src/pages/Rewards.tsx */}
            <div className="grid grid-cols-7 gap-1 mb-4">
              {safeRewards.map((coins, index) => {
                const dayNum = index + 1;
                const isCompleted = dayNum <= completedCount;
                const isCurrent = dayNum === nextClaimDay;

                return (
                  <div
                    key={`h5-day-${dayNum}`}
                    className="flex flex-col items-center justify-center gap-1 py-2 px-0.5 rounded-xl transition-colors"
                    style={{
                      backgroundColor: isCompleted
                        ? 'var(--h5-cta-bg)'
                        : isCurrent
                          ? 'var(--h5-accent-light)'
                          : 'var(--h5-bg-primary)',
                      border: isCurrent
                        ? '1.5px solid var(--h5-accent)'
                        : '1px solid var(--h5-border)',
                      color: isCompleted ? 'var(--h5-cta-text)' : 'var(--h5-text-primary)',
                    }}
                  >
                    <span
                      style={{
                        fontSize: '9px',
                        fontWeight: 600,
                        color: isCompleted ? 'var(--h5-cta-text)' : 'var(--h5-text-tertiary)',
                        opacity: isCompleted ? 0.8 : 1,
                      }}
                    >
                      D{dayNum}
                    </span>
                    <span className="tabular-nums font-bold" style={{ fontSize: '11px' }}>
                      +{coins}
                    </span>
                    {isCompleted ? (
                      <Check size={11} strokeWidth={2.5} aria-hidden="true" />
                    ) : (
                      <PreviewGoldCoin size={11} />
                    )}
                  </div>
                );
              })}
            </div>

            <div
              className="w-full py-2.5 text-center rounded-full text-xs font-semibold flex items-center justify-center gap-1.5"
              style={{
                backgroundColor: 'var(--h5-cta-bg)',
                color: 'var(--h5-cta-text)',
                opacity: hasCheckedInToday ? 0.55 : 1,
              }}
            >
              {hasCheckedInToday && <Check size={14} strokeWidth={2.2} aria-hidden="true" />}
              <span>{hasCheckedInToday ? 'Checked In Today' : 'Claim Daily Reward'}</span>
            </div>
          </div>
        </div>

        {/* Mobile Bottom Navigation mirroring reader-front BottomNav */}
        <div
          className="shrink-0 grid grid-cols-4 pt-2 pb-2.5 px-2"
          style={{
            borderTop: '1px solid var(--h5-border)',
            backgroundColor: 'var(--h5-bg-secondary)',
          }}
        >
          {(
            [
              { id: 'shelf', label: 'Library', Icon: BookOpen, active: false },
              { id: 'home', label: 'Discover', Icon: Compass, active: false },
              { id: 'rewards', label: 'Rewards', Icon: Gift, active: true },
              { id: 'profile', label: 'Profile', Icon: User, active: false },
            ] as const
          ).map(({ id, label, Icon, active }) => (
            <div
              key={id}
              className="flex flex-col items-center justify-center gap-0.5"
              style={{
                color: active ? 'var(--h5-accent-text)' : 'var(--h5-text-tertiary)',
                fontSize: '10px',
                fontWeight: active ? 600 : 500,
              }}
            >
              <Icon size={17} strokeWidth={active ? 2 : 1.65} aria-hidden="true" />
              <span>{label}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
};

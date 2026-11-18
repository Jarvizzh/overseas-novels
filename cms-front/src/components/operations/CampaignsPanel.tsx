import React, { useMemo, useState } from 'react';
import { Calendar, Coins, Gift, RotateCcw, Save, Sparkles } from 'lucide-react';
import { StatusDot } from '../ui/Primitives';

export type CampaignTypeKey = 'checkin';

interface CampaignsPanelProps {
  canEdit: boolean;
  rewardInputs: string[];
  dirty: boolean;
  saving: boolean;
  onChangeReward: (dayIndex: number, value: string) => void;
  onApplyPreset: (rewards: number[]) => void;
  onDiscard: () => void;
  onSave: () => void;
}

const CHECKIN_PRESETS: Array<{ label: string; rewards: number[] }> = [
  { label: '标准阶梯 (10→50)', rewards: [10, 15, 20, 25, 30, 35, 50] },
  { label: '温和留存 (10→30)', rewards: [10, 12, 15, 18, 22, 26, 30] },
  { label: '周末冲刺 (15→80)', rewards: [15, 15, 20, 20, 30, 40, 80] },
  { label: '每日等额 (20×7)', rewards: [20, 20, 20, 20, 20, 20, 20] },
];

export const CampaignsPanel: React.FC<CampaignsPanelProps> = ({
  canEdit,
  rewardInputs,
  dirty,
  saving,
  onChangeReward,
  onApplyPreset,
  onDiscard,
  onSave,
}) => {
  const [activeCampaign, setActiveCampaign] = useState<CampaignTypeKey>('checkin');

  const parsedRewards = useMemo(
    () =>
      rewardInputs.map((raw) => {
        const trimmed = raw.trim();
        if (!trimmed) return null;
        const n = Number(trimmed);
        if (!Number.isInteger(n) || n < 1 || n > 10000) return null;
        return n;
      }),
    [rewardInputs],
  );

  const hasInvalid = parsedRewards.some((v) => v === null);
  const safeNumbers = parsedRewards.map((v) => v ?? 0);
  const totalCoins = safeNumbers.reduce((sum, v) => sum + v, 0);
  const maxDayCoins = Math.max(1, ...safeNumbers);
  const avgCoins = Math.round((totalCoins / 7) * 10) / 10;

  return (
    <div className="space-y-4">
      {/* Extensible Activity Selector Strip */}
      <div className="cms-card p-4 space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              onClick={() => setActiveCampaign('checkin')}
              className={`focus-ring px-3.5 py-2 rounded-xl border text-left cursor-pointer transition-colors flex items-center gap-2.5 ${
                activeCampaign === 'checkin'
                  ? 'border-[var(--cms-primary)] bg-[color-mix(in_srgb,var(--cms-primary)_7%,var(--cms-card))]'
                  : 'border-[var(--cms-border)] bg-[var(--cms-content)]'
              }`}
            >
              <span className="w-8 h-8 rounded-lg nav-active-item inline-flex items-center justify-center shrink-0">
                <Calendar size={15} aria-hidden="true" />
              </span>
              <div>
                <div className="text-xs font-semibold text-[var(--cms-fg)] flex items-center gap-2">
                  <span>每日连续签到</span>
                  {dirty && (
                    <span
                      title="有未保存更改"
                      className="w-1.5 h-1.5 rounded-full bg-[var(--cms-warning)]"
                      aria-hidden="true"
                    />
                  )}
                </div>
                <div className="text-[11px] text-[var(--cms-muted-fg)] tabular-nums">
                  7 天循环 · 周累计 +{totalCoins} 金币
                </div>
              </div>
            </button>

            {/* Extensible slot hint for future campaigns */}
            <div className="px-3.5 py-2 rounded-xl border border-dashed border-[var(--cms-border)] text-left flex items-center gap-2 opacity-75">
              <Gift size={14} className="text-[var(--cms-muted-fg)] shrink-0" aria-hidden="true" />
              <div>
                <div className="text-xs font-medium text-[var(--cms-muted-fg)]">更多运营活动</div>
                <div className="text-[11px] text-[var(--cms-muted-fg)]">支持按模块继续扩展</div>
              </div>
            </div>
          </div>

          <StatusDot tone="success" label="签到活动运行中" />
        </div>
      </div>

      {/* Daily Check-in Structured Config Card */}
      {activeCampaign === 'checkin' && (
        <div className="cms-card p-5 space-y-5">
          {/* Header & Independent Save Strip */}
          <div className="flex flex-wrap items-start justify-between gap-3 pb-4 border-b border-[var(--cms-border)]">
            <div>
              <h2 className="text-base font-semibold text-[var(--cms-fg)] flex items-center gap-2 m-0">
                <Calendar size={16} className="text-[var(--cms-primary)]" aria-hidden="true" />
                <span>7 天连续签到奖励梯度（Daily Check-In）</span>
              </h2>
              <p className="text-xs text-[var(--cms-muted-fg)] mt-1 m-0">
                奖励以赠送金币（Bonus Coins）发放，按 UTC 自然日计算连续签到周期，满 7 天后自动进入下一轮。
              </p>
            </div>

            {canEdit && (
              <div className="flex items-center gap-2 shrink-0">
                {dirty && (
                  <>
                    <StatusDot tone="warning" label="有未保存更改" />
                    <button
                      type="button"
                      disabled={saving}
                      onClick={onDiscard}
                      className="btn-ghost h-8 px-2.5 text-xs text-[var(--cms-muted-fg)]"
                    >
                      <RotateCcw size={13} aria-hidden="true" />
                      <span>放弃更改</span>
                    </button>
                  </>
                )}
                <button
                  type="button"
                  disabled={!dirty || saving || hasInvalid}
                  onClick={onSave}
                  className="btn-primary h-8 px-3.5 text-xs"
                >
                  <Save size={13} aria-hidden="true" />
                  <span>{saving ? '保存中...' : '保存签到配置'}</span>
                </button>
              </div>
            )}
          </div>

          {/* Summary KPI Strip + Quick Presets */}
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex flex-wrap items-center gap-4 text-xs tabular-nums">
              <div className="inline-flex items-center gap-1.5 text-[var(--cms-fg)]">
                <Coins size={14} className="text-[var(--cms-primary)]" aria-hidden="true" />
                <span className="text-[var(--cms-muted-fg)]">7 天满签合计：</span>
                <strong>{totalCoins.toLocaleString()} 金币</strong>
              </div>
              <span className="text-[var(--cms-border)]">·</span>
              <div className="text-[var(--cms-muted-fg)]">
                日均 <strong className="text-[var(--cms-fg)]">{avgCoins}</strong> 币
              </div>
              <span className="text-[var(--cms-border)]">·</span>
              <div className="text-[var(--cms-muted-fg)]">
                第 7 天峰值 <strong className="text-[var(--cms-primary)]">+{safeNumbers[6]}</strong> 币
              </div>
            </div>

            {canEdit && (
              <div className="flex flex-wrap items-center gap-1.5">
                <span className="text-[11px] text-[var(--cms-muted-fg)] inline-flex items-center gap-1 mr-1">
                  <Sparkles size={11} aria-hidden="true" />
                  快捷梯度：
                </span>
                {CHECKIN_PRESETS.map((preset) => (
                  <button
                    key={preset.label}
                    type="button"
                    onClick={() => onApplyPreset(preset.rewards)}
                    className="btn-secondary h-7 px-2.5 text-[11px] rounded-lg"
                  >
                    {preset.label}
                  </button>
                ))}
              </div>
            )}
          </div>

          {/* 7-Day Structured Cards Grid */}
          <div className="grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-7 gap-3">
            {rewardInputs.map((rawVal, idx) => {
              const parsed = parsedRewards[idx];
              const isValid = parsed !== null;
              const isDay7 = idx === 6;
              const barPct = isValid ? Math.max(8, Math.round((parsed / maxDayCoins) * 100)) : 0;

              return (
                <label
                  key={`day-${idx + 1}`}
                  className={`p-3 rounded-xl border flex flex-col gap-2 transition-colors ${
                    !isValid
                      ? 'border-[var(--cms-error)] bg-[color-mix(in_srgb,var(--cms-error)_6%,var(--cms-card))]'
                      : isDay7
                        ? 'border-[var(--cms-primary)]/50 bg-[color-mix(in_srgb,var(--cms-primary)_5%,var(--cms-card))]'
                        : 'border-[var(--cms-border)] bg-[var(--cms-content)]/60'
                  }`}
                >
                  <div className="flex items-center justify-between gap-1 text-xs">
                    <span className={`font-semibold tabular-nums ${isDay7 ? 'text-[var(--cms-primary)]' : 'text-[var(--cms-fg)]'}`}>
                      第 {idx + 1} 天
                    </span>
                    <span className="text-[11px] text-[var(--cms-muted-fg)] tabular-nums">D{idx + 1}</span>
                  </div>

                  <div className="relative">
                    <input
                      type="number"
                      min={1}
                      max={10000}
                      step={1}
                      disabled={!canEdit}
                      aria-label={`第 ${idx + 1} 天签到奖励金币数`}
                      className={`input-field h-9 pr-7 tabular-nums font-semibold ${
                        !isValid ? 'border-[var(--cms-error)]' : ''
                      }`}
                      value={rawVal}
                      onChange={(e) => onChangeReward(idx, e.target.value)}
                    />
                    <span className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[11px] text-[var(--cms-muted-fg)] pointer-events-none">
                      币
                    </span>
                  </div>

                  <div className="h-1.5 rounded-full bg-[var(--cms-secondary)] overflow-hidden">
                    <div
                      className="h-full rounded-full transition-all"
                      style={{
                        width: `${barPct}%`,
                        backgroundColor: !isValid
                          ? 'var(--cms-error)'
                          : isDay7
                            ? 'var(--cms-primary)'
                            : 'color-mix(in srgb, var(--cms-primary) 65%, transparent)',
                      }}
                    />
                  </div>
                </label>
              );
            })}
          </div>

          {hasInvalid && (
            <div
              role="alert"
              className="px-3.5 py-2.5 rounded-xl border text-xs font-medium"
              style={{
                backgroundColor: 'color-mix(in srgb, var(--cms-error) 10%, transparent)',
                borderColor: 'color-mix(in srgb, var(--cms-error) 28%, transparent)',
                color: 'var(--cms-error)',
              }}
            >
              每天签到奖励必须为 1 至 10,000 的整数，请检查标红天数。
            </div>
          )}
        </div>
      )}
    </div>
  );
};

import React, { useEffect, useId, useState } from 'react';
import { CalendarClock, Infinity as InfinityIcon } from 'lucide-react';
import { CmsDialog } from '../ui/CmsDialog';
import type { RecommendationEntry } from '../../utils/api';
import {
  FALLBACK_COVER,
  getBrowserTimeZoneLabel,
  toISOOrNull,
  toLocalInputValue,
} from './operationsUtils';

interface ScheduleModalProps {
  open: boolean;
  targets: RecommendationEntry[];
  onClose: () => void;
  onApply: (scheduledStart: string | null, scheduledEnd: string | null) => void;
}

type ScheduleMode = 'always' | 'window';

export const ScheduleModal: React.FC<ScheduleModalProps> = ({
  open,
  targets,
  onClose,
  onApply,
}) => {
  const formId = useId();
  const [mode, setMode] = useState<ScheduleMode>('always');
  const [startLocal, setStartLocal] = useState('');
  const [endLocal, setEndLocal] = useState('');
  const [errorMsg, setErrorMsg] = useState('');

  useEffect(() => {
    if (!open || targets.length === 0) return;
    setErrorMsg('');
    if (targets.length === 1) {
      const first = targets[0];
      const s = toLocalInputValue(first.scheduled_start);
      const e = toLocalInputValue(first.scheduled_end);
      setMode(s || e ? 'window' : 'always');
      setStartLocal(s);
      setEndLocal(e);
    } else {
      setMode('window');
      const now = new Date();
      const nextWeek = new Date(now.getTime() + 7 * 24 * 60 * 60 * 1000);
      setStartLocal(toLocalInputValue(now.toISOString()));
      setEndLocal(toLocalInputValue(nextWeek.toISOString()));
    }
  }, [open, targets]);

  const applyPreset = (preset: '7d' | '30d' | 'weekend' | 'tomorrow_open') => {
    setErrorMsg('');
    setMode('window');
    const now = new Date();
    if (preset === '7d') {
      const end = new Date(now.getTime() + 7 * 24 * 60 * 60 * 1000);
      setStartLocal(toLocalInputValue(now.toISOString()));
      setEndLocal(toLocalInputValue(end.toISOString()));
      return;
    }
    if (preset === '30d') {
      const end = new Date(now.getTime() + 30 * 24 * 60 * 60 * 1000);
      setStartLocal(toLocalInputValue(now.toISOString()));
      setEndLocal(toLocalInputValue(end.toISOString()));
      return;
    }
    if (preset === 'tomorrow_open') {
      const tomorrow = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1, 0, 0, 0, 0);
      setStartLocal(toLocalInputValue(tomorrow.toISOString()));
      setEndLocal('');
      return;
    }
    // weekend: upcoming Saturday 00:00 -> Sunday 23:59
    const day = now.getDay(); // 0 Sun .. 6 Sat
    const daysUntilSat = day === 6 ? 0 : (6 - day + 7) % 7;
    const satStart = new Date(now.getFullYear(), now.getMonth(), now.getDate() + daysUntilSat, 0, 0, 0, 0);
    const sunEnd = new Date(satStart.getFullYear(), satStart.getMonth(), satStart.getDate() + 1, 23, 59, 0, 0);
    setStartLocal(toLocalInputValue(satStart.toISOString()));
    setEndLocal(toLocalInputValue(sunEnd.toISOString()));
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (mode === 'always') {
      onApply(null, null);
      onClose();
      return;
    }

    const startIso = toISOOrNull(startLocal);
    const endIso = toISOOrNull(endLocal);
    if (!startIso && !endIso) {
      setErrorMsg('请至少填写生效时间或失效时间，或切换为「长期有效」');
      return;
    }
    if (startIso && endIso && new Date(endIso).getTime() < new Date(startIso).getTime()) {
      setErrorMsg('失效时间不能早于生效时间');
      return;
    }
    onApply(startIso, endIso);
    onClose();
  };

  const isBatch = targets.length > 1;
  const singleNovel = targets.length === 1 ? targets[0].novel : undefined;

  return (
    <CmsDialog
      open={open}
      onClose={onClose}
      variant="modal"
      size="sm"
      closedBy="any"
      title={isBatch ? `批量设置排期（已选 ${targets.length} 本）` : '设置展示排期'}
      subtitle={
        isBatch
          ? '应用后将统一覆盖所选书籍的生效与失效时间'
          : singleNovel?.title || (targets[0] ? `Book #${targets[0].novel_id}` : '')
      }
      footer={
        <>
          <button type="button" onClick={onClose} className="btn-secondary">
            取消
          </button>
          <button type="submit" form={formId} className="btn-primary">
            应用排期
          </button>
        </>
      }
    >
      <form id={formId} onSubmit={handleSubmit} className="space-y-4 text-xs">
        {!isBatch && targets[0] && (
          <div className="p-3 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-content)] flex items-center gap-3">
            <img
              src={singleNovel?.cover_url || FALLBACK_COVER}
              alt={singleNovel?.title || String(targets[0].novel_id)}
              className="w-9 h-12 object-cover rounded-md border border-[var(--cms-border)] shrink-0"
            />
            <div className="min-w-0 flex-1">
              <div className="text-[0.8125rem] font-medium text-[var(--cms-fg)] truncate">
                {singleNovel?.title || `Book #${targets[0].novel_id}`}
              </div>
              <div className="text-[var(--cms-muted-fg)] mt-0.5 truncate tabular-nums">
                #{targets[0].novel_id} · {singleNovel?.author || '-'}
              </div>
            </div>
          </div>
        )}

        <div className="space-y-1.5">
          <span className="block font-medium text-[var(--cms-muted-fg)]">生效方式</span>
          <div
            role="group"
            aria-label="生效方式"
            className="grid grid-cols-2 gap-1 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] p-1"
          >
            <button
              type="button"
              aria-pressed={mode === 'always'}
              onClick={() => {
                setErrorMsg('');
                setMode('always');
              }}
              className={`focus-ring py-2 rounded-lg font-medium inline-flex items-center justify-center gap-1.5 cursor-pointer transition-colors ${
                mode === 'always'
                  ? 'bg-[var(--cms-card)] text-[var(--cms-fg)] shadow-xs'
                  : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
              }`}
            >
              <InfinityIcon size={14} aria-hidden="true" />
              <span>长期有效</span>
            </button>
            <button
              type="button"
              aria-pressed={mode === 'window'}
              onClick={() => {
                setErrorMsg('');
                setMode('window');
              }}
              className={`focus-ring py-2 rounded-lg font-medium inline-flex items-center justify-center gap-1.5 cursor-pointer transition-colors ${
                mode === 'window'
                  ? 'bg-[var(--cms-card)] text-[var(--cms-fg)] shadow-xs'
                  : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
              }`}
            >
              <CalendarClock size={14} aria-hidden="true" />
              <span>限定时间段</span>
            </button>
          </div>
        </div>

        {mode === 'always' ? (
          <div className="p-3.5 rounded-xl border border-dashed border-[var(--cms-border)] text-[var(--cms-muted-fg)] leading-relaxed">
            不限制开始和结束时间。保存配置后，只要书籍处于「已上架」状态，将一直在 H5 端对应位置生效展示。
          </div>
        ) : (
          <div className="space-y-3.5">
            <div className="space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">快捷预设</span>
              <div className="flex flex-wrap gap-1.5">
                <button
                  type="button"
                  onClick={() => applyPreset('7d')}
                  className="btn-secondary h-7 px-2.5 text-[11px] rounded-lg"
                >
                  立即起 · 7 天
                </button>
                <button
                  type="button"
                  onClick={() => applyPreset('30d')}
                  className="btn-secondary h-7 px-2.5 text-[11px] rounded-lg"
                >
                  立即起 · 30 天
                </button>
                <button
                  type="button"
                  onClick={() => applyPreset('weekend')}
                  className="btn-secondary h-7 px-2.5 text-[11px] rounded-lg"
                >
                  本周末窗口
                </button>
                <button
                  type="button"
                  onClick={() => applyPreset('tomorrow_open')}
                  className="btn-secondary h-7 px-2.5 text-[11px] rounded-lg"
                >
                  明日 0 点起长期
                </button>
              </div>
            </div>

            <label className="block space-y-1.5">
              <div className="flex items-center justify-between">
                <span className="font-medium text-[var(--cms-muted-fg)]">生效时间（可选，留空表示立即生效）</span>
                {startLocal && (
                  <button
                    type="button"
                    onClick={() => {
                      setErrorMsg('');
                      setStartLocal('');
                    }}
                    className="text-[11px] text-[var(--cms-detail)] hover:underline cursor-pointer"
                  >
                    清空
                  </button>
                )}
              </div>
              <input
                type="datetime-local"
                className="input-field tabular-nums"
                value={startLocal}
                onChange={(e) => {
                  setErrorMsg('');
                  setStartLocal(e.target.value);
                }}
              />
            </label>

            <label className="block space-y-1.5">
              <div className="flex items-center justify-between">
                <span className="font-medium text-[var(--cms-muted-fg)]">失效时间（可选，留空表示永久不过期）</span>
                {endLocal && (
                  <button
                    type="button"
                    onClick={() => {
                      setErrorMsg('');
                      setEndLocal('');
                    }}
                    className="text-[11px] text-[var(--cms-detail)] hover:underline cursor-pointer"
                  >
                    清空
                  </button>
                )}
              </div>
              <input
                type="datetime-local"
                className="input-field tabular-nums"
                value={endLocal}
                onChange={(e) => {
                  setErrorMsg('');
                  setEndLocal(e.target.value);
                }}
              />
            </label>

            <p className="text-[11px] text-[var(--cms-muted-fg)] m-0">
              当前输入按本地时区 <span className="font-mono-code">{getBrowserTimeZoneLabel()}</span> 解析。
            </p>
          </div>
        )}

        {errorMsg && (
          <div
            role="alert"
            className="px-3 py-2 rounded-xl border text-xs font-medium"
            style={{
              backgroundColor: 'color-mix(in srgb, var(--cms-error) 10%, transparent)',
              borderColor: 'color-mix(in srgb, var(--cms-error) 28%, transparent)',
              color: 'var(--cms-error)',
            }}
          >
            {errorMsg}
          </div>
        )}
      </form>
    </CmsDialog>
  );
};

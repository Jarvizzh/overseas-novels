import type {
  GroupItemInput,
  RankingConfig,
  RecommendationEntry,
  RecommendationGroupName,
} from '../../utils/api';

export const MAX_GROUP_ITEMS = 100;

export const FALLBACK_COVER =
  'data:image/svg+xml;utf8,' +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 160"><rect width="120" height="160" fill="#1e293b"/><text x="60" y="84" fill="#94a3b8" font-family="sans-serif" font-size="12" text-anchor="middle">No Cover</text></svg>',
  );

export type EntryHealthKey = 'live' | 'scheduled' | 'expired' | 'unpublished' | 'truncated';
export type DotTone = 'success' | 'warning' | 'error' | 'primary' | 'muted';

export interface AnalyzedEntry {
  entry: RecommendationEntry;
  index: number;
  status: EntryHealthKey;
  tone: DotTone;
  label: string;
  detail: string;
  liveRank: number | null;
}

export interface GroupHealthSummary {
  analyzed: AnalyzedEntry[];
  liveCount: number;
  scheduledCount: number;
  expiredCount: number;
  unpublishedCount: number;
  truncatedCount: number;
  staleCount: number; // expired + unpublished
}

export function toLocalInputValue(iso?: string | null): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const offset = d.getTimezoneOffset();
  const local = new Date(d.getTime() - offset * 60_000);
  return local.toISOString().slice(0, 16);
}

export function toISOOrNull(localVal?: string | null): string | null {
  if (!localVal || !localVal.trim()) return null;
  const d = new Date(localVal);
  if (Number.isNaN(d.getTime())) return null;
  return d.toISOString();
}

export function formatCompactDateTime(iso?: string | null): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const month = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  const hours = String(d.getHours()).padStart(2, '0');
  const minutes = String(d.getMinutes()).padStart(2, '0');
  return `${month}/${day} ${hours}:${minutes}`;
}

export function formatScheduleSummary(start?: string | null, end?: string | null): string {
  const s = formatCompactDateTime(start);
  const e = formatCompactDateTime(end);
  if (!s && !e) return '长期有效';
  if (s && e) return `${s} → ${e}`;
  if (s) return `${s} 起生效`;
  return `至 ${e} 止`;
}

export function formatCompactNumber(val: number): string {
  if (val >= 1_000_000) return `${(val / 1_000_000).toFixed(1)}M`;
  if (val >= 1_000) return `${(val / 1_000).toFixed(1)}k`;
  return String(val);
}

export function normalizeTimestampMs(iso?: string | null): number | null {
  if (!iso) return null;
  const ms = Date.parse(iso);
  return Number.isNaN(ms) ? null : ms;
}

export function cloneEntries(entries: RecommendationEntry[]): RecommendationEntry[] {
  return entries.map((e, idx) => ({
    ...e,
    sort_order: idx,
    novel: e.novel ? { ...e.novel, genres: [...(e.novel.genres || [])] } : undefined,
  }));
}

export function toGroupInputs(entries: RecommendationEntry[]): GroupItemInput[] {
  return entries.map((item, idx) => ({
    novel_id: item.novel_id,
    sort_order: idx,
    scheduled_start: item.scheduled_start || null,
    scheduled_end: item.scheduled_end || null,
  }));
}

export function areGroupEntriesEqual(a: RecommendationEntry[], b: RecommendationEntry[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i += 1) {
    const x = a[i];
    const y = b[i];
    if (x.novel_id !== y.novel_id) return false;
    if (normalizeTimestampMs(x.scheduled_start) !== normalizeTimestampMs(y.scheduled_start)) return false;
    if (normalizeTimestampMs(x.scheduled_end) !== normalizeTimestampMs(y.scheduled_end)) return false;
  }
  return true;
}

export function isRankingConfigEqual(a: RankingConfig, b: RankingConfig): boolean {
  return a.sort_rule === b.sort_rule && a.limit === b.limit;
}

export function analyzeGroupEntries(
  entries: RecommendationEntry[],
  groupName: RecommendationGroupName,
  referenceTimeMs: number,
  rankingLimit: number,
): GroupHealthSummary {
  let liveCount = 0;
  let scheduledCount = 0;
  let expiredCount = 0;
  let unpublishedCount = 0;
  let truncatedCount = 0;
  let activeCounter = 0;

  const analyzed: AnalyzedEntry[] = entries.map((entry, index) => {
    const novel = entry.novel;
    if (novel && (novel.publish_status !== 'published' || novel.status === 'archived')) {
      unpublishedCount += 1;
      return {
        entry,
        index,
        status: 'unpublished',
        tone: 'warning',
        label: '未上架',
        detail: '书籍非已上架状态，H5 端不会展示',
        liveRank: null,
      };
    }

    const startMs = normalizeTimestampMs(entry.scheduled_start);
    const endMs = normalizeTimestampMs(entry.scheduled_end);

    if (startMs !== null && referenceTimeMs < startMs) {
      scheduledCount += 1;
      return {
        entry,
        index,
        status: 'scheduled',
        tone: 'primary',
        label: '待生效',
        detail: `将于 ${formatCompactDateTime(entry.scheduled_start)} 自动上线`,
        liveRank: null,
      };
    }

    if (endMs !== null && referenceTimeMs > endMs) {
      expiredCount += 1;
      return {
        entry,
        index,
        status: 'expired',
        tone: 'muted',
        label: '已过期',
        detail: `已于 ${formatCompactDateTime(entry.scheduled_end)} 失效，H5 不展示`,
        liveRank: null,
      };
    }

    activeCounter += 1;
    if (groupName === 'ranking' && activeCounter > rankingLimit) {
      truncatedCount += 1;
      return {
        entry,
        index,
        status: 'truncated',
        tone: 'warning',
        label: '超出上限',
        detail: `超出前 ${rankingLimit} 名展示上限，H5 端会被截断`,
        liveRank: null,
      };
    }

    liveCount += 1;
    return {
      entry,
      index,
      status: 'live',
      tone: 'success',
      label: '生效中',
      detail: `当前 H5 实际展示第 ${activeCounter} 位`,
      liveRank: activeCounter,
    };
  });

  return {
    analyzed,
    liveCount,
    scheduledCount,
    expiredCount,
    unpublishedCount,
    truncatedCount,
    staleCount: expiredCount + unpublishedCount,
  };
}

export function getBrowserTimeZoneLabel(): string {
  try {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    const offsetMin = -new Date().getTimezoneOffset();
    const sign = offsetMin >= 0 ? '+' : '-';
    const absHours = Math.floor(Math.abs(offsetMin) / 60);
    const absMins = Math.abs(offsetMin) % 60;
    const offsetStr = absMins > 0 ? `UTC${sign}${absHours}:${String(absMins).padStart(2, '0')}` : `UTC${sign}${absHours}`;
    return tz ? `${tz} (${offsetStr})` : offsetStr;
  } catch {
    return '本地时区';
  }
}

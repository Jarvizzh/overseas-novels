import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  BookOpen,
  CalendarClock,
  ChevronDown,
  ChevronRight,
  Eye,
  EyeOff,
  Flame,
  Gift,
  GripVertical,
  Pin,
  Plus,
  RotateCcw,
  Save,
  Sparkles,
  Star,
  Trophy,
} from 'lucide-react';
import CustomSelect from '../components/CustomSelect';
import { BookPickerDrawer } from '../components/operations/BookPickerDrawer';
import { CampaignsPanel } from '../components/operations/CampaignsPanel';
import { H5RewardsPreview } from '../components/operations/H5RewardsPreview';
import { H5StorefrontPreview } from '../components/operations/H5StorefrontPreview';
import { MoveRankModal } from '../components/operations/MoveRankModal';
import { ScheduleModal } from '../components/operations/ScheduleModal';
import {
  FALLBACK_COVER,
  MAX_GROUP_ITEMS,
  analyzeGroupEntries,
  areGroupEntriesEqual,
  cloneEntries,
  formatCompactNumber,
  formatScheduleSummary,
  isRankingConfigEqual,
  toGroupInputs,
} from '../components/operations/operationsUtils';
import { RowActionMenu, StatusDot } from '../components/ui/Primitives';
import {
  operationsApi,
  type OperationsPreviewResponse,
  type RankingConfig,
  type RankingSortRule,
  type RecommendationEntry,
  type RecommendationGroupName,
  type RecommendationNovel,
} from '../utils/api';

type OpsSubTab = 'recommendations' | 'rankings' | 'campaigns';
type RecSlot = 'editor_pick' | 'curated';

const DEFAULT_CHECKIN_REWARDS = [10, 15, 20, 25, 30, 35, 50];

const RANKING_SORT_OPTIONS: Array<{ value: RankingSortRule; label: string }> = [
  { value: 'views', label: '按阅读量降序（默认）' },
  { value: 'rating', label: '按评分降序' },
  { value: 'latest', label: '按最新上架降序' },
  { value: 'manual', label: '仅展示人工配置榜单' },
];

const SLOT_LABELS: Record<RecommendationGroupName, string> = {
  editor_pick: '焦点轮播 · Featured Stories',
  curated: '编辑书单 · Curated For You',
  ranking: '人气排名榜置顶 · Top Charts',
};

const EMPTY_GROUPS: Record<RecommendationGroupName, RecommendationEntry[]> = {
  editor_pick: [],
  curated: [],
  ranking: [],
};

const DEFAULT_RANKING_CONFIG: RankingConfig = {
  sort_rule: 'views',
  limit: 20,
};

export default function OperationsTab() {
  const currentAdminRole = (() => {
    try {
      const saved = localStorage.getItem('cms_admin');
      return saved ? JSON.parse(saved).role || '' : '';
    } catch {
      return '';
    }
  })();
  const canEdit = ['SuperAdmin', 'Admin', 'Editor'].includes(currentAdminRole);

  const [searchParams, setSearchParams] = useSearchParams();
  const initialTabParam = searchParams.get('tab');
  const resolvedUrlTab: OpsSubTab =
    initialTabParam === 'rankings' || initialTabParam === 'campaigns' ? initialTabParam : 'recommendations';

  const [activeTab, setActiveTabState] = useState<OpsSubTab>(resolvedUrlTab);
  const [activeRecSlot, setActiveRecSlot] = useState<RecSlot>('editor_pick');
  const [showPreview, setShowPreview] = useState(true);
  const [h5Theme, setH5Theme] = useState<'light' | 'dark'>('light');

  const setActiveTab = (next: OpsSubTab) => {
    setActiveTabState(next);
    if (next === 'recommendations') {
      setSearchParams({}, { replace: true });
    } else {
      setSearchParams({ tab: next }, { replace: true });
    }
  };

  useEffect(() => {
    if (initialTabParam === 'rankings' || initialTabParam === 'campaigns' || initialTabParam === 'recommendations') {
      setActiveTabState(initialTabParam);
    }
  }, [initialTabParam]);

  const [loading, setLoading] = useState(true);
  const [savingSlot, setSavingSlot] = useState<RecommendationGroupName | null>(null);
  const [savingCheckIn, setSavingCheckIn] = useState(false);

  // Server snapshots vs independent drafts
  const [serverGroups, setServerGroups] = useState<Record<RecommendationGroupName, RecommendationEntry[]>>(EMPTY_GROUPS);
  const [draftGroups, setDraftGroups] = useState<Record<RecommendationGroupName, RecommendationEntry[]>>(EMPTY_GROUPS);

  const [serverRankingConfig, setServerRankingConfig] = useState<RankingConfig>(DEFAULT_RANKING_CONFIG);
  const [draftSortRule, setDraftSortRule] = useState<RankingSortRule>('views');
  const [rankingLimitInput, setRankingLimitInput] = useState<string>('20');
  const [availableGenres, setAvailableGenres] = useState<string[]>([]);

  const [serverCheckInRewards, setServerCheckInRewards] = useState<number[]>(DEFAULT_CHECKIN_REWARDS);
  const [draftCheckInInputs, setDraftCheckInInputs] = useState<string[]>(
    DEFAULT_CHECKIN_REWARDS.map(String),
  );

  // H5 live simulation state
  const [previewAtIso, setPreviewAtIso] = useState<string | null>(null);
  const [previewData, setPreviewData] = useState<OperationsPreviewResponse | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState('');
  const previewAbortRef = useRef<AbortController | null>(null);
  const previewSeqRef = useRef(0);
  const previewInitializedRef = useRef(false);

  // List interaction state
  const [focusedNovelId, setFocusedNovelId] = useState<number | null>(null);
  const [selectedIdsByGroup, setSelectedIdsByGroup] = useState<Record<RecommendationGroupName, number[]>>({
    editor_pick: [],
    curated: [],
    ranking: [],
  });
  const [dragIdx, setDragIdx] = useState<number | null>(null);
  const [dragOverIdx, setDragOverIdx] = useState<number | null>(null);
  const [reorderStatus, setReorderStatus] = useState('');
  const [autoBackfillExpanded, setAutoBackfillExpanded] = useState(true);

  // Modals & Drawers
  const [pickerOpen, setPickerOpen] = useState(false);
  const [scheduleTargets, setScheduleTargets] = useState<RecommendationEntry[]>([]);
  const [scheduleGroup, setScheduleGroup] = useState<RecommendationGroupName | null>(null);
  const [moveRankTarget, setMoveRankTarget] = useState<{
    group: RecommendationGroupName;
    entry: RecommendationEntry;
    index: number;
  } | null>(null);

  const limitInputId = useId();
  const sortRuleSelectId = useId();

  const currentGroupName: RecommendationGroupName = activeTab === 'rankings' ? 'ranking' : activeRecSlot;
  const currentEntries = draftGroups[currentGroupName] || [];
  const selectedIds = selectedIdsByGroup[currentGroupName] || [];

  const parsedLimit = useMemo(() => {
    const trimmed = rankingLimitInput.trim();
    if (!trimmed) return null;
    const n = Number(trimmed);
    if (!Number.isInteger(n) || n < 1 || n > 100) return null;
    return n;
  }, [rankingLimitInput]);

  const effectivePreviewLimit = parsedLimit ?? serverRankingConfig.limit;

  const draftRankingConfig: RankingConfig = useMemo(
    () => ({
      sort_rule: draftSortRule,
      limit: effectivePreviewLimit,
    }),
    [draftSortRule, effectivePreviewLimit],
  );

  // Per-module dirty flags (independent saving)
  const editorPickDirty = useMemo(
    () => !areGroupEntriesEqual(serverGroups.editor_pick, draftGroups.editor_pick),
    [serverGroups.editor_pick, draftGroups.editor_pick],
  );
  const curatedDirty = useMemo(
    () => !areGroupEntriesEqual(serverGroups.curated, draftGroups.curated),
    [serverGroups.curated, draftGroups.curated],
  );
  const rankingListDirty = useMemo(
    () => !areGroupEntriesEqual(serverGroups.ranking, draftGroups.ranking),
    [serverGroups.ranking, draftGroups.ranking],
  );
  const rankingConfigDirty = useMemo(
    () =>
      parsedLimit === null
        ? rankingLimitInput.trim() !== String(serverRankingConfig.limit) ||
          draftSortRule !== serverRankingConfig.sort_rule
        : !isRankingConfigEqual(serverRankingConfig, { sort_rule: draftSortRule, limit: parsedLimit }),
    [serverRankingConfig, draftSortRule, parsedLimit, rankingLimitInput],
  );
  const topChartsDirty = rankingListDirty || rankingConfigDirty;
  const forYouDirty = editorPickDirty || curatedDirty;
  const checkInDirty = useMemo(
    () =>
      draftCheckInInputs.length !== serverCheckInRewards.length ||
      draftCheckInInputs.some((raw, idx) => raw.trim() !== String(serverCheckInRewards[idx])),
    [draftCheckInInputs, serverCheckInRewards],
  );
  const hasAnyUnsaved = forYouDirty || topChartsDirty || checkInDirty;

  useEffect(() => {
    if (!hasAnyUnsaved) return;
    const handleBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = '';
    };
    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => window.removeEventListener('beforeunload', handleBeforeUnload);
  }, [hasAnyUnsaved]);

  const loadConfig = useCallback(async () => {
    setLoading(true);
    try {
      const data = await operationsApi.getConfig();
      const cfg: RankingConfig = {
        sort_rule: data.ranking_config?.sort_rule || 'views',
        limit: data.ranking_config?.limit || 20,
      };
      const loadedGroups: Record<RecommendationGroupName, RecommendationEntry[]> = {
        editor_pick: data.groups?.editor_pick || [],
        curated: data.groups?.curated || [],
        ranking: data.groups?.ranking || [],
      };
      setServerRankingConfig(cfg);
      setDraftSortRule(cfg.sort_rule);
      setRankingLimitInput(String(cfg.limit));
      setServerGroups(cloneEntriesRecord(loadedGroups));
      setDraftGroups(cloneEntriesRecord(loadedGroups));
      setAvailableGenres(data.available_genres || []);
      const checkInRewards =
        Array.isArray(data.checkin_config?.rewards) && data.checkin_config.rewards.length === 7
          ? data.checkin_config.rewards
          : DEFAULT_CHECKIN_REWARDS;
      setServerCheckInRewards(checkInRewards);
      setDraftCheckInInputs(checkInRewards.map(String));
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '加载运营配置失败', 'error');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadConfig();
    return () => previewAbortRef.current?.abort();
  }, [loadConfig]);

  // Debounced live H5 simulation against backend /operations/preview
  const runPreview = useCallback(
    async (
      groupsSnapshot: Record<RecommendationGroupName, RecommendationEntry[]>,
      rankingCfgSnapshot: RankingConfig,
      atIso: string | null,
    ) => {
      previewAbortRef.current?.abort();
      const controller = new AbortController();
      previewAbortRef.current = controller;
      const seq = ++previewSeqRef.current;

      setPreviewLoading(true);
      setPreviewError('');
      try {
        const res = await operationsApi.preview(
          {
            at: atIso || undefined,
            ranking_config: rankingCfgSnapshot,
            groups: {
              editor_pick: toGroupInputs(groupsSnapshot.editor_pick),
              curated: toGroupInputs(groupsSnapshot.curated),
              ranking: toGroupInputs(groupsSnapshot.ranking),
            },
          },
          controller.signal,
        );
        if (controller.signal.aborted || seq !== previewSeqRef.current) return;
        setPreviewData(res);
      } catch (err: unknown) {
        if ((err as { name?: string })?.name === 'AbortError' || controller.signal.aborted || seq !== previewSeqRef.current) {
          return;
        }
        setPreviewError((err instanceof Error ? err.message : '') || '实时预览加载失败');
      } finally {
        if (seq === previewSeqRef.current) {
          setPreviewLoading(false);
        }
      }
    },
    [],
  );

  useEffect(() => {
    if (loading) return;
    if (!previewInitializedRef.current) {
      previewInitializedRef.current = true;
      void runPreview(draftGroups, draftRankingConfig, previewAtIso);
      return;
    }
    const timer = window.setTimeout(() => {
      void runPreview(draftGroups, draftRankingConfig, previewAtIso);
    }, 220);
    return () => window.clearTimeout(timer);
  }, [loading, draftGroups, draftRankingConfig, previewAtIso, runPreview]);

  // Reference time for health dots matches preview time-travel simulator
  const referenceTimeMs = useMemo(() => {
    if (previewAtIso) {
      const parsed = Date.parse(previewAtIso);
      if (!Number.isNaN(parsed)) return parsed;
    }
    return Date.now();
  }, [previewAtIso]);

  const healthByGroup = useMemo(
    () => ({
      editor_pick: analyzeGroupEntries(draftGroups.editor_pick, 'editor_pick', referenceTimeMs, effectivePreviewLimit),
      curated: analyzeGroupEntries(draftGroups.curated, 'curated', referenceTimeMs, effectivePreviewLimit),
      ranking: analyzeGroupEntries(draftGroups.ranking, 'ranking', referenceTimeMs, effectivePreviewLimit),
    }),
    [draftGroups, referenceTimeMs, effectivePreviewLimit],
  );

  const currentHealth = healthByGroup[currentGroupName];

  const updateGroupEntries = (
    group: RecommendationGroupName,
    updater: (prev: RecommendationEntry[]) => RecommendationEntry[],
  ) => {
    setDraftGroups((prev) => ({
      ...prev,
      [group]: updater(prev[group] || []).map((item, idx) => ({ ...item, sort_order: idx })),
    }));
  };

  const handleAddBooksToGroup = (
    group: RecommendationGroupName,
    novelsToAdd: RecommendationNovel[],
    position: 'append' | 'prepend',
  ) => {
    if (!canEdit || novelsToAdd.length === 0) return;
    const existing = draftGroups[group] || [];
    const existingIds = new Set(existing.map((e) => e.novel_id));
    const uniqueNew = novelsToAdd.filter((n) => !existingIds.has(n.id));
    if (uniqueNew.length === 0) {
      window.showToast?.('所选书籍已在当前列表中', 'info');
      return;
    }

    const availableSpace = Math.max(0, MAX_GROUP_ITEMS - existing.length);
    if (availableSpace === 0) {
      window.showToast?.(`单个栏位最多配置 ${MAX_GROUP_ITEMS} 本书籍`, 'error');
      return;
    }

    const accepted = uniqueNew.slice(0, availableSpace);
    const createdEntries: RecommendationEntry[] = accepted.map((novel, idx) => ({
      group_name: group,
      novel_id: novel.id,
      sort_order: idx,
      scheduled_start: null,
      scheduled_end: null,
      novel,
    }));

    updateGroupEntries(group, (prev) =>
      position === 'prepend' ? [...createdEntries, ...prev] : [...prev, ...createdEntries],
    );
    setFocusedNovelId(accepted[0].id);

    if (accepted.length < uniqueNew.length) {
      window.showToast?.(`已添加 ${accepted.length} 本（受单栏位 ${MAX_GROUP_ITEMS} 本上限限制）`, 'info');
    } else {
      window.showToast?.(`已添加 ${accepted.length} 本书籍到草稿`, 'success');
    }
  };

  const reorderInGroup = (group: RecommendationGroupName, fromIdx: number, toIdx: number) => {
    if (!canEdit) return;
    const list = draftGroups[group] || [];
    if (fromIdx === toIdx || fromIdx < 0 || toIdx < 0 || fromIdx >= list.length || toIdx >= list.length) {
      return;
    }
    const movedTitle = list[fromIdx]?.novel?.title || `Book #${list[fromIdx]?.novel_id}`;
    updateGroupEntries(group, (prev) => {
      const next = [...prev];
      const [moved] = next.splice(fromIdx, 1);
      next.splice(toIdx, 0, moved);
      return next;
    });
    setReorderStatus(`已将《${movedTitle}》调整至第 ${toIdx + 1} 位`);
  };

  const handleRemoveMany = (group: RecommendationGroupName, novelIds: number[]) => {
    if (!canEdit || novelIds.length === 0) return;
    const removeSet = new Set(novelIds);
    updateGroupEntries(group, (prev) => prev.filter((item) => !removeSet.has(item.novel_id)));
    setSelectedIdsByGroup((prev) => ({
      ...prev,
      [group]: (prev[group] || []).filter((id) => !removeSet.has(id)),
    }));
  };

  const handleApplySchedule = (startIso: string | null, endIso: string | null) => {
    if (!canEdit || !scheduleGroup || scheduleTargets.length === 0) return;
    const targetIds = new Set(scheduleTargets.map((t) => t.novel_id));
    updateGroupEntries(scheduleGroup, (prev) =>
      prev.map((item) =>
        targetIds.has(item.novel_id)
          ? { ...item, scheduled_start: startIso, scheduled_end: endIso }
          : item,
      ),
    );
    window.showToast?.(
      scheduleTargets.length > 1
        ? `已批量更新 ${scheduleTargets.length} 本书籍的排期（未保存）`
        : '已更新排期设置（未保存）',
      'info',
    );
  };

  const handleCleanupStale = (group: RecommendationGroupName) => {
    if (!canEdit) return;
    const summary = healthByGroup[group];
    const staleIds = summary.analyzed
      .filter((a) => a.status === 'expired' || a.status === 'unpublished')
      .map((a) => a.entry.novel_id);
    if (staleIds.length === 0) return;
    handleRemoveMany(group, staleIds);
    window.showToast?.(`已从草稿清理 ${staleIds.length} 本失效/未上架书籍`, 'info');
  };

  const handleDiscardRecSlot = (slot: RecSlot) => {
    setDraftGroups((prev) => ({
      ...prev,
      [slot]: cloneEntries(serverGroups[slot]),
    }));
    setSelectedIdsByGroup((prev) => ({ ...prev, [slot]: [] }));
    window.showToast?.(`已放弃「${SLOT_LABELS[slot]}」的未保存更改`, 'info');
  };

  const handleDiscardTopCharts = () => {
    setDraftGroups((prev) => ({
      ...prev,
      ranking: cloneEntries(serverGroups.ranking),
    }));
    setDraftSortRule(serverRankingConfig.sort_rule);
    setRankingLimitInput(String(serverRankingConfig.limit));
    setSelectedIdsByGroup((prev) => ({ ...prev, ranking: [] }));
    window.showToast?.('已放弃「人气排名榜」的未保存更改', 'info');
  };

  const handleDiscardCheckIn = () => {
    setDraftCheckInInputs(serverCheckInRewards.map(String));
    window.showToast?.('已放弃「每日连续签到」的未保存更改', 'info');
  };

  const handleSaveCheckIn = async () => {
    if (!canEdit) return;
    const parsed: number[] = [];
    for (let i = 0; i < draftCheckInInputs.length; i += 1) {
      const n = Number(draftCheckInInputs[i].trim());
      if (!Number.isInteger(n) || n < 1 || n > 10000) {
        window.showToast?.(`第 ${i + 1} 天签到奖励须为 1 至 10,000 的整数`, 'error');
        return;
      }
      parsed.push(n);
    }
    setSavingCheckIn(true);
    try {
      const res = await operationsApi.saveCheckInConfig(parsed);
      const savedRewards =
        Array.isArray(res.checkin_config?.rewards) && res.checkin_config.rewards.length === 7
          ? res.checkin_config.rewards
          : parsed;
      setServerCheckInRewards(savedRewards);
      setDraftCheckInInputs(savedRewards.map(String));
      window.showToast?.('签到活动奖励梯度已保存并同步生效', 'success');
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '保存签到配置失败', 'error');
    } finally {
      setSavingCheckIn(false);
    }
  };

  const validateEntriesSchedule = (entries: RecommendationEntry[]): boolean => {
    for (const item of entries) {
      if (item.scheduled_start && item.scheduled_end && new Date(item.scheduled_end) < new Date(item.scheduled_start)) {
        window.showToast?.(`《${item.novel?.title || `ID:${item.novel_id}`}》的失效时间不能早于生效时间`, 'error');
        return false;
      }
    }
    return true;
  };

  const handleSaveRecSlot = async (slot: RecSlot) => {
    if (!canEdit) return;
    const entries = draftGroups[slot] || [];
    if (!validateEntriesSchedule(entries)) return;

    setSavingSlot(slot);
    try {
      const res = await operationsApi.saveGroup(slot, toGroupInputs(entries));
      const savedItems: RecommendationEntry[] = res.items || [];
      setServerGroups((prev) => ({ ...prev, [slot]: cloneEntries(savedItems) }));
      setDraftGroups((prev) => ({ ...prev, [slot]: cloneEntries(savedItems) }));
      window.showToast?.(`「${SLOT_LABELS[slot]}」已保存并即时同步至 H5 端`, 'success');
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '保存配置失败', 'error');
    } finally {
      setSavingSlot(null);
    }
  };

  const handleSaveTopCharts = async () => {
    if (!canEdit) return;
    if (parsedLimit === null) {
      window.showToast?.('榜单展示上限须为 1 至 100 的整数', 'error');
      return;
    }
    const entries = draftGroups.ranking || [];
    if (!validateEntriesSchedule(entries)) return;

    setSavingSlot('ranking');
    try {
      const res = await operationsApi.saveRanking({
        ranking_config: {
          sort_rule: draftSortRule,
          limit: parsedLimit,
        },
        items: toGroupInputs(entries),
      });
      const freshCfg: RankingConfig = {
        sort_rule: res.ranking_config?.sort_rule || draftSortRule,
        limit: res.ranking_config?.limit || parsedLimit,
      };
      const freshItems = res.items || [];
      setServerRankingConfig(freshCfg);
      setDraftSortRule(freshCfg.sort_rule);
      setRankingLimitInput(String(freshCfg.limit));
      setServerGroups((prev) => ({ ...prev, ranking: cloneEntries(freshItems) }));
      setDraftGroups((prev) => ({ ...prev, ranking: cloneEntries(freshItems) }));
      window.showToast?.('「人气排名榜」规则与置顶列表已独立保存并同步至 H5 端', 'success');
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '保存排名榜配置失败', 'error');
    } finally {
      setSavingSlot(null);
    }
  };

  const toggleSelectEntry = (novelId: number) => {
    setSelectedIdsByGroup((prev) => {
      const current = prev[currentGroupName] || [];
      const exists = current.includes(novelId);
      return {
        ...prev,
        [currentGroupName]: exists ? current.filter((id) => id !== novelId) : [...current, novelId],
      };
    });
  };

  const toggleSelectAllCurrent = () => {
    const allIds = currentEntries.map((e) => e.novel_id);
    const allSelected = allIds.length > 0 && allIds.every((id) => selectedIds.includes(id));
    setSelectedIdsByGroup((prev) => ({
      ...prev,
      [currentGroupName]: allSelected ? [] : allIds,
    }));
  };

  const handleDragStart = (e: React.DragEvent<HTMLElement>, idx: number) => {
    if (!canEdit) return;
    setDragIdx(idx);
    setDragOverIdx(idx);
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', String(idx));
  };

  const handleDragOver = (e: React.DragEvent<HTMLElement>, idx: number) => {
    if (!canEdit) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    if (dragOverIdx !== idx) setDragOverIdx(idx);
  };

  const handleDrop = (e: React.DragEvent<HTMLElement>, targetIdx: number) => {
    if (!canEdit) return;
    e.preventDefault();
    const raw = e.dataTransfer.getData('text/plain');
    const sourceIdx = dragIdx !== null ? dragIdx : raw !== '' ? Number(raw) : -1;
    if (sourceIdx >= 0 && sourceIdx < currentEntries.length && sourceIdx !== targetIdx) {
      reorderInGroup(currentGroupName, sourceIdx, targetIdx);
    }
    setDragIdx(null);
    setDragOverIdx(null);
  };

  const handleDragEnd = () => {
    setDragIdx(null);
    setDragOverIdx(null);
  };

  const autoBackfillRankings = useMemo(
    () => (previewData?.rankings || []).filter((item) => item.source !== 'pinned'),
    [previewData],
  );

  const isCurrentRecDirty = activeRecSlot === 'editor_pick' ? editorPickDirty : curatedDirty;
  const allCurrentSelected =
    currentEntries.length > 0 && currentEntries.every((e) => selectedIds.includes(e.novel_id));
  const selectedTargetsInCurrent = currentEntries.filter((e) => selectedIds.includes(e.novel_id));

  return (
    <div className="space-y-4 animate-fade-in">
      <p role="status" className="visually-hidden">
        {reorderStatus}
      </p>

      {/* Top Underlined Module Tabs + View Toggle */}
      <div className="border-b border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-3">
        <div role="tablist" aria-label="运营配置分类" className="flex items-center gap-6 overflow-x-auto">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'recommendations'}
            onClick={() => setActiveTab('recommendations')}
            className={`focus-ring pb-2.5 text-sm font-medium whitespace-nowrap cursor-pointer inline-flex items-center gap-2 transition-colors ${
              activeTab === 'recommendations'
                ? 'text-[var(--cms-fg)]'
                : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
            }`}
          >
            <BookOpen size={14} aria-hidden="true" />
            <span>为你推荐 · For You</span>
            {forYouDirty && (
              <span
                title="此模块有未保存改动"
                className="w-1.5 h-1.5 rounded-full bg-[var(--cms-warning)]"
                aria-hidden="true"
              />
            )}
          </button>

          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'rankings'}
            onClick={() => setActiveTab('rankings')}
            className={`focus-ring pb-2.5 text-sm font-medium whitespace-nowrap cursor-pointer inline-flex items-center gap-2 transition-colors ${
              activeTab === 'rankings'
                ? 'text-[var(--cms-fg)]'
                : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
            }`}
          >
            <Trophy size={14} aria-hidden="true" />
            <span>人气排名榜 · Top Charts</span>
            {topChartsDirty && (
              <span
                title="此模块有未保存改动"
                className="w-1.5 h-1.5 rounded-full bg-[var(--cms-warning)]"
                aria-hidden="true"
              />
            )}
          </button>

          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'campaigns'}
            onClick={() => setActiveTab('campaigns')}
            className={`focus-ring pb-2.5 text-sm font-medium whitespace-nowrap cursor-pointer inline-flex items-center gap-2 transition-colors ${
              activeTab === 'campaigns'
                ? 'text-[var(--cms-fg)]'
                : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
            }`}
          >
            <Gift size={14} aria-hidden="true" />
            <span>活动配置 · Campaigns</span>
            {checkInDirty && (
              <span
                title="此模块有未保存改动"
                className="w-1.5 h-1.5 rounded-full bg-[var(--cms-warning)]"
                aria-hidden="true"
              />
            )}
          </button>
        </div>

        <div className="pb-1.5 flex items-center gap-2">
          {!canEdit && <StatusDot tone="muted" label="只读查看模式" />}
          <button
            type="button"
            onClick={() => setShowPreview((v) => !v)}
            className="btn-secondary h-8 px-3 text-xs"
          >
            {showPreview ? <EyeOff size={13} aria-hidden="true" /> : <Eye size={13} aria-hidden="true" />}
            <span>{showPreview ? '收起 H5 预览' : '展开 H5 预览'}</span>
          </button>
        </div>
      </div>

      {loading ? (
        <div className="cms-card p-14 text-center text-xs text-[var(--cms-muted-fg)]">
          正在加载书城运营配置...
        </div>
      ) : (
        <div
          className={`grid grid-cols-1 gap-5 items-start ${
            showPreview ? 'xl:grid-cols-[1fr_390px]' : ''
          }`}
        >
          {/* LEFT WORKSPACE */}
          <div className="min-w-0 space-y-4">
            {activeTab === 'campaigns' ? (
              <CampaignsPanel
                canEdit={canEdit}
                rewardInputs={draftCheckInInputs}
                dirty={checkInDirty}
                saving={savingCheckIn}
                onChangeReward={(dayIndex, value) =>
                  setDraftCheckInInputs((prev) => prev.map((v, i) => (i === dayIndex ? value : v)))
                }
                onApplyPreset={(rewards) => setDraftCheckInInputs(rewards.map(String))}
                onDiscard={handleDiscardCheckIn}
                onSave={() => void handleSaveCheckIn()}
              />
            ) : activeTab === 'recommendations' ? (
              /* FOR YOU SLOT SWITCHER + INDEPENDENT SAVE BAR */
              <div className="cms-card p-4 space-y-3.5">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div
                    role="group"
                    aria-label="首页推荐栏位切换"
                    className="inline-flex flex-wrap items-center gap-1 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] p-1"
                  >
                    {(
                      [
                        {
                          key: 'editor_pick' as RecSlot,
                          label: '焦点轮播 (Featured Stories)',
                          dirty: editorPickDirty,
                          health: healthByGroup.editor_pick,
                          total: draftGroups.editor_pick.length,
                        },
                        {
                          key: 'curated' as RecSlot,
                          label: '编辑书单 (Curated For You)',
                          dirty: curatedDirty,
                          health: healthByGroup.curated,
                          total: draftGroups.curated.length,
                        },
                      ]
                    ).map((slot) => {
                      const active = activeRecSlot === slot.key;
                      return (
                        <button
                          key={slot.key}
                          type="button"
                          aria-pressed={active}
                          onClick={() => setActiveRecSlot(slot.key)}
                          className={`focus-ring px-3 py-1.5 rounded-lg text-xs font-medium inline-flex items-center gap-2 cursor-pointer transition-colors ${
                            active
                              ? 'bg-[var(--cms-card)] text-[var(--cms-fg)] shadow-xs'
                              : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                          }`}
                        >
                          <span>{slot.label}</span>
                          <span className="tabular-nums text-[11px] text-[var(--cms-muted-fg)]">
                            {slot.health.liveCount}/{slot.total}
                          </span>
                          {slot.dirty && (
                            <span
                              title="未保存改动"
                              className="w-1.5 h-1.5 rounded-full bg-[var(--cms-warning)]"
                              aria-hidden="true"
                            />
                          )}
                        </button>
                      );
                    })}
                  </div>

                  <span className="text-[11px] text-[var(--cms-muted-fg)] inline-flex items-center gap-1.5">
                    <Sparkles size={12} className="text-[var(--cms-primary)]" aria-hidden="true" />
                    <span>中间位「New Releases」由系统自动展示最新 6 本</span>
                  </span>
                </div>

                {/* Slot Status & Independent Save Strip */}
                <div className="pt-3 border-t border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-3">
                  <div className="space-y-1 text-xs">
                    {activeRecSlot === 'editor_pick' ? (
                      <>
                        {currentHealth.liveCount === 0 ? (
                          <StatusDot tone="muted" label="当前走系统兜底：H5 顶部轮播自动展示热门前 4 本" />
                        ) : (
                          <StatusDot
                            tone="success"
                            label={`已生效 ${currentHealth.liveCount} 本（建议配置 2–6 本以保证横向滑卡体验）`}
                          />
                        )}
                      </>
                    ) : (
                      <>
                        {currentHealth.liveCount === 0 ? (
                          <StatusDot tone="muted" label="当前走系统默认书库流：支持 Load More 无限分页浏览" />
                        ) : currentHealth.liveCount < 4 ? (
                          <StatusDot
                            tone="warning"
                            label={`已接管首页列表且隐藏 Load More（当前仅 ${currentHealth.liveCount} 本生效，建议至少 6 本）`}
                          />
                        ) : (
                          <StatusDot
                            tone="primary"
                            label={`已接管首页列表：H5 将仅展示下方 ${currentHealth.liveCount} 本并隐藏 Load More`}
                          />
                        )}
                      </>
                    )}
                  </div>

                  {canEdit && (
                    <div className="flex items-center gap-2 shrink-0">
                      {isCurrentRecDirty && (
                        <>
                          <StatusDot tone="warning" label="有未保存更改" />
                          <button
                            type="button"
                            disabled={savingSlot === activeRecSlot}
                            onClick={() => handleDiscardRecSlot(activeRecSlot)}
                            className="btn-ghost h-8 px-2.5 text-xs text-[var(--cms-muted-fg)]"
                          >
                            <RotateCcw size={13} aria-hidden="true" />
                            <span>放弃更改</span>
                          </button>
                        </>
                      )}
                      <button
                        type="button"
                        disabled={!isCurrentRecDirty || savingSlot === activeRecSlot}
                        onClick={() => void handleSaveRecSlot(activeRecSlot)}
                        className="btn-primary h-8 px-3.5 text-xs"
                      >
                        <Save size={13} aria-hidden="true" />
                        <span>
                          {savingSlot === activeRecSlot
                            ? '保存中...'
                            : activeRecSlot === 'editor_pick'
                              ? '保存焦点轮播'
                              : '保存编辑书单'}
                        </span>
                      </button>
                    </div>
                  )}
                </div>
              </div>
            ) : (
              /* TOP CHARTS STRATEGY + INDEPENDENT SAVE BAR */
              <div className="cms-card p-5 space-y-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div>
                    <h2 className="text-base font-semibold text-[var(--cms-fg)] flex items-center gap-2 m-0">
                      <Trophy size={16} className="text-[var(--cms-primary)]" aria-hidden="true" />
                      <span>排名榜排序策略与上限（Top Charts）</span>
                    </h2>
                    <p className="text-xs text-[var(--cms-muted-fg)] mt-1 m-0">
                      人工置顶书籍按序排在榜单最前；其余名额按下方排序依据自动补全至展示上限。
                    </p>
                  </div>

                  {canEdit && (
                    <div className="flex items-center gap-2 shrink-0">
                      {topChartsDirty && (
                        <>
                          <StatusDot tone="warning" label="有未保存更改" />
                          <button
                            type="button"
                            disabled={savingSlot === 'ranking'}
                            onClick={handleDiscardTopCharts}
                            className="btn-ghost h-8 px-2.5 text-xs text-[var(--cms-muted-fg)]"
                          >
                            <RotateCcw size={13} aria-hidden="true" />
                            <span>放弃更改</span>
                          </button>
                        </>
                      )}
                      <button
                        type="button"
                        disabled={!topChartsDirty || savingSlot === 'ranking'}
                        onClick={() => void handleSaveTopCharts()}
                        className="btn-primary h-8 px-3.5 text-xs"
                      >
                        <Save size={13} aria-hidden="true" />
                        <span>{savingSlot === 'ranking' ? '保存中...' : '保存排名榜配置'}</span>
                      </button>
                    </div>
                  )}
                </div>

                <div className="flex flex-wrap items-end gap-4 text-xs">
                  <label htmlFor={sortRuleSelectId} className="space-y-1.5">
                    <span className="block font-medium text-[var(--cms-muted-fg)]">自动补全排序依据</span>
                    <CustomSelect
                      id={sortRuleSelectId}
                      disabled={!canEdit}
                      options={RANKING_SORT_OPTIONS}
                      value={draftSortRule}
                      onChange={(val) => setDraftSortRule(val as RankingSortRule)}
                      width="240px"
                    />
                  </label>

                  <label htmlFor={limitInputId} className="space-y-1.5 w-40">
                    <span className="block font-medium text-[var(--cms-muted-fg)]">榜单展示上限（1–100）</span>
                    <input
                      id={limitInputId}
                      type="number"
                      min={1}
                      max={100}
                      disabled={!canEdit}
                      className={`input-field tabular-nums ${parsedLimit === null ? 'border-[var(--cms-error)]' : ''}`}
                      value={rankingLimitInput}
                      onChange={(e) => setRankingLimitInput(e.target.value)}
                    />
                  </label>

                  <div className="pb-1.5 flex-1 min-w-[220px]">
                    {parsedLimit === null ? (
                      <StatusDot tone="error" label="请输入 1 至 100 的整数上限" />
                    ) : draftSortRule === 'manual' && healthByGroup.ranking.liveCount === 0 ? (
                      <StatusDot
                        tone="warning"
                        label="纯人工模式生效置顶为 0：H5 将自动兜底按阅读量展示"
                      />
                    ) : draftSortRule === 'manual' ? (
                      <StatusDot
                        tone="primary"
                        label={`纯人工模式：实际展示 ${Math.min(healthByGroup.ranking.liveCount, parsedLimit)} 本（不自动补全）`}
                      />
                    ) : (
                      <StatusDot
                        tone="success"
                        label={`推演：人工置顶 ${Math.min(healthByGroup.ranking.liveCount, parsedLimit)} 本 + 自动补全 ${Math.max(0, parsedLimit - healthByGroup.ranking.liveCount)} 本`}
                      />
                    )}
                  </div>
                </div>
              </div>
            )}

            {activeTab !== 'campaigns' && (
              <>
                {/* CONFIGURED SLOT LIST CARD */}
                <div className="cms-card overflow-hidden">
              <div className="px-5 py-4 border-b border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-3">
                <div className="flex flex-wrap items-center gap-3">
                  {canEdit && currentEntries.length > 0 && (
                    <input
                      type="checkbox"
                      aria-label="全选当前列表书籍"
                      checked={allCurrentSelected}
                      onChange={toggleSelectAllCurrent}
                      className="w-4 h-4 rounded cursor-pointer"
                    />
                  )}
                  <div>
                    <h3 className="text-sm font-semibold text-[var(--cms-fg)] m-0">
                      {activeTab === 'rankings' ? '人工置顶书单（Top Charts Pinned）' : SLOT_LABELS[activeRecSlot]}
                    </h3>
                    <div className="text-xs text-[var(--cms-muted-fg)] tabular-nums mt-0.5 flex flex-wrap items-center gap-x-2">
                      <span>共 {currentEntries.length} / {MAX_GROUP_ITEMS} 本</span>
                      <span>·</span>
                      <span className="text-[var(--cms-success)]">生效 {currentHealth.liveCount}</span>
                      {currentHealth.scheduledCount > 0 && (
                        <>
                          <span>·</span>
                          <span className="text-[var(--cms-primary)]">待生效 {currentHealth.scheduledCount}</span>
                        </>
                      )}
                      {currentHealth.expiredCount > 0 && (
                        <>
                          <span>·</span>
                          <span>已过期 {currentHealth.expiredCount}</span>
                        </>
                      )}
                      {currentHealth.unpublishedCount > 0 && (
                        <>
                          <span>·</span>
                          <span className="text-[var(--cms-warning)]">未上架 {currentHealth.unpublishedCount}</span>
                        </>
                      )}
                      {currentHealth.truncatedCount > 0 && (
                        <>
                          <span>·</span>
                          <span className="text-[var(--cms-warning)]">超出上限 {currentHealth.truncatedCount}</span>
                        </>
                      )}
                    </div>
                  </div>
                </div>

                {canEdit && (
                  <button
                    type="button"
                    disabled={currentEntries.length >= MAX_GROUP_ITEMS}
                    onClick={() => setPickerOpen(true)}
                    className="btn-primary h-8 px-3.5 text-xs"
                  >
                    <Plus size={14} aria-hidden="true" />
                    <span>添加书籍</span>
                  </button>
                )}
              </div>

              {/* Stale Cleanup Banner */}
              {canEdit && currentHealth.staleCount > 0 && (
                <div
                  className="px-5 py-2.5 border-b border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-2 text-xs"
                  style={{ backgroundColor: 'color-mix(in srgb, var(--cms-warning) 8%, var(--cms-card))' }}
                >
                  <span className="inline-flex items-center gap-2 text-[var(--cms-fg)]">
                    <AlertTriangle size={14} className="text-[var(--cms-warning)] shrink-0" aria-hidden="true" />
                    <span>
                      当前列表有 <strong className="tabular-nums">{currentHealth.staleCount}</strong> 本失效/未上架书籍占位，不会在 H5 端展示。
                    </span>
                  </span>
                  <button
                    type="button"
                    onClick={() => handleCleanupStale(currentGroupName)}
                    className="btn-ghost h-7 px-2.5 text-xs text-[var(--cms-warning)] hover:bg-[color-mix(in_srgb,var(--cms-warning)_14%,transparent)]"
                  >
                    一键清理失效书籍
                  </button>
                </div>
              )}

              {/* Batch Action Strip */}
              {canEdit && selectedIds.length > 0 && (
                <div
                  className="px-5 py-2.5 border-b border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-2 text-xs"
                  style={{ backgroundColor: 'color-mix(in srgb, var(--cms-primary) 7%, var(--cms-card))' }}
                >
                  <span className="font-medium text-[var(--cms-fg)] tabular-nums">
                    已选择 {selectedIds.length} 本书籍
                  </span>
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      onClick={() => {
                        setScheduleGroup(currentGroupName);
                        setScheduleTargets(selectedTargetsInCurrent);
                      }}
                      className="btn-secondary h-7 px-2.5 text-xs"
                    >
                      <CalendarClock size={12} aria-hidden="true" />
                      <span>批量设排期</span>
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        const idSet = new Set(selectedIds);
                        updateGroupEntries(currentGroupName, (prev) =>
                          prev.map((item) =>
                            idSet.has(item.novel_id)
                              ? { ...item, scheduled_start: null, scheduled_end: null }
                              : item,
                          ),
                        );
                        window.showToast?.(`已将 ${selectedIds.length} 本设为长期有效（未保存）`, 'info');
                      }}
                      className="btn-secondary h-7 px-2.5 text-xs"
                    >
                      设为长期有效
                    </button>
                    <button
                      type="button"
                      onClick={() => handleRemoveMany(currentGroupName, selectedIds)}
                      className="btn-ghost h-7 px-2.5 text-xs text-[var(--cms-error)]"
                    >
                      批量移出
                    </button>
                    <button
                      type="button"
                      onClick={() =>
                        setSelectedIdsByGroup((prev) => ({ ...prev, [currentGroupName]: [] }))
                      }
                      className="btn-ghost h-7 px-2 text-xs text-[var(--cms-muted-fg)]"
                    >
                      取消选择
                    </button>
                  </div>
                </div>
              )}

              {/* Entries List */}
              {currentEntries.length === 0 ? (
                <div className="py-14 px-6 text-center space-y-3">
                  <div className="text-sm font-medium text-[var(--cms-fg)]">当前栏位暂未人工配置书籍</div>
                  <p className="text-xs text-[var(--cms-muted-fg)] max-w-md mx-auto m-0 leading-relaxed">
                    {activeTab === 'rankings'
                      ? '留空时，H5 端「Popular Rankings」将完全按照上方的排序规则自动生成榜单。'
                      : activeRecSlot === 'editor_pick'
                        ? '留空时，H5 端「Featured Stories」顶部轮播将默认展示全站阅读量前 4 本热门小说。'
                        : '留空时，H5 端「Curated For You」将按系统默认目录流展示，并支持 Load More 分页加载。'}
                  </p>
                  {canEdit && (
                    <div className="pt-1">
                      <button
                        type="button"
                        onClick={() => setPickerOpen(true)}
                        className="btn-primary h-9 px-4 text-xs"
                      >
                        <Plus size={14} aria-hidden="true" />
                        <span>从已上架书库挑选书籍</span>
                      </button>
                    </div>
                  )}
                </div>
              ) : (
                <div className="divide-y divide-[color-mix(in_srgb,var(--cms-border)_60%,transparent)]">
                  {currentHealth.analyzed.map((item) => {
                    const { entry, index: idx, tone, label, detail, liveRank } = item;
                    const novel = entry.novel;
                    const isChecked = selectedIds.includes(entry.novel_id);
                    const isFocused = focusedNovelId === entry.novel_id;
                    const isDragging = dragIdx === idx;
                    const isDragTarget = dragOverIdx === idx && dragIdx !== null && dragIdx !== idx;
                    const hasCustomSchedule = Boolean(entry.scheduled_start || entry.scheduled_end);

                    return (
                      <div
                        key={entry.novel_id}
                        draggable={canEdit}
                        onDragStart={(e) => handleDragStart(e, idx)}
                        onDragOver={(e) => handleDragOver(e, idx)}
                        onDrop={(e) => handleDrop(e, idx)}
                        onDragEnd={handleDragEnd}
                        onClick={() => setFocusedNovelId(entry.novel_id)}
                        className={`px-4 py-3 flex items-center gap-3 transition-colors ${
                          canEdit ? 'cursor-grab active:cursor-grabbing' : ''
                        } ${
                          isDragging
                            ? 'opacity-45 bg-[var(--cms-content)]'
                            : isDragTarget
                              ? 'ring-2 ring-inset ring-[var(--cms-primary)] bg-[color-mix(in_srgb,var(--cms-primary)_10%,var(--cms-card))]'
                              : isFocused
                                ? 'bg-[color-mix(in_srgb,var(--cms-primary)_6%,var(--cms-card))]'
                                : 'hover:bg-[var(--cms-content)]/70'
                        }`}
                      >
                        {canEdit && (
                          <div
                            className="flex items-center gap-2 shrink-0"
                            onClick={(e) => e.stopPropagation()}
                          >
                            <input
                              type="checkbox"
                              aria-label={`选择《${novel?.title || entry.novel_id}》`}
                              checked={isChecked}
                              onChange={() => toggleSelectEntry(entry.novel_id)}
                              className="w-4 h-4 rounded cursor-pointer"
                            />
                            <span aria-hidden="true" className="text-[var(--cms-placeholder)]">
                              <GripVertical size={15} />
                            </span>
                          </div>
                        )}

                        <div
                          className={`w-7 h-7 rounded-lg flex items-center justify-center font-semibold text-xs tabular-nums shrink-0 ${
                            idx < 3 ? 'nav-active-item' : 'bg-[var(--cms-content)] text-[var(--cms-muted-fg)]'
                          }`}
                        >
                          {String(idx + 1).padStart(2, '0')}
                        </div>

                        <img
                          src={novel?.cover_url || FALLBACK_COVER}
                          alt={novel?.title || String(entry.novel_id)}
                          className="w-10 h-14 object-cover rounded-lg border border-[var(--cms-border)] shrink-0"
                        />

                        <div className="flex-1 min-w-0 space-y-1">
                          <div className="flex flex-wrap items-center gap-2.5 min-w-0">
                            <span className="text-xs font-medium text-[var(--cms-fg)] truncate max-w-[260px]">
                              {novel?.title || `Book #${entry.novel_id}`}
                            </span>
                            <span title={detail}>
                              <StatusDot tone={tone} label={liveRank !== null ? `${label} · #${liveRank}` : label} />
                            </span>
                          </div>

                          <div className="text-xs text-[var(--cms-muted-fg)] flex flex-wrap items-center gap-x-2.5 gap-y-1 tabular-nums">
                            <span>#{entry.novel_id}</span>
                            <span className="truncate max-w-[120px]">{novel?.author || '-'}</span>
                            <span className="inline-flex items-center gap-0.5">
                              <Star size={11} aria-hidden="true" /> {novel?.rating ?? 0}
                            </span>
                            <span className="inline-flex items-center gap-0.5">
                              <Flame size={11} aria-hidden="true" /> {formatCompactNumber(novel?.view_count ?? 0)}
                            </span>
                            <button
                              type="button"
                              disabled={!canEdit}
                              onClick={(e) => {
                                e.stopPropagation();
                                if (!canEdit) return;
                                setScheduleGroup(currentGroupName);
                                setScheduleTargets([entry]);
                              }}
                              title={detail}
                              className={`focus-ring inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] transition-colors ${
                                canEdit ? 'cursor-pointer hover:opacity-85' : 'cursor-default'
                              } ${
                                hasCustomSchedule
                                  ? 'bg-[var(--cms-accent)] text-[var(--cms-nav-active-fg)]'
                                  : 'bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)]'
                              }`}
                            >
                              <CalendarClock size={10} aria-hidden="true" />
                              <span>{formatScheduleSummary(entry.scheduled_start, entry.scheduled_end)}</span>
                            </button>
                          </div>
                        </div>

                        {canEdit && (
                          <div
                            className="flex items-center gap-0.5 shrink-0"
                            onClick={(e) => e.stopPropagation()}
                          >
                            <button
                              type="button"
                              onClick={() => {
                                setScheduleGroup(currentGroupName);
                                setScheduleTargets([entry]);
                              }}
                              className="btn-detail whitespace-nowrap"
                            >
                              设排期
                            </button>

                            <button
                              type="button"
                              disabled={idx === 0}
                              onClick={() => reorderInGroup(currentGroupName, idx, idx - 1)}
                              title="上移"
                              aria-label={`上移 ${novel?.title || entry.novel_id}`}
                              className="focus-ring w-7 h-7 rounded-md hover:bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] flex items-center justify-center cursor-pointer disabled:opacity-25 disabled:cursor-not-allowed"
                            >
                              <ArrowUp size={13} aria-hidden="true" />
                            </button>
                            <button
                              type="button"
                              disabled={idx === currentEntries.length - 1}
                              onClick={() => reorderInGroup(currentGroupName, idx, idx + 1)}
                              title="下移"
                              aria-label={`下移 ${novel?.title || entry.novel_id}`}
                              className="focus-ring w-7 h-7 rounded-md hover:bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] flex items-center justify-center cursor-pointer disabled:opacity-25 disabled:cursor-not-allowed"
                            >
                              <ArrowDown size={13} aria-hidden="true" />
                            </button>

                            <RowActionMenu
                              ariaLabel={`${novel?.title || entry.novel_id} 更多操作`}
                              items={[
                                {
                                  label: '置顶（移至第 1 位）',
                                  disabled: idx === 0,
                                  onClick: () => reorderInGroup(currentGroupName, idx, 0),
                                },
                                {
                                  label: '移至指定位次...',
                                  disabled: currentEntries.length <= 1,
                                  onClick: () =>
                                    setMoveRankTarget({
                                      group: currentGroupName,
                                      entry,
                                      index: idx,
                                    }),
                                },
                                ...(hasCustomSchedule
                                  ? [
                                      {
                                        label: '清除排期（恢复长期有效）',
                                        onClick: () => {
                                          updateGroupEntries(currentGroupName, (prev) =>
                                            prev.map((it) =>
                                              it.novel_id === entry.novel_id
                                                ? { ...it, scheduled_start: null, scheduled_end: null }
                                                : it,
                                            ),
                                          );
                                        },
                                      },
                                    ]
                                  : []),
                                {
                                  label: '移出当前列表',
                                  tone: 'danger',
                                  dividerBefore: true,
                                  onClick: () => handleRemoveMany(currentGroupName, [entry.novel_id]),
                                },
                              ]}
                            />
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            {/* TOP CHARTS AUTO-BACKFILL REFERENCE PANEL */}
            {activeTab === 'rankings' && autoBackfillRankings.length > 0 && (
              <div className="cms-card overflow-hidden">
                <button
                  type="button"
                  onClick={() => setAutoBackfillExpanded((v) => !v)}
                  className="focus-ring w-full px-5 py-3.5 flex items-center justify-between gap-3 bg-transparent border-none text-left cursor-pointer hover:bg-[var(--cms-content)]/60 transition-colors"
                >
                  <div className="flex items-center gap-2 text-xs">
                    {autoBackfillExpanded ? (
                      <ChevronDown size={15} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />
                    ) : (
                      <ChevronRight size={15} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />
                    )}
                    <span className="font-semibold text-[var(--cms-fg)] text-sm">
                      系统自动补全名额参考（第 {(previewData?.rankings?.length || 0) - autoBackfillRankings.length + 1} – {previewData?.rankings?.length || 0} 名）
                    </span>
                    <span className="text-[var(--cms-muted-fg)] tabular-nums">
                      共 {autoBackfillRankings.length} 本
                    </span>
                  </div>
                  <span className="text-[11px] text-[var(--cms-muted-fg)]">
                    基于当前排序依据与模拟时间推演
                  </span>
                </button>

                {autoBackfillExpanded && (
                  <div className="border-t border-[var(--cms-border)] divide-y divide-[color-mix(in_srgb,var(--cms-border)_60%,transparent)]">
                    {autoBackfillRankings.map((item) => {
                      const rankIndex = (previewData?.rankings || []).findIndex(
                        (r) => r.novel.id === item.novel.id,
                      );
                      return (
                        <div
                          key={`backfill-${item.novel.id}`}
                          className="px-5 py-2.5 flex items-center gap-3 text-xs bg-[var(--cms-content)]/35"
                        >
                          <span className="w-7 text-center font-medium text-[var(--cms-muted-fg)] tabular-nums shrink-0">
                            #{rankIndex + 1}
                          </span>
                          <img
                            src={item.novel.cover_url || FALLBACK_COVER}
                            alt={item.novel.title}
                            className="w-8 h-11 object-cover rounded border border-[var(--cms-border)] shrink-0"
                          />
                          <div className="flex-1 min-w-0">
                            <div className="text-xs text-[var(--cms-fg)] truncate">{item.novel.title}</div>
                            <div className="text-[var(--cms-muted-fg)] flex items-center gap-2.5 mt-0.5 tabular-nums">
                              <span>#{item.novel.id}</span>
                              <span className="truncate max-w-[120px]">{item.novel.author}</span>
                              <span>★ {item.novel.rating}</span>
                              <span>👁 {formatCompactNumber(item.novel.view_count || 0)}</span>
                            </div>
                          </div>
                          {canEdit && (
                            <button
                              type="button"
                              onClick={() => handleAddBooksToGroup('ranking', [item.novel], 'append')}
                              className="btn-secondary h-7 px-2.5 text-[11px] shrink-0"
                            >
                              <Pin size={11} aria-hidden="true" />
                              <span>转为人工置顶</span>
                            </button>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            )}
              </>
            )}
          </div>

          {/* RIGHT STICKY H5 PREVIEW */}
          {showPreview && (
            activeTab === 'campaigns' ? (
              <H5RewardsPreview
                rewards={draftCheckInInputs.map((v, i) => {
                  const n = Number(v.trim());
                  return Number.isInteger(n) && n >= 1 && n <= 10000 ? n : serverCheckInRewards[i];
                })}
                h5Theme={h5Theme}
                onThemeChange={setH5Theme}
              />
            ) : (
              <H5StorefrontPreview
                activeView={activeTab === 'rankings' ? 'hot' : 'discover'}
                h5Theme={h5Theme}
                onThemeChange={setH5Theme}
                preview={previewData}
                loading={previewLoading}
                errorMsg={previewError}
                previewAtIso={previewAtIso}
                onPreviewAtChange={setPreviewAtIso}
              />
            )
          )}
        </div>
      )}

      {/* Slide-over Multi-select Book Picker Drawer */}
      <BookPickerDrawer
        open={pickerOpen}
        onClose={() => setPickerOpen(false)}
        targetGroup={currentGroupName}
        targetLabel={SLOT_LABELS[currentGroupName]}
        existingEntriesByGroup={draftGroups}
        availableGenres={availableGenres}
        onAddBooks={(novels, position) => handleAddBooksToGroup(currentGroupName, novels, position)}
      />

      {/* Schedule Modal (Single or Batch) */}
      <ScheduleModal
        open={Boolean(scheduleGroup && scheduleTargets.length > 0)}
        targets={scheduleTargets}
        onClose={() => {
          setScheduleGroup(null);
          setScheduleTargets([]);
        }}
        onApply={handleApplySchedule}
      />

      {/* Jump-to-Rank Modal */}
      <MoveRankModal
        open={Boolean(moveRankTarget)}
        entry={moveRankTarget?.entry || null}
        currentIndex={moveRankTarget?.index ?? 0}
        totalCount={moveRankTarget ? (draftGroups[moveRankTarget.group] || []).length : 0}
        onClose={() => setMoveRankTarget(null)}
        onConfirm={(targetRank) => {
          if (!moveRankTarget) return;
          reorderInGroup(moveRankTarget.group, moveRankTarget.index, targetRank - 1);
        }}
      />
    </div>
  );
}

function cloneEntriesRecord(
  groups: Record<RecommendationGroupName, RecommendationEntry[]>,
): Record<RecommendationGroupName, RecommendationEntry[]> {
  return {
    editor_pick: cloneEntries(groups.editor_pick || []),
    curated: cloneEntries(groups.curated || []),
    ranking: cloneEntries(groups.ranking || []),
  };
}

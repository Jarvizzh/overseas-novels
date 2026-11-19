import React, { useEffect, useMemo, useRef, useState } from 'react';
import { ArrowDownToLine, Check, Flame, Plus, Star } from 'lucide-react';
import CustomSelect from '../CustomSelect';
import { CmsDialog } from '../ui/CmsDialog';
import { Pagination } from '../ui/Pagination';
import {
  type FilterChip,
  FilterChipsBar,
  SearchInput,
  StatusDot,
  ToolbarPopover,
} from '../ui/Primitives';
import {
  operationsApi,
  type NovelPickerSort,
  type RecommendationEntry,
  type RecommendationGroupName,
  type RecommendationNovel,
} from '../../utils/api';
import { FALLBACK_COVER, MAX_GROUP_ITEMS, formatCompactNumber } from './operationsUtils';

interface BookPickerDrawerProps {
  open: boolean;
  onClose: () => void;
  targetGroup: RecommendationGroupName;
  targetLabel: string;
  existingEntriesByGroup: Record<RecommendationGroupName, RecommendationEntry[]>;
  availableGenres: string[];
  onAddBooks: (novels: RecommendationNovel[], position: 'append' | 'prepend') => void;
}

interface PickerQueryState {
  search: string;
  genre: string;
  status: string;
  sort: NovelPickerSort;
  page: number;
  nonce: number;
}

const INITIAL_QUERY: PickerQueryState = {
  search: '',
  genre: '',
  status: '',
  sort: 'views',
  page: 1,
  nonce: 0,
};

const GROUP_SHORT_LABELS: Record<RecommendationGroupName, string> = {
  editor_pick: '焦点轮播',
  curated: '编辑书单',
  ranking: '排名榜置顶',
};

const SORT_OPTIONS: Array<{ value: NovelPickerSort; label: string }> = [
  { value: 'views', label: '阅读量降序' },
  { value: 'rating', label: '评分降序' },
  { value: 'latest', label: '最新上架' },
  { value: 'updated', label: '最近更新' },
];

const STATUS_OPTIONS = [
  { value: '', label: '全部连载状态' },
  { value: 'Ongoing', label: '连载中 (Ongoing)' },
  { value: 'Completed', label: '已完结 (Completed)' },
];

export const BookPickerDrawer: React.FC<BookPickerDrawerProps> = ({
  open,
  onClose,
  targetGroup,
  targetLabel,
  existingEntriesByGroup,
  availableGenres,
  onAddBooks,
}) => {
  const [searchDraft, setSearchDraft] = useState('');
  const [queryState, setQueryState] = useState<PickerQueryState>(INITIAL_QUERY);
  const [pageSize, setPageSize] = useState(20);

  const [novels, setNovels] = useState<RecommendationNovel[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);

  const [selectedMap, setSelectedMap] = useState<Record<number, RecommendationNovel>>({});

  const abortRef = useRef<AbortController | null>(null);
  const seqRef = useRef(0);

  const currentEntries = useMemo(
    () => existingEntriesByGroup[targetGroup] || [],
    [existingEntriesByGroup, targetGroup],
  );

  const currentPosById = useMemo(() => {
    const map = new Map<number, number>();
    currentEntries.forEach((entry, idx) => map.set(entry.novel_id, idx + 1));
    return map;
  }, [currentEntries]);

  const otherGroupsById = useMemo(() => {
    const map = new Map<number, string[]>();
    (Object.keys(GROUP_SHORT_LABELS) as RecommendationGroupName[]).forEach((group) => {
      if (group === targetGroup) return;
      (existingEntriesByGroup[group] || []).forEach((entry) => {
        const list = map.get(entry.novel_id) || [];
        list.push(GROUP_SHORT_LABELS[group]);
        map.set(entry.novel_id, list);
      });
    });
    return map;
  }, [existingEntriesByGroup, targetGroup]);

  const remainingCapacity = Math.max(0, MAX_GROUP_ITEMS - currentEntries.length);

  // Reset selection & bump query nonce whenever drawer opens
  useEffect(() => {
    if (!open) {
      abortRef.current?.abort();
      return;
    }
    setSelectedMap({});
    setQueryState((prev) => ({ ...prev, page: 1, nonce: prev.nonce + 1 }));
  }, [open]);

  // Single authoritative effect for fetching published candidates
  useEffect(() => {
    if (!open) return;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const seq = ++seqRef.current;

    setLoading(true);
    void operationsApi
      .searchPublishedNovels({
        search: queryState.search,
        genre: queryState.genre,
        status: queryState.status,
        sort: queryState.sort,
        page: queryState.page,
        pageSize,
        signal: controller.signal,
      })
      .then((res) => {
        if (controller.signal.aborted || seq !== seqRef.current) return;
        setNovels(res.novels || []);
        setTotal(res.total || 0);
      })
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError' || controller.signal.aborted || seq !== seqRef.current) {
          return;
        }
        window.showToast?.((err instanceof Error ? err.message : '') || '搜索已上架书库失败', 'error');
      })
      .finally(() => {
        if (seq === seqRef.current) {
          setLoading(false);
        }
      });

    return () => controller.abort();
  }, [open, queryState, pageSize]);

  // Prune any already-added books out of selection if user single-adds them
  useEffect(() => {
    setSelectedMap((prev) => {
      const next: Record<number, RecommendationNovel> = {};
      let changed = false;
      for (const [idStr, novel] of Object.entries(prev)) {
        const id = Number(idStr);
        if (currentPosById.has(id)) {
          changed = true;
        } else {
          next[id] = novel;
        }
      }
      return changed ? next : prev;
    });
  }, [currentPosById]);

  const selectedList = useMemo(() => Object.values(selectedMap), [selectedMap]);

  const toggleSelect = (novel: RecommendationNovel) => {
    if (currentPosById.has(novel.id)) return;
    setSelectedMap((prev) => {
      if (prev[novel.id]) {
        const copy = { ...prev };
        delete copy[novel.id];
        return copy;
      }
      if (Object.keys(prev).length >= remainingCapacity) {
        window.showToast?.(`当前栏位最多再添加 ${remainingCapacity} 本书（上限 ${MAX_GROUP_ITEMS} 本）`, 'info');
        return prev;
      }
      return { ...prev, [novel.id]: novel };
    });
  };

  const handleBatchAdd = (position: 'append' | 'prepend') => {
    if (selectedList.length === 0) return;
    onAddBooks(selectedList, position);
    setSelectedMap({});
    onClose();
  };

  const handleImportFromGroup = (sourceGroup: RecommendationGroupName, closePopover: () => void) => {
    closePopover();
    const sourceEntries = existingEntriesByGroup[sourceGroup] || [];
    const candidates = sourceEntries
      .map((e) => e.novel)
      .filter(
        (n): n is RecommendationNovel =>
          Boolean(n && !currentPosById.has(n.id) && n.publish_status === 'published' && n.status !== 'archived'),
      );

    if (candidates.length === 0) {
      window.showToast?.(`「${GROUP_SHORT_LABELS[sourceGroup]}」中没有可导入的新增已上架书籍`, 'info');
      return;
    }
    const slice = candidates.slice(0, remainingCapacity);
    onAddBooks(slice, 'append');
    if (candidates.length > slice.length) {
      window.showToast?.(`已导入 ${slice.length} 本（受栏位 ${MAX_GROUP_ITEMS} 本上限限制）`, 'info');
    }
  };

  const resetFilters = () => {
    setSearchDraft('');
    setQueryState((prev) => ({ ...INITIAL_QUERY, nonce: prev.nonce + 1 }));
  };

  const filterChips = useMemo<FilterChip[]>(() => {
    const chips: FilterChip[] = [];
    if (queryState.search) {
      chips.push({
        key: 'search',
        label: `搜索: ${queryState.search}`,
        onClear: () => {
          setSearchDraft('');
          setQueryState((prev) => ({ ...prev, search: '', page: 1 }));
        },
      });
    }
    if (queryState.genre) {
      chips.push({
        key: 'genre',
        label: `分类: ${queryState.genre}`,
        onClear: () => setQueryState((prev) => ({ ...prev, genre: '', page: 1 })),
      });
    }
    if (queryState.status) {
      chips.push({
        key: 'status',
        label: `状态: ${queryState.status === 'Ongoing' ? '连载中' : '已完结'}`,
        onClear: () => setQueryState((prev) => ({ ...prev, status: '', page: 1 })),
      });
    }
    return chips;
  }, [queryState.search, queryState.genre, queryState.status]);

  const selectableOnPage = novels.filter((n) => !currentPosById.has(n.id));
  const allPageSelected =
    selectableOnPage.length > 0 && selectableOnPage.every((n) => Boolean(selectedMap[n.id]));

  const toggleSelectPage = () => {
    if (allPageSelected) {
      setSelectedMap((prev) => {
        const next = { ...prev };
        selectableOnPage.forEach((n) => delete next[n.id]);
        return next;
      });
      return;
    }
    setSelectedMap((prev) => {
      const next = { ...prev };
      for (const n of selectableOnPage) {
        if (next[n.id]) continue;
        if (Object.keys(next).length >= remainingCapacity) {
          window.showToast?.(`已达到当前栏位上限 (${MAX_GROUP_ITEMS} 本)`, 'info');
          break;
        }
        next[n.id] = n;
      }
      return next;
    });
  };

  const otherImportableGroups = (Object.keys(GROUP_SHORT_LABELS) as RecommendationGroupName[]).filter(
    (g) => g !== targetGroup,
  );

  return (
    <CmsDialog
      open={open}
      onClose={onClose}
      variant="drawer"
      size="lg"
      closedBy="any"
      title={`添加书籍至「${targetLabel}」`}
      subtitle={`仅展示已上架书籍 · 当前栏位 ${currentEntries.length} / ${MAX_GROUP_ITEMS} 本`}
      headerExtra={
        <ToolbarPopover
          align="end"
          label="从其他栏位导入"
          icon={<ArrowDownToLine size={13} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />}
          triggerClassName="h-8 px-2.5 rounded-lg border border-[var(--cms-border)] bg-[var(--cms-card)] hover:bg-[var(--cms-content)] text-xs font-medium text-[var(--cms-fg)]"
          panelClassName="w-60 p-2 space-y-1"
        >
          {({ close }) => (
            <>
              <div className="px-2 py-1 text-[11px] font-medium text-[var(--cms-muted-fg)]">
                将其他栏位书籍增量合并到末尾
              </div>
              {otherImportableGroups.map((g) => {
                const count = (existingEntriesByGroup[g] || []).length;
                return (
                  <button
                    key={g}
                    type="button"
                    disabled={count === 0 || remainingCapacity === 0}
                    onClick={() => handleImportFromGroup(g, close)}
                    className="focus-ring w-full text-left px-2.5 py-2 rounded-lg text-xs hover:bg-[var(--cms-secondary)] flex items-center justify-between gap-2 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                  >
                    <span className="font-medium text-[var(--cms-fg)]">{GROUP_SHORT_LABELS[g]}</span>
                    <span className="text-[var(--cms-muted-fg)] tabular-nums">{count} 本</span>
                  </button>
                );
              })}
            </>
          )}
        </ToolbarPopover>
      }
      bodyClassName="flex-1 overflow-y-auto flex flex-col"
      footer={
        <div className="w-full flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
            <span className="tabular-nums">
              已勾选 <strong className="text-[var(--cms-fg)]">{selectedList.length}</strong> 本
            </span>
            {selectedList.length > 0 && (
              <button
                type="button"
                onClick={() => setSelectedMap({})}
                className="btn-ghost h-7 px-2 text-xs text-[var(--cms-muted-fg)]"
              >
                清空勾选
              </button>
            )}
          </div>

          <div className="flex items-center gap-2">
            <button type="button" onClick={onClose} className="btn-secondary">
              完成
            </button>
            <button
              type="button"
              disabled={selectedList.length === 0}
              onClick={() => handleBatchAdd('prepend')}
              className="btn-secondary"
            >
              插入到顶部
            </button>
            <button
              type="button"
              disabled={selectedList.length === 0}
              onClick={() => handleBatchAdd('append')}
              className="btn-primary"
            >
              <Plus size={14} aria-hidden="true" />
              <span>添加到末尾 ({selectedList.length})</span>
            </button>
          </div>
        </div>
      }
    >
      {/* Search & Filter Toolbar */}
      <div className="px-6 py-4 border-b border-[var(--cms-border)] bg-[var(--cms-bg)]/50 space-y-2.5 shrink-0">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const trimmed = searchDraft.trim();
            setQueryState((prev) => ({ ...prev, search: trimmed, page: 1, nonce: prev.nonce + 1 }));
          }}
          className="flex flex-wrap items-center gap-2"
        >
          <SearchInput
            className="flex-1 min-w-[200px]"
            ariaLabel="搜索书名、作者或 ID"
            placeholder="搜索书名、作者或 ID..."
            value={searchDraft}
            onChange={setSearchDraft}
            onClear={() => {
              if (queryState.search) {
                setQueryState((prev) => ({ ...prev, search: '', page: 1 }));
              }
            }}
          />

          <CustomSelect
            aria-label="筛选分类"
            width="136px"
            value={queryState.genre}
            onChange={(val) => setQueryState((prev) => ({ ...prev, genre: val, page: 1 }))}
            options={[
              { value: '', label: '全部分类' },
              ...availableGenres.map((g) => ({ value: g, label: g })),
            ]}
          />

          <CustomSelect
            aria-label="筛选连载状态"
            width="148px"
            value={queryState.status}
            onChange={(val) => setQueryState((prev) => ({ ...prev, status: val, page: 1 }))}
            options={STATUS_OPTIONS}
          />

          <CustomSelect
            aria-label="排序方式"
            width="132px"
            value={queryState.sort}
            onChange={(val) => setQueryState((prev) => ({ ...prev, sort: val as NovelPickerSort, page: 1 }))}
            options={SORT_OPTIONS}
          />

          <button type="submit" disabled={loading} className="btn-secondary h-9 px-3 text-xs">
            查询
          </button>
        </form>

        <div className="flex flex-wrap items-center justify-between gap-2">
          <FilterChipsBar chips={filterChips} onResetAll={resetFilters} />
          <div className="flex items-center gap-3 text-xs text-[var(--cms-muted-fg)] ml-auto">
            {selectableOnPage.length > 0 && (
              <button
                type="button"
                onClick={toggleSelectPage}
                className="btn-ghost h-7 px-2 text-xs text-[var(--cms-detail)]"
              >
                {allPageSelected ? '取消全选本页' : `全选本页可加 (${selectableOnPage.length})`}
              </button>
            )}
            <span className="tabular-nums">共 {total} 本已上架</span>
          </div>
        </div>
      </div>

      {/* Candidate List */}
      <div className="flex-1 overflow-y-auto px-6 py-4 space-y-2">
        {loading && novels.length === 0 ? (
          <div className="py-16 text-center text-xs text-[var(--cms-muted-fg)]">正在检索已上架书库...</div>
        ) : novels.length === 0 ? (
          <div className="py-16 text-center text-xs text-[var(--cms-muted-fg)]">
            未找到符合条件的已上架书籍
          </div>
        ) : (
          novels.map((novel) => {
            const existingPos = currentPosById.get(novel.id);
            const alreadyAdded = existingPos !== undefined;
            const isChecked = Boolean(selectedMap[novel.id]);
            const otherGroups = otherGroupsById.get(novel.id) || [];

            return (
              <div
                key={novel.id}
                role="checkbox"
                aria-checked={alreadyAdded || isChecked}
                aria-disabled={alreadyAdded || undefined}
                tabIndex={alreadyAdded ? -1 : 0}
                onClick={() => {
                  if (!alreadyAdded) toggleSelect(novel);
                }}
                onKeyDown={(e) => {
                  if (alreadyAdded) return;
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    toggleSelect(novel);
                  }
                }}
                className={`focus-ring p-3 rounded-xl border flex items-center gap-3 transition-colors ${
                  alreadyAdded
                    ? 'border-[var(--cms-border)] bg-[var(--cms-content)]/60 opacity-75'
                    : isChecked
                      ? 'border-[var(--cms-primary)] bg-[color-mix(in_srgb,var(--cms-primary)_6%,var(--cms-card))] cursor-pointer'
                      : 'border-[var(--cms-border)] bg-[var(--cms-card)] hover:bg-[var(--cms-content)] cursor-pointer'
                }`}
              >
                <span
                  aria-hidden="true"
                  className={`w-4 h-4 rounded flex items-center justify-center shrink-0 border transition-colors ${
                    alreadyAdded
                      ? 'border-[var(--cms-border)] bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)]'
                      : isChecked
                        ? 'border-[var(--cms-primary)] bg-[var(--cms-primary)] text-white'
                        : 'border-[var(--cms-border)] bg-[var(--cms-card)]'
                  }`}
                >
                  {(alreadyAdded || isChecked) && <Check size={11} strokeWidth={2.8} />}
                </span>

                <img
                  src={novel.cover_url || FALLBACK_COVER}
                  alt={novel.title}
                  className="w-10 h-14 object-cover rounded-lg border border-[var(--cms-border)] shrink-0"
                />

                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 min-w-0">
                    <span className="text-[0.8125rem] font-medium text-[var(--cms-fg)] truncate">
                      {novel.title}
                    </span>
                    {novel.genres?.[0] && (
                      <span className="px-2 py-0.5 rounded-full text-[11px] bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)] shrink-0">
                        {novel.genres[0]}
                      </span>
                    )}
                  </div>

                  <div className="text-xs text-[var(--cms-muted-fg)] flex flex-wrap items-center gap-x-2.5 gap-y-0.5 mt-1 tabular-nums">
                    <span>#{novel.id}</span>
                    <span className="truncate max-w-[140px]">{novel.author}</span>
                    <span className="inline-flex items-center gap-1">
                      <Star size={11} aria-hidden="true" /> {novel.rating}
                    </span>
                    <span className="inline-flex items-center gap-1">
                      <Flame size={11} aria-hidden="true" /> {formatCompactNumber(novel.view_count || 0)}
                    </span>
                    {otherGroups.length > 0 && (
                      <span className="text-[11px] text-[var(--cms-detail)]">
                        已在 {otherGroups.join(' · ')}
                      </span>
                    )}
                  </div>
                </div>

                <div className="shrink-0" onClick={(e) => e.stopPropagation()}>
                  {alreadyAdded ? (
                    <StatusDot tone="primary" label={`第 ${existingPos} 位`} />
                  ) : (
                    <button
                      type="button"
                      disabled={remainingCapacity === 0}
                      onClick={() => onAddBooks([novel], 'append')}
                      className="btn-secondary h-8 px-2.5 text-xs"
                    >
                      <Plus size={13} aria-hidden="true" />
                      <span>添加</span>
                    </button>
                  )}
                </div>
              </div>
            );
          })
        )}
      </div>

      <Pagination
        insideCard
        page={queryState.page}
        pageSize={pageSize}
        pageSizeOptions={[10, 20, 50]}
        total={total}
        disabled={loading}
        onPageChange={(nextPage) => setQueryState((prev) => ({ ...prev, page: nextPage }))}
        onPageSizeChange={(nextSize) => {
          setPageSize(nextSize);
          setQueryState((prev) => ({ ...prev, page: 1 }));
        }}
        itemLabel="本书"
      />
    </CmsDialog>
  );
};

import { type KeyboardEvent, useEffect, useId, useMemo, useRef, useState } from 'react';
import { Check, ChevronDown, Plus, Search, X } from 'lucide-react';
import { usePermission } from '../context/PermissionContext';
import {
  apiRequest,
  getPromotionLinkOptions,
  type PromotionLinkDomainOption,
  type PromotionLinkNovelOption,
  type PromotionLinkPixelOption,
  type PromotionLinkTemplateOption,
} from '../utils/api';
import { copyTextToClipboard } from '../utils/clipboard';
import { validateAndBuildPromotionPayload } from '../utils/promotion';
import { filterPromotionTemplatesByEnv } from '../utils/templateCatalogHelpers';
import CustomSelect from '../components/CustomSelect';
import { CmsDialog } from '../components/ui/CmsDialog';
import { Pagination } from '../components/ui/Pagination';
import {
  type ColumnOption,
  type FilterChip,
  FilterChipsBar,
  RowActionMenu,
  SearchInput,
  SkeletonRows,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../components/ui/Primitives';

interface PromotionLink {
  id: number;
  name: string;
  environment?: 'live' | 'sandbox';
  novel_id: number;
  novel_title: string;
  chapter_index: number;
  utm_source: string;
  utm_campaign: string;
  generated_url: string;
  domain_id?: number | null;
  domain?: string;
  fb_pixel_id?: number | null;
  recharge_template_id?: number | null;
  coin_cost_per_thousand?: number | null;
  start_pay_chapter_index?: number | null;
  created_at: string;
}

const EMPTY_CREATE_FORM = {
  novelId: '',
  name: '',
  environment: 'live' as 'live' | 'sandbox',
  chapterIndex: '1',
  utmSource: 'facebook',
  utmCampaign: '',
  domainId: '',
  fbPixelId: '',
  rechargeTemplateId: '',
  coinCostPerThousand: '',
  startPayChapterIndex: '',
};

interface NovelSearchSelectProps {
  novels: PromotionLinkNovelOption[];
  value: string;
  disabled?: boolean;
  onChange: (novelId: string) => void;
}

function NovelSearchSelect({ novels, value, disabled = false, onChange }: NovelSearchSelectProps) {
  const uid = useId();
  const triggerId = `${uid}-trigger`;
  const listboxId = `${uid}-listbox`;
  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const optionRefs = useRef<Array<HTMLButtonElement | null>>([]);

  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [activeIndex, setActiveIndex] = useState(0);

  const selectedNovel = useMemo(
    () => novels.find((n) => String(n.id) === value) ?? null,
    [novels, value],
  );

  const filteredNovels = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return novels;
    const idToken = q.startsWith('#') ? q.slice(1) : q;
    return novels.filter((n) => {
      if (n.title.toLowerCase().includes(q)) return true;
      if (idToken && String(n.id).includes(idToken)) return true;
      return false;
    });
  }, [novels, query]);

  const effectiveOpen = open && !disabled;
  const clampedActiveIndex =
    filteredNovels.length === 0 ? -1 : Math.min(Math.max(activeIndex, 0), filteredNovels.length - 1);

  useEffect(() => {
    if (!effectiveOpen) return;
    const raf = window.requestAnimationFrame(() => {
      searchInputRef.current?.focus();
    });
    return () => window.cancelAnimationFrame(raf);
  }, [effectiveOpen]);

  useEffect(() => {
    if (!effectiveOpen) return;
    const onMouseDown = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
        setQuery('');
      }
    };
    document.addEventListener('mousedown', onMouseDown);
    return () => document.removeEventListener('mousedown', onMouseDown);
  }, [effectiveOpen]);

  useEffect(() => {
    if (!effectiveOpen || clampedActiveIndex < 0) return;
    optionRefs.current[clampedActiveIndex]?.scrollIntoView({ block: 'nearest' });
  }, [effectiveOpen, clampedActiveIndex]);

  const closeDropdown = (restoreFocus = false) => {
    setOpen(false);
    setQuery('');
    if (restoreFocus) {
      window.requestAnimationFrame(() => triggerRef.current?.focus());
    }
  };

  const openDropdown = (initialQuery = '') => {
    if (disabled) return;
    setQuery(initialQuery);
    const selectedIdx = initialQuery
      ? 0
      : Math.max(0, novels.findIndex((n) => String(n.id) === value));
    setActiveIndex(selectedIdx);
    setOpen(true);
  };

  const selectNovel = (novel: PromotionLinkNovelOption) => {
    onChange(String(novel.id));
    closeDropdown(true);
  };

  const handleTriggerKeyDown = (e: KeyboardEvent<HTMLButtonElement>) => {
    if (disabled) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (effectiveOpen) {
        closeDropdown();
      } else {
        openDropdown();
      }
    } else if (e.key === 'Escape' && effectiveOpen) {
      e.preventDefault();
      e.stopPropagation();
      closeDropdown();
    } else if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      openDropdown(e.key);
    }
  };

  const handleSearchKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (filteredNovels.length > 0) {
        setActiveIndex((prev) => (prev + 1) % filteredNovels.length);
      }
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (filteredNovels.length > 0) {
        setActiveIndex((prev) => (prev <= 0 ? filteredNovels.length - 1 : prev - 1));
      }
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (clampedActiveIndex >= 0 && filteredNovels[clampedActiveIndex]) {
        selectNovel(filteredNovels[clampedActiveIndex]);
      }
    } else if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      closeDropdown(true);
    } else if (e.key === 'Tab') {
      closeDropdown(false);
    }
  };

  const activeOptionId =
    effectiveOpen && clampedActiveIndex >= 0 && filteredNovels[clampedActiveIndex]
      ? `${uid}-opt-${filteredNovels[clampedActiveIndex].id}`
      : undefined;

  return (
    <div ref={containerRef} className="relative">
      <div className="relative flex items-center">
        <button
          ref={triggerRef}
          id={triggerId}
          type="button"
          role="combobox"
          aria-label="推广书籍"
          aria-expanded={effectiveOpen}
          aria-haspopup="listbox"
          aria-controls={listboxId}
          disabled={disabled}
          onClick={() => (effectiveOpen ? closeDropdown() : openDropdown())}
          onKeyDown={handleTriggerKeyDown}
          title={selectedNovel ? `#${selectedNovel.id} · ${selectedNovel.title}` : undefined}
          className={`input-field focus-ring w-full text-left flex items-center justify-between gap-2 cursor-pointer disabled:opacity-60 disabled:cursor-not-allowed ${
            selectedNovel && !disabled ? 'pr-14' : 'pr-8'
          }`}
        >
          {selectedNovel ? (
            <span className="min-w-0 flex-1 truncate flex items-center gap-1.5">
              <span className="font-mono-code tabular-nums text-[var(--cms-muted-fg)] shrink-0">
                #{selectedNovel.id}
              </span>
              <span className="truncate text-[var(--cms-fg)]">{selectedNovel.title}</span>
            </span>
          ) : (
            <span className="truncate text-[var(--cms-placeholder)]">
              {novels.length === 0 ? '-- 暂无已上架书籍 --' : '-- 搜索书名或 ID 选择书籍 (必选) --'}
            </span>
          )}
        </button>

        {selectedNovel && !disabled && (
          <button
            type="button"
            aria-label="清除已选书籍"
            onClick={() => {
              onChange('');
              setQuery('');
              triggerRef.current?.focus();
            }}
            className="focus-ring absolute right-7 top-1/2 -translate-y-1/2 w-5 h-5 rounded-full text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] inline-flex items-center justify-center cursor-pointer transition-colors"
          >
            <X size={12} aria-hidden="true" />
          </button>
        )}

        <ChevronDown
          size={14}
          aria-hidden="true"
          className={`pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-[var(--cms-muted-fg)] transition-transform ${
            effectiveOpen ? 'rotate-180' : ''
          }`}
        />
      </div>

      {effectiveOpen && (
        <div className="absolute left-0 right-0 z-30 mt-1.5 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-card)] shadow-lg overflow-hidden">
          <div className="p-2 border-b border-[var(--cms-border)] bg-[var(--cms-content)]">
            <div className="relative">
              <Search
                size={13}
                aria-hidden="true"
                className="text-[var(--cms-placeholder)] absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none"
              />
              <input
                ref={searchInputRef}
                type="text"
                aria-label="搜索推广书籍"
                aria-controls={listboxId}
                aria-activedescendant={activeOptionId}
                autoCorrect="off"
                autoCapitalize="none"
                spellCheck={false}
                placeholder="输入书名或书籍 ID 搜索..."
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  setActiveIndex(0);
                }}
                onKeyDown={handleSearchKeyDown}
                className="input-field h-8 text-xs"
                style={{ paddingLeft: '28px', paddingRight: query ? '28px' : '10px' }}
              />
              {query && (
                <button
                  type="button"
                  aria-label="清除搜索关键词"
                  onClick={() => {
                    setQuery('');
                    setActiveIndex(0);
                    searchInputRef.current?.focus();
                  }}
                  className="focus-ring absolute right-1.5 top-1/2 -translate-y-1/2 w-5 h-5 rounded-full text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] inline-flex items-center justify-center cursor-pointer"
                >
                  <X size={11} aria-hidden="true" />
                </button>
              )}
            </div>
            <div className="mt-1.5 px-0.5 flex items-center justify-between text-[11px] text-[var(--cms-muted-fg)] tabular-nums">
              <span>
                {query.trim()
                  ? `匹配 ${filteredNovels.length} / ${novels.length} 本已上架书籍`
                  : `共 ${novels.length} 本已上架书籍`}
              </span>
              <span>↑↓ 选择 · Enter 确认</span>
            </div>
          </div>

          <div
            id={listboxId}
            role="listbox"
            aria-label="已上架书籍选项"
            className="max-h-56 overflow-y-auto p-1 space-y-0.5"
          >
            {filteredNovels.length === 0 ? (
              <div className="py-6 px-3 text-center text-xs text-[var(--cms-muted-fg)] space-y-2">
                <div>{novels.length === 0 ? '暂无已上架书籍' : `未找到匹配「${query.trim()}」的书籍`}</div>
                {query.trim() && (
                  <button
                    type="button"
                    className="btn-ghost h-7 px-2.5 text-xs text-[var(--cms-primary)]"
                    onClick={() => {
                      setQuery('');
                      setActiveIndex(0);
                      searchInputRef.current?.focus();
                    }}
                  >
                    清除搜索条件
                  </button>
                )}
              </div>
            ) : (
              filteredNovels.map((novel, idx) => {
                const isSelected = String(novel.id) === value;
                const isActive = idx === clampedActiveIndex;
                const pricingParts: string[] = [];
                if (novel.coin_cost_per_thousand != null) {
                  pricingParts.push(`${novel.coin_cost_per_thousand}币/千字`);
                }
                if (novel.start_pay_chapter_index != null) {
                  pricingParts.push(`第${novel.start_pay_chapter_index}章起付`);
                }
                return (
                  <button
                    key={novel.id}
                    ref={(el) => {
                      optionRefs.current[idx] = el;
                    }}
                    id={`${uid}-opt-${novel.id}`}
                    type="button"
                    role="option"
                    aria-selected={isSelected}
                    onMouseEnter={() => setActiveIndex(idx)}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => selectNovel(novel)}
                    title={`#${novel.id} · ${novel.title}`}
                    className={`w-full text-left px-2.5 py-2 rounded-lg flex items-center justify-between gap-2 cursor-pointer transition-colors ${
                      isActive
                        ? 'bg-[var(--cms-secondary)] text-[var(--cms-fg)]'
                        : 'text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)]'
                    } ${isSelected ? 'font-medium' : 'font-normal'}`}
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5 min-w-0">
                        <span className="font-mono-code tabular-nums text-[11px] text-[var(--cms-muted-fg)] shrink-0">
                          #{novel.id}
                        </span>
                        <span className="truncate">{novel.title}</span>
                      </div>
                      {pricingParts.length > 0 && (
                        <div className="text-[11px] text-[var(--cms-muted-fg)] tabular-nums mt-0.5">
                          {pricingParts.join(' · ')}
                        </div>
                      )}
                    </div>
                    {isSelected && (
                      <Check size={14} className="text-[var(--cms-primary)] shrink-0" aria-hidden="true" />
                    )}
                  </button>
                );
              })
            )}
          </div>
        </div>
      )}
    </div>
  );
}

export default function PromotionsTab() {
  const { canWrite: hasWrite } = usePermission();
  const canWrite = hasWrite('ads.promotions');
  const [links, setLinks] = useState<PromotionLink[]>([]);
  const [novels, setNovels] = useState<PromotionLinkNovelOption[]>([]);
  const [pixels, setPixels] = useState<PromotionLinkPixelOption[]>([]);
  const [templates, setTemplates] = useState<PromotionLinkTemplateOption[]>([]);
  const [domains, setDomains] = useState<PromotionLinkDomainOption[]>([]);
  const [globalDefaults, setGlobalDefaults] = useState<{
    coinCostPerThousand?: number;
    startPayChapterIndex?: number;
  }>({});
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);

  const [searchQuery, setSearchQuery] = useState('');
  const [sourceFilter, setSourceFilter] = useState('');
  const [envFilter, setEnvFilter] = useState<'' | 'live' | 'sandbox'>('');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(15);
  const [density, setDensity] = useState<TableDensity>('comfortable');
  const [showCreatedAt, setShowCreatedAt] = useState(true);

  const [isCreateModalOpen, setIsCreateModalOpen] = useState(false);
  const [createSubmitting, setCreateSubmitting] = useState(false);
  const [createForm, setCreateForm] = useState(EMPTY_CREATE_FORM);
  const [createdUrl, setCreatedUrl] = useState('');

  const [isEditModalOpen, setIsEditModalOpen] = useState(false);
  const [editingLink, setEditingLink] = useState<PromotionLink | null>(null);
  const [editSubmitting, setEditSubmitting] = useState(false);
  const [editForm, setEditForm] = useState({
    name: '',
    environment: 'live' as 'live' | 'sandbox',
    chapterIndex: '1',
    utmSource: 'facebook',
    utmCampaign: '',
    domainId: '',
    fbPixelId: '',
    rechargeTemplateId: '',
    coinCostPerThousand: '',
    startPayChapterIndex: '',
  });

  const fetchLinks = async () => {
    try {
      const data = await apiRequest('GET', '/promotion-links');
      setLinks(data || []);
      setHasLoadedOnce(true);
    } catch (err: unknown) {
      window.showToast?.(`加载推广链接失败: ${err instanceof Error ? err.message : ''}`, 'error');
      setHasLoadedOnce(true);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    const controller = new AbortController();
    void fetchLinks();
    void getPromotionLinkOptions(controller.signal)
      .then((opts) => {
        if (controller.signal.aborted || !opts) return;
        setNovels(opts.novels || []);
        setPixels(opts.pixels || []);
        setTemplates(opts.templates || []);
        setDomains(opts.domains || []);
        setGlobalDefaults({
          coinCostPerThousand: opts.defaults?.global_coin_cost_per_thousand,
          startPayChapterIndex: opts.defaults?.global_start_pay_chapter_index,
        });
      })
      .catch(() => {});
    return () => controller.abort();
  }, []);

  const openCreateModal = () => {
    if (!canWrite) return;
    setCreateForm({
      ...EMPTY_CREATE_FORM,
      fbPixelId: pixels[0] ? String(pixels[0].id) : '',
    });
    setCreatedUrl('');
    setIsCreateModalOpen(true);
  };

  const handleCreateSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canWrite || createSubmitting) return;

    const selectedNovel = novels.find((n) => String(n.id) === createForm.novelId);
    if (!selectedNovel) {
      window.showToast?.('请选择要推广的书籍', 'error');
      return;
    }

    const trimmedName = createForm.name.trim();
    if (!trimmedName) {
      window.showToast?.('请填写推广名称', 'error');
      return;
    }

    const built = validateAndBuildPromotionPayload(
      {
        novelId: selectedNovel.id,
        novelTitle: selectedNovel.title,
        name: trimmedName,
        environment: createForm.environment,
        chapterIndex: createForm.chapterIndex,
        utmSource: createForm.utmSource,
        utmCampaign: createForm.utmCampaign,
        domainId: createForm.domainId,
        fbPixelId: createForm.fbPixelId,
        rechargeTemplateId: createForm.rechargeTemplateId,
        coinCostPerThousand: createForm.coinCostPerThousand,
        startPayChapterIndex: createForm.startPayChapterIndex,
      },
      domains,
      pixels,
    );
    if (built.error || !built.payload) {
      window.showToast?.(built.error || '推广参数无效', 'error');
      return;
    }

    setCreateSubmitting(true);
    try {
      const created = await apiRequest('POST', '/promotion-links', built.payload);
      const url = created?.generated_url || built.finalUrl || '';
      setCreatedUrl(url);
      window.showToast?.('新建推广链接成功！', 'success');
      void fetchLinks();
    } catch (err: unknown) {
      window.showToast?.(`新建推广链接失败：${err instanceof Error ? err.message : ''}`, 'error');
    } finally {
      setCreateSubmitting(false);
    }
  };

  const handleEditClick = (link: PromotionLink) => {
    if (!canWrite) return;
    setEditingLink(link);
    setEditForm({
      name: link.name || '',
      environment: link.environment === 'sandbox' ? 'sandbox' : 'live',
      chapterIndex: link.chapter_index > 0 ? String(link.chapter_index) : '',
      utmSource: link.utm_source || 'facebook',
      utmCampaign: link.utm_campaign || '',
      domainId: link.domain_id ? String(link.domain_id) : '',
      fbPixelId: link.fb_pixel_id ? String(link.fb_pixel_id) : '',
      rechargeTemplateId: link.recharge_template_id ? String(link.recharge_template_id) : '',
      coinCostPerThousand:
        link.coin_cost_per_thousand !== null && link.coin_cost_per_thousand !== undefined
          ? String(link.coin_cost_per_thousand)
          : '',
      startPayChapterIndex:
        link.start_pay_chapter_index !== null && link.start_pay_chapter_index !== undefined
          ? String(link.start_pay_chapter_index)
          : '',
    });
    setIsEditModalOpen(true);
  };

  const handleEditSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canWrite || !editingLink || editSubmitting) return;

    const built = validateAndBuildPromotionPayload(
      {
        novelId: editingLink.novel_id,
        name: editForm.name,
        environment: editForm.environment,
        chapterIndex: editForm.chapterIndex,
        utmSource: editForm.utmSource,
        utmCampaign: editForm.utmCampaign,
        domainId: editForm.domainId,
        fbPixelId: editForm.fbPixelId,
        rechargeTemplateId: editForm.rechargeTemplateId,
        coinCostPerThousand: editForm.coinCostPerThousand,
        startPayChapterIndex: editForm.startPayChapterIndex,
        linkId: editingLink.id,
      },
      domains,
      pixels,
    );
    if (built.error || !built.payload) {
      window.showToast?.(built.error || '推广参数无效', 'error');
      return;
    }

    setEditSubmitting(true);
    try {
      await apiRequest('PUT', `/promotion-links/${editingLink.id}`, built.payload);

      window.showToast?.('修改推广链接成功！', 'success');
      setIsEditModalOpen(false);
      void fetchLinks();
    } catch (err: unknown) {
      window.showToast?.(`修改推广链接失败：${err instanceof Error ? err.message : ''}`, 'error');
    } finally {
      setEditSubmitting(false);
    }
  };

  const handleDelete = (id: number) => {
    if (!canWrite) return;
    window.showConfirm?.('确定要删除这条推广链接记录吗？此操作无法撤销。', async () => {
      try {
        await apiRequest('DELETE', `/promotion-links/${id}`);
        setLinks((prev) => prev.filter((l) => l.id !== id));
        window.showToast?.('删除成功！', 'success');
      } catch (err: unknown) {
        window.showToast?.(`删除失败: ${err instanceof Error ? err.message : ''}`, 'error');
      }
    });
  };

  const handleCopy = async (url: string) => {
    const ok = await copyTextToClipboard(url);
    window.showToast?.(
      ok ? '推广链接已复制到剪贴板！' : '复制失败，请手动选中链接复制',
      ok ? 'success' : 'error',
    );
  };

  const filteredLinks = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    return links.filter((link) => {
      const matchesSearch =
        !q ||
        String(link.id).includes(q) ||
        link.novel_title.toLowerCase().includes(q) ||
        link.utm_campaign.toLowerCase().includes(q) ||
        (link.name && link.name.toLowerCase().includes(q));
      const matchesSource = !sourceFilter || link.utm_source.toLowerCase() === sourceFilter.toLowerCase();
      const linkEnv = link.environment === 'sandbox' ? 'sandbox' : 'live';
      const matchesEnv = !envFilter || linkEnv === envFilter;
      return matchesSearch && matchesSource && matchesEnv;
    });
  }, [links, searchQuery, sourceFilter, envFilter]);

  const totalPages = Math.max(1, Math.ceil(filteredLinks.length / pageSize));
  const safePage = Math.min(page, totalPages);
  const paginatedLinks = filteredLinks.slice((safePage - 1) * pageSize, safePage * pageSize);

  const uniqueSources = Array.from(new Set(links.map((l) => l.utm_source).filter(Boolean)));
  const activeDomains = (domains || []).filter((d) => d.status === undefined || d.status === 1);
  const defaultDomain =
    activeDomains.find((d) => d.is_default) || activeDomains.find((d) => d.type === 'main') || activeDomains[0];
  const selectedCreateNovel = novels.find((n) => String(n.id) === createForm.novelId);
  const createCostPlaceholder =
    selectedCreateNovel?.coin_cost_per_thousand != null
      ? `留空继承书籍 (${selectedCreateNovel.coin_cost_per_thousand})`
      : globalDefaults.coinCostPerThousand != null
        ? `留空继承全局 (${globalDefaults.coinCostPerThousand})`
        : '留空继承默认';
  const createPayChPlaceholder =
    selectedCreateNovel?.start_pay_chapter_index != null
      ? `留空继承书籍 (第 ${selectedCreateNovel.start_pay_chapter_index} 章)`
      : globalDefaults.startPayChapterIndex != null
        ? `留空继承全局 (第 ${globalDefaults.startPayChapterIndex} 章)`
        : '留空继承默认';

  const resetFilters = () => {
    setSearchQuery('');
    setSourceFilter('');
    setEnvFilter('');
    setPage(1);
  };

  const filterChips = useMemo<FilterChip[]>(() => {
    const chips: FilterChip[] = [];
    if (searchQuery) {
      chips.push({
        key: 'q',
        label: `搜索: ${searchQuery}`,
        onClear: () => {
          setSearchQuery('');
          setPage(1);
        },
      });
    }
    if (sourceFilter) {
      chips.push({
        key: 'source',
        label: `渠道: ${sourceFilter}`,
        onClear: () => {
          setSourceFilter('');
          setPage(1);
        },
      });
    }
    if (envFilter) {
      chips.push({
        key: 'env',
        label: `环境: ${envFilter === 'sandbox' ? 'Sandbox 沙箱' : 'Live 正式'}`,
        onClear: () => {
          setEnvFilter('');
          setPage(1);
        },
      });
    }
    return chips;
  }, [searchQuery, sourceFilter, envFilter]);

  const columnOptions: ColumnOption[] = [
    { key: 'created_at', label: '创建时间', visible: showCreatedAt },
  ];

  const colSpan = showCreatedAt ? 9 : 8;

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Unified Borderless Filter Strip */}
      <search className="block space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2.5">
          <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
            <SearchInput
              className="w-full sm:w-80"
              ariaLabel="搜索推广 ID、名称或书名"
              placeholder="搜索推广 ID、名称、书名或活动..."
              value={searchQuery}
              onChange={(val) => {
                setSearchQuery(val);
                setPage(1);
              }}
            />

            <CustomSelect
              aria-label="筛选广告渠道"
              options={[
                { value: '', label: '全部广告渠道' },
                ...uniqueSources.map((src) => ({ value: src, label: src })),
              ]}
              value={sourceFilter}
              onChange={(val) => {
                setSourceFilter(val);
                setPage(1);
              }}
              width="160px"
            />

            <CustomSelect
              aria-label="筛选推广环境"
              options={[
                { value: '', label: '全部环境' },
                { value: 'live', label: 'Live 正式' },
                { value: 'sandbox', label: 'Sandbox 沙箱' },
              ]}
              value={envFilter}
              onChange={(val) => {
                setEnvFilter((val === 'live' || val === 'sandbox' ? val : '') as '' | 'live' | 'sandbox');
                setPage(1);
              }}
              width="140px"
            />

            {(searchQuery || sourceFilter || envFilter) && (
              <button type="button" onClick={resetFilters} className="btn-ghost h-9 text-xs text-[var(--cms-muted-fg)]">
                重置
              </button>
            )}
          </div>

          <div className="flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
            <span className="tabular-nums">共 {filteredLinks.length} 条</span>
            <ViewOptionsPopover
              density={density}
              onDensityChange={setDensity}
              columns={columnOptions}
              onToggleColumn={(key) => {
                if (key === 'created_at') setShowCreatedAt((v) => !v);
              }}
            />
            {canWrite && (
              <button type="button" onClick={openCreateModal} className="btn-primary h-9 px-3.5 text-xs">
                <Plus size={14} aria-hidden="true" />
                <span>新建推广链接</span>
              </button>
            )}
          </div>
        </div>

        <FilterChipsBar chips={filterChips} onResetAll={resetFilters} />
      </search>

      {/* Promotions Table + External Pagination */}
      <TableRegion
        ariaLabel="推广链接列表"
        footer={
          <Pagination
            page={safePage}
            pageSize={pageSize}
            pageSizeOptions={[10, 15, 20, 50, 100]}
            total={filteredLinks.length}
            onPageChange={setPage}
            onPageSizeChange={(nextSize) => {
              setPageSize(nextSize);
              setPage(1);
            }}
            itemLabel="条链接"
          />
        }
      >
        <table className="cms-table min-w-[960px]" data-density={density}>
          <thead>
            <tr>
              <th scope="col" className="whitespace-nowrap">推广 ID</th>
              <th scope="col">推广名称 / 活动</th>
              <th scope="col">关联书籍 / 落地页</th>
              <th scope="col">域名与渠道</th>
              <th scope="col">像素 &amp; 模板</th>
              <th scope="col">计费规则</th>
              <th scope="col">推广地址</th>
              {showCreatedAt && <th scope="col">创建时间</th>}
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={6} cols={colSpan} />
            ) : paginatedLinks.length === 0 ? (
              <tr>
                <td colSpan={colSpan} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  没有找到匹配的推广链接记录
                </td>
              </tr>
            ) : (
              paginatedLinks.map((link) => {
                const pixelName = link.fb_pixel_id
                  ? pixels.find((p) => p.id === link.fb_pixel_id)?.name || `Pixel #${link.fb_pixel_id}`
                  : '-';
                const tplName = link.recharge_template_id
                  ? templates.find((t) => t.id === link.recharge_template_id)?.name || `Tpl #${link.recharge_template_id}`
                  : '默认模板';

                return (
                  <tr key={link.id}>
                    <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">#{link.id}</td>
                    <td>
                      <div className="flex items-center gap-1.5 max-w-[200px]">
                        <span className="text-[var(--cms-fg)] truncate" title={link.name}>
                          {link.name || <span className="text-[var(--cms-muted-fg)]">未命名 #{link.id}</span>}
                        </span>
                        <span
                          className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${
                            link.environment === 'sandbox'
                              ? 'bg-[var(--cms-warning)]/15 text-[var(--cms-warning)]'
                              : 'bg-[var(--cms-success)]/15 text-[var(--cms-success)]'
                          }`}
                        >
                          {link.environment === 'sandbox' ? 'Sandbox' : 'Live'}
                        </span>
                      </div>
                      <div
                        className="text-[var(--cms-muted-fg)] mt-0.5 truncate max-w-[180px]"
                        title={link.utm_campaign}
                      >
                        {link.utm_campaign || '无 utm_campaign'}
                      </div>
                    </td>
                    <td>
                      <div className="text-[var(--cms-fg)] truncate max-w-[180px]" title={link.novel_title}>
                        {link.novel_title}
                      </div>
                      <div className="text-[var(--cms-muted-fg)] mt-0.5">
                        {link.chapter_index > 0 ? `第 ${link.chapter_index} 章` : '书籍详情页'}
                      </div>
                    </td>
                    <td>
                      <div className="text-[var(--cms-fg)]">{link.utm_source}</div>
                      <div
                        className="text-[var(--cms-muted-fg)] mt-0.5 truncate max-w-[160px]"
                        title={link.domain || '默认主域名'}
                      >
                        {link.domain || '默认主域名'}
                      </div>
                    </td>
                    <td>
                      <div className="text-[var(--cms-fg)] truncate max-w-[150px]" title={pixelName}>
                        {pixelName}
                      </div>
                      <div className="text-[var(--cms-muted-fg)] mt-0.5 truncate max-w-[150px]" title={tplName}>
                        {tplName}
                      </div>
                    </td>
                    <td className="tabular-nums">
                      <div>
                        {link.coin_cost_per_thousand !== null && link.coin_cost_per_thousand !== undefined ? (
                          <span className="text-[var(--cms-fg)]">{link.coin_cost_per_thousand} 币/千字</span>
                        ) : (
                          <span className="text-[var(--cms-muted-fg)]">单价继承默认</span>
                        )}
                      </div>
                      <div className="mt-0.5">
                        {link.start_pay_chapter_index !== null && link.start_pay_chapter_index !== undefined ? (
                          <span className="text-[var(--cms-fg)]">第 {link.start_pay_chapter_index} 章起付</span>
                        ) : (
                          <span className="text-[var(--cms-muted-fg)]">起付继承默认</span>
                        )}
                      </div>
                    </td>
                    <td>
                      <div className="max-w-[200px]">
                        <span
                          className="text-[var(--cms-muted-fg)] truncate block"
                          title={link.generated_url}
                        >
                          {link.generated_url}
                        </span>
                      </div>
                    </td>
                    {showCreatedAt && (
                      <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                        {new Date(link.created_at).toLocaleString('zh-CN', { hour12: false })}
                      </td>
                    )}
                    <td className="text-right whitespace-nowrap">
                      <div className="inline-flex items-center justify-end gap-0.5 whitespace-nowrap">
                        <button
                          type="button"
                          onClick={() => handleCopy(link.generated_url)}
                          title="复制推广链接"
                          className="btn-detail whitespace-nowrap"
                        >
                          复制
                        </button>
                        {canWrite && (
                          <RowActionMenu
                            ariaLabel={`推广链接 ${link.name || link.id} 操作`}
                            items={[
                              { label: '编辑', onClick: () => handleEditClick(link) },
                              {
                                label: '删除',
                                tone: 'danger',
                                dividerBefore: true,
                                onClick: () => handleDelete(link.id),
                              },
                            ]}
                          />
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </TableRegion>

      {/* Create Promotion Link Slide-Over Drawer */}
      <CmsDialog
        open={Boolean(canWrite && isCreateModalOpen)}
        onClose={() => !createSubmitting && setIsCreateModalOpen(false)}
        closedBy={createSubmitting ? 'none' : 'any'}
        closeDisabled={createSubmitting}
        variant="drawer"
        title="新建推广链接"
        subtitle="选择已上架书籍与像素目标，直接生成推广投放链接"
        size="md"
        footer={
          <>
            <button
              type="button"
              disabled={createSubmitting}
              className="btn-secondary"
              onClick={() => !createSubmitting && setIsCreateModalOpen(false)}
            >
              {createdUrl ? '完成' : '取消'}
            </button>
            {!createdUrl && (
              <button type="submit" form="create-promo-form" disabled={createSubmitting} className="btn-primary">
                {createSubmitting ? '创建中...' : '生成并保存'}
              </button>
            )}
          </>
        }
      >
        <form id="create-promo-form" onSubmit={handleCreateSubmit} className="space-y-4 text-xs">
          <label className="block space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">
              推广名称 <span className="text-[var(--cms-error)]">*</span>
            </span>
            <input
              type="text"
              autoFocus
              className="input-field"
              placeholder="例如: FB_BlackFriday_Campaign"
              value={createForm.name}
              onChange={(e) => setCreateForm({ ...createForm, name: e.target.value })}
              disabled={createSubmitting || Boolean(createdUrl)}
              required
            />
          </label>

          <div className="space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">
              推广书籍 <span className="text-[var(--cms-error)]">*</span>
            </span>
            <NovelSearchSelect
              novels={novels}
              value={createForm.novelId}
              disabled={createSubmitting || Boolean(createdUrl)}
              onChange={(val) => setCreateForm({ ...createForm, novelId: val })}
            />
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <label className="space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">落地章节索引（可选）</span>
              <input
                type="number"
                min={0}
                className="input-field tabular-nums"
                value={createForm.chapterIndex}
                disabled={createSubmitting || Boolean(createdUrl)}
                onChange={(e) => setCreateForm({ ...createForm, chapterIndex: e.target.value })}
              />
            </label>

            <label className="space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">投放落地页域名（可选）</span>
              <CustomSelect
                aria-label="投放落地页域名"
                width="100%"
                value={createForm.domainId}
                disabled={createSubmitting || Boolean(createdUrl)}
                onChange={(val) => setCreateForm({ ...createForm, domainId: val })}
                options={[
                  {
                    value: '',
                    label: `-- 默认主域名 (${defaultDomain?.domain || '主站默认域名'}) --`,
                  },
                  ...activeDomains.map((d) => ({
                    value: String(d.id),
                    label: `${d.name} (${d.domain}) ${d.is_default ? '[默认主域名]' : ''}`,
                  })),
                ]}
              />
            </label>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <label className="space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">广告渠道 (utm_source)</span>
              <input
                type="text"
                aria-label="广告渠道"
                className="input-field"
                placeholder="例如: facebook, tiktok"
                value={createForm.utmSource}
                disabled={createSubmitting || Boolean(createdUrl)}
                onChange={(e) => setCreateForm({ ...createForm, utmSource: e.target.value })}
              />
            </label>

            <label className="space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">
                绑定 Facebook 像素 <span className="text-[var(--cms-error)]">*</span>
              </span>
              <CustomSelect
                aria-label="绑定 Facebook 像素"
                width="100%"
                value={createForm.fbPixelId}
                disabled={createSubmitting || Boolean(createdUrl)}
                onChange={(val) => setCreateForm({ ...createForm, fbPixelId: val })}
                options={[
                  { value: '', label: pixels.length === 0 ? '-- 请先添加像素配置 --' : '-- 选择像素 (必选) --' },
                  ...pixels.map((p) => ({ value: String(p.id), label: `${p.name} (${p.pixel_id})` })),
                ]}
              />
            </label>
          </div>

          <label className="block space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">广告活动名称 (utm_campaign)</span>
            <input
              type="text"
              className="input-field"
              placeholder="例如: blackfriday_adset1"
              value={createForm.utmCampaign}
              disabled={createSubmitting || Boolean(createdUrl)}
              onChange={(e) => setCreateForm({ ...createForm, utmCampaign: e.target.value })}
            />
          </label>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <label className="space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">专属千字计费（可选）</span>
              <input
                type="number"
                className="input-field tabular-nums"
                placeholder={createCostPlaceholder}
                min={0}
                value={createForm.coinCostPerThousand}
                disabled={createSubmitting || Boolean(createdUrl)}
                onChange={(e) => setCreateForm({ ...createForm, coinCostPerThousand: e.target.value })}
              />
            </label>

            <label className="space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">专属起付章节（可选）</span>
              <input
                type="number"
                className="input-field tabular-nums"
                placeholder={createPayChPlaceholder}
                min={1}
                value={createForm.startPayChapterIndex}
                disabled={createSubmitting || Boolean(createdUrl)}
                onChange={(e) => setCreateForm({ ...createForm, startPayChapterIndex: e.target.value })}
              />
            </label>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <label className="space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">推广环境</span>
              <CustomSelect
                aria-label="推广环境"
                width="100%"
                value={createForm.environment}
                disabled={createSubmitting || Boolean(createdUrl)}
                onChange={(val) => {
                  const nextEnv: 'live' | 'sandbox' = val === 'sandbox' ? 'sandbox' : 'live';
                  const matched = filterPromotionTemplatesByEnv(templates, nextEnv).some(
                    (t) => String(t.id) === createForm.rechargeTemplateId,
                  );
                  setCreateForm({
                    ...createForm,
                    environment: nextEnv,
                    rechargeTemplateId: matched ? createForm.rechargeTemplateId : '',
                  });
                }}
                options={[
                  { value: 'live', label: 'Live 正式（线上投放）' },
                  { value: 'sandbox', label: 'Sandbox 沙箱（仅联调测试）' },
                ]}
              />
            </label>

            <label className="space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">绑定充值模板（可选）</span>
              <CustomSelect
                aria-label="绑定充值模板"
                width="100%"
                value={createForm.rechargeTemplateId}
                disabled={createSubmitting || Boolean(createdUrl)}
                onChange={(val) => setCreateForm({ ...createForm, rechargeTemplateId: val })}
                options={[
                  { value: '', label: '-- 使用默认激活模板 --' },
                  ...filterPromotionTemplatesByEnv(templates, createForm.environment).map((t) => ({
                    value: String(t.id),
                    label: `${t.name} ${t.is_default ? '(默认)' : ''}`,
                  })),
                ]}
              />
            </label>
          </div>
          <span className="block text-[11px] text-[var(--cms-muted-fg)] -mt-2">
            {createForm.environment === 'sandbox'
              ? 'Sandbox 推广链接仅可绑定沙箱模板且仅在沙箱/测试环境生效，不会泄漏到线上 Live 流量。'
              : '该模板绑定仅对通过此推广链接首次注册/首次触达的读者生效；已归因老用户仍按首触推广或默认模板计价。'}
          </span>

          {createdUrl && (
            <div className="p-3.5 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] space-y-2">
              <div className="text-xs font-medium text-[var(--cms-muted-fg)]">已生成的推广链接地址</div>
              <input type="text" className="input-field font-mono-code text-xs" readOnly value={createdUrl} />
              <button type="button" className="btn-secondary w-full" onClick={() => void handleCopy(createdUrl)}>
                复制链接到剪贴板
              </button>
            </div>
          )}
        </form>
      </CmsDialog>

      {/* Edit Promotion Link Slide-Over Drawer */}
      <CmsDialog
        open={Boolean(canWrite && isEditModalOpen && editingLink)}
        onClose={() => !editSubmitting && setIsEditModalOpen(false)}
        closedBy={editSubmitting ? 'none' : 'any'}
        closeDisabled={editSubmitting}
        variant="drawer"
        title="编辑推广链接"
        subtitle={editingLink ? `${editingLink.novel_title} · #${editingLink.id}` : undefined}
        size="md"
        footer={
          <>
            <button
              type="button"
              disabled={editSubmitting}
              className="btn-secondary"
              onClick={() => !editSubmitting && setIsEditModalOpen(false)}
            >
              取消
            </button>
            <button type="submit" form="edit-promo-form" disabled={editSubmitting} className="btn-primary">
              {editSubmitting ? '保存中...' : '确认保存'}
            </button>
          </>
        }
      >
        {editingLink && (
          <form id="edit-promo-form" onSubmit={handleEditSubmit} className="space-y-4 text-xs">
            <label className="block space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">推广名称</span>
              <input
                type="text"
                autoFocus
                className="input-field"
                value={editForm.name}
                onChange={(e) => setEditForm({ ...editForm, name: e.target.value })}
                required
              />
            </label>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <label className="space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">推广书籍</span>
                <input type="text" className="input-field" disabled value={editingLink.novel_title} />
              </label>
              <label className="space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">落地章节索引（可选）</span>
                <input
                  type="number"
                  className="input-field tabular-nums"
                  value={editForm.chapterIndex}
                  onChange={(e) => setEditForm({ ...editForm, chapterIndex: e.target.value })}
                />
              </label>
            </div>

            <label className="block space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">投放落地页域名（可选）</span>
              <CustomSelect
                aria-label="投放落地页域名"
                width="100%"
                value={editForm.domainId}
                onChange={(val) => setEditForm({ ...editForm, domainId: val })}
                options={[
                  {
                    value: '',
                    label: `-- 默认主域名 (${defaultDomain?.domain || '主站默认域名'}) --`,
                  },
                  ...activeDomains.map((d) => ({
                    value: String(d.id),
                    label: `${d.name} (${d.domain}) ${d.is_default ? '[默认主域名]' : ''}`,
                  })),
                ]}
              />
            </label>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <label className="space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">广告渠道 (utm_source)</span>
                <input
                  type="text"
                  aria-label="广告渠道"
                  className="input-field"
                  placeholder="例如: facebook, tiktok"
                  value={editForm.utmSource}
                  onChange={(e) => setEditForm({ ...editForm, utmSource: e.target.value })}
                />
              </label>

              <label className="space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">
                  绑定 Facebook 像素 <span className="text-[var(--cms-error)]">*</span>
                </span>
                <CustomSelect
                  aria-label="绑定 Facebook 像素"
                  width="100%"
                  value={editForm.fbPixelId}
                  onChange={(val) => setEditForm({ ...editForm, fbPixelId: val })}
                  options={[
                    { value: '', label: pixels.length === 0 ? '-- 请先添加像素配置 --' : '-- 选择像素 (必选) --' },
                    ...pixels.map((p) => ({ value: String(p.id), label: `${p.name} (${p.pixel_id})` })),
                  ]}
                />
              </label>
            </div>

            <label className="block space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">广告活动名称 (utm_campaign)</span>
              <input
                type="text"
                className="input-field"
                value={editForm.utmCampaign}
                onChange={(e) => setEditForm({ ...editForm, utmCampaign: e.target.value })}
              />
            </label>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <label className="space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">专属千字计费（可选）</span>
                <input
                  type="number"
                  className="input-field tabular-nums"
                  placeholder="留空继承默认"
                  min={0}
                  value={editForm.coinCostPerThousand}
                  onChange={(e) => setEditForm({ ...editForm, coinCostPerThousand: e.target.value })}
                />
              </label>

              <label className="space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">专属起付章节（可选）</span>
                <input
                  type="number"
                  className="input-field tabular-nums"
                  placeholder="留空继承默认"
                  min={1}
                  value={editForm.startPayChapterIndex}
                  onChange={(e) => setEditForm({ ...editForm, startPayChapterIndex: e.target.value })}
                />
              </label>
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <label className="space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">推广环境</span>
                <CustomSelect
                  aria-label="推广环境"
                  width="100%"
                  value={editForm.environment}
                  onChange={(val) => {
                    const nextEnv: 'live' | 'sandbox' = val === 'sandbox' ? 'sandbox' : 'live';
                    const matched = filterPromotionTemplatesByEnv(templates, nextEnv).some(
                      (t) => String(t.id) === editForm.rechargeTemplateId,
                    );
                    setEditForm({
                      ...editForm,
                      environment: nextEnv,
                      rechargeTemplateId: matched ? editForm.rechargeTemplateId : '',
                    });
                  }}
                  options={[
                    { value: 'live', label: 'Live 正式（线上投放）' },
                    { value: 'sandbox', label: 'Sandbox 沙箱（仅联调测试）' },
                  ]}
                />
              </label>

              <label className="space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">绑定充值模板（可选）</span>
                <CustomSelect
                  aria-label="绑定充值模板"
                  width="100%"
                  value={editForm.rechargeTemplateId}
                  onChange={(val) => setEditForm({ ...editForm, rechargeTemplateId: val })}
                  options={[
                    { value: '', label: '-- 使用默认激活模板 --' },
                    ...filterPromotionTemplatesByEnv(templates, editForm.environment).map((t) => ({
                      value: String(t.id),
                      label: `${t.name} ${t.is_default ? '(默认)' : ''}`,
                    })),
                  ]}
                />
              </label>
            </div>
            <span className="block text-[11px] text-[var(--cms-muted-fg)] -mt-2">
              {editForm.environment === 'sandbox'
                ? 'Sandbox 推广链接仅可绑定沙箱模板且仅在沙箱/测试环境生效，不会泄漏到线上 Live 流量。'
                : '该模板绑定仅对通过此推广链接首次注册/首次触达的读者生效；已归因老用户仍按首触推广或默认模板计价。'}
            </span>
          </form>
        )}
      </CmsDialog>
    </div>
  );
}

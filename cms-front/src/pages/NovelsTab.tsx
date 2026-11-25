import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import {
  AlertCircle,
  BookOpen,
  Copy,
  Edit,
  Link2,
  Plus,
  Settings,
  SlidersHorizontal,
} from 'lucide-react';
import {
  apiRequest,
  getPromotionLinkOptions,
  type PromotionLinkDomainOption,
  type PromotionLinkPixelOption,
  type PromotionLinkTemplateOption,
} from '../utils/api';
import { usePermission } from '../context/PermissionContext';
import { copyTextToClipboard } from '../utils/clipboard';
import { validateAndBuildPromotionPayload } from '../utils/promotion';
import { FALLBACK_COVER } from '../components/operations/operationsUtils';
import { DEFAULT_CHAPTER_DELIMITER_REGEX } from '../components/novels/ChapterUploadFields';
import { MAX_GENRES_LENGTH, NovelFormModal, type NovelFormData } from '../components/novels/NovelFormModal';
import { IntegrationConfigDrawer } from '../components/conversion/IntegrationConfigDrawer';
import { defaultPolicySummary, type PaymentPolicyView, policyEndpoint, policyVersion } from '../components/conversion/paymentPolicy';
import type { ConversionIntegration } from '../components/conversion/types';
import { PromotionLinkModal } from '../components/novels/PromotionLinkModal';
import { CmsDialog } from '../components/ui/CmsDialog';
import { Pagination } from '../components/ui/Pagination';
import {
  type ColumnOption,
  type FilterChip,
  FilterChipsBar,
  RowActionMenu,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ToolbarPopover,
  ViewOptionsPopover,
} from '../components/ui/Primitives';

interface Novel {
  id: number;
  title: string;
  author: string;
  cover_url: string;
  cover_key?: string;
  rating: number;
  status: string;
  publish_status?: string;
  synopsis: string;
  genres: string[];
  word_count: number;
  view_count: number;
  coin_cost_per_thousand?: number | null;
  content_version: number;
  effective_coin_cost_per_thousand?: number;
  start_pay_chapter_index?: number;
  created_at?: string;
  updated_at?: string;
}

interface NovelListFilterDraft {
  titleOrId: string;
  author: string;
  publishStatus: '' | 'published' | 'draft' | 'offline';
  createdStart: string;
  createdEnd: string;
}

interface ChapterIdentity {
  id: string;
  title: string;
  chapter_index: number | null;
}

interface Chapter {
  id: string;
  novel_id: number;
  chapter_index: number;
  title: string;
  content?: string;
  word_count: number;
  is_paid: boolean;
  price: number;
}

const EMPTY_NOVEL_FILTER: NovelListFilterDraft = {
  titleOrId: '',
  author: '',
  publishStatus: '',
  createdStart: '',
  createdEnd: '',
};

const DEFAULT_NEW_NOVEL_GENRES = 'Contemporary, Romance, Cheating';

const STATUS_TABS: Array<{ value: NovelListFilterDraft['publishStatus']; label: string }> = [
  { value: '', label: '全部' },
  { value: 'published', label: '已上架' },
  { value: 'draft', label: '草稿' },
  { value: 'offline', label: '已下架' },
];

type NovelColKey = 'genres' | 'word_count' | 'pricing' | 'rating' | 'created_at' | 'updated_at';

function formatNovelDateTime(value?: string, withSeconds = false): string {
  if (!value) return '-';
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return '-';
  return parsed.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    ...(withSeconds ? { second: '2-digit' } : {}),
    hour12: false,
  });
}

function getPublishStatusMeta(publishStatus?: string): {
  label: string;
  tone: 'success' | 'warning' | 'error';
} {
  switch (publishStatus) {
    case 'published':
      return { label: '已上架', tone: 'success' };
    case 'offline':
      return { label: '已下架', tone: 'error' };
    case 'draft':
    default:
      return { label: '草稿', tone: 'warning' };
  }
}

export default function NovelsTab() {
  const { canRead, canWrite } = usePermission();
  const canEditNovels = canWrite('content.novels');
  const canEditGlobalSettings = canWrite('content.novels');
  const canCreatePromo = canWrite('ads.promotions');
  const canReadConversions = canRead('ads.conversions');
  const canWriteConversions = canWrite('ads.conversions');

  const [novels, setNovels] = useState<Novel[]>([]);
  const [totalNovels, setTotalNovels] = useState(0);
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);

  const [novelQuery, setNovelQuery] = useState<NovelListFilterDraft & { page: number; pageSize: number }>({
    page: 1,
    pageSize: 10,
    ...EMPTY_NOVEL_FILTER,
  });
  const [filterDraft, setFilterDraft] = useState<NovelListFilterDraft>(EMPTY_NOVEL_FILTER);
  const [dateFilterError, setDateFilterError] = useState('');

  // Table density & optional columns
  const [density, setDensity] = useState<TableDensity>('compact');
  const [visibleCols, setVisibleCols] = useState<Record<NovelColKey, boolean>>({
    genres: true,
    word_count: true,
    pricing: true,
    rating: false,
    created_at: true,
    updated_at: true,
  });

  // Batch & single status
  const [publishUpdatingId, setPublishUpdatingId] = useState<number | null>(null);
  const [selectedNovelIds, setSelectedNovelIds] = useState<number[]>([]);
  const [batchPublishUpdating, setBatchPublishUpdating] = useState(false);

  // Book Detail Drawer state
  const [selectedNovel, setSelectedNovel] = useState<Novel | null>(null);
  const [isDetailOpen, setIsDetailOpen] = useState(false);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [chaptersLoading, setChaptersLoading] = useState(false);
  const [chapterPage, setChapterPage] = useState(1);
  const [chapterPageSize, setChapterPageSize] = useState(15);

  // Chapter reader preview state (inside Book Detail Drawer)
  const [previewChapter, setPreviewChapter] = useState<Chapter | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState('');
  const [previewTargetMeta, setPreviewTargetMeta] = useState<{
    index: number;
    title: string;
    word_count: number;
    is_paid: boolean;
    price: number;
  } | null>(null);
  const [readerWidthMode, setReaderWidthMode] = useState<'desktop' | 'mobile'>('desktop');
  const previewAbortRef = useRef<AbortController | null>(null);
  const previewFetchSeqRef = useRef(0);
  const chaptersFetchSeqRef = useRef(0);

  // Create / Edit novel drawer state
  const [isCreating, setIsCreating] = useState(false);
  const [isSubmittingNovel, setIsSubmittingNovel] = useState(false);
  const [newNovel, setNewNovel] = useState<NovelFormData>({
    title: '',
    author: '',
    cover_url: '',
    cover_preview_url: '',
    status: 'Ongoing',
    publish_status: 'draft',
    rating: 4.5,
    synopsis: '',
    genres: DEFAULT_NEW_NOVEL_GENRES,
    coin_cost_per_thousand: '',
    start_pay_chapter_index: 3,
  });
  const [createImportType, setCreateImportType] = useState<'single_txt' | 'zip'>('single_txt');
  const [createImportFile, setCreateImportFile] = useState<File | null>(null);
  const [createRegex, setCreateRegex] = useState(DEFAULT_CHAPTER_DELIMITER_REGEX);
  const [createModalError, setCreateModalError] = useState('');
  const [createdDraftId, setCreatedDraftId] = useState<number | null>(null);

  const [isEditing, setIsEditing] = useState(false);
  const [editNovelForm, setEditNovelForm] = useState<NovelFormData>({
    title: '',
    author: '',
    cover_url: '',
    cover_preview_url: '',
    status: 'Ongoing',
    publish_status: 'draft',
    rating: 4.5,
    synopsis: '',
    genres: '',
    coin_cost_per_thousand: '',
    start_pay_chapter_index: 3,
  });

  // Bulk chapter import states inside Edit Drawer
  const [editImportFile, setEditImportFile] = useState<File | null>(null);
  const [editImportType, setEditImportType] = useState<'single_txt' | 'zip'>('single_txt');
  const [editRegex, setEditRegex] = useState(DEFAULT_CHAPTER_DELIMITER_REGEX);
  const [editModalError, setEditModalError] = useState('');
  const [editChapterMapping, setEditChapterMapping] = useState('');
  const [editChapterIdentities, setEditChapterIdentities] = useState<ChapterIdentity[]>([]);

  const createdStartRef = useRef<HTMLInputElement>(null);
  const selectAllCheckboxRef = useRef<HTMLInputElement>(null);
  const novelsAbortRef = useRef<AbortController | null>(null);
  const novelsFetchSeqRef = useRef(0);

  const searchFormId = useId();
  const dateFilterErrorId = useId();
  const globalCoinInputId = useId();
  const globalCoinApplyAllId = useId();
  const globalChapterInputId = useId();
  const globalChapterApplyAllId = useId();

  // Promo builder states
  const [isPromoModalOpen, setIsPromoModalOpen] = useState(false);
  const [promoSubmitting, setPromoSubmitting] = useState(false);
  const [pixels, setPixels] = useState<PromotionLinkPixelOption[]>([]);
  const [templates, setTemplates] = useState<PromotionLinkTemplateOption[]>([]);
  const [domains, setDomains] = useState<PromotionLinkDomainOption[]>([]);
  const [promotionForm, setPromotionForm] = useState<{
    name: string;
    novelId: number;
    title: string;
    chapterIndex: string;
    utmSource: string;
    utmCampaign: string;
    domainId?: string;
    fbPixelId: string;
    rechargeTemplateId: string;
    coinCostPerThousand: string;
    startPayChapterIndex: string;
  }>({
    name: '',
    novelId: 0,
    title: '',
    chapterIndex: '1',
    utmSource: 'facebook',
    utmCampaign: '',
    domainId: '',
    fbPixelId: '',
    rechargeTemplateId: '',
    coinCostPerThousand: '',
    startPayChapterIndex: '',
  });
  const [generatedLink, setGeneratedLink] = useState('');
  const [promoIntegration, setPromoIntegration] = useState<ConversionIntegration | null>(null);
  const [promoDefaultPolicy, setPromoDefaultPolicy] = useState<PaymentPolicyView | null>(null);
  const [promoLinkPolicy, setPromoLinkPolicy] = useState<PaymentPolicyView | null>(null);
  const [promoLinkId, setPromoLinkId] = useState('');
  const [promoRuleChoice, setPromoRuleChoice] = useState<'inherit' | 'own'>('inherit');
  const [isPromoRuleOpen, setIsPromoRuleOpen] = useState(false);
  const promoWriteRef = useRef(false);

  useEffect(() => {
    if (!isPromoModalOpen || !canReadConversions) return;
    const controller = new AbortController();
    setPromoIntegration(null);
    setPromoDefaultPolicy(null);
    void (async () => {
      try {
        const items: ConversionIntegration[] = await apiRequest('GET', '/conversion/integrations', undefined, false, controller.signal);
        const reader = items.find((item) => item.source === 'starnovel' && item.account_key === 'reader');
        if (!reader || controller.signal.aborted) return;
        setPromoIntegration(reader);
        const defaults: PaymentPolicyView = await apiRequest('GET', policyEndpoint(reader.id, 'reader'), undefined, false, controller.signal);
        if (!controller.signal.aborted) setPromoDefaultPolicy(defaults);
      } catch {
        // Link creation can use the existing default even if the summary is unavailable.
      }
    })();
    return () => controller.abort();
  }, [isPromoModalOpen, canReadConversions]);

  // Global pricing / chapter settings
  const [globalCoinCost, setGlobalCoinCost] = useState(500);
  const [globalStartPayChapter, setGlobalStartPayChapter] = useState(3);
  const [globalCoinDraft, setGlobalCoinDraft] = useState<number | string>(500);
  const [globalStartPayDraft, setGlobalStartPayDraft] = useState<number | string>(3);
  const [applyCoinCostToAll, setApplyCoinCostToAll] = useState(false);
  const [resetNovelCoinCost, setResetNovelCoinCost] = useState(false);
  const [applyStartPayToAll, setApplyStartPayToAll] = useState(false);
  const [activeGlobalPanel, setActiveGlobalPanel] = useState<'coin' | 'chapter' | null>(null);
  const [savingGlobalSetting, setSavingGlobalSetting] = useState(false);

  const openGlobalPanel = (panel: 'coin' | 'chapter') => {
    setGlobalCoinDraft(globalCoinCost);
    setGlobalStartPayDraft(globalStartPayChapter);
    setApplyCoinCostToAll(false);
    setResetNovelCoinCost(false);
    setApplyStartPayToAll(false);
    setActiveGlobalPanel(panel);
  };

  const currentPage = novelQuery.page;
  const startChIndex = (chapterPage - 1) * chapterPageSize;
  const paginatedChapters = chapters.slice(startChIndex, startChIndex + chapterPageSize);

  const fetchGlobalConfig = async () => {
    try {
      const data = await apiRequest('GET', '/settings');
      if (data.global_coin_cost_per_thousand) setGlobalCoinCost(Number(data.global_coin_cost_per_thousand));
      if (data.global_start_pay_chapter_index) setGlobalStartPayChapter(Number(data.global_start_pay_chapter_index));
    } catch (err) {
      console.error('加载全局配置失败:', err);
    }
  };

  useEffect(() => {
    void fetchGlobalConfig();
  }, []);

  useEffect(() => {
    if (!canCreatePromo) return;
    const controller = new AbortController();
    void getPromotionLinkOptions(controller.signal)
      .then((opts) => {
        if (controller.signal.aborted) return;
        setPixels(opts.pixels || []);
        setTemplates(opts.templates || []);
        setDomains(opts.domains || []);
      })
      .catch((err) => {
        if (!controller.signal.aborted) {
          console.error('加载推广选项失败:', err);
        }
      });
    return () => controller.abort();
  }, [canCreatePromo]);

  const fetchNovels = useCallback(async (targetQuery: NovelListFilterDraft & { page: number; pageSize: number }) => {
    novelsAbortRef.current?.abort();
    const controller = new AbortController();
    novelsAbortRef.current = controller;
    const seq = ++novelsFetchSeqRef.current;

    setLoading(true);
    try {
      const query = new URLSearchParams({
        page: String(targetQuery.page),
        page_size: String(targetQuery.pageSize),
      });
      if (targetQuery.titleOrId) query.set('title_or_id', targetQuery.titleOrId);
      if (targetQuery.author) query.set('author', targetQuery.author);
      if (targetQuery.publishStatus) query.set('publish_status', targetQuery.publishStatus);
      if (targetQuery.createdStart) {
        query.set('created_start', new Date(`${targetQuery.createdStart}T00:00:00`).toISOString());
      }
      if (targetQuery.createdEnd) {
        query.set('created_end', new Date(`${targetQuery.createdEnd}T23:59:59.999`).toISOString());
      }
      const data = await apiRequest('GET', `/novels?${query.toString()}`, undefined, false, controller.signal);
      if (controller.signal.aborted || seq !== novelsFetchSeqRef.current) return;

      const nextNovels: Novel[] = data.novels || [];
      setNovels(nextNovels);
      setTotalNovels(data.total || 0);
      setSelectedNovelIds((prev) => {
        if (prev.length === 0) return prev;
        const currentIds = new Set(nextNovels.map((n) => n.id));
        const filtered = prev.filter((id) => currentIds.has(id));
        return filtered.length === prev.length ? prev : filtered;
      });
      setHasLoadedOnce(true);
    } catch (e: unknown) {
      if ((e as { name?: string })?.name === 'AbortError' || controller.signal.aborted) return;
      console.error(e);
      setHasLoadedOnce(true);
    } finally {
      if (seq === novelsFetchSeqRef.current) {
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    setSelectedNovelIds([]);
    void fetchNovels(novelQuery);
    return () => {
      novelsAbortRef.current?.abort();
    };
  }, [fetchNovels, novelQuery]);

  const allSelectedOnPage = novels.length > 0 && novels.every((n) => selectedNovelIds.includes(n.id));
  const someSelectedOnPage = novels.some((n) => selectedNovelIds.includes(n.id));

  useEffect(() => {
    if (selectAllCheckboxRef.current) {
      selectAllCheckboxRef.current.indeterminate = someSelectedOnPage && !allSelectedOnPage;
    }
  }, [someSelectedOnPage, allSelectedOnPage]);

  const clearPreviewState = () => {
    previewAbortRef.current?.abort();
    previewAbortRef.current = null;
    previewFetchSeqRef.current += 1;
    setPreviewChapter(null);
    setPreviewError('');
    setPreviewTargetMeta(null);
    setPreviewLoading(false);
  };

  const loadChapterPreview = async (novelId: number, ch: Chapter) => {
    previewAbortRef.current?.abort();
    const controller = new AbortController();
    previewAbortRef.current = controller;
    const seq = ++previewFetchSeqRef.current;

    setPreviewChapter(null);
    setPreviewError('');
    setPreviewTargetMeta({
      index: ch.chapter_index,
      title: ch.title,
      word_count: ch.word_count,
      is_paid: ch.is_paid,
      price: ch.price,
    });
    setPreviewLoading(true);
    try {
      const data = await apiRequest(
        'GET',
        `/novels/${novelId}/chapters/${ch.chapter_index}`,
        undefined,
        false,
        controller.signal,
      );
      if (controller.signal.aborted || seq !== previewFetchSeqRef.current) return;
      setPreviewChapter(data);
    } catch (e: unknown) {
      if (
        (e as { name?: string })?.name === 'AbortError' ||
        controller.signal.aborted ||
        seq !== previewFetchSeqRef.current
      ) {
        return;
      }
      const msg = e instanceof Error ? e.message : String(e) || '加载章节预览失败';
      setPreviewError(msg);
    } finally {
      if (seq === previewFetchSeqRef.current) {
        setPreviewLoading(false);
      }
    }
  };

  const fetchChapters = async (novelID: number, autoSelectFirst = false) => {
    const seq = ++chaptersFetchSeqRef.current;
    setChaptersLoading(true);
    try {
      const data: Chapter[] = (await apiRequest('GET', `/novels/${novelID}/chapters`)) || [];
      if (seq !== chaptersFetchSeqRef.current) return;
      setChapters(data);
      if (autoSelectFirst && data.length > 0) {
        void loadChapterPreview(novelID, data[0]);
      }
    } catch (e) {
      console.error(e);
    } finally {
      if (seq === chaptersFetchSeqRef.current) {
        setChaptersLoading(false);
      }
    }
  };

  const openEditNovelModal = (novel: Novel) => {
    setEditNovelForm({
      title: novel.title,
      author: novel.author,
      cover_url: novel.cover_key || novel.cover_url,
      cover_preview_url: novel.cover_url,
      status: novel.status,
      publish_status: novel.publish_status || 'draft',
      rating: novel.rating,
      synopsis: novel.synopsis || '',
      genres: novel.genres.join(', '),
      coin_cost_per_thousand: novel.coin_cost_per_thousand != null ? novel.coin_cost_per_thousand : '',
      start_pay_chapter_index: novel.start_pay_chapter_index || globalStartPayChapter || 3,
    });
    setEditImportType('single_txt');
    setEditImportFile(null);
    setEditRegex(DEFAULT_CHAPTER_DELIMITER_REGEX);
    setEditChapterMapping('');
    setEditChapterIdentities([]);
    setEditModalError('');
    setIsEditing(true);
  };

  const openEditFromList = (novel: Novel) => {
    chaptersFetchSeqRef.current += 1;
    clearPreviewState();
    setIsDetailOpen(false);
    setSelectedNovel(novel);
    setChapters([]);
    openEditNovelModal(novel);
    void fetchChapters(novel.id, false);
  };

  const closeDetailDrawer = () => {
    chaptersFetchSeqRef.current += 1;
    clearPreviewState();
    setIsDetailOpen(false);
    setIsEditing(false);
    setEditImportFile(null);
    setEditChapterMapping('');
    setEditChapterIdentities([]);
    setEditModalError('');
    setSelectedNovel(null);
  };

  const handleNovelClick = (novel: Novel) => {
    chaptersFetchSeqRef.current += 1;
    clearPreviewState();
    setIsEditing(false);
    setEditImportFile(null);
    setEditChapterMapping('');
    setEditChapterIdentities([]);
    setEditModalError('');
    setSelectedNovel(novel);
    setIsDetailOpen(true);
    setChapters([]);
    setChapterPage(1);
    void fetchChapters(novel.id, true);
  };

  const handleCopyTitle = async (title: string) => {
    const ok = await copyTextToClipboard(title);
    window.showToast?.(
      ok ? '书名已复制到剪贴板！' : '复制失败，请手动复制书名',
      ok ? 'success' : 'error',
    );
  };

  const resetCreateModalState = () => {
    setNewNovel({
      title: '',
      author: '',
      cover_url: '',
      cover_preview_url: '',
      status: 'Ongoing',
      publish_status: 'draft',
      rating: 4.5,
      synopsis: '',
      genres: DEFAULT_NEW_NOVEL_GENRES,
      coin_cost_per_thousand: '',
      start_pay_chapter_index: globalStartPayChapter || 3,
    });
    setCreateImportType('single_txt');
    setCreateImportFile(null);
    setCreateRegex(DEFAULT_CHAPTER_DELIMITER_REGEX);
    setCreateModalError('');
    setCreatedDraftId(null);
  };

  const refreshFirstPageList = () => {
    if (novelQuery.page !== 1) {
      setNovelQuery((prev) => ({ ...prev, page: 1 }));
    } else {
      void fetchNovels({ ...novelQuery, page: 1 });
    }
  };

  const buildNovelPayload = (form: NovelFormData, publishStatus: string) => {
    const trimmedGenres = form.genres.trim();
    if (trimmedGenres.length > MAX_GENRES_LENGTH) {
      throw new Error(`分类标签不能超过 ${MAX_GENRES_LENGTH} 个字符`);
    }
    const parsedGenres = trimmedGenres.split(',').map((g) => g.trim()).filter(Boolean);
    if (parsedGenres.length === 0) {
      throw new Error('请输入至少一个有效的分类标签');
    }
    const rawCost = form.coin_cost_per_thousand;
    return {
      title: form.title,
      author: form.author,
      cover_url: form.cover_url,
      status: form.status,
      publish_status: publishStatus,
      rating: Number(form.rating),
      synopsis: form.synopsis,
      genres: parsedGenres,
      coin_cost_per_thousand: rawCost === '' || rawCost === null ? null : Number(rawCost),
      start_pay_chapter_index: Number(form.start_pay_chapter_index),
    };
  };

  const handleCreateNovel = async (e: React.FormEvent) => {
    e.preventDefault();
    if (isSubmittingNovel) return;
    if (!createImportFile) {
      const msg = '请上传书籍章节文件（支持 .txt 或 .zip）';
      setCreateModalError(msg);
      window.showToast?.(msg, 'error');
      return;
    }
    setIsSubmittingNovel(true);
    setCreateModalError('');
    let targetNovelId = createdDraftId;
    try {
      const payload = buildNovelPayload(newNovel, 'draft');
      if (!targetNovelId) {
        const created = await apiRequest('POST', '/novels', payload);
        targetNovelId = created?.id ?? null;
        if (targetNovelId) setCreatedDraftId(targetNovelId);
      } else {
        await apiRequest('PUT', `/novels/${targetNovelId}`, payload);
      }

      if (createImportFile && targetNovelId) {
        const formData = new FormData();
        formData.append('file', createImportFile);
        formData.append('import_type', createImportType);
        formData.append('delimiter_regex', createRegex);
        const importData = await apiRequest('POST', `/novels/${targetNovelId}/chapters/bulk-import`, formData, true);
        window.showToast?.(
          `书籍创建成功！已导入 ${importData.chapters_count} 个章节（总字数 ${importData.total_word_count} 字），当前为草稿状态。`,
          'success',
        );
      } else {
        window.showToast?.('新建书籍已保存为草稿！', 'success');
      }

      setIsCreating(false);
      resetCreateModalState();
      closeDetailDrawer();
      refreshFirstPageList();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '新建书籍失败';
      if (targetNovelId) {
        refreshFirstPageList();
        setCreateModalError(
          `书籍档案已保存为草稿（ID: ${targetNovelId}），但章节导入失败：${msg}。可调整文件或正则后重试导入，或点击右上角 × 关闭返回列表。`,
        );
      } else {
        setCreateModalError(msg);
      }
      window.showToast?.(msg, 'error');
    } finally {
      setIsSubmittingNovel(false);
    }
  };

  const saveGlobalSetting = async (
    payload: Record<string, string>,
    successMessage: string,
    onSuccess?: () => void,
  ) => {
    if (savingGlobalSetting) return;
    setSavingGlobalSetting(true);
    try {
      await apiRequest('POST', '/settings', payload);
      onSuccess?.();
      window.showToast?.(successMessage, 'success');
      setActiveGlobalPanel(null);
      await fetchNovels(novelQuery);
      if (selectedNovel) {
        await fetchChapters(selectedNovel.id, true);
        const updated = await apiRequest('GET', `/novels/${selectedNovel.id}`);
        if (updated) setSelectedNovel(updated);
      }
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '保存全局配置失败', 'error');
    } finally {
      setSavingGlobalSetting(false);
    }
  };

  const handleSaveGlobalCoinConfig = async (e: React.FormEvent) => {
    e.preventDefault();
    const costNum = Number(globalCoinDraft);
    if (String(globalCoinDraft).trim() === '' || !Number.isInteger(costNum) || costNum < 0) {
      window.showToast?.('全局千字收费金币必须为大于等于 0 的整数', 'error');
      return;
    }
    const payload = {
      global_coin_cost_per_thousand: String(costNum),
      apply_coin_cost_to_all_novels: applyCoinCostToAll ? 'true' : 'false',
      reset_novel_coin_cost: resetNovelCoinCost ? 'true' : 'false',
    };
    const msg = applyCoinCostToAll
      ? `全局金币单价已保存，并将全部在库书籍重置为千字 ${costNum} 币（各书起付章节保持不变）！`
      : resetNovelCoinCost
        ? '已将全部在库书籍改为继承全局单价。'
        : '全局金币单价保存成功！已同步继承全局单价的书籍。';

    const commit = () =>
      void saveGlobalSetting(payload, msg, () => {
        setGlobalCoinCost(costNum);
      });

    if (resetNovelCoinCost && window.showConfirm) {
      window.showConfirm('将清除全部未归档书籍的独立单价，并统一继承全局单价，确认继续？', commit);
      return;
    }

    if (applyCoinCostToAll && window.showConfirm) {
      window.showConfirm(
        `开启「同步覆盖所有已有书籍单价」将把全部未归档书籍（含独立定价书籍）统一重置为千字 ${costNum} 币，不会改动各书起始付费章节，确定继续吗？`,
        commit,
      );
      return;
    }
    await saveGlobalSetting(payload, msg, () => {
      setGlobalCoinCost(costNum);
    });
  };

  const handleSaveGlobalChapterConfig = async (e: React.FormEvent) => {
    e.preventDefault();
    const startNum = Number(globalStartPayDraft);
    if (String(globalStartPayDraft).trim() === '' || !Number.isInteger(startNum) || startNum < 1) {
      window.showToast?.('全局默认起始付费章节必须为大于等于 1 的整数', 'error');
      return;
    }
    const payload = {
      global_start_pay_chapter_index: String(startNum),
      apply_start_pay_to_all_novels: applyStartPayToAll ? 'true' : 'false',
    };
    const msg = applyStartPayToAll
      ? `全局起付章节已保存，并将全部在库书籍统一更新为第 ${startNum} 章起付费（千字金币单价保持不变）！`
      : '全局默认起付章节保存成功！将作为后续新建书籍的默认值。';

    if (applyStartPayToAll && window.showConfirm) {
      window.showConfirm(
        `开启「同步覆盖所有已有书籍起付章节」将把全部未归档书籍统一更新为第 ${startNum} 章起付费，不会改动千字金币单价，确定继续吗？`,
        () => {
          void saveGlobalSetting(payload, msg, () => {
            setGlobalStartPayChapter(startNum);
          });
        },
      );
      return;
    }
    await saveGlobalSetting(payload, msg, () => {
      setGlobalStartPayChapter(startNum);
    });
  };

  const commitNovelUpdate = async () => {
    if (!selectedNovel || isSubmittingNovel) return;
    setIsSubmittingNovel(true);
    setEditModalError('');
    let savedDuringRun: Novel | null = null;
    try {
      const desiredPublish = editNovelForm.publish_status || selectedNovel.publish_status || 'draft';
      const deferPublishUntilImport = Boolean(editImportFile) && chapters.length === 0 && desiredPublish === 'published';
      const payload = buildNovelPayload(
        editNovelForm,
        deferPublishUntilImport ? selectedNovel.publish_status || 'draft' : desiredPublish,
      );
      let updated: Novel = await apiRequest('PUT', `/novels/${selectedNovel.id}`, payload);
      savedDuringRun = updated;

      if (editImportFile) {
        const formData = new FormData();
        formData.append('file', editImportFile);
        formData.append('import_type', editImportType);
        formData.append('delimiter_regex', editRegex);
        if (editChapterMapping.trim()) {
          const mapping = JSON.parse(editChapterMapping);
          const versionForMapping = updated?.content_version ?? selectedNovel.content_version;
          formData.append('chapter_mapping', JSON.stringify({ ...mapping, version: versionForMapping }));
        }
        const importData = await apiRequest('POST', `/novels/${selectedNovel.id}/chapters/bulk-import`, formData, true);

        if (deferPublishUntilImport) {
          updated = await apiRequest('PATCH', `/novels/${selectedNovel.id}/publish-status`, {
            publish_status: 'published',
          });
        } else {
          updated = (await apiRequest('GET', `/novels/${selectedNovel.id}`)) || updated;
        }
        window.showToast?.(
          `书籍更新成功！已导入 ${importData.chapters_count} 个章节（总字数 ${importData.total_word_count} 字）。`,
          'success',
        );
      } else {
        window.showToast?.('书籍信息更新成功', 'success');
      }

      setEditImportFile(null);
      setEditChapterMapping('');
      setIsEditing(false);
      void fetchNovels(novelQuery);
      if (isDetailOpen) {
        setSelectedNovel(updated);
        void fetchChapters(selectedNovel.id, true);
      } else {
        setSelectedNovel(null);
      }
    } catch (err: unknown) {
      if (savedDuringRun) {
        setSelectedNovel(savedDuringRun);
        void fetchNovels(novelQuery);
        void fetchChapters(savedDuringRun.id, true);
      }
      const msg = (err instanceof Error ? err.message : '') || '更新书籍失败';
      setEditModalError(msg);
      window.showToast?.(msg, 'error');
    } finally {
      setIsSubmittingNovel(false);
    }
  };

  const handleUpdateNovel = (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedNovel || isSubmittingNovel) return;
    if (editImportFile && chapters.length > 0 && window.showConfirm) {
      window.showConfirm(
        '已选择新手稿，保存后将替换现有章节目录并重新编号（读者购买权益随章节 ID 保留）。确认保存并导入？',
        () => {
          void commitNovelUpdate();
        },
      );
      return;
    }
    void commitNovelUpdate();
  };

  const handleTogglePublishStatus = (novel: Novel, targetStatus: 'published' | 'offline') => {
    const runUpdate = async () => {
      setPublishUpdatingId(novel.id);
      try {
        const updated = await apiRequest('PATCH', `/novels/${novel.id}/publish-status`, {
          publish_status: targetStatus,
        });
        if (selectedNovel?.id === novel.id) {
          setSelectedNovel(updated);
        }
        window.showToast?.(
          targetStatus === 'published' ? '书籍已成功上架到书城！' : '书籍已下架，读者端将停止展示与访问。',
          'success',
        );
        void fetchNovels(novelQuery);
      } catch (err: unknown) {
        window.showToast?.((err instanceof Error ? err.message : '') || '更新上架状态失败', 'error');
      } finally {
        setPublishUpdatingId(null);
      }
    };

    if (targetStatus === 'offline' && window.showConfirm) {
      window.showConfirm(
        `确定将《${novel.title}》下架吗？下架后书城、搜索、书架与推广链接均会拦截访问。`,
        () => {
          void runUpdate();
        },
      );
      return;
    }
    void runUpdate();
  };

  const handleToggleSelectNovel = (id: number) => {
    setSelectedNovelIds((prev) => (prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]));
  };

  const handleToggleSelectAllOnPage = (checked: boolean) => {
    setSelectedNovelIds(checked ? novels.map((n) => n.id) : []);
  };

  const handleBatchPublishStatus = (targetStatus: 'published' | 'offline') => {
    if (selectedNovelIds.length === 0 || batchPublishUpdating) return;
    const count = selectedNovelIds.length;
    const idsSnapshot = [...selectedNovelIds];

    const runBatch = async () => {
      setBatchPublishUpdating(true);
      try {
        const res = await apiRequest('PATCH', '/novels/batch-publish-status', {
          ids: idsSnapshot,
          publish_status: targetStatus,
        });
        const updatedCount = res?.updated_count ?? idsSnapshot.length;
        const skippedList: Array<{ id: number; title?: string; reason: string }> = res?.skipped || [];
        if (skippedList.length > 0) {
          const skippedNames = skippedList
            .slice(0, 3)
            .map((item) => (item.title ? `《${item.title}》` : `#${item.id}`))
            .join('、');
          const suffix = skippedList.length > 3 ? ` 等 ${skippedList.length} 本` : '';
          window.showToast?.(
            `已${targetStatus === 'published' ? '上架' : '下架'} ${updatedCount} 本书籍；另有 ${skippedList.length} 本已跳过（${skippedNames}${suffix}：${skippedList[0].reason}）`,
            'info',
          );
        } else {
          window.showToast?.(
            targetStatus === 'published' ? `已成功批量上架 ${updatedCount} 本书籍！` : `已成功批量下架 ${updatedCount} 本书籍！`,
            'success',
          );
        }
        setSelectedNovelIds([]);
        void fetchNovels(novelQuery);
      } catch (err: unknown) {
        window.showToast?.((err instanceof Error ? err.message : '') || '批量更新上架状态失败', 'error');
      } finally {
        setBatchPublishUpdating(false);
      }
    };

    if (targetStatus === 'offline' && window.showConfirm) {
      window.showConfirm(
        `确定将选中的 ${count} 本书籍全部下架吗？下架后书城、搜索、书架与推广链接均会拦截访问。`,
        () => {
          void runBatch();
        },
      );
      return;
    }
    void runBatch();
  };

  const commitFilters = (overrideDraft?: NovelListFilterDraft) => {
    const nextDraft = overrideDraft ?? filterDraft;
    if (nextDraft.createdStart && nextDraft.createdEnd && nextDraft.createdStart > nextDraft.createdEnd) {
      setDateFilterError('开始日期不能晚于结束日期');
      createdStartRef.current?.focus();
      return false;
    }
    setDateFilterError('');
    setNovelQuery((prev) => ({
      ...prev,
      page: 1,
      titleOrId: nextDraft.titleOrId.trim(),
      author: nextDraft.author.trim(),
      publishStatus: nextDraft.publishStatus,
      createdStart: nextDraft.createdStart,
      createdEnd: nextDraft.createdEnd,
    }));
    return true;
  };

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    commitFilters();
  };

  const handleStatusTabSelect = (nextStatus: NovelListFilterDraft['publishStatus']) => {
    setFilterDraft((prev) => ({ ...prev, publishStatus: nextStatus }));
    setNovelQuery((prev) => ({
      ...prev,
      page: 1,
      publishStatus: nextStatus,
    }));
  };

  const handleResetFilters = () => {
    setDateFilterError('');
    setFilterDraft((prev) => ({ ...EMPTY_NOVEL_FILTER, publishStatus: prev.publishStatus }));
    setNovelQuery((prev) => ({
      ...prev,
      ...EMPTY_NOVEL_FILTER,
      publishStatus: prev.publishStatus,
      page: 1,
    }));
  };

  const handleDeleteNovel = (novel: Novel) => {
    window.showConfirm?.(
      `确定删除《${novel.title}》吗？书籍将被归档隐藏，清空 OSS 章节正文并停止全部读者端访问，此操作不可撤销！`,
      async () => {
        try {
          await apiRequest('DELETE', `/novels/${novel.id}`);
          window.showToast?.('书籍已删除归档', 'success');
          if (selectedNovel?.id === novel.id) {
            closeDetailDrawer();
          }
          const nextPage = novels.length === 1 && novelQuery.page > 1 ? novelQuery.page - 1 : novelQuery.page;
          if (nextPage !== novelQuery.page) {
            setNovelQuery((prev) => ({ ...prev, page: nextPage }));
          } else {
            void fetchNovels({ ...novelQuery, page: nextPage });
          }
        } catch (err: unknown) {
          window.showToast?.(err instanceof Error ? err.message : '删除失败', 'error');
        }
      },
    );
  };

  const loadChapterIdentities = async (novelId: number) => {
    try {
      const identities = await apiRequest('GET', `/novels/${novelId}/chapters/identities`);
      setEditChapterIdentities(identities ?? []);
    } catch {
      setEditModalError('无法加载章节标识，请刷新后重试。');
    }
  };

  const openPromoModalForNovel = (novel: Novel) => {
    const pubMeta = getPublishStatusMeta(novel.publish_status);
    if (novel.publish_status !== 'published') {
      window.showToast?.(`提示：当前书籍为「${pubMeta.label}」状态，投放前请先导入章节并上架发布。`, 'info');
    }
    setPromotionForm({
      name: '',
      novelId: novel.id,
      title: novel.title,
      chapterIndex: '1',
      utmSource: 'facebook',
      utmCampaign: '',
      domainId: '',
      fbPixelId: '',
      rechargeTemplateId: '',
      coinCostPerThousand: '',
      startPayChapterIndex: '',
    });
    setGeneratedLink('');
    setPromoLinkId('');
    setPromoLinkPolicy(null);
    setPromoRuleChoice('inherit');
    setIsPromoRuleOpen(false);
    setIsPromoModalOpen(true);
  };

  const handleGeneratePromotionLink = async () => {
    if (!canCreatePromo || promoWriteRef.current || generatedLink) return;

    const built = validateAndBuildPromotionPayload(
      {
        novelId: promotionForm.novelId,
        name: promotionForm.name,
        novelTitle: promotionForm.title,
        chapterIndex: promotionForm.chapterIndex,
        utmSource: promotionForm.utmSource,
        utmCampaign: promotionForm.utmCampaign,
        domainId: promotionForm.domainId || '',
        fbPixelId: promotionForm.fbPixelId,
        rechargeTemplateId: promotionForm.rechargeTemplateId,
        coinCostPerThousand: promotionForm.coinCostPerThousand,
        startPayChapterIndex: promotionForm.startPayChapterIndex,
      },
      domains,
      pixels,
    );

    if (built.error || !built.payload || !built.params || !built.baseUrl || !built.path || !built.finalUrl) {
      window.showToast?.(built.error || '推广参数无效', 'error');
      return;
    }

    promoWriteRef.current = true;
    setPromoSubmitting(true);
    try {
      const created = await apiRequest('POST', '/promotion-links', built.payload);

      let resolvedUrl = built.finalUrl;
      if (created && created.id) {
        built.params.set('link_id', String(created.id));
        resolvedUrl = `${built.baseUrl}${built.path}?${built.params.toString()}`;
      }
      setGeneratedLink(created?.generated_url || resolvedUrl);
      const createdId = created?.id ? String(created.id) : '';
      setPromoLinkId(createdId);
      if (promoRuleChoice === 'own' && createdId && promoIntegration && canWriteConversions) setIsPromoRuleOpen(true);
      window.showToast?.('推广链接已成功生成并记录！', 'success');
    } catch (err: unknown) {
      window.showToast?.(`保存推广链接失败：${err instanceof Error ? err.message : ''}`, 'error');
    } finally {
      promoWriteRef.current = false;
      setPromoSubmitting(false);
    }
  };

  const handleCopyLink = async () => {
    if (!generatedLink) return;
    const ok = await copyTextToClipboard(generatedLink);
    window.showToast?.(
      ok ? '链接已复制到剪贴板！' : '复制失败，请手动选中链接复制',
      ok ? 'success' : 'error',
    );
  };

  const toggleColumn = (key: string) => {
    const colKey = key as NovelColKey;
    setVisibleCols((prev) => ({ ...prev, [colKey]: !prev[colKey] }));
  };

  const columnOptions: ColumnOption[] = [
    { key: 'genres', label: '分类标签', visible: visibleCols.genres },
    { key: 'word_count', label: '总字数', visible: visibleCols.word_count },
    { key: 'pricing', label: '计费策略', visible: visibleCols.pricing },
    { key: 'rating', label: '评分', visible: visibleCols.rating },
    { key: 'created_at', label: '创建时间', visible: visibleCols.created_at },
    { key: 'updated_at', label: '更新时间', visible: visibleCols.updated_at },
  ];

  const advFilterCount =
    (novelQuery.author ? 1 : 0) + (novelQuery.createdStart || novelQuery.createdEnd ? 1 : 0);

  const filterChips = useMemo<FilterChip[]>(() => {
    const chips: FilterChip[] = [];
    if (novelQuery.titleOrId) {
      chips.push({
        key: 'titleOrId',
        label: `搜索: ${novelQuery.titleOrId}`,
        onClear: () => {
          setFilterDraft((prev) => ({ ...prev, titleOrId: '' }));
          setNovelQuery((prev) => ({ ...prev, page: 1, titleOrId: '' }));
        },
      });
    }
    if (novelQuery.author) {
      chips.push({
        key: 'author',
        label: `作者: ${novelQuery.author}`,
        onClear: () => {
          setFilterDraft((prev) => ({ ...prev, author: '' }));
          setNovelQuery((prev) => ({ ...prev, page: 1, author: '' }));
        },
      });
    }
    if (novelQuery.createdStart || novelQuery.createdEnd) {
      chips.push({
        key: 'created',
        label: `创建日期: ${novelQuery.createdStart || '不限'} ~ ${novelQuery.createdEnd || '至今'}`,
        onClear: () => {
          setDateFilterError('');
          setFilterDraft((prev) => ({ ...prev, createdStart: '', createdEnd: '' }));
          setNovelQuery((prev) => ({ ...prev, page: 1, createdStart: '', createdEnd: '' }));
        },
      });
    }
    return chips;
  }, [novelQuery]);

  const hasAnyFilter = Boolean(
    filterDraft.titleOrId ||
      filterDraft.author ||
      filterDraft.createdStart ||
      filterDraft.createdEnd ||
      novelQuery.titleOrId ||
      novelQuery.author ||
      novelQuery.createdStart ||
      novelQuery.createdEnd,
  );

  const tableColSpan =
    (canEditNovels ? 1 : 0) +
    3 + // 书籍 + 状态 + 操作
    (visibleCols.genres ? 1 : 0) +
    (visibleCols.word_count ? 1 : 0) +
    (visibleCols.pricing ? 1 : 0) +
    (visibleCols.rating ? 1 : 0) +
    (visibleCols.created_at ? 1 : 0) +
    (visibleCols.updated_at ? 1 : 0);

  const selectedDetailStartPay = selectedNovel
    ? selectedNovel.start_pay_chapter_index || globalStartPayChapter || 3
    : 3;
  const selectedDetailMeta = selectedNovel ? getPublishStatusMeta(selectedNovel.publish_status) : null;

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Status Underline Tabs + Global/Create Actions */}
      <div className="border-b border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-3">
        <div role="tablist" aria-label="书籍上架状态筛选" className="flex items-center gap-6 overflow-x-auto">
          {STATUS_TABS.map((tab) => {
            const active = novelQuery.publishStatus === tab.value;
            return (
              <button
                key={tab.value || 'all'}
                type="button"
                role="tab"
                aria-selected={active}
                onClick={() => handleStatusTabSelect(tab.value)}
                className={`focus-ring pb-2.5 text-sm font-medium whitespace-nowrap cursor-pointer transition-colors ${
                  active ? 'text-[var(--cms-fg)]' : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                }`}
              >
                {tab.label}
              </button>
            );
          })}
        </div>

        <div className="pb-1.5 flex flex-wrap items-center gap-2">
          {canEditGlobalSettings && (
            <>
              <button type="button" onClick={() => openGlobalPanel('coin')} className="btn-secondary h-8 px-3 text-xs">
                <Settings size={13} aria-hidden="true" />
                <span>全局计费 ({globalCoinCost} 币)</span>
              </button>
              <button type="button" onClick={() => openGlobalPanel('chapter')} className="btn-secondary h-8 px-3 text-xs">
                <BookOpen size={13} aria-hidden="true" />
                <span>起付 (第 {globalStartPayChapter} 章)</span>
              </button>
            </>
          )}
          {canEditNovels && (
            <button
              type="button"
              onClick={() => {
                resetCreateModalState();
                setIsCreating(true);
              }}
              className="btn-primary h-8 px-3.5 text-xs"
            >
              <Plus size={14} aria-hidden="true" />
              <span>新建书籍</span>
            </button>
          )}
        </div>
      </div>

      {/* Unified Borderless Filter Strip */}
      <search className="block space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2.5">
          <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
            <form id={searchFormId} onSubmit={handleSearchSubmit} className="w-full sm:w-80">
              <SearchInput
                className="w-full"
                ariaLabel="搜索书名或书籍 ID"
                placeholder="搜索书名或书籍 ID..."
                value={filterDraft.titleOrId}
                onChange={(val) => setFilterDraft((prev) => ({ ...prev, titleOrId: val }))}
                onClear={() => {
                  const nextDraft = { ...filterDraft, titleOrId: '' };
                  setFilterDraft(nextDraft);
                  if (novelQuery.titleOrId) commitFilters(nextDraft);
                }}
              />
            </form>

            <ToolbarPopover
              align="start"
              label="更多筛选"
              icon={<SlidersHorizontal size={13} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />}
              badgeCount={advFilterCount}
              panelClassName="w-80 p-4"
            >
              {({ close }) => (
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    if (commitFilters()) close();
                  }}
                  className="space-y-3.5"
                >
                  <div className="font-semibold text-[var(--cms-fg)]">作者与创建时间筛选</div>
                  <label className="block space-y-1">
                    <span className="block text-[var(--cms-muted-fg)]">作者名称</span>
                    <input
                      type="text"
                      className="input-field"
                      placeholder="输入作者姓名..."
                      value={filterDraft.author}
                      onChange={(e) => setFilterDraft((prev) => ({ ...prev, author: e.target.value }))}
                    />
                  </label>

                  <div className="grid grid-cols-2 gap-2.5">
                    <label className="block space-y-1">
                      <span className="block text-[var(--cms-muted-fg)]">创建起始</span>
                      <input
                        ref={createdStartRef}
                        type="date"
                        aria-label="创建起始日期"
                        className="input-field"
                        aria-invalid={dateFilterError ? 'true' : 'false'}
                        aria-describedby={dateFilterError ? dateFilterErrorId : undefined}
                        value={filterDraft.createdStart}
                        onChange={(e) => {
                          setDateFilterError('');
                          setFilterDraft((prev) => ({ ...prev, createdStart: e.target.value }));
                        }}
                      />
                    </label>
                    <label className="block space-y-1">
                      <span className="block text-[var(--cms-muted-fg)]">创建结束</span>
                      <input
                        type="date"
                        aria-label="创建结束日期"
                        className="input-field"
                        aria-invalid={dateFilterError ? 'true' : 'false'}
                        aria-describedby={dateFilterError ? dateFilterErrorId : undefined}
                        value={filterDraft.createdEnd}
                        onChange={(e) => {
                          setDateFilterError('');
                          setFilterDraft((prev) => ({ ...prev, createdEnd: e.target.value }));
                        }}
                      />
                    </label>
                  </div>

                  {dateFilterError && (
                    <p id={dateFilterErrorId} role="status" className="text-xs text-[var(--cms-error)] m-0">
                      {dateFilterError}
                    </p>
                  )}

                  <div className="pt-2 border-t border-[var(--cms-border)] flex items-center justify-end gap-2">
                    <button
                      type="button"
                      onClick={() => {
                        const cleared = { ...filterDraft, author: '', createdStart: '', createdEnd: '' };
                        setDateFilterError('');
                        setFilterDraft(cleared);
                        commitFilters(cleared);
                        close();
                      }}
                      className="btn-ghost text-[var(--cms-muted-fg)]"
                    >
                      清空
                    </button>
                    <button type="submit" className="btn-primary h-8 px-3 text-xs">
                      应用筛选
                    </button>
                  </div>
                </form>
              )}
            </ToolbarPopover>

            <button type="submit" form={searchFormId} className="btn-secondary h-9 px-3 text-xs font-normal whitespace-nowrap">
              查询
            </button>

            {hasAnyFilter && (
              <button
                type="button"
                onClick={handleResetFilters}
                className="btn-ghost h-9 text-xs text-[var(--cms-muted-fg)]"
              >
                重置
              </button>
            )}
          </div>

          <div className="flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
            <span className="tabular-nums">共 {totalNovels} 本书目</span>
            <ViewOptionsPopover
              density={density}
              onDensityChange={setDensity}
              columns={columnOptions}
              onToggleColumn={toggleColumn}
            />
          </div>
        </div>

        <FilterChipsBar chips={filterChips} onResetAll={handleResetFilters} />
      </search>

      {/* Batch Operations Bar */}
      {canEditNovels && selectedNovelIds.length > 0 && (
        <div className="px-4 py-2 rounded-xl bg-[var(--cms-card)] border border-[var(--cms-primary)]/40 flex flex-wrap items-center justify-between gap-3 text-xs animate-fade-in">
          <span className="font-medium text-[var(--cms-fg)] tabular-nums">
            已选择 {selectedNovelIds.length} 本书籍
          </span>
          <div className="flex items-center gap-2">
            <button
              type="button"
              disabled={batchPublishUpdating}
              onClick={() => setSelectedNovelIds([])}
              className="btn-ghost"
            >
              取消勾选
            </button>
            <button
              type="button"
              disabled={batchPublishUpdating}
              onClick={() => handleBatchPublishStatus('published')}
              className="btn-ghost"
              style={{ color: 'var(--cms-success)' }}
            >
              {batchPublishUpdating ? '处理中...' : '批量上架'}
            </button>
            <button
              type="button"
              disabled={batchPublishUpdating}
              onClick={() => handleBatchPublishStatus('offline')}
              className="btn-ghost"
              style={{ color: 'var(--cms-error)' }}
            >
              {batchPublishUpdating ? '处理中...' : '批量下架'}
            </button>
          </div>
        </div>
      )}

      {/* Minimalist Table + External Pagination */}
      <TableRegion
        ariaLabel="书籍管理列表"
        footer={
          <Pagination
            page={currentPage}
            pageSize={novelQuery.pageSize}
            total={totalNovels}
            disabled={loading}
            onPageChange={(nextPage) => setNovelQuery((prev) => ({ ...prev, page: nextPage }))}
            onPageSizeChange={(nextSize) => setNovelQuery((prev) => ({ ...prev, page: 1, pageSize: nextSize }))}
            itemLabel="本书"
          />
        }
      >
        <table className="cms-table min-w-[820px]" data-density={density}>
          <thead>
            <tr>
              {canEditNovels && (
                <th scope="col" className="w-11 text-center">
                  <input
                    ref={selectAllCheckboxRef}
                    type="checkbox"
                    aria-label="全选本页书籍"
                    checked={allSelectedOnPage}
                    disabled={novels.length === 0 || batchPublishUpdating}
                    onChange={(e) => handleToggleSelectAllOnPage(e.target.checked)}
                    className="w-4 h-4 cursor-pointer rounded align-middle"
                  />
                </th>
              )}
              <th scope="col">书籍</th>
              {visibleCols.genres && <th scope="col">分类标签</th>}
              <th scope="col">状态</th>
              {visibleCols.word_count && (
                <th scope="col" className="text-right">
                  总字数
                </th>
              )}
              {visibleCols.pricing && <th scope="col">计费策略</th>}
              {visibleCols.rating && (
                <th scope="col" className="text-right">
                  评分
                </th>
              )}
              {visibleCols.created_at && <th scope="col">创建时间</th>}
              {visibleCols.updated_at && <th scope="col">更新时间</th>}
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={6} cols={tableColSpan} />
            ) : novels.length === 0 ? (
              <tr>
                <td colSpan={tableColSpan} className="py-16 text-center">
                  <div className="text-[var(--cms-fg)]">暂无匹配条件的书籍</div>
                  <div className="text-[var(--cms-muted-fg)] mt-1">试试清除筛选条件或切换上架状态</div>
                  {hasAnyFilter && (
                    <button type="button" onClick={handleResetFilters} className="btn-secondary mt-3 h-8 px-3 text-xs">
                      重置筛选条件
                    </button>
                  )}
                </td>
              </tr>
            ) : (
              novels.map((novel) => {
                const pubMeta = getPublishStatusMeta(novel.publish_status);
                const isSelected = selectedNovelIds.includes(novel.id);
                const isViewing = isDetailOpen && selectedNovel?.id === novel.id;
                const effectiveCost =
                  novel.coin_cost_per_thousand != null
                    ? novel.coin_cost_per_thousand
                    : (novel.effective_coin_cost_per_thousand ?? globalCoinCost ?? 500);
                const isCustomPricing = novel.coin_cost_per_thousand != null;
                const startChapter = novel.start_pay_chapter_index || globalStartPayChapter || 3;

                return (
                  <tr
                    key={novel.id}
                    className={isSelected || isViewing ? 'row-selected' : undefined}
                  >
                    {canEditNovels && (
                      <td className="text-center">
                        <input
                          type="checkbox"
                          aria-label={`选择书籍《${novel.title}》`}
                          checked={isSelected}
                          disabled={batchPublishUpdating}
                          onChange={() => handleToggleSelectNovel(novel.id)}
                          className="w-4 h-4 cursor-pointer rounded align-middle"
                        />
                      </td>
                    )}
                    <td>
                      <div className="flex items-center gap-3.5">
                        <img
                          src={novel.cover_url || FALLBACK_COVER}
                          alt={novel.title}
                          onError={(e) => {
                            const img = e.currentTarget;
                            if (img.dataset.fb) return;
                            img.dataset.fb = '1';
                            img.src = FALLBACK_COVER;
                          }}
                          className="w-10 h-14 object-cover rounded-lg border border-[var(--cms-border)] shrink-0"
                        />
                        <div className="min-w-0">
                          <div className="flex items-center gap-1 min-w-0 max-w-[260px]">
                            <span className="text-[var(--cms-fg)] truncate" title={novel.title}>
                              {novel.title}
                            </span>
                            <button
                              type="button"
                              aria-label={`复制书名《${novel.title}》`}
                              title="复制书名"
                              onClick={() => void handleCopyTitle(novel.title)}
                              className="focus-ring shrink-0 p-1 rounded-md text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] inline-flex items-center justify-center cursor-pointer transition-colors"
                            >
                              <Copy size={12} aria-hidden="true" />
                            </button>
                          </div>
                          <div className="text-[var(--cms-muted-fg)] mt-0.5 truncate tabular-nums">
                            {novel.author} <span className="mx-1">·</span> #{novel.id}
                          </div>
                        </div>
                      </div>
                    </td>
                    {visibleCols.genres && (
                      <td
                        className="text-[var(--cms-muted-fg)] max-w-[160px] truncate"
                        title={novel.genres.join(', ')}
                      >
                        {novel.genres.join(' · ') || '未分类'}
                      </td>
                    )}
                    <td>
                      <div className="space-y-0.5">
                        <StatusDot tone={pubMeta.tone} label={pubMeta.label} />
                        <div className="text-[var(--cms-muted-fg)]">
                          {novel.status === 'Completed' ? '已完结' : '连载中'}
                        </div>
                      </div>
                    </td>
                    {visibleCols.word_count && (
                      <td className="text-right tabular-nums text-[var(--cms-fg)] whitespace-nowrap">
                        {novel.word_count.toLocaleString()} 字
                      </td>
                    )}
                    {visibleCols.pricing && (
                      <td className="tabular-nums">
                        <div className="text-[var(--cms-fg)] whitespace-nowrap">
                          {effectiveCost} 币/千字
                        </div>
                        <div className="text-[var(--cms-muted-fg)] mt-0.5 whitespace-nowrap">
                          {isCustomPricing ? '独立定价' : '全局基准'} · 第 {startChapter} 章起
                        </div>
                      </td>
                    )}
                    {visibleCols.rating && (
                      <td className="text-right tabular-nums text-[var(--cms-fg)] whitespace-nowrap">
                        ★ {novel.rating}
                      </td>
                    )}
                    {visibleCols.created_at && (
                      <td
                        className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap"
                        title={formatNovelDateTime(novel.created_at, true)}
                      >
                        {formatNovelDateTime(novel.created_at)}
                      </td>
                    )}
                    {visibleCols.updated_at && (
                      <td
                        className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap"
                        title={formatNovelDateTime(novel.updated_at, true)}
                      >
                        {formatNovelDateTime(novel.updated_at)}
                      </td>
                    )}
                    <td className="text-right whitespace-nowrap" onClick={(e) => e.stopPropagation()}>
                      <div className="inline-flex items-center justify-end gap-0.5 whitespace-nowrap">
                        <button
                          type="button"
                          onClick={() => handleNovelClick(novel)}
                          className="btn-detail whitespace-nowrap"
                        >
                          详情
                        </button>
                        {canCreatePromo && (
                          <button
                            type="button"
                            onClick={() => openPromoModalForNovel(novel)}
                            className="btn-detail whitespace-nowrap"
                          >
                            推广
                          </button>
                        )}
                        <RowActionMenu
                          ariaLabel={`《${novel.title}》更多操作`}
                          items={[
                            ...(canEditNovels
                              ? [
                                  {
                                    label: '编辑',
                                    tone: 'primary' as const,
                                    onClick: () => openEditFromList(novel),
                                  },
                                  {
                                    label:
                                      publishUpdatingId === novel.id
                                        ? '处理中...'
                                        : novel.publish_status === 'published'
                                          ? '下架书籍'
                                          : '上架发布',
                                    tone: (novel.publish_status === 'published' ? 'warning' : 'success') as
                                      | 'warning'
                                      | 'success',
                                    disabled: publishUpdatingId === novel.id || batchPublishUpdating,
                                    onClick: () =>
                                      handleTogglePublishStatus(
                                        novel,
                                        novel.publish_status === 'published' ? 'offline' : 'published',
                                      ),
                                  },
                                  {
                                    label: '归档删除',
                                    tone: 'danger' as const,
                                    dividerBefore: true,
                                    onClick: () => handleDeleteNovel(novel),
                                  },
                                ]
                              : []),
                          ]}
                        />
                      </div>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </TableRegion>

      {/* Slide-Over Book Detail & Chapter Reader Drawer */}
      <CmsDialog
        open={Boolean(isDetailOpen && selectedNovel)}
        onClose={closeDetailDrawer}
        variant="drawer"
        size="xl"
        wrapTitle
        title={selectedNovel ? selectedNovel.title : undefined}
        subtitle={
          selectedNovel
            ? `${selectedNovel.author} · #${selectedNovel.id} · ${selectedNovel.word_count.toLocaleString()} 字`
            : undefined
        }
        bodyClassName="p-0 flex-1 overflow-hidden flex flex-col"
        headerExtra={
          selectedNovel && (
            <div className="flex items-center gap-2">
              {selectedDetailMeta && (
                <StatusDot tone={selectedDetailMeta.tone} label={selectedDetailMeta.label} className="hidden sm:inline-flex mr-1" />
              )}
              {canCreatePromo && (
                <button
                  type="button"
                  onClick={() => openPromoModalForNovel(selectedNovel)}
                  className="btn-secondary h-8 px-3 text-xs"
                >
                  <Link2 size={13} aria-hidden="true" />
                  <span>推广链接</span>
                </button>
              )}
              {canEditNovels && (
                <>
                  <button
                    type="button"
                    disabled={publishUpdatingId === selectedNovel.id}
                    onClick={() =>
                      handleTogglePublishStatus(
                        selectedNovel,
                        selectedNovel.publish_status === 'published' ? 'offline' : 'published',
                      )
                    }
                    className="btn-secondary h-8 px-3 text-xs"
                    style={{
                      color:
                        selectedNovel.publish_status === 'published' ? 'var(--cms-error)' : 'var(--cms-success)',
                    }}
                  >
                    {publishUpdatingId === selectedNovel.id
                      ? '更新中...'
                      : selectedNovel.publish_status === 'published'
                        ? '下架'
                        : '上架发布'}
                  </button>
                  <button
                    type="button"
                    onClick={() => openEditNovelModal(selectedNovel)}
                    className="btn-primary h-8 px-3 text-xs"
                  >
                    <Edit size={13} aria-hidden="true" />
                    <span>编辑</span>
                  </button>
                </>
              )}
            </div>
          )
        }
      >
        {selectedNovel && (
          <>
            {/* Compact Book Overview Top Section */}
            <section
              aria-label="书籍概览"
              className="px-6 py-4 border-b border-[var(--cms-border)] bg-[var(--cms-card)] flex items-start gap-4 shrink-0"
            >
              <img
                src={selectedNovel.cover_url || FALLBACK_COVER}
                alt={selectedNovel.title}
                onError={(e) => {
                  const img = e.currentTarget;
                  if (img.dataset.fb) return;
                  img.dataset.fb = '1';
                  img.src = FALLBACK_COVER;
                }}
                className="w-14 h-20 object-cover rounded-lg border border-[var(--cms-border)] shrink-0"
              />
              <div className="flex-1 min-w-0 space-y-2 text-xs">
                <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[var(--cms-muted-fg)]">
                    <span className="text-[var(--cms-fg)]">{selectedNovel.status === 'Completed' ? '已完结' : '连载中'}</span>
                    <span>·</span>
                    <span className="tabular-nums text-[var(--cms-fg)]">★ {selectedNovel.rating}</span>
                    <span>·</span>
                    <span className="truncate max-w-[260px]" title={selectedNovel.genres.join(', ')}>
                      {selectedNovel.genres.join(' · ') || '未分类'}
                    </span>
                  </div>

                  <div className="flex flex-wrap items-center gap-x-4 gap-y-1 tabular-nums text-[var(--cms-muted-fg)]">
                    <span>
                      字数 <span className="text-[var(--cms-fg)]">{selectedNovel.word_count.toLocaleString()}</span>
                    </span>
                    <span>
                      章节 <span className="text-[var(--cms-fg)]">{chapters.length} 章</span>
                    </span>
                    <span>
                      计费{' '}
                      <span className="text-[var(--cms-fg)]">
                        {selectedNovel.coin_cost_per_thousand != null
                          ? `${selectedNovel.coin_cost_per_thousand} 币/千字（独立）`
                          : `全局 ${selectedNovel.effective_coin_cost_per_thousand ?? globalCoinCost} 币/千字`}
                      </span>
                    </span>
                    <span>
                      起付 <span className="text-[var(--cms-fg)]">第 {selectedDetailStartPay} 章</span>
                    </span>
                    <span>
                      版本 <span className="text-[var(--cms-fg)]">v{selectedNovel.content_version}</span>
                    </span>
                  </div>
                </div>

                <p className="text-[var(--cms-muted-fg)] leading-relaxed line-clamp-2 m-0" title={selectedNovel.synopsis || ''}>
                  {selectedNovel.synopsis || '暂无书籍简介。'}
                </p>
              </div>
            </section>

            {/* Split Directory + Live Chapter Reader */}
            <div className="flex-1 min-h-0 max-md:overflow-y-auto md:overflow-hidden grid grid-cols-1 md:grid-cols-[270px_1fr]">
              {/* Left Column: Chapters Directory */}
              <div className="max-md:border-b md:border-b-0 md:border-r border-[var(--cms-border)] bg-[var(--cms-bg)] flex flex-col overflow-hidden">
                <div className="px-4 py-2.5 border-b border-[var(--cms-border)] flex items-center justify-between text-xs shrink-0">
                  <span className="font-medium text-[var(--cms-muted-fg)]">章节目录</span>
                  <span className="text-[var(--cms-muted-fg)] tabular-nums">共 {chapters.length} 章</span>
                </div>

                <div className="flex-1 overflow-y-auto p-2.5 space-y-1 text-xs">
                  {chaptersLoading ? (
                    <div className="space-y-2 p-2">
                      {[1, 2, 3, 4, 5].map((i) => (
                        <div key={i} className="skeleton-bar h-11 rounded-xl" />
                      ))}
                    </div>
                  ) : chapters.length === 0 ? (
                    <div className="py-12 px-4 text-center space-y-3 text-[var(--cms-muted-fg)]">
                      <div>暂无章节内容</div>
                      {canEditNovels && (
                        <button
                          type="button"
                          onClick={() => openEditNovelModal(selectedNovel)}
                          className="btn-secondary h-8 px-3 text-xs"
                        >
                          前往编辑导入手稿
                        </button>
                      )}
                    </div>
                  ) : (
                    paginatedChapters.map((ch) => {
                      const active = previewTargetMeta?.index === ch.chapter_index;
                      const showPaywallDivider = ch.chapter_index + 1 === selectedDetailStartPay;
                      return (
                        <div key={ch.id}>
                          {showPaywallDivider && (
                            <div className="my-2 px-2.5 py-1 rounded-lg bg-[color-mix(in_srgb,var(--cms-warning)_12%,transparent)] text-[var(--cms-warning)] font-medium text-center">
                              第 {selectedDetailStartPay} 章起进入付费
                            </div>
                          )}
                          <button
                            type="button"
                            onClick={() => void loadChapterPreview(selectedNovel.id, ch)}
                            className={`focus-ring w-full text-left px-3 py-2.5 rounded-xl cursor-pointer transition-colors border ${
                              active
                                ? 'bg-[var(--cms-card)] border-[var(--cms-border)] shadow-xs'
                                : 'border-transparent hover:bg-[var(--cms-card)]'
                            }`}
                          >
                            <div className="flex items-center justify-between gap-2">
                              <span
                                className={`font-medium truncate ${
                                  active ? 'text-[var(--cms-primary)]' : 'text-[var(--cms-fg)]'
                                }`}
                              >
                                第 {ch.chapter_index + 1} 章 · {ch.title}
                              </span>
                              <span
                                className={`shrink-0 tabular-nums ${
                                  ch.is_paid ? 'text-[var(--cms-warning)]' : 'text-[var(--cms-success)]'
                                }`}
                              >
                                {ch.is_paid ? `${ch.price} 币` : '免费'}
                              </span>
                            </div>
                            <div className="mt-0.5 text-[var(--cms-muted-fg)] tabular-nums">
                              {ch.word_count === 0 ? (
                                <span className="text-[var(--cms-error)] font-medium">正文为空 (0 字)</span>
                              ) : (
                                `${ch.word_count.toLocaleString()} 字`
                              )}
                            </div>
                          </button>
                        </div>
                      );
                    })
                  )}
                </div>

                {chapters.length > 10 && (
                  <Pagination
                    insideCard
                    page={chapterPage}
                    pageSize={chapterPageSize}
                    pageSizeOptions={[10, 15, 30, 50]}
                    total={chapters.length}
                    onPageChange={setChapterPage}
                    onPageSizeChange={(nextSize) => {
                      setChapterPageSize(nextSize);
                      setChapterPage(1);
                    }}
                    itemLabel="章"
                  />
                )}
              </div>

              {/* Right Column: Live Chapter Reader Preview */}
              <div className="flex flex-col overflow-hidden bg-[var(--cms-card)]">
                <div className="px-5 py-2.5 border-b border-[var(--cms-border)] flex items-center justify-between gap-3 text-xs shrink-0">
                  <div className="min-w-0 truncate font-medium text-[var(--cms-fg)]">
                    {previewTargetMeta
                      ? `第 ${previewTargetMeta.index + 1} 章 · ${previewTargetMeta.title}`
                      : chapters.length === 0
                        ? '章节阅读预览'
                        : '请选择左侧章节查看正文'}
                  </div>
                  <div className="flex items-center rounded-lg bg-[var(--cms-content)] p-0.5 shrink-0">
                    <button
                      type="button"
                      onClick={() => setReaderWidthMode('desktop')}
                      className={`px-2.5 py-1 rounded-md cursor-pointer transition-colors ${
                        readerWidthMode === 'desktop'
                          ? 'bg-[var(--cms-card)] text-[var(--cms-fg)] font-medium'
                          : 'text-[var(--cms-muted-fg)]'
                      }`}
                    >
                      宽屏
                    </button>
                    <button
                      type="button"
                      onClick={() => setReaderWidthMode('mobile')}
                      className={`px-2.5 py-1 rounded-md cursor-pointer transition-colors ${
                        readerWidthMode === 'mobile'
                          ? 'bg-[var(--cms-card)] text-[var(--cms-fg)] font-medium'
                          : 'text-[var(--cms-muted-fg)]'
                      }`}
                    >
                      手机端
                    </button>
                  </div>
                </div>

                <div className="flex-1 overflow-y-auto p-6 bg-[var(--cms-bg)] flex justify-center">
                  <article
                    className="w-full bg-[var(--cms-card)] border border-[var(--cms-border)] rounded-2xl p-7 transition-all h-fit"
                    style={{ maxWidth: readerWidthMode === 'mobile' ? '375px' : '740px' }}
                  >
                    {chapters.length === 0 ? (
                      <div className="py-12 text-center text-xs text-[var(--cms-muted-fg)]">
                        暂未导入章节，请点击右上角「编辑」导入手稿内容。
                      </div>
                    ) : previewLoading ? (
                      <div className="space-y-3 py-4">
                        <div className="skeleton-bar h-5 w-1/2" />
                        <div className="skeleton-bar h-4 w-full mt-4" />
                        <div className="skeleton-bar h-4 w-11/12" />
                        <div className="skeleton-bar h-4 w-4/5" />
                      </div>
                    ) : previewError ? (
                      <div
                        role="status"
                        className="p-4 rounded-xl border text-xs leading-relaxed flex items-start gap-2.5"
                        style={{
                          backgroundColor: 'color-mix(in srgb, var(--cms-error) 10%, transparent)',
                          borderColor: 'color-mix(in srgb, var(--cms-error) 25%, transparent)',
                          color: 'var(--cms-error)',
                        }}
                      >
                        <AlertCircle size={15} className="shrink-0 mt-0.5" aria-hidden="true" />
                        <span>加载章节预览失败：{previewError}</span>
                      </div>
                    ) : previewChapter && !previewChapter.content ? (
                      <div className="p-4 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-content)] text-xs text-[var(--cms-warning)] leading-relaxed">
                        服务端存储的该章节正文为空（0 字），请点击右上角「编辑」重导手稿后重试。
                      </div>
                    ) : previewChapter ? (
                      <div className="space-y-4">
                        <div className="pb-3 border-b border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-2">
                          <h3 className="text-base font-semibold text-[var(--cms-fg)] m-0">
                            第 {previewChapter.chapter_index + 1} 章 · {previewChapter.title}
                          </h3>
                          <span className="text-xs text-[var(--cms-muted-fg)] tabular-nums">
                            {previewChapter.word_count.toLocaleString()} 字 ·{' '}
                            {previewChapter.is_paid ? `${previewChapter.price} 金币` : '免费'}
                          </span>
                        </div>
                        <div className="text-sm leading-8 text-[var(--cms-fg)] whitespace-pre-wrap break-words">
                          {previewChapter.content}
                        </div>
                      </div>
                    ) : null}
                  </article>
                </div>
              </div>
            </div>
          </>
        )}
      </CmsDialog>

      {/* Global Pricing / Start Chapter Dialog (Centered Modal) */}
      {canEditGlobalSettings && (
        <CmsDialog
          open={Boolean(activeGlobalPanel)}
          onClose={() => setActiveGlobalPanel(null)}
          variant="modal"
          title={activeGlobalPanel === 'coin' ? '全局千字金币设置' : '全局起付章节设置'}
          size="md"
          closedBy={savingGlobalSetting ? 'none' : 'any'}
          closeDisabled={savingGlobalSetting}
          footer={
            <>
              <button
                type="button"
                className="btn-secondary"
                disabled={savingGlobalSetting}
                onClick={() => setActiveGlobalPanel(null)}
              >
                取消
              </button>
              <button type="submit" form="global-config-form" className="btn-primary" disabled={savingGlobalSetting}>
                {savingGlobalSetting
                  ? '保存中...'
                  : activeGlobalPanel === 'coin'
                    ? '保存全局金币设置'
                    : '保存全局章节设置'}
              </button>
            </>
          }
        >
          {activeGlobalPanel && (
            <form
              id="global-config-form"
              onSubmit={activeGlobalPanel === 'coin' ? handleSaveGlobalCoinConfig : handleSaveGlobalChapterConfig}
              className="space-y-4 text-xs"
            >
              <label
                htmlFor={activeGlobalPanel === 'coin' ? globalCoinInputId : globalChapterInputId}
                className="block space-y-1.5"
              >
                <span className="block font-medium text-[var(--cms-muted-fg)]">
                  {activeGlobalPanel === 'coin' ? '全局千字收费价格 (金币)' : '全局默认起始付费章节 (从第几章起)'}
                </span>
                <input
                  id={activeGlobalPanel === 'coin' ? globalCoinInputId : globalChapterInputId}
                  type="number"
                  autoFocus
                  className="input-field tabular-nums"
                  min={activeGlobalPanel === 'coin' ? 0 : 1}
                  value={activeGlobalPanel === 'coin' ? globalCoinDraft : globalStartPayDraft}
                  onChange={(e) => {
                    const val = e.target.value === '' ? '' : Number(e.target.value);
                    if (activeGlobalPanel === 'coin') setGlobalCoinDraft(val);
                    else setGlobalStartPayDraft(val);
                  }}
                  required
                />
              </label>

              <label
                htmlFor={activeGlobalPanel === 'coin' ? globalCoinApplyAllId : globalChapterApplyAllId}
                className="flex items-start gap-2.5 p-3.5 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] cursor-pointer leading-relaxed text-[var(--cms-fg)]"
              >
                <input
                  type="checkbox"
                  id={activeGlobalPanel === 'coin' ? globalCoinApplyAllId : globalChapterApplyAllId}
                  checked={activeGlobalPanel === 'coin' ? applyCoinCostToAll : applyStartPayToAll}
                  disabled={activeGlobalPanel === 'coin' && resetNovelCoinCost}
                  onChange={(e) =>
                    activeGlobalPanel === 'coin'
                      ? setApplyCoinCostToAll(e.target.checked)
                      : setApplyStartPayToAll(e.target.checked)
                  }
                  className="w-4 h-4 mt-0.5 shrink-0 cursor-pointer"
                />
                <span>
                  {activeGlobalPanel === 'coin'
                    ? `同步覆盖所有已有书籍单价（勾选后将重置包括独立定价书籍在内的全部未归档书籍为千字 ${globalCoinDraft === '' ? 0 : globalCoinDraft} 币；不勾选则仅刷新继承全局单价的书籍，不影响任何书的起付章节）`
                    : `同步覆盖所有已有书籍起付章节（勾选后将把全部未归档书籍统一更新为第 ${globalStartPayDraft === '' ? 1 : globalStartPayDraft} 章起付费；不勾选则仅作为后续新建书籍默认值）`}
                </span>
              </label>

              {activeGlobalPanel === 'coin' && (
                <label className="flex items-center gap-2.5 p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] cursor-pointer text-[var(--cms-fg)]">
                  <input
                    type="checkbox"
                    checked={resetNovelCoinCost}
                    disabled={applyCoinCostToAll}
                    onChange={(event) => setResetNovelCoinCost(event.target.checked)}
                    className="w-4 h-4 cursor-pointer"
                  />
                  <span>将全部在库书籍改为继承全局单价</span>
                </label>
              )}
            </form>
          )}
        </CmsDialog>
      )}

      {/* Create Novel Drawer */}
      <NovelFormModal
        isOpen={isCreating}
        isEditing={false}
        isSubmitting={isSubmittingNovel}
        hasChapters={false}
        globalCoinCost={globalCoinCost}
        formData={newNovel}
        setFormData={(data) => setNewNovel(data)}
        onSubmit={handleCreateNovel}
        onCancel={() => {
          setIsCreating(false);
          resetCreateModalState();
        }}
        chapterImportType={createImportType}
        onChapterImportTypeChange={setCreateImportType}
        chapterFile={createImportFile}
        onChapterFileChange={setCreateImportFile}
        chapterRegex={createRegex}
        onChapterRegexChange={setCreateRegex}
        createdDraftId={createdDraftId}
        submitError={createModalError}
      />

      {/* Edit Novel Drawer (Top-layer stacks naturally above Book Detail Drawer) */}
      <NovelFormModal
        isOpen={isEditing}
        isEditing={true}
        isSubmitting={isSubmittingNovel}
        hasChapters={chapters.length > 0 || (chaptersLoading && (selectedNovel?.word_count ?? 0) > 0)}
        globalCoinCost={globalCoinCost}
        formData={editNovelForm}
        setFormData={(data) => setEditNovelForm(data)}
        onSubmit={handleUpdateNovel}
        onCancel={() => {
          setIsEditing(false);
          setEditImportFile(null);
          setEditModalError('');
          if (!isDetailOpen) {
            setSelectedNovel(null);
          }
        }}
        chapterImportType={editImportType}
        onChapterImportTypeChange={setEditImportType}
        chapterFile={editImportFile}
        onChapterFileChange={setEditImportFile}
        chapterRegex={editRegex}
        onChapterRegexChange={setEditRegex}
        chapterMapping={editChapterMapping}
        onChapterMappingChange={setEditChapterMapping}
        chapterIdentities={editChapterIdentities}
        onLoadChapterIdentities={selectedNovel ? () => void loadChapterIdentities(selectedNovel.id) : undefined}
        submitError={editModalError}
      />

      {/* Promotion Modal */}
      <PromotionLinkModal
        isOpen={isPromoModalOpen}
        onClose={() => !promoSubmitting && setIsPromoModalOpen(false)}
        submitting={promoSubmitting}
        promotionForm={promotionForm}
        setPromotionForm={setPromotionForm}
        pixels={pixels}
        templates={templates}
        domains={domains}
        onGenerateLink={handleGeneratePromotionLink}
        generatedLink={generatedLink}
        onCopyLink={handleCopyLink}
        ruleChoice={promoRuleChoice}
        ruleSummary={promoLinkPolicy?.own_version ? `专属规则 · v${policyVersion(promoLinkPolicy)?.number}`
          : promoLinkPolicy ? defaultPolicySummary(promoLinkPolicy)
            : promoDefaultPolicy ? defaultPolicySummary(promoDefaultPolicy) : '继承接入默认'}
        onRuleChoice={setPromoRuleChoice}
        canConfigureRule={canWriteConversions && Boolean(promoIntegration)}
        onConfigureRule={canWriteConversions && promoIntegration && promoLinkId ? () => setIsPromoRuleOpen(true) : undefined}
      />
      {isPromoRuleOpen && promoIntegration && (
        <IntegrationConfigDrawer
          open
          editingIntegration={promoIntegration}
          initialTab="policy"
          initialPolicyPromotion={promoLinkId}
          destinations={[]}
          onClose={() => setIsPromoRuleOpen(false)}
          onSuccess={() => {}}
          onPolicyPublished={(policy) => {
            setPromoLinkPolicy(policy);
            window.showToast?.('推广链接回传规则已发布', 'success');
          }}
        />
      )}
    </div>
  );
}

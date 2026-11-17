import { useEffect, useId, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { apiRequest, domainApi } from '../utils/api';
import type { SystemDomain } from '../utils/api';
import { Book, BookOpen, Plus, ArrowLeft, Upload, Edit, Trash, CheckCircle, Search, Settings, Link2, X } from 'lucide-react';
import { ChapterUploadFields, DEFAULT_CHAPTER_DELIMITER_REGEX } from '../components/novels/ChapterUploadFields';
import { MAX_GENRES_LENGTH, NovelFormModal, type NovelFormData } from '../components/novels/NovelFormModal';
import { PromotionLinkModal } from '../components/novels/PromotionLinkModal';

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

const EMPTY_NOVEL_FILTER: NovelListFilterDraft = {
  titleOrId: '',
  author: '',
  publishStatus: '',
  createdStart: '',
  createdEnd: '',
};

const DEFAULT_NEW_NOVEL_GENRES = 'Contemporary, Romance, Cheating';

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

export default function NovelsTab() {
  const currentAdminRole = (() => {
    try {
      const saved = localStorage.getItem('cms_admin');
      return saved ? (JSON.parse(saved).role || '') : '';
    } catch {
      return '';
    }
  })();
  const canEditNovels = ['SuperAdmin', 'Admin', 'Editor'].includes(currentAdminRole);
  const canEditGlobalSettings = ['SuperAdmin', 'Admin'].includes(currentAdminRole);
  const canCreatePromo = ['SuperAdmin', 'Admin', 'Editor', 'MediaBuyer'].includes(currentAdminRole);

  const [novels, setNovels] = useState<Novel[]>([]);
  const [totalNovels, setTotalNovels] = useState(0);
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [selectedNovel, setSelectedNovel] = useState<Novel | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [publishUpdatingId, setPublishUpdatingId] = useState<number | null>(null);
  const [selectedNovelIds, setSelectedNovelIds] = useState<number[]>([]);
  const [batchPublishUpdating, setBatchPublishUpdating] = useState(false);

  // Novels server pagination & filter states (single unified query to prevent stale/duplicate fetches)
  const [novelQuery, setNovelQuery] = useState<NovelListFilterDraft & { page: number }>({
    page: 1,
    ...EMPTY_NOVEL_FILTER,
  });
  const [filterDraft, setFilterDraft] = useState<NovelListFilterDraft>(EMPTY_NOVEL_FILTER);
  const [dateFilterError, setDateFilterError] = useState('');
  const pageSize = 10;

  // Chapters pagination states
  const [chapterPage, setChapterPage] = useState(1);
  const chapterPageSize = 10;

  // Preview states
  const [previewChapter, setPreviewChapter] = useState<Chapter | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);

  // Form states
  const [isCreating, setIsCreating] = useState(false);
  const [isSubmittingNovel, setIsSubmittingNovel] = useState(false);
  const [newNovel, setNewNovel] = useState<NovelFormData>({
    title: '', author: '', cover_url: '', cover_preview_url: '',
    status: 'Ongoing', publish_status: 'draft', rating: 4.5, synopsis: '', genres: DEFAULT_NEW_NOVEL_GENRES,
    coin_cost_per_thousand: '', start_pay_chapter_index: 3
  });
  const [createImportType, setCreateImportType] = useState<'single_txt' | 'zip'>('single_txt');
  const [createImportFile, setCreateImportFile] = useState<File | null>(null);
  const [createRegex, setCreateRegex] = useState(DEFAULT_CHAPTER_DELIMITER_REGEX);
  const [createModalError, setCreateModalError] = useState('');
  const [createdDraftId, setCreatedDraftId] = useState<number | null>(null);

  const [isEditing, setIsEditing] = useState(false);
  const [editNovelForm, setEditNovelForm] = useState<NovelFormData>({
    title: '', author: '', cover_url: '', cover_preview_url: '', status: 'Ongoing', publish_status: 'draft', rating: 4.5, synopsis: '', genres: '',
    coin_cost_per_thousand: '', start_pay_chapter_index: 3
  });

  const [importFile, setImportFile] = useState<File | null>(null);
  const [importType, setImportType] = useState<'single_txt' | 'zip'>('single_txt');
  const [regex, setRegex] = useState(DEFAULT_CHAPTER_DELIMITER_REGEX);
  const [importMessage, setImportMessage] = useState('');
  const [importError, setImportError] = useState('');
  const [chapterMapping, setChapterMapping] = useState('');
  const [chapterIdentities, setChapterIdentities] = useState<ChapterIdentity[]>([]);
  const [importLoading, setImportLoading] = useState(false);
  const titleOrIdInputId = useId();
  const authorInputId = useId();
  const statusSelectId = useId();
  const createdStartInputId = useId();
  const createdEndInputId = useId();
  const dateFilterErrorId = useId();
  const createdStartRef = useRef<HTMLInputElement>(null);
  const selectAllCheckboxRef = useRef<HTMLInputElement>(null);
  const novelsAbortRef = useRef<AbortController | null>(null);
  const novelsFetchSeqRef = useRef(0);

  const importFileInputRef = useRef<HTMLInputElement>(null);

  // Promotion Link Builder States
  const [isPromoModalOpen, setIsPromoModalOpen] = useState(false);
  interface FBPixel {
    id: number;
    name: string;
    pixel_id: string;
    access_token: string;
  }
  interface RechargeTemplate {
    id: number;
    name: string;
    is_default: boolean;
  }
  const [pixels, setPixels] = useState<FBPixel[]>([]);
  const [templates, setTemplates] = useState<RechargeTemplate[]>([]);
  const [domains, setDomains] = useState<SystemDomain[]>([]);
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
    startPayChapterIndex: ''
  });
  const [generatedLink, setGeneratedLink] = useState('');

  // Global config states (separated into two independent settings)
  const [globalCoinCost, setGlobalCoinCost] = useState(500);
  const [globalStartPayChapter, setGlobalStartPayChapter] = useState(3);
  const [applyCoinCostToAll, setApplyCoinCostToAll] = useState(false);
  const [resetNovelCoinCost, setResetNovelCoinCost] = useState(false);
  const [applyStartPayToAll, setApplyStartPayToAll] = useState(false);
  const [activeGlobalPanel, setActiveGlobalPanel] = useState<'coin' | 'chapter' | null>(null);
  const [savingGlobalSetting, setSavingGlobalSetting] = useState(false);
  const globalCoinInputId = useId();
  const globalCoinApplyAllId = useId();
  const globalChapterInputId = useId();
  const globalChapterApplyAllId = useId();

  const currentPage = novelQuery.page;
  const totalPages = Math.max(1, Math.ceil(totalNovels / pageSize));
  const startIndex = (currentPage - 1) * pageSize;

  const totalChapterPages = Math.ceil(chapters.length / chapterPageSize);
  const startChIndex = (chapterPage - 1) * chapterPageSize;
  const paginatedChapters = chapters.slice(startChIndex, startChIndex + chapterPageSize);

  const [colWidths, setColWidths] = useState<Record<string, number>>({
    select: 44,
    id: 78,
    cover: 64,
    title: 185,
    author: 95,
    genres: 105,
    publish_status: 84,
    status: 80,
    word_count: 90,
    coin_cost_per_thousand: 112,
    rating: 64,
    created_at: 146,
    updated_at: 146,
    actions: 158,
  });

  const columns = [
    ...(canEditNovels ? [{ label: '', key: 'select', resizable: false }] : []),
    { label: '书籍 ID', key: 'id' },
    { label: '封面', key: 'cover' },
    { label: '书名', key: 'title' },
    { label: '作者', key: 'author' },
    { label: '分类标签', key: 'genres' },
    { label: '上架状态', key: 'publish_status' },
    { label: '连载状态', key: 'status' },
    { label: '总字数', key: 'word_count' },
    { label: '千字收费', key: 'coin_cost_per_thousand' },
    { label: '评分', key: 'rating' },
    { label: '创建时间', key: 'created_at' },
    { label: '更新时间', key: 'updated_at' },
    { label: '操作', key: 'actions', align: 'right' }
  ];

  const getPublishStatusMeta = (publishStatus?: string) => {
    switch (publishStatus) {
      case 'published':
        return { label: '已上架', badgeClass: 'badge-green' };
      case 'offline':
        return { label: '已下架', badgeClass: 'badge-red' };
      case 'draft':
      default:
        return { label: '草稿', badgeClass: 'badge-orange' };
    }
  };

  const handleMouseDown = (colKey: string, e: React.MouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    const startWidth = colWidths[colKey] || 100;

    const handleMouseMove = (moveEvent: MouseEvent) => {
      const deltaX = moveEvent.clientX - startX;
      setColWidths((prev) => ({
        ...prev,
        [colKey]: Math.max(50, startWidth + deltaX),
      }));
    };

    const handleMouseUp = () => {
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', handleMouseUp);
    };

    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', handleMouseUp);
  };

  const fetchGlobalConfig = async () => {
    try {
      const data = await apiRequest('GET', '/settings');
      if (data.global_coin_cost_per_thousand) {
        setGlobalCoinCost(Number(data.global_coin_cost_per_thousand));
      }
      if (data.global_start_pay_chapter_index) {
        setGlobalStartPayChapter(Number(data.global_start_pay_chapter_index));
      }
    } catch (err) {
      console.error("加载全局配置失败:", err);
    }
  };

  const fetchPixels = async () => {
    try {
      const data = await apiRequest('GET', '/fb-pixels');
      setPixels(data || []);
    } catch (err) {
      console.error("加载像素列表失败:", err);
    }
  };

  const fetchTemplates = async () => {
    try {
      const data = await apiRequest('GET', '/recharge-templates');
      setTemplates(data || []);
    } catch (err) {
      console.error("加载充值模板列表失败:", err);
    }
  };

  const fetchDomains = async () => {
    try {
      const data = await domainApi.getDomains();
      setDomains(data || []);
    } catch (err) {
      console.error("加载域名列表失败:", err);
    }
  };

  useEffect(() => {
    fetchGlobalConfig();
    fetchPixels();
    fetchTemplates();
    fetchDomains();
  }, []);

  const fetchNovels = async (targetQuery = novelQuery) => {
    novelsAbortRef.current?.abort();
    const controller = new AbortController();
    novelsAbortRef.current = controller;
    const seq = ++novelsFetchSeqRef.current;

    setLoading(true);
    try {
      const query = new URLSearchParams({
        page: String(targetQuery.page),
        page_size: String(pageSize),
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
    } catch (e: any) {
      if (e?.name === 'AbortError' || controller.signal.aborted) return;
      console.error(e);
      setHasLoadedOnce(true);
    } finally {
      if (seq === novelsFetchSeqRef.current) {
        setLoading(false);
      }
    }
  };

  useEffect(() => {
    setSelectedNovelIds([]);
    void fetchNovels(novelQuery);
    return () => {
      novelsAbortRef.current?.abort();
    };
  }, [novelQuery]);

  const allSelectedOnPage = novels.length > 0 && novels.every((n) => selectedNovelIds.includes(n.id));
  const someSelectedOnPage = novels.some((n) => selectedNovelIds.includes(n.id));

  useEffect(() => {
    if (selectAllCheckboxRef.current) {
      selectAllCheckboxRef.current.indeterminate = someSelectedOnPage && !allSelectedOnPage;
    }
  }, [someSelectedOnPage, allSelectedOnPage]);

  const fetchChapters = async (novelID: number) => {
    try {
      const data = await apiRequest('GET', `/novels/${novelID}/chapters`);
      setChapters(data || []);
    } catch (e) {
      console.error(e);
    }
  };

  const resetDetailTransientState = () => {
    setIsEditing(false);
    setPreviewChapter(null);
    setPreviewLoading(false);
    setImportFile(null);
    setImportMessage('');
    setImportError('');
    if (importFileInputRef.current) {
      importFileInputRef.current.value = '';
    }
  };

  const handleNovelClick = (novel: Novel) => {
    resetDetailTransientState();
    setSelectedNovel(novel);
    setChapterMapping('');
    setChapterIdentities([]);
    setChapters([]);
    setChapterPage(1); // Reset chapter page on select
    fetchChapters(novel.id);
  };

  const handlePreviewChapter = async (index: number) => {
    if (!selectedNovel) return;
    setPreviewLoading(true);
    try {
      const data = await apiRequest('GET', `/novels/${selectedNovel.id}/chapters/${index}`);
      setPreviewChapter(data);
    } catch (e) {
      window.showToast?.('加载章节预览失败: ' + e, 'error');
    } finally {
      setPreviewLoading(false);
    }
  };

  const resetCreateModalState = () => {
    setNewNovel({
      title: '', author: '', cover_url: '', cover_preview_url: '',
      status: 'Ongoing', publish_status: 'draft', rating: 4.5, synopsis: '', genres: DEFAULT_NEW_NOVEL_GENRES,
      coin_cost_per_thousand: '', start_pay_chapter_index: globalStartPayChapter || 3,
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
        if (targetNovelId) {
          setCreatedDraftId(targetNovelId);
        }
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
          'success'
        );
      } else {
        window.showToast?.('新建书籍已保存为草稿！', 'success');
      }

      setIsCreating(false);
      resetCreateModalState();
      setSelectedNovel(null);
      refreshFirstPageList();
    } catch (err: any) {
      const msg = err?.message || '新建书籍失败';
      if (targetNovelId) {
        refreshFirstPageList();
        setCreateModalError(`书籍档案已保存为草稿（ID: ${targetNovelId}），但章节导入失败：${msg}。可调整文件或正则后重试导入，或点击右上角 × 关闭返回列表。`);
      } else {
        setCreateModalError(msg);
      }
      window.showToast?.(msg, 'error');
    } finally {
      setIsSubmittingNovel(false);
    }
  };

  const saveGlobalSetting = async (payload: Record<string, string>, successMessage: string) => {
    if (savingGlobalSetting) return;
    setSavingGlobalSetting(true);
    try {
      await apiRequest('POST', '/settings', payload);
      window.showToast?.(successMessage, 'success');
      setActiveGlobalPanel(null);
      await fetchNovels(novelQuery);
      if (selectedNovel) {
        await fetchChapters(selectedNovel.id);
        const updated = await apiRequest('GET', `/novels/${selectedNovel.id}`);
        if (updated) setSelectedNovel(updated);
      }
    } catch (err: any) {
      window.showToast?.(err.message || '保存全局配置失败', 'error');
    } finally {
      setSavingGlobalSetting(false);
    }
  };

  const handleSaveGlobalCoinConfig = async (e: React.FormEvent) => {
    e.preventDefault();
    const costNum = Number(globalCoinCost);
    if (String(globalCoinCost).trim() === '' || !Number.isInteger(costNum) || costNum < 0) {
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

    if (resetNovelCoinCost && window.showConfirm) {
      window.showConfirm('将清除全部未归档书籍的独立单价，并统一继承全局单价，确认继续？',
        () => { void saveGlobalSetting(payload, msg); });
      return;
    }

    if (applyCoinCostToAll && window.showConfirm) {
      window.showConfirm(
        `开启「同步覆盖所有已有书籍单价」将把全部未归档书籍（含独立定价书籍）统一重置为千字 ${costNum} 币，不会改动各书起始付费章节，确定继续吗？`,
        () => { void saveGlobalSetting(payload, msg); }
      );
      return;
    }
    await saveGlobalSetting(payload, msg);
  };

  const handleSaveGlobalChapterConfig = async (e: React.FormEvent) => {
    e.preventDefault();
    const startNum = Number(globalStartPayChapter);
    if (String(globalStartPayChapter).trim() === '' || !Number.isInteger(startNum) || startNum < 1) {
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
        () => { void saveGlobalSetting(payload, msg); }
      );
      return;
    }
    await saveGlobalSetting(payload, msg);
  };

  const handleUpdateNovel = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedNovel || isSubmittingNovel) return;
    setIsSubmittingNovel(true);
    try {
      const payload = buildNovelPayload(editNovelForm, editNovelForm.publish_status || selectedNovel.publish_status || 'draft');
      const updated = await apiRequest('PUT', `/novels/${selectedNovel.id}`, payload);
      setSelectedNovel(updated);
      setIsEditing(false);
      window.showToast?.('书籍信息更新成功', 'success');
      void fetchNovels(novelQuery);
      fetchChapters(selectedNovel.id);
    } catch (err: any) {
      window.showToast?.(err.message || '更新书籍失败', 'error');
    } finally {
      setIsSubmittingNovel(false);
    }
  };

  const handleTogglePublishStatus = (novel: Novel, targetStatus: 'published' | 'offline', e?: React.MouseEvent) => {
    e?.stopPropagation();
    const runUpdate = async () => {
      setPublishUpdatingId(novel.id);
      try {
        const updated = await apiRequest('PATCH', `/novels/${novel.id}/publish-status`, {
          publish_status: targetStatus,
        });
        if (selectedNovel?.id === novel.id) {
          setSelectedNovel(updated);
        }
        window.showToast?.(targetStatus === 'published' ? '书籍已成功上架到书城！' : '书籍已下架，读者端将停止展示与访问。', 'success');
        void fetchNovels(novelQuery);
      } catch (err: any) {
        window.showToast?.(err.message || '更新上架状态失败', 'error');
      } finally {
        setPublishUpdatingId(null);
      }
    };

    if (targetStatus === 'offline' && window.showConfirm) {
      window.showConfirm(
        `确定将《${novel.title}》下架吗？下架后书城、搜索、书架与推广链接均会拦截访问。`,
        () => { void runUpdate(); }
      );
      return;
    }
    void runUpdate();
  };

  const handleToggleSelectNovel = (id: number) => {
    setSelectedNovelIds((prev) =>
      prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]
    );
  };

  const handleToggleSelectAllOnPage = (checked: boolean) => {
    if (!checked) {
      setSelectedNovelIds([]);
      return;
    }
    setSelectedNovelIds(novels.map((n) => n.id));
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
            'info'
          );
        } else {
          window.showToast?.(
            targetStatus === 'published'
              ? `已成功批量上架 ${updatedCount} 本书籍！`
              : `已成功批量下架 ${updatedCount} 本书籍！`,
            'success'
          );
        }
        setSelectedNovelIds([]);
        void fetchNovels(novelQuery);
      } catch (err: any) {
        window.showToast?.(err.message || '批量更新上架状态失败', 'error');
      } finally {
        setBatchPublishUpdating(false);
      }
    };

    if (targetStatus === 'offline' && window.showConfirm) {
      window.showConfirm(
        `确定将选中的 ${count} 本书籍全部下架吗？下架后书城、搜索、书架与推广链接均会拦截访问。`,
        () => { void runBatch(); }
      );
      return;
    }
    void runBatch();
  };

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (filterDraft.createdStart && filterDraft.createdEnd && filterDraft.createdStart > filterDraft.createdEnd) {
      setDateFilterError('开始日期不能晚于结束日期');
      createdStartRef.current?.focus();
      return;
    }
    setDateFilterError('');
    setNovelQuery({
      page: 1,
      titleOrId: filterDraft.titleOrId.trim(),
      author: filterDraft.author.trim(),
      publishStatus: filterDraft.publishStatus,
      createdStart: filterDraft.createdStart,
      createdEnd: filterDraft.createdEnd,
    });
  };

  const handleResetFilters = () => {
    setDateFilterError('');
    setFilterDraft(EMPTY_NOVEL_FILTER);
    setNovelQuery({
      page: 1,
      ...EMPTY_NOVEL_FILTER,
    });
  };

  const handleDeleteNovel = async (id: number, e: React.MouseEvent) => {
    e.stopPropagation();
    window.showConfirm?.('您确定要删除这本小说吗？书籍将被归档隐藏，清空 OSS 章节正文并停止全部读者端访问，此操作不可撤销！', async () => {
      try {
        await apiRequest('DELETE', `/novels/${id}`);
        window.showToast?.('书籍已删除归档', 'success');
        const nextPage = novels.length === 1 && novelQuery.page > 1 ? novelQuery.page - 1 : novelQuery.page;
        if (nextPage !== novelQuery.page) {
          setNovelQuery(prev => ({ ...prev, page: nextPage }));
        } else {
          void fetchNovels({ ...novelQuery, page: nextPage });
        }
      } catch (err: any) {
        window.showToast?.(err.message, 'error');
      }
    });
  };

  const executeBulkImport = async () => {
    if (!importFile || !selectedNovel) return;

    setImportLoading(true);
    setImportMessage('');
    setImportError('');

    try {
      const formData = new FormData();
      formData.append('file', importFile);
      formData.append('import_type', importType);
      formData.append('delimiter_regex', regex);
      if (chapterMapping.trim()) {
        const mapping = JSON.parse(chapterMapping);
        formData.append('chapter_mapping', JSON.stringify({ ...mapping, version: selectedNovel.content_version }));
      }


      const data = await apiRequest('POST', `/novels/${selectedNovel.id}/chapters/bulk-import`, formData, true);
      const isPublished = selectedNovel.publish_status === 'published';
      setImportMessage(
        isPublished
          ? `导入成功！共提取并导入了 ${data.chapters_count} 个章节（总字数 ${data.total_word_count} 字）。`
          : `导入成功！共提取并导入了 ${data.chapters_count} 个章节（总字数 ${data.total_word_count} 字）。当前书籍处于未上架状态，核对无误后请点击右上角「上架发布」。`
      );
      setImportFile(null);
      setChapterMapping('');
      if (importFileInputRef.current) {
        importFileInputRef.current.value = '';
      }
      fetchChapters(selectedNovel.id);

      const updatedNovel = await apiRequest('GET', `/novels/${selectedNovel.id}`);
      setSelectedNovel(updatedNovel);
      void fetchNovels(novelQuery);
    } catch (err: any) {
      setImportError(err.message || '导入文件解析失败，请检查文件编码（UTF-8）或压缩包格式。');
    } finally {
      setImportLoading(false);
    }
  };

  const loadChapterIdentities = async (novelId: number) => {
    try {
      const identities = await apiRequest('GET', `/novels/${novelId}/chapters/identities`);
      setChapterIdentities(identities ?? []);
    } catch {
      setImportError('无法加载章节标识，请刷新后重试。');
    }
  };

  const handleBulkImport = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!importFile || !selectedNovel) return;
    if (chapters.length > 0 && window.showConfirm) {
      window.showConfirm(
        '导入将替换目录并重新编号。读者购买权益随章节 ID 保留。确认发布本次导入？',
        () => { void executeBulkImport(); }
      );
      return;
    }
    await executeBulkImport();
  };

  const handleGeneratePromotionLink = async () => {
    if (!promotionForm.fbPixelId) {
      window.showToast?.('推广链接必须显式绑定 Facebook 像素！请先选择像素。', 'error');
      return;
    }

    const rawPromoCost = String(promotionForm.coinCostPerThousand ?? '').trim();
    if (rawPromoCost !== '') {
      const parsedCost = Number(rawPromoCost);
      if (!Number.isInteger(parsedCost) || parsedCost < 0) {
        window.showToast?.('推广覆写单价（千字金币）必须为大于等于 0 的整数。', 'error');
        return;
      }
    }

    const rawPromoStartPay = String(promotionForm.startPayChapterIndex ?? '').trim();
    if (rawPromoStartPay !== '') {
      const parsedStartPay = Number(rawPromoStartPay);
      if (!Number.isInteger(parsedStartPay) || parsedStartPay < 1) {
        window.showToast?.('推广覆写起始付费章节必须为大于等于 1 的整数。', 'error');
        return;
      }
    }

    // 1. 匹配选中的可用域名或默认可用主域名
    const activeDomains = (domains || []).filter((d) => d.status === 1);
    const targetDomainObj = activeDomains.find((d) => String(d.id) === promotionForm.domainId);
    const defaultDomainObj = activeDomains.find((d) => d.is_default) || activeDomains.find((d) => d.type === 'main') || activeDomains[0];
    const domainHost = targetDomainObj ? targetDomainObj.domain : (defaultDomainObj ? defaultDomainObj.domain : window.location.host);

    let baseUrl = domainHost.startsWith('http://') || domainHost.startsWith('https://')
      ? domainHost
      : `https://${domainHost}`;
    baseUrl = baseUrl.replace(/\/+$/, '');

    const params = new URLSearchParams();
    params.set('novel_id', String(promotionForm.novelId));
    if (promotionForm.chapterIndex) {
      params.set('chapter_index', promotionForm.chapterIndex);
    }
    if (promotionForm.utmSource) {
      params.set('utm_source', promotionForm.utmSource);
    }
    if (promotionForm.utmCampaign) {
      params.set('utm_campaign', promotionForm.utmCampaign);
    }

    const targetPixel = pixels.find((p) => p.id === parseInt(promotionForm.fbPixelId, 10));
    if (targetPixel) {
      params.set('pixel_id', targetPixel.pixel_id);
    }

    if (promotionForm.rechargeTemplateId) {
      params.set('template_id', promotionForm.rechargeTemplateId);
    }

    if (rawPromoCost !== '') {
      params.set('cost', rawPromoCost);
    }
    if (rawPromoStartPay !== '') {
      params.set('pay_ch', rawPromoStartPay);
    }

    const path = promotionForm.chapterIndex ? '/content' : '/detail';
    const finalUrl = `${baseUrl}${path}?${params.toString()}`;

    try {
      const created = await apiRequest('POST', '/promotion-links', {
        name: promotionForm.name,
        novel_id: promotionForm.novelId,
        novel_title: promotionForm.title,
        chapter_index: promotionForm.chapterIndex ? parseInt(promotionForm.chapterIndex, 10) : 0,
        utm_source: promotionForm.utmSource,
        utm_campaign: promotionForm.utmCampaign,
        generated_url: finalUrl,
        domain_id: targetDomainObj ? targetDomainObj.id : (defaultDomainObj ? defaultDomainObj.id : null),
        domain: targetDomainObj ? targetDomainObj.domain : (defaultDomainObj ? defaultDomainObj.domain : ''),
        fb_pixel_id: promotionForm.fbPixelId ? parseInt(promotionForm.fbPixelId, 10) : null,
        recharge_template_id: promotionForm.rechargeTemplateId ? parseInt(promotionForm.rechargeTemplateId, 10) : null,
        coin_cost_per_thousand: rawPromoCost !== '' ? parseInt(rawPromoCost, 10) : null,
        start_pay_chapter_index: rawPromoStartPay !== '' ? parseInt(rawPromoStartPay, 10) : null,
      });

      // If link was created with an ID, append link_id to the final URL for accurate tracking
      let resolvedUrl = finalUrl;
      if (created && created.id) {
        params.set('link_id', String(created.id));
        resolvedUrl = `${baseUrl}${path}?${params.toString()}`;
      }
      setGeneratedLink(created?.generated_url || resolvedUrl);
      window.showToast?.('推广链接已成功生成并记录！', 'success');
    } catch (err: any) {
      window.showToast?.('保存推广链接失败：' + err.message, 'error');
    }
  };

  const handleCopyLink = () => {
    if (generatedLink) {
      navigator.clipboard.writeText(generatedLink);
      window.showToast?.('链接已复制到剪贴板！', 'success');
    }
  };

  const renderPromotionModal = () => {
    return (
      <PromotionLinkModal
        isOpen={isPromoModalOpen}
        onClose={() => setIsPromoModalOpen(false)}
        promotionForm={promotionForm}
        setPromotionForm={setPromotionForm}
        pixels={pixels}
        templates={templates}
        domains={domains}
        onGenerateLink={handleGeneratePromotionLink}
        generatedLink={generatedLink}
        onCopyLink={handleCopyLink}
      />
    );
  };

  if (!hasLoadedOnce && loading) {
    return <div style={{ color: 'hsl(var(--text-secondary))', padding: '20px' }}>正在加载书籍工作台...</div>;
  }

  // --- Novel Detail View ---
  if (selectedNovel) {
    const publishMeta = getPublishStatusMeta(selectedNovel.publish_status);
    return (
      <div className="animate-fade-in" style={{ padding: '16px 24px 24px 24px' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px', gap: '12px', flexWrap: 'wrap' }}>
          <button
            type="button"
            onClick={() => {
              resetDetailTransientState();
              setSelectedNovel(null);
            }}
            className="btn-secondary"
            style={{ display: 'inline-flex', gap: '8px' }}
          >
            <ArrowLeft size={16} /> 返回书籍列表
          </button>
          <div style={{ display: 'flex', gap: '12px', flexWrap: 'wrap' }}>
            {canCreatePromo && (
              <button
                type="button"
                onClick={() => {
                  if (selectedNovel.publish_status !== 'published') {
                    window.showToast?.(`提示：当前书籍为「${publishMeta.label}」状态，投放前请先导入章节并上架发布。`, 'info');
                  }
                  setPromotionForm({
                    name: '',
                    novelId: selectedNovel.id,
                    title: selectedNovel.title,
                    chapterIndex: '1',
                    utmSource: 'facebook',
                    utmCampaign: '',
                    fbPixelId: '',
                    rechargeTemplateId: '',
                    coinCostPerThousand: '',
                    startPayChapterIndex: ''
                  });
                  setGeneratedLink('');
                  setIsPromoModalOpen(true);
                }}
                className="btn-secondary"
                style={{ display: 'inline-flex', gap: '8px' }}
              >
                <Link2 size={16} /> 生成推广链接
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
                      selectedNovel.publish_status === 'published' ? 'offline' : 'published'
                    )
                  }
                  className="btn-secondary"
                  style={{
                    display: 'inline-flex',
                    gap: '8px',
                    color: selectedNovel.publish_status === 'published' ? '#f87171' : '#10b981',
                    borderColor: selectedNovel.publish_status === 'published' ? 'rgba(239, 68, 68, 0.3)' : 'rgba(16, 185, 129, 0.35)',
                  }}
                >
                  {publishUpdatingId === selectedNovel.id
                    ? '状态更新中...'
                    : selectedNovel.publish_status === 'published'
                      ? '下架书籍'
                      : '上架发布'}
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setEditNovelForm({
                      title: selectedNovel.title,
                      author: selectedNovel.author,
                      cover_url: selectedNovel.cover_key || selectedNovel.cover_url,
                      cover_preview_url: selectedNovel.cover_url,
                      status: selectedNovel.status,
                      publish_status: selectedNovel.publish_status || 'draft',
                      rating: selectedNovel.rating,
                      synopsis: selectedNovel.synopsis || '',
                      genres: selectedNovel.genres.join(', '),
                      coin_cost_per_thousand: selectedNovel.coin_cost_per_thousand != null ? selectedNovel.coin_cost_per_thousand : '',
                      start_pay_chapter_index: selectedNovel.start_pay_chapter_index || globalStartPayChapter || 3
                    });
                    setIsEditing(true);
                  }}
                  className="btn-primary"
                  style={{ display: 'inline-flex', gap: '8px' }}
                >
                  <Edit size={16} /> 编辑书籍与计费
                </button>
              </>
            )}
          </div>
        </div>

        {/* Novel Card Cover Header */}
        <div style={{ display: 'grid', gridTemplateColumns: '200px 1fr', gap: '30px', marginBottom: '32px' }}>
          <img
            src={selectedNovel.cover_url}
            alt={selectedNovel.title}
            style={{ width: '100%', borderRadius: '12px', border: '1px solid hsl(var(--border))', boxShadow: 'var(--shadow-lg)' }}
          />
          <div>
            <div style={{ display: 'flex', gap: '8px', marginBottom: '12px', flexWrap: 'wrap' }}>
              <span className={`badge ${publishMeta.badgeClass}`}>
                {publishMeta.label}
              </span>
              <span className={`badge ${selectedNovel.status === 'Completed' ? 'badge-green' : 'badge-orange'}`}>
                {selectedNovel.status === 'Completed' ? '已完结' : '连载中'}
              </span>
              <span className="badge badge-violet">{selectedNovel.rating} ★</span>
            </div>
            <h2 style={{ fontSize: '2rem', marginBottom: '8px' }}>{selectedNovel.title}</h2>
            <p style={{ color: 'hsl(var(--text-secondary))', marginBottom: '14px', fontSize: '0.95rem' }}>
              作者: <span style={{ color: 'hsl(var(--text-primary))', fontWeight: 500 }}>{selectedNovel.author}</span>
            </p>
            <p style={{ color: 'hsl(var(--text-muted))', fontSize: '0.9rem', lineHeight: 1.6, marginBottom: '20px' }}>
              {selectedNovel.synopsis || '暂无书籍简介。'}
            </p>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '24px', borderTop: '1px solid hsl(var(--border))', paddingTop: '16px' }}>
              <div>
                <span style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>小说总字数</span>
                <span style={{ fontSize: '1.2rem', fontWeight: 600 }}>{selectedNovel.word_count.toLocaleString()} 字</span>
              </div>
              <div>
                <span style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>千字价格</span>
                <span style={{ fontSize: '1.2rem', fontWeight: 600, color: 'hsl(var(--primary))' }}>
                  {selectedNovel.coin_cost_per_thousand != null
                    ? `${selectedNovel.coin_cost_per_thousand} 金币`
                    : `全局 (${selectedNovel.effective_coin_cost_per_thousand ?? globalCoinCost ?? 500} 金币)`}
                </span>
              </div>
              <div>
                <span style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>收费起点</span>
                <span style={{ fontSize: '1.2rem', fontWeight: 600, color: 'hsl(var(--primary))' }}>第 {selectedNovel.start_pay_chapter_index || globalStartPayChapter || 3} 章起</span>
              </div>
              <div>
                <span style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>分类标签</span>
                <span style={{ fontSize: '0.9rem', color: 'hsl(var(--text-secondary))' }}>{selectedNovel.genres.join(', ') || '未分类'}</span>
              </div>
            </div>
          </div>
        </div>

        {/* Chapters & Bulk Import Split Grid */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '30px', alignItems: 'start' }}>
          {/* Chapter catalogue */}
          <div className="glass-panel" style={{ padding: '24px' }}>
            <h3 style={{ fontSize: '1.2rem', marginBottom: '18px', display: 'flex', alignItems: 'center' }}>
              <Book size={18} style={{ marginRight: '8px', color: 'hsl(var(--primary))' }} />
              目录列表 ({chapters.length} 章)
            </h3>
            <div style={{ maxHeight: '420px', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: '10px', paddingRight: '8px' }}>
              {chapters.length === 0 ? (
                <p style={{ color: 'hsl(var(--text-muted))', fontSize: '0.9rem' }}>暂无章节，请在右侧批量导入章节。</p>
              ) : (
                paginatedChapters.map((ch) => (
                  <div key={ch.id} style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    padding: '12px 16px',
                    backgroundColor: 'hsl(var(--bg-card))',
                    border: '1px solid hsl(var(--border))',
                    borderRadius: '8px'
                  }}>
                    <div>
                      <span style={{ fontSize: '0.8rem', color: 'hsl(var(--text-muted))', marginRight: '8px' }}>第 {ch.chapter_index + 1} 章</span>
                      <span style={{ fontSize: '0.9rem', fontWeight: 500 }}>{ch.title}</span>
                      <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))', display: 'block' }}>{ch.word_count} 字</span>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                      {ch.is_paid ? (
                        <span className="badge badge-orange">{ch.price} 金币</span>
                      ) : (
                        <span className="badge badge-green">免费章节</span>
                      )}
                      <button
                        onClick={() => handlePreviewChapter(ch.chapter_index)}
                        className="btn-secondary"
                        style={{ padding: '4px 8px', fontSize: '0.75rem', borderRadius: '6px' }}
                      >
                        预览
                      </button>
                    </div>
                  </div>
                ))
              )}
            </div>

            {totalChapterPages > 1 && (
              <div style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                marginTop: '14px',
                fontSize: '0.8rem',
                color: 'hsl(var(--text-secondary))'
              }}>
                <button
                  className="btn-secondary"
                  onClick={() => setChapterPage(p => Math.max(1, p - 1))}
                  disabled={chapterPage === 1}
                  style={{ padding: '4px 8px', fontSize: '0.75rem', borderRadius: '4px' }}
                >
                  上一页
                </button>
                <span>
                  第 {chapterPage} / {totalChapterPages} 页 (共 {chapters.length} 章)
                </span>
                <button
                  className="btn-secondary"
                  onClick={() => setChapterPage(p => Math.min(totalChapterPages, p + 1))}
                  disabled={chapterPage === totalChapterPages}
                  style={{ padding: '4px 8px', fontSize: '0.75rem', borderRadius: '4px' }}
                >
                  下一页
                </button>
              </div>
            )}
          </div>

          {/* Bulk Importer panel */}
          <div className="glass-panel" style={{ padding: '24px' }}>
            <h3 style={{ fontSize: '1.2rem', marginBottom: '18px', display: 'flex', alignItems: 'center' }}>
              <Upload size={18} style={{ marginRight: '8px', color: 'hsl(var(--accent-pink))' }} />
              批量导入章节手稿
            </h3>

            {importMessage && (
              <div style={{ backgroundColor: 'rgba(16, 185, 129, 0.1)', border: '1px solid rgba(16, 185, 129, 0.25)', color: '#10b981', padding: '12px', borderRadius: '8px', fontSize: '0.85rem', marginBottom: '18px' }}>
                <CheckCircle size={14} style={{ marginRight: '6px', inlineSize: 'auto' }} /> {importMessage}
              </div>
            )}

            {importError && (
              <div style={{ backgroundColor: 'rgba(239, 68, 68, 0.1)', border: '1px solid rgba(239, 68, 68, 0.25)', color: '#f87171', padding: '12px', borderRadius: '8px', fontSize: '0.85rem', marginBottom: '18px' }}>
                {importError}
              </div>
            )}

            <form onSubmit={handleBulkImport}>
              <div style={{ marginBottom: '16px' }}>
                <ChapterUploadFields
                  importType={importType}
                  onImportTypeChange={setImportType}
                  file={importFile}
                  onFileChange={setImportFile}
                  fileInputRef={importFileInputRef}
                  regex={regex}
                  onRegexChange={setRegex}
                  required={true}
                  disabled={importLoading}
                />
              </div>

              {canEditNovels && (
                <details style={{ marginBottom: '16px' }}>
                  <summary>章节映射</summary>
                  <p>正文完全一致的唯一章节会自动保留 ID。改写、拆分或下架章节时，请填写对应 ID。新章节序号从 1 开始。</p>
                  <label htmlFor="chapter-import-mapping">保留、拆分及下架映射（JSON）</label>
                  <textarea
                    id="chapter-import-mapping"
                    className="input-field"
                    rows={3}
                    value={chapterMapping}
                    disabled={importLoading}
                    placeholder={'{"keep":{"2":"原章节ID"},"split_from":{"3":"原章节ID"},"retire":[]}'}
                    onChange={(event) => setChapterMapping(event.target.value)}
                  />
                  <details onToggle={(event) => {
                    if (event.currentTarget.open) void loadChapterIdentities(selectedNovel.id);
                  }}>
                    <summary>现有及已下架的章节 ID</summary>
                    <ul>{chapterIdentities.map((chapter) => (
                      <li key={chapter.id}>
                        {chapter.chapter_index === null ? '已下架' : chapter.chapter_index + 1} · {chapter.title} · <code>{chapter.id}</code>
                      </li>
                    ))}</ul>
                  </details>
                </details>
              )}

              {canEditNovels ? (
                <button
                  type="submit"
                  className="btn-primary"
                  disabled={importLoading || !importFile}
                  style={{ width: '100%', display: 'flex', gap: '8px' }}
                >
                  <Upload size={16} /> {importLoading ? '正在分析手稿写入数据库...' : '导入并分割章节（单文件上限 50MB）'}
                </button>
              ) : (
                <div style={{ fontSize: '0.82rem', color: 'hsl(var(--text-muted))', textAlign: 'center', padding: '10px', borderRadius: '8px', border: '1px solid hsl(var(--border))' }}>
                  当前账号角色仅支持查看书籍目录，如需导入章节请联系管理员授予编辑权限。
                </div>
              )}
            </form>
          </div>
        </div>

        {/* Chapter Preview Modal */}
        {(previewChapter || previewLoading) && createPortal(
          <div style={{
            position: 'fixed', top: 0, left: 0, right: 0, bottom: 0,
            backgroundColor: 'rgba(15, 23, 42, 0.35)',
            backdropFilter: 'blur(2.5px)',
            WebkitBackdropFilter: 'blur(2.5px)',
            display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 10000,
            padding: '20px',
            overscrollBehavior: 'contain'
          }}>
            <div className="glass-panel animate-fade-in" style={{
              width: '100%', maxWidth: '640px', maxHeight: '80vh',
              backgroundColor: 'hsl(var(--bg-surface))', border: '1px solid hsl(var(--border))',
              borderRadius: '16px', display: 'flex', flexDirection: 'column',
              boxShadow: 'var(--shadow-lg)', overflow: 'hidden'
            }}>

              {/* Modal Header */}
              <div style={{
                padding: '20px 24px', borderBottom: '1px solid hsl(var(--border))',
                display: 'flex', justifyContent: 'space-between', alignItems: 'center'
              }}>
                <div>
                  <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))', display: 'block', marginBottom: '2px' }}>
                    {selectedNovel.title}
                  </span>
                  <h3 style={{ fontSize: '1.25rem', fontWeight: 600 }}>
                    {previewLoading ? '正在载入内容...' : `第 ${(previewChapter?.chapter_index ?? 0) + 1} 章: ${previewChapter?.title}`}
                  </h3>
                </div>
                <button
                  type="button"
                  aria-label="关闭预览"
                  style={{
                    background: 'transparent', border: 'none', padding: '6px',
                    borderRadius: '8px', color: 'hsl(var(--text-secondary))', cursor: 'pointer',
                    display: 'inline-flex', alignItems: 'center', justifyContent: 'center'
                  }}
                  onClick={() => setPreviewChapter(null)}
                >
                  <X size={20} aria-hidden="true" />
                </button>
              </div>

              {/* Modal Body */}
              <div style={{ padding: '24px', overflowY: 'auto', flex: 1, backgroundColor: 'hsl(var(--bg-card) / 0.3)' }}>
                {previewLoading ? (
                  <div style={{ textAlign: 'center', color: 'hsl(var(--text-secondary))', padding: '40px 0' }}>
                    正在从服务器获取章节手稿文本...
                  </div>
                ) : (
                  <div style={{
                    fontSize: '0.95rem', lineHeight: 1.8, color: 'hsl(var(--text-primary))',
                    whiteSpace: 'pre-wrap', fontFamily: 'system-ui, -apple-system, sans-serif'
                  }}>
                    {previewChapter?.content}
                  </div>
                )}
              </div>

              {/* Modal Footer */}
              <div style={{
                padding: '16px 24px', borderTop: '1px solid hsl(var(--border))',
                display: 'flex', justifyContent: 'space-between', alignItems: 'center',
                backgroundColor: 'hsl(var(--bg-surface))'
              }}>
                <span style={{ fontSize: '0.8rem', color: 'hsl(var(--text-muted))' }}>
                  {!previewLoading && `总字数: ${previewChapter?.word_count} 字 | 订阅价: ${previewChapter?.is_paid ? `${previewChapter?.price} 金币` : '免费'}`}
                </span>
                <button className="btn-primary" style={{ padding: '6px 16px', fontSize: '0.85rem' }} onClick={() => setPreviewChapter(null)}>
                  确认
                </button>
              </div>

            </div>
          </div>,
          document.body
        )}

        {renderPromotionModal()}
        {/* Edit Novel Modal */}
        <NovelFormModal
          isOpen={isEditing}
          isEditing={true}
          isSubmitting={isSubmittingNovel}
          hasChapters={chapters.length > 0}
          globalCoinCost={globalCoinCost}
          formData={editNovelForm}
          setFormData={(data) => setEditNovelForm(data)}
          onSubmit={handleUpdateNovel}
          onCancel={() => setIsEditing(false)}
        />
      </div>
    );
  }

  // --- Novels List View ---
  return (
    <div className="animate-fade-in" style={{ padding: '16px 24px 24px 24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px', gap: '12px', flexWrap: 'wrap' }}>
        <div>
          <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">书籍管理</h1>
        </div>
        <div style={{ display: 'flex', gap: '12px', flexWrap: 'wrap' }}>
          {canEditGlobalSettings && (
            <>
              <button
                type="button"
                onClick={() => setActiveGlobalPanel('coin')}
                className="btn-secondary"
                style={{ display: 'flex', gap: '6px', alignItems: 'center' }}
              >
                <Settings size={16} aria-hidden="true" /> 全局金币设置 (千字 {globalCoinCost} 币)
              </button>
              <button
                type="button"
                onClick={() => setActiveGlobalPanel('chapter')}
                className="btn-secondary"
                style={{ display: 'flex', gap: '6px', alignItems: 'center' }}
              >
                <BookOpen size={16} aria-hidden="true" /> 全局章节设置 (第 {globalStartPayChapter} 章起)
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
              className="btn-primary"
              style={{ display: 'flex', gap: '6px' }}
            >
              <Plus size={16} /> 新建书籍档案
            </button>
          )}
        </div>
      </div>

      {canEditGlobalSettings && activeGlobalPanel && (() => {
        const isCoin = activeGlobalPanel === 'coin';
        const modalHeadingId = isCoin ? 'global-coin-modal-title' : 'global-chapter-modal-title';
        return createPortal(
          <div
            style={{
              position: 'fixed',
              inset: 0,
              backgroundColor: 'rgba(15, 23, 42, 0.35)',
              backdropFilter: 'blur(2.5px)',
              WebkitBackdropFilter: 'blur(2.5px)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              zIndex: 10000,
              padding: '20px',
              overscrollBehavior: 'contain',
            }}
          >
            <div
              role="dialog"
              aria-modal="true"
              aria-labelledby={modalHeadingId}
              className="glass-panel animate-fade-in"
              style={{
                width: '100%',
                maxWidth: '500px',
                backgroundColor: 'hsl(var(--bg-surface))',
                border: '1px solid hsl(var(--border))',
                borderRadius: '16px',
                boxShadow: 'var(--shadow-lg)',
                overflow: 'hidden',
              }}
            >
              <div
                style={{
                  padding: '18px 24px',
                  borderBottom: '1px solid hsl(var(--border))',
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                }}
              >
                <h3 id={modalHeadingId} style={{ fontSize: '1.15rem', fontWeight: 600, display: 'flex', alignItems: 'center', gap: '8px', margin: 0 }}>
                  {isCoin ? (
                    <>
                      <Settings size={18} style={{ color: 'hsl(var(--primary))' }} aria-hidden="true" /> 全局千字金币设置
                    </>
                  ) : (
                    <>
                      <BookOpen size={18} style={{ color: 'hsl(var(--primary))' }} aria-hidden="true" /> 全局起付章节设置
                    </>
                  )}
                </h3>
                <button
                  type="button"
                  aria-label="关闭窗口"
                  disabled={savingGlobalSetting}
                  onClick={() => setActiveGlobalPanel(null)}
                  style={{
                    background: 'transparent',
                    border: 'none',
                    padding: '6px',
                    borderRadius: '8px',
                    display: 'inline-flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    color: 'hsl(var(--text-secondary))',
                    cursor: savingGlobalSetting ? 'not-allowed' : 'pointer',
                    opacity: savingGlobalSetting ? 0.5 : 1,
                  }}
                >
                  <X size={20} aria-hidden="true" />
                </button>
              </div>

              <form onSubmit={isCoin ? handleSaveGlobalCoinConfig : handleSaveGlobalChapterConfig} style={{ margin: 0 }}>
                <div style={{ padding: '24px' }}>
                  <div style={{ marginBottom: '18px' }}>
                    <label
                      htmlFor={isCoin ? globalCoinInputId : globalChapterInputId}
                      style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}
                    >
                      {isCoin ? '全局千字收费价格 (金币)' : '全局默认起始付费章节 (从第几章起)'}
                    </label>
                    <input
                      id={isCoin ? globalCoinInputId : globalChapterInputId}
                      type="number"
                      className="input-field"
                      min={isCoin ? 0 : 1}
                      value={isCoin ? globalCoinCost : globalStartPayChapter}
                      onChange={(e) => {
                        const val = e.target.value === '' ? ('' as any) : Number(e.target.value);
                        if (isCoin) {
                          setGlobalCoinCost(val);
                        } else {
                          setGlobalStartPayChapter(val);
                        }
                      }}
                      required
                    />
                  </div>

                  <div style={{ display: 'flex', alignItems: 'flex-start', gap: '8px', padding: '12px', borderRadius: '10px', backgroundColor: 'hsl(var(--bg-card))', border: '1px solid hsl(var(--border))' }}>
                    <input
                      type="checkbox"
                      id={isCoin ? globalCoinApplyAllId : globalChapterApplyAllId}
                      checked={isCoin ? applyCoinCostToAll : applyStartPayToAll}
                      disabled={isCoin && resetNovelCoinCost}
                      onChange={(e) =>
                        isCoin
                          ? setApplyCoinCostToAll(e.target.checked)
                          : setApplyStartPayToAll(e.target.checked)
                      }
                      style={{ width: '16px', height: '16px', marginTop: '3px', flexShrink: 0, cursor: 'pointer', accentColor: 'hsl(var(--primary))' }}
                    />
                    <label
                      htmlFor={isCoin ? globalCoinApplyAllId : globalChapterApplyAllId}
                      style={{ fontSize: '0.82rem', lineHeight: 1.55, color: 'hsl(var(--text-primary))', cursor: 'pointer' }}
                    >
                      {isCoin
                        ? `同步覆盖所有已有书籍单价（勾选后将重置包括独立定价书籍在内的全部未归档书籍为千字 ${globalCoinCost} 币；不勾选则仅刷新继承全局单价的书籍，不影响任何书的起付章节）`
                        : `同步覆盖所有已有书籍起付章节（勾选后将把全部未归档书籍统一更新为第 ${globalStartPayChapter} 章起付费；不勾选则仅作为后续新建书籍默认值，不影响千字金币单价）`}
                    </label>
                  </div>
                  {isCoin && (
                    <label>
                      <input
                        type="checkbox"
                        checked={resetNovelCoinCost}
                        disabled={applyCoinCostToAll}
                        onChange={(event) => setResetNovelCoinCost(event.target.checked)}
                      />
                      将全部在库书籍改为继承全局单价
                    </label>
                  )}
                </div>

                <div
                  style={{
                    padding: '16px 24px',
                    borderTop: '1px solid hsl(var(--border))',
                    display: 'flex',
                    justifyContent: 'flex-end',
                    alignItems: 'center',
                    gap: '12px',
                    backgroundColor: 'hsl(var(--bg-surface))',
                  }}
                >
                  <button type="button" className="btn-secondary" disabled={savingGlobalSetting} onClick={() => setActiveGlobalPanel(null)}>
                    取消
                  </button>
                  <button type="submit" className="btn-primary" disabled={savingGlobalSetting}>
                    {savingGlobalSetting ? '保存中...' : isCoin ? '保存全局金币设置' : '保存全局章节设置'}
                  </button>
                </div>
              </form>
            </div>
          </div>,
          document.body
        );
      })()}

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

      {/* Compact Multi-Field Search & Filter Bar */}
      <search style={{ display: 'block', marginBottom: '16px' }}>
        <form
          onSubmit={handleSearchSubmit}
          className="glass-panel"
          style={{
            padding: '14px 16px',
            display: 'flex',
            flexDirection: 'column',
            gap: '10px',
          }}
        >
          <div style={{ display: 'flex', gap: '12px', alignItems: 'flex-end', flexWrap: 'wrap' }}>
            <div style={{ flex: '1 1 170px', maxWidth: '220px' }}>
              <label
                htmlFor={titleOrIdInputId}
                style={{ display: 'block', fontSize: '0.76rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px', fontWeight: 500 }}
              >
                书名 / ID
              </label>
              <div style={{ position: 'relative' }}>
                <span style={{ position: 'absolute', left: '10px', top: '50%', transform: 'translateY(-50%)', color: 'hsl(var(--text-muted))', pointerEvents: 'none', display: 'inline-flex' }}>
                  <Search size={14} aria-hidden="true" />
                </span>
                <input
                  id={titleOrIdInputId}
                  name="title_or_id"
                  type="search"
                  className="input-field"
                  style={{ paddingLeft: '32px' }}
                  placeholder="书名或书籍 ID"
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  enterKeyHint="search"
                  value={filterDraft.titleOrId}
                  onChange={(e) => setFilterDraft((prev) => ({ ...prev, titleOrId: e.target.value }))}
                />
              </div>
            </div>

            <div style={{ flex: '1 1 140px', maxWidth: '175px' }}>
              <label
                htmlFor={authorInputId}
                style={{ display: 'block', fontSize: '0.76rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px', fontWeight: 500 }}
              >
                作者
              </label>
              <input
                id={authorInputId}
                name="author"
                type="text"
                className="input-field"
                placeholder="输入作者名"
                autoCorrect="off"
                autoCapitalize="none"
                spellCheck={false}
                enterKeyHint="search"
                value={filterDraft.author}
                onChange={(e) => setFilterDraft((prev) => ({ ...prev, author: e.target.value }))}
              />
            </div>

            <div style={{ flex: '0 1 124px', minWidth: '116px' }}>
              <label
                htmlFor={statusSelectId}
                style={{ display: 'block', fontSize: '0.76rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px', fontWeight: 500 }}
              >
                状态
              </label>
              <select
                id={statusSelectId}
                name="publish_status"
                className="input-field"
                value={filterDraft.publishStatus}
                onChange={(e) =>
                  setFilterDraft((prev) => ({
                    ...prev,
                    publishStatus: e.target.value as '' | 'published' | 'draft' | 'offline',
                  }))
                }
              >
                <option value="">全部</option>
                <option value="published">已上架</option>
                <option value="draft">草稿</option>
                <option value="offline">已下架</option>
              </select>
            </div>

            <div style={{ flex: '0 1 148px' }}>
              <label
                htmlFor={createdStartInputId}
                style={{ display: 'block', fontSize: '0.76rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px', fontWeight: 500 }}
              >
                创建时间 (起)
              </label>
              <input
                ref={createdStartRef}
                id={createdStartInputId}
                name="created_start"
                type="date"
                className="input-field"
                aria-invalid={dateFilterError ? 'true' : 'false'}
                aria-describedby={dateFilterError ? dateFilterErrorId : undefined}
                value={filterDraft.createdStart}
                onChange={(e) => {
                  setDateFilterError('');
                  setFilterDraft((prev) => ({ ...prev, createdStart: e.target.value }));
                }}
              />
            </div>

            <div style={{ flex: '0 1 148px' }}>
              <label
                htmlFor={createdEndInputId}
                style={{ display: 'block', fontSize: '0.76rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px', fontWeight: 500 }}
              >
                创建时间 (止)
              </label>
              <input
                id={createdEndInputId}
                name="created_end"
                type="date"
                className="input-field"
                aria-invalid={dateFilterError ? 'true' : 'false'}
                aria-describedby={dateFilterError ? dateFilterErrorId : undefined}
                value={filterDraft.createdEnd}
                onChange={(e) => {
                  setDateFilterError('');
                  setFilterDraft((prev) => ({ ...prev, createdEnd: e.target.value }));
                }}
              />
            </div>

            <div style={{ display: 'flex', gap: '8px', alignItems: 'center', marginLeft: 'auto' }}>
              <button
                type="submit"
                className="btn-primary"
                style={{ height: '38px', padding: '0 16px', fontSize: '0.8rem', whiteSpace: 'nowrap', gap: '6px' }}
              >
                <Search size={14} aria-hidden="true" /> 查询
              </button>
              <button
                type="button"
                className="btn-secondary"
                onClick={handleResetFilters}
                style={{ height: '38px', padding: '0 14px', fontSize: '0.8rem', whiteSpace: 'nowrap' }}
              >
                重置
              </button>
            </div>
          </div>

          {dateFilterError && (
            <p
              id={dateFilterErrorId}
              role="status"
              aria-live="polite"
              style={{ margin: 0, fontSize: '0.76rem', color: '#ef4444' }}
            >
              {dateFilterError}
            </p>
          )}
        </form>
      </search>

      {/* Batch Operations Toolbar */}
      {canEditNovels && (
        <div
          className="glass-panel"
          style={{
            padding: '10px 14px',
            marginBottom: '12px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: '12px',
            flexWrap: 'wrap',
            borderColor: selectedNovelIds.length > 0 ? 'hsl(var(--primary) / 0.35)' : undefined,
          }}
        >
          <span style={{ fontSize: '0.8rem', color: 'hsl(var(--text-secondary))' }}>
            {selectedNovelIds.length > 0
              ? `已选 ${selectedNovelIds.length} 本书籍`
              : '勾选下方书籍后可批量上架或下架'}
          </span>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap' }}>
            {selectedNovelIds.length > 0 && (
              <button
                type="button"
                className="btn-secondary"
                disabled={batchPublishUpdating}
                onClick={() => setSelectedNovelIds([])}
                style={{ padding: '4px 10px', fontSize: '0.75rem', whiteSpace: 'nowrap' }}
              >
                取消勾选
              </button>
            )}
            <button
              type="button"
              className="btn-secondary"
              disabled={selectedNovelIds.length === 0 || batchPublishUpdating}
              onClick={() => handleBatchPublishStatus('published')}
              style={{
                padding: '4px 12px',
                fontSize: '0.75rem',
                whiteSpace: 'nowrap',
                color: selectedNovelIds.length > 0 ? '#10b981' : undefined,
                borderColor: selectedNovelIds.length > 0 ? 'rgba(16, 185, 129, 0.35)' : undefined,
                opacity: selectedNovelIds.length === 0 || batchPublishUpdating ? 0.5 : 1,
                cursor: selectedNovelIds.length === 0 || batchPublishUpdating ? 'not-allowed' : 'pointer',
              }}
            >
              {batchPublishUpdating ? '处理中...' : '批量上架'}
            </button>
            <button
              type="button"
              className="btn-secondary"
              disabled={selectedNovelIds.length === 0 || batchPublishUpdating}
              onClick={() => handleBatchPublishStatus('offline')}
              style={{
                padding: '4px 12px',
                fontSize: '0.75rem',
                whiteSpace: 'nowrap',
                color: selectedNovelIds.length > 0 ? '#f87171' : undefined,
                borderColor: selectedNovelIds.length > 0 ? 'rgba(239, 68, 68, 0.3)' : undefined,
                opacity: selectedNovelIds.length === 0 || batchPublishUpdating ? 0.5 : 1,
                cursor: selectedNovelIds.length === 0 || batchPublishUpdating ? 'not-allowed' : 'pointer',
              }}
            >
              {batchPublishUpdating ? '处理中...' : '批量下架'}
            </button>
          </div>
        </div>
      )}

      {/* Books List Table */}
      <div
        tabIndex={0}
        role="region"
        aria-label="书籍管理列表"
        className="glass-panel"
        style={{ padding: '8px', overflowX: 'auto', opacity: loading ? 0.65 : 1, transition: 'opacity 0.15s' }}
      >
        <table style={{ width: '100%', minWidth: '1320px', borderCollapse: 'collapse', textAlign: 'left', tableLayout: 'fixed' }}>
          <colgroup>
            {columns.map((col) => (
              <col key={col.key} style={{ width: `${colWidths[col.key] || 100}px` }} />
            ))}
          </colgroup>
          <thead>
            <tr style={{ borderBottom: '1px solid hsl(var(--border))', color: 'hsl(var(--text-secondary))', fontSize: '0.8rem' }}>
              {columns.map((col, index) => (
                <th
                  key={col.key}
                  style={{
                    padding: col.key === 'select' ? '12px 10px' : '12px 12px',
                    textAlign: col.key === 'select' ? 'center' : col.align === 'right' ? 'right' : 'left',
                    position: 'relative',
                    userSelect: 'none',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {col.key === 'select' ? (
                    <input
                      ref={selectAllCheckboxRef}
                      type="checkbox"
                      aria-label="全选本页书籍"
                      checked={allSelectedOnPage}
                      disabled={novels.length === 0 || batchPublishUpdating}
                      onChange={(e) => handleToggleSelectAllOnPage(e.target.checked)}
                      style={{ width: '15px', height: '15px', cursor: novels.length === 0 ? 'not-allowed' : 'pointer', accentColor: 'hsl(var(--primary))', verticalAlign: 'middle' }}
                    />
                  ) : (
                    col.label
                  )}
                  {col.resizable !== false && index < columns.length - 1 && (
                    <div
                      onMouseDown={(e) => handleMouseDown(col.key, e)}
                      className="resize-handle"
                      style={{
                        position: 'absolute',
                        right: 0,
                        top: 0,
                        bottom: 0,
                        width: '5px',
                        cursor: 'col-resize',
                        zIndex: 10,
                        borderRadius: '2px'
                      }}
                    />
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {loading && novels.length === 0 ? (
              <tr>
                <td colSpan={columns.length} style={{ padding: '20px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>正在加载书籍列表...</td>
              </tr>
            ) : novels.length === 0 ? (
              <tr>
                <td colSpan={columns.length} style={{ padding: '20px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>未找到匹配条件的书籍。</td>
              </tr>
            ) : (
              novels.map((novel) => {
                const pubMeta = getPublishStatusMeta(novel.publish_status);
                const isSelected = selectedNovelIds.includes(novel.id);
                const createdShort = formatNovelDateTime(novel.created_at);
                const createdFull = formatNovelDateTime(novel.created_at, true);
                const updatedShort = formatNovelDateTime(novel.updated_at);
                const updatedFull = formatNovelDateTime(novel.updated_at, true);
                return (
                <tr
                  key={novel.id}
                  onClick={() => handleNovelClick(novel)}
                  style={{
                    borderBottom: '1px solid hsl(var(--border) / 0.5)',
                    fontSize: '0.88rem',
                    cursor: 'pointer',
                    transition: 'background-color 0.2s',
                    color: 'hsl(var(--text-secondary))',
                    backgroundColor: isSelected ? 'hsl(var(--primary) / 0.06)' : 'transparent',
                  }}
                  onMouseEnter={(e) => {
                    if (!isSelected) e.currentTarget.style.backgroundColor = 'hsl(var(--bg-card))';
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.backgroundColor = isSelected ? 'hsl(var(--primary) / 0.06)' : 'transparent';
                  }}
                >
                  {canEditNovels && (
                    <td style={{ padding: '14px 10px', textAlign: 'center' }} onClick={(e) => e.stopPropagation()}>
                      <input
                        type="checkbox"
                        aria-label={`选择书籍《${novel.title}》`}
                        checked={isSelected}
                        disabled={batchPublishUpdating}
                        onChange={() => handleToggleSelectNovel(novel.id)}
                        style={{ width: '15px', height: '15px', cursor: 'pointer', accentColor: 'hsl(var(--primary))', verticalAlign: 'middle' }}
                      />
                    </td>
                  )}
                  <td style={{ padding: '14px 12px', fontFamily: 'monospace', fontSize: '0.8rem', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={String(novel.id)}>
                    {novel.id}
                  </td>
                  <td style={{ padding: '6px 12px' }}>
                    <img
                      src={novel.cover_url}
                      alt={novel.title}
                      style={{ width: '36px', height: '48px', objectFit: 'cover', borderRadius: '4px', border: '1px solid hsl(var(--border))' }}
                    />
                  </td>
                  <td style={{ padding: '14px 12px', fontWeight: 600, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={novel.title}>
                    {novel.title}
                  </td>
                  <td style={{ padding: '14px 12px', color: 'hsl(var(--text-secondary))', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={novel.author}>
                    {novel.author}
                  </td>
                  <td style={{ padding: '14px 12px', color: 'hsl(var(--text-secondary))', fontSize: '0.8rem', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={novel.genres.join(', ')}>
                    {novel.genres.join(', ') || '未分类'}
                  </td>
                  <td style={{ padding: '14px 12px', whiteSpace: 'nowrap' }}>
                    <span className={`badge ${pubMeta.badgeClass}`} style={{ fontSize: '0.7rem', whiteSpace: 'nowrap' }}>
                      {pubMeta.label}
                    </span>
                  </td>
                  <td style={{ padding: '14px 12px', whiteSpace: 'nowrap' }}>
                    <span className={`badge ${novel.status === 'Completed' ? 'badge-green' : 'badge-orange'}`} style={{ fontSize: '0.7rem', whiteSpace: 'nowrap' }}>
                      {novel.status === 'Completed' ? '已完结' : '连载中'}
                    </span>
                  </td>
                  <td style={{ padding: '14px 12px', color: 'hsl(var(--text-secondary))', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {novel.word_count.toLocaleString()} 字
                  </td>
                  <td style={{ padding: '14px 12px', color: 'hsl(var(--text-secondary))', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {novel.coin_cost_per_thousand != null
                      ? `${novel.coin_cost_per_thousand} 金币`
                      : `全局 (${novel.effective_coin_cost_per_thousand ?? globalCoinCost ?? 500})`}
                  </td>
                  <td style={{ padding: '14px 12px', whiteSpace: 'nowrap' }}>
                    <span className="badge badge-violet" style={{ fontSize: '0.7rem', whiteSpace: 'nowrap' }}>{novel.rating} ★</span>
                  </td>
                  <td
                    style={{ padding: '14px 12px', fontSize: '0.78rem', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}
                    title={createdFull}
                  >
                    {createdShort}
                  </td>
                  <td
                    style={{ padding: '14px 12px', fontSize: '0.78rem', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}
                    title={updatedFull}
                  >
                    {updatedShort}
                  </td>
                  <td style={{ padding: '14px 12px', textAlign: 'right' }} onClick={(e) => e.stopPropagation()}>
                    <div style={{ display: 'flex', gap: '5px', justifyContent: 'flex-end', alignItems: 'center', flexWrap: 'nowrap' }}>
                      {canEditNovels && (
                        <button
                          type="button"
                          disabled={publishUpdatingId === novel.id || batchPublishUpdating}
                          onClick={(e) => handleTogglePublishStatus(novel, novel.publish_status === 'published' ? 'offline' : 'published', e)}
                          className="btn-secondary"
                          style={{
                            padding: '3px 7px',
                            borderRadius: '5px',
                            fontSize: '0.68rem',
                            lineHeight: 1.2,
                            whiteSpace: 'nowrap',
                            flexShrink: 0,
                            color: novel.publish_status === 'published' ? '#f87171' : '#10b981',
                            borderColor: novel.publish_status === 'published' ? 'rgba(239, 68, 68, 0.25)' : 'rgba(16, 185, 129, 0.3)',
                          }}
                          title={novel.publish_status === 'published' ? '下架书籍' : '上架发布'}
                        >
                          {novel.publish_status === 'published' ? '下架' : '上架'}
                        </button>
                      )}
                      {canCreatePromo && (
                        <button
                          type="button"
                          className="btn-secondary"
                          style={{ padding: '5px', borderRadius: '6px', flexShrink: 0 }}
                          title="生成推广链接"
                          onClick={() => {
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
                              fbPixelId: '',
                              rechargeTemplateId: '',
                              coinCostPerThousand: '',
                              startPayChapterIndex: ''
                            });
                            setGeneratedLink('');
                            setIsPromoModalOpen(true);
                          }}
                        >
                          <Link2 size={14} />
                        </button>
                      )}
                      <button type="button" className="btn-secondary" style={{ padding: '5px', borderRadius: '6px', flexShrink: 0 }} title="进入详情/导入" onClick={() => handleNovelClick(novel)}>
                        <Edit size={14} />
                      </button>
                      {canEditNovels && (
                        <button
                          type="button"
                          className="btn-secondary"
                          onClick={(e) => handleDeleteNovel(novel.id, e)}
                          style={{ padding: '5px', borderRadius: '6px', flexShrink: 0, color: '#f87171', borderColor: 'rgba(239, 68, 68, 0.2)' }}
                          title="删除书籍"
                        >
                          <Trash size={14} />
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              );
              })
            )}
          </tbody>
        </table>
      </div>

      {/* Pagination Controls */}
      {totalPages > 1 && (
        <div style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginTop: '20px',
          padding: '8px 16px',
          fontSize: '0.85rem',
          color: 'hsl(var(--text-secondary))',
          backgroundColor: 'hsl(var(--bg-surface) / 0.5)',
          borderRadius: '8px',
          border: '1px solid hsl(var(--border))'
        }}>
          <div>
            显示第 {startIndex + 1} 至 {Math.min(startIndex + novels.length, totalNovels)} 项，共 {totalNovels} 项
          </div>
          <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
            <button
              type="button"
              className="btn-secondary"
              onClick={() => setNovelQuery(prev => ({ ...prev, page: Math.max(1, prev.page - 1) }))}
              disabled={currentPage === 1}
              style={{ padding: '6px 12px', fontSize: '0.8rem', borderRadius: '6px' }}
            >
              上一页
            </button>
            <span style={{ display: 'flex', alignItems: 'center', fontSize: '0.85rem', padding: '0 8px' }}>
              第 {currentPage} 页 / 共 {totalPages} 页
            </span>
            <button
              type="button"
              className="btn-secondary"
              onClick={() => setNovelQuery(prev => ({ ...prev, page: Math.min(totalPages, prev.page + 1) }))}
              disabled={currentPage === totalPages}
              style={{ padding: '6px 12px', fontSize: '0.8rem', borderRadius: '6px' }}
            >
              下一页
            </button>
          </div>
        </div>
      )}
      {renderPromotionModal()}
    </div>
  );
}

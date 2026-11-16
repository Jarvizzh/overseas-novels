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
  effective_coin_cost_per_thousand?: number;
  start_pay_chapter_index?: number;
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

  // Novels server pagination & filter states (single unified query to prevent debounce double-fetch)
  const [novelQuery, setNovelQuery] = useState<{
    page: number;
    search: string;
    publishStatus: '' | 'published' | 'draft' | 'offline';
  }>({
    page: 1,
    search: '',
    publishStatus: '',
  });
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
    status: 'Ongoing', publish_status: 'draft', rating: 4.5, synopsis: '', genres: 'Romance, Werewolf',
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
  const [importLoading, setImportLoading] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const searchInputId = useId();
  const searchDebounceTimerRef = useRef<number | null>(null);
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
  const [applyStartPayToAll, setApplyStartPayToAll] = useState(false);
  const [activeGlobalPanel, setActiveGlobalPanel] = useState<'coin' | 'chapter' | null>(null);
  const [savingGlobalSetting, setSavingGlobalSetting] = useState(false);
  const globalCoinInputId = useId();
  const globalCoinApplyAllId = useId();
  const globalChapterInputId = useId();
  const globalChapterApplyAllId = useId();

  useEffect(() => {
    const trimmed = searchQuery.trim();
    searchDebounceTimerRef.current = window.setTimeout(() => {
      searchDebounceTimerRef.current = null;
      setNovelQuery((prev) => (prev.search === trimmed ? prev : { ...prev, page: 1, search: trimmed }));
    }, 300);
    return () => {
      if (searchDebounceTimerRef.current !== null) {
        window.clearTimeout(searchDebounceTimerRef.current);
        searchDebounceTimerRef.current = null;
      }
    };
  }, [searchQuery]);

  const currentPage = novelQuery.page;
  const totalPages = Math.max(1, Math.ceil(totalNovels / pageSize));
  const startIndex = (currentPage - 1) * pageSize;

  const totalChapterPages = Math.ceil(chapters.length / chapterPageSize);
  const startChIndex = (chapterPage - 1) * chapterPageSize;
  const paginatedChapters = chapters.slice(startChIndex, startChIndex + chapterPageSize);

  const [colWidths, setColWidths] = useState<number[]>([
    80, // 书籍 ID
    70, // 封面
    210, // 书名
    85, // 作者
    100, // 分类标签
    95, // 上架状态
    85, // 连载状态
    95, // 总字数
    125, // 千字收费
    70, // 评分
    130 // 操作
  ]);

  const columns = [
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
        return { label: '草稿(未上架)', badgeClass: 'badge-orange' };
    }
  };

  const handleMouseDown = (index: number, e: React.MouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    const startWidth = colWidths[index];

    const handleMouseMove = (moveEvent: MouseEvent) => {
      const deltaX = moveEvent.clientX - startX;
      const newWidths = [...colWidths];
      newWidths[index] = Math.max(50, startWidth + deltaX);
      setColWidths(newWidths);
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
      if (targetQuery.search) query.set('search', targetQuery.search);
      if (targetQuery.publishStatus) query.set('publish_status', targetQuery.publishStatus);
      const data = await apiRequest('GET', `/novels?${query.toString()}`, undefined, false, controller.signal);
      if (controller.signal.aborted || seq !== novelsFetchSeqRef.current) return;

      setNovels(data.novels || []);
      setTotalNovels(data.total || 0);
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
    void fetchNovels(novelQuery);
    return () => {
      novelsAbortRef.current?.abort();
    };
  }, [novelQuery]);

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
      status: 'Ongoing', publish_status: 'draft', rating: 4.5, synopsis: '', genres: 'Romance, Werewolf',
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
    };
    const msg = applyCoinCostToAll
      ? `全局金币单价已保存，并将全部在库书籍重置为千字 ${costNum} 币（各书起付章节保持不变）！`
      : '全局金币单价保存成功！已同步继承全局单价的书籍。';

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

      const data = await apiRequest('POST', `/novels/${selectedNovel.id}/chapters/bulk-import`, formData, true);
      const isPublished = selectedNovel.publish_status === 'published';
      setImportMessage(
        isPublished
          ? `导入成功！共提取并导入了 ${data.chapters_count} 个章节（总字数 ${data.total_word_count} 字）。`
          : `导入成功！共提取并导入了 ${data.chapters_count} 个章节（总字数 ${data.total_word_count} 字）。当前书籍处于未上架状态，核对无误后请点击右上角「上架发布」。`
      );
      setImportFile(null);
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

  const handleBulkImport = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!importFile || !selectedNovel) return;
    if (chapters.length > 0 && window.showConfirm) {
      window.showConfirm(
        '覆盖导入会清空现有章节并从第 1 章重新编号，可能影响已有读者的阅读进度与已解锁章节序号，确定继续吗？',
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
                aria-expanded={activeGlobalPanel === 'coin'}
                onClick={() => setActiveGlobalPanel((prev) => (prev === 'coin' ? null : 'coin'))}
                className={activeGlobalPanel === 'coin' ? 'btn-primary' : 'btn-secondary'}
                style={{ display: 'flex', gap: '6px', alignItems: 'center' }}
              >
                <Settings size={16} aria-hidden="true" /> 全局金币设置 (千字 {globalCoinCost} 币)
              </button>
              <button
                type="button"
                aria-expanded={activeGlobalPanel === 'chapter'}
                onClick={() => setActiveGlobalPanel((prev) => (prev === 'chapter' ? null : 'chapter'))}
                className={activeGlobalPanel === 'chapter' ? 'btn-primary' : 'btn-secondary'}
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
        return (
          <form
            onSubmit={isCoin ? handleSaveGlobalCoinConfig : handleSaveGlobalChapterConfig}
            className="glass-panel"
            style={{ padding: '24px', marginBottom: '24px' }}
          >
            <h3 style={{ marginBottom: '18px', display: 'flex', alignItems: 'center', gap: '8px' }}>
              {isCoin ? (
                <>
                  <Settings size={18} style={{ color: 'hsl(var(--primary))' }} /> 全局千字金币设置
                </>
              ) : (
                <>
                  <BookOpen size={18} style={{ color: 'hsl(var(--primary))' }} /> 全局起付章节设置
                </>
              )}
            </h3>
            <div style={{ maxWidth: '340px', marginBottom: '16px' }}>
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

            <div style={{ marginBottom: '18px', display: 'flex', alignItems: 'flex-start', gap: '8px' }}>
              <input
                type="checkbox"
                id={isCoin ? globalCoinApplyAllId : globalChapterApplyAllId}
                checked={isCoin ? applyCoinCostToAll : applyStartPayToAll}
                onChange={(e) =>
                  isCoin
                    ? setApplyCoinCostToAll(e.target.checked)
                    : setApplyStartPayToAll(e.target.checked)
                }
                style={{ width: '16px', height: '16px', marginTop: '3px', flexShrink: 0, cursor: 'pointer', accentColor: 'hsl(var(--primary))' }}
              />
              <label
                htmlFor={isCoin ? globalCoinApplyAllId : globalChapterApplyAllId}
                style={{ fontSize: '0.85rem', lineHeight: 1.5, color: 'hsl(var(--text-primary))', cursor: 'pointer' }}
              >
                {isCoin
                  ? `同步覆盖所有已有书籍单价（勾选后将重置包括独立定价书籍在内的全部未归档书籍为千字 ${globalCoinCost} 币；不勾选则仅刷新继承全局单价的书籍，不影响任何书的起付章节）`
                  : `同步覆盖所有已有书籍起付章节（勾选后将把全部未归档书籍统一更新为第 ${globalStartPayChapter} 章起付费；不勾选则仅作为后续新建书籍默认值，不影响千字金币单价）`}
              </label>
            </div>

            <div style={{ display: 'flex', gap: '12px' }}>
              <button type="submit" className="btn-primary" disabled={savingGlobalSetting} style={{ height: '38px' }}>
                {savingGlobalSetting ? '保存中...' : isCoin ? '保存全局金币设置' : '保存全局章节设置'}
              </button>
              <button type="button" className="btn-secondary" style={{ height: '38px' }} onClick={() => setActiveGlobalPanel(null)}>
                取消
              </button>
            </div>
          </form>
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

      {/* Search & Publish Status Filter Bar */}
      <search>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (searchDebounceTimerRef.current !== null) {
              window.clearTimeout(searchDebounceTimerRef.current);
              searchDebounceTimerRef.current = null;
            }
            const trimmed = searchQuery.trim();
            setNovelQuery((prev) => ({ ...prev, page: 1, search: trimmed }));
          }}
          style={{ display: 'flex', gap: '16px', marginBottom: '20px', alignItems: 'center', flexWrap: 'wrap' }}
        >
          <div style={{ position: 'relative', flex: '1 1 320px' }}>
            <label htmlFor={searchInputId} className="visually-hidden">搜索书籍</label>
            <span style={{ position: 'absolute', left: '12px', top: '12px', color: 'hsl(var(--text-muted))' }}>
              <Search size={16} aria-hidden="true" />
            </span>
            <input
              id={searchInputId}
              name="search"
              type="search"
              className="input-field"
              style={{ paddingLeft: '38px' }}
              placeholder="输入书名、作者或书籍 ID 搜索全库..."
              value={searchQuery}
              onChange={(e) => {
                setSearchQuery(e.target.value);
              }}
            />
          </div>
          <fieldset style={{ border: 'none', margin: 0, padding: 0, display: 'flex', gap: '8px', flexWrap: 'wrap', alignItems: 'center' }}>
            <legend className="visually-hidden">按上架状态筛选</legend>
            {[
              { value: '', label: '全部' },
              { value: 'published', label: '已上架' },
              { value: 'draft', label: '草稿(未上架)' },
              { value: 'offline', label: '已下架' },
            ].map((opt) => {
              const active = novelQuery.publishStatus === opt.value;
              return (
                <button
                  key={opt.value || 'all'}
                  type="button"
                  aria-pressed={active}
                  className={active ? 'btn-primary' : 'btn-secondary'}
                  style={{ padding: '8px 14px', fontSize: '0.82rem' }}
                  onClick={() => {
                    const nextStatus = opt.value as '' | 'published' | 'draft' | 'offline';
                    setNovelQuery((prev) =>
                      prev.publishStatus === nextStatus && prev.page === 1
                        ? prev
                        : { ...prev, page: 1, publishStatus: nextStatus }
                    );
                  }}
                >
                  {opt.label}
                </button>
              );
            })}
          </fieldset>
        </form>
      </search>

      {/* Books List Table */}
      <div className="glass-panel" style={{ padding: '8px', overflowX: 'auto', opacity: loading ? 0.65 : 1, transition: 'opacity 0.15s' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', tableLayout: 'fixed' }}>
          <colgroup>
            {colWidths.map((width, i) => (
              <col key={i} style={{ width: `${width}px` }} />
            ))}
          </colgroup>
          <thead>
            <tr style={{ borderBottom: '1px solid hsl(var(--border))', color: 'hsl(var(--text-secondary))', fontSize: '0.8rem' }}>
              {columns.map((col, index) => (
                <th
                  key={col.key}
                  style={{
                    padding: '12px 12px',
                    textAlign: col.align === 'right' ? 'right' : 'left',
                    position: 'relative',
                    userSelect: 'none'
                  }}
                >
                  {col.label}
                  {index < columns.length - 1 && (
                    <div
                      onMouseDown={(e) => handleMouseDown(index, e)}
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
                return (
                <tr
                  key={novel.id}
                  onClick={() => handleNovelClick(novel)}
                  style={{ borderBottom: '1px solid hsl(var(--border) / 0.5)', fontSize: '0.9rem', cursor: 'pointer', transition: 'background-color 0.2s', color: 'hsl(var(--text-secondary))' }}
                  onMouseEnter={(e) => e.currentTarget.style.backgroundColor = 'hsl(var(--bg-card))'}
                  onMouseLeave={(e) => e.currentTarget.style.backgroundColor = 'transparent'}
                >
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
                  <td style={{ padding: '14px 12px' }}>
                    <span className={`badge ${pubMeta.badgeClass}`} style={{ fontSize: '0.7rem' }}>
                      {pubMeta.label}
                    </span>
                  </td>
                  <td style={{ padding: '14px 12px' }}>
                    <span className={`badge ${novel.status === 'Completed' ? 'badge-green' : 'badge-orange'}`} style={{ fontSize: '0.7rem' }}>
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
                  <td style={{ padding: '14px 12px' }}>
                    <span className="badge badge-violet" style={{ fontSize: '0.7rem' }}>{novel.rating} ★</span>
                  </td>
                  <td style={{ padding: '14px 12px', textAlign: 'right' }} onClick={(e) => e.stopPropagation()}>
                    <div style={{ display: 'flex', gap: '6px', justifyContent: 'flex-end' }}>
                      {canEditNovels && (
                        <button
                          type="button"
                          disabled={publishUpdatingId === novel.id}
                          onClick={(e) => handleTogglePublishStatus(novel, novel.publish_status === 'published' ? 'offline' : 'published', e)}
                          className="btn-secondary"
                          style={{
                            padding: '4px 8px',
                            borderRadius: '6px',
                            fontSize: '0.72rem',
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
                          style={{ padding: '6px', borderRadius: '6px' }}
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
                      <button type="button" className="btn-secondary" style={{ padding: '6px', borderRadius: '6px' }} title="进入详情/导入" onClick={() => handleNovelClick(novel)}>
                        <Edit size={14} />
                      </button>
                      {canEditNovels && (
                        <button
                          type="button"
                          className="btn-secondary"
                          onClick={(e) => handleDeleteNovel(novel.id, e)}
                          style={{ padding: '6px', borderRadius: '6px', color: '#f87171', borderColor: 'rgba(239, 68, 68, 0.2)' }}
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

import { useEffect, useState } from 'react';
import { apiRequest } from '../utils/api';
import { Book, Plus, ArrowLeft, Upload, Edit, Trash, CheckCircle, HelpCircle, Search, Settings, Link2 } from 'lucide-react';
import { NovelFormModal, type NovelFormData } from '../components/novels/NovelFormModal';
import { PromotionLinkModal } from '../components/novels/PromotionLinkModal';

interface Novel {
  id: number;
  title: string;
  author: string;
  cover_url: string;
  rating: number;
  status: string;
  synopsis: string;
  genres: string[];
  word_count: number;
  view_count: number;
  coin_cost_per_thousand?: number;
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
  const [novels, setNovels] = useState<Novel[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedNovel, setSelectedNovel] = useState<Novel | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);

  // Novels pagination states
  const [currentPage, setCurrentPage] = useState(1);
  const pageSize = 10;

  // Chapters pagination states
  const [chapterPage, setChapterPage] = useState(1);
  const chapterPageSize = 10;

  // Preview states
  const [previewChapter, setPreviewChapter] = useState<Chapter | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);

  // Form states
  const [isCreating, setIsCreating] = useState(false);
  const [newNovel, setNewNovel] = useState<NovelFormData>({
    title: '', author: '', cover_url: 'https://images.unsplash.com/photo-1543002588-bfa74002ed7e?w=300',
    status: 'Ongoing', rating: 4.5, synopsis: '', genres: 'Romance, Werewolf',
    coin_cost_per_thousand: 5, start_pay_chapter_index: 3
  });

  const [isEditing, setIsEditing] = useState(false);
  const [editNovelForm, setEditNovelForm] = useState<NovelFormData>({
    title: '', author: '', cover_url: '', status: 'Ongoing', rating: 4.5, synopsis: '', genres: '',
    coin_cost_per_thousand: 5, start_pay_chapter_index: 3
  });

  const [importFile, setImportFile] = useState<File | null>(null);
  const [importType, setImportType] = useState<'single_txt' | 'zip'>('single_txt');
  const [regex, setRegex] = useState(`(?i)(chapter\\s+\\d+|第[一二三四五六七八九十百千\\d]+章)`);
  const [importMessage, setImportMessage] = useState('');
  const [importError, setImportError] = useState('');
  const [importLoading, setImportLoading] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');

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
  const [promotionForm, setPromotionForm] = useState<{
    name: string;
    novelId: number;
    title: string;
    chapterIndex: string;
    utmSource: string;
    utmCampaign: string;
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
    fbPixelId: '',
    rechargeTemplateId: '',
    coinCostPerThousand: '',
    startPayChapterIndex: ''
  });
  const [generatedLink, setGeneratedLink] = useState('');

  // Global config states
  const [globalCoinCost, setGlobalCoinCost] = useState(5);
  const [globalStartPayChapter, setGlobalStartPayChapter] = useState(3);
  const [applyToAllNovels, setApplyToAllNovels] = useState(true);
  const [isConfiguringGlobal, setIsConfiguringGlobal] = useState(false);

  const filteredNovels = novels.filter(novel =>
    novel.title.toLowerCase().includes(searchQuery.toLowerCase()) ||
    novel.author.toLowerCase().includes(searchQuery.toLowerCase()) ||
    String(novel.id).toLowerCase().includes(searchQuery.toLowerCase())
  );

  useEffect(() => {
    setCurrentPage(1);
  }, [searchQuery]);

  const totalPages = Math.ceil(filteredNovels.length / pageSize);
  const startIndex = (currentPage - 1) * pageSize;
  const paginatedNovels = filteredNovels.slice(startIndex, startIndex + pageSize);

  const totalChapterPages = Math.ceil(chapters.length / chapterPageSize);
  const startChIndex = (chapterPage - 1) * chapterPageSize;
  const paginatedChapters = chapters.slice(startChIndex, startChIndex + chapterPageSize);

  const [colWidths, setColWidths] = useState<number[]>([
    80, // 书籍 ID
    70,  // 封面
    220, // 书名
    80, // 作者
    100, // 分类标签
    90,  // 连载状态
    100, // 总字数
    110, // 千字收费
    70,  // 评分
    100  // 操作
  ]);

  const columns = [
    { label: '书籍 ID', key: 'id' },
    { label: '封面', key: 'cover' },
    { label: '书名', key: 'title' },
    { label: '作者', key: 'author' },
    { label: '分类标签', key: 'genres' },
    { label: '连载状态', key: 'status' },
    { label: '总字数', key: 'word_count' },
    { label: '千字收费', key: 'coin_cost_per_thousand' },
    { label: '评分', key: 'rating' },
    { label: '操作', key: 'actions', align: 'right' }
  ];

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

  useEffect(() => {
    fetchNovels();
    fetchGlobalConfig();
    fetchPixels();
    fetchTemplates();
  }, []);

  const fetchNovels = async () => {
    setLoading(true);
    try {
      const data = await apiRequest('GET', '/novels?page_size=100');
      setNovels(data.novels || []);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  const fetchChapters = async (novelID: number) => {
    try {
      const data = await apiRequest('GET', `/novels/${novelID}/chapters`);
      setChapters(data || []);
    } catch (e) {
      console.error(e);
    }
  };

  const handleNovelClick = (novel: Novel) => {
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

  const handleCreateNovel = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const payload = {
        ...newNovel,
        rating: Number(newNovel.rating),
        genres: newNovel.genres.split(',').map(g => g.trim()).filter(Boolean),
        coin_cost_per_thousand: Number(newNovel.coin_cost_per_thousand),
        start_pay_chapter_index: Number(newNovel.start_pay_chapter_index)
      };
      await apiRequest('POST', '/novels', payload);
      setIsCreating(false);
      setNewNovel({
        title: '', author: '', cover_url: 'https://images.unsplash.com/photo-1543002588-bfa74002ed7e?w=300',
        status: 'Ongoing', rating: 4.5, synopsis: '', genres: 'Romance, Werewolf',
        coin_cost_per_thousand: 5, start_pay_chapter_index: globalStartPayChapter || 3
      });
      fetchNovels();
    } catch (err: any) {
      window.showToast?.(err.message || '新建书籍失败', 'error');
    }
  };

  const handleSaveGlobalConfig = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await apiRequest('POST', '/settings', {
        global_coin_cost_per_thousand: String(globalCoinCost),
        global_start_pay_chapter_index: String(globalStartPayChapter),
        apply_to_all_novels: applyToAllNovels ? 'true' : 'false'
      });
      window.showToast?.(
        applyToAllNovels 
          ? `全局运营计费配置已保存，并已将全部书籍更新为第 ${globalStartPayChapter} 章起付费！` 
          : '全局运营计费配置保存成功！', 
        'success'
      );
      setIsConfiguringGlobal(false);
      await fetchNovels();
      if (selectedNovel) {
        await fetchChapters(selectedNovel.id);
        const updated = await apiRequest('GET', `/novels/${selectedNovel.id}`);
        if (updated) setSelectedNovel(updated);
      }
    } catch (err: any) {
      window.showToast?.(err.message || '保存全局配置失败', 'error');
    }
  };

  const handleUpdateNovel = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedNovel) return;
    try {
      const payload = {
        title: editNovelForm.title,
        author: editNovelForm.author,
        cover_url: editNovelForm.cover_url,
        status: editNovelForm.status,
        rating: Number(editNovelForm.rating),
        synopsis: editNovelForm.synopsis,
        genres: editNovelForm.genres.split(',').map(g => g.trim()).filter(Boolean),
        coin_cost_per_thousand: Number(editNovelForm.coin_cost_per_thousand),
        start_pay_chapter_index: Number(editNovelForm.start_pay_chapter_index)
      };
      const updated = await apiRequest('PUT', `/novels/${selectedNovel.id}`, payload);
      setSelectedNovel(updated);
      setIsEditing(false);
      fetchNovels();
      fetchChapters(selectedNovel.id);
    } catch (err: any) {
      window.showToast?.(err.message || '更新书籍失败', 'error');
    }
  };

  const handleDeleteNovel = async (id: number, e: React.MouseEvent) => {
    e.stopPropagation();
    window.showConfirm?.('您确定要彻底删除这本小说吗？这将会同时级联删除旗下所有章节目录，此操作不可撤销！', async () => {
      try {
        await apiRequest('DELETE', `/novels/${id}`);
        fetchNovels();
      } catch (err: any) {
        window.showToast?.(err.message, 'error');
      }
    });
  };

  const handleBulkImport = async (e: React.FormEvent) => {
    e.preventDefault();
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
      setImportMessage(`导入成功！共提取并导入了 ${data.chapters_count} 个章节（总字数 ${data.total_word_count} 字）。`);
      setImportFile(null);
      fetchChapters(selectedNovel.id);

      const updatedNovel = await apiRequest('GET', `/novels/${selectedNovel.id}`);
      setSelectedNovel(updatedNovel);
      fetchNovels();
    } catch (err: any) {
      setImportError(err.message || '导入文件解析失败，请检查文件编码（UTF-8）或压缩包格式。');
    } finally {
      setImportLoading(false);
    }
  };

  const handleGeneratePromotionLink = async () => {
    const baseUrl = window.location.origin;
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

    if (promotionForm.coinCostPerThousand) {
      params.set('cost', promotionForm.coinCostPerThousand);
    }
    if (promotionForm.startPayChapterIndex) {
      params.set('pay_ch', promotionForm.startPayChapterIndex);
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
        fb_pixel_id: promotionForm.fbPixelId ? parseInt(promotionForm.fbPixelId, 10) : null,
        recharge_template_id: promotionForm.rechargeTemplateId ? parseInt(promotionForm.rechargeTemplateId, 10) : null,
        coin_cost_per_thousand: promotionForm.coinCostPerThousand ? parseInt(promotionForm.coinCostPerThousand, 10) : null,
        start_pay_chapter_index: promotionForm.startPayChapterIndex ? parseInt(promotionForm.startPayChapterIndex, 10) : null,
      });

      // If link was created with an ID, append link_id to the final URL for accurate tracking
      let resolvedUrl = finalUrl;
      if (created && created.id) {
        params.set('link_id', String(created.id));
        resolvedUrl = `${baseUrl}${path}?${params.toString()}`;
      }
      setGeneratedLink(resolvedUrl);
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
        onGenerateLink={handleGeneratePromotionLink}
        generatedLink={generatedLink}
        onCopyLink={handleCopyLink}
      />
    );
  };

  if (loading && novels.length === 0) {
    return <div style={{ color: 'hsl(var(--text-secondary))', padding: '20px' }}>正在加载书籍工作台...</div>;
  }

  // --- Novel Detail View ---
  if (selectedNovel) {
    return (
      <div className="animate-fade-in" style={{ padding: '16px 24px 24px 24px' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
          <button onClick={() => setSelectedNovel(null)} className="btn-secondary" style={{ display: 'inline-flex', gap: '8px' }}>
            <ArrowLeft size={16} /> 返回书籍列表
          </button>
          <div style={{ display: 'flex', gap: '12px' }}>
            <button onClick={() => {
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
            }} className="btn-secondary" style={{ display: 'inline-flex', gap: '8px' }}>
              <Link2 size={16} /> 生成推广链接
            </button>
            <button onClick={() => {
              setEditNovelForm({
                title: selectedNovel.title,
                author: selectedNovel.author,
                cover_url: selectedNovel.cover_url,
                status: selectedNovel.status,
                rating: selectedNovel.rating,
                synopsis: selectedNovel.synopsis || '',
                genres: selectedNovel.genres.join(', '),
                coin_cost_per_thousand: selectedNovel.coin_cost_per_thousand || 5,
                start_pay_chapter_index: selectedNovel.start_pay_chapter_index || 3
              });
              setIsEditing(true);
            }} className="btn-primary" style={{ display: 'inline-flex', gap: '8px' }}>
              <Edit size={16} /> 编辑书籍与计费
            </button>
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
            <div style={{ display: 'flex', gap: '8px', marginBottom: '12px' }}>
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
                <span style={{ fontSize: '1.2rem', fontWeight: 600, color: 'hsl(var(--primary))' }}>{selectedNovel.coin_cost_per_thousand || 5} 金币</span>
              </div>
              <div>
                <span style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>收费起点</span>
                <span style={{ fontSize: '1.2rem', fontWeight: 600, color: 'hsl(var(--primary))' }}>第 {selectedNovel.start_pay_chapter_index || 3} 章起</span>
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
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>导入类型</label>
                <div style={{ display: 'flex', gap: '16px' }}>
                  <label style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.85rem', cursor: 'pointer' }}>
                    <input type="radio" name="import_type" checked={importType === 'single_txt'} onChange={() => setImportType('single_txt')} /> 单个大 TXT 文件 (正则分章)
                  </label>
                  <label style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.85rem', cursor: 'pointer' }}>
                    <input type="radio" name="import_type" checked={importType === 'zip'} onChange={() => setImportType('zip')} /> ZIP 压缩包 (内含多个 TXT)
                  </label>
                </div>
              </div>

              <div style={{ marginBottom: '16px' }}>
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>选择文件 ({importType === 'single_txt' ? '.txt' : '.zip'})</label>
                <input
                  type="file"
                  accept={importType === 'single_txt' ? '.txt' : '.zip'}
                  onChange={(e) => setImportFile(e.target.files ? e.target.files[0] : null)}
                  required
                  style={{
                    width: '100%',
                    padding: '12px',
                    border: '1px dashed hsl(var(--border))',
                    borderRadius: '8px',
                    backgroundColor: 'hsl(var(--bg-card))',
                    cursor: 'pointer'
                  }}
                />
              </div>

              {importType === 'single_txt' && (
                <div style={{ marginBottom: '16px' }}>
                  <label style={{ display: 'flex', alignItems: 'center', gap: '4px', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                    章节匹配分割正则
                    <span title="正则表达式用来匹配文章分割起点。默认会识别类似 'Chapter 1' 或 '第一章'。编辑可以根据实际手稿格式自定义调整。" style={{ display: 'inline-flex', cursor: 'help' }}>
                      <HelpCircle size={12} />
                    </span>
                  </label>
                  <input
                    type="text"
                    className="input-field"
                    value={regex}
                    onChange={(e) => setRegex(e.target.value)}
                    required
                  />
                </div>
              )}



              <button
                type="submit"
                className="btn-primary"
                disabled={importLoading || !importFile}
                style={{ width: '100%', display: 'flex', gap: '8px' }}
              >
                <Upload size={16} /> {importLoading ? '正在分析手稿写入数据库...' : '导入并分割章节'}
              </button>
            </form>
          </div>
        </div>

        {/* Chapter Preview Modal */}
        {(previewChapter || previewLoading) && (
          <div style={{
            position: 'fixed', top: 0, left: 0, right: 0, bottom: 0,
            backgroundColor: 'rgba(15, 23, 42, 0.4)',
            display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
            padding: '20px'
          }} onClick={() => setPreviewChapter(null)}>
            <div className="glass-panel animate-fade-in" style={{
              width: '100%', maxWidth: '640px', maxHeight: '80vh',
              backgroundColor: 'hsl(var(--bg-surface))', border: '1px solid hsl(var(--border))',
              borderRadius: '16px', display: 'flex', flexDirection: 'column',
              boxShadow: 'var(--shadow-lg)', overflow: 'hidden'
            }} onClick={(e) => e.stopPropagation()}>

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
                  className="btn-secondary"
                  style={{ padding: '6px 12px', fontSize: '0.8rem' }}
                  onClick={() => setPreviewChapter(null)}
                >
                  关闭
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
          </div>
        )}

        {renderPromotionModal()}
        {/* Edit Novel Modal */}
        <NovelFormModal
          isOpen={isEditing}
          isEditing={true}
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
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">书籍管理</h1>
        </div>
        <div style={{ display: 'flex', gap: '12px' }}>
          <button onClick={() => setIsConfiguringGlobal(!isConfiguringGlobal)} className="btn-secondary" style={{ display: 'flex', gap: '6px', alignItems: 'center' }}>
            <Settings size={16} /> 全局计费配置 (千字 {globalCoinCost} 币 · 第 {globalStartPayChapter} 章起)
          </button>
          <button onClick={() => setIsCreating(!isCreating)} className="btn-primary" style={{ display: 'flex', gap: '6px' }}>
            <Plus size={16} /> 新建书籍档案
          </button>
        </div>
      </div>

      {isConfiguringGlobal && (
        <form onSubmit={handleSaveGlobalConfig} className="glass-panel" style={{ padding: '24px', marginBottom: '30px' }}>
          <h3 style={{ marginBottom: '18px', display: 'flex', alignItems: 'center', gap: '8px' }}>
            <Settings size={18} style={{ color: 'hsl(var(--primary))' }} /> 配置全局运营计费与起付章节
          </h3>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '20px', maxWidth: '640px', marginBottom: '16px' }}>
            <div>
              <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                全局千字收费价格 (金币)
              </label>
              <input
                type="number"
                className="input-field"
                min={0}
                value={globalCoinCost}
                onChange={(e) => setGlobalCoinCost(e.target.value === '' ? '' as any : Number(e.target.value))}
                required
              />
            </div>
            <div>
              <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                全局默认起始付费章节 (从第几章起)
              </label>
              <input
                type="number"
                className="input-field"
                min={1}
                value={globalStartPayChapter}
                onChange={(e) => setGlobalStartPayChapter(e.target.value === '' ? '' as any : Number(e.target.value))}
                required
              />
            </div>
          </div>

          <div style={{ marginBottom: '18px', display: 'flex', alignItems: 'center', gap: '8px' }}>
            <input
              type="checkbox"
              id="applyAllNovelsCheckbox"
              checked={applyToAllNovels}
              onChange={(e) => setApplyToAllNovels(e.target.checked)}
              style={{ width: '16px', height: '16px', cursor: 'pointer' }}
            />
            <label htmlFor="applyAllNovelsCheckbox" style={{ fontSize: '0.85rem', color: 'hsl(var(--text-primary))', cursor: 'pointer' }}>
              同步覆盖并应用到全部已有书籍（所有书籍均从第 {globalStartPayChapter} 章开始付费）
            </label>
          </div>

          <div style={{ display: 'flex', gap: '12px' }}>
            <button type="submit" className="btn-primary" style={{ height: '38px' }}>
              保存全局计费配置
            </button>
            <button type="button" className="btn-secondary" style={{ height: '38px' }} onClick={() => setIsConfiguringGlobal(false)}>
              取消
            </button>
          </div>
        </form>
      )}

      <NovelFormModal
        isOpen={isCreating}
        isEditing={false}
        formData={newNovel}
        setFormData={(data) => setNewNovel(data)}
        onSubmit={handleCreateNovel}
        onCancel={() => setIsCreating(false)}
      />

      {/* Search Input Bar */}
      <div style={{ display: 'flex', gap: '16px', marginBottom: '20px' }}>
        <div style={{ position: 'relative', flex: 1 }}>
          <span style={{ position: 'absolute', left: '12px', top: '12px', color: 'hsl(var(--text-muted))' }}>
            <Search size={16} />
          </span>
          <input
            type="text"
            className="input-field"
            style={{ paddingLeft: '38px' }}
            placeholder="输入书名、作者或书籍 ID 进行过滤搜索..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
        </div>
      </div>

      {/* Books List Table */}
      <div className="glass-panel" style={{ padding: '8px', overflowX: 'auto' }}>
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
            {paginatedNovels.length === 0 ? (
              <tr>
                <td colSpan={10} style={{ padding: '20px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>未找到匹配条件的书籍。</td>
              </tr>
            ) : (
              paginatedNovels.map((novel) => (
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
                    <span className={`badge ${novel.status === 'Completed' ? 'badge-green' : 'badge-orange'}`} style={{ fontSize: '0.7rem' }}>
                      {novel.status === 'Completed' ? '已完结' : '连载中'}
                    </span>
                  </td>
                  <td style={{ padding: '14px 12px', color: 'hsl(var(--text-secondary))', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {novel.word_count.toLocaleString()} 字
                  </td>
                  <td style={{ padding: '14px 12px', color: 'hsl(var(--text-secondary))', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {novel.coin_cost_per_thousand ? `${novel.coin_cost_per_thousand} 金币` : '全局配置'}
                  </td>
                  <td style={{ padding: '14px 12px' }}>
                    <span className="badge badge-violet" style={{ fontSize: '0.7rem' }}>{novel.rating} ★</span>
                  </td>
                  <td style={{ padding: '14px 12px', textAlign: 'right' }} onClick={(e) => e.stopPropagation()}>
                    <div style={{ display: 'flex', gap: '6px', justifyContent: 'flex-end' }}>
                      <button
                        className="btn-secondary"
                        style={{ padding: '6px', borderRadius: '6px' }}
                        title="生成推广链接"
                        onClick={() => {
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
                      <button className="btn-secondary" style={{ padding: '6px', borderRadius: '6px' }} title="进入详情/导入" onClick={() => handleNovelClick(novel)}>
                        <Edit size={14} />
                      </button>
                      <button
                        className="btn-secondary"
                        onClick={(e) => handleDeleteNovel(novel.id, e)}
                        style={{ padding: '6px', borderRadius: '6px', color: '#f87171', borderColor: 'rgba(239, 68, 68, 0.2)' }}
                        title="删除书籍"
                      >
                        <Trash size={14} />
                      </button>
                    </div>
                  </td>
                </tr>
              ))
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
            显示第 {startIndex + 1} 至 {Math.min(startIndex + pageSize, filteredNovels.length)} 项，共 {filteredNovels.length} 项
          </div>
          <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
            <button
              className="btn-secondary"
              onClick={() => setCurrentPage(p => Math.max(1, p - 1))}
              disabled={currentPage === 1}
              style={{ padding: '6px 12px', fontSize: '0.8rem', borderRadius: '6px' }}
            >
              上一页
            </button>
            <span style={{ display: 'flex', alignItems: 'center', fontSize: '0.85rem', padding: '0 8px' }}>
              第 {currentPage} 页 / 共 {totalPages} 页
            </span>
            <button
              className="btn-secondary"
              onClick={() => setCurrentPage(p => Math.min(totalPages, p + 1))}
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

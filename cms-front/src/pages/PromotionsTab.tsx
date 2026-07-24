import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { apiRequest } from '../utils/api';
import { Trash, Clipboard, Search, Edit2, X } from 'lucide-react';
import CustomSelect from '../components/CustomSelect';

interface PromotionLink {
  id: number;
  name: string;
  novel_id: number;
  novel_title: string;
  chapter_index: number;
  utm_source: string;
  utm_campaign: string;
  generated_url: string;
  fb_pixel_id?: number | null;
  recharge_template_id?: number | null;
  created_at: string;
}

interface FBPixel {
  id: number;
  name: string;
  pixel_id: string;
}

interface RechargeTemplate {
  id: number;
  name: string;
  is_default: boolean;
}

export default function PromotionsTab() {
  const [links, setLinks] = useState<PromotionLink[]>([]);
  const [pixels, setPixels] = useState<FBPixel[]>([]);
  const [templates, setTemplates] = useState<RechargeTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchQuery, setSearchQuery] = useState('');
  const [sourceFilter, setSourceFilter] = useState('');

  const [colWidths, setColWidths] = useState<number[]>([
    150, // 创建时间
    140, // 推广名称
    150, // 小说书名
    100, // 渠道 (Source)
    120, // 绑定像素
    120, // 绑定模板
    120, // 活动 (Campaign)
    90,  // 落地页
    180, // 推广地址
    140  // 操作
  ]);

  const columns = [
    { label: '创建时间', key: 'created_at' },
    { label: '推广名称', key: 'name' },
    { label: '小说书名', key: 'novel_title' },
    { label: '投放渠道', key: 'utm_source' },
    { label: '绑定像素', key: 'fb_pixel_id' },
    { label: '绑定模板', key: 'recharge_template_id' },
    { label: '广告名称', key: 'utm_campaign' },
    { label: '落地页', key: 'chapter_index' },
    { label: '推广地址', key: 'generated_url' },
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

  const [tooltip, setTooltip] = useState<{
    text: string;
    x: number;
    y: number;
    visible: boolean;
  }>({ text: '', x: 0, y: 0, visible: false });
  const [hideTimeout, setHideTimeout] = useState<number | null>(null);

  const showTooltip = (text: string, e: React.MouseEvent) => {
    if (hideTimeout) {
      window.clearTimeout(hideTimeout);
      setHideTimeout(null);
    }
    const rect = e.currentTarget.getBoundingClientRect();
    setTooltip({
      text,
      x: rect.left + rect.width / 2,
      y: rect.top - 8,
      visible: true
    });
  };

  const hideTooltip = () => {
    const timeout = window.setTimeout(() => {
      setTooltip(prev => ({ ...prev, visible: false }));
    }, 300);
    setHideTimeout(timeout);
  };

  const [isEditModalOpen, setIsEditModalOpen] = useState(false);
  const [editingLink, setEditingLink] = useState<PromotionLink | null>(null);
  const [editForm, setEditForm] = useState({
    name: '',
    chapterIndex: '1',
    utmSource: 'facebook',
    utmCampaign: '',
    fbPixelId: '',
    rechargeTemplateId: ''
  });

  const fetchLinks = async () => {
    try {
      const data = await apiRequest('GET', '/promotion-links');
      setLinks(data || []);
    } catch (err: any) {
      window.showToast?.('加载推广链接失败: ' + err.message, 'error');
    } finally {
      setLoading(false);
    }
  };

  const fetchPixels = async () => {
    try {
      const data = await apiRequest('GET', '/fb-pixels');
      setPixels(data || []);
    } catch (e) {
      console.error("加载像素列表失败:", e);
    }
  };

  const fetchTemplates = async () => {
    try {
      const data = await apiRequest('GET', '/recharge-templates');
      setTemplates(data || []);
    } catch (e) {
      console.error("加载模板列表失败:", e);
    }
  };

  useEffect(() => {
    fetchLinks();
    fetchPixels();
    fetchTemplates();
  }, []);

  const handleEditClick = (link: PromotionLink) => {
    setEditingLink(link);
    setEditForm({
      name: link.name || '',
      chapterIndex: link.chapter_index > 0 ? String(link.chapter_index) : '',
      utmSource: link.utm_source || 'facebook',
      utmCampaign: link.utm_campaign || '',
      fbPixelId: link.fb_pixel_id ? String(link.fb_pixel_id) : '',
      rechargeTemplateId: link.recharge_template_id ? String(link.recharge_template_id) : ''
    });
    setIsEditModalOpen(true);
  };

  const handleEditSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editingLink) return;

    const baseUrl = 'http://localhost:5173';
    const params = new URLSearchParams();
    params.set('novel_id', String(editingLink.novel_id));
    if (editForm.chapterIndex) {
      params.set('chapter_index', editForm.chapterIndex);
    }
    if (editForm.utmSource) {
      params.set('utm_source', editForm.utmSource);
    }
    if (editForm.utmCampaign) {
      params.set('utm_campaign', editForm.utmCampaign);
    }

    const targetPixel = pixels.find(p => p.id === parseInt(editForm.fbPixelId, 10));
    if (targetPixel) {
      params.set('pixel_id', targetPixel.pixel_id);
    }

    if (editForm.rechargeTemplateId) {
      params.set('template_id', editForm.rechargeTemplateId);
    }

    const finalUrl = `${baseUrl}/?${params.toString()}`;

    try {
      await apiRequest('PUT', `/promotion-links/${editingLink.id}`, {
        name: editForm.name,
        chapter_index: editForm.chapterIndex ? parseInt(editForm.chapterIndex, 10) : 0,
        utm_source: editForm.utmSource,
        utm_campaign: editForm.utmCampaign,
        generated_url: finalUrl,
        fb_pixel_id: editForm.fbPixelId ? parseInt(editForm.fbPixelId, 10) : null,
        recharge_template_id: editForm.rechargeTemplateId ? parseInt(editForm.rechargeTemplateId, 10) : null
      });

      window.showToast?.("修改推广链接成功！", "success");
      setIsEditModalOpen(false);
      fetchLinks();
    } catch (err: any) {
      window.showToast?.("修改推广链接失败：" + err.message, "error");
    }
  };

  const handleDelete = async (id: number) => {
    window.showConfirm?.('确定要删除这条推广链接记录吗？此操作无法撤销。', async () => {
      try {
        await apiRequest('DELETE', `/promotion-links/${id}`);
        setLinks(links.filter(l => l.id !== id));
        window.showToast?.('删除成功！', 'success');
      } catch (err: any) {
        window.showToast?.('删除失败: ' + err.message, 'error');
      }
    });
  };

  const handleCopy = (url: string) => {
    navigator.clipboard.writeText(url);
    window.showToast?.('推广链接已复制到剪贴板！', 'success');
  };

  // Filtered links
  const filteredLinks = links.filter(link => {
    const matchesSearch = link.novel_title.toLowerCase().includes(searchQuery.toLowerCase()) ||
      link.utm_campaign.toLowerCase().includes(searchQuery.toLowerCase()) ||
      (link.name && link.name.toLowerCase().includes(searchQuery.toLowerCase()));
    const matchesSource = !sourceFilter || link.utm_source.toLowerCase() === sourceFilter.toLowerCase();
    return matchesSearch && matchesSource;
  });

  // Extract unique sources for filter dropdown
  const uniqueSources = Array.from(new Set(links.map(l => l.utm_source).filter(Boolean)));

  if (loading) {
    return <div style={{ color: 'hsl(var(--text-secondary))', padding: '20px' }}>正在加载推广链接数据...</div>;
  }

  return (
    <div className="animate-fade-in" style={{ padding: '16px 24px 24px 24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <h1 style={{ fontSize: '1.5rem', fontWeight: 700, display: 'flex', alignItems: 'center', gap: '8px' }}>
          <span className="gradient-text">推广链接管理</span>
        </h1>
      </div>

      {/* Filters Area */}
      <div className="glass-panel" style={{ position: 'relative', zIndex: 10, padding: '16px', marginBottom: '24px', display: 'flex', gap: '16px', flexWrap: 'wrap', alignItems: 'center' }}>
        <div style={{ flex: 1, minWidth: '240px', position: 'relative' }}>
          <span style={{ position: 'absolute', left: '12px', top: '50%', transform: 'translateY(-50%)', color: 'hsl(var(--text-muted))', display: 'flex' }}>
            <Search size={16} />
          </span>
          <input
            type="text"
            className="input-field"
            style={{ paddingLeft: '36px' }}
            placeholder="搜索推广名称、书名或活动 (utm_campaign)..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
        </div>

        <div style={{ width: '180px' }}>
          <CustomSelect
            options={[
              { value: '', label: '全部广告渠道' },
              ...uniqueSources.map(src => ({ value: src, label: src }))
            ]}
            value={sourceFilter}
            onChange={setSourceFilter}
            width="180px"
            placeholder="全部广告渠道"
          />
        </div>
      </div>

      {/* List Table */}
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
            {filteredLinks.length === 0 ? (
              <tr>
                <td colSpan={10} style={{ padding: '20px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>
                  没有找到匹配的推广链接记录。
                </td>
              </tr>
            ) : (
              filteredLinks.map((link) => (
                <tr
                  key={link.id}
                  style={{ borderBottom: '1px solid hsl(var(--border) / 0.5)', fontSize: '0.9rem', transition: 'background-color 0.2s', color: 'hsl(var(--text-secondary))' }}
                  onMouseEnter={(e) => e.currentTarget.style.backgroundColor = 'hsl(var(--bg-card))'}
                  onMouseLeave={(e) => e.currentTarget.style.backgroundColor = 'transparent'}
                >
                  <td style={{ padding: '14px 12px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={new Date(link.created_at).toLocaleString('zh-CN', { hour12: false })}>
                    {new Date(link.created_at).toLocaleString('zh-CN', { hour12: false })}
                  </td>
                  <td
                    style={{ padding: '14px 12px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}
                    onMouseEnter={(e) => showTooltip(link.name || '未命名', e)}
                    onMouseLeave={hideTooltip}
                  >
                    {link.name || <span style={{ color: 'hsl(var(--text-muted))' }}>未命名</span>}
                  </td>
                  <td style={{ padding: '14px 12px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={link.novel_title}>
                    {link.novel_title}
                  </td>
                  <td style={{ padding: '14px 12px' }}>
                    <span className="badge badge-blue">{link.utm_source}</span>
                  </td>
                  <td style={{ padding: '14px 12px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={link.fb_pixel_id ? (pixels.find(p => p.id === link.fb_pixel_id)?.name || `ID: ${link.fb_pixel_id}`) : '-'}>
                    {link.fb_pixel_id ? (
                      pixels.find(p => p.id === link.fb_pixel_id)?.name || `ID: ${link.fb_pixel_id}`
                    ) : (
                      <span style={{ color: 'hsl(var(--text-muted))' }}>-</span>
                    )}
                  </td>
                  <td style={{ padding: '14px 12px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={link.recharge_template_id ? (templates.find(t => t.id === link.recharge_template_id)?.name || `ID: ${link.recharge_template_id}`) : '-'}>
                    {link.recharge_template_id ? (
                      templates.find(t => t.id === link.recharge_template_id)?.name || `ID: ${link.recharge_template_id}`
                    ) : (
                      <span style={{ color: 'hsl(var(--text-muted))' }}>-</span>
                    )}
                  </td>
                  <td style={{ padding: '14px 12px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={link.utm_campaign || '-'}>
                    {link.utm_campaign || <span style={{ color: 'hsl(var(--text-muted))' }}>-</span>}
                  </td>
                  <td style={{ padding: '14px 12px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {link.chapter_index > 0 ? `第 ${link.chapter_index} 章` : '书籍详情页'}
                  </td>
                  <td
                    style={{ padding: '14px 12px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}
                    onMouseEnter={(e) => showTooltip(link.generated_url, e)}
                    onMouseLeave={hideTooltip}
                  >
                    <code style={{ fontSize: '0.75rem', color: 'hsl(var(--primary))' }}>{link.generated_url}</code>
                  </td>
                  <td style={{ padding: '14px 12px', textAlign: 'right' }} onClick={(e) => e.stopPropagation()}>
                    <div style={{ display: 'flex', gap: '6px', justifyContent: 'flex-end' }}>
                      <button
                        className="btn-secondary"
                        style={{ padding: '6px', borderRadius: '6px' }}
                        title="编辑推广链接"
                        onClick={() => handleEditClick(link)}
                      >
                        <Edit2 size={14} />
                      </button>
                      <button
                        className="btn-secondary"
                        style={{ padding: '6px', borderRadius: '6px' }}
                        title="复制推广链接"
                        onClick={() => handleCopy(link.generated_url)}
                      >
                        <Clipboard size={14} />
                      </button>
                      <button
                        className="btn-secondary"
                        style={{ padding: '6px', borderRadius: '6px', color: '#f87171', borderColor: 'rgba(239, 68, 68, 0.2)' }}
                        title="删除推广链接"
                        onClick={() => handleDelete(link.id)}
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

      {isEditModalOpen && editingLink && createPortal(
        <div style={{
          position: 'fixed',
          top: 0,
          left: 0,
          right: 0,
          bottom: 0,
          backgroundColor: 'rgba(15, 23, 42, 0.4)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          zIndex: 1000,
          padding: '20px'
        }} onClick={() => setIsEditModalOpen(false)}>
          <div className="glass-panel animate-scale-in" style={{
            width: '100%',
            maxWidth: '520px',
            padding: '24px',
            position: 'relative',
            backgroundColor: 'hsl(var(--bg-surface))',
            border: '1px solid hsl(var(--border))',
            borderRadius: '16px',
            boxShadow: 'var(--shadow-lg)',
            overflow: 'hidden'
          }} onClick={(e) => e.stopPropagation()}>
            <button
              type="button"
              onClick={() => setIsEditModalOpen(false)}
              style={{ position: 'absolute', top: '16px', right: '16px', background: 'transparent', border: 'none', color: 'hsl(var(--text-secondary))', cursor: 'pointer' }}
            >
              <X size={18} />
            </button>
            <h3 style={{ fontSize: '1.25rem', fontWeight: 600, marginBottom: '20px' }}>编辑推广链接</h3>

            <form onSubmit={handleEditSubmit} style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>推广名称</label>
                <input
                  type="text"
                  className="input-field"
                  value={editForm.name}
                  onChange={(e) => setEditForm({ ...editForm, name: e.target.value })}
                  required
                />
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>推广书籍</label>
                <input type="text" className="input-field" disabled value={editingLink.novel_title} />
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>落地章节索引 (可选)</label>
                <input
                  type="number"
                  className="input-field"
                  value={editForm.chapterIndex}
                  onChange={(e) => setEditForm({ ...editForm, chapterIndex: e.target.value })}
                />
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>广告渠道 (utm_source)</label>
                <CustomSelect
                  options={[{ value: 'facebook', label: 'Facebook' }]}
                  value={editForm.utmSource}
                  onChange={(val) => setEditForm({ ...editForm, utmSource: val })}
                  width="100%"
                />
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>选择 Facebook 像素</label>
                <CustomSelect
                  options={[
                    { value: '', label: '-- 选择绑定的像素 (可选) --' },
                    ...pixels.map(p => ({ value: String(p.id), label: `${p.name} (${p.pixel_id})` }))
                  ]}
                  value={editForm.fbPixelId}
                  onChange={(val) => setEditForm({ ...editForm, fbPixelId: val })}
                  width="100%"
                />
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>广告活动名称 (utm_campaign)</label>
                <input
                  type="text"
                  className="input-field"
                  value={editForm.utmCampaign}
                  onChange={(e) => setEditForm({ ...editForm, utmCampaign: e.target.value })}
                />
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>绑定充值模板 (可选)</label>
                <CustomSelect
                  options={[
                    { value: '', label: '-- 使用默认激活模板 --' },
                    ...templates.map(t => ({ value: String(t.id), label: `${t.name} ${t.is_default ? '(默认)' : ''}` }))
                  ]}
                  value={editForm.rechargeTemplateId}
                  onChange={(val) => setEditForm({ ...editForm, rechargeTemplateId: val })}
                  width="100%"
                />
              </div>

              <div style={{ display: 'flex', gap: '12px', marginTop: '8px' }}>
                <button type="button" className="btn-secondary" style={{ flex: 1 }} onClick={() => setIsEditModalOpen(false)}>
                  取消
                </button>
                <button type="submit" className="btn-primary" style={{ flex: 1.5 }}>
                  确认保存
                </button>
              </div>
            </form>
          </div>
        </div>,
        document.body
      )}

      {tooltip.visible && createPortal(
        <div
          onMouseEnter={() => {
            if (hideTimeout) {
              window.clearTimeout(hideTimeout);
              setHideTimeout(null);
            }
          }}
          onMouseLeave={hideTooltip}
          style={{
            position: 'fixed',
            left: tooltip.x,
            top: tooltip.y,
            transform: 'translate(-50%, -100%)',
            backgroundColor: '#1e293b',
            color: '#ffffff',
            border: 'none',
            borderRadius: '6px',
            padding: '6px 12px',
            boxShadow: 'var(--shadow-md)',
            fontSize: '0.8rem',
            zIndex: 9999,
            pointerEvents: 'auto',
            maxWidth: '360px',
            wordBreak: 'break-all',
            whiteSpace: 'normal',
            boxSizing: 'border-box',
            textAlign: 'center'
          }}
        >
          {tooltip.text}
          {/* Downward pointing arrow */}
          <div style={{
            position: 'absolute',
            top: '100%',
            left: '50%',
            transform: 'translateX(-50%)',
            width: 0,
            height: 0,
            borderLeft: '6px solid transparent',
            borderRight: '6px solid transparent',
            borderTop: '6px solid #1e293b'
          }} />
        </div>,
        document.body
      )}
    </div>
  );
}

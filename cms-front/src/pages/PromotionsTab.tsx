import { useEffect, useMemo, useState } from 'react';
import { apiRequest, domainApi } from '../utils/api';
import type { SystemDomain } from '../utils/api';
import { copyTextToClipboard } from '../utils/clipboard';
import { canWritePromotions, validateAndBuildPromotionPayload } from '../utils/promotion';
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
  const canWrite = canWritePromotions();
  const [links, setLinks] = useState<PromotionLink[]>([]);
  const [pixels, setPixels] = useState<FBPixel[]>([]);
  const [templates, setTemplates] = useState<RechargeTemplate[]>([]);
  const [domains, setDomains] = useState<SystemDomain[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);

  const [searchQuery, setSearchQuery] = useState('');
  const [sourceFilter, setSourceFilter] = useState('');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(15);
  const [density, setDensity] = useState<TableDensity>('comfortable');
  const [showCreatedAt, setShowCreatedAt] = useState(true);

  const [isEditModalOpen, setIsEditModalOpen] = useState(false);
  const [editingLink, setEditingLink] = useState<PromotionLink | null>(null);
  const [editSubmitting, setEditSubmitting] = useState(false);
  const [editForm, setEditForm] = useState({
    name: '',
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
    void fetchLinks();
    void apiRequest('GET', '/fb-pixels').then((d) => setPixels(d || [])).catch(() => {});
    void apiRequest('GET', '/recharge-templates').then((d) => setTemplates(d || [])).catch(() => {});
    void domainApi.getDomains().then((d) => setDomains(d || [])).catch(() => {});
  }, []);

  const handleEditClick = (link: PromotionLink) => {
    setEditingLink(link);
    setEditForm({
      name: link.name || '',
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
      return matchesSearch && matchesSource;
    });
  }, [links, searchQuery, sourceFilter]);

  const totalPages = Math.max(1, Math.ceil(filteredLinks.length / pageSize));
  const safePage = Math.min(page, totalPages);
  const paginatedLinks = filteredLinks.slice((safePage - 1) * pageSize, safePage * pageSize);

  const uniqueSources = Array.from(new Set(links.map((l) => l.utm_source).filter(Boolean)));
  const activeDomains = (domains || []).filter((d) => d.status === 1);
  const defaultDomain =
    activeDomains.find((d) => d.is_default) || activeDomains.find((d) => d.type === 'main') || activeDomains[0];

  const resetFilters = () => {
    setSearchQuery('');
    setSourceFilter('');
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
    return chips;
  }, [searchQuery, sourceFilter]);

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

            {(searchQuery || sourceFilter) && (
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
                      <div className="text-[var(--cms-fg)] truncate max-w-[180px]" title={link.name}>
                        {link.name || <span className="text-[var(--cms-muted-fg)]">未命名 #{link.id}</span>}
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

      {/* Edit Promotion Link Slide-Over Drawer */}
      <CmsDialog
        open={Boolean(isEditModalOpen && editingLink)}
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

            <label className="block space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">绑定充值模板（可选）</span>
              <CustomSelect
                aria-label="绑定充值模板"
                width="100%"
                value={editForm.rechargeTemplateId}
                onChange={(val) => setEditForm({ ...editForm, rechargeTemplateId: val })}
                options={[
                  { value: '', label: '-- 使用默认激活模板 --' },
                  ...templates.map((t) => ({ value: String(t.id), label: `${t.name} ${t.is_default ? '(默认)' : ''}` })),
                ]}
              />
            </label>
          </form>
        )}
      </CmsDialog>
    </div>
  );
}

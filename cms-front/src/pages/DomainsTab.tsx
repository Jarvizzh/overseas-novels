import { useEffect, useMemo, useState } from 'react';
import { ExternalLink, Plus, RefreshCw } from 'lucide-react';
import { domainApi } from '../utils/api';
import type { SystemDomain } from '../utils/api';
import { normalizeDomainHref } from '../utils/promotion';
import CustomSelect from '../components/CustomSelect';
import { CmsDialog } from '../components/ui/CmsDialog';
import {
  type FilterChip,
  FilterChipsBar,
  RowActionMenu,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../components/ui/Primitives';

export default function DomainsTab() {
  const [domains, setDomains] = useState<SystemDomain[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [typeFilter, setTypeFilter] = useState<string>('all');
  const [density, setDensity] = useState<TableDensity>('comfortable');

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [formData, setFormData] = useState<{
    name: string;
    domain: string;
    type: 'main' | 'sub';
    is_default: boolean;
  }>({
    name: '',
    domain: '',
    type: 'sub',
    is_default: false,
  });

  const fetchDomains = async () => {
    setLoading(true);
    try {
      const data = await domainApi.getDomains();
      setDomains(data || []);
      setHasLoadedOnce(true);
    } catch (err: unknown) {
      window.showToast?.(`获取域名列表失败: ${err instanceof Error ? err.message : ''}`, 'error');
      setHasLoadedOnce(true);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchDomains();
  }, []);

  const handleCreateSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    if (!formData.name.trim() || !formData.domain.trim()) {
      window.showToast?.('请填写完整的域名信息', 'error');
      return;
    }

    setSubmitting(true);
    try {
      await domainApi.createDomain(formData);
      window.showToast?.('域名新建成功！', 'success');
      setIsModalOpen(false);
      setFormData({ name: '', domain: '', type: 'sub', is_default: false });
      void fetchDomains();
    } catch (err: unknown) {
      window.showToast?.(`新建域名失败: ${err instanceof Error ? err.message : ''}`, 'error');
    } finally {
      setSubmitting(false);
    }
  };

  const handleCloseModal = () => {
    if (submitting) return;
    setIsModalOpen(false);
    setFormData({ name: '', domain: '', type: 'sub', is_default: false });
  };

  const handleToggleStatus = async (domain: SystemDomain) => {
    const nextStatus = domain.status === 1 ? 2 : 1;
    try {
      await domainApi.updateDomainStatus(domain.id, nextStatus);
      window.showToast?.(`已${nextStatus === 1 ? '启用' : '禁用'}域名 ${domain.domain}`, 'success');
      void fetchDomains();
    } catch (err: unknown) {
      window.showToast?.(`操作失败: ${err instanceof Error ? err.message : ''}`, 'error');
    }
  };

  const handleSetDefault = async (domain: SystemDomain) => {
    try {
      await domainApi.setDefaultDomain(domain.id);
      window.showToast?.(`已将 ${domain.domain} 设为默认主域名`, 'success');
      void fetchDomains();
    } catch (err: unknown) {
      window.showToast?.(`设置默认失败: ${err instanceof Error ? err.message : ''}`, 'error');
    }
  };

  const handleDelete = (domain: SystemDomain) => {
    if (domain.is_default) {
      window.showToast?.('默认域名不可删除', 'error');
      return;
    }
    window.showConfirm?.(`确定要删除域名「${domain.name}」(${domain.domain}) 吗？`, async () => {
      try {
        await domainApi.deleteDomain(domain.id);
        window.showToast?.('域名删除成功', 'success');
        void fetchDomains();
      } catch (err: unknown) {
        window.showToast?.(`删除失败: ${err instanceof Error ? err.message : ''}`, 'error');
      }
    });
  };

  const filteredDomains = useMemo(() => {
    const q = searchQuery.toLowerCase();
    return domains.filter((d) => {
      const matchesSearch = d.name.toLowerCase().includes(q) || d.domain.toLowerCase().includes(q);
      const matchesType = typeFilter === 'all' || d.type === typeFilter;
      return matchesSearch && matchesType;
    });
  }, [domains, searchQuery, typeFilter]);

  const totalCount = domains.length;
  const mainCount = domains.filter((d) => d.type === 'main').length;
  const activeSubCount = domains.filter((d) => d.type === 'sub' && d.status === 1).length;

  const filterChips = useMemo<FilterChip[]>(() => {
    const chips: FilterChip[] = [];
    if (searchQuery) {
      chips.push({ key: 'q', label: `搜索: ${searchQuery}`, onClear: () => setSearchQuery('') });
    }
    if (typeFilter !== 'all') {
      chips.push({
        key: 'type',
        label: `类型: ${typeFilter === 'main' ? '主域名' : '子域名'}`,
        onClear: () => setTypeFilter('all'),
      });
    }
    return chips;
  }, [searchQuery, typeFilter]);

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Unified Borderless Filter Strip */}
      <search className="block space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2.5">
          <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
            <SearchInput
              className="w-full sm:w-80"
              ariaLabel="搜索域名"
              placeholder="搜索域名描述或地址..."
              value={searchQuery}
              onChange={setSearchQuery}
            />
            <CustomSelect
              aria-label="筛选域名类型"
              options={[
                { value: 'all', label: '全部类型' },
                { value: 'main', label: '主域名' },
                { value: 'sub', label: '子域名' },
              ]}
              value={typeFilter}
              onChange={setTypeFilter}
              width="140px"
            />
          </div>

          <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
            <span className="tabular-nums">
              共 {filteredDomains.length}/{totalCount} 个 · 主域名 {mainCount} · 活跃子域名 {activeSubCount}
            </span>
            <ViewOptionsPopover density={density} onDensityChange={setDensity} />
            <button type="button" onClick={() => void fetchDomains()} className="btn-secondary h-8 px-3 text-xs">
              <RefreshCw size={13} className={loading ? 'animate-spin' : ''} aria-hidden="true" />
              <span>刷新</span>
            </button>
            <button type="button" onClick={() => setIsModalOpen(true)} className="btn-primary h-8 px-3.5 text-xs">
              <Plus size={14} aria-hidden="true" />
              <span>新建域名</span>
            </button>
          </div>
        </div>

        <FilterChipsBar
          chips={filterChips}
          onResetAll={() => {
            setSearchQuery('');
            setTypeFilter('all');
          }}
        />
      </search>

      {/* Domains Table */}
      <TableRegion ariaLabel="系统域名列表">
        <table className="cms-table min-w-[840px]" data-density={density}>
          <thead>
            <tr>
              <th scope="col">ID</th>
              <th scope="col">域名说明</th>
              <th scope="col">域名地址 (URL)</th>
              <th scope="col">类型</th>
              <th scope="col">默认入口</th>
              <th scope="col">状态</th>
              <th scope="col">创建时间</th>
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={5} cols={8} />
            ) : filteredDomains.length === 0 ? (
              <tr>
                <td colSpan={8} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  暂无匹配的域名配置
                </td>
              </tr>
            ) : (
              filteredDomains.map((item) => (
                <tr key={item.id}>
                  <td className="tabular-nums text-[var(--cms-muted-fg)]">#{item.id}</td>
                  <td className="text-[var(--cms-fg)]">{item.name}</td>
                  <td>
                    <a
                      href={normalizeDomainHref(item.domain)}
                      target="_blank"
                      rel="noreferrer"
                      className="inline-flex items-center gap-1.5 text-[var(--cms-fg)] hover:underline"
                    >
                      <span>{item.domain}</span>
                      <ExternalLink size={12} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />
                    </a>
                  </td>
                  <td className={item.type === 'main' ? 'text-[var(--cms-fg)]' : 'text-[var(--cms-muted-fg)]'}>
                    {item.type === 'main' ? '主域名' : '子域名'}
                  </td>
                  <td>
                    {item.is_default ? (
                      <StatusDot tone="primary" label="默认落地页" />
                    ) : (
                      <span className="text-[var(--cms-muted-fg)]">-</span>
                    )}
                  </td>
                  <td>
                    <StatusDot
                      tone={item.status === 1 ? 'success' : 'muted'}
                      label={item.status === 1 ? '正常启用' : '已禁用'}
                    />
                  </td>
                  <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                    {new Date(item.created_at).toLocaleString()}
                  </td>
                  <td className="text-right whitespace-nowrap">
                    <div className="inline-flex items-center justify-end gap-0.5 whitespace-nowrap">
                      <button
                        type="button"
                        onClick={() => void handleToggleStatus(item)}
                        className="btn-ghost whitespace-nowrap"
                      >
                        {item.status === 1 ? '禁用' : '启用'}
                      </button>
                      {!item.is_default && (
                        <RowActionMenu
                          ariaLabel={`域名 ${item.domain} 更多操作`}
                          items={[
                            {
                              label: '设为默认落地页',
                              onClick: () => void handleSetDefault(item),
                            },
                            {
                              label: '删除域名',
                              tone: 'danger' as const,
                              dividerBefore: true,
                              onClick: () => handleDelete(item),
                            },
                          ]}
                        />
                      )}
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableRegion>

      <CmsDialog
        open={isModalOpen}
        onClose={handleCloseModal}
        closedBy={submitting ? 'none' : 'any'}
        closeDisabled={submitting}
        variant="drawer"
        title="新建域名 / 子域名"
        size="md"
        footer={
          <>
            <button type="button" onClick={handleCloseModal} disabled={submitting} className="btn-secondary">
              取消
            </button>
            <button type="submit" form="domain-create-form" disabled={submitting} className="btn-primary">
              {submitting ? '创建中...' : '提交创建'}
            </button>
          </>
        }
      >
        <form id="domain-create-form" onSubmit={handleCreateSubmit} className="space-y-4 text-xs">
          <label className="block space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">
              域名描述名称 <span className="text-[var(--cms-error)]">*</span>
            </span>
            <input
              type="text"
              autoFocus
              className="input-field"
              placeholder="如: Facebook引流专用子域名A"
              value={formData.name}
              onChange={(e) => setFormData({ ...formData, name: e.target.value })}
              required
            />
          </label>

          <label className="block space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">
              域名地址 (Domain String) <span className="text-[var(--cms-error)]">*</span>
            </span>
            <input
              type="text"
              className="input-field font-mono-code"
              placeholder="如: sub1.star-novel.com 或 star-novel.com"
              value={formData.domain}
              onChange={(e) => setFormData({ ...formData, domain: e.target.value })}
              required
            />
            <span className="block text-[var(--cms-muted-fg)]">无需包含 http:// 或 https:// 前缀</span>
          </label>

          <div className="space-y-2">
            <span className="block font-medium text-[var(--cms-muted-fg)]">域名类型</span>
            <div className="flex items-center gap-5">
              <label className="inline-flex items-center gap-2 cursor-pointer text-[var(--cms-fg)]">
                <input
                  type="radio"
                  name="domain_type"
                  checked={formData.type === 'sub'}
                  onChange={() => setFormData({ ...formData, type: 'sub' })}
                />
                <span>子域名 (Subdomain)</span>
              </label>
              <label className="inline-flex items-center gap-2 cursor-pointer text-[var(--cms-fg)]">
                <input
                  type="radio"
                  name="domain_type"
                  checked={formData.type === 'main'}
                  onChange={() => setFormData({ ...formData, type: 'main' })}
                />
                <span>主域名 (Main Domain)</span>
              </label>
            </div>
          </div>

          <label className="flex items-center gap-2.5 p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] cursor-pointer text-[var(--cms-fg)]">
            <input
              type="checkbox"
              checked={formData.is_default}
              onChange={(e) => setFormData({ ...formData, is_default: e.target.checked })}
              className="w-4 h-4 cursor-pointer"
            />
            <span>设为系统默认落地页入口域名</span>
          </label>
        </form>
      </CmsDialog>
    </div>
  );
}

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { feedbackApi } from '../utils/api';
import type { FeedbackItem } from '../utils/api';
import CustomSelect from '../components/CustomSelect';
import { CmsDialog } from '../components/ui/CmsDialog';
import { Pagination } from '../components/ui/Pagination';
import {
  type FilterChip,
  FilterChipsBar,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../components/ui/Primitives';

const statusOptions = [
  { value: '', label: '全部状态' },
  { value: 'pending', label: '待处理 (Pending)' },
  { value: 'replied', label: '已回复 (Replied)' },
  { value: 'resolved', label: '已解决 (Resolved)' },
];

const modalStatusOptions = [
  { value: 'pending', label: '待处理 (Pending)' },
  { value: 'replied', label: '已回复 (Replied)' },
  { value: 'resolved', label: '已解决 (Resolved)' },
];

export default function FeedbackTab() {
  const [items, setItems] = useState<FeedbackItem[]>([]);
  const [total, setTotal] = useState(0);
  const [keyword, setKeyword] = useState('');
  const [queryState, setQueryState] = useState({
    page: 1,
    pageSize: 15,
    statusFilter: '',
    keyword: '',
    nonce: 0,
  });
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [density, setDensity] = useState<TableDensity>('comfortable');
  const fetchSeqRef = useRef(0);

  const [selectedItem, setSelectedItem] = useState<FeedbackItem | null>(null);
  const [editStatus, setEditStatus] = useState<string>('pending');
  const [adminReply, setAdminReply] = useState<string>('');
  const [saving, setSaving] = useState(false);

  const fetchList = useCallback(async () => {
    const seq = ++fetchSeqRef.current;
    setLoading(true);
    try {
      const data = await feedbackApi.getFeedbackList({
        page: queryState.page,
        limit: queryState.pageSize,
        status: queryState.statusFilter,
        keyword: queryState.keyword,
      });
      if (seq !== fetchSeqRef.current) return;
      setItems(data.items || []);
      setTotal(data.total || 0);
      setHasLoadedOnce(true);
    } catch (e: unknown) {
      if (seq !== fetchSeqRef.current) return;
      window.showToast?.((e instanceof Error ? e.message : '') || '获取反馈列表失败', 'error');
      setHasLoadedOnce(true);
    } finally {
      if (seq === fetchSeqRef.current) {
        setLoading(false);
      }
    }
  }, [queryState]);

  useEffect(() => {
    void fetchList();
  }, [fetchList]);

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = keyword.trim();
    setQueryState((prev) => ({
      ...prev,
      page: 1,
      keyword: trimmed,
      nonce: prev.nonce + 1,
    }));
  };

  const handleReset = () => {
    setKeyword('');
    setQueryState((prev) => ({
      ...prev,
      page: 1,
      statusFilter: '',
      keyword: '',
      nonce: prev.nonce + 1,
    }));
  };

  const openDetailModal = (item: FeedbackItem) => {
    setSelectedItem(item);
    setEditStatus(item.status || 'pending');
    setAdminReply(item.admin_reply || '');
  };

  const handleUpdateSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedItem || saving) return;

    setSaving(true);
    try {
      await feedbackApi.updateFeedback(selectedItem.id, {
        status: editStatus,
        admin_reply: adminReply,
      });
      window.showToast?.('反馈状态与回复已成功保存', 'success');
      setSelectedItem(null);
      setQueryState((prev) => ({ ...prev, nonce: prev.nonce + 1 }));
    } catch (e: unknown) {
      window.showToast?.((e instanceof Error ? e.message : '') || '更新失败', 'error');
    } finally {
      setSaving(false);
    }
  };

  const filterChips = useMemo<FilterChip[]>(() => {
    const chips: FilterChip[] = [];
    if (queryState.statusFilter) {
      const label = statusOptions.find((o) => o.value === queryState.statusFilter)?.label || queryState.statusFilter;
      chips.push({
        key: 'status',
        label: `状态: ${label}`,
        onClear: () => setQueryState((prev) => ({ ...prev, page: 1, statusFilter: '' })),
      });
    }
    if (queryState.keyword) {
      chips.push({
        key: 'keyword',
        label: `搜索: ${queryState.keyword}`,
        onClear: () => {
          setKeyword('');
          setQueryState((prev) => ({ ...prev, page: 1, keyword: '' }));
        },
      });
    }
    return chips;
  }, [queryState]);

  const hasFilter = Boolean(keyword || queryState.keyword || queryState.statusFilter);

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Unified Borderless Filter Strip */}
      <search className="block space-y-2">
        <form onSubmit={handleSearch} className="flex flex-wrap items-center justify-between gap-2.5">
          <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
            <SearchInput
              className="w-full sm:w-80"
              ariaLabel="搜索用户反馈"
              placeholder="搜索联系邮箱、主题或正文..."
              value={keyword}
              onChange={setKeyword}
              onClear={() => {
                if (queryState.keyword) {
                  setQueryState((prev) => ({ ...prev, page: 1, keyword: '' }));
                }
              }}
            />

            <CustomSelect
              aria-label="筛选反馈状态"
              options={statusOptions}
              value={queryState.statusFilter}
              onChange={(val) => {
                setQueryState((prev) => ({ ...prev, statusFilter: val, page: 1 }));
              }}
              width="160px"
            />

            <button type="submit" className="btn-secondary h-9 px-3 text-xs font-normal whitespace-nowrap">
              查询
            </button>

            {hasFilter && (
              <button
                type="button"
                className="btn-ghost h-9 text-xs text-[var(--cms-muted-fg)]"
                onClick={handleReset}
              >
                重置
              </button>
            )}
          </div>

          <div className="flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
            <span className="tabular-nums">共 {total} 条</span>
            <ViewOptionsPopover density={density} onDensityChange={setDensity} />
          </div>
        </form>

        <FilterChipsBar chips={filterChips} onResetAll={handleReset} />
      </search>

      {/* Airy Table + External Pagination */}
      <TableRegion
        ariaLabel="用户反馈列表"
        footer={
          <Pagination
            page={queryState.page}
            pageSize={queryState.pageSize}
            pageSizeOptions={[10, 15, 20, 50, 100]}
            total={total}
            disabled={loading}
            onPageChange={(nextPage) => setQueryState((prev) => ({ ...prev, page: nextPage }))}
            onPageSizeChange={(nextSize) => setQueryState((prev) => ({ ...prev, page: 1, pageSize: nextSize }))}
            itemLabel="条反馈"
          />
        }
      >
        <table className="cms-table min-w-[880px]" data-density={density}>
          <thead>
            <tr>
              <th scope="col">工单 / 用户</th>
              <th scope="col">联系邮箱</th>
              <th scope="col">咨询主题</th>
              <th scope="col">反馈摘要</th>
              <th scope="col">处理状态</th>
              <th scope="col">提交时间</th>
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={6} cols={7} />
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={7} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  暂无用户反馈记录
                </td>
              </tr>
            ) : (
              items.map((item) => (
                <tr
                  key={item.id}
                  onClick={() => openDetailModal(item)}
                  className={`cursor-pointer ${selectedItem?.id === item.id ? 'row-selected' : ''}`}
                >
                  <td className="tabular-nums">
                    <div className="text-[var(--cms-fg)]">#{item.id}</div>
                    <div className="text-[var(--cms-muted-fg)] mt-0.5">
                      {item.user_id && item.user_id > 0 ? `UID ${item.user_id}` : '访客'}
                    </div>
                  </td>
                  <td onClick={(e) => e.stopPropagation()}>
                    <a href={`mailto:${item.email}`} className="text-[var(--cms-fg)] hover:underline">
                      {item.email}
                    </a>
                  </td>
                  <td className="text-[var(--cms-fg)] whitespace-nowrap">
                    {item.subject || '常规咨询'}
                  </td>
                  <td className="max-w-[300px] truncate text-[var(--cms-muted-fg)]" title={item.content}>
                    {item.content}
                  </td>
                  <td>
                    {item.status === 'resolved' ? (
                      <StatusDot tone="success" label="已解决" />
                    ) : item.status === 'replied' ? (
                      <StatusDot tone="primary" label="已回复" />
                    ) : (
                      <StatusDot tone="warning" label="待处理" />
                    )}
                  </td>
                  <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                    {new Date(item.created_at).toLocaleString()}
                  </td>
                  <td className="text-right" onClick={(e) => e.stopPropagation()}>
                    <button
                      type="button"
                      onClick={() => openDetailModal(item)}
                      className="btn-detail"
                    >
                      处理
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableRegion>

      {/* Right Slide-Over Ticket Drawer */}
      <CmsDialog
        open={Boolean(selectedItem)}
        onClose={() => !saving && setSelectedItem(null)}
        variant="drawer"
        title={selectedItem ? `反馈工单 #${selectedItem.id}` : ''}
        subtitle={selectedItem ? `${selectedItem.email} · ${selectedItem.subject || '常规咨询'}` : undefined}
        size="md"
        closedBy={saving ? 'none' : 'any'}
        closeDisabled={saving}
        footer={
          <>
            <button type="button" className="btn-secondary" disabled={saving} onClick={() => !saving && setSelectedItem(null)}>
              取消
            </button>
            <button type="submit" form="feedback-reply-form" disabled={saving} className="btn-primary">
              {saving ? '保存中...' : '保存处理结果'}
            </button>
          </>
        }
      >
        {selectedItem && (
          <form id="feedback-reply-form" onSubmit={handleUpdateSubmit} className="space-y-4 text-xs">
            <div className="grid grid-cols-2 gap-3 p-4 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)]">
              <div>
                <div className="text-[var(--cms-muted-fg)] mb-0.5">联系邮箱</div>
                <a href={`mailto:${selectedItem.email}`} className="text-[var(--cms-primary)] font-semibold hover:underline">
                  {selectedItem.email}
                </a>
              </div>
              <div>
                <div className="text-[var(--cms-muted-fg)] mb-0.5">用户 ID</div>
                <div className="font-semibold text-[var(--cms-fg)] tabular-nums">
                  {selectedItem.user_id || '访客（未登录）'}
                </div>
              </div>
              <div>
                <div className="text-[var(--cms-muted-fg)] mb-0.5">咨询主题</div>
                <div className="font-semibold text-[var(--cms-fg)]">{selectedItem.subject || '常规咨询'}</div>
              </div>
              <div>
                <div className="text-[var(--cms-muted-fg)] mb-0.5">提交时间</div>
                <div className="tabular-nums text-[var(--cms-fg)]">
                  {new Date(selectedItem.created_at).toLocaleString()}
                </div>
              </div>
            </div>

            <div className="space-y-1.5">
              <div className="font-medium text-[var(--cms-muted-fg)]">用户反馈原始内容</div>
              <div className="p-3.5 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] text-sm leading-relaxed whitespace-pre-wrap text-[var(--cms-fg)] max-h-52 overflow-y-auto">
                {selectedItem.content}
              </div>
            </div>

            <div className="pt-2 border-t border-[var(--cms-border)] space-y-4">
              <label className="block space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">处理状态变更</span>
                <CustomSelect
                  aria-label="处理状态变更"
                  options={modalStatusOptions}
                  value={editStatus}
                  onChange={setEditStatus}
                  width="100%"
                />
              </label>

              <label className="block space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">运营回复 / 处理备注</span>
                <textarea
                  rows={5}
                  value={adminReply}
                  onChange={(e) => setAdminReply(e.target.value)}
                  placeholder="输入处理记录或回复备注（如已通过邮件回复、退款补单单号等）..."
                  className="input-field"
                />
              </label>
            </div>
          </form>
        )}
      </CmsDialog>
    </div>
  );
}

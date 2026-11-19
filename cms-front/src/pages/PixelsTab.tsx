import { useEffect, useMemo, useState } from 'react';
import { Plus } from 'lucide-react';
import { apiRequest } from '../utils/api';
import { getCurrentAdminRole } from '../utils/promotion';
import { CmsDialog } from '../components/ui/CmsDialog';
import ConversionIntegrationsPanel from './ConversionIntegrationsPanel';
import {
  RowActionMenu,
  SearchInput,
  SkeletonRows,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../components/ui/Primitives';

interface FBPixel {
  id: string;
  name: string;
  pixel_id: string;
  credential_configured: boolean;
  retired_at?: string;
  promotion_dependencies: number;
  created_at: string;
}

export default function PixelsTab() {
  const canWrite = ['Admin', 'SuperAdmin'].includes(getCurrentAdminRole());
  const [pixels, setPixels] = useState<FBPixel[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [density, setDensity] = useState<TableDensity>('compact');

  const [editingPixel, setEditingPixel] = useState<FBPixel | null>(null);
  const [isPixelModalOpen, setIsPixelModalOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [retiring, setRetiring] = useState(false);
  const [pixelForm, setPixelForm] = useState({
    name: '',
    pixel_id: '',
    access_token: '',
    reason: '',
  });

  const fetchPixels = async () => {
    setLoading(true);
    try {
      const data = await apiRequest('GET', '/conversion/destinations');
      setPixels(data || []);
      setHasLoadedOnce(true);
    } catch (err) {
      console.error('Failed to load pixels:', err);
      setHasLoadedOnce(true);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchPixels();
  }, []);

  const handleClosePixelModal = (force = false) => {
    if (submitting && !force) return;
    setIsPixelModalOpen(false);
    setEditingPixel(null);
    setRetiring(false);
    setPixelForm({ name: '', pixel_id: '', access_token: '', reason: '' });
  };

  const handlePixelFormSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canWrite || submitting) return;
    if (!pixelForm.reason.trim()) {
      window.showToast?.('请输入操作原因。', 'error');
      return;
    }
    setSubmitting(true);
    try {
      if (editingPixel) {
        await apiRequest(
          'PATCH',
          `/conversion/destinations/${editingPixel.id}`,
          retiring
            ? { retire: true, reason: pixelForm.reason.trim() }
            : {
                display_name: pixelForm.name,
                access_token: pixelForm.access_token || undefined,
                reason: pixelForm.reason.trim(),
              },
        );
        window.showToast?.(retiring ? '像素已退役。' : '像素已更新。', 'success');
      } else {
        await apiRequest('POST', '/conversion/destinations', {
          display_name: pixelForm.name,
          pixel_id: pixelForm.pixel_id,
          access_token: pixelForm.access_token,
          reason: pixelForm.reason.trim(),
        });
        window.showToast?.('像素已创建。', 'success');
      }
      handleClosePixelModal(true);
      void fetchPixels();
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '操作失败，请重试', 'error');
    } finally {
      setSubmitting(false);
    }
  };

  const handleEditPixelClick = (pixel: FBPixel) => {
    if (!canWrite) return;
    setEditingPixel(pixel);
    setPixelForm({
      name: pixel.name,
      pixel_id: pixel.pixel_id,
      access_token: '',
      reason: '',
    });
    setIsPixelModalOpen(true);
  };

  const handleRetirePixel = (pixel: FBPixel) => {
    handleEditPixelClick(pixel);
    setRetiring(true);
  };

  const filteredPixels = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    if (!q) return pixels;
    return pixels.filter((p) => p.name.toLowerCase().includes(q) || p.pixel_id.toLowerCase().includes(q));
  }, [pixels, searchQuery]);

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Unified Borderless Filter Strip */}
      <search className="flex flex-wrap items-center justify-between gap-2.5">
        <SearchInput
          className="w-full sm:w-80"
          ariaLabel="搜索像素名称或 ID"
          placeholder="搜索像素名称或像素 ID..."
          value={searchQuery}
          onChange={setSearchQuery}
        />

        <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
          <span className="tabular-nums">共 {filteredPixels.length} 个像素</span>
          <ViewOptionsPopover density={density} onDensityChange={setDensity} />
          {canWrite && (
            <button
              type="button"
              onClick={() => {
                setEditingPixel(null);
                setPixelForm({ name: '', pixel_id: '', access_token: '', reason: '' });
                setIsPixelModalOpen(true);
              }}
              className="btn-primary h-8 px-3.5 text-xs"
            >
              <Plus size={14} aria-hidden="true" />
              <span>新增像素</span>
            </button>
          )}
        </div>
      </search>

      <TableRegion ariaLabel="Facebook 像素配置列表">
        <table className="cms-table min-w-[760px]" data-density={density}>
          <thead>
            <tr>
              <th scope="col">像素名称</th>
              <th scope="col">像素 ID / 目标 ID</th>
              <th scope="col">Access Token</th>
              <th scope="col">创建时间</th>
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={4} cols={5} />
            ) : filteredPixels.length === 0 ? (
              <tr>
                <td colSpan={5} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  暂无像素配置
                </td>
              </tr>
            ) : (
              filteredPixels.map((pixel) => (
                <tr key={pixel.id}>
                  <td className="text-[var(--cms-fg)]">{pixel.name}</td>
                  <td className="tabular-nums text-[var(--cms-fg)]">
                    {pixel.pixel_id}
                    <div className="text-xs text-[var(--cms-muted-fg)]">目标 #{pixel.id}</div>
                  </td>
                  <td className="text-[var(--cms-muted-fg)]">
                    {pixel.credential_configured ? '已配置' : '缺凭据'}
                    {pixel.retired_at && <div>已退役</div>}
                    {pixel.promotion_dependencies > 0 && (
                      <div>{pixel.promotion_dependencies} 条推广链接引用</div>
                    )}
                  </td>
                  <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                    {new Date(pixel.created_at).toLocaleString()}
                  </td>
                  <td className="text-right whitespace-nowrap">
                    {canWrite ? (
                      <div className="inline-flex items-center justify-end gap-0.5 whitespace-nowrap">
                        <button
                          type="button"
                          onClick={() => handleEditPixelClick(pixel)}
                          className="btn-detail whitespace-nowrap"
                        >
                          编辑
                        </button>
                        {!pixel.retired_at && pixel.promotion_dependencies === 0 && (
                          <RowActionMenu
                            ariaLabel={`像素 ${pixel.name} 更多操作`}
                            items={[
                              { label: '退役像素', tone: 'danger', onClick: () => handleRetirePixel(pixel) },
                            ]}
                          />
                        )}
                      </div>
                    ) : (
                      <span className="text-[var(--cms-muted-fg)]">只读</span>
                    )}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableRegion>

      <ConversionIntegrationsPanel canWrite={canWrite} />

      <CmsDialog
        open={isPixelModalOpen}
        onClose={() => handleClosePixelModal()}
        closedBy={submitting ? 'none' : 'any'}
        closeDisabled={submitting}
        variant="drawer"
        title={retiring ? '退役像素' : editingPixel ? '编辑像素' : '添加像素'}
        size="md"
        footer={
          <>
            <button
              type="button"
              disabled={submitting}
              className="btn-secondary"
              onClick={() => handleClosePixelModal()}
            >
              取消
            </button>
            <button type="submit" form="pixel-form" disabled={submitting} className="btn-primary">
              {submitting ? '保存中...' : '确认保存'}
            </button>
          </>
        }
      >
        <form id="pixel-form" onSubmit={handlePixelFormSubmit} className="space-y-4 text-xs">
          {retiring ? (
            <p>退役后，像素 {editingPixel?.pixel_id} 继续服务已保存的历史归因。</p>
          ) : (
            <>
              <label className="block space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">像素名称</span>
                <input
                  type="text"
                  autoFocus
                  className="input-field"
                  placeholder="e.g. 主站像素 / 欧美投放像素"
                  value={pixelForm.name}
                  onChange={(e) => setPixelForm({ ...pixelForm, name: e.target.value })}
                  required
                />
              </label>
              <label className="block space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">像素 ID</span>
                <input
                  type="text"
                  className="input-field font-mono-code"
                  placeholder="e.g. 102938475610293"
                  value={pixelForm.pixel_id}
                  readOnly={Boolean(editingPixel)}
                  pattern="[0-9]+"
                  maxLength={50}
                  onChange={(e) => setPixelForm({ ...pixelForm, pixel_id: e.target.value })}
                  required
                />
              </label>
              <label className="block space-y-1.5">
                <span className="block font-medium text-[var(--cms-muted-fg)]">
                  Access Token (Conversions API Token)
                </span>
                <input
                  type="password"
                  autoComplete="new-password"
                  className="input-field font-mono-code text-xs"
                  placeholder={editingPixel ? '留空保留当前凭据' : '输入 Access Token'}
                  value={pixelForm.access_token}
                  onChange={(e) => setPixelForm({ ...pixelForm, access_token: e.target.value })}
                  required={!editingPixel}
                  maxLength={4096}
                />
              </label>
            </>
          )}
          <label className="block space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">操作原因（必填）</span>
            <textarea
              className="input-field user-invalid:border-[var(--cms-error)]"
              rows={2}
              required
              maxLength={500}
              value={pixelForm.reason}
              onChange={(e) => setPixelForm({ ...pixelForm, reason: e.target.value })}
            />
          </label>
        </form>
      </CmsDialog>
    </div>
  );
}

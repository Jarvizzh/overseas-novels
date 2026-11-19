import { useEffect, useMemo, useState } from 'react';
import { Plus } from 'lucide-react';
import { apiRequest } from '../utils/api';
import { CmsDialog } from '../components/ui/CmsDialog';
import {
  RowActionMenu,
  SearchInput,
  SkeletonRows,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../components/ui/Primitives';

interface FBPixel {
  id: number;
  name: string;
  pixel_id: string;
  access_token: string;
  created_at: string;
}

export default function PixelsTab() {
  const [pixels, setPixels] = useState<FBPixel[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [density, setDensity] = useState<TableDensity>('compact');

  const [editingPixel, setEditingPixel] = useState<FBPixel | null>(null);
  const [isPixelModalOpen, setIsPixelModalOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [pixelForm, setPixelForm] = useState({
    name: '',
    pixel_id: '',
    access_token: '',
  });

  const fetchPixels = async () => {
    setLoading(true);
    try {
      const data = await apiRequest('GET', '/fb-pixels');
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
    setPixelForm({ name: '', pixel_id: '', access_token: '' });
  };

  const handlePixelFormSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    setSubmitting(true);
    try {
      if (editingPixel) {
        await apiRequest('PUT', `/fb-pixels/${editingPixel.id}`, pixelForm);
        window.showToast?.(`像素「${pixelForm.name}」更新成功。`, 'success');
      } else {
        await apiRequest('POST', '/fb-pixels', pixelForm);
        window.showToast?.(`像素「${pixelForm.name}」创建成功。`, 'success');
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
    setEditingPixel(pixel);
    setPixelForm({
      name: pixel.name,
      pixel_id: pixel.pixel_id,
      access_token: pixel.access_token,
    });
    setIsPixelModalOpen(true);
  };

  const handleDeletePixel = (id: number, name: string) => {
    window.showConfirm?.(`您确定要永久删除像素「${name}」吗？此操作不可撤回！`, async () => {
      try {
        await apiRequest('DELETE', `/fb-pixels/${id}`);
        window.showToast?.(`像素「${name}」删除成功。`, 'success');
        void fetchPixels();
      } catch (err: unknown) {
        window.showToast?.((err instanceof Error ? err.message : '') || '删除失败', 'error');
      }
    });
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
          <button
            type="button"
            onClick={() => {
              setEditingPixel(null);
              setPixelForm({ name: '', pixel_id: '', access_token: '' });
              setIsPixelModalOpen(true);
            }}
            className="btn-primary h-8 px-3.5 text-xs"
          >
            <Plus size={14} aria-hidden="true" />
            <span>新增像素</span>
          </button>
        </div>
      </search>

      <TableRegion ariaLabel="Facebook 像素配置列表">
        <table className="cms-table min-w-[760px]" data-density={density}>
          <thead>
            <tr>
              <th scope="col">像素名称</th>
              <th scope="col">像素 ID</th>
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
                  <td className="tabular-nums text-[var(--cms-fg)]">{pixel.pixel_id}</td>
                  <td className="text-[var(--cms-muted-fg)]">
                    {pixel.access_token.length > 20
                      ? `${pixel.access_token.substring(0, 8)}...${pixel.access_token.substring(pixel.access_token.length - 8)}`
                      : pixel.access_token}
                  </td>
                  <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                    {new Date(pixel.created_at).toLocaleString()}
                  </td>
                  <td className="text-right whitespace-nowrap">
                    <div className="inline-flex items-center justify-end gap-0.5 whitespace-nowrap">
                      <button type="button" onClick={() => handleEditPixelClick(pixel)} className="btn-detail whitespace-nowrap">
                        编辑
                      </button>
                      <RowActionMenu
                        ariaLabel={`像素 ${pixel.name} 更多操作`}
                        items={[
                          {
                            label: '删除像素',
                            tone: 'danger',
                            onClick: () => handleDeletePixel(pixel.id, pixel.name),
                          },
                        ]}
                      />
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableRegion>

      <CmsDialog
        open={isPixelModalOpen}
        onClose={() => handleClosePixelModal()}
        closedBy={submitting ? 'none' : 'any'}
        closeDisabled={submitting}
        variant="drawer"
        title={editingPixel ? '编辑像素' : '添加像素'}
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
              onChange={(e) => setPixelForm({ ...pixelForm, pixel_id: e.target.value })}
              required
            />
          </label>
          <label className="block space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">Access Token (Conversions API Token)</span>
            <textarea
              className="input-field font-mono-code text-xs"
              rows={4}
              placeholder="EAAG..."
              value={pixelForm.access_token}
              onChange={(e) => setPixelForm({ ...pixelForm, access_token: e.target.value })}
              required
            />
          </label>
        </form>
      </CmsDialog>
    </div>
  );
}

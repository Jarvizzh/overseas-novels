import { useState, useEffect } from 'react';
import { createPortal } from 'react-dom';
import { apiRequest } from '../utils/api';
import {
  Plus,
  Edit2,
  Trash2,
  X
} from 'lucide-react';

interface FBPixel {
  id: number;
  name: string;
  pixel_id: string;
  access_token: string;
  created_at: string;
}

export default function PixelsTab() {
  const [pixels, setPixels] = useState<FBPixel[]>([]);
  const [editingPixel, setEditingPixel] = useState<FBPixel | null>(null);
  const [isPixelModalOpen, setIsPixelModalOpen] = useState(false);
  const [pixelForm, setPixelForm] = useState({
    name: '',
    pixel_id: '',
    access_token: ''
  });

  const fetchPixels = async () => {
    try {
      const data = await apiRequest('GET', '/fb-pixels');
      setPixels(data || []);
    } catch (err: any) {
      console.error('Failed to load pixels:', err);
    }
  };

  useEffect(() => {
    fetchPixels();
  }, []);

  const handlePixelFormSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      if (editingPixel) {
        await apiRequest('PUT', `/fb-pixels/${editingPixel.id}`, pixelForm);
        window.showToast?.(`像素“${pixelForm.name}”更新成功。`, 'success');
      } else {
        await apiRequest('POST', '/fb-pixels', pixelForm);
        window.showToast?.(`像素“${pixelForm.name}”创建成功。`, 'success');
      }
      setIsPixelModalOpen(false);
      fetchPixels();
    } catch (err: any) {
      window.showToast?.(err.message || '操作失败，请重试', 'error');
    }
  };

  const handleEditPixelClick = (pixel: FBPixel) => {
    setEditingPixel(pixel);
    setPixelForm({
      name: pixel.name,
      pixel_id: pixel.pixel_id,
      access_token: pixel.access_token
    });
    setIsPixelModalOpen(true);
  };

  const handleDeletePixel = async (id: number, name: string) => {
    window.showConfirm?.(`您确定要永久删除像素 “${name}” 吗？此操作不可撤回！`, async () => {
      try {
        await apiRequest('DELETE', `/fb-pixels/${id}`);
        window.showToast?.(`像素 “${name}” 删除成功。`, 'success');
        fetchPixels();
      } catch (err: any) {
        window.showToast?.(err.message || '删除失败', 'error');
      }
    });
  };

  const handleCreatePixelClick = () => {
    setEditingPixel(null);
    setPixelForm({ name: '', pixel_id: '', access_token: '' });
    setIsPixelModalOpen(true);
  };

  const handleClosePixelModal = () => {
    setIsPixelModalOpen(false);
    setEditingPixel(null);
    setPixelForm({ name: '', pixel_id: '', access_token: '' });
  };

  return (
    <div className="animate-fade-in" style={{ padding: '16px 24px 24px 24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">
          像素管理
        </h1>
        <button
          onClick={handleCreatePixelClick}
          className="btn-primary"
          style={{ display: 'flex', gap: '6px', padding: '6px 12px', fontSize: '0.8rem' }}
        >
          <Plus size={14} /> 新增像素
        </button>
      </div>

      <div className="glass-panel animate-fade-in" style={{ padding: '24px' }}>
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', fontSize: '0.85rem' }}>
            <thead>
              <tr style={{ borderBottom: '1px solid hsl(var(--border))', color: 'hsl(var(--text-secondary))' }}>
                <th style={{ padding: '10px 8px' }}>像素名称</th>
                <th style={{ padding: '10px 8px' }}>像素 ID</th>
                <th style={{ padding: '10px 8px' }}>Access Token</th>
                <th style={{ padding: '10px 8px' }}>创建时间</th>
                <th style={{ padding: '10px 8px' }}>操作</th>
              </tr>
            </thead>
            <tbody>
              {pixels.length === 0 ? (
                <tr>
                  <td colSpan={5} style={{ padding: '20px 8px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>暂无像素配置</td>
                </tr>
              ) : (
                pixels.map((pixel) => (
                  <tr key={pixel.id} style={{ borderBottom: '1px solid hsl(var(--border) / 0.5)', fontSize: '0.9rem', color: 'hsl(var(--text-secondary))' }}>
                    <td style={{ padding: '12px 8px' }}>{pixel.name}</td>
                    <td style={{ padding: '12px 8px' }}>{pixel.pixel_id}</td>
                    <td style={{ padding: '12px 8px', color: 'hsl(var(--text-secondary))', fontFamily: 'monospace' }}>
                      {pixel.access_token.length > 20
                        ? `${pixel.access_token.substring(0, 8)}...${pixel.access_token.substring(pixel.access_token.length - 8)}`
                        : pixel.access_token
                      }
                    </td>
                    <td style={{ padding: '12px 8px' }}>{new Date(pixel.created_at).toLocaleString()}</td>
                    <td style={{ padding: '12px 8px' }}>
                      <div style={{ display: 'flex', gap: '8px' }}>
                        <button
                          onClick={() => handleEditPixelClick(pixel)}
                          className="btn-secondary"
                          style={{ padding: '4px 8px', fontSize: '0.75rem', minWidth: 'auto', display: 'flex', gap: '4px', alignItems: 'center' }}
                        >
                          <Edit2 size={12} /> 编辑
                        </button>
                        <button
                          onClick={() => handleDeletePixel(pixel.id, pixel.name)}
                          className="btn-secondary"
                          style={{ padding: '4px 8px', fontSize: '0.75rem', minWidth: 'auto', display: 'flex', gap: '4px', alignItems: 'center', color: '#ef4444', borderColor: 'rgba(239, 68, 68, 0.15)' }}
                        >
                          <Trash2 size={12} /> 删除
                        </button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {isPixelModalOpen && createPortal(
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
        }} onClick={handleClosePixelModal}>
          <div className="glass-panel animate-scale-in" style={{
            width: '100%',
            maxWidth: '480px',
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
              onClick={handleClosePixelModal}
              style={{ position: 'absolute', top: '16px', right: '16px', background: 'transparent', border: 'none', color: 'hsl(var(--text-secondary))', cursor: 'pointer' }}
            >
              <X size={18} />
            </button>
            <h3 style={{ fontSize: '1.2rem', fontWeight: 600, marginBottom: '20px' }}>
              {editingPixel ? '编辑像素' : '添加像素'}
            </h3>

            <form onSubmit={handlePixelFormSubmit}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: '16px', marginBottom: '24px' }}>
                <div>
                  <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>像素名称</label>
                  <input
                    type="text"
                    className="input-field"
                    placeholder="e.g. 主站像素 / 欧美投放像素"
                    value={pixelForm.name}
                    onChange={(e) => setPixelForm({ ...pixelForm, name: e.target.value })}
                    required
                  />
                </div>
                <div>
                  <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>像素 ID</label>
                  <input
                    type="text"
                    className="input-field"
                    placeholder="e.g. 102938475610293"
                    value={pixelForm.pixel_id}
                    onChange={(e) => setPixelForm({ ...pixelForm, pixel_id: e.target.value })}
                    required
                  />
                </div>
                <div>
                  <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>Access Token (Conversions API Token)</label>
                  <textarea
                    className="input-field"
                    rows={4}
                    placeholder="EAAG..."
                    value={pixelForm.access_token}
                    onChange={(e) => setPixelForm({ ...pixelForm, access_token: e.target.value })}
                    required
                    style={{ resize: 'vertical', width: '100%', maxWidth: '100%', backgroundColor: 'hsl(var(--bg-surface))', color: 'hsl(var(--text-primary))' }}
                  />
                </div>
              </div>

              <div style={{ display: 'flex', gap: '12px' }}>
                <button type="button" className="btn-secondary" style={{ flex: 1, gap: '6px' }} onClick={handleClosePixelModal}>
                  取消
                </button>
                <button type="submit" className="btn-primary" style={{ flex: 1.5, gap: '6px' }}>
                  确认保存
                </button>
              </div>
            </form>
          </div>
        </div>,
        document.body
      )}
    </div>
  );
}

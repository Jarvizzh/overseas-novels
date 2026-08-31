import React from 'react';
import { createPortal } from 'react-dom';
import CustomSelect from '../CustomSelect';

export interface NovelFormData {
  title: string;
  author: string;
  cover_url: string;
  status: string;
  rating: number;
  synopsis: string;
  genres: string;
  coin_cost_per_thousand: number | string;
  start_pay_chapter_index: number | string;
}

interface NovelFormModalProps {
  isOpen: boolean;
  isEditing?: boolean;
  formData: NovelFormData;
  setFormData: (data: NovelFormData) => void;
  onSubmit: (e: React.FormEvent) => void;
  onCancel: () => void;
}

export const NovelFormModal: React.FC<NovelFormModalProps> = ({
  isOpen,
  isEditing = false,
  formData,
  setFormData,
  onSubmit,
  onCancel,
}) => {
  React.useEffect(() => {
    if (!isOpen) return;
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onCancel();
      }
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onCancel]);

  if (!isOpen) return null;

  return createPortal(
    <div
      style={{
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
        padding: '20px',
      }}
      onClick={onCancel}
    >
      <div
        className="glass-panel animate-fade-in"
        style={{
          width: '100%',
          maxWidth: '640px',
          maxHeight: '90vh',
          backgroundColor: 'hsl(var(--bg-surface))',
          border: '1px solid hsl(var(--border))',
          borderRadius: '16px',
          display: 'flex',
          flexDirection: 'column',
          boxShadow: 'var(--shadow-lg)',
          overflow: 'hidden',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Modal Header */}
        <div
          style={{
            padding: '20px 24px',
            borderBottom: '1px solid hsl(var(--border))',
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
          }}
        >
          <h3 style={{ fontSize: '1.25rem', fontWeight: 600 }}>
            {isEditing ? '编辑书籍及计费设置' : '新建书籍档案'}
          </h3>
        </div>

        {/* Modal Form Body */}
        <form
          onSubmit={onSubmit}
          style={{ display: 'flex', flexDirection: 'column', flex: 1, overflow: 'hidden', margin: 0 }}
        >
          <div style={{ padding: '24px', overflowY: 'auto', flex: 1 }}>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px', marginBottom: '16px' }}>
              <div>
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  书名
                </label>
                <input
                  type="text"
                  className="input-field"
                  placeholder="e.g. Alpha King Bound"
                  value={formData.title}
                  onChange={(e) => setFormData({ ...formData, title: e.target.value })}
                  required
                />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  作者
                </label>
                <input
                  type="text"
                  className="input-field"
                  placeholder="e.g. Catherine A."
                  value={formData.author}
                  onChange={(e) => setFormData({ ...formData, author: e.target.value })}
                  required
                />
              </div>
              <div style={{ gridColumn: 'span 2' }}>
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  封面图片 URL 地址
                </label>
                <input
                  type="text"
                  className="input-field"
                  value={formData.cover_url}
                  onChange={(e) => setFormData({ ...formData, cover_url: e.target.value })}
                  required
                />
              </div>
              <div style={{ gridColumn: 'span 2' }}>
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  分类标签 (用逗号分隔，如 Romance, Werewolf)
                </label>
                <input
                  type="text"
                  className="input-field"
                  placeholder="e.g. Romance, Werewolf, CEO"
                  value={formData.genres}
                  onChange={(e) => setFormData({ ...formData, genres: e.target.value })}
                  required
                />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  完结状态
                </label>
                <CustomSelect
                  options={[
                    { value: 'Ongoing', label: '连载中 (Ongoing)' },
                    { value: 'Completed', label: '已完结 (Completed)' },
                  ]}
                  value={formData.status}
                  onChange={(val) => setFormData({ ...formData, status: val })}
                  width="100%"
                />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  评分 (1-5)
                </label>
                <input
                  type="number"
                  className="input-field"
                  min={0}
                  max={5}
                  step={0.1}
                  value={formData.rating}
                  onChange={(e) => setFormData({ ...formData, rating: Number(e.target.value) })}
                  required
                />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  千字付费金币数 (默认 500 金币/千字)
                </label>
                <input
                  type="number"
                  className="input-field"
                  placeholder="500"
                  min={0}
                  value={formData.coin_cost_per_thousand}
                  onChange={(e) =>
                    setFormData({
                      ...formData,
                      coin_cost_per_thousand: e.target.value === '' ? '' : Number(e.target.value),
                    })
                  }
                />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  起始收费章节 (默认从第 3 章起)
                </label>
                <input
                  type="number"
                  className="input-field"
                  min={1}
                  value={formData.start_pay_chapter_index}
                  onChange={(e) =>
                    setFormData({
                      ...formData,
                      start_pay_chapter_index: e.target.value === '' ? '' : Number(e.target.value),
                    })
                  }
                  required
                />
              </div>
              <div style={{ gridColumn: 'span 2' }}>
                <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  书籍简介
                </label>
                <textarea
                  className="input-field"
                  rows={4}
                  placeholder="描述一下小说的主要情节吸引读者..."
                  value={formData.synopsis}
                  onChange={(e) => setFormData({ ...formData, synopsis: e.target.value })}
                />
              </div>
            </div>
          </div>

          {/* Modal Footer */}
          <div
            style={{
              padding: '16px 24px',
              borderTop: '1px solid hsl(var(--border))',
              display: 'flex',
              justifyContent: 'flex-end',
              alignItems: 'center',
              gap: '12px',
              backgroundColor: 'hsl(var(--bg-surface))',
              width: '100%',
              boxSizing: 'border-box',
            }}
          >
            <button type="button" className="btn-secondary" onClick={onCancel} style={{ marginLeft: 'auto' }}>
              取消
            </button>
            <button type="submit" className="btn-primary">
              {isEditing ? '保存修改' : '保存书籍档案'}
            </button>
          </div>
        </form>
      </div>
    </div>,
    document.body
  );
};

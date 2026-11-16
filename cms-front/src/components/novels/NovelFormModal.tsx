import React, { useEffect, useId, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { AlertCircle, CheckCircle2, Image as ImageIcon, Loader2, Upload } from 'lucide-react';
import { apiRequest } from '../../utils/api';
import CustomSelect from '../CustomSelect';

const MAX_COVER_BYTES = 5 * 1024 * 1024;
const ALLOWED_MIME_TYPES = new Set(['image/jpeg', 'image/png', 'image/webp']);
const ALLOWED_EXT_RE = /\.(jpe?g|png|webp)$/i;

export interface NovelFormData {
  title: string;
  author: string;
  cover_url: string;
  cover_preview_url?: string;
  status: string;
  publish_status?: string;
  rating: number;
  synopsis: string;
  genres: string;
  coin_cost_per_thousand: number | string;
  start_pay_chapter_index: number | string;
}

interface NovelFormModalProps {
  isOpen: boolean;
  isEditing?: boolean;
  isSubmitting?: boolean;
  hasChapters?: boolean;
  globalCoinCost?: number;
  formData: NovelFormData;
  setFormData: React.Dispatch<React.SetStateAction<NovelFormData>>;
  onSubmit: (e: React.FormEvent) => void;
  onCancel: () => void;
}

export const NovelFormModal: React.FC<NovelFormModalProps> = ({
  isOpen,
  isEditing = false,
  isSubmitting = false,
  hasChapters = false,
  globalCoinCost = 500,
  formData,
  setFormData,
  onSubmit,
  onCancel,
}) => {
  const [uploadingCover, setUploadingCover] = useState(false);
  const [coverError, setCoverError] = useState('');
  const [isDragging, setIsDragging] = useState(false);
  const [previewFailed, setPreviewFailed] = useState(false);

  const fileInputRef = useRef<HTMLInputElement>(null);
  const uploadBtnRef = useRef<HTMLButtonElement>(null);

  const modalTitleId = useId();
  const titleId = useId();
  const authorId = useId();
  const coverInputId = useId();
  const coverHintId = useId();
  const coverErrorId = useId();
  const genresId = useId();
  const ratingId = useId();
  const costId = useId();
  const costHintId = useId();
  const startPayId = useId();
  const synopsisId = useId();

  useEffect(() => {
    if (!isOpen) return;
    setCoverError('');
    setIsDragging(false);
    setPreviewFailed(false);
  }, [isOpen]);

  useEffect(() => {
    setPreviewFailed(false);
  }, [formData.cover_preview_url, formData.cover_url]);

  useEffect(() => {
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

  const previewSrc = formData.cover_preview_url || (formData.cover_url.startsWith('oss://') ? '' : formData.cover_url);

  const handleCoverFile = async (file: File | undefined | null) => {
    if (!file) return;

    const typeAllowed = ALLOWED_MIME_TYPES.has(file.type) || (!file.type && ALLOWED_EXT_RE.test(file.name));
    if (!typeAllowed) {
      const msg = '仅支持 JPG、PNG、WebP 格式的封面图片';
      setCoverError(msg);
      window.showToast?.(msg, 'error');
      return;
    }

    if (file.size <= 0 || file.size > MAX_COVER_BYTES) {
      const msg = '封面图片大小不能超过 5MB';
      setCoverError(msg);
      window.showToast?.(msg, 'error');
      return;
    }

    setCoverError('');
    setUploadingCover(true);
    try {
      const body = new FormData();
      body.append('file', file);
      const res = await apiRequest('POST', '/novels/cover-upload', body, true);
      setFormData((prev) => ({
        ...prev,
        cover_url: res.cover_key,
        cover_preview_url: res.preview_url,
      }));
      setPreviewFailed(false);
      window.showToast?.('封面图片上传成功', 'success');
    } catch (err: any) {
      const msg = err?.message || '封面图片上传失败';
      setCoverError(msg);
      window.showToast?.(msg, 'error');
    } finally {
      setUploadingCover(false);
      if (fileInputRef.current) {
        fileInputRef.current.value = '';
      }
    }
  };

  const handleFormSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (uploadingCover) {
      setCoverError('封面正在上传中，请稍候再保存');
      uploadBtnRef.current?.focus();
      return;
    }
    if (!formData.cover_url.trim()) {
      setCoverError('请先上传书籍封面图片');
      uploadBtnRef.current?.focus();
      return;
    }
    onSubmit(e);
  };

  const describedByCover = coverError ? `${coverHintId} ${coverErrorId}` : coverHintId;

  return createPortal(
    <div
      style={{
        position: 'fixed',
        top: 0,
        left: 0,
        right: 0,
        bottom: 0,
        backgroundColor: 'rgba(15, 23, 42, 0.35)',
        backdropFilter: 'blur(2.5px)',
        WebkitBackdropFilter: 'blur(2.5px)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 10000,
        padding: '20px',
      }}
      onClick={onCancel}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={modalTitleId}
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
          <h3 id={modalTitleId} style={{ fontSize: '1.25rem', fontWeight: 600 }}>
            {isEditing ? '编辑书籍及计费设置' : '新建书籍档案'}
          </h3>
        </div>

        {/* Modal Form Body */}
        <form
          onSubmit={handleFormSubmit}
          style={{ display: 'flex', flexDirection: 'column', flex: 1, overflow: 'hidden', margin: 0 }}
        >
          <div style={{ padding: '24px', overflowY: 'auto', flex: 1 }}>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px', marginBottom: '16px' }}>
              <div>
                <label htmlFor={titleId} style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  书名 <span aria-hidden="true" style={{ color: '#ef4444' }}>*</span>
                </label>
                <input
                  id={titleId}
                  name="title"
                  type="text"
                  className="input-field"
                  placeholder="e.g. Alpha King Bound"
                  value={formData.title}
                  onChange={(e) => setFormData((prev) => ({ ...prev, title: e.target.value }))}
                  required
                />
              </div>
              <div>
                <label htmlFor={authorId} style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  作者 <span aria-hidden="true" style={{ color: '#ef4444' }}>*</span>
                </label>
                <input
                  id={authorId}
                  name="author"
                  type="text"
                  className="input-field"
                  placeholder="e.g. Catherine A."
                  value={formData.author}
                  onChange={(e) => setFormData((prev) => ({ ...prev, author: e.target.value }))}
                  required
                />
              </div>

              <div style={{ gridColumn: 'span 2' }}>
                <label htmlFor={coverInputId} style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  书籍封面图片 <span aria-hidden="true" style={{ color: '#ef4444' }}>*</span>
                </label>
                <div
                  className="cover-upload-dropzone"
                  onDragOver={(e) => {
                    e.preventDefault();
                    setIsDragging(true);
                  }}
                  onDragLeave={(e) => {
                    e.preventDefault();
                    setIsDragging(false);
                  }}
                  onDrop={(e) => {
                    e.preventDefault();
                    setIsDragging(false);
                    if (!uploadingCover) {
                      handleCoverFile(e.dataTransfer.files?.[0]);
                    }
                  }}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '16px',
                    padding: '14px',
                    borderRadius: '12px',
                    border: coverError
                      ? '1px dashed #ef4444'
                      : isDragging
                        ? '1px dashed hsl(var(--primary))'
                        : '1px dashed hsl(var(--border))',
                    backgroundColor: isDragging ? 'hsl(var(--primary) / 0.05)' : 'hsl(var(--bg-card))',
                    transition: 'border-color 0.2s ease, background-color 0.2s ease',
                  }}
                >
                  <div
                    style={{
                      width: '84px',
                      height: '112px',
                      borderRadius: '8px',
                      border: '1px solid hsl(var(--border))',
                      backgroundColor: 'hsl(var(--bg-surface))',
                      display: 'flex',
                      flexDirection: 'column',
                      alignItems: 'center',
                      justifyContent: 'center',
                      overflow: 'hidden',
                      flexShrink: 0,
                    }}
                  >
                    {previewSrc && !previewFailed ? (
                      <img
                        src={previewSrc}
                        alt="书籍封面预览"
                        style={{ width: '100%', height: '100%', objectFit: 'cover' }}
                        onError={() => setPreviewFailed(true)}
                      />
                    ) : (
                      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '6px', padding: '8px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>
                        <ImageIcon size={22} aria-hidden="true" />
                        <span style={{ fontSize: '0.7rem' }}>{previewFailed ? '加载失败' : '暂无封面'}</span>
                      </div>
                    )}
                  </div>

                  <div style={{ flex: 1, minWidth: 0 }}>
                    <input
                      ref={fileInputRef}
                      id={coverInputId}
                      name="cover_file"
                      type="file"
                      className="visually-hidden"
                      accept=".jpg,.jpeg,.png,.webp,image/jpeg,image/png,image/webp"
                      aria-invalid={coverError ? 'true' : 'false'}
                      aria-describedby={describedByCover}
                      disabled={uploadingCover}
                      onChange={(e) => handleCoverFile(e.target.files?.[0])}
                    />

                    <div style={{ display: 'flex', alignItems: 'center', gap: '10px', flexWrap: 'wrap', marginBottom: '8px' }}>
                      <button
                        ref={uploadBtnRef}
                        type="button"
                        className="btn-secondary"
                        disabled={uploadingCover}
                        onClick={() => fileInputRef.current?.click()}
                        style={{ gap: '6px', padding: '7px 14px', fontSize: '0.825rem' }}
                      >
                        {uploadingCover ? (
                          <>
                            <Loader2 size={15} className="animate-spin" aria-hidden="true" />
                            <span>正在上传至 OSS...</span>
                          </>
                        ) : (
                          <>
                            <Upload size={15} aria-hidden="true" />
                            <span>{formData.cover_url ? '更换封面图片' : '选择或拖拽上传封面'}</span>
                          </>
                        )}
                      </button>

                      {formData.cover_url && !uploadingCover && (
                        <span
                          role="status"
                          style={{ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: '0.78rem', color: 'hsl(var(--accent-green))', fontWeight: 500 }}
                        >
                          <CheckCircle2 size={14} aria-hidden="true" />
                          <span>已上传封面</span>
                        </span>
                      )}
                    </div>

                    <p id={coverHintId} style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))', lineHeight: 1.5 }}>
                      支持 JPG、PNG、WebP 原图上传，单张不超过 5MB（推荐 3:4 竖版封面）。仅支持上传，不支持手填链接。
                    </p>

                    {coverError && (
                      <p
                        id={coverErrorId}
                        role="status"
                        aria-live="polite"
                        style={{ display: 'flex', alignItems: 'center', gap: '4px', fontSize: '0.78rem', color: '#ef4444', marginTop: '6px' }}
                      >
                        <AlertCircle size={14} aria-hidden="true" />
                        <span>{coverError}</span>
                      </p>
                    )}
                  </div>
                </div>
              </div>

              <div style={{ gridColumn: 'span 2' }}>
                <label htmlFor={genresId} style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  分类标签 (用逗号分隔，如 Romance, Werewolf) <span aria-hidden="true" style={{ color: '#ef4444' }}>*</span>
                </label>
                <input
                  id={genresId}
                  name="genres"
                  type="text"
                  className="input-field"
                  placeholder="e.g. Romance, Werewolf, CEO"
                  value={formData.genres}
                  onChange={(e) => setFormData((prev) => ({ ...prev, genres: e.target.value }))}
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
                  onChange={(val) => setFormData((prev) => ({ ...prev, status: val }))}
                  width="100%"
                />
              </div>
              <div>
                <label htmlFor={ratingId} style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  评分 (1-5)
                </label>
                <input
                  id={ratingId}
                  name="rating"
                  type="number"
                  className="input-field"
                  min={0}
                  max={5}
                  step={0.1}
                  value={formData.rating}
                  onChange={(e) => setFormData((prev) => ({ ...prev, rating: Number(e.target.value) }))}
                  required
                />
              </div>
              <div>
                <label htmlFor={costId} style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  千字付费金币数 (留空跟随全局)
                </label>
                <p id={costHintId} style={{ fontSize: '0.72rem', color: 'hsl(var(--text-muted))', marginBottom: '6px' }}>
                  留空将自动继承全局默认单价（当前 {globalCoinCost || 500} 金币/千字）。
                </p>
                <input
                  id={costId}
                  name="coin_cost_per_thousand"
                  type="number"
                  className="input-field"
                  placeholder={`跟随全局（${globalCoinCost || 500} 金币）`}
                  aria-describedby={costHintId}
                  min={1}
                  value={formData.coin_cost_per_thousand}
                  onChange={(e) =>
                    setFormData((prev) => ({
                      ...prev,
                      coin_cost_per_thousand: e.target.value === '' ? '' : Number(e.target.value),
                    }))
                  }
                />
              </div>
              <div>
                <label htmlFor={startPayId} style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  起始收费章节 (默认从第 3 章起) <span aria-hidden="true" style={{ color: '#ef4444' }}>*</span>
                </label>
                <input
                  id={startPayId}
                  name="start_pay_chapter_index"
                  type="number"
                  className="input-field"
                  min={1}
                  value={formData.start_pay_chapter_index}
                  onChange={(e) =>
                    setFormData((prev) => ({
                      ...prev,
                      start_pay_chapter_index: e.target.value === '' ? '' : Number(e.target.value),
                    }))
                  }
                  required
                />
              </div>
              {isEditing ? (
                <fieldset style={{ gridColumn: 'span 2', border: '1px solid hsl(var(--border))', borderRadius: '10px', padding: '12px 14px', margin: 0 }}>
                  <legend style={{ fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', padding: '0 6px' }}>
                    上架发布状态
                  </legend>
                  <div style={{ display: 'flex', gap: '16px', flexWrap: 'wrap', alignItems: 'center' }}>
                    {[
                      { value: 'draft', label: '草稿（未上架）', disabled: false },
                      { value: 'published', label: hasChapters ? '已上架（书城可见）' : '已上架（需先导入章节）', disabled: !hasChapters },
                      { value: 'offline', label: '已下架（书城隐藏）', disabled: false },
                    ].map((opt) => (
                      <label
                        key={opt.value}
                        style={{
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: '6px',
                          fontSize: '0.85rem',
                          cursor: opt.disabled ? 'not-allowed' : 'pointer',
                          color: opt.disabled ? 'hsl(var(--text-muted))' : 'hsl(var(--text-primary))',
                        }}
                      >
                        <input
                          type="radio"
                          name="publish_status"
                          value={opt.value}
                          checked={(formData.publish_status || 'draft') === opt.value}
                          disabled={opt.disabled}
                          onChange={(e) => setFormData((prev) => ({ ...prev, publish_status: e.target.value }))}
                        />
                        <span>{opt.label}</span>
                      </label>
                    ))}
                  </div>
                </fieldset>
              ) : (
                <div style={{ gridColumn: 'span 2', padding: '10px 12px', borderRadius: '8px', backgroundColor: 'hsl(var(--bg-card))', border: '1px solid hsl(var(--border))', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))' }}>
                  新建书籍默认保存为 <strong>草稿（未上架）</strong>，不会立即暴露在书城；请保存后进入详情页导入章节再点击“上架发布”。
                </div>
              )}
              <div style={{ gridColumn: 'span 2' }}>
                <label htmlFor={synopsisId} style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  书籍简介
                </label>
                <textarea
                  id={synopsisId}
                  name="synopsis"
                  className="input-field"
                  rows={4}
                  placeholder="描述一下小说的主要情节吸引读者..."
                  value={formData.synopsis}
                  onChange={(e) => setFormData((prev) => ({ ...prev, synopsis: e.target.value }))}
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
            <button type="submit" className="btn-primary" disabled={uploadingCover || isSubmitting}>
              {isSubmitting ? '保存中...' : isEditing ? '保存修改' : '保存书籍档案'}
            </button>
          </div>
        </form>
      </div>
    </div>,
    document.body
  );
};

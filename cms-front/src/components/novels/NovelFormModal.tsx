import React, { useEffect, useId, useRef, useState } from 'react';
import { AlertCircle, CheckCircle2, Image as ImageIcon, Loader2, Upload } from 'lucide-react';
import { apiRequest } from '../../utils/api';
import CustomSelect from '../CustomSelect';
import { CmsDialog } from '../ui/CmsDialog';
import { ChapterUploadFields, DEFAULT_CHAPTER_DELIMITER_REGEX } from './ChapterUploadFields';

const MAX_COVER_BYTES = 5 * 1024 * 1024;
export const MAX_GENRES_LENGTH = 2048;
const ALLOWED_MIME_TYPES = new Set(['image/jpeg', 'image/png', 'image/webp']);
const ALLOWED_EXT_RE = /\.(jpe?g|png|webp)$/i;

export interface NovelFormData {
  title: string;
  author: string;
  cover_url: string;
  cover_preview_url?: string;
  status: string;
  publish_status?: string;
  rating: number | string;
  synopsis: string;
  genres: string;
  coin_cost_per_thousand: number | string;
  start_pay_chapter_index: number | string;
}

export interface ChapterIdentityOption {
  id: string;
  title: string;
  chapter_index: number | null;
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
  chapterImportType?: 'single_txt' | 'zip';
  onChapterImportTypeChange?: (next: 'single_txt' | 'zip') => void;
  chapterFile?: File | null;
  onChapterFileChange?: (file: File | null) => void;
  chapterRegex?: string;
  onChapterRegexChange?: (val: string) => void;
  chapterMapping?: string;
  onChapterMappingChange?: (val: string) => void;
  chapterIdentities?: ChapterIdentityOption[];
  onLoadChapterIdentities?: () => void;
  createdDraftId?: number | null;
  submitError?: string;
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
  chapterImportType = 'single_txt',
  onChapterImportTypeChange,
  chapterFile = null,
  onChapterFileChange,
  chapterRegex = DEFAULT_CHAPTER_DELIMITER_REGEX,
  onChapterRegexChange,
  chapterMapping = '',
  onChapterMappingChange,
  chapterIdentities = [],
  onLoadChapterIdentities,
  createdDraftId = null,
  submitError = '',
}) => {
  const [uploadingCover, setUploadingCover] = useState(false);
  const [coverError, setCoverError] = useState('');
  const [chapterFileError, setChapterFileError] = useState('');
  const [genresTouched, setGenresTouched] = useState(false);
  const [genresError, setGenresError] = useState('');
  const [isDragging, setIsDragging] = useState(false);
  const [previewFailed, setPreviewFailed] = useState(false);

  const fileInputRef = useRef<HTMLInputElement>(null);
  const uploadBtnRef = useRef<HTMLButtonElement>(null);
  const genresInputRef = useRef<HTMLInputElement>(null);
  const chapterFileInputRef = useRef<HTMLInputElement>(null);
  const chapterUploadBtnRef = useRef<HTMLButtonElement>(null);
  const chapterRegexInputRef = useRef<HTMLInputElement>(null);
  const formRef = useRef<HTMLFormElement>(null);

  const formId = useId();
  const titleId = useId();
  const authorId = useId();
  const coverInputId = useId();
  const coverHintId = useId();
  const coverErrorId = useId();
  const genresId = useId();
  const genresHintId = useId();
  const genresErrorId = useId();
  const ratingId = useId();
  const costId = useId();
  const costHintId = useId();
  const startPayId = useId();
  const synopsisId = useId();

  useEffect(() => {
    if (!isOpen) return;
    setCoverError('');
    setChapterFileError('');
    setGenresTouched(false);
    setGenresError('');
    setIsDragging(false);
    setPreviewFailed(false);
  }, [isOpen]);

  useEffect(() => {
    if (chapterFile) setChapterFileError('');
  }, [chapterFile]);

  useEffect(() => {
    setPreviewFailed(false);
  }, [formData.cover_preview_url, formData.cover_url]);

  const isCloseBlocked = uploadingCover || isSubmitting;
  const previewSrc = formData.cover_preview_url || (formData.cover_url.startsWith('oss://') ? '' : formData.cover_url);

  const validateGenresValue = (raw: string): string => {
    const trimmed = raw.trim();
    if (!trimmed) return '请输入至少一个分类标签';
    if (trimmed.length > MAX_GENRES_LENGTH) return `分类标签不能超过 ${MAX_GENRES_LENGTH} 个字符`;
    const tags = trimmed.split(',').map((g) => g.trim()).filter(Boolean);
    if (tags.length === 0) return '请输入至少一个有效的分类标签';
    return '';
  };

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
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '封面图片上传失败';
      setCoverError(msg);
      window.showToast?.(msg, 'error');
    } finally {
      setUploadingCover(false);
      if (fileInputRef.current) fileInputRef.current.value = '';
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
    const gErr = validateGenresValue(formData.genres);
    if (gErr) {
      setGenresTouched(true);
      setGenresError(gErr);
      genresInputRef.current?.focus();
      return;
    }
    if (!isEditing && !chapterFile) {
      setChapterFileError('请先选择或拖拽上传书籍章节文件（支持 .txt 或 .zip）');
      chapterUploadBtnRef.current?.focus();
      return;
    }
    const ratingRaw = String(formData.rating).trim();
    const parsedRating = Number(ratingRaw);
    if (!ratingRaw || !Number.isFinite(parsedRating) || parsedRating < 0 || parsedRating > 5) {
      window.showToast?.('评分须为 0 到 5 之间的数字', 'error');
      return;
    }
    if (chapterFile && chapterImportType === 'single_txt' && !chapterRegex.trim()) {
      window.showToast?.('请输入章节匹配分割正则表达式', 'error');
      chapterRegexInputRef.current?.focus();
      return;
    }
    if (isEditing && chapterFile && chapterMapping.trim()) {
      try {
        JSON.parse(chapterMapping);
      } catch {
        window.showToast?.('章节映射 JSON 格式无效，请检查语法后再提交', 'error');
        return;
      }
    }
    onSubmit(e);
  };

  const describedByCover = coverError ? `${coverHintId} ${coverErrorId}` : coverHintId;
  const describedByGenres = genresError ? `${genresHintId} ${genresErrorId}` : genresHintId;

  return (
    <CmsDialog
      open={isOpen}
      onClose={onCancel}
      variant="drawer"
      size="lg"
      closedBy={isCloseBlocked ? 'none' : 'closerequest'}
      closeDisabled={isCloseBlocked}
      title={isEditing ? '编辑书籍及计费设置' : '新建书籍档案'}
      subtitle={isEditing ? '修改书目信息、独立单价、上架状态或批量更新章节手稿' : '录入书名封面与初始章节手稿，保存为草稿'}
      footer={
        <>
          <button type="button" className="btn-secondary" disabled={isCloseBlocked} onClick={onCancel}>
            取消
          </button>
          <button type="submit" form={formId} className="btn-primary" disabled={uploadingCover || isSubmitting}>
            {isSubmitting
              ? isEditing
                ? chapterFile
                  ? '正在保存并导入章节...'
                  : '保存中...'
                : '正在创建并导入章节...'
              : isEditing
                ? chapterFile
                  ? '保存修改并导入章节'
                  : '保存修改'
                : createdDraftId
                  ? '重试导入章节并完成'
                  : '创建书籍并导入章节'}
          </button>
        </>
      }
    >
      <form id={formId} ref={formRef} onSubmit={handleFormSubmit} className="space-y-5">
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <label htmlFor={titleId} className="space-y-1.5">
            <span className="block text-xs font-medium text-[var(--cms-muted-fg)]">
              书名 <span aria-hidden="true" className="text-[var(--cms-error)]">*</span>
            </span>
            <input
              id={titleId}
              name="title"
              type="text"
              autoFocus
              className="input-field"
              placeholder="e.g. Alpha King Bound"
              value={formData.title}
              onChange={(e) => setFormData((prev) => ({ ...prev, title: e.target.value }))}
              required
            />
          </label>

          <label htmlFor={authorId} className="space-y-1.5">
            <span className="block text-xs font-medium text-[var(--cms-muted-fg)]">
              作者 <span aria-hidden="true" className="text-[var(--cms-error)]">*</span>
            </span>
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
          </label>
        </div>

        {/* Cover Upload */}
        <div className="space-y-1.5">
          <label htmlFor={coverInputId} className="block text-xs font-medium text-[var(--cms-muted-fg)]">
            书籍封面图片 <span aria-hidden="true" className="text-[var(--cms-error)]">*</span>
          </label>
          <div
            className="cover-upload-dropzone flex items-center gap-4 p-4 rounded-xl border transition-colors"
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
              if (!uploadingCover) handleCoverFile(e.dataTransfer.files?.[0]);
            }}
            style={{
              borderStyle: 'dashed',
              borderColor: coverError
                ? 'var(--cms-error)'
                : isDragging
                  ? 'var(--cms-primary)'
                  : 'var(--cms-border)',
              backgroundColor: isDragging
                ? 'color-mix(in srgb, var(--cms-primary) 6%, var(--cms-card))'
                : 'var(--cms-content)',
            }}
          >
            <div className="w-20 h-28 rounded-lg border border-[var(--cms-border)] bg-[var(--cms-card)] flex items-center justify-center overflow-hidden shrink-0">
              {previewSrc && !previewFailed ? (
                <img
                  src={previewSrc}
                  alt="书籍封面预览"
                  className="w-full h-full object-cover"
                  onError={() => setPreviewFailed(true)}
                />
              ) : (
                <div className="flex flex-col items-center gap-1.5 p-2 text-center text-[var(--cms-placeholder)]">
                  <ImageIcon size={20} aria-hidden="true" />
                  <span className="text-xs">{previewFailed ? '加载失败' : '暂无封面'}</span>
                </div>
              )}
            </div>

            <div className="flex-1 min-w-0 space-y-2">
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

              <div className="flex items-center gap-2.5 flex-wrap">
                <button
                  ref={uploadBtnRef}
                  type="button"
                  className="btn-secondary h-8 px-3 text-xs"
                  disabled={uploadingCover}
                  onClick={() => fileInputRef.current?.click()}
                >
                  {uploadingCover ? (
                    <>
                      <Loader2 size={14} className="animate-spin" aria-hidden="true" />
                      <span>正在上传至 OSS...</span>
                    </>
                  ) : (
                    <>
                      <Upload size={14} aria-hidden="true" />
                      <span>{formData.cover_url ? '更换封面图片' : '选择或拖拽上传封面'}</span>
                    </>
                  )}
                </button>

                {formData.cover_url && !uploadingCover && (
                  <span role="status" className="inline-flex items-center gap-1 text-xs font-medium text-[var(--cms-success)]">
                    <CheckCircle2 size={14} aria-hidden="true" />
                    <span>已上传封面</span>
                  </span>
                )}
              </div>

              <p id={coverHintId} className="text-xs text-[var(--cms-muted-fg)] m-0">
                支持 JPG、PNG、WebP 原图上传，单张不超过 5MB（推荐 3:4 竖版封面）。仅支持上传，不支持手填链接。
              </p>

              {coverError && (
                <p id={coverErrorId} role="status" aria-live="polite" className="flex items-center gap-1 text-xs text-[var(--cms-error)] m-0">
                  <AlertCircle size={13} aria-hidden="true" />
                  <span>{coverError}</span>
                </p>
              )}
            </div>
          </div>
        </div>

        {/* Genres */}
        <div className="space-y-1.5">
          <div className="flex items-center justify-between gap-2">
            <label htmlFor={genresId} className="text-xs font-medium text-[var(--cms-muted-fg)]">
              分类标签 (用逗号分隔，如 Romance, Werewolf) <span aria-hidden="true" className="text-[var(--cms-error)]">*</span>
            </label>
            <span className={`text-xs tabular-nums ${formData.genres.length >= MAX_GENRES_LENGTH ? 'text-[var(--cms-warning)]' : 'text-[var(--cms-muted-fg)]'}`}>
              {formData.genres.length} / {MAX_GENRES_LENGTH}
            </span>
          </div>
          <input
            ref={genresInputRef}
            id={genresId}
            name="genres"
            type="text"
            maxLength={MAX_GENRES_LENGTH}
            className="input-field"
            placeholder="e.g. Romance, Werewolf, CEO"
            aria-invalid={genresError ? 'true' : 'false'}
            aria-describedby={describedByGenres}
            value={formData.genres}
            onChange={(e) => {
              const nextVal = e.target.value;
              setFormData((prev) => ({ ...prev, genres: nextVal }));
              if (genresError) setGenresError('');
            }}
            onBlur={() => {
              if (genresTouched || formData.genres.length > 0) {
                setGenresTouched(true);
                setGenresError(validateGenresValue(formData.genres));
              }
            }}
            required
          />
          <p id={genresHintId} className="text-xs text-[var(--cms-muted-fg)] m-0">
            多个标签请使用英文逗号分隔，总长度最多支持 {MAX_GENRES_LENGTH} 个字符。
          </p>
          {genresError && (
            <p id={genresErrorId} role="status" aria-live="polite" className="flex items-center gap-1 text-xs text-[var(--cms-error)] m-0">
              <AlertCircle size={13} aria-hidden="true" />
              <span>{genresError}</span>
            </p>
          )}
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div className="space-y-1.5">
            <span className="block text-xs font-medium text-[var(--cms-muted-fg)]">完结状态</span>
            <CustomSelect
              aria-label="完结状态"
              options={[
                { value: 'Ongoing', label: '连载中 (Ongoing)' },
                { value: 'Completed', label: '已完结 (Completed)' },
              ]}
              value={formData.status}
              onChange={(val) => setFormData((prev) => ({ ...prev, status: val }))}
              width="100%"
            />
          </div>

          <label htmlFor={ratingId} className="space-y-1.5">
            <span className="block text-xs font-medium text-[var(--cms-muted-fg)]">评分 (0–5)</span>
            <input
              id={ratingId}
              name="rating"
              type="number"
              className="input-field tabular-nums"
              min={0}
              max={5}
              step={0.1}
              value={formData.rating}
              onChange={(e) => setFormData((prev) => ({ ...prev, rating: e.target.value }))}
              required
            />
          </label>

          <label htmlFor={costId} className="space-y-1.5">
            <span className="block text-xs font-medium text-[var(--cms-muted-fg)]">
              千字付费金币数（留空跟随全局）
            </span>
            <input
              id={costId}
              name="coin_cost_per_thousand"
              type="number"
              className="input-field tabular-nums"
              placeholder={`跟随全局（${globalCoinCost} 金币）`}
              aria-describedby={costHintId}
              min={0}
              max={2147483647}
              value={formData.coin_cost_per_thousand}
              onChange={(e) =>
                setFormData((prev) => ({
                  ...prev,
                  coin_cost_per_thousand: e.target.value === '' ? '' : Number(e.target.value),
                }))
              }
            />
            <span id={costHintId} className="block text-xs text-[var(--cms-muted-fg)]">
              当前全局 {globalCoinCost} 金币/千字，输入 0 为全书免费
            </span>
          </label>

          <label htmlFor={startPayId} className="space-y-1.5">
            <span className="block text-xs font-medium text-[var(--cms-muted-fg)]">
              起始收费章节 <span aria-hidden="true" className="text-[var(--cms-error)]">*</span>
            </span>
            <input
              id={startPayId}
              name="start_pay_chapter_index"
              type="number"
              className="input-field tabular-nums"
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
            <span className="block text-xs text-[var(--cms-muted-fg)]">前 N-1 章为免费试读章节</span>
          </label>
        </div>

        {isEditing ? (
          <fieldset className="p-4 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-content)] m-0 space-y-2.5">
            <legend className="text-xs font-medium text-[var(--cms-muted-fg)] px-1">上架发布状态</legend>
            <div className="flex flex-wrap gap-4">
              {[
                { value: 'draft', label: '草稿（待上架）', disabled: false },
                {
                  value: 'published',
                  label: hasChapters || Boolean(chapterFile) ? '已上架（书城可见）' : '已上架（需先导入章节）',
                  disabled: !hasChapters && !chapterFile,
                },
                { value: 'offline', label: '已下架（书城隐藏）', disabled: false },
              ].map((opt) => (
                <label
                  key={opt.value}
                  className={`inline-flex items-center gap-2 text-xs font-medium ${
                    opt.disabled ? 'cursor-not-allowed text-[var(--cms-placeholder)]' : 'cursor-pointer text-[var(--cms-fg)]'
                  }`}
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
          <div className="px-4 py-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] text-xs text-[var(--cms-muted-fg)] leading-relaxed">
            新建书籍默认保存为 <strong className="text-[var(--cms-fg)]">草稿（待上架）</strong>，不会立即暴露在书城；创建并在列表核对无误后再「上架发布」。
          </div>
        )}

        <label htmlFor={synopsisId} className="block space-y-1.5">
          <span className="block text-xs font-medium text-[var(--cms-muted-fg)]">书籍简介</span>
          <textarea
            id={synopsisId}
            name="synopsis"
            className="input-field"
            rows={3}
            placeholder="描述一下小说的主要情节吸引读者..."
            value={formData.synopsis}
            onChange={(e) => setFormData((prev) => ({ ...prev, synopsis: e.target.value }))}
          />
        </label>

        {onChapterImportTypeChange && onChapterFileChange && onChapterRegexChange && (
          <div className="p-4 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-content)] space-y-3">
            <div className="flex items-center justify-between gap-2">
              <div className="text-xs font-semibold text-[var(--cms-fg)] flex items-center gap-2">
                <Upload size={15} aria-hidden="true" className="text-[var(--cms-primary)]" />
                <span>
                  {isEditing ? (
                    '批量导入 / 更新章节手稿（可选）'
                  ) : (
                    <>
                      书籍章节内容上传 <span aria-hidden="true" className="text-[var(--cms-error)]">*</span>
                    </>
                  )}
                </span>
              </div>
              <span className="text-xs text-[var(--cms-muted-fg)]">上限 50MB</span>
            </div>

            <ChapterUploadFields
              importType={chapterImportType}
              onImportTypeChange={onChapterImportTypeChange}
              file={chapterFile}
              onFileChange={onChapterFileChange}
              fileInputRef={chapterFileInputRef}
              uploadBtnRef={chapterUploadBtnRef}
              regex={chapterRegex}
              onRegexChange={onChapterRegexChange}
              regexInputRef={chapterRegexInputRef}
              required={!isEditing}
              disabled={isSubmitting}
              isCreateMode={!isEditing}
              error={chapterFileError}
            />

            {isEditing && onChapterMappingChange && (
              <details className="p-3.5 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-card)] text-xs space-y-2.5">
                <summary className="font-medium text-[var(--cms-fg)] cursor-pointer select-none">
                  章节映射（高级：保留 / 拆分 / 下架章节 ID）
                </summary>
                <p className="text-[var(--cms-muted-fg)] m-0 pt-2 leading-relaxed">
                  正文完全一致的唯一章节会自动保留 ID。改写、拆分或下架章节时，请填写对应 ID，新章节序号从 1 开始。
                </p>
                <label htmlFor={`${formId}-chapter-mapping`} className="block font-medium text-[var(--cms-muted-fg)]">
                  映射 JSON
                </label>
                <textarea
                  id={`${formId}-chapter-mapping`}
                  className="input-field font-mono-code text-xs"
                  rows={3}
                  value={chapterMapping}
                  disabled={isSubmitting}
                  placeholder={'{"keep":{"2":"原章节ID"},"split_from":{"3":"原章节ID"},"retire":[]}'}
                  onChange={(event) => onChapterMappingChange(event.target.value)}
                />
                {onLoadChapterIdentities && (
                  <details
                    onToggle={(event) => {
                      if (event.currentTarget.open) onLoadChapterIdentities();
                    }}
                  >
                    <summary className="font-medium text-[var(--cms-primary)] cursor-pointer">
                      查看现有及已下架的章节 ID
                    </summary>
                    <ul className="mt-2 pl-4 max-h-40 overflow-y-auto space-y-1 text-[var(--cms-muted-fg)]">
                      {chapterIdentities.map((chapter) => (
                        <li key={chapter.id}>
                          {chapter.chapter_index === null ? '已下架' : `第 ${chapter.chapter_index + 1} 章`} ·{' '}
                          {chapter.title} · <code className="text-[var(--cms-fg)]">{chapter.id}</code>
                        </li>
                      ))}
                    </ul>
                  </details>
                )}
              </details>
            )}
          </div>
        )}

        {submitError && (
          <div
            role="status"
            aria-live="polite"
            className="p-3.5 rounded-xl border text-xs flex items-start gap-2"
            style={{
              backgroundColor: 'color-mix(in srgb, var(--cms-error) 10%, transparent)',
              borderColor: 'color-mix(in srgb, var(--cms-error) 28%, transparent)',
              color: 'var(--cms-error)',
            }}
          >
            <AlertCircle size={15} aria-hidden="true" className="shrink-0 mt-0.5" />
            <span>{submitError}</span>
          </div>
        )}
      </form>
    </CmsDialog>
  );
};

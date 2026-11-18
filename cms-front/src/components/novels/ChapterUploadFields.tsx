import React, { useId, useState } from 'react';
import { AlertCircle, CheckCircle2, FileText, HelpCircle, Upload, X } from 'lucide-react';

export const DEFAULT_CHAPTER_DELIMITER_REGEX = `(?i)(chapter\\s+\\d+|第[一二三四五六七八九十百千万零两\\d]+章)`;

interface ChapterUploadFieldsProps {
  importType: 'single_txt' | 'zip';
  onImportTypeChange: (next: 'single_txt' | 'zip') => void;
  file: File | null;
  onFileChange: (file: File | null) => void;
  fileInputRef?: React.RefObject<HTMLInputElement | null>;
  uploadBtnRef?: React.RefObject<HTMLButtonElement | null>;
  regex: string;
  onRegexChange: (val: string) => void;
  regexInputRef?: React.RefObject<HTMLInputElement | null>;
  required?: boolean;
  disabled?: boolean;
  isCreateMode?: boolean;
  error?: string;
}

function splitFilenameParts(name: string): { stem: string; ext: string } {
  const dotIdx = name.lastIndexOf('.');
  if (dotIdx <= 0 || dotIdx === name.length - 1) {
    return { stem: name, ext: '' };
  }
  return { stem: name.slice(0, dotIdx), ext: name.slice(dotIdx) };
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024 * 1024) {
    return `${Math.max(1, Math.round(bytes / 1024))} KB`;
  }
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`;
}

export const ChapterUploadFields: React.FC<ChapterUploadFieldsProps> = ({
  importType,
  onImportTypeChange,
  file,
  onFileChange,
  fileInputRef,
  uploadBtnRef,
  regex,
  onRegexChange,
  regexInputRef,
  required = false,
  disabled = false,
  isCreateMode = false,
  error = '',
}) => {
  const [isDragging, setIsDragging] = useState(false);

  const fileInputId = useId();
  const hintId = useId();
  const errorId = useId();
  const regexInputId = useId();
  const guideId = useId();
  const radioName = useId();
  const localInputRef = React.useRef<HTMLInputElement>(null);
  const resolvedInputRef = fileInputRef || localInputRef;

  const expectedExt = importType === 'single_txt' ? '.txt' : '.zip';

  const clearSelectedFile = () => {
    onFileChange(null);
    if (resolvedInputRef.current) {
      resolvedInputRef.current.value = '';
    }
  };

  const handleSelectedFile = (candidate: File | null | undefined) => {
    if (!candidate) return;
    if (!candidate.name.toLowerCase().endsWith(expectedExt)) {
      window.showToast?.(`当前导入模式仅支持 ${expectedExt} 格式文件`, 'error');
      return;
    }
    onFileChange(candidate);
  };

  const fileParts = file ? splitFilenameParts(file.name) : null;

  return (
    <div className="space-y-4">
      <fieldset className="border-0 p-0 m-0 space-y-2">
        <legend className="block text-xs font-medium text-[var(--cms-muted-fg)]">导入类型</legend>
        <div className="flex flex-wrap gap-5">
          <label className={`inline-flex items-center gap-2 text-xs font-medium text-[var(--cms-fg)] ${disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer'}`}>
            <input
              type="radio"
              name={radioName}
              checked={importType === 'single_txt'}
              disabled={disabled}
              onChange={() => {
                onImportTypeChange('single_txt');
                clearSelectedFile();
              }}
            />
            <span>单个大 TXT 文件 (正则分章)</span>
          </label>
          <label className={`inline-flex items-center gap-2 text-xs font-medium text-[var(--cms-fg)] ${disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer'}`}>
            <input
              type="radio"
              name={radioName}
              checked={importType === 'zip'}
              disabled={disabled}
              onChange={() => {
                onImportTypeChange('zip');
                clearSelectedFile();
              }}
            />
            <span>ZIP 压缩包 (内含多个 TXT)</span>
          </label>
        </div>
      </fieldset>

      <div
        id={guideId}
        className="p-3.5 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-content)] text-xs leading-relaxed text-[var(--cms-muted-fg)]"
      >
        {importType === 'single_txt' ? (
          <div>
            <div className="font-semibold text-[var(--cms-fg)] mb-1">TXT 正则分章说明（UTF-8 编码）</div>
            <ul className="pl-4 m-0 space-y-0.5 list-disc">
              <li>
                每章标题须<strong>单独占一行</strong>且位于行首，例如：<code>Chapter 1: The Start</code>、<code>第一章 初遇</code>、<code>第10章</code>。
              </li>
              <li>
                正则从每行起始位置匹配（含 <code>(?i)</code>），正文句中提及的 <code>Chapter 1</code> 不会误切分。
              </li>
              <li>
                {isCreateMode
                  ? '首个标题行之前的序言或版权页不会入库，匹配标题超出 150 字自动截断。'
                  : '导入会覆盖当前全部已有章节，首个标题行之前的序言或版权页不会入库。'}
              </li>
            </ul>
          </div>
        ) : (
          <div>
            <div className="font-semibold text-[var(--cms-fg)] mb-1">ZIP 压缩包命名规范（UTF-8 编码）</div>
            <ul className="pl-4 m-0 space-y-0.5 list-disc">
              <li>
                章节文件按 <code>数字序号_章节标题.txt</code> 命名（支持 <code>_</code>、<code>-</code> 或空格分隔）。
              </li>
              <li>
                示例：<code>001_The Shattered Promise.txt</code>、<code>002-A New Path.txt</code>、<code>003.txt</code>。
              </li>
              <li>
                {isCreateMode
                  ? '系统按序号升序生成连续章节（单章上限 10MB）。'
                  : '系统按序号升序生成连续章节（单章上限 10MB）；导入会覆盖当前全部已有章节。'}
              </li>
            </ul>
          </div>
        )}
      </div>

      <div className="space-y-2">
        <label htmlFor={fileInputId} className="block text-xs font-medium text-[var(--cms-muted-fg)]">
          选择章节文件 ({expectedExt}){' '}
          {required ? (
            <span aria-hidden="true" className="text-[var(--cms-error)]">*</span>
          ) : isCreateMode ? (
            '（可选）'
          ) : (
            ''
          )}
        </label>

        <div
          className="file-upload-dropzone flex items-center gap-4 p-4 rounded-xl border transition-colors"
          onDragOver={(e) => {
            e.preventDefault();
            if (!disabled) setIsDragging(true);
          }}
          onDragLeave={(e) => {
            e.preventDefault();
            setIsDragging(false);
          }}
          onDrop={(e) => {
            e.preventDefault();
            setIsDragging(false);
            if (!disabled) {
              handleSelectedFile(e.dataTransfer.files?.[0]);
            }
          }}
          style={{
            borderStyle: file && !error && !isDragging ? 'solid' : 'dashed',
            borderColor: error
              ? 'var(--cms-error)'
              : isDragging || file
                ? 'var(--cms-primary)'
                : 'var(--cms-border)',
            backgroundColor: isDragging
              ? 'color-mix(in srgb, var(--cms-primary) 6%, var(--cms-card))'
              : 'var(--cms-card)',
          }}
        >
          <input
            key={importType}
            ref={resolvedInputRef}
            id={fileInputId}
            name="chapter_file"
            type="file"
            className="visually-hidden"
            accept={expectedExt}
            aria-required={required}
            aria-invalid={error ? 'true' : 'false'}
            aria-describedby={error ? `${guideId} ${hintId} ${errorId}` : `${guideId} ${hintId}`}
            disabled={disabled}
            onChange={(e) => handleSelectedFile(e.target.files?.[0])}
          />

          <div
            className={`w-10 h-10 rounded-xl border border-[var(--cms-border)] flex items-center justify-center shrink-0 ${
              file ? 'nav-active-item' : 'bg-[var(--cms-content)] text-[var(--cms-muted-fg)]'
            }`}
          >
            <FileText size={18} aria-hidden="true" />
          </div>

          <div className="flex-1 min-w-0 space-y-1.5">
            <div className="flex items-center gap-2.5 flex-wrap">
              <button
                ref={uploadBtnRef}
                type="button"
                className="btn-secondary h-8 px-3 text-xs"
                disabled={disabled}
                onClick={() => resolvedInputRef.current?.click()}
              >
                <Upload size={13} aria-hidden="true" />
                <span>{file ? '更换章节文件' : `选择 ${expectedExt} 文件`}</span>
              </button>

              {file && (
                <>
                  <span role="status" className="inline-flex items-center gap-1 text-xs font-medium text-[var(--cms-success)]">
                    <CheckCircle2 size={14} aria-hidden="true" />
                    <span>已选文件</span>
                  </span>
                  <button
                    type="button"
                    disabled={disabled}
                    onClick={clearSelectedFile}
                    aria-label="移除已选章节文件"
                    className="focus-ring inline-flex items-center gap-1 px-2 py-1 rounded-md border border-[var(--cms-border)] text-xs text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] cursor-pointer"
                  >
                    <X size={12} aria-hidden="true" />
                    <span>移除</span>
                  </button>
                </>
              )}
            </div>

            {file && fileParts ? (
              <div id={hintId} title={file.name} className="flex items-center gap-2 text-xs text-[var(--cms-fg)] min-w-0">
                <span className="truncate font-medium">{fileParts.stem}{fileParts.ext}</span>
                <span className="text-[var(--cms-muted-fg)] shrink-0 tabular-nums">({formatFileSize(file.size)})</span>
              </div>
            ) : (
              <p id={hintId} className="text-xs text-[var(--cms-muted-fg)] m-0">
                点击按钮选择或拖拽 UTF-8 编码的 <code>{expectedExt}</code> 文件到此处
              </p>
            )}

            {error && (
              <p id={errorId} role="status" aria-live="polite" className="flex items-center gap-1.5 text-xs text-[var(--cms-error)] m-0">
                <AlertCircle size={13} aria-hidden="true" />
                <span>{error}</span>
              </p>
            )}
          </div>
        </div>
      </div>

      {importType === 'single_txt' && (
        <label className="block space-y-1.5">
          <span className="flex items-center gap-1.5 text-xs font-medium text-[var(--cms-muted-fg)]">
            <span>章节匹配分割正则</span>
            <span
              title="正则表达式会从每行非空白起始位置匹配章节标题（含 (?i) 不区分大小写），正文句中出现的关键词不会误切分。"
              className="inline-flex cursor-help"
            >
              <HelpCircle size={13} aria-hidden="true" />
            </span>
          </span>
          <input
            ref={regexInputRef}
            id={regexInputId}
            name="delimiter_regex"
            type="text"
            className="input-field font-mono-code text-xs"
            aria-describedby={guideId}
            value={regex}
            disabled={disabled}
            onChange={(e) => onRegexChange(e.target.value)}
            required={required || Boolean(file)}
          />
        </label>
      )}
    </div>
  );
};

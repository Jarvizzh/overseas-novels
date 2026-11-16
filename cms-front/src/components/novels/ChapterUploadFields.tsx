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
    const lowerName = candidate.name.toLowerCase();
    if (!lowerName.endsWith(expectedExt)) {
      window.showToast?.(`当前导入模式仅支持 ${expectedExt} 格式文件`, 'error');
      return;
    }
    onFileChange(candidate);
  };

  const fileParts = file ? splitFilenameParts(file.name) : null;

  return (
    <div>
      <fieldset style={{ border: 'none', marginBottom: '14px', padding: 0 }}>
        <legend style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '8px' }}>
          导入类型
        </legend>
        <div style={{ display: 'flex', gap: '18px', flexWrap: 'wrap' }}>
          <label style={{ display: 'inline-flex', alignItems: 'center', gap: '6px', fontSize: '0.85rem', cursor: disabled ? 'not-allowed' : 'pointer' }}>
            <input
              type="radio"
              name={radioName}
              checked={importType === 'single_txt'}
              disabled={disabled}
              style={{ accentColor: 'hsl(var(--primary))' }}
              onChange={() => {
                onImportTypeChange('single_txt');
                clearSelectedFile();
              }}
            />
            <span>单个大 TXT 文件 (正则分章)</span>
          </label>
          <label style={{ display: 'inline-flex', alignItems: 'center', gap: '6px', fontSize: '0.85rem', cursor: disabled ? 'not-allowed' : 'pointer' }}>
            <input
              type="radio"
              name={radioName}
              checked={importType === 'zip'}
              disabled={disabled}
              style={{ accentColor: 'hsl(var(--primary))' }}
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
        style={{
          marginBottom: '16px',
          padding: '12px 14px',
          borderRadius: '8px',
          border: '1px solid hsl(var(--border))',
          backgroundColor: 'hsl(var(--bg-base) / 0.7)',
          fontSize: '0.78rem',
          lineHeight: 1.6,
          color: 'hsl(var(--text-secondary))',
        }}
      >
        {importType === 'single_txt' ? (
          <div>
            <div style={{ fontWeight: 600, color: 'hsl(var(--text-primary))', marginBottom: '4px' }}>
              TXT 正则分章说明（UTF-8 编码）
            </div>
            <ul style={{ paddingLeft: '16px', margin: 0 }}>
              <li>
                每章标题须<strong>单独占一行</strong>且位于行首（允许行首空格），例如：<code>Chapter 1: The Start</code>、<code>chapter 2</code>、<code>第一章 初遇</code>、<code>第10章</code>。
              </li>
              <li>
                正则从每行起始位置匹配（默认含 <code>(?i)</code> 不区分大小写），正文句子中提及的 <code>Chapter 1</code> 不会误切分。
              </li>
              <li>
                {isCreateMode
                  ? '整行匹配标题将保留为章节名（超出 150 字自动截断），首个标题行之前的序言/版权页不会入库。'
                  : '整行匹配标题将保留为章节名（超出 150 字自动截断），首个标题行之前的序言/版权页不会入库；导入会覆盖当前全部已有章节。'}
              </li>
            </ul>
          </div>
        ) : (
          <div>
            <div style={{ fontWeight: 600, color: 'hsl(var(--text-primary))', marginBottom: '4px' }}>
              ZIP 压缩包文件命名规范（UTF-8 编码）
            </div>
            <ul style={{ paddingLeft: '16px', margin: 0 }}>
              <li>
                包内章节文件须按 <code>数字序号_章节标题.txt</code> 命名（支持 <code>_</code>、<code>-</code> 或空格分隔，后缀不区分大小写）。
              </li>
              <li>
                示例：<code>001_The Shattered Promise.txt</code>、<code>002-A New Path.txt</code>、<code>003.txt</code>（省略标题时默认命名为 <code>Chapter 序号</code>）。
              </li>
              <li>
                {isCreateMode
                  ? '系统按文件名前缀数字从小到大排序生成连续章节（单章 TXT 上限 10MB）。'
                  : '系统按文件名前缀数字从小到大排序生成连续章节（单章 TXT 上限 10MB）；导入会覆盖当前全部已有章节。'}
              </li>
            </ul>
          </div>
        )}
      </div>

      <div style={{ marginBottom: importType === 'single_txt' ? '16px' : 0 }}>
        <label
          htmlFor={fileInputId}
          style={{
            display: 'block',
            fontSize: '0.8rem',
            lineHeight: 1.5,
            color: 'hsl(var(--text-secondary))',
            marginBottom: '8px',
          }}
        >
          选择章节文件 ({expectedExt}) {required ? <span aria-hidden="true" style={{ color: '#ef4444' }}>*</span> : (isCreateMode ? '（可选，也可创建后在详情页补传）' : '')}
        </label>

        <div
          className="file-upload-dropzone"
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
            display: 'flex',
            alignItems: 'center',
            gap: '14px',
            padding: '14px 16px',
            borderRadius: '10px',
            border: error
              ? '1px dashed #ef4444'
              : isDragging
                ? '1px dashed hsl(var(--primary))'
                : file
                  ? '1px solid hsl(var(--primary) / 0.4)'
                  : '1px dashed hsl(var(--border))',
            backgroundColor: isDragging
              ? 'hsl(var(--primary) / 0.06)'
              : 'hsl(var(--bg-surface) / 0.7)',
            transition: 'border-color 0.2s ease, background-color 0.2s ease',
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
            style={{
              width: '42px',
              height: '42px',
              borderRadius: '10px',
              backgroundColor: file ? 'hsl(var(--primary) / 0.14)' : 'hsl(var(--bg-card))',
              border: '1px solid hsl(var(--border))',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: file ? 'hsl(var(--primary))' : 'hsl(var(--text-muted))',
              flexShrink: 0,
            }}
          >
            <FileText size={20} aria-hidden="true" />
          </div>

          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px', flexWrap: 'wrap', marginBottom: '6px' }}>
              <button
                ref={uploadBtnRef}
                type="button"
                className="btn-secondary"
                disabled={disabled}
                onClick={() => resolvedInputRef.current?.click()}
                style={{ gap: '6px', padding: '6px 12px', fontSize: '0.8rem' }}
              >
                <Upload size={14} aria-hidden="true" />
                <span>{file ? '更换章节文件' : `选择 ${expectedExt} 文件`}</span>
              </button>

              {file && fileParts && (
                <>
                  <span
                    role="status"
                    style={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      gap: '4px',
                      fontSize: '0.78rem',
                      color: 'hsl(var(--accent-green))',
                      fontWeight: 500,
                    }}
                  >
                    <CheckCircle2 size={14} aria-hidden="true" />
                    <span>已选文件</span>
                  </span>
                  <button
                    type="button"
                    disabled={disabled}
                    onClick={clearSelectedFile}
                    aria-label="移除已选章节文件"
                    style={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      gap: '3px',
                      padding: '3px 8px',
                      borderRadius: '6px',
                      fontSize: '0.75rem',
                      color: 'hsl(var(--text-secondary))',
                      border: '1px solid hsl(var(--border))',
                      cursor: disabled ? 'not-allowed' : 'pointer',
                    }}
                  >
                    <X size={12} aria-hidden="true" />
                    <span>移除</span>
                  </button>
                </>
              )}
            </div>

            {file && fileParts ? (
              <div
                id={hintId}
                title={file.name}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '8px',
                  fontSize: '0.78rem',
                  color: 'hsl(var(--text-primary))',
                  minWidth: 0,
                }}
              >
                <span style={{ display: 'flex', whiteSpace: 'nowrap', minWidth: 0, flex: '0 1 auto' }}>
                  <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{fileParts.stem}</span>
                  <span>{fileParts.ext}</span>
                </span>
                <span style={{ color: 'hsl(var(--text-muted))', flexShrink: 0 }}>
                  ({formatFileSize(file.size)})
                </span>
              </div>
            ) : (
              <p id={hintId} style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))', lineHeight: 1.5, margin: 0 }}>
                点击按钮选择文件，或直接将 UTF-8 编码的 <code>{expectedExt}</code> 文件拖拽到此处
              </p>
            )}

            {error && (
              <p
                id={errorId}
                role="status"
                aria-live="polite"
                style={{ display: 'flex', alignItems: 'center', gap: '4px', fontSize: '0.78rem', color: '#ef4444', marginTop: '6px' }}
              >
                <AlertCircle size={14} aria-hidden="true" />
                <span>{error}</span>
              </p>
            )}
          </div>
        </div>
      </div>

      {importType === 'single_txt' && (
        <div>
          <label htmlFor={regexInputId} style={{ display: 'flex', alignItems: 'center', gap: '4px', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
            章节匹配分割正则
            <span title="正则表达式会从每行非空白起始位置匹配章节标题（含 (?i) 不区分大小写），正文句中出现的关键词不会误切分。" style={{ display: 'inline-flex', cursor: 'help' }}>
              <HelpCircle size={12} aria-hidden="true" />
            </span>
          </label>
          <input
            ref={regexInputRef}
            id={regexInputId}
            name="delimiter_regex"
            type="text"
            className="input-field"
            aria-describedby={guideId}
            value={regex}
            disabled={disabled}
            onChange={(e) => onRegexChange(e.target.value)}
            required={required || Boolean(file)}
          />
        </div>
      )}
    </div>
  );
};

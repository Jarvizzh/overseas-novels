import React, { useId } from 'react';
import { FileText, HelpCircle, X } from 'lucide-react';

export const DEFAULT_CHAPTER_DELIMITER_REGEX = `(?i)(chapter\\s+\\d+|第[一二三四五六七八九十百千万零两\\d]+章)`;

interface ChapterUploadFieldsProps {
  importType: 'single_txt' | 'zip';
  onImportTypeChange: (next: 'single_txt' | 'zip') => void;
  file: File | null;
  onFileChange: (file: File | null) => void;
  fileInputRef?: React.RefObject<HTMLInputElement | null>;
  regex: string;
  onRegexChange: (val: string) => void;
  regexInputRef?: React.RefObject<HTMLInputElement | null>;
  required?: boolean;
  disabled?: boolean;
  isCreateMode?: boolean;
}

export const ChapterUploadFields: React.FC<ChapterUploadFieldsProps> = ({
  importType,
  onImportTypeChange,
  file,
  onFileChange,
  fileInputRef,
  regex,
  onRegexChange,
  regexInputRef,
  required = false,
  disabled = false,
  isCreateMode = false,
}) => {
  const fileInputId = useId();
  const regexInputId = useId();
  const guideId = useId();
  const radioName = useId();

  const clearSelectedFile = () => {
    onFileChange(null);
    if (fileInputRef?.current) {
      fileInputRef.current.value = '';
    }
  };

  return (
    <div>
      <fieldset style={{ border: 'none', marginBottom: '14px', padding: 0 }}>
        <legend style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
          导入类型
        </legend>
        <div style={{ display: 'flex', gap: '16px', flexWrap: 'wrap' }}>
          <label style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.85rem', cursor: disabled ? 'not-allowed' : 'pointer' }}>
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
            单个大 TXT 文件 (正则分章)
          </label>
          <label style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.85rem', cursor: disabled ? 'not-allowed' : 'pointer' }}>
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
            ZIP 压缩包 (内含多个 TXT)
          </label>
        </div>
      </fieldset>

      <div
        id={guideId}
        style={{
          marginBottom: '14px',
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

      <div style={{ marginBottom: importType === 'single_txt' ? '14px' : 0 }}>
        <label htmlFor={fileInputId} style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
          选择章节文件 ({importType === 'single_txt' ? '.txt' : '.zip'}) {isCreateMode && !required ? '（可选，也可创建后在详情页补传）' : ''}
        </label>
        <input
          key={importType}
          ref={fileInputRef}
          id={fileInputId}
          name="chapter_file"
          type="file"
          accept={importType === 'single_txt' ? '.txt' : '.zip'}
          aria-describedby={guideId}
          disabled={disabled}
          onChange={(e) => onFileChange(e.target.files ? e.target.files[0] : null)}
          required={required}
          style={{
            width: '100%',
            padding: '12px',
            border: '1px dashed hsl(var(--border))',
            borderRadius: '8px',
            backgroundColor: 'hsl(var(--bg-card))',
            cursor: disabled ? 'not-allowed' : 'pointer',
          }}
        />
        {file && (
          <div
            style={{
              marginTop: '8px',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: '8px',
              padding: '6px 10px',
              borderRadius: '6px',
              backgroundColor: 'hsl(var(--primary) / 0.08)',
              fontSize: '0.78rem',
              color: 'hsl(var(--text-primary))',
            }}
          >
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: '6px', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              <FileText size={14} aria-hidden="true" style={{ flexShrink: 0, color: 'hsl(var(--primary))' }} />
              <span title={file.name} style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {file.name} ({(file.size / 1024 / 1024).toFixed(2)} MB)
              </span>
            </span>
            <button
              type="button"
              disabled={disabled}
              onClick={clearSelectedFile}
              aria-label="移除已选章节文件"
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: '2px',
                padding: '2px 6px',
                borderRadius: '4px',
                color: 'hsl(var(--text-secondary))',
                cursor: disabled ? 'not-allowed' : 'pointer',
                flexShrink: 0,
              }}
            >
              <X size={13} aria-hidden="true" />
              <span>清除</span>
            </button>
          </div>
        )}
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

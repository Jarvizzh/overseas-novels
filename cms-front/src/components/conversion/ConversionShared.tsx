import { useState } from 'react';
import { AlertTriangle, Check, Copy, RefreshCw, ServerOff } from 'lucide-react';
import { StatusDot } from '../ui/Primitives';
import { copyTextToClipboard } from '../../utils/clipboard';
import { getStatusMeta } from '../../utils/conversionStatus';

export function ConversionStatusDot({
  code,
  className = '',
}: {
  code: string;
  className?: string;
}) {
  const meta = getStatusMeta(code);
  return <StatusDot tone={meta.tone} label={meta.label} className={className} />;
}

export function CopyableText({
  value,
  label = '编号',
  displayValue,
  mono = true,
  className = '',
}: {
  value: string;
  label?: string;
  displayValue?: string;
  mono?: boolean;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async (e: React.MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation();
    const ok = await copyTextToClipboard(value);
    if (ok) {
      setCopied(true);
      window.showToast?.(`${label}已复制到剪贴板`, 'success');
      window.setTimeout(() => setCopied(false), 1600);
    } else {
      window.showToast?.('复制失败，请手动选中复制', 'error');
    }
  };

  return (
    <span className={`inline-flex items-center gap-1 min-w-0 ${className}`}>
      <span
        className={`${mono ? 'font-mono-code' : 'tabular-nums'} truncate`}
        title={value}
      >
        {displayValue ?? value}
      </span>
      <button
        type="button"
        onClick={(e) => void handleCopy(e)}
        aria-label={`复制${label} ${value}`}
        title={`复制${label}`}
        className="focus-ring shrink-0 w-5 h-5 rounded hover:bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] inline-flex items-center justify-center cursor-pointer transition-colors"
      >
        {copied ? (
          <Check size={11} className="text-[var(--cms-success)]" aria-hidden="true" />
        ) : (
          <Copy size={11} aria-hidden="true" />
        )}
      </button>
    </span>
  );
}

export function ServiceUnavailableState({
  title = '统一转化服务暂不可用',
  message = 'CMS 未连接或暂时无法访问 conversion-backend 私网接口，无法读取实时回传事件与对账数据。',
  retrying = false,
  onRetry,
}: {
  title?: string;
  message?: string;
  retrying?: boolean;
  onRetry?: () => void;
}) {
  return (
    <div className="cms-card p-8 text-center space-y-3">
      <div className="w-10 h-10 rounded-full mx-auto bg-[var(--cms-secondary)] text-[var(--cms-warning)] inline-flex items-center justify-center">
        <ServerOff size={18} aria-hidden="true" />
      </div>
      <div className="space-y-1 max-w-lg mx-auto">
        <h3 className="text-sm font-semibold text-[var(--cms-fg)] m-0">{title}</h3>
        <p className="text-xs text-[var(--cms-muted-fg)] m-0 leading-relaxed">{message}</p>
        <p className="text-xs text-[var(--cms-muted-fg)] m-0">
          请确认 <code className="font-mono-code">conversion --mode=api</code> 已启动且{' '}
          <code className="font-mono-code">CONVERSION_PRIVATE_URL</code> /{' '}
          <code className="font-mono-code">CONVERSION_PRIVATE_TOKEN</code> 配置正确。
        </p>
      </div>
      <div className="flex flex-wrap items-center justify-center gap-2 pt-1">
        {onRetry && (
          <button
            type="button"
            disabled={retrying}
            onClick={onRetry}
            className="btn-secondary h-8 px-3 text-xs"
          >
            <RefreshCw size={13} className={retrying ? 'animate-spin' : ''} aria-hidden="true" />
            <span>{retrying ? '正在检测…' : '重新检测'}</span>
          </button>
        )}
      </div>
    </div>
  );
}

export function InlineErrorBanner({
  message,
  onRetry,
}: {
  message: string;
  onRetry?: () => void;
}) {
  return (
    <div
      role="alert"
      className="p-3.5 rounded-xl border text-xs flex flex-wrap items-center justify-between gap-2"
      style={{
        backgroundColor: 'color-mix(in srgb, var(--cms-error) 8%, transparent)',
        borderColor: 'color-mix(in srgb, var(--cms-error) 25%, transparent)',
        color: 'var(--cms-error)',
      }}
    >
      <span className="inline-flex items-center gap-2">
        <AlertTriangle size={14} aria-hidden="true" />
        <span>{message}</span>
      </span>
      {onRetry && (
        <button type="button" onClick={onRetry} className="btn-ghost h-7 px-2 text-xs">
          重试
        </button>
      )}
    </div>
  );
}

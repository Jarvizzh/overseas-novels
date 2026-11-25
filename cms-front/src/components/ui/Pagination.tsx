import React, { useEffect, useRef, useState } from 'react';
import { ChevronLeft, ChevronRight, ChevronsLeft } from 'lucide-react';

interface PaginationProps {
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (nextPage: number) => void;
  onPageSizeChange?: (nextPageSize: number) => void;
  pageSizeOptions?: number[];
  disabled?: boolean;
  itemLabel?: string;
  insideCard?: boolean;
}

const DEFAULT_PAGE_SIZE_OPTIONS = [10, 20, 50, 100];

const footerClass = 'pt-0.5 flex flex-wrap items-center justify-between gap-3 text-xs text-[var(--cms-muted-fg)]';
const arrowClass =
  'focus-ring w-7 h-7 rounded-lg hover:bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] inline-flex items-center justify-center cursor-pointer disabled:opacity-30 disabled:cursor-not-allowed transition-colors';

export function CursorPagination({
  page,
  count,
  hasNext,
  onFirst,
  onPrevious,
  onNext,
}: {
  page: number;
  count: number;
  hasNext: boolean;
  onFirst: () => void;
  onPrevious: () => void;
  onNext: () => void;
}) {
  return (
    <div className={footerClass}>
      <span className="tabular-nums">本页 {count} 条</span>
      <nav aria-label="分页导航" className="flex items-center gap-1">
        <button type="button" aria-label="首页" className={arrowClass} disabled={page <= 1} onClick={onFirst}>
          <ChevronsLeft size={14} aria-hidden="true" />
        </button>
        <button type="button" aria-label="上一页" className={arrowClass} disabled={page <= 1} onClick={onPrevious}>
          <ChevronLeft size={14} aria-hidden="true" />
        </button>
        <span
          aria-current="page"
          aria-label={`第 ${page} 页`}
          className="cta-primary min-w-7 h-7 px-2 rounded-lg inline-flex items-center justify-center font-medium tabular-nums"
        >
          {page}
        </span>
        <button type="button" aria-label="下一页" className={arrowClass} disabled={!hasNext} onClick={onNext}>
          <ChevronRight size={14} aria-hidden="true" />
        </button>
      </nav>
    </div>
  );
}

function resolvePageSizeOptions(pageSize: number, options: number[] = DEFAULT_PAGE_SIZE_OPTIONS): number[] {
  const merged = new Set(options.filter((n) => Number.isInteger(n) && n > 0));
  if (Number.isInteger(pageSize) && pageSize > 0) {
    merged.add(pageSize);
  }
  return Array.from(merged).sort((a, b) => a - b);
}

function getPageNumbers(current: number, totalPages: number): Array<number | 'ellipsis'> {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const pages: Array<number | 'ellipsis'> = [1];
  const start = Math.max(2, current - 1);
  const end = Math.min(totalPages - 1, current + 1);

  if (start > 2) pages.push('ellipsis');
  for (let p = start; p <= end; p++) pages.push(p);
  if (end < totalPages - 1) pages.push('ellipsis');
  pages.push(totalPages);
  return pages;
}

export const Pagination: React.FC<PaginationProps> = ({
  page,
  pageSize,
  total,
  onPageChange,
  onPageSizeChange,
  pageSizeOptions,
  disabled = false,
  itemLabel = '条记录',
  insideCard = false,
}) => {
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const safePage = Math.min(Math.max(1, page), totalPages);
  const startItem = total === 0 ? 0 : (safePage - 1) * pageSize + 1;
  const endItem = Math.min(safePage * pageSize, total);
  const pageNumbers = getPageNumbers(safePage, totalPages);
  const resolvedSizeOptions = resolvePageSizeOptions(pageSize, pageSizeOptions);
  const [jumpInput, setJumpInput] = useState('');

  const onPageChangeRef = useRef(onPageChange);
  useEffect(() => {
    onPageChangeRef.current = onPageChange;
  }, [onPageChange]);

  useEffect(() => {
    if (!disabled && total > 0 && page > totalPages) {
      onPageChangeRef.current(totalPages);
    }
  }, [disabled, total, page, totalPages]);

  if (total === 0) return null;

  const handleJumpSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const parsed = Number(jumpInput.trim());
    if (!Number.isInteger(parsed) || parsed < 1) return;
    const clamped = Math.min(parsed, totalPages);
    setJumpInput('');
    if (clamped !== safePage) {
      onPageChange(clamped);
    }
  };

  return (
    <div
      className={
        insideCard
          ? 'px-4 py-2.5 border-t border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-2 bg-[var(--cms-card)] text-xs text-[var(--cms-muted-fg)]'
          : footerClass
      }
    >
      <div className="tabular-nums">
        显示 <span className="font-medium text-[var(--cms-fg)]">{startItem}–{endItem}</span> 条，共{' '}
        <span className="font-medium text-[var(--cms-fg)]">{total}</span> {itemLabel}
      </div>

      {(totalPages > 1 || Boolean(onPageSizeChange)) && (
        <div className="flex flex-wrap items-center gap-2.5">
          {onPageSizeChange && (
            <label className="inline-flex items-center gap-1.5">
              <span>每页</span>
              <select
                aria-label="每页显示条数"
                disabled={disabled}
                value={pageSize}
                onChange={(e) => {
                  const nextSize = Number(e.target.value);
                  if (Number.isInteger(nextSize) && nextSize > 0 && nextSize !== pageSize) {
                    onPageSizeChange(nextSize);
                  }
                }}
                className="focus-ring h-7 px-2 rounded-lg border border-[var(--cms-border)] bg-[var(--cms-card)] text-xs text-[var(--cms-fg)] tabular-nums cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
              >
                {resolvedSizeOptions.map((opt) => (
                  <option key={opt} value={opt}>
                    {opt} 条
                  </option>
                ))}
              </select>
            </label>
          )}

          {totalPages > 1 && (
            <>
              <nav aria-label="分页导航" className="flex items-center gap-1">
            <button
              type="button"
              disabled={disabled || safePage <= 1}
              onClick={() => onPageChange(Math.max(1, safePage - 1))}
              aria-label="上一页"
              className={arrowClass}
            >
              <ChevronLeft size={14} aria-hidden="true" />
            </button>

            <div className="flex items-center gap-1">
              {pageNumbers.map((item, idx) =>
                item === 'ellipsis' ? (
                  <span key={`e-${idx}`} className="px-1 text-[var(--cms-muted-fg)]">
                    …
                  </span>
                ) : (
                  <button
                    key={item}
                    type="button"
                    disabled={disabled}
                    onClick={() => onPageChange(item)}
                    aria-current={item === safePage ? 'page' : undefined}
                    className={`focus-ring min-w-7 h-7 px-2 rounded-lg text-xs tabular-nums cursor-pointer transition-colors ${
                      item === safePage
                        ? 'cta-primary font-medium'
                        : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)]'
                    }`}
                  >
                    {item}
                  </button>
                ),
              )}
            </div>

            <button
              type="button"
              disabled={disabled || safePage >= totalPages}
              onClick={() => onPageChange(Math.min(totalPages, safePage + 1))}
              aria-label="下一页"
              className={arrowClass}
            >
              <ChevronRight size={14} aria-hidden="true" />
            </button>
          </nav>

              {!insideCard && totalPages > 5 && (
                <form
                  onSubmit={handleJumpSubmit}
                  className="hidden sm:inline-flex items-center gap-1.5 pl-2.5 border-l border-[var(--cms-border)]"
                >
                  <span>跳至</span>
                  <input
                    type="number"
                    min={1}
                    max={totalPages}
                    aria-label="跳转到页码"
                    value={jumpInput}
                    onChange={(e) => setJumpInput(e.target.value)}
                    className="focus-ring w-11 h-7 px-1.5 rounded-lg border border-[var(--cms-border)] bg-[var(--cms-card)] text-xs text-[var(--cms-fg)] text-center tabular-nums outline-none"
                  />
                  <span>页</span>
                </form>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
};

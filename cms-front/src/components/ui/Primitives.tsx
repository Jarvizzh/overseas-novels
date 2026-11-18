import React, { useId, useRef } from 'react';
import { Columns3, MoreHorizontal, Search, X } from 'lucide-react';

type DotTone = 'success' | 'warning' | 'error' | 'primary' | 'muted';

const TONE_TEXT_COLOR: Record<DotTone, string> = {
  success: 'text-[var(--cms-success)]',
  warning: 'text-[var(--cms-warning)]',
  error: 'text-[var(--cms-error)]',
  primary: 'text-[var(--cms-primary)]',
  muted: 'text-[var(--cms-muted-fg)]',
};

const TONE_DOT_COLOR: Record<DotTone, string> = {
  success: 'bg-[var(--cms-success)]',
  warning: 'bg-[var(--cms-warning)]',
  error: 'bg-[var(--cms-error)]',
  primary: 'bg-[var(--cms-primary)]',
  muted: 'bg-[var(--cms-placeholder)]',
};

export const StatusDot: React.FC<{ tone: DotTone; label: string; className?: string }> = ({
  tone,
  label,
  className = '',
}) => (
  <span className={`inline-flex items-center gap-2 text-xs font-normal whitespace-nowrap ${TONE_TEXT_COLOR[tone]} ${className}`}>
    <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${TONE_DOT_COLOR[tone]}`} aria-hidden="true" />
    <span>{label}</span>
  </span>
);

interface TableRegionProps {
  ariaLabel: string;
  children: React.ReactNode;
  footer?: React.ReactNode;
  className?: string;
}

export const TableRegion: React.FC<TableRegionProps> = ({
  ariaLabel,
  children,
  footer,
  className = '',
}) => (
  <div className={`space-y-3 ${className}`}>
    <div className="bg-[var(--cms-card)] border border-[var(--cms-border)] rounded-2xl overflow-hidden">
      <div tabIndex={0} role="region" aria-label={ariaLabel} className="table-region">
        {children}
      </div>
    </div>
    {footer}
  </div>
);

export type PopoverAlign = 'end' | 'start' | 'top-start';

function useAnchoredPopover(align: PopoverAlign = 'end') {
  const uid = useId().replace(/:/g, '');
  const popoverId = `pop-${uid}`;
  const anchorName = `--anchor-${uid}`;
  const triggerRef = useRef<HTMLButtonElement>(null);
  const popRef = useRef<HTMLDivElement>(null);

  const handleToggle = (e: React.ToggleEvent<HTMLDivElement>) => {
    const pop = popRef.current;
    const trigger = triggerRef.current;
    if (!pop || !trigger || e.newState !== 'open') return;
    if (typeof CSS !== 'undefined' && CSS.supports('position-area', 'block-end')) return;

    const tr = trigger.getBoundingClientRect();
    const pr = pop.getBoundingClientRect();
    const leftRaw = align === 'end' ? tr.right - pr.width : tr.left;
    const left = Math.max(12, Math.min(leftRaw, window.innerWidth - pr.width - 12));

    let top: number;
    if (align === 'top-start') {
      top = tr.top - 6 - pr.height >= 12 ? tr.top - 6 - pr.height : Math.min(window.innerHeight - pr.height - 12, tr.bottom + 6);
    } else {
      const fitsBelow = tr.bottom + 6 + pr.height <= window.innerHeight - 12;
      top = fitsBelow ? tr.bottom + 6 : Math.max(12, tr.top - pr.height - 6);
    }

    pop.style.inset = 'auto';
    pop.style.margin = '0';
    pop.style.left = `${left}px`;
    pop.style.top = `${top}px`;
  };

  const hidePopover = () => {
    popRef.current?.hidePopover();
  };

  const anchorClass =
    align === 'start'
      ? 'anchored-start-popover'
      : align === 'top-start'
        ? 'anchored-top-start-popover'
        : 'anchored-action-popover';

  return { popoverId, anchorName, triggerRef, popRef, handleToggle, hidePopover, anchorClass };
}

export interface RowActionItem {
  label: string;
  onClick: () => void;
  tone?: 'default' | 'success' | 'warning' | 'danger';
  dividerBefore?: boolean;
  disabled?: boolean;
}

export const RowActionMenu: React.FC<{
  items: RowActionItem[];
  ariaLabel?: string;
}> = ({ items, ariaLabel = '更多操作' }) => {
  const { popoverId, anchorName, triggerRef, popRef, handleToggle, hidePopover, anchorClass } =
    useAnchoredPopover('end');

  const visibleItems = items.filter(Boolean);
  if (visibleItems.length === 0) return null;

  return (
    <div className="inline-block">
      <button
        ref={triggerRef}
        type="button"
        popoverTarget={popoverId}
        aria-label={ariaLabel}
        style={{ anchorName } as React.CSSProperties}
        className="row-actions-secondary focus-ring w-8 h-8 rounded-lg hover:bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] inline-flex items-center justify-center cursor-pointer transition-colors"
      >
        <MoreHorizontal size={16} aria-hidden="true" />
      </button>

      <div
        ref={popRef}
        id={popoverId}
        popover="auto"
        onToggle={handleToggle}
        style={{ positionAnchor: anchorName } as React.CSSProperties}
        className={`cms-popover ${anchorClass} w-44 text-left text-xs space-y-0.5`}
      >
        {visibleItems.map((item, idx) => (
          <React.Fragment key={`${item.label}-${idx}`}>
            {item.dividerBefore && <div className="my-1 border-t border-[var(--cms-border)]" />}
            <button
              type="button"
              disabled={item.disabled}
              onClick={() => {
                hidePopover();
                item.onClick();
              }}
              className={`focus-ring w-full text-left px-3 py-2 rounded-lg cursor-pointer transition-colors font-medium disabled:opacity-40 disabled:cursor-not-allowed ${
                item.tone === 'danger'
                  ? 'text-[var(--cms-error)] hover:bg-[color-mix(in_srgb,var(--cms-error)_12%,transparent)]'
                  : item.tone === 'warning'
                    ? 'text-[var(--cms-warning)] hover:bg-[var(--cms-secondary)]'
                    : item.tone === 'success'
                      ? 'text-[var(--cms-success)] hover:bg-[var(--cms-secondary)]'
                      : 'text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)]'
              }`}
            >
              {item.label}
            </button>
          </React.Fragment>
        ))}
      </div>
    </div>
  );
};

interface ToolbarPopoverProps {
  label: React.ReactNode;
  icon?: React.ReactNode;
  badgeCount?: number;
  align?: PopoverAlign;
  triggerClassName?: string;
  panelClassName?: string;
  ariaLabel?: string;
  children: (helpers: { close: () => void }) => React.ReactNode;
}

export const ToolbarPopover: React.FC<ToolbarPopoverProps> = ({
  label,
  icon,
  badgeCount = 0,
  align = 'end',
  triggerClassName = 'h-9 px-3 rounded-[10px] border border-[var(--cms-border)] bg-[var(--cms-card)] hover:bg-[var(--cms-content)] text-xs font-medium text-[var(--cms-fg)]',
  panelClassName = 'w-72 p-4',
  ariaLabel,
  children,
}) => {
  const { popoverId, anchorName, triggerRef, popRef, handleToggle, hidePopover, anchorClass } =
    useAnchoredPopover(align);

  return (
    <div className="inline-block shrink-0">
      <button
        ref={triggerRef}
        type="button"
        popoverTarget={popoverId}
        aria-label={ariaLabel}
        style={{ anchorName } as React.CSSProperties}
        className={`focus-ring inline-flex items-center gap-1.5 cursor-pointer transition-colors ${triggerClassName}`}
      >
        {icon}
        <span>{label}</span>
        {badgeCount > 0 && (
          <span className="min-w-[18px] h-[18px] px-1 rounded-full cta-primary text-[11px] font-semibold tabular-nums inline-flex items-center justify-center leading-none">
            {badgeCount}
          </span>
        )}
      </button>

      <div
        ref={popRef}
        id={popoverId}
        popover="auto"
        onToggle={handleToggle}
        style={{ positionAnchor: anchorName } as React.CSSProperties}
        className={`cms-popover ${anchorClass} text-left text-xs ${panelClassName}`}
      >
        {children({ close: hidePopover })}
      </div>
    </div>
  );
};

export type TableDensity = 'comfortable' | 'compact';

export interface ColumnOption {
  key: string;
  label: string;
  visible: boolean;
}

export const ViewOptionsPopover: React.FC<{
  density: TableDensity;
  onDensityChange: (density: TableDensity) => void;
  columns?: ColumnOption[];
  onToggleColumn?: (key: string) => void;
  align?: PopoverAlign;
}> = ({ density, onDensityChange, columns, onToggleColumn, align = 'end' }) => (
  <ToolbarPopover
    align={align}
    label="视图选项"
    ariaLabel="视图选项"
    icon={<Columns3 size={14} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />}
    triggerClassName="h-9 px-2.5 rounded-lg hover:bg-[var(--cms-secondary)] text-xs font-medium text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]"
    panelClassName="w-52 p-3 space-y-3"
  >
    {() => (
      <>
        <div className="space-y-1.5">
          <div className="px-1 font-medium text-[var(--cms-muted-fg)]">行高密度</div>
          <div className="grid grid-cols-2 gap-1 bg-[var(--cms-content)] p-1 rounded-lg">
            <button
              type="button"
              onClick={() => onDensityChange('comfortable')}
              className={`focus-ring py-1 rounded-md text-xs cursor-pointer transition-colors ${
                density === 'comfortable'
                  ? 'bg-[var(--cms-card)] text-[var(--cms-fg)] font-medium shadow-xs'
                  : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
              }`}
            >
              舒展
            </button>
            <button
              type="button"
              onClick={() => onDensityChange('compact')}
              className={`focus-ring py-1 rounded-md text-xs cursor-pointer transition-colors ${
                density === 'compact'
                  ? 'bg-[var(--cms-card)] text-[var(--cms-fg)] font-medium shadow-xs'
                  : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
              }`}
            >
              紧凑
            </button>
          </div>
        </div>

        {columns && columns.length > 0 && onToggleColumn && (
          <div className="border-t border-[var(--cms-border)] pt-2.5 space-y-1">
            <div className="px-1 font-medium text-[var(--cms-muted-fg)]">显示字段</div>
            {columns.map((col) => (
              <label
                key={col.key}
                className="flex items-center gap-2 px-1.5 py-1 rounded-md hover:bg-[var(--cms-secondary)] cursor-pointer text-[var(--cms-fg)]"
              >
                <input
                  type="checkbox"
                  checked={col.visible}
                  onChange={() => onToggleColumn(col.key)}
                  className="w-3.5 h-3.5 cursor-pointer rounded"
                />
                <span>{col.label}</span>
              </label>
            ))}
          </div>
        )}
      </>
    )}
  </ToolbarPopover>
);

export interface FilterChip {
  key: string;
  label: string;
  onClear: () => void;
}

export const FilterChipsBar: React.FC<{
  chips: FilterChip[];
  onResetAll?: () => void;
}> = ({ chips, onResetAll }) => {
  if (chips.length === 0) return null;

  return (
    <div className="flex flex-wrap items-center gap-2 text-xs">
      {chips.map((chip) => (
        <button
          key={chip.key}
          type="button"
          onClick={chip.onClear}
          className="focus-ring inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-[var(--cms-secondary)] text-[var(--cms-fg)] hover:opacity-80 cursor-pointer transition-opacity"
        >
          <span>{chip.label}</span>
          <X size={12} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />
        </button>
      ))}
      {onResetAll && chips.length > 1 && (
        <button
          type="button"
          onClick={onResetAll}
          className="focus-ring px-2 py-1 rounded-md text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] cursor-pointer transition-colors"
        >
          清空全部
        </button>
      )}
    </div>
  );
};

interface SearchInputProps {
  value: string;
  onChange: (value: string) => void;
  onClear?: () => void;
  placeholder?: string;
  ariaLabel?: string;
  className?: string;
}

export const SearchInput: React.FC<SearchInputProps> = ({
  value,
  onChange,
  onClear,
  placeholder = '搜索...',
  ariaLabel,
  className = '',
}) => (
  <div className={`relative ${className}`}>
    <Search
      size={14}
      aria-hidden="true"
      className="text-[var(--cms-placeholder)] absolute left-3 top-1/2 -translate-y-1/2 pointer-events-none"
    />
    <input
      type="search"
      aria-label={ariaLabel || placeholder}
      className="input-field"
      style={{ paddingLeft: '32px', paddingRight: value ? '30px' : '12px' }}
      placeholder={placeholder}
      autoCorrect="off"
      autoCapitalize="none"
      spellCheck={false}
      enterKeyHint="search"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
    {value && (
      <button
        type="button"
        aria-label="清除搜索"
        onClick={() => {
          onChange('');
          onClear?.();
        }}
        className="focus-ring absolute right-2 top-1/2 -translate-y-1/2 w-5 h-5 rounded-full text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] inline-flex items-center justify-center cursor-pointer transition-colors"
      >
        <X size={12} aria-hidden="true" />
      </button>
    )}
  </div>
);

export const SkeletonRows: React.FC<{
  rows?: number;
  cols: number;
}> = ({ rows = 6, cols }) => (
  <>
    {Array.from({ length: rows }).map((_, rIdx) => (
      <tr key={`skel-${rIdx}`}>
        {Array.from({ length: cols }).map((__, cIdx) => (
          <td key={`skel-c-${cIdx}`}>
            <div
              className="skeleton-bar h-4"
              style={{
                width: cIdx === 0 ? '75%' : cIdx === cols - 1 ? '56px' : `${55 + ((rIdx * 17 + cIdx * 23) % 35)}%`,
                marginLeft: cIdx === cols - 1 ? 'auto' : undefined,
              }}
            />
          </td>
        ))}
      </tr>
    ))}
  </>
);

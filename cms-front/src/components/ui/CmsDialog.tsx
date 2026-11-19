import React, { useEffect, useId, useRef } from 'react';
import { X } from 'lucide-react';

interface CmsDialogProps {
  open: boolean;
  onClose: () => void;
  title?: React.ReactNode;
  subtitle?: React.ReactNode;
  variant?: 'modal' | 'drawer';
  size?: 'sm' | 'md' | 'lg' | 'xl';
  closedBy?: 'any' | 'closerequest' | 'none';
  closeDisabled?: boolean;
  headerExtra?: React.ReactNode;
  footer?: React.ReactNode;
  children: React.ReactNode;
  bodyClassName?: string;
}

const MODAL_WIDTH: Record<NonNullable<CmsDialogProps['size']>, string> = {
  sm: 'w-[420px]',
  md: 'w-[540px]',
  lg: 'w-[700px]',
  xl: 'w-[860px]',
};

const DRAWER_WIDTH: Record<NonNullable<CmsDialogProps['size']>, string> = {
  sm: 'w-full max-w-[440px]',
  md: 'w-full max-w-[560px]',
  lg: 'w-full max-w-[720px]',
  xl: 'w-full max-w-[920px]',
};

export const CmsDialog: React.FC<CmsDialogProps> = ({
  open,
  onClose,
  title,
  subtitle,
  variant = 'modal',
  size = 'md',
  closedBy = 'any',
  closeDisabled = false,
  headerExtra,
  footer,
  children,
  bodyClassName = 'p-6 overflow-y-auto flex-1',
}) => {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const pressedOutsideRef = useRef(false);

  useEffect(() => {
    const el = dialogRef.current;
    if (!el) return;
    if (open) {
      if (!el.open) {
        el.showModal();
      }
    } else if (el.open) {
      el.close();
    }
  }, [open]);

  // Light-dismiss pointerdown/pointerup fallback when HTMLDialogElement closedBy is unsupported
  useEffect(() => {
    const el = dialogRef.current;
    if (!el || closedBy !== 'any' || closeDisabled) return;
    if ('closedBy' in HTMLDialogElement.prototype) return;

    const isOutside = (e: PointerEvent) => {
      if (e.target !== el) return false;
      const r = el.getBoundingClientRect();
      return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
    };
    const onPointerDown = (e: PointerEvent) => {
      pressedOutsideRef.current = isOutside(e);
    };
    const onPointerUp = (e: PointerEvent) => {
      if (pressedOutsideRef.current && isOutside(e)) {
        el.close();
      }
      pressedOutsideRef.current = false;
    };

    el.addEventListener('pointerdown', onPointerDown);
    el.addEventListener('pointerup', onPointerUp);
    return () => {
      el.removeEventListener('pointerdown', onPointerDown);
      el.removeEventListener('pointerup', onPointerUp);
    };
  }, [closedBy, closeDisabled]);

  const handleCancel = (e: React.SyntheticEvent<HTMLDialogElement>) => {
    if (closeDisabled || closedBy === 'none') {
      e.preventDefault();
      return;
    }
  };

  const handleNativeClose = () => {
    if (open && closeDisabled) {
      dialogRef.current?.showModal();
      return;
    }
    if (open) {
      onClose();
    }
  };

  const widthClass = variant === 'drawer' ? DRAWER_WIDTH[size] : MODAL_WIDTH[size];

  return (
    <dialog
      ref={dialogRef}
      {...({ closedby: closeDisabled ? 'none' : closedBy } as Record<string, string>)}
      aria-labelledby={title ? titleId : undefined}
      onCancel={handleCancel}
      onClose={handleNativeClose}
      className={`${variant === 'drawer' ? 'cms-drawer' : 'cms-dialog'} ${widthClass}`}
    >
      {open && (
        <>
          {(title || headerExtra) && (
            <div className="px-6 py-4 border-b border-[var(--cms-border)] flex items-center justify-between gap-4 shrink-0 bg-[var(--cms-card)]">
              <div className="min-w-0 flex-1">
                {title && (
                  <h2 id={titleId} className="text-base font-semibold text-[var(--cms-fg)] truncate m-0">
                    {title}
                  </h2>
                )}
                {subtitle && (
                  <p className="text-xs text-[var(--cms-muted-fg)] mt-0.5 truncate m-0">{subtitle}</p>
                )}
              </div>
              <div className="flex items-center gap-2 shrink-0">
                {headerExtra}
                <button
                  type="button"
                  aria-label="关闭窗口"
                  disabled={closeDisabled}
                  onClick={() => {
                    if (!closeDisabled) {
                      onClose();
                    }
                  }}
                  className="focus-ring w-8 h-8 rounded-lg hover:bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] inline-flex items-center justify-center cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
                >
                  <X size={18} aria-hidden="true" />
                </button>
              </div>
            </div>
          )}

          <div className={bodyClassName}>{children}</div>

          {footer && (
            <div className="px-6 py-4 border-t border-[var(--cms-border)] bg-[var(--cms-card)] flex items-center justify-end gap-2.5 shrink-0">
              {footer}
            </div>
          )}
        </>
      )}
    </dialog>
  );
};

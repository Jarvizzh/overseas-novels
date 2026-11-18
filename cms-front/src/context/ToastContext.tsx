import React, { createContext, useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { X } from 'lucide-react';

export type ToastType = 'success' | 'error' | 'info';

interface ToastItem {
  id: number;
  message: string;
  type: ToastType;
}

interface ToastContextType {
  showToast: (message: string, type?: ToastType) => void;
}

const ToastContext = createContext<ToastContextType | undefined>(undefined);

let nextToastId = 1;

export const ToastProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const popoverRef = useRef<HTMLDivElement>(null);
  const timersRef = useRef<Map<number, number>>(new Map());

  const dismissToast = useCallback((id: number) => {
    const timer = timersRef.current.get(id);
    if (timer !== undefined) {
      window.clearTimeout(timer);
      timersRef.current.delete(id);
    }
    setToasts((prev) => prev.filter((item) => item.id !== id));
  }, []);

  const showToast = useCallback((message: string, type: ToastType = 'success') => {
    const id = nextToastId++;
    setToasts((prev) => [...prev.slice(-3), { id, message, type }]);
    const timer = window.setTimeout(() => {
      timersRef.current.delete(id);
      setToasts((prev) => prev.filter((item) => item.id !== id));
    }, 3600);
    timersRef.current.set(id, timer);
  }, []);

  // Re-promote toast container in Top Layer whenever a toast appears so it stays above open <dialog>.showModal() modals
  const latestToastId = toasts.length > 0 ? toasts[toasts.length - 1].id : 0;
  useEffect(() => {
    const el = popoverRef.current;
    if (!el || typeof el.showPopover !== 'function') return;
    try {
      if (latestToastId > 0) {
        if (el.matches(':popover-open')) {
          el.hidePopover();
        }
        el.showPopover();
      } else if (el.matches(':popover-open')) {
        el.hidePopover();
      }
    } catch {
      // Fallback to fixed z-index positioning if Top Layer promotion is unavailable
    }
  }, [latestToastId]);

  useEffect(() => {
    const timers = timersRef.current;
    window.showToast = showToast;
    return () => {
      if (window.showToast === showToast) {
        delete window.showToast;
      }
      timers.forEach((timer) => window.clearTimeout(timer));
      timers.clear();
    };
  }, [showToast]);

  return (
    <ToastContext.Provider value={{ showToast }}>
      {children}
      <div
        ref={popoverRef}
        popover="manual"
        aria-live="polite"
        className="fixed z-[99999] flex flex-col gap-2 w-80 max-w-[calc(100vw-32px)] pointer-events-none"
        style={{
          inset: '20px 20px auto auto',
          margin: 0,
          padding: 0,
          border: 'none',
          backgroundColor: 'transparent',
          overflow: 'visible',
        }}
      >
        {toasts.map((item) => {
          const dotColor =
            item.type === 'success'
              ? 'var(--cms-success)'
              : item.type === 'error'
                ? 'var(--cms-error)'
                : 'var(--cms-primary)';
          return (
            <div
              key={item.id}
              className="animate-fade-in pointer-events-auto flex items-start gap-2.5 px-4 py-3 rounded-xl bg-[var(--cms-card)] border border-[var(--cms-border)] text-xs text-[var(--cms-fg)]"
              style={{ boxShadow: 'var(--shadow-lg)' }}
            >
              <span
                aria-hidden="true"
                className="w-2 h-2 rounded-full mt-1 shrink-0"
                style={{ backgroundColor: dotColor }}
              />
              <span className="flex-1 leading-relaxed font-medium">{item.message}</span>
              <button
                type="button"
                aria-label="关闭通知"
                onClick={() => dismissToast(item.id)}
                className="focus-ring text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] cursor-pointer p-0.5 rounded transition-colors"
              >
                <X size={14} aria-hidden="true" />
              </button>
            </div>
          );
        })}
      </div>
    </ToastContext.Provider>
  );
};

export { ToastContext };

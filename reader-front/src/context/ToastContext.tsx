import React, { useState, useCallback, type ReactNode } from 'react';
import { CheckCircle2, AlertCircle, Info } from 'lucide-react';
import { ToastContext, type ToastType } from './toastStore';

interface ToastState {
  id: number;
  message: string;
  type: ToastType;
}

export const ToastProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [toasts, setToasts] = useState<ToastState[]>([]);

  const showToast = useCallback((message: string, type: ToastType = 'info') => {
    const id = Date.now() + Math.random();
    setToasts((prev) => [...prev, { id, message, type }]);

    setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id));
    }, 2600);
  }, []);

  return (
    <ToastContext.Provider value={{ showToast }}>
      {children}
      <div
        aria-live="polite"
        aria-atomic="false"
        style={{
          position: 'fixed',
          bottom: 'calc(76px + env(safe-area-inset-bottom))',
          left: '50%',
          transform: 'translateX(-50%)',
          zIndex: 9999,
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: '8px',
          pointerEvents: 'none',
          maxWidth: 'calc(100dvw - 32px)',
          width: 'max-content',
        }}
      >
        {toasts.map((toast) => {
          const isError = toast.type === 'error';
          const isSuccess = toast.type === 'success';
          return (
            <div
              key={toast.id}
              role={isError ? 'alert' : 'status'}
              style={{
                padding: '10px 16px',
                borderRadius: '999px',
                fontSize: '0.8125rem',
                fontWeight: 500,
                color: isError || isSuccess ? '#FFFFFF' : 'var(--toast-text)',
                backgroundColor: isError
                  ? 'rgba(185, 28, 28, 0.94)'
                  : isSuccess
                  ? 'rgba(21, 128, 61, 0.94)'
                  : 'var(--toast-bg)',
                backdropFilter: 'blur(10px)',
                WebkitBackdropFilter: 'blur(10px)',
                boxShadow: '0 8px 24px rgba(0, 0, 0, 0.2)',
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
                animation: 'fadeIn 0.2s cubic-bezier(0.16, 1, 0.3, 1) forwards',
                textAlign: 'left',
                pointerEvents: 'auto',
              }}
            >
              {isError ? (
                <AlertCircle size={15} strokeWidth={2} aria-hidden="true" style={{ flexShrink: 0 }} />
              ) : isSuccess ? (
                <CheckCircle2 size={15} strokeWidth={2} aria-hidden="true" style={{ flexShrink: 0 }} />
              ) : (
                <Info size={15} strokeWidth={2} aria-hidden="true" style={{ flexShrink: 0 }} />
              )}
              <span>{toast.message}</span>
            </div>
          );
        })}
      </div>
    </ToastContext.Provider>
  );
};

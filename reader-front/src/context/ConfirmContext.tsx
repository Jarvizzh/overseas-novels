import React, { useState, useCallback, useRef, useEffect, useId, type ReactNode } from 'react';
import { ConfirmContext, type ConfirmOptions } from './confirmStore';

export const ConfirmProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [confirmConfig, setConfirmConfig] = useState<ConfirmOptions | null>(null);
  const dialogRef = useRef<HTMLDialogElement>(null);
  const pressedOutsideRef = useRef(false);
  const titleId = useId();
  const descId = useId();

  const showConfirm = useCallback((options: ConfirmOptions) => {
    setConfirmConfig(options);
  }, []);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;

    if (confirmConfig && !dialog.open) {
      dialog.showModal();
    } else if (!confirmConfig && dialog.open) {
      dialog.close();
    }
  }, [confirmConfig]);

  const handleDismiss = useCallback(() => {
    if (confirmConfig?.onCancel) {
      confirmConfig.onCancel();
    }
    setConfirmConfig(null);
  }, [confirmConfig]);

  const handleConfirmAction = () => {
    if (confirmConfig) {
      confirmConfig.onConfirm();
    }
    setConfirmConfig(null);
  };

  const isPointerOutside = (e: React.PointerEvent<HTMLDialogElement>, dialog: HTMLDialogElement) => {
    const r = dialog.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  };

  return (
    <ConfirmContext.Provider value={{ showConfirm }}>
      {children}
      <dialog
        ref={dialogRef}
        className="sn-dialog"
        aria-labelledby={confirmConfig?.title ? titleId : undefined}
        aria-describedby={descId}
        onCancel={(e) => {
          e.preventDefault();
          handleDismiss();
        }}
        onPointerDown={(e) => {
          if (dialogRef.current) {
            pressedOutsideRef.current = isPointerOutside(e, dialogRef.current);
          }
        }}
        onPointerUp={(e) => {
          if (dialogRef.current && pressedOutsideRef.current && isPointerOutside(e, dialogRef.current)) {
            handleDismiss();
          }
          pressedOutsideRef.current = false;
        }}
        style={{ width: '100%', maxWidth: '360px' }}
      >
        {confirmConfig && (
          <div
            style={{
              padding: '24px',
              display: 'flex',
              flexDirection: 'column',
              gap: '14px',
            }}
          >
            {confirmConfig.title && (
              <h2
                id={titleId}
                style={{
                  fontFamily: 'var(--font-display)',
                  fontSize: '1.125rem',
                  fontWeight: 600,
                  margin: 0,
                  textAlign: 'center',
                  color: 'var(--text-primary)',
                }}
              >
                {confirmConfig.title}
              </h2>
            )}
            <p
              id={descId}
              style={{
                fontSize: '0.875rem',
                lineHeight: 1.55,
                color: 'var(--text-secondary)',
                margin: 0,
                textAlign: 'center',
              }}
            >
              {confirmConfig.message}
            </p>
            <div style={{ display: 'flex', gap: '10px', marginTop: '6px' }}>
              <button
                type="button"
                autoFocus
                className="btn-cta-secondary"
                style={{ flex: 1 }}
                onClick={handleDismiss}
              >
                {confirmConfig.cancelText || 'Cancel'}
              </button>
              <button
                type="button"
                className="btn-cta-primary"
                style={{
                  flex: 1,
                  backgroundColor: confirmConfig.destructive === false ? 'var(--cta-bg)' : '#DC2626',
                  color: confirmConfig.destructive === false ? 'var(--cta-text)' : '#FFFFFF',
                }}
                onClick={handleConfirmAction}
              >
                {confirmConfig.confirmText || 'Confirm'}
              </button>
            </div>
          </div>
        )}
      </dialog>
    </ConfirmContext.Provider>
  );
};

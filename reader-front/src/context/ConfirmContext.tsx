import React, { createContext, useContext, useState, useCallback, type ReactNode } from 'react';

interface ConfirmOptions {
  title?: string;
  message: string;
  confirmText?: string;
  cancelText?: string;
  onConfirm: () => void;
  onCancel?: () => void;
}

interface ConfirmContextType {
  showConfirm: (options: ConfirmOptions) => void;
}

const ConfirmContext = createContext<ConfirmContextType | undefined>(undefined);

export const ConfirmProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [confirmConfig, setConfirmConfig] = useState<ConfirmOptions | null>(null);

  const showConfirm = useCallback((options: ConfirmOptions) => {
    setConfirmConfig(options);
  }, []);

  const handleClose = () => {
    setConfirmConfig(null);
  };

  const handleConfirmAction = () => {
    if (confirmConfig) {
      confirmConfig.onConfirm();
    }
    handleClose();
  };

  const handleCancelAction = () => {
    if (confirmConfig?.onCancel) {
      confirmConfig.onCancel();
    }
    handleClose();
  };

  return (
    <ConfirmContext.Provider value={{ showConfirm }}>
      {children}
      {confirmConfig && (
        <div
          style={{
            position: 'fixed',
            top: 0,
            left: 0,
            right: 0,
            bottom: 0,
            backgroundColor: 'rgba(15, 23, 42, 0.5)',
            backdropFilter: 'blur(4px)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 10000,
            padding: '24px',
          }}
          onClick={handleCancelAction}
        >
          <div
            style={{
              width: '100%',
              maxWidth: '360px',
              backgroundColor: 'var(--bg-surface, #ffffff)',
              borderRadius: '20px',
              padding: '24px',
              boxShadow: '0 20px 40px rgba(0, 0, 0, 0.25)',
              display: 'flex',
              flexDirection: 'column',
              gap: '16px',
              animation: 'scaleIn 0.2s cubic-bezier(0.16, 1, 0.3, 1) forwards',
              color: 'var(--text-primary, #0f172a)',
            }}
            onClick={(e) => e.stopPropagation()}
          >
            {confirmConfig.title && (
              <h3 style={{ fontSize: '17px', fontWeight: 700, margin: 0, textAlign: 'center' }}>
                {confirmConfig.title}
              </h3>
            )}
            <p
              style={{
                fontSize: '14px',
                lineHeight: 1.5,
                color: 'var(--text-secondary, #64748b)',
                margin: 0,
                textAlign: 'center',
              }}
            >
              {confirmConfig.message}
            </p>
            <div
              style={{
                display: 'flex',
                gap: '12px',
                marginTop: '8px',
                justifyContent: 'center',
              }}
            >
              <button
                type="button"
                style={{
                  flex: 1,
                  padding: '10px 16px',
                  borderRadius: '12px',
                  fontSize: '14px',
                  fontWeight: 600,
                  border: '1px solid var(--border, #e2e8f0)',
                  backgroundColor: 'transparent',
                  color: 'var(--text-primary, #0f172a)',
                  cursor: 'pointer',
                }}
                onClick={handleCancelAction}
              >
                {confirmConfig.cancelText || 'Cancel'}
              </button>
              <button
                type="button"
                style={{
                  flex: 1,
                  padding: '10px 16px',
                  borderRadius: '12px',
                  fontSize: '14px',
                  fontWeight: 600,
                  border: 'none',
                  backgroundColor: '#ff4757',
                  color: '#ffffff',
                  cursor: 'pointer',
                }}
                onClick={handleConfirmAction}
              >
                {confirmConfig.confirmText || 'Confirm'}
              </button>
            </div>
          </div>
        </div>
      )}
    </ConfirmContext.Provider>
  );
};

export const useConfirm = (): ConfirmContextType => {
  const context = useContext(ConfirmContext);
  if (!context) {
    throw new Error('useConfirm must be used within a ConfirmProvider');
  }
  return context;
};

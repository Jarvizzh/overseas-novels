import React, { createContext, useContext, useState, useEffect, type ReactNode } from 'react';

interface ConfirmData {
  message: string;
  onConfirm: () => void;
}

interface ConfirmContextType {
  showConfirm: (message: string, onConfirm: () => void) => void;
}

const ConfirmContext = createContext<ConfirmContextType | undefined>(undefined);

export const ConfirmProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [confirmData, setConfirmData] = useState<ConfirmData | null>(null);

  const showConfirm = (message: string, onConfirm: () => void) => {
    setConfirmData({ message, onConfirm });
  };

  // Keep window fallback for legacy backwards compatibility during migration
  useEffect(() => {
    window.showConfirm = showConfirm;
  }, []);

  return (
    <ConfirmContext.Provider value={{ showConfirm }}>
      {children}
      {confirmData && (
        <div
          style={{
            position: 'fixed',
            top: 0,
            left: 0,
            right: 0,
            bottom: 0,
            backgroundColor: 'rgba(15, 23, 42, 0.35)',
            backdropFilter: 'blur(2.5px)',
            WebkitBackdropFilter: 'blur(2.5px)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 999999,
          }}
          className="animate-fade-in"
        >
          <div
            className="animate-scale-up"
            style={{
              width: '420px',
              padding: '24px',
              borderRadius: '16px',
              backgroundColor: 'hsl(var(--bg-surface))',
              border: '1px solid hsl(var(--border))',
              boxShadow: 'var(--shadow-lg)',
            }}
          >
            <h3
              style={{
                fontSize: '1.1rem',
                fontWeight: 700,
                color: 'hsl(var(--text-primary))',
                marginBottom: '12px',
              }}
            >
              确认操作
            </h3>
            <p
              style={{
                fontSize: '0.875rem',
                color: 'hsl(var(--text-secondary))',
                lineHeight: 1.6,
                marginBottom: '24px',
              }}
            >
              {confirmData.message}
            </p>
            <div
              style={{
                display: 'flex',
                justifyContent: 'flex-end',
                gap: '12px',
              }}
            >
              <button
                onClick={() => setConfirmData(null)}
                className="btn-secondary"
                style={{ padding: '8px 16px', fontSize: '0.85rem' }}
              >
                取消
              </button>
              <button
                onClick={() => {
                  confirmData.onConfirm();
                  setConfirmData(null);
                }}
                className="btn-primary"
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: '#ef4444',
                  borderColor: '#ef4444',
                }}
              >
                确定
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

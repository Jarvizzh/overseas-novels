import React, { createContext, useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { CmsDialog } from '../components/ui/CmsDialog';

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
  const cancelBtnRef = useRef<HTMLButtonElement>(null);

  const showConfirm = useCallback((message: string, onConfirm: () => void) => {
    setConfirmData({ message, onConfirm });
  }, []);

  useEffect(() => {
    window.showConfirm = showConfirm;
    return () => {
      if (window.showConfirm === showConfirm) {
        delete window.showConfirm;
      }
    };
  }, [showConfirm]);

  useEffect(() => {
    if (confirmData) {
      cancelBtnRef.current?.focus();
    }
  }, [confirmData]);

  const handleClose = () => setConfirmData(null);

  return (
    <ConfirmContext.Provider value={{ showConfirm }}>
      {children}
      <CmsDialog
        open={Boolean(confirmData)}
        onClose={handleClose}
        title="确认执行此操作？"
        size="sm"
        closedBy="any"
        footer={
          <>
            <button
              ref={cancelBtnRef}
              type="button"
              autoFocus
              onClick={handleClose}
              className="btn-secondary"
            >
              取消
            </button>
            <button
              type="button"
              onClick={() => {
                const action = confirmData?.onConfirm;
                setConfirmData(null);
                action?.();
              }}
              className="focus-ring h-9 px-4 rounded-xl text-xs font-medium text-white cursor-pointer transition-opacity hover:opacity-90"
              style={{ backgroundColor: '#dc2626' }}
            >
              确认执行
            </button>
          </>
        }
      >
        <p className="text-sm text-[var(--cms-muted-fg)] leading-relaxed m-0">
          {confirmData?.message}
        </p>
      </CmsDialog>
    </ConfirmContext.Provider>
  );
};

export { ConfirmContext };

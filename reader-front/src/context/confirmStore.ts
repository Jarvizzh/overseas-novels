import { createContext, useContext } from 'react';

export interface ConfirmOptions {
  title?: string;
  message: string;
  confirmText?: string;
  cancelText?: string;
  destructive?: boolean;
  onConfirm: () => void;
  onCancel?: () => void;
}

export interface ConfirmContextType {
  showConfirm: (options: ConfirmOptions) => void;
}

export const ConfirmContext = createContext<ConfirmContextType | undefined>(undefined);

export function useConfirm(): ConfirmContextType {
  const context = useContext(ConfirmContext);
  if (!context) {
    throw new Error('useConfirm must be used within a ConfirmProvider');
  }
  return context;
}

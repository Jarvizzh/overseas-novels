import React, { useEffect, useId } from 'react';
import { X } from 'lucide-react';

interface DrawerProps {
  visible: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
}

export const Drawer: React.FC<DrawerProps> = ({
  visible,
  onClose,
  title,
  children,
}) => {
  const titleId = useId();

  useEffect(() => {
    if (!visible) return;
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [visible, onClose]);

  return (
    <div
      className={`drawer-backdrop ${visible ? 'visible' : ''}`}
      onClick={onClose}
      aria-hidden={!visible}
      inert={!visible}
    >
      <div
        className="drawer-content"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="drawer-header">
          <h2 id={titleId} className="drawer-title">{title}</h2>
          <button
            type="button"
            className="header-btn"
            onClick={onClose}
            aria-label="Close drawer"
          >
            <X size={20} strokeWidth={1.8} aria-hidden="true" />
          </button>
        </div>
        <div className="scroll-container-no-pad" style={{ flex: 1 }}>
          {children}
        </div>
      </div>
    </div>
  );
};

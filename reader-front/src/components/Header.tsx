import React from 'react';
import { ChevronLeft } from 'lucide-react';

interface HeaderProps {
  title: string;
  showBack?: boolean;
  onBack?: () => void;
  rightElement?: React.ReactNode;
  alwaysShow?: boolean;
}

export const Header: React.FC<HeaderProps> = ({
  title,
  showBack = false,
  onBack,
  rightElement,
  alwaysShow = false,
}) => {
  return (
    <header className={`app-header glass-panel ${alwaysShow ? '' : 'mobile-only-header'}`}>
      <div className={alwaysShow ? 'sn-medium-container' : ''} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', width: '100%', gap: '12px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '6px', flex: 1, minWidth: 0 }}>
          {showBack && (
            <button
              type="button"
              className="header-btn"
              onClick={onBack}
              aria-label="Go back"
            >
              <ChevronLeft size={20} strokeWidth={1.8} aria-hidden="true" />
            </button>
          )}
          <h1 className="header-title">{title}</h1>
        </div>
        {rightElement && (
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', flexShrink: 0 }}>
            {rightElement}
          </div>
        )}
      </div>
    </header>
  );
};

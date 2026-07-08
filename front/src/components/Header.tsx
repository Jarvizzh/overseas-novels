import React from 'react';

interface HeaderProps {
  title: string;
  showBack?: boolean;
  onBack?: () => void;
  rightElement?: React.ReactNode;
}

export const Header: React.FC<HeaderProps> = ({
  title,
  showBack = false,
  onBack,
  rightElement,
}) => {
  return (
    <header className="app-header glass-panel">
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', flex: 1, minWidth: 0 }}>
        {showBack && (
          <button 
            className="header-btn" 
            onClick={onBack}
            aria-label="Go back"
          >
            <svg 
              xmlns="http://www.w3.org/2000/svg" 
              fill="none" 
              viewBox="0 0 24 24" 
              strokeWidth={2.5} 
              stroke="currentColor" 
              style={{ width: '20px', height: '20px' }}
            >
              <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
            </svg>
          </button>
        )}
        <h1 className="header-title">{title}</h1>
      </div>
      {rightElement && (
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end' }}>
          {rightElement}
        </div>
      )}
    </header>
  );
};

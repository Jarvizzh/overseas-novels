import React from 'react';

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
  return (
    <div className={`drawer-backdrop ${visible ? 'visible' : ''}`} onClick={onClose}>
      <div 
        className="drawer-content" 
        onClick={(e) => e.stopPropagation()} // Prevent closing when clicking drawer content
      >
        <div className="drawer-header">
          <h2 className="drawer-title">{title}</h2>
          <button 
            className="header-btn" 
            onClick={onClose}
            aria-label="Close drawer"
          >
            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '20px', height: '20px' }}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="scroll-container-no-pad" style={{ flex: 1 }}>
          {children}
        </div>
      </div>
    </div>
  );
};

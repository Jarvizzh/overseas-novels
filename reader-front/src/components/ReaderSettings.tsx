import React from 'react';

interface ReaderSettingsProps {
  visible: boolean;
  fontSize: number;
  onFontSizeChange: (size: number) => void;
  theme: string;
  onThemeChange: (theme: string) => void;
  fontFamily: 'serif' | 'sans';
  onFontFamilyChange: (font: 'serif' | 'sans') => void;
  lineHeight: 'narrow' | 'medium' | 'wide';
  onLineHeightChange: (lh: 'narrow' | 'medium' | 'wide') => void;
  onOpenDrawer: () => void;
  novelTitle: string;
  onBack: () => void;
  isInShelf: boolean;
  onAddToShelf: () => void;
}

export const ReaderSettings: React.FC<ReaderSettingsProps> = ({
  visible,
  fontSize,
  onFontSizeChange,
  theme,
  onThemeChange,
  fontFamily,
  onFontFamilyChange,
  lineHeight,
  onLineHeightChange,
  onOpenDrawer,
  novelTitle,
  onBack,
  isInShelf,
  onAddToShelf,
}) => {
  const themes = [
    { id: 'day', name: 'Day', bg: '#fdfdfd' },
    { id: 'night', name: 'Night', bg: '#121214' },
    { id: 'sepia', name: 'Sepia', bg: '#f4edd8' },
    { id: 'forest', name: 'Forest', bg: '#e2edd5' },
    { id: 'mint', name: 'Mint', bg: '#e6f3f0' },
  ];

  return (
    <div className="reader-settings-overlay">
      {/* Top Controls */}
      <div className={`reader-hud-top ${visible ? 'visible' : ''}`}>
        <button className="reader-hud-btn" onClick={onBack} aria-label="Go back">
          <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '20px', height: '20px' }}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
          </svg>
        </button>
        <span className="reader-hud-title">{novelTitle}</span>
        <div style={{ display: 'flex', gap: '8px' }}>
          <button 
            className="reader-hud-btn" 
            onClick={onAddToShelf}
            aria-label={isInShelf ? "Remove from Shelf" : "Add to Shelf"}
            style={{ color: isInShelf ? '#818cf8' : 'white' }}
          >
            {isInShelf ? (
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" style={{ width: '20px', height: '20px' }}>
                <path fillRule="evenodd" d="M6.32 2.577a49.255 49.255 0 0111.36 0c1.497.174 2.57 1.46 2.57 2.93V21a.75.75 0 01-1.085.67L12 18.089l-7.165 3.583A.75.75 0 013.75 21V5.507c0-1.47 1.073-2.756 2.57-2.93z" clipRule="evenodd" />
              </svg>
            ) : (
              <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor" style={{ width: '20px', height: '20px' }}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M17.593 3.322c1.1.128 1.907 1.077 1.907 2.185V21L12 17.25 4.5 21V5.507c0-1.108.806-2.057 1.907-2.185a48.507 48.507 0 0111.186 0z" />
              </svg>
            )}
          </button>
          <button className="reader-hud-btn" onClick={onOpenDrawer} aria-label="Table of Contents">
            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor" style={{ width: '20px', height: '20px' }}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 6.75h12M8.25 12h12m-12 5.25h12M3.75 6.75h.007v.008H3.75V6.75zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zM3.75 12h.007v.008H3.75V12zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm-.375 5.25h.007v.008H3.75v-.008zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
            </svg>
          </button>
        </div>
      </div>

      {/* Bottom Controls */}
      <div className={`reader-hud-bottom ${visible ? 'visible' : ''}`}>
        {/* Themes Selector */}
        <div className="hud-section">
          <span className="hud-label">Theme</span>
          <div className="hud-theme-picker">
            {themes.map((t) => (
              <button
                key={t.id}
                className={`hud-theme-dot ${theme === t.id ? 'active' : ''}`}
                style={{ backgroundColor: t.bg }}
                onClick={() => onThemeChange(t.id)}
                title={t.name}
                aria-label={`Switch to ${t.name} theme`}
              >
                {theme === t.id && (
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" style={{ width: '14px', height: '14px', color: t.id === 'day' ? '#1e293b' : '#818cf8' }}>
                    <path fillRule="evenodd" d="M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z" clipRule="evenodd" />
                  </svg>
                )}
              </button>
            ))}
          </div>
        </div>

        {/* Font & Spacing Settings */}
        <div className="hud-section" style={{ gridColumn: 'span 2' }}>
          <span className="hud-label">Typography</span>
          <div style={{ display: 'flex', gap: '12px', width: '100%', alignItems: 'center' }}>
            {/* Font Size Row */}
            <div className="hud-font-size-row" style={{ flex: 1 }}>
              <button 
                className="hud-font-size-btn" 
                onClick={() => onFontSizeChange(Math.max(12, fontSize - 2))}
                aria-label="Decrease font size"
              >
                A-
              </button>
              <span className="hud-font-size-val">{fontSize}px</span>
              <button 
                className="hud-font-size-btn" 
                onClick={() => onFontSizeChange(Math.min(28, fontSize + 2))}
                aria-label="Increase font size"
              >
                A+
              </button>
            </div>

            {/* Font Family Toggle */}
            <div className="hud-font-family-toggle">
              <button
                className={`hud-sub-btn ${fontFamily === 'serif' ? 'active' : ''}`}
                onClick={() => onFontFamilyChange('serif')}
              >
                Serif
              </button>
              <button
                className={`hud-sub-btn ${fontFamily === 'sans' ? 'active' : ''}`}
                onClick={() => onFontFamilyChange('sans')}
              >
                Sans
              </button>
            </div>

            {/* Line Height Toggle */}
            <div className="hud-line-height-toggle">
              <button
                className={`hud-sub-btn ${lineHeight === 'narrow' ? 'active' : ''}`}
                onClick={() => onLineHeightChange('narrow')}
              >
                Compact
              </button>
              <button
                className={`hud-sub-btn ${lineHeight === 'medium' ? 'active' : ''}`}
                onClick={() => onLineHeightChange('medium')}
              >
                Norm
              </button>
              <button
                className={`hud-sub-btn ${lineHeight === 'wide' ? 'active' : ''}`}
                onClick={() => onLineHeightChange('wide')}
              >
                Wide
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

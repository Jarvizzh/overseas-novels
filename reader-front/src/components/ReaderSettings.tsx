import React from 'react';

interface ReaderSettingsProps {
  visible: boolean;
  fontSize: number;
  onFontSizeChange: (size: number) => void;
  onOpenDrawer: () => void;
  novelTitle: string;
  onBack: () => void;
  isInShelf: boolean;
  onAddToShelf: () => void;
  currentChapterIndex?: number;
  totalChapters?: number;
  onPrevChapter?: () => void;
  onNextChapter?: () => void;
}

export const ReaderSettings: React.FC<ReaderSettingsProps> = ({
  visible,
  fontSize,
  onFontSizeChange,
  onOpenDrawer,
  novelTitle,
  onBack,
  isInShelf,
  onAddToShelf,
  currentChapterIndex = 0,
  totalChapters = 1,
  onPrevChapter,
  onNextChapter,
}) => {
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
      <div className={`reader-hud-bottom ${visible ? 'visible' : ''}`} style={{ padding: '16px 20px', gap: '16px' }}>
        {/* Chapter Quick Switcher */}
        {totalChapters > 1 && (
          <div className="hud-chapter-nav">
            <button
              className="hud-chapter-btn"
              disabled={currentChapterIndex <= 0}
              onClick={onPrevChapter}
              aria-label="Previous chapter"
            >
              <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '14px', height: '14px' }}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
              </svg>
              <span>Prev</span>
            </button>
            <span className="hud-chapter-info">
              Chapter {currentChapterIndex + 1} / {totalChapters}
            </span>
            <button
              className="hud-chapter-btn"
              disabled={currentChapterIndex >= totalChapters - 1}
              onClick={onNextChapter}
              aria-label="Next chapter"
            >
              <span>Next</span>
              <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '14px', height: '14px' }}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
              </svg>
            </button>
          </div>
        )}

        {/* Font Size Adjuster Only */}
        <div className="hud-section" style={{ width: '100%' }}>
          <span className="hud-label" style={{ width: 'auto', marginRight: '8px' }}>Font Size</span>
          <div className="hud-font-size-row" style={{ flex: 1, maxWidth: '240px' }}>
            <button 
              className="hud-font-size-btn" 
              onClick={() => onFontSizeChange(Math.max(14, fontSize - 2))}
              aria-label="Decrease font size"
            >
              A-
            </button>
            <span className="hud-font-size-val">{fontSize}px</span>
            <button 
              className="hud-font-size-btn" 
              onClick={() => onFontSizeChange(Math.min(30, fontSize + 2))}
              aria-label="Increase font size"
            >
              A+
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};

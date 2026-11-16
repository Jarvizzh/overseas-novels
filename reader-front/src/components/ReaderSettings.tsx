import React from 'react';
import { ChevronLeft, Bookmark, List } from 'lucide-react';

export type ReaderFontFamily = 'serif' | 'sans';
export type ReaderLineHeight = 'compact' | 'standard' | 'relaxed';

interface ReaderSettingsProps {
  visible: boolean;
  fontSize: number;
  onFontSizeChange: (size: number) => void;
  fontFamily: ReaderFontFamily;
  onFontFamilyChange: (family: ReaderFontFamily) => void;
  lineHeight: ReaderLineHeight;
  onLineHeightChange: (lh: ReaderLineHeight) => void;
  theme: string;
  onThemeChange: (theme: string) => void;
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
  fontFamily,
  onFontFamilyChange,
  lineHeight,
  onLineHeightChange,
  theme,
  onThemeChange,
  onOpenDrawer,
  novelTitle,
  onBack,
  isInShelf,
  onAddToShelf,
}) => {
  const normalizedTheme = theme === 'day' ? 'paper' : theme === 'mint' ? 'forest' : theme;

  const themes = [
    { id: 'paper', name: 'Paper', bg: '#FAF8F4', fg: '#1E1C1A' },
    { id: 'sepia', name: 'Sepia', bg: '#F3ECD9', fg: '#3A2E21' },
    { id: 'forest', name: 'Sage', bg: '#E7EFE4', fg: '#223324' },
    { id: 'night', name: 'Night', bg: '#141413', fg: '#D6D2CA' },
  ];

  return (
    <div className="reader-settings-overlay" aria-hidden={!visible}>
      {/* Top Controls */}
      <div className={`reader-hud-top ${visible ? 'visible' : ''}`}>
        <div className="reader-hud-top-inner">
          <button type="button" className="reader-hud-btn" onClick={onBack} aria-label="Go back">
            <ChevronLeft size={20} strokeWidth={1.8} aria-hidden="true" />
          </button>
          <span className="reader-hud-title">{novelTitle}</span>
          <div style={{ display: 'flex', gap: '4px' }}>
            <button
              type="button"
              className="reader-hud-btn"
              onClick={onAddToShelf}
              aria-label={isInShelf ? 'Remove from Library' : 'Save to Library'}
              style={{ color: isInShelf ? 'var(--reader-accent)' : 'var(--reader-text)' }}
            >
              <Bookmark
                size={18}
                strokeWidth={1.8}
                fill={isInShelf ? 'currentColor' : 'none'}
                aria-hidden="true"
              />
            </button>
            <button type="button" className="reader-hud-btn" onClick={onOpenDrawer} aria-label="Table of Contents">
              <List size={19} strokeWidth={1.8} aria-hidden="true" />
            </button>
          </div>
        </div>
      </div>

      {/* Bottom Controls */}
      <div className={`reader-hud-bottom ${visible ? 'visible' : ''}`}>
        <div className="reader-hud-bottom-inner">
          {/* Surface Themes */}
          <div className="hud-section">
            <span className="hud-label">Surface</span>
            <div className="hud-theme-picker">
              {themes.map((t) => {
                const isActive = normalizedTheme === t.id;
                return (
                  <button
                    key={t.id}
                    type="button"
                    className={`hud-theme-pill ${isActive ? 'active' : ''}`}
                    style={{ backgroundColor: t.bg, color: t.fg }}
                    onClick={() => onThemeChange(t.id)}
                    aria-pressed={isActive}
                  >
                    <span>{t.name}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {/* Font Family & Font Size */}
          <div className="hud-section">
            <span className="hud-label">Typeface</span>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap', justifyContent: 'flex-end' }}>
              <div className="hud-segmented" role="group" aria-label="Font typeface">
                <button
                  type="button"
                  className={`hud-seg-btn ${fontFamily === 'serif' ? 'active' : ''}`}
                  style={{ fontFamily: 'var(--font-serif)' }}
                  onClick={() => onFontFamilyChange('serif')}
                  aria-pressed={fontFamily === 'serif'}
                >
                  Serif
                </button>
                <button
                  type="button"
                  className={`hud-seg-btn ${fontFamily === 'sans' ? 'active' : ''}`}
                  style={{ fontFamily: 'var(--font-sans)' }}
                  onClick={() => onFontFamilyChange('sans')}
                  aria-pressed={fontFamily === 'sans'}
                >
                  Sans
                </button>
              </div>

              <div className="hud-font-size-row">
                <button
                  type="button"
                  className="hud-font-size-btn"
                  onClick={() => onFontSizeChange(Math.max(14, fontSize - 1))}
                  aria-label="Decrease font size"
                >
                  A-
                </button>
                <span className="hud-font-size-val">{fontSize}px</span>
                <button
                  type="button"
                  className="hud-font-size-btn"
                  onClick={() => onFontSizeChange(Math.min(28, fontSize + 1))}
                  aria-label="Increase font size"
                >
                  A+
                </button>
              </div>
            </div>
          </div>

          {/* Line Spacing */}
          <div className="hud-section">
            <span className="hud-label">Spacing</span>
            <div className="hud-segmented" role="group" aria-label="Line spacing">
              {(
                [
                  { id: 'compact', label: 'Compact' },
                  { id: 'standard', label: 'Standard' },
                  { id: 'relaxed', label: 'Relaxed' },
                ] as const
              ).map((opt) => (
                <button
                  key={opt.id}
                  type="button"
                  className={`hud-seg-btn ${lineHeight === opt.id ? 'active' : ''}`}
                  onClick={() => onLineHeightChange(opt.id)}
                  aria-pressed={lineHeight === opt.id}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

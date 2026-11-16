import React, { useState } from 'react';
import { BookOpen, Check, Trash2 } from 'lucide-react';
import type { Novel } from '../utils/api';

interface ShelfProps {
  novels: Novel[];
  loading?: boolean;
  onNavigate: (page: string, params?: Record<string, unknown>) => void;
  shelfBookIds: number[];
  onRemoveFromShelf: (bookIds: number[]) => void;
  readingProgress: {
    [bookId: number]: {
      chapterIndex: number;
      scrollOffsetPercentage: number;
    };
  };
}

export const Shelf: React.FC<ShelfProps> = ({
  novels,
  loading = false,
  onNavigate,
  shelfBookIds,
  onRemoveFromShelf,
  readingProgress,
}) => {
  const [isEditMode, setIsEditMode] = useState(false);
  const [selectedIds, setSelectedIds] = useState<number[]>([]);

  const shelfNovels = novels.filter((n) => shelfBookIds.includes(n.id));

  const handleToggleSelect = (id: number) => {
    setSelectedIds((prev) =>
      prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]
    );
  };

  const handleDeleteSelected = () => {
    if (selectedIds.length === 0) return;
    onRemoveFromShelf(selectedIds);
    setSelectedIds([]);
    setIsEditMode(false);
  };

  const handleCancelEdit = () => {
    setSelectedIds([]);
    setIsEditMode(false);
  };

  return (
    <div className="scroll-container animate-fade-in">
      <div className="sn-shell-container">
        <div className="shelf-header">
          <h2 className="shelf-title">My Library</h2>
          {shelfNovels.length > 0 && (
            <button
              type="button"
              onClick={isEditMode ? handleCancelEdit : () => setIsEditMode(true)}
              style={{
                background: 'transparent',
                border: 'none',
                color: 'var(--accent-text)',
                fontWeight: 600,
                fontSize: '0.875rem',
                cursor: 'pointer',
                padding: '6px 4px',
              }}
            >
              {isEditMode ? 'Done' : 'Edit'}
            </button>
          )}
        </div>

        {loading && shelfNovels.length === 0 ? (
          <div className="shelf-empty-state">
            <div className="sn-spinner" />
            <p style={{ fontSize: '0.875rem', marginTop: '8px' }}>Loading your library...</p>
          </div>
        ) : shelfNovels.length > 0 ? (
          <div className="shelf-grid">
            {shelfNovels.map((novel) => {
              const progress = readingProgress[novel.id];
              const isSelected = selectedIds.includes(novel.id);

              let progressText = 'Unread';
              let progressPercent = 0;
              if (progress) {
                const chNum = progress.chapterIndex + 1;
                const chScrollPercent = Math.min(100, Math.max(0, Math.round((progress.scrollOffsetPercentage || 0) * 100)));
                progressText = `Ch. ${chNum} · ${chScrollPercent}%`;
                progressPercent = Math.min(100, Math.max(6, chScrollPercent));
              }

              return (
                <article
                  key={novel.id}
                  className="shelf-item"
                  onClick={() => {
                    if (isEditMode) {
                      handleToggleSelect(novel.id);
                    } else {
                      onNavigate('detail', { id: novel.id });
                    }
                  }}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      if (isEditMode) handleToggleSelect(novel.id);
                      else onNavigate('detail', { id: novel.id });
                    }
                  }}
                  role="button"
                  tabIndex={0}
                >
                  {isEditMode && (
                    <div className={`shelf-checkbox ${isSelected ? 'checked' : ''}`} aria-hidden="true">
                      {isSelected && <Check size={13} strokeWidth={2.5} />}
                    </div>
                  )}

                  <div className="book-card-cover-wrapper" style={{ opacity: isEditMode && !isSelected ? 0.65 : 1 }}>
                    <img src={novel.cover} alt={novel.title} className="book-card-cover" loading="lazy" />
                    {progress && (
                      <div className="shelf-progress-bar">
                        <div className="shelf-progress-fill" style={{ width: `${progressPercent}%` }} />
                      </div>
                    )}
                  </div>

                  <h3 className="book-card-title">{novel.title}</h3>
                  <span
                    className="book-card-author tabular-nums"
                    style={{
                      color: progress ? 'var(--accent-text)' : 'var(--text-tertiary)',
                      fontWeight: progress ? 500 : 400,
                    }}
                  >
                    {progressText}
                  </span>
                </article>
              );
            })}
          </div>
        ) : (
          <div className="shelf-empty-state">
            <div className="shelf-empty-icon-wrap">
              <BookOpen size={26} strokeWidth={1.65} aria-hidden="true" />
            </div>
            <p style={{ fontWeight: 600, fontSize: '0.9375rem', color: 'var(--text-primary)' }}>
              Your library is empty
            </p>
            <p style={{ fontSize: '0.8125rem', maxWidth: '280px', lineHeight: 1.5 }}>
              Save stories while browsing to keep track of your reading progress across devices.
            </p>
            <button
              type="button"
              className="btn-cta-primary"
              style={{ marginTop: '12px' }}
              onClick={() => onNavigate('home')}
            >
              Browse Stories
            </button>
          </div>
        )}

        {isEditMode && (
          <div className="shelf-edit-bar glass-panel animate-slide-up">
            <span style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--text-primary)' }} className="tabular-nums">
              Selected: {selectedIds.length} {selectedIds.length === 1 ? 'book' : 'books'}
            </span>
            <button
              type="button"
              className="shelf-edit-btn-delete"
              onClick={handleDeleteSelected}
              disabled={selectedIds.length === 0}
              style={{ display: 'inline-flex', alignItems: 'center', gap: '6px' }}
            >
              <Trash2 size={14} strokeWidth={2} aria-hidden="true" />
              <span>Remove</span>
            </button>
          </div>
        )}
      </div>
    </div>
  );
};

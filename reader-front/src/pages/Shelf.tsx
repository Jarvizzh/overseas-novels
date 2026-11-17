import React, { useEffect, useState } from 'react';
import { BookOpen, Check, Clock, Trash2 } from 'lucide-react';
import type { BookshelfItem, Novel } from '../utils/api';

const FALLBACK_COVER =
  'data:image/svg+xml;utf8,' +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 400"><rect width="300" height="400" fill="#262421"/><rect x="24" y="24" width="252" height="352" fill="none" stroke="#9A7132" stroke-opacity="0.4" stroke-width="2"/><text x="150" y="205" fill="#F4F1EA" font-family="Georgia, serif" font-size="22" text-anchor="middle">StarNovel</text></svg>',
  );

interface ShelfProps {
  novels: Novel[];
  recentBooks?: BookshelfItem[];
  recentLoading?: boolean;
  initialTab?: 'shelf' | 'recent';
  loading?: boolean;
  loadingMore: boolean;
  hasMore: boolean;
  pageFailed: boolean;
  editing: boolean;
  onLoadMore: () => void;
  onNavigate: (page: string, params?: Record<string, unknown>) => void;
  onRemoveFromShelf: (bookIds: number[]) => Promise<boolean>;
  readingProgress: {
    [bookId: number]: {
      chapterIndex: number;
      scrollOffsetPercentage: number;
    };
  };
}

export const Shelf: React.FC<ShelfProps> = ({
  novels,
  recentBooks = [],
  recentLoading = false,
  initialTab = 'shelf',
  loading = false,
  loadingMore,
  hasMore,
  pageFailed,
  editing,
  onLoadMore,
  onNavigate,
  onRemoveFromShelf,
  readingProgress,
}) => {
  const [activeTab, setActiveTab] = useState<'shelf' | 'recent'>(initialTab);
  const [isEditMode, setIsEditMode] = useState(false);
  const [selectedIds, setSelectedIds] = useState<number[]>([]);

  useEffect(() => {
    setActiveTab(initialTab);
    if (initialTab === 'recent') {
      setIsEditMode(false);
      setSelectedIds([]);
    }
  }, [initialTab]);

  const handleToggleSelect = (id: number) => {
    setSelectedIds((prev) =>
      prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]
    );
  };

  const handleDeleteSelected = async () => {
    if (editing || selectedIds.length === 0) return;
    if (await onRemoveFromShelf(selectedIds)) {
      setSelectedIds([]);
      setIsEditMode(false);
    }
  };

  const handleCancelEdit = () => {
    setSelectedIds([]);
    setIsEditMode(false);
  };

  return (
    <div className="scroll-container animate-fade-in">
      <div className="sn-shell-container">
        <div className="shelf-header">
          <h2 className="shelf-title">{activeTab === 'shelf' ? 'My Library' : 'Recently Read'}</h2>
          {activeTab === 'shelf' && novels.length > 0 && (
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

        <div className="sub-tabs-container" role="tablist" aria-label="Library sections" style={{ marginBottom: '18px' }}>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'shelf'}
            className={`sub-tab-btn ${activeTab === 'shelf' ? 'active' : ''}`}
            onClick={() => setActiveTab('shelf')}
          >
            Saved Shelf ({novels.length})
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'recent'}
            className={`sub-tab-btn ${activeTab === 'recent' ? 'active' : ''}`}
            onClick={() => {
              setActiveTab('recent');
              setIsEditMode(false);
              setSelectedIds([]);
            }}
          >
            Recently Read ({recentBooks.length})
          </button>
        </div>

        {activeTab === 'recent' ? (
          recentLoading && recentBooks.length === 0 ? (
            <div className="shelf-empty-state">
              <div className="sn-spinner" />
              <p style={{ fontSize: '0.875rem', marginTop: '8px' }}>Loading reading history...</p>
            </div>
          ) : recentBooks.length > 0 ? (
            <div className="shelf-grid">
              {recentBooks.map((item) => {
                const live = readingProgress[item.novel_id];
                const chapterIndex = live ? live.chapterIndex : item.chapter_index;
                const scrollOffset = live ? live.scrollOffsetPercentage : item.scroll_offset_percentage;
                const pct = Math.min(100, Math.max(0, Math.round((scrollOffset || 0) * 100)));
                const barWidth = Math.min(100, Math.max(6, pct));
                return (
                  <article
                    key={`recent-${item.novel_id}`}
                    className="shelf-item"
                    onClick={() => onNavigate('reader', { id: item.novel_id, chapterIndex })}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        onNavigate('reader', { id: item.novel_id, chapterIndex });
                      }
                    }}
                    role="button"
                    tabIndex={0}
                  >
                    <div className="book-card-cover-wrapper">
                      <img
                        src={item.novel.cover || FALLBACK_COVER}
                        alt={item.novel.title}
                        className="book-card-cover"
                        loading="lazy"
                        onError={(e) => {
                          const img = e.currentTarget;
                          if (img.src !== FALLBACK_COVER) img.src = FALLBACK_COVER;
                        }}
                      />
                      <div className="shelf-progress-bar">
                        <div className="shelf-progress-fill" style={{ width: `${barWidth}%` }} />
                      </div>
                    </div>
                    <h3 className="book-card-title">{item.novel.title}</h3>
                    <span
                      className="book-card-author tabular-nums"
                      style={{ color: 'var(--accent-text)', fontWeight: 500 }}
                    >
                      Ch. {chapterIndex + 1} · {pct}%
                    </span>
                  </article>
                );
              })}
            </div>
          ) : (
            <div className="shelf-empty-state">
              <div className="shelf-empty-icon-wrap">
                <Clock size={26} strokeWidth={1.65} aria-hidden="true" />
              </div>
              <p style={{ fontWeight: 600, fontSize: '0.9375rem', color: 'var(--text-primary)' }}>
                No recently read books
              </p>
              <p style={{ fontSize: '0.8125rem', maxWidth: '280px', lineHeight: 1.5 }}>
                Start reading any story to automatically keep track of where you left off.
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
          )
        ) : (
        <>

        {loading && novels.length === 0 ? (
          <div className="shelf-empty-state">
            <div className="sn-spinner" />
            <p style={{ fontSize: '0.875rem', marginTop: '8px' }}>Loading your library...</p>
          </div>
        ) : novels.length > 0 ? (
          <div className="shelf-grid">
            {novels.map((novel) => {
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
        ) : pageFailed ? null : (
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

        <div role="status">{pageFailed && !loadingMore ? 'Could not load your library.' : ''}</div>
        {(hasMore || pageFailed) && (
          <div style={{ display: 'flex', justifyContent: 'center', marginTop: '20px' }}>
            <button
              type="button"
              className="btn-cta-secondary"
              aria-disabled={loadingMore || editing}
              onClick={() => {
                if (!loadingMore && !editing) onLoadMore();
              }}
            >
              {loadingMore ? 'Loading…' : pageFailed ? 'Try Again' : 'Load More Books'}
            </button>
          </div>
        )}

        {isEditMode && activeTab === 'shelf' && (
          <div className="shelf-edit-bar glass-panel animate-slide-up">
            <span style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--text-primary)' }} className="tabular-nums">
              Selected: {selectedIds.length} {selectedIds.length === 1 ? 'book' : 'books'}
            </span>
            <button
              type="button"
              className="shelf-edit-btn-delete"
              onClick={handleDeleteSelected}
              disabled={editing || selectedIds.length === 0}
              style={{ display: 'inline-flex', alignItems: 'center', gap: '6px' }}
            >
              <Trash2 size={14} strokeWidth={2} aria-hidden="true" />
              <span>Remove</span>
            </button>
          </div>
        )}
        </>
        )}
      </div>
    </div>
  );
};

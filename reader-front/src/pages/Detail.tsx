import React, { useState, useEffect } from 'react';
import { ChevronLeft, Bookmark, Star, BookOpen, ChevronDown, ChevronUp, ArrowUpDown, Check } from 'lucide-react';
import { api, ApiError } from '../utils/api';
import type { Novel, Chapter } from '../utils/api';

interface DetailProps {
  authReady?: boolean;
  sessionToken?: string;
  novelId: number;
  novels?: Novel[];
  onNavigate: (page: string, params?: Record<string, unknown>, options?: { replace?: boolean }) => void;
  onBack: () => void;
  shelfBookIds: number[];
  onToggleShelf: (bookId: number) => void;
  readingProgress: {
    [bookId: number]: {
      chapterIndex: number;
      scrollOffsetPercentage: number;
    };
  };
}

export const Detail: React.FC<DetailProps> = ({
  authReady = true,
  sessionToken,
  novelId,
  onNavigate,
  onBack,
  shelfBookIds,
  onToggleShelf,
  readingProgress,
}) => {
  const [novel, setNovel] = useState<Novel | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [loading, setLoading] = useState(true);
  const [chapterError, setChapterError] = useState('');
  const [chapterRetry, setChapterRetry] = useState(0);

  const [activeTab, setActiveTab] = useState<'about' | 'chapters'>('about');
  const [isDescCollapsed, setIsDescCollapsed] = useState(true);
  const [isDescendingOrder, setIsDescendingOrder] = useState(false);

  useEffect(() => {
    if (!authReady) return;
    let cancelled = false;
    const loadNovelDetails = async () => {
      setLoading(true);
      setNovel(null);
      setChapters([]);
      setChapterError('');
      const [detail, chapterList] = await Promise.allSettled([
        api.getNovelDetail(novelId),
        api.getChaptersList(novelId),
      ]);
      if (cancelled) return;
      if (detail.status === 'fulfilled') {
        setNovel(detail.value);
      } else {
        console.error('Failed to load novel detail:', detail.reason);
      }
      if (chapterList.status === 'fulfilled') {
        setChapters(chapterList.value || []);
      } else {
        const expired = chapterList.reason instanceof ApiError && chapterList.reason.status === 401;
        setChapterError(expired
          ? 'Your session expired. Retry to reconnect.'
          : 'Could not load chapters. Please try again.');
      }
      setLoading(false);
    };
    loadNovelDetails();
    return () => {
      cancelled = true;
    };
  }, [authReady, sessionToken, novelId, chapterRetry]);

  if (loading) {
    return (
      <div className="scroll-container animate-fade-in" style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: '12px', color: 'var(--text-secondary)' }}>
        <div className="sn-spinner" />
        <p style={{ fontSize: '0.875rem' }}>Loading book details...</p>
      </div>
    );
  }

  if (!novel) {
    return (
      <div className="scroll-container animate-fade-in">
        <div className="shelf-empty-state">
          <div className="shelf-empty-icon-wrap">
            <BookOpen size={24} strokeWidth={1.65} aria-hidden="true" />
          </div>
          <p style={{ fontWeight: 600, color: 'var(--text-primary)' }}>Book not found.</p>
          <button type="button" className="btn-cta-primary" style={{ marginTop: '12px' }} onClick={() => onNavigate('home', undefined, { replace: true })}>
            Back to Store
          </button>
        </div>
      </div>
    );
  }

  const isInShelf = shelfBookIds.includes(novel.id);
  const progress = readingProgress[novel.id];
  const lastReadChapterIndex = progress ? progress.chapterIndex : 0;

  const sortedChapters = isDescendingOrder
    ? [...chapters].reverse()
    : chapters;

  const startReadingButton = chapterError ? (
    <button type="button" className="btn-cta-primary" style={{ flex: 1 }} onClick={() => setChapterRetry((attempt) => attempt + 1)}>
      Retry Chapters
    </button>
  ) : (
    <button
      type="button"
      className="btn-cta-primary"
      style={{ flex: 1 }}
      disabled={chapters.length === 0}
      onClick={() => onNavigate('reader', { id: novel.id })}
    >
      <BookOpen size={17} strokeWidth={1.8} aria-hidden="true" />
      <span>
        {chapters.length === 0
          ? 'No Chapters Yet'
          : progress
            ? `Continue · Ch. ${lastReadChapterIndex + 1}`
            : 'Start Reading'}
      </span>
    </button>
  );

  const toggleShelfButton = (
    <button
      type="button"
      className="btn-cta-secondary"
      onClick={() => onToggleShelf(novel.id)}
    >
      <Bookmark
        size={16}
        strokeWidth={1.8}
        fill={isInShelf ? 'currentColor' : 'none'}
        style={{ color: isInShelf ? 'var(--accent-color)' : 'currentColor' }}
        aria-hidden="true"
      />
      <span>{isInShelf ? 'In Library' : 'Add to Library'}</span>
    </button>
  );

  return (
    <div className="page-container-full animate-fade-in">
      {/* Minimal Top Bar */}
      <header className="detail-top-bar glass-panel">
        <div className="detail-top-bar-inner">
          <button
            type="button"
            className="header-btn"
            onClick={onBack}
            aria-label="Go back"
          >
            <ChevronLeft size={20} strokeWidth={1.8} aria-hidden="true" />
          </button>
          <span style={{ fontSize: '0.75rem', fontWeight: 600, letterSpacing: '0.08em', textTransform: 'uppercase', color: 'var(--text-tertiary)' }}>
            Book Details
          </span>
          <button
            type="button"
            className="header-btn"
            onClick={() => onToggleShelf(novel.id)}
            aria-label={isInShelf ? 'Remove from Library' : 'Add to Library'}
            style={{ color: isInShelf ? 'var(--accent-color)' : 'var(--text-primary)' }}
          >
            <Bookmark size={18} strokeWidth={1.8} fill={isInShelf ? 'currentColor' : 'none'} aria-hidden="true" />
          </button>
        </div>
      </header>

      {/* Scrollable Content */}
      <div className="scroll-container" style={{ flex: 1, minHeight: 0 }}>
        <div className="sn-medium-container">
          {/* Atmospheric Editorial Hero */}
          <div className="detail-hero">
            <img src={novel.cover} alt={novel.title} className="detail-cover" />
            <div className="detail-info">
              <div className="detail-meta-row">
                {novel.genres?.[0] && (
                  <>
                    <span>{novel.genres[0]}</span>
                    <span>·</span>
                  </>
                )}
                <span>{novel.status}</span>
              </div>
              <h1 className="detail-title">{novel.title}</h1>
              <p className="detail-author">By {novel.author}</p>

              {/* Tablet / Desktop Inline Action Buttons */}
              <div className="detail-desktop-actions">
                {startReadingButton}
                {toggleShelfButton}
              </div>
            </div>
          </div>

          {/* Hairline Stats Bar */}
          <div className="detail-stats-bar">
            <div className="detail-stat-item">
              <span className="detail-stat-val">
                <Star size={14} fill="currentColor" strokeWidth={0} style={{ color: 'var(--accent-color)' }} aria-hidden="true" />
                <span>{novel.rating}</span>
              </span>
              <span className="detail-stat-lbl">Rating</span>
            </div>
            <div className="detail-stat-item">
              <span className="detail-stat-val">{novel.words.toLocaleString()}</span>
              <span className="detail-stat-lbl">Words</span>
            </div>
            <div className="detail-stat-item">
              <span className="detail-stat-val">{chapterError ? '—' : chapters.length}</span>
              <span className="detail-stat-lbl">Chapters</span>
            </div>
          </div>

          {chapterError && <p role="alert">{chapterError}</p>}

          {/* Tabs */}
          <div className="detail-tabs" role="tablist">
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === 'about'}
              className={`detail-tab ${activeTab === 'about' ? 'active' : ''}`}
              onClick={() => setActiveTab('about')}
            >
              About
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === 'chapters'}
              className={`detail-tab ${activeTab === 'chapters' ? 'active' : ''}`}
              onClick={() => setActiveTab('chapters')}
            >
              Chapters{!chapterError && ` (${chapters.length})`}
            </button>
          </div>

          {/* Tab Panel */}
          <div style={{ padding: '20px 0' }}>
            {activeTab === 'about' ? (
              <div>
                <h2 className="hot-tags-title">Synopsis</h2>
                <div
                  className={`detail-synopsis ${isDescCollapsed ? 'collapsed' : ''}`}
                  onClick={() => setIsDescCollapsed(!isDescCollapsed)}
                >
                  {novel.synopsis}
                </div>
                <button
                  type="button"
                  className="detail-synopsis-toggle"
                  onClick={() => setIsDescCollapsed(!isDescCollapsed)}
                >
                  {isDescCollapsed ? (
                    <>
                      <span>Read more</span>
                      <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
                    </>
                  ) : (
                    <>
                      <span>Show less</span>
                      <ChevronUp size={14} strokeWidth={2} aria-hidden="true" />
                    </>
                  )}
                </button>

                {novel.genres && novel.genres.length > 0 && (
                  <div style={{ marginTop: '24px' }}>
                    <h2 className="hot-tags-title">Genres</h2>
                    <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap' }}>
                      {novel.genres.map((g) => (
                        <button
                          key={g}
                          type="button"
                          className="tag-pill"
                          onClick={() => onNavigate('search', { genre: g })}
                        >
                          {g}
                        </button>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            ) : !chapterError && (
              <div>
                <div className="chapters-header-row">
                  <span className="tabular-nums">Total: {chapters.length} Chapters</span>
                  <button
                    type="button"
                    onClick={() => setIsDescendingOrder(!isDescendingOrder)}
                    style={{
                      background: 'transparent',
                      border: 'none',
                      color: 'var(--accent-text)',
                      fontWeight: 600,
                      fontSize: '0.75rem',
                      cursor: 'pointer',
                      display: 'inline-flex',
                      alignItems: 'center',
                      gap: '5px',
                      padding: '4px 0',
                    }}
                  >
                    <ArrowUpDown size={13} strokeWidth={2} aria-hidden="true" />
                    <span>{isDescendingOrder ? 'Oldest First' : 'Newest First'}</span>
                  </button>
                </div>

                <div className="chapters-list">
                  {sortedChapters.map((ch, index) => {
                    const fallbackIndex = isDescendingOrder ? chapters.length - 1 - index : index;
                    const chIndex = ch.chapter_index ?? fallbackIndex;
                    const isRead = progress !== undefined && chIndex <= progress.chapterIndex;
                    return (
                      <button
                        type="button"
                        key={ch.id || chIndex}
                        className="chapter-item-link"
                        onClick={() => onNavigate('reader', { id: novel.id, chapterIndex: chIndex })}
                      >
                        <span
                          className="chapter-item-title"
                          style={{
                            color: isRead ? 'var(--text-tertiary)' : 'var(--text-primary)',
                            fontWeight: isRead ? 400 : 500,
                            display: 'inline-flex',
                            alignItems: 'center',
                            gap: '6px',
                          }}
                        >
                          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{ch.title}</span>
                          {isRead && (
                            <span
                              style={{
                                display: 'inline-flex',
                                alignItems: 'center',
                                gap: '2px',
                                fontSize: '0.6875rem',
                                color: 'var(--accent-text)',
                                flexShrink: 0,
                              }}
                            >
                              <Check size={11} strokeWidth={2.4} aria-hidden="true" />
                              <span>Read</span>
                            </span>
                          )}
                        </span>
                        <span className="chapter-item-meta">{ch.word_count.toLocaleString()} w</span>
                      </button>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Mobile Sticky Bottom CTA Bar (< 768px) */}
      <div className="detail-cta-bar glass-panel">
        {toggleShelfButton}
        {startReadingButton}
      </div>
    </div>
  );
};

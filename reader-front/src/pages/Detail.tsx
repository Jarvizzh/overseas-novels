import React, { useState, useEffect } from 'react';
import { api } from '../utils/api';
import type { Novel, Chapter } from '../utils/api';

interface DetailProps {
  novelId: number;
  novels?: Novel[]; // Keep signature for backward compatibility
  onNavigate: (page: string, params?: any) => void;
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
  novelId,
  onNavigate,
  shelfBookIds,
  onToggleShelf,
  readingProgress,
}) => {
  const [novel, setNovel] = useState<Novel | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [loading, setLoading] = useState(true);
  
  const [activeTab, setActiveTab] = useState<'about' | 'chapters'>('about');
  const [isDescCollapsed, setIsDescCollapsed] = useState(true);
  const [isDescendingOrder, setIsDescendingOrder] = useState(false);

  useEffect(() => {
    const loadNovelDetails = async () => {
      setLoading(true);
      try {
        const detail = await api.getNovelDetail(novelId);
        setNovel(detail);
        const chList = await api.getChaptersList(novelId);
        setChapters(chList);
      } catch (err) {
        console.error("Failed to load novel detail or chapters list:", err);
      } finally {
        setLoading(false);
      }
    };
    loadNovelDetails();
  }, [novelId]);

  if (loading) {
    return (
      <div className="scroll-container animate-fade-in" style={{ textAlign: 'center', paddingTop: '100px', color: 'var(--text-secondary)' }}>
        <p>Loading novel details...</p>
      </div>
    );
  }

  if (!novel) {
    return (
      <div className="scroll-container animate-fade-in" style={{ textAlign: 'center', paddingTop: '80px' }}>
        <p>Book not found.</p>
        <button className="shelf-empty-btn" onClick={() => onNavigate('home')}>Go back store</button>
      </div>
    );
  }

  const isInShelf = shelfBookIds.includes(novel.id);
  const progress = readingProgress[novel.id];
  const lastReadChapterIndex = progress ? progress.chapterIndex : 0;

  // Sorting chapters list
  const sortedChapters = isDescendingOrder
    ? [...chapters].reverse()
    : chapters;

  return (
    <div className="page-container-full animate-fade-in">
      <div 
        className="scroll-container-no-pad" 
        style={{ flex: 1, minHeight: 0 }}
      >
        {/* Cover backdrop header */}
        <div className="detail-bg">
          <div className="detail-bg-blur" style={{ backgroundImage: `url(${novel.cover})` }} />
          
          {/* Back Header overlay */}
          <div style={{ position: 'relative', zIndex: 1, padding: '12px 16px 0', display: 'flex', justifyContent: 'space-between' }}>
            <button 
              className="header-btn" 
              onClick={() => onNavigate('home')} 
              style={{ backgroundColor: 'rgba(15,23,42,0.4)', color: 'white' }}
              aria-label="Back to store"
            >
              <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '18px', height: '18px' }}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
              </svg>
            </button>
          </div>

          <div className="detail-header-content">
            <img src={novel.cover} alt={novel.title} className="detail-cover" />
            <div className="detail-info">
              <h2 className="detail-title">{novel.title}</h2>
              <p className="detail-author">By {novel.author}</p>
              <div className="detail-meta-row">
                <span className="detail-badge" style={{ backgroundColor: 'var(--accent-color)' }}>{novel.genres[0]}</span>
                <span className="detail-badge">{novel.status}</span>
              </div>
              <div className="detail-rating">
                <span>⭐ {novel.rating}</span>
                <span style={{ color: 'rgba(255,255,255,0.7)', fontSize: '11px', fontWeight: 500 }}>(Real Reviews)</span>
              </div>
            </div>
          </div>
        </div>

        {/* Stats Row */}
        <div className="detail-stats-bar">
          <div className="detail-stat-item">
            <span className="detail-stat-val">{novel.words.toLocaleString()}</span>
            <span className="detail-stat-lbl">Words</span>
          </div>
          <div className="detail-stat-item">
            <span className="detail-stat-val">{novel.views.toLocaleString()}</span>
            <span className="detail-stat-lbl">Views</span>
          </div>
          <div className="detail-stat-item">
            <span className="detail-stat-val">{chapters.length}</span>
            <span className="detail-stat-lbl">Chapters</span>
          </div>
        </div>

        {/* Tabs for Info and Chapter Index */}
        <div className="detail-tabs">
          <button 
            className={`detail-tab ${activeTab === 'about' ? 'active' : ''}`}
            onClick={() => setActiveTab('about')}
          >
            About Info
          </button>
          <button 
            className={`detail-tab ${activeTab === 'chapters' ? 'active' : ''}`}
            onClick={() => setActiveTab('chapters')}
          >
            Chapters ({chapters.length})
          </button>
        </div>

        {/* Tab Contents */}
        <div style={{ padding: '16px' }}>
          {activeTab === 'about' ? (
            <div>
              <h3 className="hot-tags-title" style={{ fontSize: '14px', marginBottom: '8px' }}>Synopsis</h3>
              <div 
                className={`detail-synopsis ${isDescCollapsed ? 'collapsed' : ''}`}
                onClick={() => setIsDescCollapsed(!isDescCollapsed)}
              >
                {novel.synopsis}
              </div>
              <div 
                className="detail-synopsis-toggle"
                onClick={() => setIsDescCollapsed(!isDescCollapsed)}
              >
                {isDescCollapsed ? 'Expand Synopsis ▽' : 'Collapse Synopsis △'}
              </div>

              <h3 className="hot-tags-title" style={{ fontSize: '14px', margin: '24px 0 8px' }}>Genres</h3>
              <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap' }}>
                {novel.genres.map((g) => (
                  <span key={g} className="tag-pill" style={{ cursor: 'default' }}>
                    {g}
                  </span>
                ))}
              </div>
            </div>
          ) : (
            <div>
              <div className="chapters-header-row">
                <span>Total: {chapters.length} Chapters</span>
                <button 
                  onClick={() => setIsDescendingOrder(!isDescendingOrder)}
                  style={{ background: 'transparent', border: 'none', color: 'var(--accent-color)', fontWeight: 600, fontSize: '12px', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: '4px' }}
                >
                  {isDescendingOrder ? 'Oldest First ⬆️' : 'Newest First ⬇️'}
                </button>
              </div>

              <div className="chapters-list">
                {sortedChapters.map((ch, index) => {
                  const chIndex = isDescendingOrder ? chapters.length - 1 - index : index;
                  const isRead = progress && chIndex <= progress.chapterIndex;
                  return (
                    <div 
                      key={ch.id} 
                      className="chapter-item-link"
                      onClick={() => onNavigate('reader', { id: novel.id, chapterIndex: chIndex })}
                    >
                      <span className="chapter-item-title" style={{ color: isRead ? 'var(--text-tertiary)' : 'var(--text-primary)', fontWeight: isRead ? 400 : 500 }}>
                        {ch.title}
                        {isRead && <span style={{ fontSize: '10px', marginLeft: '6px', color: 'var(--accent-color)' }}>✓ Read</span>}
                      </span>
                      <span className="chapter-item-meta">{ch.word_count.toLocaleString()} words</span>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Floating Action CTA Panel at the Bottom */}
      <div className="detail-cta-bar glass-panel">
        <button 
          className="btn-cta-secondary"
          onClick={() => onToggleShelf(novel.id)}
        >
          {isInShelf ? (
            <>
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" style={{ width: '16px', height: '16px', color: '#ef4444' }}>
                <path fillRule="evenodd" d="M6.32 2.577a49.255 49.255 0 0111.36 0c1.497.174 2.57 1.46 2.57 2.93V21a.75.75 0 01-1.085.67L12 18.089l-7.165 3.583A.75.75 0 013.75 21V5.507c0-1.47 1.073-2.756 2.57-2.93z" clipRule="evenodd" />
              </svg>
              <span>Saved in Shelf</span>
            </>
          ) : (
            <>
              <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor" style={{ width: '16px', height: '16px' }}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M17.593 3.322c1.1.128 1.907 1.077 1.907 2.185V21L12 17.25 4.5 21V5.507c0-1.108.806-2.057 1.907-2.185a48.507 48.507 0 0111.186 0z" />
              </svg>
              <span>Add to Shelf</span>
            </>
          )}
        </button>
        <button 
          className="btn-cta-primary"
          onClick={() => onNavigate('reader', { id: novel.id, chapterIndex: lastReadChapterIndex })}
        >
          <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor" style={{ width: '18px', height: '18px' }}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-16.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-16.25v14.25" />
          </svg>
          <span>{progress ? 'Continue Reading' : 'Start Reading'}</span>
        </button>
      </div>
    </div>
  );
};

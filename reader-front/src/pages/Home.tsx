import React, { useState } from 'react';
import { Search as SearchIcon, Star, Flame, ArrowRight, Clock } from 'lucide-react';
import type { BookshelfItem, Novel } from '../utils/api';
import { BookCard } from '../components/BookCard';
import { GoldCoin } from '../components/GoldCoin';

const FALLBACK_COVER =
  'data:image/svg+xml;utf8,' +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 400"><rect width="300" height="400" fill="#262421"/><rect x="24" y="24" width="252" height="352" fill="none" stroke="#9A7132" stroke-opacity="0.4" stroke-width="2"/><text x="150" y="205" fill="#F4F1EA" font-family="Georgia, serif" font-size="22" text-anchor="middle">StarNovel</text></svg>',
  );

interface HomeProps {
  novels: Novel[];
  latestNovels?: Novel[];
  featuredNovels?: Novel[];
  curatedNovels?: Novel[];
  rankedNovels?: Novel[];
  recentBooks?: BookshelfItem[];
  hasMore?: boolean;
  loadingMore?: boolean;
  onLoadMore?: () => void;
  onNavigate: (page: string, params?: Record<string, unknown>) => void;
  userCoins: number;
}

export const Home: React.FC<HomeProps> = ({
  novels,
  latestNovels = [],
  featuredNovels = [],
  curatedNovels = [],
  rankedNovels: configuredRankings = [],
  recentBooks = [],
  hasMore = false,
  loadingMore = false,
  onLoadMore,
  onNavigate,
  userCoins,
}) => {
  const [activeTab, setActiveTab] = useState<'discover' | 'hot'>('discover');

  const formatViews = (views: number) => {
    if (views >= 1000000) return `${(views / 1000000).toFixed(1)}M`;
    if (views >= 1000) return `${Math.round(views / 1000)}k`;
    return String(views);
  };

  const rankedNovels =
    configuredRankings.length > 0
      ? configuredRankings
      : [...novels].sort((a, b) => b.views - a.views);
  const spotlightNovels = featuredNovels.length > 0 ? featuredNovels : novels.slice(0, 4);
  const displayedCurated = curatedNovels.length > 0 ? curatedNovels : novels;
  const usingCuratedOverride = curatedNovels.length > 0;

  return (
    <div className="home-page-layout animate-fade-in">
      {/* Mobile Sticky Header (< 768px) */}
      <div className="home-sticky-header glass-panel">
        <div className="home-header-top-row">
          <span className="home-brand-mobile">StarNovel</span>

          <button
            type="button"
            className="home-search-input-fake"
            onClick={() => onNavigate('search')}
            aria-label="Search books or authors"
          >
            <SearchIcon size={15} strokeWidth={1.75} aria-hidden="true" style={{ flexShrink: 0 }} />
            <span className="home-search-placeholder">Title or author...</span>
          </button>

          <button
            type="button"
            className="coin-pill-btn"
            onClick={() => onNavigate('recharge')}
            aria-label={`Wallet balance ${userCoins} coins, open top up`}
          >
            <GoldCoin size={14} />
            <span>{userCoins.toLocaleString()}</span>
            <span className="coin-pill-plus">+</span>
          </button>
        </div>

        <div className="home-header-tabs-row" role="tablist" aria-label="Store sections">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'discover'}
            className={`home-header-tab-btn ${activeTab === 'discover' ? 'active' : ''}`}
            onClick={() => setActiveTab('discover')}
          >
            For You
            {activeTab === 'discover' && <span className="home-header-tab-indicator" />}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'hot'}
            className={`home-header-tab-btn ${activeTab === 'hot' ? 'active' : ''}`}
            onClick={() => setActiveTab('hot')}
          >
            Top Charts
            {activeTab === 'hot' && <span className="home-header-tab-indicator" />}
          </button>
        </div>
      </div>

      {/* Tablet / Desktop Sub-Header Tabs (>= 768px) */}
      <div className="home-desktop-tabs-wrap">
        <div className="sn-shell-container home-header-tabs-row" role="tablist" aria-label="Store sections">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'discover'}
            className={`home-header-tab-btn ${activeTab === 'discover' ? 'active' : ''}`}
            onClick={() => setActiveTab('discover')}
          >
            For You
            {activeTab === 'discover' && <span className="home-header-tab-indicator" />}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'hot'}
            className={`home-header-tab-btn ${activeTab === 'hot' ? 'active' : ''}`}
            onClick={() => setActiveTab('hot')}
          >
            Top Charts
            {activeTab === 'hot' && <span className="home-header-tab-indicator" />}
          </button>
        </div>
      </div>

      {/* Main Scroll Content */}
      <div className="home-scroll-content">
        <div className="sn-shell-container">
          {activeTab === 'discover' ? (
            <div>
              {spotlightNovels.length > 0 && (
                <section style={{ marginBottom: '12px' }}>
                  <div className="section-header-row">
                    <div>
                      <div className="section-eyebrow">Editor&apos;s Picks</div>
                      <h2 className="section-title">Featured Stories</h2>
                    </div>
                  </div>
                  <div className="hero-slider">
                    {spotlightNovels.map((novel) => (
                      <article
                        key={novel.id}
                        className="spotlight-card"
                        onClick={() => onNavigate('detail', { id: novel.id })}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            onNavigate('detail', { id: novel.id });
                          }
                        }}
                        role="button"
                        tabIndex={0}
                      >
                        <img
                          src={novel.cover}
                          alt={novel.title}
                          className="spotlight-card-cover"
                        />
                        <div className="spotlight-card-info">
                          <div className="spotlight-card-meta">
                            <span>{novel.genres?.[0] || 'Fiction'}</span>
                            <span>·</span>
                            <span className="spotlight-rating">
                              <Star size={11} fill="currentColor" strokeWidth={0} aria-hidden="true" />
                              <span>{novel.rating}</span>
                            </span>
                          </div>
                          <h3 className="spotlight-card-title">{novel.title}</h3>
                          <p className="spotlight-card-desc">{novel.synopsis}</p>
                          <div className="spotlight-card-cta">
                            <span>Read Story</span>
                            <ArrowRight size={13} strokeWidth={2} aria-hidden="true" />
                          </div>
                        </div>
                      </article>
                    ))}
                  </div>
                </section>
              )}

              {recentBooks.length > 0 && (
                <section style={{ marginBottom: '28px' }}>
                  <div className="section-header-row">
                    <div>
                      <div className="section-eyebrow" style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
                        <Clock size={11} aria-hidden="true" />
                        <span>Pick Up Where You Left Off</span>
                      </div>
                      <h2 className="section-title">Recently Read</h2>
                    </div>
                    <button
                      type="button"
                      className="section-sub-link"
                      onClick={() => onNavigate('shelf', { tab: 'recent' })}
                    >
                      View all
                    </button>
                  </div>
                  <div className="book-grid">
                    {recentBooks.slice(0, 6).map((item) => (
                      <article
                        key={`recent-${item.novel_id}`}
                        className="book-card"
                        onClick={() =>
                          onNavigate('reader', {
                            id: item.novel_id,
                            chapterIndex: item.chapter_index,
                          })
                        }
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            onNavigate('reader', {
                              id: item.novel_id,
                              chapterIndex: item.chapter_index,
                            });
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
                          <div className="book-card-rating">
                            <span>Ch. {item.chapter_index + 1}</span>
                          </div>
                        </div>
                        <h3 className="book-card-title">{item.novel.title}</h3>
                        <span className="book-card-author">
                          Continue · {Math.round((item.scroll_offset_percentage || 0) * 100)}%
                        </span>
                      </article>
                    ))}
                  </div>
                </section>
              )}

              {latestNovels.length > 0 && (
                <section style={{ marginBottom: '32px' }}>
                  <div className="section-header-row">
                    <h2 className="section-title">New Releases</h2>
                    <button
                      type="button"
                      className="section-sub-link"
                      onClick={() => onNavigate('search')}
                    >
                      Browse all
                    </button>
                  </div>
                  <div className="book-grid">
                    {latestNovels.slice(0, 6).map((novel) => (
                      <BookCard
                        key={`new-${novel.id}`}
                        novel={novel}
                        onClick={() => onNavigate('detail', { id: novel.id })}
                      />
                    ))}
                  </div>
                </section>
              )}

              <section>
                <div className="section-header-row">
                  <h2 className="section-title">Curated For You</h2>
                </div>
                <div className="book-horizontal-list">
                  {displayedCurated.map((novel) => (
                    <BookCard
                      key={novel.id}
                      novel={novel}
                      layout="horizontal"
                      onClick={() => onNavigate('detail', { id: novel.id })}
                    />
                  ))}
                </div>

                {!usingCuratedOverride && hasMore && onLoadMore && (
                  <div style={{ display: 'flex', justifyContent: 'center', marginTop: '20px' }}>
                    <button
                      type="button"
                      className="btn-cta-secondary"
                      disabled={loadingMore}
                      onClick={onLoadMore}
                    >
                      {loadingMore ? 'Loading...' : 'Load More Stories'}
                    </button>
                  </div>
                )}
              </section>
            </div>
          ) : (
            <section>
              <div className="section-header-row">
                <div>
                  <div className="section-eyebrow">Most Read</div>
                  <h2 className="section-title">Popular Rankings</h2>
                </div>
              </div>

              <div className="rank-list">
                {rankedNovels.map((novel, index) => {
                  const rankNum = index + 1;
                  let rankClass = 'rank-number-other';
                  if (rankNum === 1) rankClass = 'rank-number-1';
                  else if (rankNum === 2) rankClass = 'rank-number-2';
                  else if (rankNum === 3) rankClass = 'rank-number-3';

                  return (
                    <article
                      key={novel.id}
                      className="rank-item"
                      onClick={() => onNavigate('detail', { id: novel.id })}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault();
                          onNavigate('detail', { id: novel.id });
                        }
                      }}
                      role="button"
                      tabIndex={0}
                    >
                      <div className={`rank-number-box ${rankClass}`}>
                        {rankNum}
                      </div>

                      <img src={novel.cover} alt={novel.title} className="rank-cover" loading="lazy" />

                      <div className="rank-info">
                        <h3 className="rank-title">{novel.title}</h3>
                        <p className="rank-author">By {novel.author}</p>

                        <div className="rank-meta">
                          <span className="rank-genre">{novel.genres?.[0] || 'Fiction'}</span>
                          <div style={{ display: 'flex', gap: '10px', alignItems: 'center' }}>
                            <span className="rating-inline">
                              <Star size={11} fill="currentColor" strokeWidth={0} aria-hidden="true" />
                              <span>{novel.rating}</span>
                            </span>
                            <span className="rank-views">
                              <Flame size={12} strokeWidth={1.8} aria-hidden="true" />
                              <span>{formatViews(novel.views)}</span>
                            </span>
                          </div>
                        </div>
                      </div>
                    </article>
                  );
                })}
              </div>
            </section>
          )}
        </div>
      </div>
    </div>
  );
};

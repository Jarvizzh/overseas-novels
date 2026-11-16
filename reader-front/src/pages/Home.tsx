import React, { useState } from 'react';
import { Search as SearchIcon, Star, Flame, ArrowRight } from 'lucide-react';
import type { Novel } from '../utils/api';
import { BookCard } from '../components/BookCard';
import { GoldCoin } from '../components/GoldCoin';

interface HomeProps {
  novels: Novel[];
  latestNovels?: Novel[];
  hasMore?: boolean;
  loadingMore?: boolean;
  onLoadMore?: () => void;
  onNavigate: (page: string, params?: Record<string, unknown>) => void;
  userCoins: number;
}

export const Home: React.FC<HomeProps> = ({
  novels,
  latestNovels = [],
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

  const rankedNovels = [...novels].sort((a, b) => b.views - a.views);
  const spotlightNovels = novels.slice(0, 4);

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
                  {novels.map((novel) => (
                    <BookCard
                      key={novel.id}
                      novel={novel}
                      layout="horizontal"
                      onClick={() => onNavigate('detail', { id: novel.id })}
                    />
                  ))}
                </div>

                {hasMore && onLoadMore && (
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

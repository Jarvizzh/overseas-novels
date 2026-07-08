import React, { useState } from 'react';
import type { Novel } from '../utils/api';
import { BookCard } from '../components/BookCard';
import { GoldCoin } from '../components/GoldCoin';

interface HomeProps {
  novels: Novel[];
  onNavigate: (page: string, params?: any) => void;
  userCoins: number;
}

export const Home: React.FC<HomeProps> = ({ novels, onNavigate, userCoins }) => {
  const [activeTab, setActiveTab] = useState<'discover' | 'hot'>('discover');

  // Format views count (e.g. 2.4M, 98k)
  const formatViews = (views: number) => {
    if (views >= 1000000) return (views / 1000000).toFixed(1) + 'M';
    if (views >= 1000) return (views / 1000).toFixed(0) + 'k';
    return String(views);
  };

  // For the HOT tab, sort novels by views (popularity)
  const getRankedNovels = () => {
    return [...novels].sort((a, b) => b.views - a.views);
  };

  return (
    <div className="home-page-layout animate-fade-in">
      {/* Custom Sticky Header with Search Box & Wallet & Tabs */}
      <div className="home-sticky-header">
        {/* Top Row: Search input + Coin Bubble */}
        <div className="home-header-top-row">
          <div 
            className="home-search-input-fake" 
            onClick={() => onNavigate('search')}
          >
            <svg 
              xmlns="http://www.w3.org/2000/svg" 
              fill="none" 
              viewBox="0 0 24 24" 
              strokeWidth={2.5} 
              stroke="currentColor" 
              className="home-search-icon"
            >
              <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.637 10.637z" />
            </svg>
            <span className="home-search-placeholder">Search book name/author</span>
          </div>

          <div 
            className="home-header-coin-bubble" 
            onClick={() => onNavigate('recharge')}
          >
            <GoldCoin size={14} />
            <span className="home-header-coin-count">{userCoins}</span>
            <span className="home-header-coin-plus">+</span>
          </div>
        </div>

        {/* Navigation Tabs Row: DISCOVER | HOT */}
        <div className="home-header-tabs-row">
          <button 
            className={`home-header-tab-btn ${activeTab === 'discover' ? 'active' : ''}`}
            onClick={() => setActiveTab('discover')}
          >
            DISCOVER
            {activeTab === 'discover' && <span className="home-header-tab-indicator" />}
          </button>
          <button 
            className={`home-header-tab-btn ${activeTab === 'hot' ? 'active' : ''}`}
            onClick={() => setActiveTab('hot')}
          >
            HOT
            {activeTab === 'hot' && <span className="home-header-tab-indicator" />}
          </button>
        </div>
      </div>

      {/* Main Scroll Content */}
      <div className="home-scroll-content">
        {activeTab === 'discover' ? (
          <div className="discover-tab-content">
            {/* Featured Banner Slider (Styled exactly like target's BannerInlineCard) */}
            <h2 className="section-title">
              <span>Featured Banner</span>
            </h2>
            <div className="hero-slider">
              {novels.slice(0, 4).map((novel) => (
                <div 
                  key={novel.id} 
                  className="banner-inline-card"
                  onClick={() => onNavigate('detail', { id: novel.id })}
                >
                  <div className="banner-inline-card__bg" style={{ backgroundImage: `url(${novel.cover})` }} />
                  <div className="banner-inline-card__mask" />
                  <div className="banner-inline-card__inner">
                    <div className="banner-inline-card__cover">
                      <img 
                        src={novel.cover} 
                        alt={novel.title} 
                        style={{ width: '100%', height: '100%', objectFit: 'cover' }} 
                      />
                    </div>
                    <div className="banner-inline-card__info">
                      <div className="banner-inline-card__title">{novel.title}</div>
                      <div className="banner-inline-card__desc">{novel.synopsis}</div>
                      <div className="banner-inline-card__tags">
                        {novel.genres.slice(0, 3).map((tag, i) => (
                          <span key={i} className="banner-inline-card__tag-item">{tag}</span>
                        ))}
                      </div>
                    </div>
                  </div>
                </div>
              ))}
            </div>

            {/* Editor's Choice Recommendations */}
            <h2 className="section-title">
              <span>Recommendations</span>
            </h2>
            <div style={{ display: 'flex', flexDirection: 'column' }}>
              {novels.map((novel) => (
                <BookCard 
                  key={novel.id} 
                  novel={novel} 
                  layout="horizontal" 
                  onClick={() => onNavigate('detail', { id: novel.id })}
                />
              ))}
            </div>
          </div>
        ) : (
          <div className="hot-tab-content">
            <h2 className="section-title">
              <span>Popular Rankings</span>
            </h2>
            
            <div className="rank-list">
              {getRankedNovels().map((novel, index) => {
                const rankNum = index + 1;
                let rankClass = 'rank-number-other';
                if (rankNum === 1) rankClass = 'rank-number-1';
                else if (rankNum === 2) rankClass = 'rank-number-2';
                else if (rankNum === 3) rankClass = 'rank-number-3';

                return (
                  <div 
                    key={novel.id} 
                    className="rank-item"
                    onClick={() => onNavigate('detail', { id: novel.id })}
                  >
                    <div className={`rank-number-box ${rankClass}`}>
                      {rankNum}
                    </div>
                    
                    <img src={novel.cover} alt={novel.title} className="rank-cover" />
                    
                    <div className="rank-info">
                      <h3 className="rank-title">{novel.title}</h3>
                      <p className="rank-author">By {novel.author}</p>
                      
                      <div className="rank-meta">
                        <span className="rank-genre">{novel.genres[0]}</span>
                        <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
                          <span className="rank-score">⭐ {novel.rating}</span>
                          <span className="rank-views">🔥 {formatViews(novel.views)}</span>
                        </div>
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </div>
    </div>
  );
};

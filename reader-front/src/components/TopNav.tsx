import React from 'react';
import { Search, Sparkles } from 'lucide-react';
import { GoldCoin } from './GoldCoin';
import type { NavTabType } from './BottomNav';

interface TopNavProps {
  activeTab: NavTabType | null;
  onChangeTab: (tab: NavTabType) => void;
  onNavigate: (page: string) => void;
  userCoins: number;
}

export const TopNav: React.FC<TopNavProps> = ({
  activeTab,
  onChangeTab,
  onNavigate,
  userCoins,
}) => {
  const navItems: Array<{ id: NavTabType; label: string }> = [
    { id: 'home', label: 'Discover' },
    { id: 'shelf', label: 'Library' },
    { id: 'rewards', label: 'Rewards' },
    { id: 'profile', label: 'Profile' },
  ];

  return (
    <header className="top-nav glass-panel">
      <div className="top-nav-inner">
        <div className="top-nav-left">
          <button
            type="button"
            className="top-nav-brand"
            onClick={() => onChangeTab('home')}
            aria-label="StarNovel Home"
          >
            <Sparkles size={18} strokeWidth={1.75} style={{ color: 'var(--accent-color)' }} aria-hidden="true" />
            <span>StarNovel</span>
          </button>

          <nav className="top-nav-links" aria-label="Primary">
            {navItems.map((item) => {
              const isActive = activeTab === item.id;
              return (
                <button
                  key={item.id}
                  type="button"
                  className={`top-nav-link ${isActive ? 'active' : ''}`}
                  onClick={() => onChangeTab(item.id)}
                  aria-current={isActive ? 'page' : undefined}
                >
                  {item.label}
                </button>
              );
            })}
          </nav>
        </div>

        <div className="top-nav-right">
          <button
            type="button"
            className="top-nav-search-btn"
            onClick={() => onNavigate('search')}
            aria-label="Search books or authors"
          >
            <Search size={15} strokeWidth={1.75} aria-hidden="true" />
            <span>Search titles, authors...</span>
          </button>

          <button
            type="button"
            className="coin-pill-btn"
            onClick={() => onNavigate('recharge')}
            aria-label={`Wallet balance ${userCoins} coins, open top up`}
          >
            <GoldCoin size={15} />
            <span>{userCoins.toLocaleString()}</span>
            <span className="coin-pill-plus">+</span>
          </button>
        </div>
      </div>
    </header>
  );
};

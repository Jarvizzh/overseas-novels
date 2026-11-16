import React from 'react';
import { BookOpen, Compass, Gift, User } from 'lucide-react';

export type NavTabType = 'home' | 'shelf' | 'rewards' | 'profile';

interface BottomNavProps {
  activeTab: NavTabType;
  onChangeTab: (tab: NavTabType) => void;
}

export const BottomNav: React.FC<BottomNavProps> = ({ activeTab, onChangeTab }) => {
  const items: Array<{ id: NavTabType; label: string; Icon: React.FC<{ size?: number; strokeWidth?: number }> }> = [
    { id: 'shelf', label: 'Library', Icon: BookOpen },
    { id: 'home', label: 'Discover', Icon: Compass },
    { id: 'rewards', label: 'Rewards', Icon: Gift },
    { id: 'profile', label: 'Profile', Icon: User },
  ];

  return (
    <nav className="bottom-nav glass-panel" aria-label="Primary mobile">
      {items.map(({ id, label, Icon }) => {
        const isActive = activeTab === id;
        return (
          <button
            key={id}
            type="button"
            className={`nav-item ${isActive ? 'active' : ''}`}
            onClick={() => onChangeTab(id)}
            aria-current={isActive ? 'page' : undefined}
          >
            <span className="nav-item-icon" aria-hidden="true">
              <Icon size={20} strokeWidth={isActive ? 2 : 1.65} />
            </span>
            <span>{label}</span>
          </button>
        );
      })}
    </nav>
  );
};

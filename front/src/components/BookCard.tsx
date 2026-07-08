import React from 'react';
import type { Novel } from '../utils/api';

interface BookCardProps {
  novel: Novel;
  onClick: () => void;
  layout?: 'grid' | 'horizontal';
}

export const BookCard: React.FC<BookCardProps> = ({
  novel,
  onClick,
  layout = 'grid',
}) => {
  if (layout === 'horizontal') {
    return (
      <div className="book-card-horizontal" onClick={onClick}>
        <img 
          src={novel.cover} 
          alt={novel.title} 
          className="book-card-horizontal-cover"
          loading="lazy"
          onError={(e) => {
            // fallback in case of loading error
            (e.target as HTMLImageElement).src = 'https://placehold.co/150x200?text=Novel';
          }}
        />
        <div className="book-card-horizontal-info">
          <div>
            <h3 className="book-card-horizontal-title">{novel.title}</h3>
            <p className="book-card-horizontal-desc">{novel.synopsis}</p>
          </div>
          <div className="book-card-horizontal-meta">
            <span className="book-card-horizontal-author">{novel.author}</span>
            <div className="book-card-horizontal-stats">
              <span className="book-card-horizontal-genre">{novel.genres[0]}</span>
              <span>⭐ {novel.rating}</span>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="book-card" onClick={onClick}>
      <div className="book-card-cover-wrapper">
        <img 
          src={novel.cover} 
          alt={novel.title} 
          className="book-card-cover" 
          loading="lazy"
          onError={(e) => {
            (e.target as HTMLImageElement).src = 'https://placehold.co/150x200?text=Novel';
          }}
        />
        <div className="book-card-rating">
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" style={{ width: '10px', height: '10px' }}>
            <path fillRule="evenodd" d="M10.788 3.21c.448-1.077 1.976-1.077 2.424 0l2.082 5.007 5.404.433c1.164.093 1.636 1.545.749 2.305l-4.117 3.527 1.257 5.273c.271 1.136-.964 2.033-1.96 1.425L12 18.354 7.373 21.18c-.996.608-2.231-.29-1.96-1.425l1.257-5.273-4.117-3.527c-.887-.76-.415-2.212.749-2.305l5.404-.433 2.082-5.006z" clipRule="evenodd" />
          </svg>
          <span>{novel.rating}</span>
        </div>
      </div>
      <h3 className="book-card-title">{novel.title}</h3>
      <span className="book-card-author">{novel.author}</span>
    </div>
  );
};

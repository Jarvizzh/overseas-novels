import React from 'react';
import { Star } from 'lucide-react';
import type { Novel } from '../utils/api';

const FALLBACK_COVER =
  "data:image/svg+xml;utf8," +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 400"><rect width="300" height="400" fill="#262421"/><rect x="24" y="24" width="252" height="352" fill="none" stroke="#9A7132" stroke-opacity="0.4" stroke-width="2"/><text x="150" y="205" fill="#F4F1EA" font-family="Georgia, serif" font-size="22" text-anchor="middle">StarNovel</text></svg>'
  );

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
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      onClick();
    }
  };

  if (layout === 'horizontal') {
    return (
      <article
        className="book-card-horizontal"
        onClick={onClick}
        onKeyDown={handleKeyDown}
        role="button"
        tabIndex={0}
      >
        <img
          src={novel.cover || FALLBACK_COVER}
          alt={novel.title}
          className="book-card-horizontal-cover"
          loading="lazy"
          onError={(e) => {
            const img = e.currentTarget;
            if (img.src !== FALLBACK_COVER) img.src = FALLBACK_COVER;
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
              {novel.genres?.[0] && (
                <span className="book-card-horizontal-genre">{novel.genres[0]}</span>
              )}
              <span className="rating-inline">
                <Star size={11} fill="currentColor" strokeWidth={0} aria-hidden="true" />
                <span>{novel.rating}</span>
              </span>
            </div>
          </div>
        </div>
      </article>
    );
  }

  return (
    <article
      className="book-card"
      onClick={onClick}
      onKeyDown={handleKeyDown}
      role="button"
      tabIndex={0}
    >
      <div className="book-card-cover-wrapper">
        <img
          src={novel.cover || FALLBACK_COVER}
          alt={novel.title}
          className="book-card-cover"
          loading="lazy"
          onError={(e) => {
            const img = e.currentTarget;
            if (img.src !== FALLBACK_COVER) img.src = FALLBACK_COVER;
          }}
        />
        <div className="book-card-rating">
          <Star size={10} fill="currentColor" strokeWidth={0} aria-hidden="true" />
          <span>{novel.rating}</span>
        </div>
      </div>
      <h3 className="book-card-title">{novel.title}</h3>
      <span className="book-card-author">{novel.author}</span>
    </article>
  );
};

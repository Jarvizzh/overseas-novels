import React, { useState, useEffect, useMemo } from 'react';
import { Search as SearchIcon, X, TrendingUp, Clock } from 'lucide-react';
import { api } from '../utils/api';
import type { Novel } from '../utils/api';
import { BookCard } from '../components/BookCard';

interface SearchProps {
  novels: Novel[];
  onNavigate: (page: string, params?: Record<string, unknown>) => void;
  initialGenre?: string;
}

function loadSearchHistory(): string[] {
  try {
    const raw = localStorage.getItem('search-history');
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === 'string') : [];
  } catch {
    return [];
  }
}

export const Search: React.FC<SearchProps> = ({ novels, onNavigate, initialGenre }) => {
  const [query, setQuery] = useState('');
  const [selectedGenre, setSelectedGenre] = useState<string>(initialGenre || 'All');
  const [searchHistory, setSearchHistory] = useState<string[]>(loadSearchHistory);
  const [results, setResults] = useState<Novel[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (initialGenre) {
      setSelectedGenre(initialGenre);
    }
  }, [initialGenre]);

  useEffect(() => {
    const performSearch = async () => {
      setLoading(true);
      try {
        let list: Novel[] = [];
        if (query.trim()) {
          list = (await api.searchNovels(query.trim(), 1, 50)) || [];
          if (selectedGenre !== 'All') {
            list = list.filter((n) => (n.genres || []).includes(selectedGenre));
          }
        } else if (selectedGenre === 'All') {
          list = (await api.getNovels({ limit: 50 })) || [];
        } else {
          list = (await api.getNovels({ genre: selectedGenre, limit: 50 })) || [];
        }
        setResults(list);
      } catch (err) {
        console.error('Search failed:', err);
      } finally {
        setLoading(false);
      }
    };

    const delayDebounce = setTimeout(() => {
      performSearch();
    }, 280);

    return () => clearTimeout(delayDebounce);
  }, [query, selectedGenre]);

  const allGenres = useMemo(() => {
    const base = ['All', 'Fantasy', 'Romance', 'Xianxia', 'CEO', 'Sci-Fi', 'Cozy'];
    const dynamicGenres = new Set<string>(base);
    novels.forEach((n) => (n.genres || []).forEach((g) => dynamicGenres.add(g)));
    return Array.from(dynamicGenres);
  }, [novels]);

  const trendingTerms = useMemo(() => {
    if (novels.length > 0) {
      return novels.slice(0, 4).map((n) => n.title);
    }
    return ['Romance', 'Fantasy', 'Billionaire', 'Sci-Fi'];
  }, [novels]);

  const handleSearchSubmit = (searchTerm: string) => {
    const trimmed = searchTerm.trim();
    if (!trimmed) return;
    setQuery(trimmed);

    const updatedHistory = [
      trimmed,
      ...searchHistory.filter((h) => h !== trimmed),
    ].slice(0, 6);

    setSearchHistory(updatedHistory);
    try {
      localStorage.setItem('search-history', JSON.stringify(updatedHistory));
    } catch {
      // Ignore storage errors
    }
  };

  const clearHistory = () => {
    setSearchHistory([]);
    try {
      localStorage.removeItem('search-history');
    } catch {
      // Ignore storage errors
    }
  };

  return (
    <div className="scroll-container animate-fade-in">
      <div className="sn-shell-container">
        {/* Search Input Box */}
        <div className="search-input-wrapper">
          <SearchIcon size={17} strokeWidth={1.8} style={{ color: 'var(--text-tertiary)', flexShrink: 0 }} aria-hidden="true" />
          <input
            type="search"
            aria-label="Search book title, author, or keywords"
            placeholder="Search title, author, or keyword..."
            className="search-input"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') handleSearchSubmit(query);
            }}
          />
          {query && (
            <button
              type="button"
              onClick={() => setQuery('')}
              aria-label="Clear search"
              style={{
                background: 'transparent',
                border: 'none',
                color: 'var(--text-tertiary)',
                cursor: 'pointer',
                padding: '4px',
                display: 'inline-flex',
                alignItems: 'center',
              }}
            >
              <X size={16} strokeWidth={2} aria-hidden="true" />
            </button>
          )}
        </div>

        {/* Genre Filter Pills */}
        <div className="genre-filter-row" role="group" aria-label="Filter by genre">
          {allGenres.map((genre) => (
            <button
              key={genre}
              type="button"
              className={`genre-filter-btn ${selectedGenre === genre ? 'active' : ''}`}
              onClick={() => setSelectedGenre(genre)}
              aria-pressed={selectedGenre === genre}
            >
              {genre}
            </button>
          ))}
        </div>

        {/* Discovery Tags & Recent History when query is empty */}
        {!query.trim() && (
          <>
            <h2 className="hot-tags-title">Trending Searches</h2>
            <div className="tag-cloud">
              {trendingTerms.map((term) => (
                <button
                  key={term}
                  type="button"
                  className="tag-pill"
                  onClick={() => handleSearchSubmit(term)}
                >
                  <TrendingUp size={12} strokeWidth={2} style={{ color: 'var(--accent-text)' }} aria-hidden="true" />
                  <span>{term}</span>
                </button>
              ))}
            </div>

            {searchHistory.length > 0 && (
              <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '10px' }}>
                  <h2 className="hot-tags-title" style={{ margin: 0 }}>Recent Searches</h2>
                  <button
                    type="button"
                    onClick={clearHistory}
                    style={{
                      background: 'transparent',
                      border: 'none',
                      color: 'var(--accent-text)',
                      fontSize: '0.75rem',
                      fontWeight: 600,
                      cursor: 'pointer',
                    }}
                  >
                    Clear All
                  </button>
                </div>
                <div className="tag-cloud">
                  {searchHistory.map((hist) => (
                    <button
                      key={hist}
                      type="button"
                      className="tag-pill"
                      onClick={() => setQuery(hist)}
                    >
                      <Clock size={12} strokeWidth={1.8} aria-hidden="true" />
                      <span>{hist}</span>
                    </button>
                  ))}
                </div>
              </div>
            )}
          </>
        )}

        {/* Results Header */}
        <h2 className="hot-tags-title" style={{ marginTop: '8px' }}>
          {query.trim()
            ? `Search Results (${results.length})`
            : selectedGenre === 'All'
              ? 'All Stories'
              : `${selectedGenre} Stories`}
        </h2>

        {/* Search Result List */}
        {loading ? (
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '48px 0', gap: '10px', color: 'var(--text-secondary)' }}>
            <div className="sn-spinner" />
            <span style={{ fontSize: '0.875rem' }}>Searching catalog...</span>
          </div>
        ) : results.length > 0 ? (
          <div className="book-horizontal-list">
            {results.map((novel) => (
              <BookCard
                key={novel.id}
                novel={novel}
                layout="horizontal"
                onClick={() => onNavigate('detail', { id: novel.id })}
              />
            ))}
          </div>
        ) : (
          <div className="shelf-empty-state">
            <div className="shelf-empty-icon-wrap">
              <SearchIcon size={24} strokeWidth={1.65} aria-hidden="true" />
            </div>
            <p style={{ fontWeight: 600, color: 'var(--text-primary)' }}>No stories matched your search</p>
            <p style={{ fontSize: '0.8125rem' }}>Try another keyword or switch genre filters.</p>
          </div>
        )}
      </div>
    </div>
  );
};

import React, { useState, useEffect } from 'react';
import { api } from '../utils/api';
import type { Novel } from '../utils/api';
import { BookCard } from '../components/BookCard';

interface SearchProps {
  novels: Novel[];
  onNavigate: (page: string, params?: any) => void;
  initialGenre?: string;
}

export const Search: React.FC<SearchProps> = ({ onNavigate, initialGenre }) => {
  const [query, setQuery] = useState('');
  const [selectedGenre, setSelectedGenre] = useState<string>(initialGenre || 'All');
  const [searchHistory, setSearchHistory] = useState<string[]>([]);
  const [results, setResults] = useState<Novel[]>([]);
  const [loading, setLoading] = useState(false);

  // Load search history on mount
  useEffect(() => {
    const history = localStorage.getItem('search-history');
    if (history) {
      setSearchHistory(JSON.parse(history));
    }
  }, []);

  // Fetch search results from backend reactively
  useEffect(() => {
    const performSearch = async () => {
      setLoading(true);
      try {
        let list: Novel[] = [];
        if (query.trim()) {
          list = await api.searchNovels(query.trim());
          if (selectedGenre !== 'All') {
            list = list.filter((n) => n.genres.includes(selectedGenre));
          }
        } else {
          if (selectedGenre === 'All') {
            list = await api.getNovels();
          } else {
            list = await api.getNovels({ genre: selectedGenre });
          }
        }
        setResults(list);
      } catch (err) {
        console.error("Search failed:", err);
      } finally {
        setLoading(false);
      }
    };

    const delayDebounce = setTimeout(() => {
      performSearch();
    }, 300);

    return () => clearTimeout(delayDebounce);
  }, [query, selectedGenre]);

  const allGenres = ['All', 'Fantasy', 'Xianxia', 'Romance', 'CEO', 'Sci-Fi', 'Cozy'];

  const handleSearchSubmit = (searchTerm: string) => {
    if (!searchTerm.trim()) return;
    setQuery(searchTerm);
    
    // Add to history and persist
    const updatedHistory = [
      searchTerm,
      ...searchHistory.filter((h) => h !== searchTerm)
    ].slice(0, 5);
    
    setSearchHistory(updatedHistory);
    localStorage.setItem('search-history', JSON.stringify(updatedHistory));
  };

  const clearHistory = () => {
    setSearchHistory([]);
    localStorage.removeItem('search-history');
  };

  return (
    <div className="scroll-container animate-fade-in" style={{ paddingBottom: '90px' }}>
      {/* Search Input Box */}
      <div className="search-input-wrapper">
        <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '18px', height: '18px', color: 'var(--text-tertiary)' }}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.637 10.637z" />
        </svg>
        <input 
          type="text" 
          placeholder="Search book title, author, key terms..." 
          className="search-input"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') handleSearchSubmit(query);
          }}
        />
        {query && (
          <button 
            onClick={() => setQuery('')}
            style={{ background: 'transparent', border: 'none', color: 'var(--text-tertiary)', cursor: 'pointer' }}
          >
            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '16px', height: '16px' }}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        )}
      </div>

      {/* Genre Filter Pills */}
      <div className="genre-filter-row">
        {allGenres.map((genre) => (
          <button
            key={genre}
            className={`genre-filter-btn ${selectedGenre === genre ? 'active' : ''}`}
            onClick={() => setSelectedGenre(genre)}
          >
            {genre}
          </button>
        ))}
      </div>

      {/* Conditional Search History and Hot Search tags */}
      {!query && (
        <>
          {/* Hot Searches */}
          <h3 className="hot-tags-title">Trending Searches</h3>
          <div className="tag-cloud">
            {['Celestial Monarch', 'Billionaire', 'System Cheat', 'Cozy Alchemist'].map((tag) => (
              <span 
                key={tag} 
                className="tag-pill"
                onClick={() => handleSearchSubmit(tag)}
              >
                🔥 {tag}
              </span>
            ))}
          </div>

          {/* Search History */}
          {searchHistory.length > 0 && (
            <div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '10px' }}>
                <h3 className="hot-tags-title" style={{ margin: 0 }}>Search History</h3>
                <button 
                  onClick={clearHistory}
                  style={{ background: 'transparent', border: 'none', color: 'var(--accent-color)', fontSize: '12px', fontWeight: 600, cursor: 'pointer' }}
                >
                  Clear All
                </button>
              </div>
              <div className="tag-cloud">
                {searchHistory.map((hist) => (
                  <span 
                    key={hist} 
                    className="tag-pill"
                    onClick={() => setQuery(hist)}
                  >
                    ⏱️ {hist}
                  </span>
                ))}
              </div>
            </div>
          )}
        </>
      )}

      {/* Results Header */}
      {query && (
        <h3 className="hot-tags-title" style={{ margin: '12px 0 8px' }}>
          Search Results ({results.length})
        </h3>
      )}

      {/* Search Result List */}
      <div style={{ display: 'flex', flexDirection: 'column' }}>
        {loading ? (
          <div style={{ textAlign: 'center', padding: '40px', color: 'var(--text-secondary)' }}>
            Loading search results...
          </div>
        ) : results.length > 0 ? (
          results.map((novel) => (
            <BookCard 
              key={novel.id} 
              novel={novel} 
              layout="horizontal" 
              onClick={() => onNavigate('detail', { id: novel.id })}
            />
          ))
        ) : (
          <div className="shelf-empty-state">
            <span className="shelf-empty-icon">🔍</span>
            <p>No novels matched your search.</p>
            <p style={{ fontSize: '12px', marginTop: '4px' }}>Try using different keywords or categories.</p>
          </div>
        )}
      </div>
    </div>
  );
};

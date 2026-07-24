import React, { useState } from 'react';
import type { Novel } from '../utils/api';

interface ShelfProps {
  novels: Novel[];
  onNavigate: (page: string, params?: any) => void;
  shelfBookIds: number[];
  onRemoveFromShelf: (bookIds: number[]) => void;
  readingProgress: {
    [bookId: number]: {
      chapterIndex: number;
      scrollOffsetPercentage: number;
    };
  };
}

export const Shelf: React.FC<ShelfProps> = ({
  novels,
  onNavigate,
  shelfBookIds,
  onRemoveFromShelf,
  readingProgress,
}) => {
  const [isEditMode, setIsEditMode] = useState(false);
  const [selectedIds, setSelectedIds] = useState<number[]>([]);

  const shelfNovels = novels.filter((n) => shelfBookIds.includes(n.id));

  const handleToggleSelect = (id: number) => {
    if (selectedIds.includes(id)) {
      setSelectedIds(selectedIds.filter((item) => item !== id));
    } else {
      setSelectedIds([...selectedIds, id]);
    }
  };

  const handleDeleteSelected = () => {
    onRemoveFromShelf(selectedIds);
    setSelectedIds([]);
    setIsEditMode(false);
  };

  const handleCancelEdit = () => {
    setSelectedIds([]);
    setIsEditMode(false);
  };

  return (
    <div className="scroll-container animate-fade-in" style={{ paddingBottom: isEditMode ? '80px' : '20px' }}>
      {/* Shelf Header Actions */}
      <div className="shelf-header">
        <h2 className="shelf-title">My Shelf</h2>
        {shelfNovels.length > 0 && (
          <button 
            onClick={isEditMode ? handleCancelEdit : () => setIsEditMode(true)}
            style={{ 
              background: 'transparent', 
              border: 'none', 
              color: 'var(--accent-color)', 
              fontWeight: 600, 
              fontSize: '14px', 
              cursor: 'pointer' 
            }}
          >
            {isEditMode ? 'Cancel' : 'Edit'}
          </button>
        )}
      </div>

      {shelfNovels.length > 0 ? (
        <div className="shelf-grid">
          {shelfNovels.map((novel) => {
            const progress = readingProgress[novel.id];
            const isSelected = selectedIds.includes(novel.id);
            
            // Calculate progress percentage
            let progressText = 'Unread';
            let progressPercent = 0;
            if (progress) {
              const chNum = progress.chapterIndex + 1;
              const chScrollPercent = Math.round(progress.scrollOffsetPercentage * 100);
              progressText = `Ch ${chNum} (${chScrollPercent}%)`;
              const totalChCount = (novel as any).chapters?.length || 10;
              progressPercent = ((progress.chapterIndex + progress.scrollOffsetPercentage) / totalChCount) * 100;
            }

            return (
              <div 
                key={novel.id} 
                className="shelf-item"
                onClick={() => {
                  if (isEditMode) {
                    handleToggleSelect(novel.id);
                  } else {
                    onNavigate('detail', { id: novel.id });
                  }
                }}
              >
                {/* Checkbox overlay in edit mode */}
                {isEditMode && (
                  <div className={`shelf-checkbox ${isSelected ? 'checked' : ''}`}>
                    {isSelected && (
                      <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" style={{ width: '12px', height: '12px' }}>
                        <path fillRule="evenodd" d="M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z" clipRule="evenodd" />
                      </svg>
                    )}
                  </div>
                )}

                {/* Cover Wrapper with Reading Progress bar */}
                <div className="book-card-cover-wrapper" style={{ opacity: isEditMode && !isSelected ? 0.7 : 1 }}>
                  <img src={novel.cover} alt={novel.title} className="book-card-cover" />
                  
                  {/* Progress Line */}
                  <div className="shelf-progress-bar">
                    <div 
                      className="shelf-progress-fill" 
                      style={{ width: `${Math.max(3, progressPercent)}%` }} 
                    />
                  </div>
                </div>

                {/* Novel Info */}
                <h3 className="book-card-title">{novel.title}</h3>
                <span 
                  className="book-card-author" 
                  style={{ 
                    color: progress ? 'var(--accent-color)' : 'var(--text-tertiary)',
                    fontWeight: progress ? 600 : 400 
                  }}
                >
                  {progressText}
                </span>
              </div>
            );
          })}
        </div>
      ) : (
        <div className="shelf-empty-state">
          <span className="shelf-empty-icon">📚</span>
          <p>Your library shelf is empty.</p>
          <p style={{ fontSize: '12px', marginTop: '4px' }}>Save your favorite books to read them later!</p>
          <button 
            className="shelf-empty-btn" 
            onClick={() => onNavigate('home')}
          >
            Browse Books
          </button>
        </div>
      )}

      {/* Edit Mode Bottom Action Bar */}
      {isEditMode && (
        <div className="shelf-edit-bar glass-panel animate-slide-up">
          <span style={{ fontSize: '14px', fontWeight: 600, color: 'var(--text-primary)' }}>
            Selected: {selectedIds.length} books
          </span>
          <button 
            className="shelf-edit-btn-delete"
            onClick={handleDeleteSelected}
            disabled={selectedIds.length === 0}
          >
            Delete
          </button>
        </div>
      )}
    </div>
  );
};

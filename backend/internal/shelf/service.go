package shelf

import (
	"context"
	"time"

	"novel-backend/internal/model"
)

type ProgressUpdate struct {
	NovelID                int64     `json:"novel_id" binding:"required"`
	ChapterIndex           int       `json:"chapter_index"`
	ScrollOffsetPercentage float64   `json:"scroll_offset_percentage"`
	UpdatedAt              time.Time `json:"updated_at" binding:"required"`
}

type Service interface {
	GetShelf(ctx context.Context, userID int64) ([]model.BookshelfWithNovel, error)
	AddToShelf(ctx context.Context, userID int64, novelID int64) error
	RemoveFromShelf(ctx context.Context, userID int64, novelIDs []int64) error
	SyncProgress(ctx context.Context, userID int64, updates []ProgressUpdate) error
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{
		repo: repo,
	}
}

func (s *service) GetShelf(ctx context.Context, userID int64) ([]model.BookshelfWithNovel, error) {
	return s.repo.GetShelfItemsWithNovel(ctx, userID)
}

func (s *service) AddToShelf(ctx context.Context, userID int64, novelID int64) error {
	return s.repo.AddToShelf(ctx, userID, novelID)
}

func (s *service) RemoveFromShelf(ctx context.Context, userID int64, novelIDs []int64) error {
	return s.repo.RemoveFromShelf(ctx, userID, novelIDs)
}

func (s *service) SyncProgress(ctx context.Context, userID int64, updates []ProgressUpdate) error {
	for _, update := range updates {
		current, err := s.repo.GetProgress(ctx, userID, update.NovelID)
		if err != nil {
			return err
		}

		if current != nil {
			if !update.UpdatedAt.After(current.UpdatedAt) {
				continue
			}
		}

		err = s.repo.SaveProgress(ctx, userID, update.NovelID, update.ChapterIndex, update.ScrollOffsetPercentage, update.UpdatedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

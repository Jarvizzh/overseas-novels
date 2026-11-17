package shelf

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"reader-backend/internal/model"
	"reader-backend/internal/storage"
)

const MaxBatchSize = 100

var ErrInvalidBatch = errors.New("batch must contain 1–100 positive novel IDs")

var ErrInvalidProgress = errors.New("invalid reading progress")

type ProgressUpdate struct {
	Version                string    `json:"version,omitempty"`
	NovelID                int64     `json:"novel_id" binding:"required"`
	ChapterIndex           int       `json:"chapter_index"`
	ScrollOffsetPercentage float64   `json:"scroll_offset_percentage"`
	UpdatedAt              time.Time `json:"updated_at" binding:"required"`
}

type Service interface {
	GetProgress(ctx context.Context, userID, novelID int64) (*ProgressUpdate, error)
	GetShelf(ctx context.Context, userID int64) ([]model.BookshelfWithNovel, error)
	AddToShelf(ctx context.Context, userID int64, novelID int64) error
	RemoveFromShelf(ctx context.Context, userID int64, novelIDs []int64) error
	SyncProgress(ctx context.Context, userID int64, updates []ProgressUpdate) error
}

type service struct {
	repo        Repository
	coverSigner storage.CoverSigner
}

func NewService(repo Repository, coverSigner storage.CoverSigner) Service {
	return &service{
		repo:        repo,
		coverSigner: coverSigner,
	}
}

func (s *service) GetProgress(ctx context.Context, userID, novelID int64) (*ProgressUpdate, error) {
	if novelID <= 0 || novelID > math.MaxInt32 {
		return nil, ErrInvalidProgress
	}
	p, err := s.repo.GetProgress(ctx, userID, novelID)
	if err != nil || p == nil || !p.UpdatedAt.After(time.Unix(0, 0)) {
		return nil, err
	}
	return &ProgressUpdate{
		NovelID: p.NovelID, ChapterIndex: p.ChapterIndex,
		ScrollOffsetPercentage: p.ScrollOffsetPercentage, UpdatedAt: p.UpdatedAt, Version: p.ProgressVersion,
	}, nil
}

func (s *service) GetShelf(ctx context.Context, userID int64) ([]model.BookshelfWithNovel, error) {
	items, err := s.repo.GetShelfItemsWithNovel(ctx, userID)
	if err != nil {
		return nil, err
	}
	if s.coverSigner != nil {
		for i := range items {
			items[i].Novel.CoverURL = s.coverSigner.SignCoverURL(items[i].Novel.CoverURL)
		}
	}
	return items, nil
}

func (s *service) AddToShelf(ctx context.Context, userID int64, novelID int64) error {
	if novelID <= 0 {
		return ErrInvalidBatch
	}
	return s.repo.AddToShelf(ctx, userID, novelID)
}

func (s *service) RemoveFromShelf(ctx context.Context, userID int64, novelIDs []int64) error {
	if len(novelIDs) == 0 || len(novelIDs) > MaxBatchSize {
		return ErrInvalidBatch
	}
	for _, id := range novelIDs {
		if id <= 0 {
			return ErrInvalidBatch
		}
	}
	return s.repo.RemoveFromShelf(ctx, userID, novelIDs)
}

func (s *service) SyncProgress(ctx context.Context, userID int64, updates []ProgressUpdate) error {
	if len(updates) == 0 || len(updates) > MaxBatchSize {
		return ErrInvalidBatch
	}
	latest := make(map[int64]ProgressUpdate, len(updates))
	maxTime := time.Now().Add(5 * time.Minute)
	for _, update := range updates {
		if update.NovelID <= 0 || update.NovelID > math.MaxInt32 ||
			update.ChapterIndex < 0 || update.ChapterIndex > math.MaxInt32 ||
			math.IsNaN(update.ScrollOffsetPercentage) || update.ScrollOffsetPercentage < 0 || update.ScrollOffsetPercentage > 1 ||
			update.UpdatedAt.IsZero() || update.UpdatedAt.After(maxTime) || len(update.Version) > 64 ||
			strings.ContainsFunc(update.Version, func(r rune) bool { return r != '-' && (r < '0' || r > '9') }) {
			return ErrInvalidProgress
		}
		if previous, ok := latest[update.NovelID]; !ok || update.UpdatedAt.After(previous.UpdatedAt) || (update.UpdatedAt.Equal(previous.UpdatedAt) && update.Version > previous.Version) {
			latest[update.NovelID] = update
		}
	}
	batch := make([]ProgressUpdate, 0, len(latest))
	for _, update := range latest {
		batch = append(batch, update)
	}
	return s.repo.SaveProgressBatch(ctx, userID, batch)
}

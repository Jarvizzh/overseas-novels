package novel

import (
	"context"
	"strings"

	"novel-backend/internal/model"
)

type ChapterReadResult struct {
	Chapter *model.Chapter `json:"chapter"`
	Locked  bool           `json:"locked"`
	Price   int            `json:"price"`
}

type Service interface {
	GetNovels(ctx context.Context, genre string, limit, offset int) ([]model.Novel, error)
	SearchNovels(ctx context.Context, search string, limit, offset int) ([]model.Novel, error)
	GetNovelDetail(ctx context.Context, id int64) (*model.Novel, error)
	GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error)
	GetChapterContent(ctx context.Context, userID int64, novelID int64, chapterIndex int) (*ChapterReadResult, error)
}

type service struct {
	repo  Repository
	cache Cache
}

func NewService(repo Repository, cache Cache) Service {
	return &service{
		repo:  repo,
		cache: cache,
	}
}

func (s *service) GetNovels(ctx context.Context, genre string, limit, offset int) ([]model.Novel, error) {
	return s.repo.GetNovels(ctx, genre, limit, offset)
}

func (s *service) SearchNovels(ctx context.Context, search string, limit, offset int) ([]model.Novel, error) {
	return s.repo.SearchNovels(ctx, search, limit, offset)
}

func (s *service) GetNovelDetail(ctx context.Context, id int64) (*model.Novel, error) {
	n, err := s.cache.GetNovel(ctx, id)
	if err == nil && n != nil {
		return n, nil
	}

	n, err = s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, nil
	}

	_ = s.cache.SetNovel(ctx, id, n)
	return n, nil
}

func (s *service) GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error) {
	list, err := s.cache.GetChaptersList(ctx, novelID)
	if err == nil && len(list) > 0 {
		return list, nil
	}

	list, err = s.repo.GetChaptersList(ctx, novelID)
	if err != nil {
		return nil, err
	}

	if len(list) > 0 {
		_ = s.cache.SetChaptersList(ctx, novelID, list)
	}
	return list, nil
}

func (s *service) GetChapterContent(ctx context.Context, userID int64, novelID int64, chapterIndex int) (*ChapterReadResult, error) {
	ch, err := s.cache.GetChapter(ctx, novelID, chapterIndex)
	if err != nil {
		// Log error, continue to DB fallback
	}
	if ch == nil {
		ch, err = s.repo.GetChapter(ctx, novelID, chapterIndex)
		if err != nil {
			return nil, err
		}
		if ch == nil {
			return nil, nil
		}
		_ = s.cache.SetChapter(ctx, novelID, chapterIndex, ch)
	}

	result := &ChapterReadResult{
		Price: ch.Price,
	}

	if !ch.IsPaid {
		result.Chapter = ch
		result.Locked = false
		return result, nil
	}

	if userID == 0 {
		result.Chapter = getLockedPreview(ch)
		result.Locked = true
		return result, nil
	}

	unlocked, err := s.cache.IsChapterUnlocked(ctx, userID, novelID, chapterIndex)
	if err == nil && unlocked {
		result.Chapter = ch
		result.Locked = false
		return result, nil
	}

	dbUnlocked, err := s.repo.CheckChapterUnlocked(ctx, userID, novelID, chapterIndex)
	if err != nil {
		return nil, err
	}

	if dbUnlocked {
		_ = s.cache.SetChapterUnlocked(ctx, userID, novelID, chapterIndex)
		result.Chapter = ch
		result.Locked = false
	} else {
		result.Chapter = getLockedPreview(ch)
		result.Locked = true
	}

	return result, nil
}

func getLockedPreview(original *model.Chapter) *model.Chapter {
	preview := *original // shallow copy

	paragraphs := strings.Split(original.Content, "\n\n")
	if len(paragraphs) > 2 {
		preview.Content = strings.Join(paragraphs[:2], "\n\n")
	} else {
		preview.Content = original.Content
	}

	return &preview
}

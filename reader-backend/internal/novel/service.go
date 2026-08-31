package novel

import (
	"context"
	"fmt"
	"math"
	"strings"

	"golang.org/x/sync/singleflight"
	"reader-backend/internal/model"
	"reader-backend/internal/storage"
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
	GetChaptersList(ctx context.Context, userID int64, novelID int64, promoID int, utmSource, utmCampaign string) ([]model.Chapter, error)
	GetChapterContent(ctx context.Context, userID int64, novelID int64, chapterIndex int, promoID int, utmSource, utmCampaign string) (*ChapterReadResult, error)
}

type service struct {
	repo    Repository
	cache   Cache
	storage storage.ContentStorage
	sf      singleflight.Group
}

func NewService(repo Repository, cache Cache, storage storage.ContentStorage) Service {
	return &service{
		repo:    repo,
		cache:   cache,
		storage: storage,
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

	sfKey := fmt.Sprintf("sf:novel:detail:%d", id)
	val, err, _ := s.sf.Do(sfKey, func() (interface{}, error) {
		if cached, _ := s.cache.GetNovel(ctx, id); cached != nil {
			return cached, nil
		}
		nDB, errDB := s.repo.GetByID(ctx, id)
		if errDB != nil || nDB == nil {
			return nDB, errDB
		}
		_ = s.cache.SetNovel(ctx, id, nDB)
		return nDB, nil
	})

	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	return val.(*model.Novel), nil
}

func (s *service) GetChaptersList(ctx context.Context, userID int64, novelID int64, promoID int, utmSource, utmCampaign string) ([]model.Chapter, error) {
	list, err := s.cache.GetChaptersList(ctx, novelID)
	if err != nil || len(list) == 0 {
		sfKey := fmt.Sprintf("sf:novel:chapters:%d", novelID)
		val, errSF, _ := s.sf.Do(sfKey, func() (interface{}, error) {
			if cached, _ := s.cache.GetChaptersList(ctx, novelID); len(cached) > 0 {
				return cached, nil
			}
			listDB, errDB := s.repo.GetChaptersList(ctx, novelID)
			if errDB != nil {
				return nil, errDB
			}
			if len(listDB) > 0 {
				_ = s.cache.SetChaptersList(ctx, novelID, listDB)
			}
			return listDB, nil
		})

		if errSF != nil {
			return nil, errSF
		}
		if val != nil {
			list = val.([]model.Chapter)
		}
	}

	effectiveStartPay, effectiveCost, _ := s.repo.GetEffectivePricingRule(ctx, userID, novelID, promoID, utmSource, utmCampaign)

	resultList := make([]model.Chapter, len(list))
	for i, ch := range list {
		isPaid := (ch.ChapterIndex >= (effectiveStartPay - 1))
		price := 0
		if isPaid {
			price = int(math.Round((float64(ch.WordCount) / 1000.0) * float64(effectiveCost)))
		}
		ch.IsPaid = isPaid
		ch.Price = price
		resultList[i] = ch
	}

	return resultList, nil
}

func (s *service) GetChapterContent(ctx context.Context, userID int64, novelID int64, chapterIndex int, promoID int, utmSource, utmCampaign string) (*ChapterReadResult, error) {
	ch, err := s.cache.GetChapter(ctx, novelID, chapterIndex)
	if err != nil || ch == nil {
		sfKey := fmt.Sprintf("sf:chapter:%d:%d", novelID, chapterIndex)
		val, errSF, _ := s.sf.Do(sfKey, func() (interface{}, error) {
			if cached, _ := s.cache.GetChapter(ctx, novelID, chapterIndex); cached != nil {
				return cached, nil
			}
			chDB, errDB := s.repo.GetChapter(ctx, novelID, chapterIndex)
			if errDB != nil || chDB == nil {
				return chDB, errDB
			}

			// If storage is provided and (chDB.Content is empty or storage is OSS), load content from storage
			if s.storage != nil && (chDB.Content == "" || s.storage.StorageType() == "oss") {
				content, errStorage := s.storage.GetContent(ctx, novelID, chapterIndex)
				if errStorage == nil && content != "" {
					chDB.Content = content
				}
			}

			_ = s.cache.SetChapter(ctx, novelID, chapterIndex, chDB)
			return chDB, nil
		})
		if errSF != nil {
			return nil, errSF
		}
		if val == nil {
			return nil, nil
		}
		ch = val.(*model.Chapter)
	}

	effectiveStartPay, effectiveCost, _ := s.repo.GetEffectivePricingRule(ctx, userID, novelID, promoID, utmSource, utmCampaign)

	isPaid := (ch.ChapterIndex >= (effectiveStartPay - 1))
	price := 0
	if isPaid {
		price = int(math.Round((float64(ch.WordCount) / 1000.0) * float64(effectiveCost)))
	}

	result := &ChapterReadResult{
		Price: price,
	}

	if !isPaid {
		clone := *ch
		clone.IsPaid = false
		clone.Price = 0
		result.Chapter = &clone
		result.Locked = false
		return result, nil
	}

	if userID == 0 {
		preview := getLockedPreview(ch)
		preview.IsPaid = true
		preview.Price = price
		result.Chapter = preview
		result.Locked = true
		return result, nil
	}

	// VIP Subscription Check: Members with active subscription have unlimited free reading access to all content
	if isVIP, errVIP := s.repo.HasActiveSubscription(ctx, userID); errVIP == nil && isVIP {
		clone := *ch
		clone.IsPaid = true
		clone.Price = price
		result.Chapter = &clone
		result.Locked = false
		return result, nil
	}


	unlocked, err := s.cache.IsChapterUnlocked(ctx, userID, novelID, chapterIndex)
	if err == nil && unlocked {
		clone := *ch
		clone.IsPaid = true
		clone.Price = price
		result.Chapter = &clone
		result.Locked = false
		return result, nil
	}

	sfUnlockKey := fmt.Sprintf("sf:unlock:%d:%d", userID, novelID)
	valUnlock, errUnlock, _ := s.sf.Do(sfUnlockKey, func() (interface{}, error) {
		if cachedUnlocked, errCache := s.cache.IsChapterUnlocked(ctx, userID, novelID, chapterIndex); errCache == nil && cachedUnlocked {
			return true, nil
		}
		indices, errDB := s.repo.GetUnlockedChapterIndices(ctx, userID, novelID)
		if errDB != nil {
			return false, errDB
		}
		if len(indices) > 0 {
			_ = s.cache.SetUnlockedChaptersBatch(ctx, userID, novelID, indices)
		}
		for _, idx := range indices {
			if idx == chapterIndex {
				return true, nil
			}
		}
		return false, nil
	})

	if errUnlock != nil {
		return nil, errUnlock
	}

	dbUnlocked := valUnlock.(bool)
	if dbUnlocked {
		clone := *ch
		clone.IsPaid = true
		clone.Price = price
		result.Chapter = &clone
		result.Locked = false
	} else {
		preview := getLockedPreview(ch)
		preview.IsPaid = true
		preview.Price = price
		result.Chapter = preview
		result.Locked = true
	}

	return result, nil
}

func getLockedPreview(original *model.Chapter) *model.Chapter {
	preview := *original // shallow copy

	// Try splitting by double newline first
	paragraphs := strings.Split(original.Content, "\n\n")
	if len(paragraphs) > 2 {
		preview.Content = strings.Join(paragraphs[:2], "\n\n")
		return &preview
	}

	// Fallback to single newline if text uses \n
	lines := strings.Split(original.Content, "\n")
	if len(lines) > 3 {
		preview.Content = strings.Join(lines[:3], "\n")
		return &preview
	}

	// If no line breaks or single long block, truncate to first 250 runes
	runes := []rune(original.Content)
	if len(runes) > 250 {
		preview.Content = string(runes[:250]) + "..."
	} else {
		preview.Content = original.Content
	}

	return &preview
}

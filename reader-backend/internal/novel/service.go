package novel

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"golang.org/x/sync/singleflight"
	"reader-backend/internal/model"
	"reader-backend/internal/storage"
)

var (
	ErrNovelNotFound = errors.New("novel not found")
)

// IsNovelPublished reports whether a novel is active and published for reader access.
func IsNovelPublished(n *model.Novel) bool {
	return n != nil && n.Status != "archived" && n.PublishStatus == "published"
}

// CalculateChapterPrice computes coin price consistently across catalog, reader, and unlock.
func CalculateChapterPrice(wordCount, effectiveCost int) int {
	if wordCount <= 0 || effectiveCost <= 0 {
		return 0
	}
	return int(math.Round((float64(wordCount) / 1000.0) * float64(effectiveCost)))
}

type ChapterReadResult struct {
	Chapter    *model.Chapter `json:"chapter"`
	Locked     bool           `json:"locked"`
	Price      int            `json:"price"`
	AccessType string         `json:"access_type,omitempty"`
}

type Service interface {
	GetNovels(ctx context.Context, genre, sortBy string, limit, offset int) ([]model.Novel, error)
	SearchNovels(ctx context.Context, search string, limit, offset int) ([]model.Novel, error)
	GetNovelDetail(ctx context.Context, id int64) (*model.Novel, error)
	GetChaptersList(ctx context.Context, userID int64, novelID int64, promoID int, utmSource, utmCampaign string) ([]model.Chapter, error)
	GetChapterContent(ctx context.Context, userID int64, novelID int64, chapterIndex int, promoID int, utmSource, utmCampaign string) (*ChapterReadResult, error)
}

type service struct {
	repo        Repository
	cache       Cache
	storage     storage.ContentStorage
	coverSigner storage.CoverSigner
	sf          singleflight.Group
}

func NewService(repo Repository, cache Cache, storage storage.ContentStorage, coverSigner storage.CoverSigner) Service {
	return &service{
		repo:        repo,
		cache:       cache,
		storage:     storage,
		coverSigner: coverSigner,
	}
}

func (s *service) signCoverURL(raw string) string {
	if s.coverSigner == nil {
		return raw
	}
	return s.coverSigner.SignCoverURL(raw)
}

func (s *service) GetNovels(ctx context.Context, genre, sortBy string, limit, offset int) ([]model.Novel, error) {
	novels, err := s.repo.GetNovels(ctx, genre, sortBy, limit, offset)
	if err != nil {
		return nil, err
	}
	if novels == nil {
		novels = []model.Novel{}
	}
	for i := range novels {
		novels[i].CoverURL = s.signCoverURL(novels[i].CoverURL)
	}
	return novels, nil
}

func (s *service) SearchNovels(ctx context.Context, search string, limit, offset int) ([]model.Novel, error) {
	novels, err := s.repo.SearchNovels(ctx, search, limit, offset)
	if err != nil {
		return nil, err
	}
	if novels == nil {
		novels = []model.Novel{}
	}
	for i := range novels {
		novels[i].CoverURL = s.signCoverURL(novels[i].CoverURL)
	}
	return novels, nil
}

func (s *service) GetNovelDetail(ctx context.Context, id int64) (*model.Novel, error) {
	n, err := s.cache.GetNovel(ctx, id)
	if err == nil && n != nil && n.PublishStatus != "" {
		if !IsNovelPublished(n) {
			return nil, nil
		}
		signed := *n
		signed.CoverURL = s.signCoverURL(n.CoverURL)
		return &signed, nil
	}

	sfKey := fmt.Sprintf("sf:novel:detail:%d", id)
	val, err, _ := s.sf.Do(sfKey, func() (interface{}, error) {
		if cached, _ := s.cache.GetNovel(ctx, id); cached != nil && cached.PublishStatus != "" {
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
	cachedNovel := val.(*model.Novel)
	if !IsNovelPublished(cachedNovel) {
		return nil, nil
	}
	signed := *cachedNovel
	signed.CoverURL = s.signCoverURL(cachedNovel.CoverURL)
	return &signed, nil
}

func (s *service) GetChaptersList(ctx context.Context, userID int64, novelID int64, promoID int, utmSource, utmCampaign string) ([]model.Chapter, error) {
	n, err := s.GetNovelDetail(ctx, novelID)
	if err != nil {
		return nil, err
	}
	if !IsNovelPublished(n) {
		return nil, ErrNovelNotFound
	}

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

	effectiveStartPay, effectiveCost, err := s.repo.GetEffectivePricingRule(ctx, userID, novelID, promoID, utmSource, utmCampaign)
	if err != nil {
		return nil, err
	}

	hasPass, err := HasReadingPass(ctx, s.repo, userID, novelID)
	if err != nil {
		return nil, err
	}

	resultList := make([]model.Chapter, len(list))
	for i, ch := range list {
		isPaid := ch.ChapterIndex >= effectiveStartPay-1
		price := 0
		if isPaid && !hasPass {
			price = CalculateChapterPrice(ch.WordCount, effectiveCost)
		}
		ch.IsPaid = isPaid
		ch.Price = price
		resultList[i] = ch
	}

	return resultList, nil
}

func (s *service) GetChapterContent(ctx context.Context, userID int64, novelID int64, chapterIndex int, promoID int, utmSource, utmCampaign string) (*ChapterReadResult, error) {
	n, err := s.GetNovelDetail(ctx, novelID)
	if err != nil {
		return nil, err
	}
	if !IsNovelPublished(n) {
		return nil, nil
	}

	ch, err := s.cache.GetChapter(ctx, novelID, chapterIndex)
	if err != nil || ch == nil {
		sfKey := fmt.Sprintf("sf:chapter:%d:%d", novelID, chapterIndex)
		val, errSF, _ := s.sf.Do(sfKey, func() (interface{}, error) {
			if cached, _ := s.cache.GetChapter(ctx, novelID, chapterIndex); cached != nil {
				return cached, nil
			}
			chDB, errDB := s.repo.GetChapter(ctx, novelID, chapterIndex)
			if errDB != nil || chDB == nil {
				return nil, errDB
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

	effectiveStartPay, effectiveCost, err := s.repo.GetEffectivePricingRule(ctx, userID, novelID, promoID, utmSource, utmCampaign)
	if err != nil {
		return nil, err
	}

	isPaid := (ch.ChapterIndex >= (effectiveStartPay - 1))
	price := 0
	if isPaid {
		price = CalculateChapterPrice(ch.WordCount, effectiveCost)
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
		result.AccessType = "free"
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
	hasPass, err := HasReadingPass(ctx, s.repo, userID, novelID)
	if err != nil {
		return nil, err
	}
	if hasPass {
		clone := *ch
		clone.IsPaid = true
		clone.Price = price
		result.Chapter = &clone
		result.Locked = false
		result.AccessType = "pass"
		result.Price = 0
		return result, nil
	}

	unlocked, err := s.cache.IsChapterUnlocked(ctx, userID, novelID, chapterIndex)
	if err == nil && unlocked {
		clone := *ch
		clone.IsPaid = true
		clone.Price = price
		result.Chapter = &clone
		result.Locked = false
		result.AccessType = "chapter"
		return result, nil
	}

	sfUnlockKey := fmt.Sprintf("sf:unlock:%d:%d", userID, novelID)
	valUnlock, errUnlock, _ := s.sf.Do(sfUnlockKey, func() (interface{}, error) {
		indices, err := s.repo.GetUnlockedChapterIndices(ctx, userID, novelID)
		if err != nil {
			return nil, err
		}
		if len(indices) > 0 {
			_ = s.cache.SetUnlockedChaptersBatch(ctx, userID, novelID, indices)
		}
		return indices, nil
	})

	if errUnlock != nil {
		return nil, errUnlock
	}

	dbUnlocked := slices.Contains(valUnlock.([]int), chapterIndex)
	if dbUnlocked {
		clone := *ch
		clone.IsPaid = true
		clone.Price = price
		result.Chapter = &clone
		result.Locked = false
		result.AccessType = "chapter"
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

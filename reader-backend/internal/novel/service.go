package novel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/singleflight"
	"reader-backend/internal/model"
	"reader-backend/internal/storage"
)

var (
	ErrContentChanged = errors.New("chapter publication changed; reload and retry")
	ErrContentBusy    = errors.New("chapter content temporarily unavailable")
	ErrNovelNotFound  = errors.New("novel not found")
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
	// PostgreSQL numeric ROUND rounds a positive half up. Keep the same result in Go.
	return int((int64(wordCount)*int64(effectiveCost) + 500) / 1000)
}

type ChapterReadResult struct {
	PurchaseNeedsReview bool           `json:"purchase_needs_review,omitempty"`
	ContentVersion      int64          `json:"content_version"`
	Chapter             *model.Chapter `json:"chapter"`
	Locked              bool           `json:"locked"`
	Price               int            `json:"price"`
	AccessType          string         `json:"access_type,omitempty"`
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
	loads       chan struct{}
}

func NewService(repo Repository, cache Cache, storage storage.ContentStorage, coverSigner storage.CoverSigner) Service {
	return &service{
		repo:        repo,
		cache:       cache,
		storage:     storage,
		coverSigner: coverSigner,
		loads:       make(chan struct{}, 12),
	}
}

func (s *service) load(ctx context.Context, key string, fn func(context.Context) (any, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := s.sf.DoChan(key, func() (any, error) {
		select {
		case s.loads <- struct{}{}:
			defer func() { <-s.loads }()
		default:
			return nil, ErrContentBusy
		}
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
		defer cancel()
		return fn(loadCtx)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case item := <-result:
		return item.Val, item.Err
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
	version, err := s.repo.GetContentVersion(ctx, id)
	if err != nil {
		return nil, err
	}
	if version == 0 {
		return nil, nil
	}
	ctx = withContentVersion(ctx, version)
	n, err := s.cache.GetNovel(ctx, id)
	if err == nil && n != nil && n.PublishStatus != "" {
		if !IsNovelPublished(n) {
			return nil, nil
		}
		signed := *n
		signed.CoverURL = s.signCoverURL(n.CoverURL)
		return &signed, nil
	}

	sfKey := fmt.Sprintf("sf:novel:detail:%d:%d", id, contentVersion(ctx))
	val, err := s.load(ctx, sfKey, func(ctx context.Context) (any, error) {
		if cached, _ := s.cache.GetNovel(ctx, id); cached != nil && cached.PublishStatus != "" {
			return cached, nil
		}
		nDB, errDB := s.repo.GetByID(ctx, id)
		if errDB != nil || nDB == nil {
			return nil, errDB
		}
		if nDB.ContentVersion != contentVersion(ctx) {
			return nil, ErrContentChanged
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

	ctx = withContentVersion(ctx, n.ContentVersion)
	list, err := s.cache.GetChaptersList(ctx, novelID)
	if err != nil || len(list) == 0 {
		sfKey := fmt.Sprintf("sf:novel:chapters:%d:%d", novelID, contentVersion(ctx))
		val, errSF := s.load(ctx, sfKey, func(ctx context.Context) (any, error) {
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
		ch.PublicationVersion = n.ContentVersion
		ch.IsPaid = isPaid && effectiveCost > 0
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

	ctx = withContentVersion(ctx, n.ContentVersion)
	ch, err := s.cache.GetChapter(ctx, novelID, chapterIndex)
	if err != nil || ch == nil {
		sfKey := fmt.Sprintf("sf:chapter:%d:%d:%d", novelID, chapterIndex, contentVersion(ctx))
		val, errSF := s.load(ctx, sfKey, func(ctx context.Context) (any, error) {
			if cached, _ := s.cache.GetChapter(ctx, novelID, chapterIndex); cached != nil {
				return cached, nil
			}
			chDB, errDB := s.repo.GetChapter(ctx, novelID, chapterIndex)
			if errDB != nil || chDB == nil {
				return nil, errDB
			}

			switch {
			case chDB.ObjectKey != "":
				store, ok := s.storage.(interface {
					ReadChapterRevision(context.Context, string, string) (string, error)
				})
				if !ok {
					return nil, errors.New("chapter storage does not support immutable objects")
				}
				content, err := store.ReadChapterRevision(ctx, chDB.ObjectKey, chDB.ContentHash)
				if err != nil {
					return nil, err
				}
				chDB.Content = content
			case chDB.Content == "" && s.storage != nil && s.storage.StorageType() == "oss":
				store, ok := s.storage.(interface {
					ReadLegacyChapter(context.Context, int64, int) (string, error)
				})
				if !ok {
					return nil, errors.New("chapter storage does not support legacy reads")
				}
				content, err := store.ReadLegacyChapter(ctx, novelID, chapterIndex)
				if err != nil {
					return nil, err
				}
				chDB.Content = content
			}
			if chDB.Content == "" {
				return nil, storage.ErrContentNotFound
			}

			_ = s.cache.SetChapter(ctx, novelID, chapterIndex, chDB)
			return chDB, nil
		})
		if errSF != nil {
			return nil, errSF
		}
		if val == nil {
			current, err := s.repo.GetContentVersion(ctx, novelID)
			if err == nil && current != 0 && current != n.ContentVersion {
				return nil, ErrContentChanged
			}
			return nil, err
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
		Price: price, ContentVersion: n.ContentVersion,
	}

	if !isPaid || effectiveCost == 0 {
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

	unlocked, err := s.cache.IsChapterUnlocked(ctx, userID, novelID, ch.ID)
	if err != nil || !unlocked {
		unlocked, result.PurchaseNeedsReview, err = s.repo.CheckChapterAccess(ctx, userID, novelID, ch.ID)
		if err != nil {
			return nil, err
		}
		if unlocked {
			_ = s.cache.SetChapterUnlocked(ctx, userID, novelID, ch.ID)
		}
	}
	if unlocked {
		clone := *ch
		clone.IsPaid = true
		clone.Price = price
		result.Chapter = &clone
		result.Locked = false
		result.AccessType = "chapter"
		result.PurchaseNeedsReview = false
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
	runes := []rune(original.Content)
	limit := min(250, len(runes)/2)
	if len(runes) > limit {
		preview.Content = string(runes[:limit]) + "..."
	}
	return &preview
}

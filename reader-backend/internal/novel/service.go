package novel

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	requests    chan struct{}
	misses      [2048]absentChapter
	missesMu    sync.Mutex
}

type ReadLimits struct {
	Requests int
	Loads    int
}

func NewService(repo Repository, cache Cache, storage storage.ContentStorage, coverSigner storage.CoverSigner, limits ...ReadLimits) Service {
	requests, loads := 48, 12
	if len(limits) > 0 {
		requests, loads = limits[0].Requests, limits[0].Loads
	}
	return &service{
		repo:        repo,
		cache:       cache,
		storage:     storage,
		coverSigner: coverSigner,
		loads:       make(chan struct{}, loads),
		requests:    make(chan struct{}, requests),
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

const wholeNovel = -1

type absentChapter struct {
	novelID int64
	index   int
	until   time.Time
}

func (s *service) admit() bool {
	select {
	case s.requests <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *service) knownAbsent(novelID int64, index int) bool {
	s.missesMu.Lock()
	defer s.missesMu.Unlock()
	entry := s.misses[missBucket(novelID, index)]
	return entry.novelID == novelID && entry.index == index && time.Now().Before(entry.until)
}

func missBucket(novelID int64, index int) uint64 {
	return (uint64(novelID)*31 + uint64(index)) % 2048
}

func (s *service) rememberAbsent(novelID int64, index int) {
	s.missesMu.Lock()
	s.misses[missBucket(novelID, index)] = absentChapter{novelID, index, time.Now().Add(2 * time.Second)}
	s.missesMu.Unlock()
}

func (s *service) GetNovelDetail(ctx context.Context, id int64) (*model.Novel, error) {
	if s.knownAbsent(id, wholeNovel) {
		return nil, nil
	}
	if !s.admit() {
		return nil, ErrContentBusy
	}
	defer func() { <-s.requests }()
	n, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		s.rememberAbsent(id, wholeNovel)
		return nil, nil
	}
	n.CoverURL = s.signCoverURL(n.CoverURL)
	return n, nil
}

func (s *service) GetChaptersList(ctx context.Context, userID, novelID int64, promoID int, _, _ string) ([]model.Chapter, error) {
	if s.knownAbsent(novelID, wholeNovel) {
		return nil, ErrNovelNotFound
	}
	if !s.admit() {
		return nil, ErrContentBusy
	}
	defer func() { <-s.requests }()
	snapshot, err := s.repo.ReadSnapshot(ctx, userID, novelID, promoID, nil)
	if err != nil {
		return nil, err
	}
	if err := verifyReadIdentity(ctx, snapshot.userID); err != nil {
		return nil, err
	}
	if snapshot.version == 0 {
		s.rememberAbsent(novelID, wholeNovel)
		return nil, ErrNovelNotFound
	}
	ctx = withContentVersion(ctx, snapshot.version)
	list, _ := s.cache.GetChaptersList(ctx, novelID)
	if list == nil {
		key := fmt.Sprintf("catalog:%d:%d", novelID, snapshot.version)
		value, err := s.load(ctx, key, func(ctx context.Context) (any, error) {
			list, _ := s.cache.GetChaptersList(ctx, novelID)
			if list != nil {
				return list, nil
			}
			list, err := s.repo.GetChaptersList(ctx, novelID)
			if err != nil {
				return nil, err
			}
			if len(list) == 0 {
				current, err := s.repo.GetContentVersion(ctx, novelID)
				if err != nil {
					return nil, err
				}
				if current != snapshot.version {
					return nil, ErrContentChanged
				}
			}
			_ = s.cache.SetChaptersList(ctx, novelID, list)
			return list, nil
		})
		if err != nil {
			return nil, err
		}
		list = value.([]model.Chapter)
	}
	result := make([]model.Chapter, len(list))
	for i, ch := range list {
		ch.PublicationVersion = snapshot.version
		ch.IsPaid = ch.ChapterIndex >= snapshot.start-1 && snapshot.cost > 0
		if ch.IsPaid && !snapshot.pass {
			ch.Price = CalculateChapterPrice(ch.WordCount, snapshot.cost)
		}
		result[i] = ch
	}
	return result, nil
}

func (s *service) GetChapterContent(ctx context.Context, userID, novelID int64, index, promoID int, _, _ string) (*ChapterReadResult, error) {
	if s.knownAbsent(novelID, wholeNovel) || s.knownAbsent(novelID, index) {
		return nil, nil
	}
	if !s.admit() {
		return nil, ErrContentBusy
	}
	defer func() { <-s.requests }()
	snapshot, err := s.repo.ReadSnapshot(ctx, userID, novelID, promoID, &index)
	if err != nil {
		return nil, err
	}
	if err := verifyReadIdentity(ctx, snapshot.userID); err != nil {
		return nil, err
	}
	if snapshot.version == 0 {
		s.rememberAbsent(novelID, wholeNovel)
		return nil, nil
	}
	if snapshot.chapter == nil {
		s.rememberAbsent(novelID, index)
		return nil, nil
	}
	ch := *snapshot.chapter
	ch.IsPaid = index >= snapshot.start-1 && snapshot.cost > 0
	if ch.IsPaid {
		ch.Price = CalculateChapterPrice(ch.WordCount, snapshot.cost)
	}
	result := &ChapterReadResult{Chapter: &ch, Price: ch.Price, ContentVersion: snapshot.version}
	switch {
	case !ch.IsPaid:
		result.AccessType = "free"
	case snapshot.pass:
		result.AccessType, result.Price = "pass", 0
	case snapshot.unlocked:
		result.AccessType = "chapter"
	default:
		ch.Content = snapshot.preview
		result.Locked, result.PurchaseNeedsReview = true, snapshot.review
		return result, nil
	}
	ch.Content, err = s.readBody(withContentVersion(ctx, snapshot.version), &ch)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func bodyCacheKey(ch *model.Chapter) string {
	if ch.ContentHash != "" {
		return "sha256:" + ch.ContentHash
	}
	return fmt.Sprintf("legacy:%d:%s:%d:%d", ch.NovelID, ch.ID, ch.ChapterIndex, ch.BodyRevision)
}

func (s *service) readBody(ctx context.Context, ch *model.Chapter) (string, error) {
	key := bodyCacheKey(ch)
	if body, _ := s.cache.GetBody(ctx, key); body != "" {
		return body, nil
	}
	value, err := s.load(ctx, "body:"+key, func(ctx context.Context) (any, error) {
		if body, _ := s.cache.GetBody(ctx, key); body != "" {
			return body, nil
		}
		body, err := s.loadBody(ctx, ch)
		if err != nil {
			return nil, err
		}
		if body == "" {
			return nil, storage.ErrContentNotFound
		}
		_ = s.cache.SetBody(ctx, key, body)
		return body, nil
	})
	if err != nil {
		return "", err
	}
	return value.(string), nil
}

func (s *service) loadBody(ctx context.Context, ch *model.Chapter) (string, error) {
	if ch.ObjectKey != "" {
		store, ok := s.storage.(interface {
			ReadChapterRevision(context.Context, string, string) (string, error)
		})
		if !ok {
			return "", errors.New("chapter storage does not support immutable objects")
		}
		return store.ReadChapterRevision(ctx, ch.ObjectKey, ch.ContentHash)
	}
	row, err := s.repo.GetChapter(ctx, ch.NovelID, ch.ChapterIndex)
	if err != nil {
		return "", err
	}
	if row == nil || row.ID != ch.ID || row.ContentHash != ch.ContentHash || row.BodyRevision != ch.BodyRevision {
		return "", ErrContentChanged
	}
	if row.Content != "" || s.storage == nil || s.storage.StorageType() != "oss" {
		return row.Content, nil
	}
	store, ok := s.storage.(interface {
		ReadLegacyChapter(context.Context, int64, int) (string, error)
	})
	if !ok {
		return "", errors.New("chapter storage does not support legacy reads")
	}
	return store.ReadLegacyChapter(ctx, ch.NovelID, ch.ChapterIndex)
}

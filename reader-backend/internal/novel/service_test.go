package novel

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reader-backend/internal/model"
	"reader-backend/internal/storage"
)

type fakeReaderRepo struct {
	novels       []model.Novel
	chapters     map[int64][]model.Chapter
	lastSortBy   string
	effectiveCost int
	effectiveStart int
}

func (r *fakeReaderRepo) GetNovels(_ context.Context, _ string, sortBy string, _, _ int) ([]model.Novel, error) {
	r.lastSortBy = sortBy
	var out []model.Novel
	for _, n := range r.novels {
		if IsNovelPublished(&n) {
			out = append(out, n)
		}
	}
	return out, nil
}
func (r *fakeReaderRepo) SearchNovels(_ context.Context, _ string, _, _ int) ([]model.Novel, error) {
	var out []model.Novel
	for _, n := range r.novels {
		if IsNovelPublished(&n) {
			out = append(out, n)
		}
	}
	return out, nil
}
func (r *fakeReaderRepo) GetByID(_ context.Context, id int64) (*model.Novel, error) {
	for _, n := range r.novels {
		if n.ID == id && IsNovelPublished(&n) {
			c := n
			return &c, nil
		}
	}
	return nil, nil
}
func (r *fakeReaderRepo) GetChaptersList(_ context.Context, novelID int64) ([]model.Chapter, error) {
	return r.chapters[novelID], nil
}
func (r *fakeReaderRepo) GetChapter(_ context.Context, novelID int64, chapterIndex int) (*model.Chapter, error) {
	for _, ch := range r.chapters[novelID] {
		if ch.ChapterIndex == chapterIndex {
			c := ch
			return &c, nil
		}
	}
	return nil, nil
}
func (r *fakeReaderRepo) GetUnlockedChapterIndices(context.Context, int64, int64) ([]int, error) {
	return nil, nil
}
func (r *fakeReaderRepo) HasActiveSubscription(context.Context, int64) (bool, error) {
	return false, nil
}
func (r *fakeReaderRepo) GetEffectivePricingRule(context.Context, int64, int64, int, string, string) (int, int, error) {
	start := r.effectiveStart
	if start == 0 {
		start = 3
	}
	cost := r.effectiveCost
	if cost == 0 {
		cost = 500
	}
	return start, cost, nil
}

type fakeReaderCache struct {
	cachedNovel *model.Novel
}

func (c *fakeReaderCache) GetNovel(context.Context, int64) (*model.Novel, error) {
	return c.cachedNovel, nil
}
func (c *fakeReaderCache) SetNovel(_ context.Context, _ int64, n *model.Novel) error {
	c.cachedNovel = n
	return nil
}
func (c *fakeReaderCache) GetChaptersList(context.Context, int64) ([]model.Chapter, error) {
	return nil, nil
}
func (c *fakeReaderCache) SetChaptersList(context.Context, int64, []model.Chapter) error {
	return nil
}
func (c *fakeReaderCache) GetChapter(context.Context, int64, int) (*model.Chapter, error) {
	return nil, nil
}
func (c *fakeReaderCache) SetChapter(context.Context, int64, int, *model.Chapter) error {
	return nil
}
func (c *fakeReaderCache) IsChapterUnlocked(context.Context, int64, int64, int) (bool, error) {
	return false, nil
}
func (c *fakeReaderCache) SetChapterUnlocked(context.Context, int64, int64, int) error {
	return nil
}
func (c *fakeReaderCache) SetUnlockedChaptersBatch(context.Context, int64, int64, []int) error {
	return nil
}

type fakeCoverSigner struct{}

func (fakeCoverSigner) IsEnabled() bool { return true }
func (fakeCoverSigner) SignCoverURL(raw string) string {
	if strings.HasPrefix(raw, "oss://") {
		return "https://oss.starsfic.com/" + strings.TrimPrefix(raw, "oss://") + "?signed=1"
	}
	return raw
}

var _ storage.CoverSigner = fakeCoverSigner{}

func TestReaderNovelService_SignsWithoutMutatingCache(t *testing.T) {
	repo := &fakeReaderRepo{
		novels: []model.Novel{
			{ID: 1, Title: "OSS Book", CoverURL: "oss://covers/2026/11/abc.webp", Status: "Ongoing", PublishStatus: "published"},
			{ID: 2, Title: "Legacy Book", CoverURL: "/assets/legacy.png", Status: "Completed", PublishStatus: "published"},
		},
	}
	cache := &fakeReaderCache{}
	svc := NewService(repo, cache, nil, fakeCoverSigner{})
	ctx := context.Background()

	list, err := svc.GetNovels(ctx, "", "latest", 10, 0)
	if err != nil {
		t.Fatalf("GetNovels failed: %v", err)
	}
	if repo.lastSortBy != "latest" {
		t.Fatalf("expected sortBy=latest forwarded, got %q", repo.lastSortBy)
	}
	if list[0].CoverURL != "https://oss.starsfic.com/covers/2026/11/abc.webp?signed=1" {
		t.Fatalf("expected signed cover in list, got %q", list[0].CoverURL)
	}
	if list[1].CoverURL != "/assets/legacy.png" {
		t.Fatalf("expected legacy cover untouched, got %q", list[1].CoverURL)
	}

	detail1, err := svc.GetNovelDetail(ctx, 1)
	if err != nil {
		t.Fatalf("GetNovelDetail failed: %v", err)
	}
	if detail1.CoverURL != "https://oss.starsfic.com/covers/2026/11/abc.webp?signed=1" {
		t.Fatalf("expected signed detail cover, got %q", detail1.CoverURL)
	}
	if cache.cachedNovel == nil || cache.cachedNovel.CoverURL != "oss://covers/2026/11/abc.webp" {
		t.Fatalf("cache must retain raw oss:// ref, got %+v", cache.cachedNovel)
	}

	detail2, err := svc.GetNovelDetail(ctx, 1)
	if err != nil || detail2.CoverURL != "https://oss.starsfic.com/covers/2026/11/abc.webp?signed=1" {
		t.Fatalf("expected cache-hit detail signed cleanly, got %+v err=%v", detail2, err)
	}
}

func TestReaderNovelService_HidesUnpublishedOrArchivedAndPricesConsistently(t *testing.T) {
	repo := &fakeReaderRepo{
		novels: []model.Novel{
			{ID: 1, Title: "Draft Book", Status: "Ongoing", PublishStatus: "draft"},
			{ID: 2, Title: "Archived Book", Status: "archived", PublishStatus: "published"},
			{ID: 3, Title: "Live Book", Status: "Ongoing", PublishStatus: "published"},
		},
		chapters: map[int64][]model.Chapter{
			1: {{ID: "d1", NovelID: 1, ChapterIndex: 0, Title: "Ch 1", Content: "Hi", WordCount: 100}},
			2: {{ID: "a1", NovelID: 2, ChapterIndex: 0, Title: "Ch 1", Content: "Hi", WordCount: 100}},
			3: {{ID: "l1", NovelID: 3, ChapterIndex: 2, Title: "Ch 3", Content: "Short", WordCount: 1, Price: 99}},
		},
	}
	cache := &fakeReaderCache{}
	svc := NewService(repo, cache, nil, fakeCoverSigner{})
	ctx := context.Background()

	for _, hiddenID := range []int64{1, 2} {
		if n, err := svc.GetNovelDetail(ctx, hiddenID); err != nil || n != nil {
			t.Fatalf("expected hidden novel %d to return nil, got %+v err=%v", hiddenID, n, err)
		}
		if _, err := svc.GetChaptersList(ctx, 0, hiddenID, 0, "", ""); !errors.Is(err, ErrNovelNotFound) {
			t.Fatalf("expected ErrNovelNotFound on chapters list for hidden novel %d, got %v", hiddenID, err)
		}
		if res, err := svc.GetChapterContent(ctx, 0, hiddenID, 0, 0, "", ""); err != nil || res != nil {
			t.Fatalf("expected nil chapter content for hidden novel %d, got %+v err=%v", hiddenID, res, err)
		}
	}

	// Verify CalculateChapterPrice matches GetChaptersList & GetChapterContent
	list, err := svc.GetChaptersList(ctx, 0, 3, 0, "", "")
	if err != nil || len(list) != 1 || list[0].Price != 1 {
		// round(1/1000 * 500) = round(0.5) = 1
		t.Fatalf("unexpected chapters list price: %+v err=%v", list, err)
	}
	contentRes, err := svc.GetChapterContent(ctx, 0, 3, 2, 0, "", "")
	if err != nil || contentRes == nil || contentRes.Price != list[0].Price {
		t.Fatalf("expected chapter price=%d, got %+v err=%v", list[0].Price, contentRes, err)
	}
}

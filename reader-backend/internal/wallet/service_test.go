package wallet

import (
	"context"
	"errors"
	"testing"

	"reader-backend/internal/model"
	"reader-backend/internal/novel"
)

type fakeWalletRepo struct {
	Repository
	chargedNovelID int64
	chargedIdx     int
	chargedPrice   int
}

func (r *fakeWalletRepo) UnlockChapterTx(_ context.Context, _ int64, novelID int64, chapterIndex int, price int, _ string) error {
	r.chargedNovelID = novelID
	r.chargedIdx = chapterIndex
	r.chargedPrice = price
	return nil
}

type fakeWalletNovelRepo struct {
	novels        map[int64]*model.Novel
	chapters      map[int64]map[int]*model.Chapter
	effectiveCost int
	startPay      int
}

func (r *fakeWalletNovelRepo) GetNovels(context.Context, string, string, int, int) ([]model.Novel, error) {
	return nil, nil
}
func (r *fakeWalletNovelRepo) SearchNovels(context.Context, string, int, int) ([]model.Novel, error) {
	return nil, nil
}
func (r *fakeWalletNovelRepo) GetByID(_ context.Context, id int64) (*model.Novel, error) {
	n := r.novels[id]
	if !novel.IsNovelPublished(n) {
		return nil, nil
	}
	copyN := *n
	return &copyN, nil
}
func (r *fakeWalletNovelRepo) GetChaptersList(context.Context, int64) ([]model.Chapter, error) {
	return nil, nil
}
func (r *fakeWalletNovelRepo) GetChapter(_ context.Context, novelID int64, chapterIndex int) (*model.Chapter, error) {
	ch := r.chapters[novelID][chapterIndex]
	if ch == nil {
		return nil, nil
	}
	copyCh := *ch
	return &copyCh, nil
}
func (r *fakeWalletNovelRepo) GetUnlockedChapterIndices(context.Context, int64, int64) ([]int, error) {
	return nil, nil
}
func (r *fakeWalletNovelRepo) HasActiveSubscription(context.Context, int64) (bool, error) {
	return false, nil
}
func (r *fakeWalletNovelRepo) GetEffectivePricingRule(context.Context, int64, int64, int, string, string) (int, int, error) {
	return r.startPay, r.effectiveCost, nil
}

type fakeWalletNovelCache struct{ novel.Cache }

func (fakeWalletNovelCache) SetChapterUnlocked(context.Context, int64, int64, int) error { return nil }

func TestUnlockChapter_EnforcesPublishedAndUnifiedPrice(t *testing.T) {
	ctx := context.Background()
	nRepo := &fakeWalletNovelRepo{
		novels: map[int64]*model.Novel{
			1: {ID: 1, Status: "Ongoing", PublishStatus: "draft"},
			2: {ID: 2, Status: "archived", PublishStatus: "published"},
			3: {ID: 3, Status: "Ongoing", PublishStatus: "published"},
		},
		chapters: map[int64]map[int]*model.Chapter{
			1: {2: {ID: "c1", NovelID: 1, ChapterIndex: 2, Title: "Ch 3", WordCount: 1000, Price: 500}},
			2: {2: {ID: "c2", NovelID: 2, ChapterIndex: 2, Title: "Ch 3", WordCount: 1000, Price: 500}},
			3: {2: {ID: "c3", NovelID: 3, ChapterIndex: 2, Title: "Ch 3", WordCount: 5, Price: 99}},
		},
		startPay:      3,
		effectiveCost: 10, // round(5/1000 * 10) = 0; must not fallback to stale ch.Price=99
	}
	wRepo := &fakeWalletRepo{}
	svc := NewService(wRepo, nRepo, fakeWalletNovelCache{}, nil, nil)

	for _, hiddenID := range []int64{1, 2} {
		err := svc.UnlockChapter(ctx, 42, hiddenID, 2, 0, "", "")
		if !errors.Is(err, novel.ErrNovelNotFound) {
			t.Fatalf("expected ErrNovelNotFound for hidden novel %d, got %v", hiddenID, err)
		}
	}

	if err := svc.UnlockChapter(ctx, 42, 3, 2, 0, "", ""); err != nil {
		t.Fatalf("UnlockChapter failed: %v", err)
	}
	if wRepo.chargedNovelID != 3 || wRepo.chargedIdx != 2 || wRepo.chargedPrice != 0 {
		t.Fatalf("expected unified calculated price=0 (not stale 99), got novel=%d idx=%d price=%d", wRepo.chargedNovelID, wRepo.chargedIdx, wRepo.chargedPrice)
	}
}

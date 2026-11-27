package novel

import (
	"context"
	"strings"
	"testing"

	"star-novel-cms/internal/storage"
)

type fakeBackfillRevisionStore struct {
	storage.ContentStorage
	revisions map[string]string
	legacy    map[int]string
}

func (f *fakeBackfillRevisionStore) GetContent(_ context.Context, _ int64, chapterIndex int) (string, error) {
	return f.legacy[chapterIndex], nil
}
func (f *fakeBackfillRevisionStore) StorageType() string { return "oss" }
func (f *fakeBackfillRevisionStore) ReadChapterRevision(_ context.Context, key, _ string) (string, error) {
	return f.revisions[key], nil
}
func (f *fakeBackfillRevisionStore) ReadLegacyChapter(_ context.Context, _ int64, chapterIndex int) (string, error) {
	return f.legacy[chapterIndex], nil
}
func (f *fakeBackfillRevisionStore) WriteChapterRevision(context.Context, string, string, string) error {
	return nil
}

func TestComputeChapterWordCountBackfill_InlineAndOSS(t *testing.T) {
	// 200 words ("hello" repeated 200 times): 1000 chars previously -> 500 coins,
	// now 200 words -> 100 coins at 500 coins per 1000 words.
	body := strings.TrimSpace(strings.Repeat("hello ", 200))

	inline := computeChapterWordCountBackfill(context.Background(), nil, backfillChapterRow{
		ID:           "ch-inline",
		NovelID:      1,
		ChapterIndex: 2,
		WordCount:    1000,
		IsPaid:       true,
		Price:        500,
		Content:      body,
	}, 3, 500)
	if inline.err != nil {
		t.Fatalf("inline computation failed: %v", inline.err)
	}
	if !inline.changed || inline.newWordCount != 200 || !inline.newIsPaid || inline.newPrice != 100 {
		t.Fatalf("unexpected inline backfill result: %+v", inline)
	}

	// Already backfilled chapter should be marked unchanged (idempotent).
	idempotent := computeChapterWordCountBackfill(context.Background(), nil, backfillChapterRow{
		ID:           "ch-inline",
		NovelID:      1,
		ChapterIndex: 2,
		WordCount:    200,
		IsPaid:       true,
		Price:        100,
		Content:      body,
	}, 3, 500)
	if idempotent.err != nil || idempotent.changed {
		t.Fatalf("expected unchanged idempotent result, got %+v", idempotent)
	}

	// OSS revision + free chapter before startPay.
	key := "revisions/ch-oss/hash.txt"
	hash := "hash"
	store := &fakeBackfillRevisionStore{
		revisions: map[string]string{key: "Daisy's inheritance—the empire!"},
		legacy:    map[int]string{5: "Legacy chapter body with six words"},
	}
	ossRev := computeChapterWordCountBackfill(context.Background(), store, backfillChapterRow{
		ID:           "ch-oss",
		NovelID:      1,
		ChapterIndex: 0,
		WordCount:    28,
		IsPaid:       false,
		Price:        0,
		ObjectKey:    &key,
		ContentHash:  &hash,
	}, 3, 500)
	if ossRev.err != nil || !ossRev.changed || ossRev.newWordCount != 4 || ossRev.newIsPaid || ossRev.newPrice != 0 {
		t.Fatalf("unexpected OSS revision backfill result: %+v", ossRev)
	}

	// Legacy OSS chapter without ObjectKey.
	ossLegacy := computeChapterWordCountBackfill(context.Background(), store, backfillChapterRow{
		ID:           "ch-legacy",
		NovelID:      1,
		ChapterIndex: 5,
		WordCount:    29,
		IsPaid:       true,
		Price:        15,
	}, 3, 500)
	if ossLegacy.err != nil || !ossLegacy.changed || ossLegacy.newWordCount != 6 || !ossLegacy.newIsPaid || ossLegacy.newPrice != 3 {
		t.Fatalf("unexpected legacy OSS backfill result: %+v", ossLegacy)
	}
}

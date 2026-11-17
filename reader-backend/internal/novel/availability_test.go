package novel

import (
	"context"
	"errors"
	"reader-backend/internal/model"
	"reader-backend/internal/storage"
	"reader-backend/internal/testdb"
	"strings"
	"testing"
)

type unavailableStorage struct{ storage.ContentStorage }

func (unavailableStorage) StorageType() string { return "oss" }
func (unavailableStorage) GetContent(context.Context, int64, int) (string, error) {
	return "", errors.New("unavailable")
}
func TestBoundedPreviewAndStorageFailure(t *testing.T) {
	for _, text := range []string{strings.Repeat("字", 10000) + "\n\nlong\n\nend", "short"} {
		preview := getLockedPreview(&model.Chapter{Content: text})
		if len([]rune(preview.Content)) > 253 || preview.Content == text {
			t.Fatal("unbounded preview")
		}
	}
	repo := &fakeReaderRepo{novels: []model.Novel{{ID: 1, Status: "Ongoing", ContentVersion: 1, PublishStatus: "published"}}, chapters: map[int64][]model.Chapter{1: {{NovelID: 1, ChapterIndex: 0}}}}
	svc := NewService(repo, &fakeReaderCache{}, unavailableStorage{}, nil)
	if _, err := svc.GetChapterContent(context.Background(), 0, 1, 0, 0, "", ""); err == nil {
		t.Fatal("storage failure suppressed")
	}
}
func TestStablePromotionAndZeroPrice(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	_, err := p.Exec(ctx, `INSERT INTO novels(id,title,author) VALUES(1,'Book','Author');INSERT INTO promotion_links(id,novel_id,novel_title,utm_source,utm_campaign,generated_url,coin_cost_per_thousand) VALUES(1,1,'Book','s','c','https://example.invalid',0),(2,1,'Book','s','c','https://example.invalid',900);INSERT INTO users(id,promotion_link_id,utm_source,utm_campaign) VALUES(1,1,'s','c')`)
	if err != nil {
		t.Fatal(err)
	}
	_, cost, err := NewDBRepository().GetEffectivePricingRule(ctx, 1, 1, 2, "s", "c")
	if err != nil || cost != 0 {
		t.Fatalf("stable promotion zero=%d %v", cost, err)
	}
	var id *int
	if err := p.QueryRow(ctx, `SELECT resolve_reader_promotion(0,'s','c')`).Scan(&id); err != nil || id != nil {
		t.Fatal("ambiguous legacy promotion selected", err)
	}
}

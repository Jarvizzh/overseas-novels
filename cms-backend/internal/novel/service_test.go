package novel

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/config"
	"star-novel-cms/internal/storage"
)

type fakeTx struct {
	pgx.Tx
	novelID          int64
	stagedWordCount  *int
	stagedChapters   map[int]*Chapter
	stagedSettings   map[string]string
	repo             *fakeNovelRepo
	committed        bool
	rolledBack       bool
}

func (t *fakeTx) Commit(context.Context) error {
	t.committed = true
	if t.repo != nil && t.novelID != 0 {
		if t.stagedChapters != nil {
			t.repo.chapters[t.novelID] = t.stagedChapters
		}
		if t.stagedWordCount != nil {
			if n, ok := t.repo.novels[t.novelID]; ok {
				n.WordCount = *t.stagedWordCount
			}
		}
	}
	if t.repo != nil && t.stagedSettings != nil {
		for k, v := range t.stagedSettings {
			t.repo.settings[k] = v
		}
	}
	return nil
}

func (t *fakeTx) Rollback(context.Context) error {
	if !t.committed {
		t.rolledBack = true
	}
	return nil
}

type fakeNovelRepo struct {
	novels              map[int64]*Novel
	chapters            map[int64]map[int]*Chapter
	settings            map[string]string
	promoLinks          map[int]*PromotionLink
	lastListSelectQuery string
	batchAllCalled      bool
	batchInheritedCalls int
	batchErr            error
	nextID              int64
	nextPromoID         int
}

func newFakeNovelRepo() *fakeNovelRepo {
	return &fakeNovelRepo{
		novels:      map[int64]*Novel{},
		chapters:    map[int64]map[int]*Chapter{},
		settings:    map[string]string{},
		promoLinks:  map[int]*PromotionLink{},
		nextID:      10000021,
		nextPromoID: 1,
	}
}

func (r *fakeNovelRepo) cloneChapters(novelID int64) map[int]*Chapter {
	out := map[int]*Chapter{}
	for idx, ch := range r.chapters[novelID] {
		copyCh := *ch
		out[idx] = &copyCh
	}
	return out
}

func (r *fakeNovelRepo) BeginTx(context.Context) (pgx.Tx, error) {
	return &fakeTx{repo: r}, nil
}
func (r *fakeNovelRepo) CountNovels(context.Context, string, []interface{}) (int, error) {
	return len(r.novels), nil
}
func (r *fakeNovelRepo) ListNovels(_ context.Context, selectQuery string, _ []interface{}) ([]Novel, error) {
	r.lastListSelectQuery = selectQuery
	out := make([]Novel, 0, len(r.novels))
	for _, n := range r.novels {
		if n.Status == "archived" {
			continue
		}
		out = append(out, *n)
	}
	return out, nil
}
func (r *fakeNovelRepo) GetNovel(_ context.Context, id int64) (*Novel, error) {
	n, ok := r.novels[id]
	if !ok || n.Status == "archived" {
		return nil, nil
	}
	copyN := *n
	return &copyN, nil
}
func (r *fakeNovelRepo) CreateNovel(_ context.Context, n *Novel) error {
	n.ID = r.nextID
	r.nextID++
	if n.Genres == nil {
		n.Genres = []string{}
	}
	copyN := *n
	r.novels[n.ID] = &copyN
	return nil
}
func (r *fakeNovelRepo) UpdateNovel(_ context.Context, n *Novel) error {
	existing, ok := r.novels[n.ID]
	if !ok || existing.Status == "archived" {
		return pgx.ErrNoRows
	}
	copyN := *n
	copyN.WordCount = existing.WordCount
	if copyN.Genres == nil {
		copyN.Genres = []string{}
	}
	r.novels[n.ID] = &copyN
	return nil
}
func (r *fakeNovelRepo) UpdatePublishStatus(_ context.Context, id int64, publishStatus string) (*Novel, error) {
	n, ok := r.novels[id]
	if !ok || n.Status == "archived" {
		return nil, nil
	}
	n.PublishStatus = publishStatus
	copyN := *n
	return &copyN, nil
}
func (r *fakeNovelRepo) DeleteNovel(_ context.Context, id int64) (int64, error) {
	n, ok := r.novels[id]
	if !ok || n.Status == "archived" {
		return 0, nil
	}
	n.Status = "archived"
	n.PublishStatus = "offline"
	return 1, nil
}
func (r *fakeNovelRepo) CountChapters(_ context.Context, novelID int64) (int, error) {
	return len(r.chapters[novelID]), nil
}
func (r *fakeNovelRepo) CountNovelsByCoverURL(_ context.Context, coverURL string, excludeNovelID int64) (int, error) {
	count := 0
	for id, n := range r.novels {
		if id != excludeNovelID && n.Status != "archived" && n.CoverURL == coverURL {
			count++
		}
	}
	return count, nil
}
func (r *fakeNovelRepo) UpdateChapterPrices(context.Context, int, *int, int64) error { return nil }
func (r *fakeNovelRepo) BatchUpdateAllNovelChapterPrices(context.Context, int, int) error {
	r.batchAllCalled = true
	return r.batchErr
}
func (r *fakeNovelRepo) BatchUpdateInheritedNovelChapterPrices(context.Context, int) error {
	r.batchInheritedCalls++
	return r.batchErr
}
func (r *fakeNovelRepo) ListChapters(_ context.Context, novelID int64) ([]Chapter, error) {
	var out []Chapter
	for _, ch := range r.chapters[novelID] {
		out = append(out, *ch)
	}
	return out, nil
}
func (r *fakeNovelRepo) GetChapter(_ context.Context, novelID int64, idx int) (*Chapter, error) {
	if ch, ok := r.chapters[novelID][idx]; ok {
		copyCh := *ch
		return &copyCh, nil
	}
	return nil, nil
}
func (r *fakeNovelRepo) CreateChapter(context.Context, *Chapter) error { return nil }
func (r *fakeNovelRepo) CreateChapterTx(_ context.Context, tx pgx.Tx, c *Chapter) error {
	ftx := tx.(*fakeTx)
	if ftx.novelID == 0 {
		ftx.novelID = c.NovelID
	}
	if ftx.stagedChapters == nil {
		ftx.stagedChapters = r.cloneChapters(c.NovelID)
	}
	copyCh := *c
	ftx.stagedChapters[c.ChapterIndex] = &copyCh
	return nil
}
func (r *fakeNovelRepo) UpdateChapter(context.Context, *Chapter) error { return nil }
func (r *fakeNovelRepo) UpdateChapterTx(_ context.Context, tx pgx.Tx, c *Chapter) (int64, error) {
	ftx := tx.(*fakeTx)
	if ftx.novelID == 0 {
		ftx.novelID = c.NovelID
	}
	if ftx.stagedChapters == nil {
		ftx.stagedChapters = r.cloneChapters(c.NovelID)
	}
	existing, ok := ftx.stagedChapters[c.ChapterIndex]
	if !ok {
		return 0, nil
	}
	existing.Title = c.Title
	existing.Content = c.Content
	existing.WordCount = c.WordCount
	existing.IsPaid = c.IsPaid
	existing.Price = c.Price
	return 1, nil
}
func (r *fakeNovelRepo) SyncNovelWordCountTx(_ context.Context, tx pgx.Tx, novelID int64) error {
	ftx := tx.(*fakeTx)
	ftx.novelID = novelID
	if ftx.stagedChapters == nil {
		ftx.stagedChapters = r.cloneChapters(novelID)
	}
	sum := 0
	for _, ch := range ftx.stagedChapters {
		sum += ch.WordCount
	}
	ftx.stagedWordCount = &sum
	return nil
}
func (r *fakeNovelRepo) DeleteChaptersByNovelIDTx(_ context.Context, tx pgx.Tx, novelID int64) error {
	ftx := tx.(*fakeTx)
	ftx.novelID = novelID
	ftx.stagedChapters = map[int]*Chapter{}
	return nil
}
func (r *fakeNovelRepo) GetSettings(context.Context) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range r.settings {
		out[k] = v
	}
	return out, nil
}
func (r *fakeNovelRepo) UpdateSettingTx(_ context.Context, tx pgx.Tx, key, val string) error {
	ftx := tx.(*fakeTx)
	if ftx.stagedSettings == nil {
		ftx.stagedSettings = map[string]string{}
	}
	ftx.stagedSettings[key] = val
	return nil
}
func (r *fakeNovelRepo) ListPromotionLinks(context.Context) ([]PromotionLink, error)  { return nil, nil }
func (r *fakeNovelRepo) CreatePromotionLink(_ context.Context, link *PromotionLink) (int, error) {
	id := r.nextPromoID
	r.nextPromoID++
	copyL := *link
	copyL.ID = id
	r.promoLinks[id] = &copyL
	return id, nil
}
func (r *fakeNovelRepo) UpdatePromotionLinkURL(_ context.Context, id int, generatedURL string) error {
	if l, ok := r.promoLinks[id]; ok {
		l.GeneratedURL = generatedURL
	}
	return nil
}
func (r *fakeNovelRepo) UpdatePromotionLink(context.Context, int, string, int, string, string, string, *int, *int, *int, *int, *int, string) error {
	return nil
}
func (r *fakeNovelRepo) DeletePromotionLink(context.Context, int) error           { return nil }
func (r *fakeNovelRepo) ListFBPixels(context.Context) ([]FBPixel, error)          { return nil, nil }
func (r *fakeNovelRepo) CreateFBPixel(context.Context, *FBPixel) (int, error)     { return 0, nil }
func (r *fakeNovelRepo) UpdateFBPixel(context.Context, int, string, string, string) error {
	return nil
}
func (r *fakeNovelRepo) DeleteFBPixel(context.Context, int) error { return nil }

type fakeCoverStore struct {
	deletedRefs *[]string
}

func (fakeCoverStore) IsEnabled() bool       { return true }
func (fakeCoverStore) Prefix() string        { return "covers" }
func (fakeCoverStore) MaxUploadBytes() int64 { return 5 << 20 }
func (fakeCoverStore) IsValidCoverRef(ref string) bool {
	return strings.HasPrefix(ref, "oss://covers/") && strings.HasSuffix(ref, ".png")
}
func (fakeCoverStore) SignCoverURL(raw string) string {
	if strings.HasPrefix(raw, "oss://") {
		return "https://oss.starsfic.com/" + strings.TrimPrefix(raw, "oss://") + "?signed=1"
	}
	return raw
}
func (fakeCoverStore) UploadCover(context.Context, []byte) (string, string, error) {
	return "oss://covers/2026/11/abc.png", "https://oss.starsfic.com/covers/2026/11/abc.png?signed=1", nil
}
func (s fakeCoverStore) DeleteCover(_ context.Context, rawRef string) error {
	if s.deletedRefs != nil {
		*s.deletedRefs = append(*s.deletedRefs, rawRef)
	}
	return nil
}

var _ storage.CoverStore = fakeCoverStore{}

func TestNovelService_CoverUploadValidationAndSigning(t *testing.T) {
	repo := newFakeNovelRepo()
	repo.novels[10000001] = &Novel{
		ID:       10000001,
		Title:    "Legacy Book",
		Author:   "Author",
		CoverURL: "/assets/legacy.png",
		Status:   "Ongoing",
	}

	svc := NewNovelService(repo, nil, fakeCoverStore{})
	ctx := context.Background()

	// 1. Rejects external URL or empty cover on create
	for _, bad := range []string{"", "https://images.unsplash.com/foo.jpg", "/assets/legacy.png"} {
		_, err := svc.CreateNovel(ctx, &Novel{Title: "Book", Author: "A", CoverURL: bad, Status: "Ongoing"})
		if !errors.Is(err, ErrInvalidCover) {
			t.Fatalf("expected ErrInvalidCover for %q, got %v", bad, err)
		}
	}

	// 2. Accepts uploaded oss:// ref on create and returns signed CoverURL + CoverKey
	created, err := svc.CreateNovel(ctx, &Novel{
		Title:    "New Book",
		Author:   "Author",
		CoverURL: "oss://covers/2026/11/abc.png",
		Status:   "Ongoing",
	})
	if err != nil {
		t.Fatalf("CreateNovel failed: %v", err)
	}
	if created.CoverKey != "oss://covers/2026/11/abc.png" {
		t.Fatalf("expected raw CoverKey preserved, got %q", created.CoverKey)
	}
	if created.CoverURL != "https://oss.starsfic.com/covers/2026/11/abc.png?signed=1" {
		t.Fatalf("expected signed CoverURL, got %q", created.CoverURL)
	}
	if repo.novels[created.ID].CoverURL != "oss://covers/2026/11/abc.png" {
		t.Fatalf("DB must persist raw oss:// ref, got %q", repo.novels[created.ID].CoverURL)
	}

	// 3. Update allows preserving legacy cover when untouched, rejects new external URL, allows new uploaded cover
	updatedLegacy, err := svc.UpdateNovel(ctx, 10000001, "Legacy Book v2", "Author", "/assets/legacy.png", "Ongoing", "", "", nil, 4.5, nil, 3)
	if err != nil || updatedLegacy.CoverURL != "/assets/legacy.png" {
		t.Fatalf("expected legacy cover preserved on edit, got %+v err=%v", updatedLegacy, err)
	}
	if _, err := svc.UpdateNovel(ctx, 10000001, "Legacy Book v2", "Author", "https://evil.com/x.png", "Ongoing", "", "", nil, 4.5, nil, 3); !errors.Is(err, ErrInvalidCover) {
		t.Fatalf("expected ErrInvalidCover for external URL on update, got %v", err)
	}
	updatedOSS, err := svc.UpdateNovel(ctx, 10000001, "Legacy Book v2", "Author", "oss://covers/2026/11/abc.png", "Ongoing", "", "", nil, 4.5, nil, 3)
	if err != nil || updatedOSS.CoverKey != "oss://covers/2026/11/abc.png" || updatedOSS.CoverURL != "https://oss.starsfic.com/covers/2026/11/abc.png?signed=1" {
		t.Fatalf("unexpected updated OSS novel: %+v err=%v", updatedOSS, err)
	}
}

func TestParseChaptersFromSingleTXT(t *testing.T) {
	t.Run("default regex handles mixed case, Chinese numerals, and ignores mid-sentence mentions", func(t *testing.T) {
		txt := "\ufeff   chapter 1: The Start\r\n" +
			"She remembered Chapter 2 from last night.\r\n" +
			"  CHAPTER 2\r\n" +
			"Second body line.\r\n" +
			"第两百零一章 终局\r\n" +
			"Final body.\r\n"

		chapters, err := parseChaptersFromSingleTXT([]byte(txt), "")
		if err != nil {
			t.Fatalf("parseChaptersFromSingleTXT failed: %v", err)
		}
		if len(chapters) != 3 {
			t.Fatalf("expected 3 chapters (mid-sentence mention must not split), got %d: %+v", len(chapters), chapters)
		}
		if chapters[0].Index != 0 || chapters[0].Title != "chapter 1: The Start" || !strings.Contains(chapters[0].Content, "Chapter 2") {
			t.Fatalf("unexpected chapter 0: %+v", chapters[0])
		}
		if chapters[1].Index != 1 || chapters[1].Title != "CHAPTER 2" || chapters[1].Content != "Second body line." {
			t.Fatalf("unexpected chapter 1: %+v", chapters[1])
		}
		if chapters[2].Index != 2 || chapters[2].Title != "第两百零一章 终局" || chapters[2].Content != "Final body." {
			t.Fatalf("unexpected chapter 2: %+v", chapters[2])
		}
	})

	t.Run("custom delimiter_regex splits chapters and rejects invalid regex", func(t *testing.T) {
		txt := "Episode 1 - Pilot\nFirst body.\nEpisode 2 - Encore\nSecond body.\n"
		chapters, err := parseChaptersFromSingleTXT([]byte(txt), `(?i)episode\s+\d+`)
		if err != nil {
			t.Fatalf("custom delimiter failed: %v", err)
		}
		if len(chapters) != 2 || chapters[0].Title != "Episode 1 - Pilot" || chapters[1].Index != 1 {
			t.Fatalf("unexpected custom chapters: %+v", chapters)
		}

		if _, err := parseChaptersFromSingleTXT([]byte(txt), `[unclosed`); !errors.Is(err, ErrInvalidDelimiterRegex) {
			t.Fatalf("expected ErrInvalidDelimiterRegex, got %v", err)
		}
	})
}

func TestParseChaptersFromZIP_ZeroBasedSorted(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct {
		name string
		body string
	}{
		{"010_Tenth.txt", "Body ten"},
		{"001_First.txt", "\ufeffBody one\r\nLine two"},
		{"002.TXT", "Body two"},
	} {
		w, err := zw.Create(f.name)
		if err != nil {
			t.Fatalf("create zip entry failed: %v", err)
		}
		_, _ = w.Write([]byte(f.body))
	}
	_ = zw.Close()

	chapters, err := parseChaptersFromZIP(buf.Bytes())
	if err != nil {
		t.Fatalf("parseChaptersFromZIP failed: %v", err)
	}
	if len(chapters) != 3 {
		t.Fatalf("expected 3 chapters, got %d", len(chapters))
	}
	if chapters[0].Index != 0 || chapters[0].Title != "First" || chapters[0].Content != "Body one\nLine two" {
		t.Fatalf("unexpected zip chapter 0: %+v", chapters[0])
	}
	if chapters[1].Index != 1 || chapters[1].Title != "Chapter 2" {
		t.Fatalf("unexpected zip chapter 1: %+v", chapters[1])
	}
	if chapters[2].Index != 2 || chapters[2].Title != "Tenth" {
		t.Fatalf("unexpected zip chapter 2: %+v", chapters[2])
	}
}

type fakeOSSContentStorage struct {
	batchPutErr          error
	putErr               error
	deletedFromCallCount int
	lastDeletedFromIdx   int
	stored               map[int]string
}

func (s *fakeOSSContentStorage) PutContent(_ context.Context, _ int64, idx int, content string) error {
	if s.putErr != nil {
		return s.putErr
	}
	if s.stored == nil {
		s.stored = map[int]string{}
	}
	s.stored[idx] = content
	return nil
}
func (s *fakeOSSContentStorage) GetContent(_ context.Context, _ int64, idx int) (string, error) {
	return s.stored[idx], nil
}
func (s *fakeOSSContentStorage) DeleteContent(context.Context, int64, int) error      { return nil }
func (s *fakeOSSContentStorage) DeleteNovelContent(context.Context, int64) error      { return nil }
func (s *fakeOSSContentStorage) DeleteNovelContentFrom(_ context.Context, _ int64, idx int) error {
	s.deletedFromCallCount++
	s.lastDeletedFromIdx = idx
	return nil
}
func (s *fakeOSSContentStorage) BatchPutContent(_ context.Context, _ int64, items []storage.ChapterContentItem) error {
	if s.batchPutErr != nil {
		return s.batchPutErr
	}
	if s.stored == nil {
		s.stored = map[int]string{}
	}
	for _, it := range items {
		s.stored[it.ChapterIndex] = it.Content
	}
	return nil
}
func (s *fakeOSSContentStorage) StorageType() string { return "oss" }

func TestPublishLifecycleSoftDeleteAndListFilter(t *testing.T) {
	repo := newFakeNovelRepo()
	repo.settings["global_start_pay_chapter_index"] = "4"
	svc := NewNovelService(repo, nil, fakeCoverStore{})
	ctx := context.Background()

	// Creating with publish_status=published on 0-chapter book is rejected
	if _, err := svc.CreateNovel(ctx, &Novel{
		Title: "Empty", Author: "A", CoverURL: "oss://covers/2026/11/abc.png", Status: "Ongoing", PublishStatus: "published",
	}); !errors.Is(err, ErrCannotPublishEmptyNovel) {
		t.Fatalf("expected ErrCannotPublishEmptyNovel on empty create, got %v", err)
	}

	// Default publish_status is draft, and start_pay <= 0 resolves to global setting (4)
	created, err := svc.CreateNovel(ctx, &Novel{
		Title: "Draft Book", Author: "A", CoverURL: "oss://covers/2026/11/abc.png", Status: "Ongoing",
	})
	if err != nil {
		t.Fatalf("CreateNovel failed: %v", err)
	}
	if created.PublishStatus != "draft" {
		t.Fatalf("expected default publish_status=draft, got %q", created.PublishStatus)
	}
	if created.StartPayChapterIndex != 4 {
		t.Fatalf("expected start pay resolved to global 4, got %d", created.StartPayChapterIndex)
	}

	// Still blocked from publishing before chapters are added
	if _, err := svc.UpdatePublishStatus(ctx, created.ID, "published"); !errors.Is(err, ErrCannotPublishEmptyNovel) {
		t.Fatalf("expected ErrCannotPublishEmptyNovel before import, got %v", err)
	}

	// Adding chapter updates novel word_count and allows publishing
	ch, err := svc.CreateChapter(ctx, created.ID, 0, "Chapter 1", "Hello world", false, 0)
	if err != nil || ch.WordCount != 10 {
		t.Fatalf("CreateChapter failed: ch=%+v err=%v", ch, err)
	}
	if repo.novels[created.ID].WordCount != 10 {
		t.Fatalf("expected word_count=10 synced, got %d", repo.novels[created.ID].WordCount)
	}

	pub, err := svc.UpdatePublishStatus(ctx, created.ID, "published")
	if err != nil || pub.PublishStatus != "published" {
		t.Fatalf("expected published, got %+v err=%v", pub, err)
	}

	// ListNovels SQL excludes archived by default
	_, _, err = svc.ListNovels(ctx, 1, 10, "", "", "", "")
	if err != nil || !strings.Contains(repo.lastListSelectQuery, "status != 'archived'") {
		t.Fatalf("expected ListNovels SQL to exclude archived, got %q (err=%v)", repo.lastListSelectQuery, err)
	}

	// DeleteNovel archives and sets publish_status=offline; GetNovel returns ErrNovelNotFound afterwards
	if err := svc.DeleteNovel(ctx, created.ID); err != nil {
		t.Fatalf("DeleteNovel failed: %v", err)
	}
	if repo.novels[created.ID].PublishStatus != "offline" {
		t.Fatalf("expected archived novel publish_status=offline, got %q", repo.novels[created.ID].PublishStatus)
	}
	if _, err := svc.GetNovel(ctx, created.ID); !errors.Is(err, ErrNovelNotFound) {
		t.Fatalf("expected ErrNovelNotFound for archived novel, got %v", err)
	}
}

func TestBulkImportOSSAtomicRollbackAndSettingsPropagation(t *testing.T) {
	repo := newFakeNovelRepo()
	repo.novels[10000001] = &Novel{
		ID: 10000001, Title: "Book", Author: "A", CoverURL: "oss://covers/2026/11/abc.png", Status: "Ongoing", PublishStatus: "draft",
	}
	repo.chapters[10000001] = map[int]*Chapter{
		0: {ID: "old", NovelID: 10000001, ChapterIndex: 0, Title: "Old Chapter", WordCount: 123},
	}

	ossStore := &fakeOSSContentStorage{batchPutErr: errors.New("oss down")}
	svc := NewNovelService(repo, ossStore, fakeCoverStore{})
	ctx := context.Background()

	// OSS failure must roll back DB delete/insert cleanly
	_, err := svc.BulkImportChapters(ctx, 10000001, []byte("Chapter 1: New\nNew body\nChapter 2: Two\nTwo body\n"), "single_txt", "")
	if err == nil {
		t.Fatalf("expected error when OSS upload fails")
	}
	if len(repo.chapters[10000001]) != 1 || repo.chapters[10000001][0].Title != "Old Chapter" {
		t.Fatalf("DB chapters were corrupted after OSS upload failure: %+v", repo.chapters[10000001])
	}

	// Succeeding OSS upload commits and cleans leftover chapters >= len(parsedChapters)
	ossStore.batchPutErr = nil
	res, err := svc.BulkImportChapters(ctx, 10000001, []byte("Chapter 1: New\nNew body\nChapter 2: Two\nTwo body\n"), "single_txt", "")
	if err != nil || res.ChaptersCount != 2 {
		t.Fatalf("BulkImportChapters failed: res=%+v err=%v", res, err)
	}
	if ossStore.deletedFromCallCount != 1 || ossStore.lastDeletedFromIdx != 2 {
		t.Fatalf("expected DeleteNovelContentFrom(novelID, 2), got count=%d idx=%d", ossStore.deletedFromCallCount, ossStore.lastDeletedFromIdx)
	}

	// UpdateSettings with apply_to_all_novels=false updates inherited novels and propagates error
	if err := svc.UpdateSettings(ctx, map[string]string{
		"global_coin_cost_per_thousand": "600",
		"apply_to_all_novels":           "false",
	}); err != nil {
		t.Fatalf("UpdateSettings failed: %v", err)
	}
	if repo.batchInheritedCalls != 1 || repo.batchAllCalled {
		t.Fatalf("expected inherited batch update only, got inherited=%d all=%v", repo.batchInheritedCalls, repo.batchAllCalled)
	}
}

func TestHandlerRBACAndBulkImportLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	config.AppConfig = &config.Config{JWTSecret: "unit-test-secret"}

	repo := newFakeNovelRepo()
	repo.novels[10000001] = &Novel{ID: 10000001, Title: "Book", Author: "A", CoverURL: "oss://covers/2026/11/abc.png", Status: "Ongoing", PublishStatus: "draft"}
	svc := NewNovelService(repo, nil, fakeCoverStore{})
	h := NewHandler(svc)

	r := gin.New()
	api := r.Group("/api/v1/admin")
	h.RegisterRoutes(api)

	buyerToken, _ := auth.GenerateToken("buyer-1", "MediaBuyer")
	editorToken, _ := auth.GenerateToken("editor-1", "Editor")

	// MediaBuyer can read novels list but cannot delete novel or change settings
	reqRead := httptest.NewRequest(http.MethodGet, "/api/v1/admin/novels", nil)
	reqRead.Header.Set("Authorization", "Bearer "+buyerToken)
	wRead := httptest.NewRecorder()
	r.ServeHTTP(wRead, reqRead)
	if wRead.Code != http.StatusOK {
		t.Fatalf("expected MediaBuyer GET /novels=200, got %d: %s", wRead.Code, wRead.Body.String())
	}

	reqDel := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/novels/10000001", nil)
	reqDel.Header.Set("Authorization", "Bearer "+buyerToken)
	wDel := httptest.NewRecorder()
	r.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusForbidden {
		t.Fatalf("expected MediaBuyer DELETE /novels=403, got %d", wDel.Code)
	}

	reqSettings := httptest.NewRequest(http.MethodPost, "/api/v1/admin/settings", strings.NewReader(`{"global_coin_cost_per_thousand":"500"}`))
	reqSettings.Header.Set("Authorization", "Bearer "+editorToken)
	reqSettings.Header.Set("Content-Type", "application/json")
	wSettings := httptest.NewRecorder()
	r.ServeHTTP(wSettings, reqSettings)
	if wSettings.Code != http.StatusForbidden {
		t.Fatalf("expected Editor POST /settings=403, got %d", wSettings.Code)
	}
}

func TestCoverCleanupSettingsValidationAndPromoLinkGuards(t *testing.T) {
	repo := newFakeNovelRepo()
	var deleted []string
	coverStore := fakeCoverStore{deletedRefs: &deleted}
	svc := NewNovelService(repo, nil, coverStore)
	ctx := context.Background()

	// Create two books sharing the same OSS cover key, and one with unique cover key
	sharedKey := "oss://covers/2026/11/shared.png"
	uniqueKey := "oss://covers/2026/11/old.png"
	newKey := "oss://covers/2026/11/new.png"
	repo.novels[1] = &Novel{ID: 1, Title: "B1", Author: "A", CoverURL: sharedKey, Status: "Ongoing", PublishStatus: "draft"}
	repo.novels[2] = &Novel{ID: 2, Title: "B2", Author: "A", CoverURL: sharedKey, Status: "Ongoing", PublishStatus: "draft"}
	repo.novels[3] = &Novel{ID: 3, Title: "B3", Author: "A", CoverURL: uniqueKey, Status: "Ongoing", PublishStatus: "draft"}
	repo.novels[4] = &Novel{ID: 4, Title: "Archived", Author: "A", CoverURL: newKey, Status: "archived", PublishStatus: "offline"}

	// Replacing shared cover on Book 1 must NOT delete OSS object because Book 2 still uses it
	if _, err := svc.UpdateNovel(ctx, 1, "B1", "A", newKey, "Ongoing", "", "", nil, 4.5, nil, 3); err != nil {
		t.Fatalf("UpdateNovel book 1 failed: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("shared cover must not be deleted while referenced by book 2, deleted=%v", deleted)
	}

	// Replacing unique cover on Book 3 deletes old unreferenced OSS object
	if _, err := svc.UpdateNovel(ctx, 3, "B3", "A", newKey, "Ongoing", "", "", nil, 4.5, nil, 3); err != nil {
		t.Fatalf("UpdateNovel book 3 failed: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != uniqueKey {
		t.Fatalf("expected old unique cover %q deleted, got %v", uniqueKey, deleted)
	}

	// UpdateSettings rejects invalid integer strings or <=0 start pay without writing DB
	for _, badCfg := range []map[string]string{
		{"global_coin_cost_per_thousand": "abc"},
		{"global_coin_cost_per_thousand": "-5"},
		{"global_start_pay_chapter_index": "0"},
	} {
		if err := svc.UpdateSettings(ctx, badCfg); !errors.Is(err, ErrInvalidSettingValue) {
			t.Fatalf("expected ErrInvalidSettingValue for %+v, got %v", badCfg, err)
		}
	}
	if len(repo.settings) != 0 {
		t.Fatalf("invalid settings must not persist to DB, got %+v", repo.settings)
	}

	// CreatePromotionLink validates novel_id, pricing, and persists link_id in generated_url
	badStart := 0
	if _, err := svc.CreatePromotionLink(ctx, &PromotionLink{
		NovelID: 1, NovelTitle: "B1", GeneratedURL: "https://starsfic.com/detail?novel_id=1", StartPayChapterIndex: &badStart,
	}); !errors.Is(err, ErrInvalidPromoPricing) {
		t.Fatalf("expected ErrInvalidPromoPricing for startPay=0, got %v", err)
	}
	if _, err := svc.CreatePromotionLink(ctx, &PromotionLink{
		NovelID: 4, NovelTitle: "Archived", GeneratedURL: "https://starsfic.com/detail?novel_id=4",
	}); !errors.Is(err, ErrNovelNotFound) {
		t.Fatalf("expected ErrNovelNotFound for archived novel, got %v", err)
	}

	goodLink, err := svc.CreatePromotionLink(ctx, &PromotionLink{
		NovelID: 1, NovelTitle: "B1", GeneratedURL: "https://starsfic.com/detail?novel_id=1",
	})
	if err != nil {
		t.Fatalf("CreatePromotionLink failed: %v", err)
	}
	if !strings.Contains(goodLink.GeneratedURL, "link_id=1") || repo.promoLinks[goodLink.ID].GeneratedURL != goodLink.GeneratedURL {
		t.Fatalf("expected link_id=1 persisted in generated_url, got link=%q repo=%q", goodLink.GeneratedURL, repo.promoLinks[goodLink.ID].GeneratedURL)
	}

	// Genres length validation: allows up to 2048 runes (including >50 rune single tags), rejects 2049
	exactMaxTag := strings.Repeat(" tag", 0) + strings.Repeat("a", 2048)
	createdMaxGenres, err := svc.CreateNovel(ctx, &Novel{
		Title: "LongGenres", Author: "A", CoverURL: newKey, Status: "Ongoing", Genres: []string{" " + exactMaxTag + " "},
	})
	if err != nil || len(createdMaxGenres.Genres) != 1 || len(createdMaxGenres.Genres[0]) != 2048 {
		t.Fatalf("expected 2048-char genre accepted, got n=%+v err=%v", createdMaxGenres, err)
	}
	if _, err := svc.CreateNovel(ctx, &Novel{
		Title: "TooLongGenres", Author: "A", CoverURL: newKey, Status: "Ongoing", Genres: []string{strings.Repeat("a", 2049)},
	}); !errors.Is(err, ErrInvalidGenres) {
		t.Fatalf("expected ErrInvalidGenres on CreateNovel >2048 chars, got %v", err)
	}
	if _, err := svc.UpdateNovel(ctx, 1, "B1", "A", newKey, "Ongoing", "", "", []string{strings.Repeat("a", 1024), strings.Repeat("b", 1024)}, 4.5, nil, 3); !errors.Is(err, ErrInvalidGenres) {
		t.Fatalf("expected ErrInvalidGenres on UpdateNovel when joined with comma >2048 chars, got %v", err)
	}
}

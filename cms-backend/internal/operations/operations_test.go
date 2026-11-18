package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	dbassets "star-novel-cms/db"
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/config"
	"star-novel-cms/internal/db"
)

func openOperationsTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("STAR_NOVEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "ops_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET search_path TO "+schema+",public")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, _ = admin.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_trgm")
	ddl := strings.ReplaceAll(dbassets.SchemaSQL, "CREATE EXTENSION IF NOT EXISTS pg_trgm;", "")
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestOperationsConfigAndGroups(t *testing.T) {
	pool := openOperationsTestDB(t)
	prevDB := db.DB
	db.DB = pool
	t.Cleanup(func() { db.DB = prevDB })

	ctx := context.Background()
	var id1, id2 int64
	if err := pool.QueryRow(ctx, `INSERT INTO novels (title, author, publish_status, view_count, rating) VALUES ('Book One', 'Author A', 'published', 100, 4.9) RETURNING id`).Scan(&id1); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO novels (title, author, publish_status, view_count, rating) VALUES ('Book Two', 'Author B', 'published', 500, 4.5) RETURNING id`).Scan(&id2); err != nil {
		t.Fatal(err)
	}

	svc := NewService(NewRepository(), nil)

	// Default config
	cfg, err := svc.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if cfg.RankingConfig.SortRule != RankingSortViews || cfg.RankingConfig.Limit != 20 {
		t.Fatalf("unexpected default ranking config: %+v", cfg.RankingConfig)
	}

	// Save invalid ranking rule / limit
	if _, err := svc.SaveRankingConfig(ctx, SaveRankingConfigRequest{SortRule: "bad", Limit: 10}); !errors.Is(err, ErrInvalidRankingSort) {
		t.Fatalf("expected ErrInvalidRankingSort, got %v", err)
	}
	if _, err := svc.SaveRankingConfig(ctx, SaveRankingConfigRequest{SortRule: RankingSortRating, Limit: 0}); !errors.Is(err, ErrInvalidRankingLimit) {
		t.Fatalf("expected ErrInvalidRankingLimit, got %v", err)
	}

	// Save valid ranking rule
	savedCfg, err := svc.SaveRankingConfig(ctx, SaveRankingConfigRequest{SortRule: RankingSortRating, Limit: 15})
	if err != nil {
		t.Fatalf("SaveRankingConfig failed: %v", err)
	}
	if savedCfg.SortRule != RankingSortRating || savedCfg.Limit != 15 {
		t.Fatalf("unexpected saved ranking config: %+v", savedCfg)
	}

	// Save group with ordering and validate error guards
	if _, err := svc.SaveGroup(ctx, "unknown", nil); !errors.Is(err, ErrInvalidGroup) {
		t.Fatalf("expected ErrInvalidGroup, got %v", err)
	}
	if _, err := svc.SaveGroup(ctx, GroupEditorPick, []GroupItemInput{{NovelID: id1}, {NovelID: id1}}); !errors.Is(err, ErrDuplicateNovelInGroup) {
		t.Fatalf("expected ErrDuplicateNovelInGroup, got %v", err)
	}
	start := time.Now().UTC()
	end := start.Add(-time.Hour)
	if _, err := svc.SaveGroup(ctx, GroupEditorPick, []GroupItemInput{{NovelID: id1, ScheduledStart: &start, ScheduledEnd: &end}}); !errors.Is(err, ErrInvalidScheduleRange) {
		t.Fatalf("expected ErrInvalidScheduleRange, got %v", err)
	}

	items, err := svc.SaveGroup(ctx, GroupEditorPick, []GroupItemInput{{NovelID: id2}, {NovelID: id1}})
	if err != nil {
		t.Fatalf("SaveGroup failed: %v", err)
	}
	if len(items) != 2 || items[0].NovelID != id2 || items[1].NovelID != id1 || items[0].SortOrder != 0 || items[1].SortOrder != 1 {
		t.Fatalf("unexpected saved group order: %+v", items)
	}

	fullCfg, err := svc.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig after save failed: %v", err)
	}
	if len(fullCfg.Groups[GroupEditorPick]) != 2 || fullCfg.RankingConfig.SortRule != RankingSortRating || fullCfg.RankingConfig.Limit != 15 {
		t.Fatalf("unexpected persisted config: %+v", fullCfg)
	}

	// Atomic SaveRanking + Preview against real PostgreSQL
	rankingItems := []GroupItemInput{{NovelID: id1}}
	savedRank, err := svc.SaveRanking(ctx, SaveRankingRequest{
		RankingConfig: &SaveRankingConfigRequest{SortRule: RankingSortViews, Limit: 2},
		Items:         &rankingItems,
	})
	if err != nil {
		t.Fatalf("SaveRanking failed: %v", err)
	}
	if savedRank.RankingConfig.SortRule != RankingSortViews || savedRank.RankingConfig.Limit != 2 || len(savedRank.Items) != 1 || savedRank.Items[0].NovelID != id1 {
		t.Fatalf("unexpected SaveRanking result: %+v", savedRank)
	}

	previewRes, err := svc.Preview(ctx, PreviewRequest{
		Groups: map[string][]GroupItemInput{
			GroupEditorPick: {{NovelID: id1}},
			GroupCurated:    {},
			GroupRanking:    {{NovelID: id1}},
		},
	})
	if err != nil {
		t.Fatalf("Preview failed: %v", err)
	}
	if len(previewRes.Featured) != 1 || previewRes.Featured[0].Novel.ID != id1 {
		t.Fatalf("unexpected preview featured: %+v", previewRes.Featured)
	}
	if previewRes.CuratedIsOverride || len(previewRes.Curated) != 2 {
		t.Fatalf("unexpected preview curated fallback: override=%v curated=%+v", previewRes.CuratedIsOverride, previewRes.Curated)
	}
	if len(previewRes.Rankings) != 2 || previewRes.Rankings[0].Novel.ID != id1 || previewRes.Rankings[0].Source != "pinned" || previewRes.Rankings[1].Novel.ID != id2 || previewRes.Rankings[1].Source != "auto" {
		t.Fatalf("unexpected preview rankings: %+v", previewRes.Rankings)
	}
}

type mockOpsRepo struct {
	rankingCfg RankingConfig
	checkInCfg CheckInConfig
	novels     map[int64]RecommendationNovel
	groups     map[string][]RecommendationEntry
}

func newMockOpsRepo() *mockOpsRepo {
	return &mockOpsRepo{
		rankingCfg: RankingConfig{SortRule: RankingSortViews, Limit: 20},
		checkInCfg: CheckInConfig{Rewards: []int{10, 15, 20, 25, 30, 35, 50}},
		novels:     map[int64]RecommendationNovel{},
		groups: map[string][]RecommendationEntry{
			GroupEditorPick: {},
			GroupCurated:    {},
			GroupRanking:    {},
		},
	}
}

func (m *mockOpsRepo) GetCheckInConfig(context.Context) (CheckInConfig, error) {
	out := make([]int, len(m.checkInCfg.Rewards))
	copy(out, m.checkInCfg.Rewards)
	return CheckInConfig{Rewards: out}, nil
}

func (m *mockOpsRepo) SaveCheckInConfig(_ context.Context, cfg CheckInConfig) error {
	out := make([]int, len(cfg.Rewards))
	copy(out, cfg.Rewards)
	m.checkInCfg = CheckInConfig{Rewards: out}
	return nil
}

func (m *mockOpsRepo) GetRankingConfig(context.Context) (RankingConfig, error) {
	return m.rankingCfg, nil
}

func (m *mockOpsRepo) SaveRankingConfig(_ context.Context, cfg RankingConfig) error {
	m.rankingCfg = cfg
	return nil
}

func (m *mockOpsRepo) SaveRanking(_ context.Context, cfg *RankingConfig, items *[]GroupItemInput) error {
	if cfg != nil {
		m.rankingCfg = *cfg
	}
	if items != nil {
		if err := m.SaveGroup(context.Background(), GroupRanking, *items); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockOpsRepo) ListRecommendations(context.Context) ([]RecommendationEntry, error) {
	var all []RecommendationEntry
	for _, g := range []string{GroupEditorPick, GroupCurated, GroupRanking} {
		all = append(all, m.groups[g]...)
	}
	return all, nil
}

func (m *mockOpsRepo) ListGroup(_ context.Context, groupName string) ([]RecommendationEntry, error) {
	entries := m.groups[groupName]
	out := make([]RecommendationEntry, len(entries))
	copy(out, entries)
	return out, nil
}

func (m *mockOpsRepo) SaveGroup(_ context.Context, groupName string, items []GroupItemInput) error {
	out := make([]RecommendationEntry, 0, len(items))
	for i, it := range items {
		n, ok := m.novels[it.NovelID]
		if !ok || n.Status == "archived" {
			return ErrNovelNotFound
		}
		nCopy := n
		out = append(out, RecommendationEntry{
			ID:             int64(i + 1),
			GroupName:      groupName,
			NovelID:        it.NovelID,
			SortOrder:      i,
			ScheduledStart: it.ScheduledStart,
			ScheduledEnd:   it.ScheduledEnd,
			Novel:          &nCopy,
		})
	}
	m.groups[groupName] = out
	return nil
}

func (m *mockOpsRepo) ListPublishedGenres(context.Context) ([]string, error) {
	set := map[string]bool{}
	for _, n := range m.novels {
		if n.Status != "archived" && n.PublishStatus == "published" {
			for _, g := range n.Genres {
				if strings.TrimSpace(g) != "" {
					set[strings.TrimSpace(g)] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out, nil
}

func (m *mockOpsRepo) GetNovelsByIDs(_ context.Context, ids []int64, publishedOnly bool) (map[int64]RecommendationNovel, error) {
	out := make(map[int64]RecommendationNovel, len(ids))
	for _, id := range ids {
		n, ok := m.novels[id]
		if !ok || n.Status == "archived" {
			continue
		}
		if publishedOnly && n.PublishStatus != "published" {
			continue
		}
		out[id] = n
	}
	return out, nil
}

func (m *mockOpsRepo) ListTopPublishedNovels(_ context.Context, sortRule string, excludeIDs []int64, limit int) ([]RecommendationNovel, error) {
	excl := make(map[int64]bool, len(excludeIDs))
	for _, id := range excludeIDs {
		excl[id] = true
	}
	var list []RecommendationNovel
	for _, n := range m.novels {
		if n.Status != "archived" && n.PublishStatus == "published" && !excl[n.ID] {
			list = append(list, n)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		switch sortRule {
		case RankingSortRating:
			if list[i].Rating != list[j].Rating {
				return list[i].Rating > list[j].Rating
			}
			if list[i].ViewCount != list[j].ViewCount {
				return list[i].ViewCount > list[j].ViewCount
			}
		case RankingSortLatest:
			if list[i].ID != list[j].ID {
				return list[i].ID > list[j].ID
			}
		default:
			if list[i].ViewCount != list[j].ViewCount {
				return list[i].ViewCount > list[j].ViewCount
			}
		}
		return list[i].ID > list[j].ID
	})
	if len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

func TestPreviewAndSaveRankingUnit(t *testing.T) {
	repo := newMockOpsRepo()
	repo.novels = map[int64]RecommendationNovel{
		1: {ID: 1, Title: "Most Viewed", Author: "A", Rating: 4.1, ViewCount: 1000, Status: "Ongoing", PublishStatus: "published", Synopsis: "Syn 1", Genres: []string{"Romance"}},
		2: {ID: 2, Title: "Top Rated", Author: "B", Rating: 4.9, ViewCount: 300, Status: "Ongoing", PublishStatus: "published", Synopsis: "Syn 2", Genres: []string{"Fantasy"}},
		3: {ID: 3, Title: "Newest", Author: "C", Rating: 4.3, ViewCount: 100, Status: "Completed", PublishStatus: "published", Synopsis: "Syn 3", Genres: []string{"Fantasy", "CEO"}},
		4: {ID: 4, Title: "Draft Hidden", Author: "D", Rating: 5.0, ViewCount: 9999, Status: "Ongoing", PublishStatus: "draft", Synopsis: "Syn 4", Genres: []string{"Horror"}},
	}

	svc := NewService(repo, nil)
	ctx := context.Background()

	// AvailableGenres includes only published novels
	cfg, err := svc.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if strings.Join(cfg.AvailableGenres, ",") != "CEO,Fantasy,Romance" {
		t.Fatalf("unexpected available genres: %v", cfg.AvailableGenres)
	}

	// Empty SaveRanking rejected
	if _, err := svc.SaveRanking(ctx, SaveRankingRequest{}); !errors.Is(err, ErrEmptyRankingUpdate) {
		t.Fatalf("expected ErrEmptyRankingUpdate, got %v", err)
	}

	// Atomic SaveRanking
	items := []GroupItemInput{{NovelID: 3}}
	saved, err := svc.SaveRanking(ctx, SaveRankingRequest{
		RankingConfig: &SaveRankingConfigRequest{SortRule: RankingSortRating, Limit: 3},
		Items:         &items,
	})
	if err != nil {
		t.Fatalf("SaveRanking failed: %v", err)
	}
	if saved.RankingConfig.SortRule != RankingSortRating || saved.RankingConfig.Limit != 3 || len(saved.Items) != 1 || saved.Items[0].NovelID != 3 {
		t.Fatalf("unexpected SaveRanking response: %+v", saved)
	}

	// Time-travel preview with draft overrides
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	futureStart := now.Add(24 * time.Hour)
	pastEnd := now.Add(-1 * time.Hour)
	futurePreviewAt := now.Add(25 * time.Hour)

	draftReq := PreviewRequest{
		At: &now,
		RankingConfig: &SaveRankingConfigRequest{
			SortRule: RankingSortManual,
			Limit:    2,
		},
		Groups: map[string][]GroupItemInput{
			GroupEditorPick: {
				{NovelID: 4},                              // draft -> filtered out
				{NovelID: 2, ScheduledEnd: &pastEnd},      // expired -> filtered out
				{NovelID: 3, ScheduledStart: &futureStart}, // future -> filtered out now
			},
			GroupCurated: {
				{NovelID: 2},
			},
			GroupRanking: {
				{NovelID: 3, ScheduledStart: &futureStart}, // not active now
			},
		},
	}

	resNow, err := svc.Preview(ctx, draftReq)
	if err != nil {
		t.Fatalf("Preview(now) failed: %v", err)
	}
	if !resNow.FeaturedIsFallback || len(resNow.Featured) == 0 || resNow.Featured[0].Source != "fallback" {
		t.Fatalf("expected featured fallback at now, got fallback=%v items=%+v", resNow.FeaturedIsFallback, resNow.Featured)
	}
	if !resNow.CuratedIsOverride || len(resNow.Curated) != 1 || resNow.Curated[0].Novel.ID != 2 {
		t.Fatalf("expected curated override [2], got %+v", resNow.Curated)
	}
	if !resNow.RankingManualFallback || len(resNow.Rankings) != 2 || resNow.Rankings[0].Source != "fallback" {
		t.Fatalf("expected manual fallback when 0 active pinned items, got %+v", resNow)
	}

	// Same draft previewed 25 hours later activates scheduled novel 3
	draftReq.At = &futurePreviewAt
	resFuture, err := svc.Preview(ctx, draftReq)
	if err != nil {
		t.Fatalf("Preview(future) failed: %v", err)
	}
	if resFuture.FeaturedIsFallback || len(resFuture.Featured) != 1 || resFuture.Featured[0].Novel.ID != 3 || resFuture.Featured[0].Source != "pinned" {
		t.Fatalf("expected scheduled featured [3] at future, got %+v", resFuture.Featured)
	}
	if resFuture.RankingManualFallback || len(resFuture.Rankings) != 1 || resFuture.Rankings[0].Novel.ID != 3 || resFuture.Rankings[0].Source != "pinned" {
		t.Fatalf("expected manual ranking [3] at future without fallback, got %+v", resFuture.Rankings)
	}
}

func TestOperationsHandlerRolePermissionsAndPreview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "test-secret"}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	repo := newMockOpsRepo()
	repo.novels[1] = RecommendationNovel{ID: 1, Title: "Book 1", Author: "A", Rating: 4.5, ViewCount: 100, Status: "Ongoing", PublishStatus: "published"}
	svc := NewService(repo, nil)
	h := NewHandler(svc)

	r := gin.New()
	api := r.Group("/api/v1/admin")
	h.RegisterRoutes(api)

	editorToken, err := auth.GenerateToken("admin-1", "Editor")
	if err != nil {
		t.Fatal(err)
	}
	buyerToken, err := auth.GenerateToken("admin-2", "MediaBuyer")
	if err != nil {
		t.Fatal(err)
	}

	// MediaBuyer can preview (read-only)
	wPreview := httptest.NewRecorder()
	reqPreview := httptest.NewRequest(http.MethodPost, "/api/v1/admin/operations/preview", bytes.NewBufferString(`{}`))
	reqPreview.Header.Set("Authorization", "Bearer "+buyerToken)
	reqPreview.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wPreview, reqPreview)
	if wPreview.Code != http.StatusOK {
		t.Fatalf("expected 200 for preview by MediaBuyer, got %d: %s", wPreview.Code, wPreview.Body.String())
	}

	// MediaBuyer cannot save ranking
	wBuyerSave := httptest.NewRecorder()
	reqBuyerSave := httptest.NewRequest(http.MethodPut, "/api/v1/admin/operations/ranking", bytes.NewBufferString(`{"ranking_config":{"sort_rule":"views","limit":10}}`))
	reqBuyerSave.Header.Set("Authorization", "Bearer "+buyerToken)
	reqBuyerSave.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wBuyerSave, reqBuyerSave)
	if wBuyerSave.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for SaveRanking by MediaBuyer, got %d", wBuyerSave.Code)
	}

	// Editor can save ranking atomically
	wEditorSave := httptest.NewRecorder()
	reqEditorSave := httptest.NewRequest(http.MethodPut, "/api/v1/admin/operations/ranking", bytes.NewBufferString(`{"ranking_config":{"sort_rule":"rating","limit":12},"items":[{"novel_id":1}]}`))
	reqEditorSave.Header.Set("Authorization", "Bearer "+editorToken)
	reqEditorSave.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wEditorSave, reqEditorSave)
	if wEditorSave.Code != http.StatusOK {
		t.Fatalf("expected 200 for SaveRanking by Editor, got %d: %s", wEditorSave.Code, wEditorSave.Body.String())
	}
	var saveResp SaveRankingResponse
	if err := json.Unmarshal(wEditorSave.Body.Bytes(), &saveResp); err != nil {
		t.Fatal(err)
	}
	if saveResp.RankingConfig.SortRule != RankingSortRating || saveResp.RankingConfig.Limit != 12 || len(saveResp.Items) != 1 {
		t.Fatalf("unexpected SaveRanking HTTP response: %+v", saveResp)
	}

	// Editor can save check-in gradient; invalid length rejected with 400
	wInvalidCheckin := httptest.NewRecorder()
	reqInvalidCheckin := httptest.NewRequest(http.MethodPut, "/api/v1/admin/operations/campaigns/checkin", bytes.NewBufferString(`{"rewards":[10,20]}`))
	reqInvalidCheckin.Header.Set("Authorization", "Bearer "+editorToken)
	reqInvalidCheckin.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wInvalidCheckin, reqInvalidCheckin)
	if wInvalidCheckin.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid checkin rewards, got %d", wInvalidCheckin.Code)
	}

	wSaveCheckin := httptest.NewRecorder()
	reqSaveCheckin := httptest.NewRequest(http.MethodPut, "/api/v1/admin/operations/campaigns/checkin", bytes.NewBufferString(`{"rewards":[10,12,15,20,25,30,60]}`))
	reqSaveCheckin.Header.Set("Authorization", "Bearer "+editorToken)
	reqSaveCheckin.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wSaveCheckin, reqSaveCheckin)
	if wSaveCheckin.Code != http.StatusOK {
		t.Fatalf("expected 200 for SaveCheckInConfig by Editor, got %d: %s", wSaveCheckin.Code, wSaveCheckin.Body.String())
	}
}

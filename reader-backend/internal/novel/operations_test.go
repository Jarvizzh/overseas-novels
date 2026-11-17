package novel

import (
	"context"
	"testing"

	"reader-backend/internal/testdb"
)

func TestOperationsCatalogHonorsCMSRecommendationsAndRankingRules(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO novels(id, title, author, cover_url, rating, view_count, publish_status, status) VALUES
		(1, 'Views Leader', 'Auth A', '', 4.1, 1000, 'published', 'Ongoing'),
		(2, 'Rating Leader', 'Auth B', '', 4.9, 100, 'published', 'Ongoing'),
		(3, 'Pinned Rank One', 'Auth C', '', 4.2, 50, 'published', 'Ongoing'),
		(4, 'Draft Hidden', 'Auth D', '', 5.0, 9999, 'draft', 'Ongoing');
		INSERT INTO recommendations(group_name, novel_id, sort_order) VALUES
		('editor_pick', 2, 0),
		('editor_pick', 4, 1),
		('curated', 3, 0),
		('curated', 1, 1),
		('ranking', 3, 0);
		INSERT INTO system_configs(key, value) VALUES
		('ranking_sort_rule', 'rating'),
		('ranking_limit', '3')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
	`)
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService(NewDBRepository(), nil, nil, nil)
	cat, err := svc.GetOperationsCatalog(ctx)
	if err != nil {
		t.Fatalf("GetOperationsCatalog failed: %v", err)
	}

	if len(cat.Featured) != 1 || cat.Featured[0].ID != 2 {
		t.Fatalf("expected only published featured book 2, got %+v", cat.Featured)
	}
	if len(cat.Curated) != 2 || cat.Curated[0].ID != 3 || cat.Curated[1].ID != 1 {
		t.Fatalf("expected curated order [3, 1], got %+v", cat.Curated)
	}
	if cat.RankingRule != "rating" {
		t.Fatalf("expected ranking rule rating, got %q", cat.RankingRule)
	}
	// Pinned book 3 comes first, then remaining published books sorted by rating DESC (2 then 1)
	if len(cat.Rankings) != 3 || cat.Rankings[0].ID != 3 || cat.Rankings[1].ID != 2 || cat.Rankings[2].ID != 1 {
		t.Fatalf("expected rankings [3, 2, 1], got %+v", cat.Rankings)
	}

	// Switch to manual-only mode
	if _, err := pool.Exec(ctx, `UPDATE system_configs SET value='manual' WHERE key='ranking_sort_rule'`); err != nil {
		t.Fatal(err)
	}
	catManual, err := svc.GetOperationsCatalog(ctx)
	if err != nil {
		t.Fatalf("GetOperationsCatalog manual failed: %v", err)
	}
	if len(catManual.Rankings) != 1 || catManual.Rankings[0].ID != 3 {
		t.Fatalf("expected manual-only rankings [3], got %+v", catManual.Rankings)
	}

	// Empty recommendations and empty system_configs fallback
	if _, err := pool.Exec(ctx, `DELETE FROM recommendations; DELETE FROM system_configs WHERE key IN ('ranking_sort_rule', 'ranking_limit');`); err != nil {
		t.Fatal(err)
	}
	catDefault, err := svc.GetOperationsCatalog(ctx)
	if err != nil {
		t.Fatalf("GetOperationsCatalog default failed: %v", err)
	}
	if catDefault.RankingRule != "views" || len(catDefault.Rankings) != 3 || catDefault.Rankings[0].ID != 1 {
		t.Fatalf("expected views fallback rankings starting with book 1, got rule=%q rankings=%+v", catDefault.RankingRule, catDefault.Rankings)
	}
}

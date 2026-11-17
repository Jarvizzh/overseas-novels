package operations

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	dbassets "star-novel-cms/db"
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
}

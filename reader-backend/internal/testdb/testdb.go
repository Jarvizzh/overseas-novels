// Package testdb provisions a private schema for opt-in integration tests.
package testdb

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"reader-backend/internal/db"
	"testing"
)

func Open(t *testing.T) *pgxpool.Pool {
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
	schema := "test_" + uuid.New().String()[:8]
	if _, err = admin.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public; CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.DB = nil
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	sql, err := os.ReadFile("../../../cms-backend/db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	applyChapterMigration(t, pool)

	db.DB = pool
	return pool
}

// applyChapterMigration applies the current chapter publication release to a baseline test database.
func applyChapterMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, version := range []string{"2026111702", "2026111703"} {
		sql, err := os.ReadFile("../../../cms-backend/db/migrations/" + version + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
			t.Fatal(err)
		}
	}
}

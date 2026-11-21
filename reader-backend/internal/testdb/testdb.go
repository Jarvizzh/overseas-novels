// Package testdb provisions a private schema for opt-in integration tests.
package testdb

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"reader-backend/internal/db"
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
		_, _ = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+"_conversion, "+schema+" CASCADE")
		admin.Close()
	})
	sql, err := os.ReadFile("../../../cms-backend/db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	applyMigrations(t, pool, schema)

	db.DB = pool
	return pool
}

func applyMigrations(t *testing.T, pool *pgxpool.Pool, schema string) {
	t.Helper()
	for _, version := range []string{"2026111702", "2026111703", "2026111704", "2026111901", "2026111902", "2026111903", "2026111904", "2026111905", "2026111906", "2026111907", "2026111908", "2026111909", "2026111910", "2026111911", "2026111912", "2026111913", "2026111914", "2026111915"} {
		sql, err := os.ReadFile("../../../cms-backend/db/migrations/" + version + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		migration := string(sql)
		if version >= "2026111901" {
			// Preserve schema isolation for tests that share a database.
			migration = strings.NewReplacer("CREATE SCHEMA conversion;", "CREATE SCHEMA "+schema+"_conversion;", "conversion.", schema+"_conversion.", "public.admins", schema+".admins", "public.recharge_orders", schema+".recharge_orders", "public.payment_event_outbox", schema+".payment_event_outbox",
				"public.promotion_links", schema+".promotion_links", "public.fb_pixels", schema+".fb_pixels",
				"public.reader_schema_migrations", schema+".reader_schema_migrations", "public.reader_payment_promotion_coverage", schema+".reader_payment_promotion_coverage",
				"public.reader_payment_first_on_promotion", schema+".reader_payment_first_on_promotion",
				"public.reader_payment_promotion_key", schema+".reader_payment_promotion_key",
				"public.reader_payment_coverage_verified", schema+".reader_payment_coverage_verified",
				"public.reader_subscription_payment_kind", schema+".reader_subscription_payment_kind",
				"public.user_subscriptions", schema+".user_subscriptions", "public.third_party_payment_orders", schema+".third_party_payment_orders",
				"pg_catalog, public, pg_temp", "pg_catalog, "+schema+", pg_temp").Replace(migration)
		}
		if _, err := pool.Exec(context.Background(), migration); err != nil {
			t.Fatal(err)
		}
	}
}

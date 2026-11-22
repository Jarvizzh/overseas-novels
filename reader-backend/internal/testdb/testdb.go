// Package testdb provisions a private schema for opt-in integration tests.
package testdb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"reader-backend/internal/db"
)

func cmsDBDir() string {
	_, helperFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(helperFile), "../../../cms-backend/db")
}

// ApplyBaseSchema runs the CMS schema bootstrap in the pool's isolated schema.
func ApplyBaseSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	sql, err := os.ReadFile(filepath.Join(cmsDBDir(), "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatal(err)
	}
}

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
	ApplyBaseSchema(t, pool)
	applyMigrations(t, pool, schema, cmsDBDir())

	db.DB = pool
	return pool
}

func applyMigrations(t *testing.T, pool *pgxpool.Pool, schema, cmsDB string) {
	t.Helper()
	for _, version := range []string{"2026111702", "2026111703", "2026111704", "2026111901", "2026111902", "2026111903", "2026111904", "2026111905", "2026111906", "2026111907", "2026111908", "2026111909", "2026111910", "2026111911", "2026111912", "2026111913", "2026111914", "2026111915", "2026111916", "2026111917", "2026111918", "2026111919", "2026111920", "2026111921", "2026111922", "2026111923", "2026111924", "2026111925", "2026111926", "2026111927"} {
		sql, err := os.ReadFile(filepath.Join(cmsDB, "migrations", version+".sql"))
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
				"public.payment_stripe_invoice_periods", schema+".payment_stripe_invoice_periods",
				"public.user_subscriptions", schema+".user_subscriptions", "public.third_party_payment_orders", schema+".third_party_payment_orders",
				"pg_catalog, public, pg_temp", "pg_catalog, "+schema+", pg_temp").Replace(migration)
		}
		if _, err := pool.Exec(context.Background(), migration); err != nil {
			t.Fatal(err)
		}
	}
}

// ScopeSubscriptions records the channel identity for explicitly seeded agreements.
func ScopeSubscriptions(t *testing.T, pool *pgxpool.Pool, provider, environment string, refs ...string) {
	t.Helper()
	ctx := t.Context()
	account := "fixture-" + provider + "-" + environment
	_, err := pool.Exec(ctx, `INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES($1,$2,$3,'fixture','fixture') ON CONFLICT DO NOTHING`, provider, account, environment)
	if err != nil {
		t.Fatal(err)
	}
	result, err := pool.Exec(ctx, `UPDATE user_subscriptions s SET checkout_account_id=a.id
		FROM payment_channel_accounts a WHERE a.provider=$1 AND a.account_key=$2 AND a.environment=$3
		AND s.payment_method=$1 AND s.subscription_id=ANY($4)`, provider, account, environment, refs)
	if err != nil || result.RowsAffected() != int64(len(refs)) {
		t.Fatalf("scope subscription fixtures %v: %v", refs, err)
	}
}

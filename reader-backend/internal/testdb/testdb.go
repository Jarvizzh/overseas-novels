// Package testdb provisions a private database for opt-in integration tests.
package testdb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"reader-backend/internal/db"
)

func cmsDBDir() string {
	_, helperFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(helperFile), "../../../cms-backend/db")
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
	database := "reader_test_" + uuid.NewString()[:8]
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+database); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+database+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = database
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, asset := range []string{"schema.sql", "migrations/2026112801.sql"} {
		sql, err := os.ReadFile(filepath.Join(cmsDBDir(), asset))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	db.DB = pool
	t.Cleanup(func() { db.DB = nil })
	return pool
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

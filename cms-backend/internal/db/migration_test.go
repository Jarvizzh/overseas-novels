package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	dbassets "star-novel-cms/db"
)

func migrationPool(t *testing.T) (*pgxpool.Pool, string) {
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
	schema := "cms_test_" + uuid.NewString()[:8]
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
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
	t.Cleanup(func() { pool.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	return pool, schema
}

func TestMigrationAtomicityAndVersionGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, stage := range []string{"schema", "seed"} {
		t.Run(stage+" failure rolls back", func(t *testing.T) {
			pool, schema := migrationPool(t)
			originalSchema, originalSeed := dbassets.SchemaSQL, dbassets.SeedSQL
			t.Cleanup(func() { dbassets.SchemaSQL, dbassets.SeedSQL = originalSchema, originalSeed })
			if stage == "schema" {
				dbassets.SchemaSQL += "\nSELECT 1/0;"
			} else {
				dbassets.SeedSQL += "\nSELECT 1/0;"
			}
			if err := autoMigrate(ctx, pool); err == nil {
				t.Fatal("migration failure ignored")
			}
			var ledger *string
			if err := pool.QueryRow(ctx, "SELECT to_regclass($1)::text", schema+".reader_schema_migrations").Scan(&ledger); err != nil || ledger != nil {
				t.Fatalf("partial migration committed: %v %v", ledger, err)
			}
		})
	}
	pool, _ := migrationPool(t)
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var before, after time.Time
	if err := pool.QueryRow(ctx, "SELECT applied_at FROM reader_schema_migrations WHERE version=$1", RequiredSchemaVersion).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT applied_at FROM reader_schema_migrations WHERE version=$1", RequiredSchemaVersion).Scan(&after); err != nil || !after.Equal(before) {
		t.Fatalf("cutover was extended: %s %s %v", before, after, err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO reader_schema_migrations(version) VALUES($1)", RequiredSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	if err := autoMigrate(ctx, pool); err == nil {
		t.Fatal("older CMS accepted newer schema")
	}
}

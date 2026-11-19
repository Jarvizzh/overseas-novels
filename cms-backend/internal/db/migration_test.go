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
	database := "cms_test_" + uuid.NewString()[:8]
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
	return pool, "public"
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

func TestMigrationSkipsBaselineOnUpgradeAndConcurrentRestart(t *testing.T) {
	pool, _ := migrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, dbassets.SchemaSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO novels(id,title,author) VALUES(1,'Book','Author');
		INSERT INTO users(id) VALUES(1);
		INSERT INTO chapters(id,novel_id,chapter_index,title,content,created_at)
		VALUES('original',1,0,'Original','A',now()-interval '2 days')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO unlock_records(user_id,novel_id,chapter_index,created_at)
		VALUES(1,1,0,clock_timestamp()),(1,1,1,clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO chapters(id,novel_id,chapter_index,title,content,created_at)
		VALUES('replacement',1,1,'Replacement','B',now()-interval '2 days')`); err != nil {
		t.Fatal(err)
	}
	var track string
	if err := pool.QueryRow(ctx, "SHOW track_commit_timestamp").Scan(&track); err != nil {
		t.Fatal(err)
	}

	original := dbassets.SchemaSQL
	dbassets.SchemaSQL = "SELECT 1/0;"
	t.Cleanup(func() { dbassets.SchemaSQL = original })
	results := make(chan error, 3)
	for range 3 {
		go func() { results <- autoMigrate(ctx, pool) }()
	}
	for range 3 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var mapped, unresolved, versions int
	err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM unlock_records WHERE chapter_id='original' AND identity_evidence IS NOT NULL),
		(SELECT count(*) FROM unlock_records WHERE chapter_id IS NULL AND chapter_index=1),
		(SELECT count(*) FROM reader_schema_migrations WHERE version=$1)`, RequiredSchemaVersion).
		Scan(&mapped, &unresolved, &versions)
	wantMapped := 0
	if track == "on" {
		wantMapped = 1
	}
	if err != nil || mapped != wantMapped || unresolved != 1 || versions != 1 {
		t.Fatalf("migration evidence: resolved=%d unresolved=%d versions=%d error=%v", mapped, unresolved, versions, err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, "LOCK TABLE transactions IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	restartCtx, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	defer stop()
	if err := autoMigrate(restartCtx, pool); err != nil {
		t.Fatalf("same-version migration tried to lock a business table: %v", err)
	}
	var title string
	if err := pool.QueryRow(restartCtx, "SELECT title FROM novels WHERE id=1").Scan(&title); err != nil || title != "Book" {
		t.Fatalf("reader query blocked: %q %v", title, err)
	}
}

func TestConversionMigrationUpgradesReaderQueueVersionAtomically(t *testing.T) {
	pool, _ := migrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, migration := range []string{
		dbassets.SchemaSQL, dbassets.ChapterPublicationSQL, dbassets.ReaderPerformanceSQL, dbassets.ReaderQueueAndShelfSQL,
	} {
		if _, err := pool.Exec(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}

	original := dbassets.ConversionReportingSQL
	defer func() { dbassets.ConversionReportingSQL = original }()
	dbassets.ConversionReportingSQL += "\nSELECT 1/0;"
	if err := autoMigrate(ctx, pool); err == nil {
		t.Fatal("conversion migration failure ignored")
	}
	var version int64
	var conversionTable *string
	err := pool.QueryRow(ctx, "SELECT max(version), to_regclass('conversion.events')::text FROM reader_schema_migrations").
		Scan(&version, &conversionTable)
	if err != nil || version != readerQueueVersion || conversionTable != nil {
		t.Fatalf("failed upgrade changed schema: version=%d, conversion=%v, err=%v", version, conversionTable, err)
	}

	dbassets.ConversionReportingSQL = original
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, "SELECT max(version), to_regclass('conversion.events')::text FROM reader_schema_migrations").
		Scan(&version, &conversionTable)
	if err != nil || version != RequiredSchemaVersion || conversionTable == nil {
		t.Fatalf("upgrade did not install schema: version=%d, conversion=%v, err=%v", version, conversionTable, err)
	}
}

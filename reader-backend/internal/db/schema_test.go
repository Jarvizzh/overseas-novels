package db_test

import (
	"context"
	"testing"

	"reader-backend/internal/db"
	"reader-backend/internal/testdb"
)

func TestSchemaReadinessGate(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if err := db.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM reader_schema_migrations WHERE version = $1", db.RequiredSchemaVersion); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckSchema(ctx); err == nil {
		t.Fatal("older schema accepted")
	}
	newer := db.RequiredSchemaVersion + 1
	if _, err := pool.Exec(ctx, "INSERT INTO reader_schema_migrations(version) VALUES($1)", newer); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckSchema(ctx); err == nil {
		t.Fatalf("schema %d accepted", newer)
	}

	if _, err := pool.Exec(ctx, "ALTER TABLE reader_schema_migrations RENAME TO missing_ledger; CREATE TABLE reader_schema_migrations(unrelated int)"); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckSchema(ctx); err == nil {
		t.Fatal("missing schema contract accepted")
	}
}

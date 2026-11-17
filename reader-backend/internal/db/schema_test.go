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
	for _, version := range []int64{0, db.RequiredSchemaVersion + 1} {
		if _, err := pool.Exec(ctx, "UPDATE reader_schema_migrations SET version=$1", version); err != nil {
			t.Fatal(err)
		}
		if err := db.CheckSchema(ctx); err == nil {
			t.Fatalf("schema %d accepted", version)
		}
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE reader_schema_migrations RENAME TO missing_ledger; CREATE TABLE reader_schema_migrations(unrelated int)"); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckSchema(ctx); err == nil {
		t.Fatal("missing schema contract accepted")
	}
}

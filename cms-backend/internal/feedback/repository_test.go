package feedback

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	dbassets "star-novel-cms/db"
	"star-novel-cms/internal/db"
)

func openFeedbackTestDB(t *testing.T) *pgxpool.Pool {
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
	schema := "feedback_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

func TestFeedbackListAndGetHandleNullColumns(t *testing.T) {
	pool := openFeedbackTestDB(t)
	prevDB := db.DB
	db.DB = pool
	t.Cleanup(func() { db.DB = prevDB })

	ctx := context.Background()
	var insertedID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO feedback (user_id, email, subject, content, status, admin_reply)
		VALUES (NULL, NULL, NULL, 'Reader message without optional fields', NULL, NULL)
		RETURNING id
	`).Scan(&insertedID)
	if err != nil {
		t.Fatal(err)
	}

	repo := NewRepository()
	items, total, err := repo.List(ctx, 1, 20, "", "")
	if err != nil {
		t.Fatalf("List failed with NULL columns: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected 1 feedback item, got total=%d len=%d", total, len(items))
	}
	if items[0].ID != insertedID || items[0].AdminReply != "" || items[0].Email != "" || items[0].Subject != "" || items[0].Status != "pending" || items[0].UserID != 0 {
		t.Fatalf("unexpected scanned defaults: %+v", items[0])
	}

	single, err := repo.GetByID(ctx, insertedID)
	if err != nil {
		t.Fatalf("GetByID failed with NULL columns: %v", err)
	}
	if single.AdminReply != "" || single.Status != "pending" || single.Content != "Reader message without optional fields" {
		t.Fatalf("unexpected GetByID result: %+v", single)
	}
}

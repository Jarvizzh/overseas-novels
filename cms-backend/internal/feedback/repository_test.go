package feedback

import (
	"context"
	"testing"

	"star-novel-cms/internal/db"
	"star-novel-cms/internal/testdb"
)

func TestFeedbackListAndGetHandleNullColumns(t *testing.T) {
	pool := testdb.Initialized(t)
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

package shelf

import (
	"context"
	"reader-backend/internal/testdb"
	"testing"
	"time"
)

func TestProgressWritesAreMonotonic(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO users(id) VALUES(1);INSERT INTO novels(id,title,author) VALUES(1,'Book','Author')`); err != nil {
		t.Fatal(err)
	}
	r := NewDBRepository()
	now := time.Now().UTC()
	if err := r.SaveProgress(ctx, 1, 1, 2, 0.5, now); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveProgress(ctx, 1, 1, 1, 0.1, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetProgress(ctx, 1, 1)
	if err != nil || got.ChapterIndex != 2 {
		t.Fatalf("progress regressed: %+v %v", got, err)
	}
	if err := NewService(r, nil).SyncProgress(ctx, 1, []ProgressUpdate{{NovelID: 1, UpdatedAt: now.AddDate(1, 0, 0)}}); err == nil {
		t.Fatal("future progress accepted")
	}
}

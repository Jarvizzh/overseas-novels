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

func TestProgressBatchDeduplicatesAndCommitsAtomically(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO users(id) VALUES(1); INSERT INTO novels(id,title,author) VALUES(1,'A','A'),(2,'B','B'); INSERT INTO bookshelves(user_id,novel_id,in_shelf,updated_at) VALUES(1,1,true,now()-interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	r := NewDBRepository()
	svc := NewService(r, nil)
	now := time.Now().UTC()
	updates := []ProgressUpdate{
		{NovelID: 1, ChapterIndex: 5, ScrollOffsetPercentage: .75, UpdatedAt: now},
		{NovelID: 1, ChapterIndex: 3, UpdatedAt: now.Add(-time.Minute)},
		{NovelID: 2, ChapterIndex: 8, UpdatedAt: now},
	}
	if err := svc.SyncProgress(ctx, 1, updates); err != nil {
		t.Fatal(err)
	}
	first, err := r.GetProgress(ctx, 1, 1)
	if err != nil || first == nil || first.ChapterIndex != 5 || first.ScrollOffsetPercentage != .75 || !first.InShelf {
		t.Fatalf("progress/collection changed incorrectly: %+v %v", first, err)
	}
	second, err := r.GetProgress(ctx, 1, 2)
	if err != nil || second == nil || second.ChapterIndex != 8 || second.InShelf {
		t.Fatalf("new progress: %+v %v", second, err)
	}
	if err := svc.SyncProgress(ctx, 1, []ProgressUpdate{
		{NovelID: 1, ChapterIndex: 10, UpdatedAt: now.Add(time.Minute)},
		{NovelID: 99, ChapterIndex: 10, UpdatedAt: now},
	}); err == nil {
		t.Fatal("expected foreign key error")
	}
	first, err = r.GetProgress(ctx, 1, 1)
	if err != nil || first == nil || first.ChapterIndex != 5 {
		t.Fatalf("partially committed failing batch: %+v %v", first, err)
	}
}

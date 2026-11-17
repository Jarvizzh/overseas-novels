package novel

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"reader-backend/internal/testdb"
)

func TestContentGenerationFollowsCommit(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO novels(id,title,author) VALUES(1,'Book','Author')`); err != nil {
		t.Fatal(err)
	}
	r := NewDBRepository()
	before, err := r.GetContentVersion(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO chapters(id,novel_id,chapter_index,title,content) VALUES('chapter',1,0,'Chapter','text')`); err != nil {
		t.Fatal(err)
	}
	during, err := r.GetContentVersion(ctx, 1)
	if err != nil || during != before {
		t.Fatal("uncommitted version visible", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := r.GetContentVersion(ctx, 1)
	if err != nil || after <= before {
		t.Fatal("chapter commit did not advance generation", err)
	}
	if _, err := p.Exec(ctx, `UPDATE novels SET publish_status='offline' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if v, err := r.GetContentVersion(ctx, 1); err != nil || v != 0 {
		t.Fatal("offline content remains published", err)
	}
}

func TestSharedLoadSurvivesCallerCancellationAndBoundsDistinctMisses(t *testing.T) {
	s := NewService(nil, nil, nil, nil).(*service)
	base, cancelBase := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelBase()
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{}, cap(s.loads))
	load := func(ctx context.Context) (any, error) {
		started <- struct{}{}
		select {
		case <-release:
			return "body", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	first, cancelFirst := context.WithCancel(base)
	owner := make(chan error, 1)
	go func() { _, err := s.load(first, "hot", load); owner <- err }()
	<-started
	cancelFirst()
	if err := <-owner; !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller did not exit on cancellation: %v", err)
	}
	waiter, cancelWaiter := context.WithTimeout(base, 15*time.Millisecond)
	defer cancelWaiter()
	if _, err := s.load(waiter, "hot", load); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter did not exit at its own deadline: %v", err)
	}
	for i := 1; i < cap(s.loads); i++ {
		go func() {
			_, _ = s.load(base, strconv.Itoa(i), load)
		}()
		<-started
	}
	if _, err := s.load(base, "new", load); !errors.Is(err, ErrContentBusy) {
		t.Fatalf("repeated cold misses exceeded the fallback limit: %v", err)
	}
}

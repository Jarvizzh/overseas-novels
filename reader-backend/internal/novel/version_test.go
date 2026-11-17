package novel

import (
	"context"
	"sync"
	"testing"

	"reader-backend/internal/model"
	"reader-backend/internal/testdb"
)

type changingVersionRepo struct {
	*fakeReaderRepo
	mu      sync.Mutex
	version int64
}

func (r *changingVersionRepo) GetContentVersion(context.Context, int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.version, nil
}
func (r *changingVersionRepo) GetByID(ctx context.Context, id int64) (*model.Novel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fakeReaderRepo.GetByID(ctx, id)
}

type versionedTestCache struct {
	fakeReaderCache
	mu               sync.Mutex
	novels           map[int64]*model.Novel
	started, release chan struct{}
	once             sync.Once
}

func (c *versionedTestCache) GetNovel(ctx context.Context, _ int64) (*model.Novel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.novels[contentVersion(ctx)], nil
}
func (c *versionedTestCache) SetNovel(ctx context.Context, _ int64, n *model.Novel) error {
	c.once.Do(func() { close(c.started); <-c.release })
	c.mu.Lock()
	defer c.mu.Unlock()
	c.novels[contentVersion(ctx)] = n
	return nil
}
func TestLateCacheFillCannotReplaceNewGeneration(t *testing.T) {
	repo := &changingVersionRepo{fakeReaderRepo: &fakeReaderRepo{novels: []model.Novel{{ID: 1, Title: "Old", Status: "Ongoing", PublishStatus: "published", ContentVersion: 1}}}, version: 1}
	cache := &versionedTestCache{novels: make(map[int64]*model.Novel), started: make(chan struct{}), release: make(chan struct{})}
	svc := NewService(repo, cache, nil, nil)
	done := make(chan error, 1)
	go func() { _, err := svc.GetNovelDetail(context.Background(), 1); done <- err }()
	<-cache.started
	repo.mu.Lock()
	repo.version = 2
	repo.novels[0].Title = "New"
	repo.novels[0].ContentVersion = 2
	repo.mu.Unlock()
	close(cache.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetNovelDetail(context.Background(), 1)
	if err != nil || got.Title != "New" {
		t.Fatalf("late fill restored old content: %+v %v", got, err)
	}
}
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

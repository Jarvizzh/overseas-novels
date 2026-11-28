package novel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"star-novel-cms/internal/db"
	"star-novel-cms/internal/storage"
	"star-novel-cms/internal/testdb"
)

func publicationDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testdb.Initialized(t)
	previous := db.DB
	db.DB = pool
	t.Cleanup(func() { db.DB = previous })
	return pool
}

type controlledRevisionStore struct {
	fakeOSSContentStorage
	beforeWrite func(context.Context, string) error
}

func (s *controlledRevisionStore) WriteChapterRevision(ctx context.Context, key, hash, body string) error {
	if s.beforeWrite != nil {
		if err := s.beforeWrite(ctx, body); err != nil {
			return err
		}
	}
	return s.fakeOSSContentStorage.WriteChapterRevision(ctx, key, hash, body)
}

func (s *controlledRevisionStore) ReadChapterRevision(ctx context.Context, key, hash string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, err := s.fakeOSSContentStorage.ReadChapterRevision(ctx, key, hash)
	if err == nil && chapterDigest(body) != hash {
		return "", errors.New("revision digest mismatch")
	}
	return body, err
}

type failingCommitRepo struct{ NovelRepository }
type failingCommitTx struct{ pgx.Tx }

func (r failingCommitRepo) BeginTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := r.NovelRepository.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	return failingCommitTx{tx}, nil
}

func (t failingCommitTx) Commit(ctx context.Context) error {
	return errors.Join(errors.New("simulated commit failure"), t.Tx.Rollback(ctx))
}

func TestPublicationIsAtomicAcrossStorageAndCatalog(t *testing.T) {
	pool := publicationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `
		INSERT INTO novels(id,title,author,cover_url,synopsis,coin_cost_per_thousand,start_pay_chapter_index)
		VALUES (1,'Book','Author','','',500,1)`); err != nil {
		t.Fatal(err)
	}
	repo := NewNovelRepository()
	store := &controlledRevisionStore{}
	service := NewNovelService(repo, store, nil)
	original, err := service.CreateChapter(ctx, 1, 0, "Original", "Old body")
	if err != nil {
		t.Fatal(err)
	}
	readOriginal := func(wantVersion int64) {
		t.Helper()
		got, err := service.GetChapterDetail(ctx, 1, 0)
		if err != nil || got == nil || got.Content != "Old body" || got.Title != "Original" || got.ID != original.ID {
			t.Fatalf("unexpected old snapshot: %+v %v", got, err)
		}
		book, err := repo.GetNovel(ctx, 1)
		if err != nil || book.ContentVersion != wantVersion || book.WordCount != original.WordCount {
			t.Fatalf("unexpected old book snapshot: %+v %v", book, err)
		}
	}
	book, err := repo.GetNovel(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	version := book.ContentVersion
	failure := NewNovelService(failingCommitRepo{repo}, store, nil)
	if err := failure.UpdateChapter(ctx, 1, 0, "New title", "New body", original.ID); err == nil {
		t.Fatal("commit failure accepted")
	}
	readOriginal(version)

	// Let one successful upload complete before injecting the failure on the other.
	store.beforeWrite = func(ctx context.Context, body string) error {
		if body != "Partial upload" {
			return nil
		}
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			store.mu.Lock()
			uploadedFirst := false
			for _, uploaded := range store.revisions {
				if uploaded == "First upload" {
					uploadedFirst = true
					break
				}
			}
			store.mu.Unlock()
			if uploadedFirst {
				return errors.New("second upload failed")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
	manuscript := []byte("Chapter 1: Original\nFirst upload\nChapter 2: Extra\nPartial upload")
	if _, err := service.BulkImportChapters(ctx, 1, manuscript, "single_txt", "", ChapterImportMapping{Version: version, Keep: map[int]string{1: original.ID}}); err == nil {
		t.Fatal("partial upload published")
	}
	readOriginal(version)
	var staged int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM chapter_content_objects WHERE state='staged'").Scan(&staged); err != nil || staged != 3 {
		t.Fatalf("orphaned objects not durably tracked: %d %v", staged, err)
	}

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	store.beforeWrite = func(ctx context.Context, _ string) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, body := range []string{"One newer body", "Another newer body"} {
		workers.Go(func() {
			_, err := service.BulkImportChapters(ctx, 1, []byte("Chapter 1: Original\n"+body), "single_txt", "",
				ChapterImportMapping{Version: version, Keep: map[int]string{1: original.ID}})
			results <- err
		})
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	readOriginal(version)
	close(release)
	workers.Wait()
	err1, err2 := <-results, <-results
	if !((err1 == nil && errors.Is(err2, ErrPublicationChanged)) || (err2 == nil && errors.Is(err1, ErrPublicationChanged))) {
		t.Fatalf("expect one publication and one rejected version, got %v, %v", err1, err2)
	}
	got, err := service.GetChapterDetail(ctx, 1, 0)
	if err != nil || got.ID != original.ID || got.WordCount != countWords(got.Content) || got.ContentHash != chapterDigest(got.Content) || got.ObjectKey == original.ObjectKey {
		t.Fatalf("published metadata mismatch: %+v %v", got, err)
	}
	published, err := repo.GetNovel(ctx, 1)
	if err != nil || published.ContentVersion != version+1 {
		t.Fatalf("bulk publication must advance the version once: %+v %v", published, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE chapters SET price=price+1 WHERE novel_id=1; UPDATE novels SET view_count=view_count+1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	priced, err := repo.GetNovel(ctx, 1)
	if err != nil || priced.ContentVersion != published.ContentVersion {
		t.Fatalf("price and views must preserve catalog version: %+v %v", priced, err)
	}

	var state string
	if err := pool.QueryRow(ctx, "SELECT state FROM chapter_content_objects WHERE object_key=$1", original.ObjectKey).Scan(&state); err != nil || state != "retired" {
		t.Fatalf("old object was not queued for cleanup: %q %v", state, err)
	}
}

func TestChapterIdentitySurvivesImportMovesRetirementAndSplits(t *testing.T) {
	pool := publicationDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO novels(id,title,author,cover_url,synopsis,start_pay_chapter_index) VALUES (1,'Book','Author','','',1);
		INSERT INTO users(id) VALUES (1),(2)`); err != nil {
		t.Fatal(err)
	}
	repo := NewNovelRepository()
	service := NewNovelService(repo, nil, nil)
	a, err := service.CreateChapter(ctx, 1, 0, "Same title", "First body")
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.CreateChapter(ctx, 1, 1, "Same title", "Second body")
	if err != nil {
		t.Fatal(err)
	}
	buy := func(user int, id string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO unlock_records(user_id,novel_id,chapter_index,chapter_id,identity_evidence,identity_resolved_at)
			VALUES ($1,1,1,$2,'test purchase',now())`, user, id); err != nil {
			t.Fatal(err)
		}
	}
	assertAccess := func(user int, id string, want bool) {
		t.Helper()
		var got bool
		if err := pool.QueryRow(ctx, "SELECT reader_has_chapter_unlock($1,$2)", user, id).Scan(&got); err != nil || got != want {
			t.Fatalf("access for %d to %s = %v, want %v (%v)", user, id, got, want, err)
		}
	}
	buy(1, b.ID)
	buy(2, a.ID)
	if _, err := service.BulkImportChapters(ctx, 1, []byte("Chapter 1: Preface\nNew preface\nChapter 2: Renamed\nFirst body\nChapter 3: Same title\nSecond body"), "single_txt", ""); err != nil {
		t.Fatal(err)
	}
	chapters, err := service.ListChapters(ctx, 1)
	if err != nil || len(chapters) != 3 || chapters[1].ID != a.ID || chapters[2].ID != b.ID {
		t.Fatalf("positions did not retain chapter identities: %+v %v", chapters, err)
	}
	assertAccess(1, a.ID, false)
	assertAccess(1, b.ID, true)
	preface := chapters[0].ID

	variant := []byte("Chapter 1: Preface\nNew preface\nChapter 2: Part 1\nFirst part\nChapter 3: Part 2\nSecond part")
	if _, err := service.BulkImportChapters(ctx, 1, variant, "single_txt", ""); !errors.Is(err, ErrChapterMapping) {
		t.Fatalf("ambiguous replacement was accepted: %v", err)
	}
	book, err := repo.GetNovel(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.BulkImportChapters(ctx, 1, variant, "single_txt", "", ChapterImportMapping{
		Version: book.ContentVersion, Keep: map[int]string{2: b.ID}, Retire: []string{a.ID}, SplitFrom: map[int]string{3: b.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	chapters, err = service.ListChapters(ctx, 1)
	if err != nil || len(chapters) != 3 || chapters[0].ID != preface || chapters[1].ID != b.ID || chapters[2].ID == b.ID {
		t.Fatalf("incorrect split catalog: %+v %v", chapters, err)
	}
	split := chapters[2].ID
	buy(2, b.ID)
	assertAccess(1, split, true)
	assertAccess(2, split, false)

	if _, err := service.BulkImportChapters(ctx, 1, []byte("Chapter 1: Preface\nNew preface\nChapter 2: Restored\nFirst body\nChapter 3: Part 1\nFirst part\nChapter 4: Part 2\nSecond part"), "single_txt", ""); err != nil {
		t.Fatal(err)
	}
	chapters, err = service.ListChapters(ctx, 1)
	if err != nil || len(chapters) != 4 || chapters[1].ID != a.ID || chapters[3].ID != split {
		t.Fatalf("retired identity was not restored: %+v %v", chapters, err)
	}
	assertAccess(2, a.ID, true)
}

func TestCleanupFencesExpiredObjectsBeforeExternalDelete(t *testing.T) {
	pool := publicationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const expired = "revisions/chapter/expired.txt"
	const current = "revisions/chapter/current.txt"
	const newer = "revisions/chapter/newer.txt"
	if _, err := pool.Exec(ctx, `
		INSERT INTO novels(id,title,author) VALUES(1,'Book','Author');
		INSERT INTO chapter_content_objects(object_key,chapter_id,sha256)
		VALUES ('revisions/chapter/current.txt','chapter','current'),
		       ('revisions/chapter/newer.txt','chapter','newer'),
		       ('revisions/chapter/expired.txt','chapter','expired');
		INSERT INTO chapters(id,novel_id,chapter_index,title,content,content_object_key,content_sha256)
		VALUES ('chapter',1,0,'Chapter','','revisions/chapter/current.txt','current');
		UPDATE chapter_content_objects SET next_attempt_at=now()-interval '1 hour' WHERE sha256='expired'`); err != nil {
		t.Fatal(err)
	}
	arrived := make(chan string, 1)
	release := make(chan struct{})
	var released sync.Once
	resume := func() { released.Do(func() { close(release) }) }
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- r.Method + " " + r.URL.Path
		select {
		case <-release:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	defer httpServer.Close()
	defer resume()
	store, err := storage.NewAliyunOSSStorage(httpServer.URL, "test-id", "test-secret", "test-bucket", "novels", nil)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		store.RunChapterCleanup(workerCtx, pool)
	}()
	defer func() {
		resume()
		stopWorker()
		<-done
	}()
	select {
	case request := <-arrived:
		if !strings.HasPrefix(request, "DELETE ") || !strings.HasSuffix(request, expired) {
			t.Fatalf("wrong object selected for deletion: %s", request)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	update := "UPDATE chapters SET content_object_key=$1, content_sha256=$2 WHERE id='chapter'"
	if _, err := pool.Exec(ctx, update, expired, "expired"); err == nil {
		t.Fatal("cleanup and publish both claimed the same object")
	}
	if _, err := pool.Exec(ctx, update, newer, "newer"); err != nil {
		t.Fatal(err)
	}
	resume()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var oldState, newState, expiredState string
		if err := pool.QueryRow(ctx, `SELECT
			(SELECT state FROM chapter_content_objects WHERE object_key=$1),
			(SELECT state FROM chapter_content_objects WHERE object_key=$2),
			(SELECT state FROM chapter_content_objects WHERE object_key=$3)`, current, newer, expired).
			Scan(&oldState, &newState, &expiredState); err != nil {
			t.Fatal(err)
		}
		if oldState == "retired" && newState == "published" && expiredState == "deleted" {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("cleanup states: %s %s %s (%v)", oldState, newState, expiredState, ctx.Err())
		}
	}
}

func TestBatchImportAndBatchPublishPostgresIntegration(t *testing.T) {
	pool := publicationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, `
		INSERT INTO system_configs (key, value) VALUES
			('global_coin_cost_per_thousand', '500'),
			('global_start_pay_chapter_index', '3')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}

	repo := NewNovelRepository()
	store := &controlledRevisionStore{}
	service := NewNovelService(repo, store, fakeCoverStore{})

	// 1. Atomic batch import persists novel, chapters, word count, timestamps, and published revision objects
	res, err := service.BatchImportNovels(ctx, []BatchImportNovelInput{
		{
			Title:                "PG Batch Novel",
			Author:               "Author PG",
			CoverURL:             "oss://covers/2026/11/abc.png",
			StartPayChapterIndex: 2,
			ChapterBytes:         []byte("Chapter 1: Opening\nFirst chapter free content\nChapter 2: Climax\nSecond chapter paid content here with extra words"),
		},
	})
	if err != nil || res.CreatedCount != 1 || res.FailedCount != 0 {
		t.Fatalf("expected 1 created batch novel, got res=%+v err=%v", res, err)
	}
	created := res.Results[0].Novel
	if created.PublishStatus != "draft" || created.WordCount <= 0 || created.UpdatedAt.IsZero() {
		t.Fatalf("unexpected created novel metadata: %+v", created)
	}
	var chCount, paidCount, pubObjects int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM chapters WHERE novel_id=$1 AND chapter_index IS NOT NULL),
			(SELECT COUNT(*) FROM chapters WHERE novel_id=$1 AND chapter_index IS NOT NULL AND is_paid=true AND price>0),
			(SELECT COUNT(*) FROM chapter_content_objects o JOIN chapters c ON c.content_object_key=o.object_key WHERE c.novel_id=$1 AND o.state='published')`,
		created.ID).Scan(&chCount, &paidCount, &pubObjects); err != nil {
		t.Fatal(err)
	}
	if chCount != 2 || paidCount != 1 || pubObjects != 2 {
		t.Fatalf("unexpected postgres persisted state: ch=%d paid=%d pubObjects=%d", chCount, paidCount, pubObjects)
	}

	// 2. Simulated commit failure rolls back novel and chapters atomically (no empty orphan novel)
	failService := NewNovelService(failingCommitRepo{repo}, store, fakeCoverStore{})
	failRes, err := failService.BatchImportNovels(ctx, []BatchImportNovelInput{
		{
			Title:        "Should Rollback",
			Author:       "Author Fail",
			CoverURL:     "oss://covers/2026/11/abc.png",
			ChapterBytes: []byte("Chapter 1\nBody"),
		},
	})
	if err != nil || failRes.FailedCount != 1 || failRes.CreatedCount != 0 {
		t.Fatalf("expected commit failure reported per item, got res=%+v err=%v", failRes, err)
	}
	var totalNovels int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM novels").Scan(&totalNovels); err != nil || totalNovels != 1 {
		t.Fatalf("expected rollback to leave 1 novel total, got %d (err=%v)", totalNovels, err)
	}

	// 3. BatchUpdatePublishStatus must NOT allow publishing a novel whose chapters are all retired (chapter_index IS NULL)
	var retiredNovelID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO novels(title, author, cover_url, status, publish_status)
		VALUES ('Retired Only', 'Author', 'oss://covers/2026/11/abc.png', 'Ongoing', 'draft')
		RETURNING id`).Scan(&retiredNovelID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO chapters(id, novel_id, chapter_index, title, content, word_count)
		VALUES ('retired-ch-1', $1, NULL, 'Old Ch', 'text', 4)`, retiredNovelID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BatchUpdatePublishStatus(ctx, []int64{retiredNovelID}, "published"); !errors.Is(err, ErrCannotPublishEmptyNovel) {
		t.Fatalf("expected ErrCannotPublishEmptyNovel when novel only has retired chapters, got %v", err)
	}

	// BatchUpdatePublishStatus succeeds for novel with active chapters
	pubRes, err := service.BatchUpdatePublishStatus(ctx, []int64{created.ID, retiredNovelID}, "published")
	if err != nil || pubRes.UpdatedCount != 1 || len(pubRes.Skipped) != 1 {
		t.Fatalf("expected 1 published and 1 skipped, got res=%+v err=%v", pubRes, err)
	}
}

func TestHistoricalOSSPreviewBackfillIsBoundedAndIdempotent(t *testing.T) {
	pool := publicationDB(t)
	ctx := context.Background()
	body := strings.Repeat("字", 700)
	if _, err := pool.Exec(ctx, `
		INSERT INTO novels(id,title,author) VALUES(1,'Book','Author');
		INSERT INTO chapters(id,novel_id,chapter_index,title,content)
		VALUES('legacy-oss',1,0,'Legacy',''),('legacy-inline',1,1,'Inline','inline body')`); err != nil {
		t.Fatal(err)
	}
	var gets atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/novels/1/0.txt") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gets.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	ossStore, err := storage.NewAliyunOSSStorage(server.URL, "key", "secret", "test-bucket", "novels", nil)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := pool.QueryRow(ctx, "SELECT content_version FROM novels WHERE id=1").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ossStore.BackfillChapterPreviews(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var preview string
	err = pool.QueryRow(ctx, `SELECT c.content_preview,n.content_version FROM chapters c
		JOIN novels n ON n.id=c.novel_id WHERE c.id='legacy-oss'`).Scan(&preview, &after)
	if err != nil || preview != strings.Repeat("字", 250)+"..." || after != before || gets.Load() != 1 {
		t.Fatalf("preview=%q version=%d/%d OSS reads=%d error=%v", preview, before, after, gets.Load(), err)
	}
}

func TestOSSReimportReusesPublishedRevisionsWithoutRewritingChapters(t *testing.T) {
	pool := publicationDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO novels(id,title,author,cover_url,synopsis,start_pay_chapter_index,coin_cost_per_thousand)
		VALUES(1,'Book','Author','','',1,500)`); err != nil {
		t.Fatal(err)
	}
	var puts atomic.Int64
	store := &controlledRevisionStore{beforeWrite: func(context.Context, string) error { puts.Add(1); return nil }}
	repo := NewNovelRepository()
	service := NewNovelService(repo, store, nil)
	manuscript := []byte("Chapter 1: First\nFirst body\nChapter 2: Second\nSecond body")
	if _, err := service.BulkImportChapters(ctx, 1, manuscript, "single_txt", ""); err != nil {
		t.Fatal(err)
	}
	type revision struct{ id, key, xmin string }
	revisions := func() []revision {
		t.Helper()
		rows, err := pool.Query(ctx, "SELECT id,content_object_key,xmin::text FROM chapters WHERE novel_id=1 ORDER BY chapter_index")
		if err != nil {
			t.Fatal(err)
		}
		result, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (revision, error) {
			var item revision
			err := row.Scan(&item.id, &item.key, &item.xmin)
			return item, err
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	checkObjects := func(total, published, retired, staged int) {
		t.Helper()
		var counts [4]int
		err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE state='published'),
			count(*) FILTER(WHERE state='retired'),count(*) FILTER(WHERE state='staged')
			FROM chapter_content_objects`).Scan(&counts[0], &counts[1], &counts[2], &counts[3])
		if err != nil || counts != [4]int{total, published, retired, staged} {
			t.Fatalf("objects=%v error=%v", counts, err)
		}
	}
	before := revisions()
	if _, err := service.BulkImportChapters(ctx, 1, manuscript, "single_txt", ""); err != nil {
		t.Fatal(err)
	}
	after := revisions()
	if puts.Load() != 2 || before[0] != after[0] || before[1] != after[1] {
		t.Fatalf("same import rewrote objects or rows: puts=%d before=%+v after=%+v", puts.Load(), before, after)
	}
	checkObjects(2, 2, 0, 0)
	moved := []byte("Chapter 1: Renamed second\nSecond body\nChapter 2: Renamed first\nFirst body")
	if _, err := service.BulkImportChapters(ctx, 1, moved, "single_txt", ""); err != nil {
		t.Fatal(err)
	}
	after = revisions()
	if puts.Load() != 2 || after[0].key != before[1].key || after[1].key != before[0].key {
		t.Fatalf("move or rename uploaded body: %+v puts=%d", after, puts.Load())
	}
	checkObjects(2, 2, 0, 0)
	book, err := repo.GetNovel(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	mapping := ChapterImportMapping{Version: book.ContentVersion, Keep: map[int]string{1: after[0].id}}
	edited := []byte("Chapter 1: Renamed second\nEdited second body\nChapter 2: Renamed first\nFirst body")
	if _, err := service.BulkImportChapters(ctx, 1, edited, "single_txt", "", mapping); err != nil {
		t.Fatal(err)
	}
	newer := revisions()
	if puts.Load() != 3 || newer[0].key == after[0].key || newer[1] != after[1] {
		t.Fatalf("single edit rewrote unchanged body: puts=%d after=%+v before=%+v", puts.Load(), newer, after)
	}
	checkObjects(3, 2, 1, 0)
	book, err = repo.GetNovel(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	mapping.Version = book.ContentVersion
	failed := NewNovelService(failingCommitRepo{repo}, store, nil)
	if _, err := failed.BulkImportChapters(ctx, 1, moved, "single_txt", "", mapping); err == nil {
		t.Fatal("injected commit failure was accepted")
	}
	checkObjects(4, 2, 1, 1)
	failedRows := revisions()
	if failedRows[0] != newer[0] || failedRows[1] != newer[1] {
		t.Fatal("failed mixed publication changed online references")
	}

	// Hold deletion of an old retired object while both importers reuse the
	// current published object. Published references must remain protected.
	if _, err := pool.Exec(ctx, "UPDATE chapter_content_objects SET next_attempt_at=now()-interval '1 hour' WHERE state='retired'"); err != nil {
		t.Fatal(err)
	}
	deleteStarted := make(chan struct{})
	releaseDelete := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseDelete) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		close(deleteStarted)
		select {
		case <-releaseDelete:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer release()
	cleaner, err := storage.NewAliyunOSSStorage(server.URL, "test", "test", "bucket", "novels", nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanupCtx, stop := context.WithCancel(ctx)
	cleanupDone := make(chan struct{})
	go func() { defer close(cleanupDone); cleaner.RunChapterCleanup(cleanupCtx, pool) }()
	defer func() { release(); stop(); <-cleanupDone }()
	select {
	case <-deleteStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not claim retired object")
	}
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := service.BulkImportChapters(ctx, 1, edited, "single_txt", "", ChapterImportMapping{Version: book.ContentVersion})
			done <- err
		}()
	}
	success, conflict := 0, 0
	for range 2 {
		err := <-done
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrPublicationChanged):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 || puts.Load() != 4 {
		t.Fatalf("concurrent reuse success=%d conflict=%d puts=%d", success, conflict, puts.Load())
	}
	release()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var deleted int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM chapter_content_objects WHERE state='deleted'").Scan(&deleted); err != nil {
			t.Fatal(err)
		}
		if deleted == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	checkObjects(4, 2, 0, 1)
}

package novel

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/storage"
	"reader-backend/internal/testdb"
)

type queryCount struct{ count atomic.Int64 }

func (q *queryCount) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	q.count.Add(1)
	return ctx
}
func (*queryCount) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type snapshotStorage struct {
	storage.ContentStorage
	loads atomic.Int64
}

func (s *snapshotStorage) ReadChapterRevision(_ context.Context, _, _ string) (string, error) {
	s.loads.Add(1)
	return strings.Repeat("chapter body ", 100), nil
}

type snapshotCache struct {
	fakeReaderCache
	bodies map[string]string
}

func (c *snapshotCache) GetBody(_ context.Context, key string) (string, error) {
	return c.bodies[key], nil
}
func (c *snapshotCache) SetBody(_ context.Context, key, body string) error {
	c.bodies[key] = body
	return nil
}

func TestReadingSnapshotChecksLiveAccessBeforeLoadingBody(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(1),(2),(3);
		INSERT INTO reader_sessions(token_hash,user_id,expires_at) VALUES('reader',1,now()+interval '1 day');
		INSERT INTO novels(id,title,author,coin_cost_per_thousand,start_pay_chapter_index) VALUES(1,'Book','Writer',50,1);
		INSERT INTO chapter_content_objects(object_key,chapter_id,sha256) VALUES('revision','chapter','fixture-hash');
		INSERT INTO chapters(id,novel_id,chapter_index,title,word_count,content,content_object_key,content_sha256,content_preview)
		VALUES('chapter',1,0,'Chapter',1000,'','revision','fixture-hash','preview...');`)
	q := &queryCount{}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = q
	tracked, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	db.DB = tracked
	t.Cleanup(func() { db.DB = pool; tracked.Close() })

	store := &snapshotStorage{}
	cache := &snapshotCache{bodies: make(map[string]string)}
	service := NewService(NewDBRepository(), cache, store, nil)
	read := func(reader int64, request context.Context, wantAccess string) *ChapterReadResult {
		t.Helper()
		q.count.Store(0)
		body, err := service.GetChapterContent(request, reader, 1, 0, 0, "", "")
		if err != nil || body == nil || body.AccessType != wantAccess || q.count.Load() != 1 {
			t.Fatalf("reading access=%s result=%+v SQL=%d error=%v", wantAccess, body, q.count.Load(), err)
		}
		return body
	}
	if preview := read(0, ctx, ""); !preview.Locked || preview.Chapter.Content != "preview..." || store.loads.Load() != 0 {
		t.Fatalf("anonymous preview reached full storage: %+v", preview)
	}
	expected := "1"
	identified := context.WithValue(ctx, readIdentityKey{}, readIdentity{sessionHash: "reader", expected: &expected})
	if !read(1, identified, "").Locked {
		t.Fatal("unpaid reader got full body")
	}
	exec(`INSERT INTO user_subscriptions(user_id,subscription_id,status,cycle,price_cents,current_period_end,last_payment_time)
		VALUES(1,'I-VIP','ACTIVE','month',100,now()+interval '1 day',now());
		INSERT INTO recharge_orders(user_id,subscription_id,payment_method,amount_cents,status,order_type,
		    paid_period_start,paid_period_end) VALUES(1,'I-VIP','paypal',100,'Success','subscription',now()-interval '1 day',now()+interval '1 day');`)
	testdb.ScopeSubscriptions(t, pool, "paypal", "live", "I-VIP")
	result := read(1, identified, "pass")
	if result.Locked || result.Price != 0 || store.loads.Load() != 1 {
		t.Fatalf("wrong VIP result: %+v", result)
	}
	read(1, identified, "pass")
	exec(`UPDATE recharge_orders SET paypal_refund_reported=true WHERE subscription_id='I-VIP'`)
	if preview := read(1, identified, ""); preview.Chapter.Content != "preview..." || store.loads.Load() != 1 {
		t.Fatal("refunded VIP received cached full body")
	}
	exec(`INSERT INTO unlock_records(user_id,novel_id,chapter_index,chapter_id,identity_evidence,identity_resolved_at)
		  VALUES(1,1,0,'chapter','fixture',now())`)
	read(1, identified, "chapter")
	if !read(2, ctx, "").Locked {
		t.Fatal("a different reader inherited the cached entitlement")
	}
	exec(`UPDATE novels SET view_count=view_count+1 WHERE id=1;
		INSERT INTO chapters(id,novel_id,chapter_index,title,content) VALUES('another',1,1,'Other','inline')`)
	read(1, identified, "chapter")
	if store.loads.Load() != 1 {
		t.Fatal("changing a different chapter invalidated the body digest cache")
	}

	exec(`INSERT INTO recharge_orders(user_id,novel_id,amount_cents,payment_method,status,order_type,checkout_id,fulfilled_at,checkout_environment)
		VALUES(3,1,999,'paypal','Success','whole_book',gen_random_uuid(),now(),'sandbox')`)
	if result := read(3, ctx, ""); !result.Locked {
		t.Fatal("sandbox purchase unlocked the production reading snapshot")
	}
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	if result := read(3, ctx, "pass"); result.Locked {
		t.Fatal("paid whole-book reader was blocked")
	}
	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	exec(`UPDATE reader_sessions SET expires_at=now()-interval '1 second' WHERE token_hash='reader'`)
	if _, err := service.GetChapterContent(identified, 1, 1, 0, 0, "", ""); !errors.Is(err, errReadSessionExpired) {
		t.Fatalf("expired session retained cached full access: %v", err)
	}
	exec(`UPDATE reader_sessions SET expires_at=now()+interval '1 day' WHERE token_hash='reader'`)
	exec(`UPDATE users SET status=2 WHERE id=1`)
	if _, err := service.GetChapterContent(identified, 1, 1, 0, 0, "", ""); !errors.Is(err, errReadAccountUnavailable) {
		t.Fatalf("banned reader got access after cache warmup: %v", err)
	}
	exec(`UPDATE novels SET publish_status='offline' WHERE id=1`)
	q.count.Store(0)
	for range 3 {
		if body, err := service.GetChapterContent(ctx, 0, 1, 0, 0, "", ""); err != nil || body != nil {
			t.Fatalf("offline chapter available: %+v %v", body, err)
		}
	}
	if q.count.Load() != 1 {
		t.Fatalf("confirmed miss made %d queries", q.count.Load())
	}
}

func TestChapterBodyCacheKeyUsesBodyIdentity(t *testing.T) {
	first := model.Chapter{ID: "first", NovelID: 1, ChapterIndex: 0, BodyRevision: 4, ContentHash: "digest"}
	second := model.Chapter{ID: "second", NovelID: 2, ChapterIndex: 8, ContentHash: "digest"}
	if bodyCacheKey(&first) != bodyCacheKey(&second) {
		t.Fatal("same verified content must have the same cache key")
	}
	first.ContentHash, second.ContentHash = "", ""
	if bodyCacheKey(&first) == bodyCacheKey(&second) {
		t.Fatal("distinct legacy chapters share a cache key")
	}
}

func TestInlineChapterEditChangesBodyCacheIdentity(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	exec := func(query string) {
		t.Helper()
		if _, err := pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO novels(id,title,author,coin_cost_per_thousand,start_pay_chapter_index) VALUES(1,'Book','Writer',0,1);
		INSERT INTO chapters(id,novel_id,chapter_index,title,content,word_count,content_sha256,content_preview)
		VALUES('inline',1,0,'Chapter','Old full body',13,'initial-digest','Old...')`)
	cache := &snapshotCache{bodies: make(map[string]string)}
	service := NewService(NewDBRepository(), cache, nil, nil)
	before, err := service.GetChapterContent(ctx, 0, 1, 0, 0, "", "")
	if err != nil || before.Chapter.Content != "Old full body" {
		t.Fatalf("initial inline body: %+v %v", before, err)
	}
	exec(`UPDATE chapters SET content='New full body' WHERE id='inline'`)
	after, err := service.GetChapterContent(ctx, 0, 1, 0, 0, "", "")
	if err != nil || after.Chapter.Content != "New full body" || after.ContentVersion != before.ContentVersion+1 {
		t.Fatalf("inline edit returned cached body: %+v %v", after, err)
	}
	var preview, hash *string
	if err := pool.QueryRow(ctx, "SELECT content_sha256,content_preview FROM chapters WHERE id='inline'").Scan(&hash, &preview); err != nil || hash != nil || preview != nil {
		t.Fatalf("direct edit kept stale hash or preview: %v %v %v", hash, preview, err)
	}
}

package wallet

import (
	"context"
	"errors"
	"sync"
	"testing"

	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/testdb"
)

func TestChapterPurchaseLocksPublicationAndUsesStableIdentity(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO novels(id,title,author,cover_url,synopsis,start_pay_chapter_index,coin_cost_per_thousand)
		VALUES(1,'Book','Author','','',1,500);
		INSERT INTO users(id) VALUES(1);
		INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(1,1000,0);
		INSERT INTO chapters(id,novel_id,chapter_index,title,content,word_count)
		VALUES('chapter-a',1,0,'A','First body',1000),('chapter-b',1,1,'B','Second body',1000)`)

	nr := novel.NewDBRepository()
	repo := &dbRepository{}
	version, err := nr.GetContentVersion(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	oldQuote := ChapterQuote{ID: "chapter-b", Version: version, Price: 500}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM novels WHERE id=1 FOR UPDATE;
		UPDATE chapters SET chapter_index=NULL WHERE id='chapter-b';
		UPDATE chapters SET chapter_index=1 WHERE id='chapter-a';
		UPDATE chapters SET chapter_index=2 WHERE id='chapter-b'`); err != nil {
		t.Fatal(err)
	}
	purchase := make(chan error, 1)
	go func() { purchase <- repo.UnlockChapterTx(ctx, 1, 1, 1, 0, oldQuote) }()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-purchase; !errors.Is(err, ErrPriceChanged) {
		t.Fatalf("old quote was accepted after publication: %v", err)
	}

	version, err = nr.GetContentVersion(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	movedQuote := ChapterQuote{ID: "chapter-b", Version: version, Price: 500}
	wg := sync.WaitGroup{}
	results := make(chan error, 12)
	for range cap(results) {
		wg.Go(func() {
			results <- repo.UnlockChapterTx(ctx, 1, 1, 2, 0, movedQuote)
		})
	}
	wg.Wait()
	close(results)
	var paid int
	for err := range results {
		switch {
		case err == nil:
			paid++
		case !errors.Is(err, ErrAlreadyUnlocked):
			t.Fatalf("unexpected purchase failure: %v", err)
		}
	}
	for _, id := range []string{"chapter-a", "chapter-b"} {
		got, _, err := nr.CheckChapterAccess(ctx, 1, 1, id)
		if err != nil || got != (id == "chapter-b") {
			t.Fatalf("entitlement for %s: %t %v", id, got, err)
		}
	}
	var coins, debits int
	if err := pool.QueryRow(ctx, `SELECT charged_coins,(SELECT count(*) FROM transactions WHERE user_id=1 AND biz_type='unlock') FROM wallets WHERE user_id=1`).
		Scan(&coins, &debits); err != nil || paid != 1 || coins != 500 || debits != 1 {
		t.Fatalf("incorrect debit: paid=%d coins=%d debits=%d err=%v", paid, coins, debits, err)
	}

	// Re-pricing the publisher's global settings must invalidate the quote even
	// when the chapter version itself did not change.
	exec(`UPDATE novels SET coin_cost_per_thousand=NULL WHERE id=1;
		INSERT INTO system_configs(key,value) VALUES('global_coin_cost_per_thousand','300')`)
	version, err = nr.GetContentVersion(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	quote := ChapterQuote{ID: "chapter-a", Version: version, Price: 300}
	exec(`UPDATE system_configs SET value='350' WHERE key='global_coin_cost_per_thousand'`)
	if err := repo.UnlockChapterTx(ctx, 1, 1, 1, 0, quote); !errors.Is(err, ErrPriceChanged) {
		t.Fatalf("repriced chapter accepted: %v", err)
	}
}

func TestExplicitFreePricingSkipsDebitAndPreservesUnresolvedPurchases(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id) VALUES(1);
		INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(1,1000,0);
		INSERT INTO novels(id,title,author,cover_url,synopsis,start_pay_chapter_index,coin_cost_per_thousand)
		VALUES(1,'Book','Author','','',1,0);
		INSERT INTO chapters(id,novel_id,chapter_index,title,content,word_count)
		VALUES('chapter',1,0,'Chapter','Full free body',1000);
		-- Simulate an index-only purchase retained by the legacy migration.
		ALTER TABLE unlock_records DISABLE TRIGGER unlock_identity_guard;
		INSERT INTO unlock_records(user_id,novel_id,chapter_index) VALUES(1,1,8);
		SET CONSTRAINTS ALL IMMEDIATE;
		ALTER TABLE unlock_records ENABLE TRIGGER unlock_identity_guard`); err != nil {
		t.Fatal(err)
	}
	nr := novel.NewDBRepository()
	versions := func() int64 {
		t.Helper()
		version, err := nr.GetContentVersion(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		return version
	}
	cache := coldChapterCache{}
	reader := novel.NewService(nr, cache, nil, nil)
	wallet := NewService(&dbRepository{}, nr, cache, nil, nil)
	assertFree := func() {
		t.Helper()
		list, err := reader.GetChaptersList(ctx, 1, 1, 0, "", "")
		if err != nil || len(list) != 1 || list[0].IsPaid || list[0].Price != 0 {
			t.Fatalf("wrong free catalog: %+v %v", list, err)
		}
		body, err := reader.GetChapterContent(ctx, 1, 1, 0, 0, "", "")
		if err != nil || body == nil || body.Locked || body.Chapter.Content != "Full free body" || body.ContentVersion != versions() {
			t.Fatalf("wrong free content: %+v %v", body, err)
		}
		err = wallet.UnlockChapter(ctx, 1, 1, 0, 0, "", "", ChapterQuote{ID: "chapter", Version: versions(), Price: 0})
		if err != nil {
			t.Fatalf("free chapter attempted a purchase: %v", err)
		}
	}
	assertFree()
	if _, err := pool.Exec(ctx, "UPDATE novels SET coin_cost_per_thousand=NULL WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	body, err := reader.GetChapterContent(ctx, 1, 1, 0, 0, "", "")
	if err != nil || body == nil || !body.Locked || !body.PurchaseNeedsReview || body.Price != 500 {
		t.Fatalf("missing pending historical purchase state: %+v %v", body, err)
	}
	err = wallet.UnlockChapter(ctx, 1, 1, 0, 0, "", "", ChapterQuote{ID: "chapter", Version: versions(), Price: 500})
	if !errors.Is(err, ErrPurchaseNeedsReview) {
		t.Fatalf("unresolved historical purchase was charged again: %v", err)
	}
	var coins, debits int
	if err := pool.QueryRow(ctx, "SELECT charged_coins,(SELECT count(*) FROM transactions) FROM wallets WHERE user_id=1").Scan(&coins, &debits); err != nil || coins != 1000 || debits != 0 {
		t.Fatalf("unexpected debit: coins=%d debits=%d err=%v", coins, debits, err)
	}
}

type coldChapterCache struct{ novel.Cache }

func (coldChapterCache) GetNovel(context.Context, int64) (*model.Novel, error) { return nil, nil }
func (coldChapterCache) SetNovel(context.Context, int64, *model.Novel) error   { return nil }
func (coldChapterCache) GetChaptersList(context.Context, int64) ([]model.Chapter, error) {
	return nil, nil
}
func (coldChapterCache) SetChaptersList(context.Context, int64, []model.Chapter) error { return nil }
func (coldChapterCache) GetChapter(context.Context, int64, int) (*model.Chapter, error) {
	return nil, nil
}
func (coldChapterCache) SetChapter(context.Context, int64, int, *model.Chapter) error { return nil }
func (coldChapterCache) IsChapterUnlocked(context.Context, int64, int64, string) (bool, error) {
	return false, nil
}
func (coldChapterCache) SetChapterUnlocked(context.Context, int64, int64, string) error { return nil }

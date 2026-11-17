package wallet

import (
	"context"
	"slices"
	"strings"
	"testing"

	"reader-backend/internal/testdb"
)

func TestHistoryCursorKeepsTiesAndHistoricalRows(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO users(id) VALUES(1),(2);
		INSERT INTO transactions(id,user_id,type,biz_type,amount,created_at) VALUES
		('a',1,'debit','unlock',1,'2026-11-16 02:00:00+00'),
		('b',1,'debit','unlock',1,'2026-11-16 02:00:00+00'),
		('c',1,'debit','unlock',1,'2026-11-16 02:00:00+00'),
		('null-a',1,'debit','unlock',1,NULL),('null-b',1,'debit','unlock',1,NULL),
		('other',2,'debit','unlock',1,'2026-11-16 01:00:00+00');`)
	if err != nil {
		t.Fatal(err)
	}
	svc := &service{repo: NewDBRepository()}
	var cursor *historyCursor
	var ids []string
	for pageNumber := 0; pageNumber < 3; pageNumber++ {
		page, err := svc.GetHistoryPage(ctx, 1, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, tx := range page.Items {
			ids = append(ids, tx.ID)
		}
		if (page.NextCursor == "") != (pageNumber == 2) {
			t.Fatalf("unexpected cursor on page %d: %q", pageNumber, page.NextCursor)
		}
		cursor, err = parseHistoryCursor(page.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
		if pageNumber == 0 {
			_, err := pool.Exec(ctx, `INSERT INTO transactions(id,user_id,type,biz_type,amount) VALUES('new',1,'credit','recharge',1)`)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if want := []string{"c", "b", "a", "null-b", "null-a"}; !slices.Equal(ids, want) {
		t.Fatalf("history = %v, want %v", ids, want)
	}
	for _, invalid := range []string{"!", "bm90LWEtdGltZQ", strings.Repeat("A", 257)} {
		if _, err := parseHistoryCursor(invalid); err == nil {
			t.Fatalf("accepted invalid cursor %q", invalid)
		}
	}
}

func TestCheckInCountsOnlyCurrentConsecutiveDays(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO users(id) VALUES(1);
		INSERT INTO transactions(id,user_id,type,biz_type,amount,created_at)
		SELECT 'check-'||i,1,'credit','checkin',1,now()-i*interval '1 day'
		FROM generate_series(1,10) i WHERE i<>3;`)
	if err != nil {
		t.Fatal(err)
	}
	r := NewDBRepository()
	state, err := r.CheckIn(ctx, 1, false)
	if err != nil || state.CheckedIn || state.Day != 2 {
		t.Fatalf("current streak: %+v %v", state, err)
	}
	state, err = r.CheckIn(ctx, 1, true)
	if err != nil || !state.CheckedIn || state.Day != 3 || state.CoinsAwarded != 20 {
		t.Fatalf("claim: %+v %v", state, err)
	}
}

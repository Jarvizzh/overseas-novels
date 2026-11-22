package application

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/novel"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

func TestWholeBookEnvironmentScope(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	pool := testdb.Open(t)
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(1),(2),(3),(4);
		INSERT INTO novels(id,title,author,publish_status,coin_cost_per_thousand,start_pay_chapter_index)
		VALUES(1,'Book','Author','published',50,1);
		INSERT INTO chapters(id,novel_id,chapter_index,title,word_count,content) VALUES('chapter',1,0,'Chapter',1000,'Content');
		INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(1,100,0),(2,100,0),(3,100,0),(4,100,0);
		INSERT INTO admins(id,username,password_hash,role) VALUES('proof','proof','test','Finance');
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('paypal','book-live','live','test','test'),('paypal','book-sandbox','sandbox','test','test');
		INSERT INTO recharge_orders(id,user_id,novel_id,amount_cents,payment_method,status,order_type,checkout_id,fulfilled_at,checkout_environment)
		VALUES(1,1,1,999,'paypal','Success','whole_book',gen_random_uuid(),now(),'sandbox'),
		  (2,2,1,999,'paypal','Success','whole_book',gen_random_uuid(),now(),'production'),
		  (3,3,1,999,'paypal','Success','whole_book',NULL,now(),NULL),
		  (4,4,1,999,'paypal','Success','whole_book',NULL,now(),NULL);
		INSERT INTO legacy_whole_book_proofs(order_id,novel_id,evidence_reference,verified_by)
		VALUES(3,1,'legacy product only','proof'),(4,1,'legacy product and verified receipt','proof');
		INSERT INTO third_party_payment_orders(order_id,payment_provider,capture_id,currency,gross_amount,status,account_id,object_type)
		SELECT 4,'paypal','capture-book','USD',9.99,'VERIFIED',id,'capture'
		FROM payment_channel_accounts WHERE account_key='book-live'`)
	var version int64
	if err := pool.QueryRow(ctx, `SELECT content_version FROM novels WHERE id=1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	check := func(user int64, environment string, owned bool, checkUnlock bool) {
		t.Helper()
		actual, err := novel.NewDBRepository().HasWholeBook(ctx, user, 1)
		if err != nil || actual != owned {
			t.Fatalf("%s user %d ownership=%v: %v", environment, user, actual, err)
		}
		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			_, err := reserveWholeBook(ctx, tx, environment, user, 1, 1)
			return err
		})
		if (owned && !errors.Is(err, ErrCheckoutConflict)) || (!owned && err != nil) {
			t.Fatalf("%s user %d reservation: %v", environment, user, err)
		}
		if checkUnlock {
			err := wallet.NewDBRepository().UnlockChapterTx(ctx, user, 1, 0, 0, wallet.ChapterQuote{ID: "chapter", Version: version, Price: 50})
			if (owned && !errors.Is(err, wallet.ErrAlreadyUnlocked)) || (!owned && err != nil) {
				t.Fatalf("%s user %d wallet unlock: %v", environment, user, err)
			}
		}
	}
	check(1, "production", false, true)
	check(2, "production", true, true)
	check(3, "production", false, true)
	check(4, "production", true, true)
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	check(1, "sandbox", true, false)
	check(2, "sandbox", false, false)
	check(3, "sandbox", false, false)
	check(4, "sandbox", false, false)
}

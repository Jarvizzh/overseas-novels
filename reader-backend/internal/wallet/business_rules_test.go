package wallet

import (
	"context"
	"testing"

	"reader-backend/internal/novel"
	"reader-backend/internal/testdb"
)

func TestPartialRefundPreservesAccessUntilFullAndRecordsShortfall(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id) VALUES(1),(2);
 INSERT INTO novels(id,title,author) VALUES(1,'Book','Author');
 INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(1,0,3),(2,0,0);
 INSERT INTO user_subscriptions(user_id,subscription_id,status,cycle,price_cents,payment_method,last_payment_time,current_period_end) VALUES(2,'I-POLICY','CANCELLED','week',999,'paypal',now()-interval '1 hour',now()+interval '7 days');
 INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,order_type,checkout_id,novel_id,fulfilled_at,paid_at,bonus_coins_credited) VALUES(1,1,999,'paypal','Success','whole_book',gen_random_uuid(),1,now(),now(),7);
 INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,order_type,subscription_id,fulfilled_at,paid_at,paid_period_start,paid_period_end) VALUES(2,2,999,'paypal','Success','subscription','I-POLICY',now(),now(),now()-interval '1 hour',now()+interval '7 days');
 INSERT INTO third_party_payment_orders(order_id,payment_provider,capture_id,currency,gross_amount,status) VALUES(1,'paypal','POLICY-BOOK','USD',9.99,'VERIFIED'),(2,'paypal','POLICY-VIP','USD',9.99,'VERIFIED')`); err != nil {
		t.Fatal(err)
	}
	r := &dbRepository{}
	access := novel.NewDBRepository()
	check := func(want bool) {
		t.Helper()
		book, err := access.HasWholeBook(ctx, 1, 1)
		if err != nil || book != want {
			t.Fatalf("book=%t %v", book, err)
		}
		vip, err := access.HasActiveSubscription(ctx, 2)
		if err != nil || vip != want {
			t.Fatalf("VIP=%t %v", vip, err)
		}
	}
	check(true) // Cancellation preserves the already paid period.
	for _, capture := range []string{"POLICY-BOOK", "POLICY-VIP"} {
		for range 2 {
			if err := r.CompletePayPalRefund(ctx, capture+"-partial", capture, 400, "USD"); err != nil {
				t.Fatal(err)
			}
		}
	}
	check(true)
	wallet, err := r.GetWallet(ctx, 1)
	if err != nil || wallet.BonusCoins != 3 {
		t.Fatalf("partial refund reclaimed coins: %+v %v", wallet, err)
	}
	for _, capture := range []string{"POLICY-BOOK", "POLICY-VIP"} {
		for range 2 {
			if err := r.CompletePayPalRefund(ctx, capture+"-remaining", capture, 599, "USD"); err != nil {
				t.Fatal(err)
			}
		}
	}
	check(false)
	wallet, err = r.GetWallet(ctx, 1)
	if err != nil || wallet.BonusCoins != 0 || wallet.ChargedCoins != 0 {
		t.Fatalf("negative or duplicate debit: %+v %v", wallet, err)
	}
	var shortfall, debits int
	if err := pool.QueryRow(ctx, `SELECT bonus_coin_shortfall,(SELECT count(*) FROM transactions WHERE user_id=1 AND biz_type='refund') FROM payment_refunds WHERE order_id=1`).Scan(&shortfall, &debits); err != nil || shortfall != 4 || debits != 1 {
		t.Fatalf("shortfall=%d debits=%d error=%v", shortfall, debits, err)
	}
}

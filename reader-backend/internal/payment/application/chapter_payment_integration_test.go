package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

func TestChapterPurchasesConcurrentWithPaymentLifecycle(t *testing.T) {
	pool := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `
		INSERT INTO novels(id,title,author,start_pay_chapter_index,coin_cost_per_thousand)
		    VALUES(1,'Book','Author',1,50);
		INSERT INTO chapters(id,novel_id,chapter_index,title,content,word_count)
		    SELECT 'c'||i,1,i,'Chapter','body',1000 FROM generate_series(0,20) i;
		INSERT INTO users(id) VALUES(11),(12),(13);
		INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(11,0,450),(12,0,2000),(13,0,2000)`)
	if err != nil {
		t.Fatal(err)
	}
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 9, TemplateID: 7, TemplateName: "test", IsDefault: true, SlotIndex: 1,
		Code: "TEST_CH_PAY_9", ProductType: "single", PriceCents: 999, Coins: 100, Bonus: 0,
	})
	nr := novel.NewDBRepository()
	version, err := nr.GetContentVersion(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	buy := func(user int64, index int) error {
		// No process-local cache or coordination is shared across callers.
		return wallet.NewDBRepository().UnlockChapterTx(ctx, user, 1, index, 0, wallet.ChapterQuote{
			ID: fmt.Sprintf("c%d", index), Version: version, Price: 50,
		})
	}
	run := func(t *testing.T, user int64, paymentAction func() error) (int, int, int) {
		t.Helper()
		var group sync.WaitGroup
		start := make(chan struct{})
		results := make(chan error, 20)
		paymentResult := make(chan error, 1)
		for index := range cap(results) {
			group.Go(func() { <-start; results <- buy(user, index) })
		}
		if paymentAction != nil {
			group.Go(func() { <-start; paymentResult <- paymentAction() })
		} else {
			paymentResult <- nil
		}
		close(start)
		group.Wait()
		if err := <-paymentResult; err != nil {
			t.Fatal(err)
		}
		close(results)
		var paid, insufficient, pass int
		for err := range results {
			switch {
			case err == nil:
				paid++
			case errors.Is(err, wallet.ErrInsufficientBalance):
				insufficient++
			case errors.Is(err, wallet.ErrAlreadyUnlocked):
				pass++
			default:
				t.Fatalf("unexpected unlock outcome: %v", err)
			}
		}
		return paid, insufficient, pass
	}
	check := func(t *testing.T, user int64, unlocked, bonus int) {
		t.Helper()
		var entitlements, debits, balance, charged int
		err := pool.QueryRow(ctx, `
			SELECT bonus_coins, charged_coins,
			    (SELECT count(*) FROM unlock_records WHERE user_id=$1),
			    (SELECT COALESCE(sum(amount),0) FROM transactions WHERE user_id=$1 AND biz_type='unlock')
			FROM wallets WHERE user_id=$1`, user).Scan(&balance, &charged, &entitlements, &debits)
		if err != nil || entitlements != unlocked || debits != 50*unlocked || balance != bonus || charged != 0 {
			t.Fatalf("wallet: bonus=%d charged=%d unlocks=%d debits=%d error=%v", balance, charged, entitlements, debits, err)
		}
	}
	repo := NewDBRepository(wallet.NewDBRepository())
	t.Run("twenty chapters competing for nine purchases", func(t *testing.T) {
		paid, insufficient, pass := run(t, 11, nil)
		if paid != 9 || insufficient != 11 || pass != 0 {
			t.Fatalf("unlocked=%d insufficient=%d pass=%d", paid, insufficient, pass)
		}
		check(t, 11, 9, 0)
	})
	t.Run("verified recharge and refund while reading", func(t *testing.T) {
		slot, err := repo.GetRechargeSlotByID(ctx, 9)
		if err != nil {
			t.Fatal(err)
		}
		checkout, err := repo.CreateCheckout(ctx, 12, slot, 0, checkoutAttribution{})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.BindCheckout(ctx, checkout, "load-order"); err != nil {
			t.Fatal(err)
		}
		paid, insufficient, _ := run(t, 12, func() error {
			verified := &payment.OrderCapture{OrderID: "load-order", CaptureID: "load-capture", CustomID: checkout,
				CurrencyCode: "USD", GrossAmount: "9.99", FeeAmount: "0.49", NetAmount: "9.50", Status: "COMPLETED", RawPayload: "{}"}
			if _, err := repo.settleCheckout(ctx, verified.OrderID, verified); err != nil {
				return err
			}
			return repo.CompletePayPalRefund(ctx, "load-refund", verified.CaptureID, 999, "USD")
		})
		if paid != 20 || insufficient != 0 {
			t.Fatalf("unlocked=%d insufficient=%d", paid, insufficient)
		}
		check(t, 12, 20, 1000)
		// Simulate a fresh caller retrying after the first successful response was lost.
		if err := buy(12, 3); !errors.Is(err, wallet.ErrAlreadyUnlocked) {
			t.Fatalf("retry result: %v", err)
		}
		check(t, 12, 20, 1000)
	})
	t.Run("subscription renewal while reading", func(t *testing.T) {
		sub := &model.UserSubscription{UserID: 13, SubscriptionID: "I-LOAD", PlanID: "plan", Status: "PENDING",
			Cycle: "month", PriceCents: 999, Currency: "USD", PaymentMethod: "paypal"}
		if err := repo.UpsertUserSubscription(ctx, sub); err != nil {
			t.Fatal(err)
		}
		testdb.ScopeSubscriptions(t, pool, "paypal", "live", "I-LOAD")
		paid, insufficient, pass := run(t, 13, func() error {
			at := time.Now().Add(-time.Minute)
			_, err := repo.RecordSubscriptionRenewalOrderTx(ctx, sub, "load-renewal", 999, "USD", at, at.AddDate(0, 1, 0), "ACTIVE",
				&model.ThirdPartyPaymentOrder{RawPayload: "{}"})
			return err
		})
		if insufficient != 0 || paid+pass != 20 {
			t.Fatalf("unlocked=%d insufficient=%d pass=%d", paid, insufficient, pass)
		}
		if err := buy(13, 20); !errors.Is(err, wallet.ErrAlreadyUnlocked) {
			t.Fatalf("purchase after renewal: %v", err)
		}
		check(t, 13, paid, 2000-paid*50)
	})
}

package wallet

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
)

func TestPaymentSettlementIntegration(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,utm_source,utm_campaign) VALUES (101,'source','campaign'),(102,'','')`)
	exec(`INSERT INTO novels(id,title,author,publish_status) VALUES(101,'Test novel','Test author','published')`)
	exec(`INSERT INTO recharge_templates(id,name,is_default) VALUES (101,'Test',true)`)
	exec(`INSERT INTO recharge_slots(id,template_id,slot_index,type,coins,bonus,price,price_cents)
		VALUES(101,101,1,'whole_book',0,7,'$9.99',999)`)
	r := &dbRepository{}
	ctxTemplate, err := r.GetCheckoutTemplate(ctx, 101)
	if err != nil || ctxTemplate != 101 {
		t.Fatalf("checkout template = %d, %v", ctxTemplate, err)
	}
	slot, err := r.GetRechargeSlotByID(ctx, 101)
	if err != nil || slot == nil {
		t.Fatalf("slot = %v, %v", slot, err)
	}
	id, err := r.CreateCheckout(ctx, 101, slot, 101, checkoutAttribution{})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.BindCheckout(ctx, id, "integration-order"); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE recharge_slots SET price_cents=1999,bonus=90 WHERE id=101`)
	capture := &payment.PayPalCaptureResult{
		OrderID: "integration-order", CaptureID: "integration-capture", CustomID: id, CurrencyCode: "USD",
		GrossAmount: "9.99", FeeAmount: "0.50", NetAmount: "9.49", Status: "COMPLETED", RawPayload: "{}",
	}
	wrong := *capture
	wrong.CurrencyCode = "EUR"
	if _, err := r.settleCheckout(ctx, capture.OrderID, &wrong); err == nil {
		t.Fatal("currency mismatch settled")
	}
	checkoutService := NewService(r, nil, nil, nil, nil)
	if _, err := checkoutService.CapturePayPalPayment(ctx, 102, capture.OrderID); err == nil {
		t.Fatal("another account reached capture")
	}
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			result, err := r.settleCheckout(ctx, capture.OrderID, capture)
			if err != nil || result.Coins != 7 {
				t.Errorf("settlement: %+v %v", result, err)
			}
		})
	}
	group.Wait()
	n := novel.NewDBRepository()
	book, err := n.HasWholeBook(ctx, 101, 101)
	if err != nil || !book {
		t.Fatalf("whole-book entitlement: %t, %v", book, err)
	}
	w, err := r.GetWallet(ctx, 101)
	if err != nil || w.BonusCoins != 7 {
		t.Fatalf("credited wallet: %+v, %v", w, err)
	}
	exec(`INSERT INTO chapters(id,novel_id,chapter_index,title,content) VALUES('purchased',101,2,'Chapter','Body');
		INSERT INTO unlock_records(user_id,novel_id,chapter_index,chapter_id,identity_evidence,identity_resolved_at)
		VALUES(101,101,2,'purchased','fixture',now())`)
	exec(`UPDATE wallets SET bonus_coins=3 WHERE user_id=101`)
	exec(`INSERT INTO payment_refunds(order_id,request_id)
		SELECT id,gen_random_uuid() FROM recharge_orders WHERE external_ref_id=$1`, capture.OrderID)
	exec(`UPDATE recharge_orders SET status='RefundPending' WHERE external_ref_id=$1`, capture.OrderID)
	for range 2 {
		if err := r.CompletePayPalRefund(ctx, "integration-refund", capture.CaptureID, 999, "USD"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.settleCheckout(ctx, capture.OrderID, capture); err == nil {
		t.Fatal("payment replay moved refunded order")
	}
	var shortfall, debit int
	err = pool.QueryRow(ctx, `SELECT bonus_coin_shortfall,
		(SELECT SUM(amount) FROM transactions WHERE user_id=101 AND biz_type='refund')
		FROM payment_refunds WHERE provider_refund_id='integration-refund'`).Scan(&shortfall, &debit)
	if err != nil || debit != 3 || shortfall != 4 {
		t.Fatalf("refund debit %d, shortfall %d: %v", debit, shortfall, err)
	}
	book, err = n.HasWholeBook(ctx, 101, 101)
	if err != nil || book {
		t.Fatalf("refunded entitlement: %t, %v", book, err)
	}
	indices, err := n.GetUnlockedChapterIndices(ctx, 101, 101)
	if err != nil || len(indices) != 1 || indices[0] != 2 {
		t.Fatalf("chapter entitlement: %v, %v", indices, err)
	}

	sub := &model.UserSubscription{UserID: 101, SubscriptionID: "I-TEST", PlanID: "P-TEST", SlotID: 101, TemplateID: 101,
		Status: "PENDING", Cycle: "week", PriceCents: 999, Currency: "USD", PaymentMethod: "paypal"}
	if err := r.UpsertUserSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	tp := &model.ThirdPartyPaymentOrder{RawPayload: "{}"}
	paidAt := time.Now().UTC()
	for attempt := range 2 {
		first, err := r.RecordSubscriptionRenewalOrderTx(ctx, sub, "integration-sale", 999, "USD", paidAt, paidAt.AddDate(0, 0, 7), "ACTIVE", tp)
		if err != nil || first != (attempt == 0) {
			t.Fatalf("first payment=%t: %v", first, err)
		}
	}
	if err := r.UpdateUserSubscriptionStatus(ctx, sub.SubscriptionID, "CANCELLED"); err != nil {
		t.Fatal(err)
	}
	active, err := n.HasActiveSubscription(ctx, 101)
	if err != nil || !active {
		t.Fatalf("cancelled but paid VIP: %t, %v", active, err)
	}
	exec(`UPDATE user_subscriptions SET current_period_end=now()-interval '1 second' WHERE subscription_id='I-TEST'`)
	active, err = n.HasActiveSubscription(ctx, 101)
	if err != nil || active {
		t.Fatalf("expired subscription entitlement: %t, %v", active, err)
	}

	exec(`UPDATE user_subscriptions SET current_period_end=$1 WHERE subscription_id='I-TEST'`, paidAt.AddDate(0, 0, 7))
	if err := r.CompletePayPalRefund(ctx, "external-refund", "integration-sale", 999, "USD"); err != nil {
		t.Fatal(err)
	}
	active, err = n.HasActiveSubscription(ctx, 101)
	if err != nil || active {
		t.Fatalf("refunded VIP should no longer grant access: %t, %v", active, err)
	}

	for range 4 {
		group.Go(func() {
			if _, err := r.CheckIn(ctx, 101, true); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	w, err = r.GetWallet(ctx, 101)
	if err != nil || w.BonusCoins != 10 {
		t.Fatalf("check-in credited more than once: %+v, %v", w, err)
	}

	t.Run("verified refund delivered before a subscription catch-up payment", func(t *testing.T) {
		early := &model.UserSubscription{UserID: 102, SubscriptionID: "I-EARLY-REFUND", PlanID: "P-TEST", Status: "PENDING",
			Cycle: "month", PriceCents: 999, Currency: "USD", PaymentMethod: "paypal"}
		if err := r.UpsertUserSubscription(ctx, early); err != nil {
			t.Fatal(err)
		}
		event := &payment.WebhookEventResult{EventID: "WH-EARLY-REFUND", EventType: "refund_completed",
			ExternalRefID: "early-refund", CaptureID: "catch-up-sale", AmountCents: 1998, Currency: "USD"}
		for range 2 {
			if err := r.enqueuePayPalEvent(ctx, event); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.CompletePayPalRefund(ctx, event.ExternalRefID, event.CaptureID, event.AmountCents, event.Currency); !errors.Is(err, errRefundUnmatched) {
			t.Fatalf("unmatched refund: %v", err)
		}
		first, err := r.RecordSubscriptionRenewalOrderTx(ctx, early, "catch-up-sale", 1998, "USD", paidAt, paidAt.AddDate(0, 1, 0), "ACTIVE", tp)
		if err != nil || first {
			t.Fatalf("refunded sale granted access: first=%t, %v", first, err)
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM recharge_orders WHERE external_ref_id='catch-up-sale'`).Scan(&status); err != nil || status != "Refunded" {
			t.Fatalf("refunded sale status=%s: %v", status, err)
		}
		if err := r.CompletePayPalRefund(ctx, event.ExternalRefID, event.CaptureID, event.AmountCents, event.Currency); err != nil {
			t.Fatal(err)
		}
		active, err := n.HasActiveSubscription(ctx, 102)
		if err != nil || active {
			t.Fatalf("already refunded subscription has access: %t %v", active, err)
		}
	})

	t.Run("verified catch-up funds wait for a confirmed billing period", func(t *testing.T) {
		exec(`INSERT INTO users(id) VALUES(103)`)
		sub := &model.UserSubscription{UserID: 103, SubscriptionID: "I-CATCH-UP", PlanID: "P-TEST", Status: "PENDING",
			Cycle: "month", PriceCents: 999, Currency: "USD", PaymentMethod: "paypal"}
		if err := r.UpsertUserSubscription(ctx, sub); err != nil {
			t.Fatal(err)
		}
		service := NewService(r, n, nil, nil, nil).(*service)
		transaction := payment.PayPalSubscriptionTransaction{ID: "unmapped-catch-up", Status: "COMPLETED", Time: paidAt}
		transaction.Amount.Gross.Value = "19.98"
		transaction.Amount.Gross.Currency = "USD"
		if err := service.recordPayPalSubscriptionTransaction(ctx, sub, &payment.SubscriptionDetails{Status: "ACTIVE"}, transaction); err != nil {
			t.Fatal(err)
		}
		var gross int
		var review bool
		if err := pool.QueryRow(ctx, `SELECT amount_cents,reconciliation_note IS NOT NULL FROM recharge_orders
			WHERE external_ref_id='unmapped-catch-up'`).Scan(&gross, &review); err != nil || gross != 1998 || !review {
			t.Fatalf("catch-up record: %d %t %v", gross, review, err)
		}
		active, err := n.HasActiveSubscription(ctx, 103)
		if active || err != nil {
			t.Fatalf("unconfirmed billing period: %t %v", active, err)
		}
		first, err := r.RecordSubscriptionRenewalOrderTx(ctx, sub, transaction.ID, 1998, "USD", paidAt, paidAt.AddDate(0, 1, 0), "ACTIVE", tp)
		if !first || err != nil {
			t.Fatalf("confirmed catch-up period: %t %v", first, err)
		}
	})

	t.Run("legacy whole-book verification does not credit coins again", func(t *testing.T) {
		exec(`INSERT INTO recharge_orders(user_id,external_ref_id,amount_cents,payment_method,status,order_type,novel_id,coins,paid_at)
			VALUES(102,'legacy-book',999,'paypal','Success','whole_book',101,20,now())`)
		exec(`INSERT INTO third_party_payment_orders(order_id,payment_provider,external_order_id,capture_id,currency,gross_amount,status)
			SELECT id,'paypal','legacy-book','legacy-capture','USD',9.99,'COMPLETED' FROM recharge_orders WHERE external_ref_id='legacy-book'`)
		receipt := &payment.PayPalCaptureResult{OrderID: "legacy-book", CaptureID: "legacy-capture", Status: "COMPLETED",
			CustomID: "102:20", CurrencyCode: "USD", GrossAmount: "9.99", RawPayload: "{}"}
		if _, err := r.settleCheckout(ctx, receipt.OrderID, receipt); !errors.Is(err, ErrCheckoutUnavailable) {
			t.Fatalf("unproven legacy product granted: %v", err)
		}
		exec(`UPDATE recharge_orders SET fulfilled_at=now() WHERE external_ref_id='legacy-book'`)
		if hasBook, err := n.HasWholeBook(ctx, 102, 101); err != nil || hasBook {
			t.Fatalf("unproven earlier backfill has access: %t %v", hasBook, err)
		}
		if _, err := checkoutService.CapturePayPalPayment(ctx, 102, receipt.OrderID); !errors.Is(err, ErrCheckoutUnavailable) {
			t.Fatalf("unproven earlier backfill bypassed service: %v", err)
		}
		exec(`UPDATE recharge_orders SET fulfilled_at=NULL WHERE external_ref_id='legacy-book'`)
		exec(`INSERT INTO admins(id,username,password_hash,role,status) VALUES('legacy-auditor','legacy-auditor','none','Finance',1)`)
		exec(`INSERT INTO legacy_whole_book_proofs(order_id,novel_id,evidence_reference,verified_by)
            SELECT id,101,'archived merchant item record #1','legacy-auditor' FROM recharge_orders WHERE external_ref_id='legacy-book'`)
		if _, err := r.settleCheckout(ctx, receipt.OrderID, receipt); err != nil {
			t.Fatal(err)
		}
		hasBook, err := n.HasWholeBook(ctx, 102, 101)
		if !hasBook || err != nil {
			t.Fatalf("backfilled entitlement: %t %v", hasBook, err)
		}
		wallet, err := r.GetWallet(ctx, 102)
		if err != nil || wallet != nil {
			t.Fatalf("legacy payment re-credited coins: %+v %v", wallet, err)
		}
	})

	t.Run("whole-book promotion only sells its associated title", func(t *testing.T) {
		exec(`INSERT INTO novels(id,title,author,publish_status) VALUES(102,'Other novel','Test author','published')`)
		exec(`INSERT INTO promotion_links(novel_id,novel_title,generated_url,utm_source,utm_campaign,recharge_template_id)
			VALUES(101,'Test novel','https://example.invalid','source','campaign',101)`)
		slot, err := r.GetRechargeSlotByID(ctx, 101)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.CreateCheckout(ctx, 101, slot, 102, checkoutAttribution{}); !errors.Is(err, ErrCheckoutUnavailable) {
			t.Fatalf("cross-book promotion: %v", err)
		}
		if _, err := r.CreateCheckout(ctx, 101, slot, 101, checkoutAttribution{}); err != nil {
			t.Fatal(err)
		}
		for _, target := range []int64{0, 101, 102} {
			offer, err := checkoutService.GetRechargeTemplates(ctx, 101, target)
			if err != nil {
				t.Fatal(err)
			}
			expected := 0
			if target == 101 {
				expected = 1
			}
			if len(offer.Slots) != expected {
				t.Fatalf("offer visibility for novel %d: %+v", target, offer.Slots)
			}
		}

	})

	t.Run("due cancellation claims do not starve the sixth request", func(t *testing.T) {
		for i := range 6 {
			exec(`INSERT INTO user_subscriptions(user_id,subscription_id,payment_method,status)
				VALUES(102,$1,'paypal','CANCEL_PENDING')`, fmt.Sprintf("I-CANCEL-%d", i))
		}
		first, err := claimPayPalCancellations(ctx)
		if err != nil || len(first) != 5 {
			t.Fatalf("first claim: %+v %v", first, err)
		}
		second, err := claimPayPalCancellations(ctx)
		if err != nil || len(second) != 1 {
			t.Fatalf("next claim: %+v %v", second, err)
		}
	})

	t.Run("partial refund totals are capture scoped and replay safe", func(t *testing.T) {
		exec(`INSERT INTO users(id) VALUES(191)`)
		exec(`INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(191,100,10)`)
		exec(`INSERT INTO recharge_orders(user_id,external_ref_id,amount_cents,payment_method,status,coins,bonus_coins_credited)
   VALUES(191,'partial-order',999,'paypal','Success',100,10)`)
		exec(`INSERT INTO third_party_payment_orders(order_id,payment_provider,capture_id,currency,gross_amount,status)
   SELECT id,'paypal','partial-capture','USD',9.99,'VERIFIED' FROM recharge_orders WHERE external_ref_id='partial-order'`)
		for range 2 {
			if err := r.CompletePayPalRefund(ctx, "partial-a", "partial-capture", 400, "USD"); err != nil {
				t.Fatal(err)
			}
		}
		wallet, err := r.GetWallet(ctx, 191)
		if err != nil || wallet.ChargedCoins != 100 {
			t.Fatalf("partial refund debited wallet: %+v %v", wallet, err)
		}
		for range 2 {
			if err := r.CompletePayPalRefund(ctx, "partial-b", "partial-capture", 599, "USD"); err != nil {
				t.Fatal(err)
			}
		}
		wallet, err = r.GetWallet(ctx, 191)
		if err != nil || wallet.ChargedCoins != 0 || wallet.BonusCoins != 0 {
			t.Fatalf("full cumulative refund: %+v %v", wallet, err)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE user_id=191 AND biz_type='refund'`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("duplicate refund debit: %d %v", count, err)
		}
	})
	t.Run("account subscription intent serializes checkout and preserves snapshot", func(t *testing.T) {
		exec(`INSERT INTO users(id) VALUES(192)`)
		proposed := subscriptionIntent{userID: 192, slotID: 101, templateID: 101, priceCents: 999, cycle: "month", returnURL: "https://example.invalid/return", cancelURL: "https://example.invalid/cancel"}
		first, err := r.reserveSubscriptionIntent(ctx, proposed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.reserveSubscriptionIntent(ctx, proposed); !errors.Is(err, ErrSubscriptionPending) {
			t.Fatalf("concurrent checkout: %v", err)
		}
		different := proposed
		different.slotID = 102
		if _, err := r.reserveSubscriptionIntent(ctx, different); !errors.Is(err, ErrSubscriptionConflict) {
			t.Fatalf("conflicting package: %v", err)
		}
		r.releaseSubscriptionIntent(ctx, first.id)
		proposed.priceCents = 1999
		retry, err := r.reserveSubscriptionIntent(ctx, proposed)
		if err != nil || retry.id != first.id || retry.priceCents != 999 {
			t.Fatalf("retry lost payment snapshot: %+v %v", retry, err)
		}
		retry.planID = "test-plan"
		if err := r.submitSubscriptionIntent(ctx, retry.id, retry.planID); err != nil {
			t.Fatal(err)
		}
		if err := r.finishSubscriptionIntent(ctx, retry, &payment.SubscriptionResult{SubscriptionID: "I-INTENT", ApproveURL: "https://example.invalid/approval"}); err != nil {
			t.Fatal(err)
		}
		resumed, err := r.reserveSubscriptionIntent(ctx, proposed)
		if err != nil || resumed.subscriptionID != "I-INTENT" || resumed.approvalURL == "" {
			t.Fatalf("approval link not reused: %+v %v", resumed, err)
		}
		exec(`UPDATE user_subscriptions SET status='ACTIVE' WHERE subscription_id='I-INTENT'`)
		if _, err := r.reserveSubscriptionIntent(ctx, proposed); !errors.Is(err, ErrSubscriptionConflict) {
			t.Fatalf("active duplicate: %v", err)
		}
		exec(`UPDATE user_subscriptions SET status='CANCELLED' WHERE subscription_id='I-INTENT'`)
		replacement, err := r.reserveSubscriptionIntent(ctx, proposed)
		if err != nil || replacement.id == first.id {
			t.Fatalf("cancelled subscription could not switch: %+v %v", replacement, err)
		}
	})
	t.Run("paid boundary snapshot survives external cancellation", func(t *testing.T) {
		paidAt := time.Now().UTC().Truncate(time.Second)
		end := paidAt.AddDate(0, 1, 0)
		exec(`INSERT INTO user_subscriptions(user_id,subscription_id,payment_method,status) VALUES(192,'I-SNAPSHOT','paypal','CANCELLED')`)
		d := &payment.SubscriptionDetails{SubscriptionID: "I-SNAPSHOT", LastPaymentTime: &paidAt, CurrentPeriodEnd: &end, LastPaymentCents: 999, LastPaymentCurrency: "USD"}
		if err := r.savePayPalBillingSnapshot(ctx, d); err != nil {
			t.Fatal(err)
		}
		found, err := r.getPayPalBillingSnapshot(ctx, d.SubscriptionID, paidAt, "USD", 999)
		if err != nil || !found.Equal(end) {
			t.Fatalf("saved paid boundary: %v %v", found, err)
		}
		sub, err := r.GetSubscriptionByID(ctx, d.SubscriptionID)
		if err != nil {
			t.Fatal(err)
		}
		sub.PriceCents = 999
		sub.Currency = "USD"
		transaction := payment.PayPalSubscriptionTransaction{ID: "snapshot-sale", Status: "COMPLETED", Time: paidAt}
		transaction.Amount.Gross.Value = "9.99"
		transaction.Amount.Gross.Currency = "USD"
		service := NewService(r, n, nil, nil, nil).(*service)
		if err := service.recordPayPalSubscriptionTransaction(ctx, sub, &payment.SubscriptionDetails{Status: "CANCELLED"}, transaction); err != nil {
			t.Fatal(err)
		}
		var stored time.Time
		if err := pool.QueryRow(ctx, `SELECT paid_period_end FROM recharge_orders WHERE external_ref_id='snapshot-sale'`).Scan(&stored); err != nil || !stored.Equal(end) {
			t.Fatalf("cancelled payment missed saved period: %v %v", stored, err)
		}

	})

	t.Run("concurrent subscription refunds recompute after acquiring the same lock", func(t *testing.T) {
		exec(`INSERT INTO users(id) VALUES(193)`)
		exec(`INSERT INTO user_subscriptions(user_id,subscription_id,status,payment_method,last_payment_time,current_period_end)
   VALUES(193,'I-CONCURRENT','ACTIVE','paypal',now(),now()+interval '2 hours')`)
		exec(`INSERT INTO recharge_orders(user_id,external_ref_id,amount_cents,payment_method,status,order_type,subscription_id,paid_at,paid_period_start,paid_period_end)
   VALUES(193,'concurrent-old',999,'paypal','RefundPending','subscription','I-CONCURRENT',now()-interval '2 hours',now()-interval '2 hours',now()+interval '1 hour'),
   (193,'concurrent-new',999,'paypal','RefundPending','subscription','I-CONCURRENT',now()-interval '1 hour',now()-interval '1 hour',now()+interval '2 hours')`)
		exec(`INSERT INTO payment_refunds(order_id,request_id) SELECT id,gen_random_uuid() FROM recharge_orders WHERE subscription_id='I-CONCURRENT'`)
		newest, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer newest.Rollback(context.WithoutCancel(ctx))
		if _, err = newest.Exec(ctx, `SELECT complete_paypal_refund(id,'concurrent-new-refund') FROM recharge_orders WHERE external_ref_id='concurrent-new'`); err != nil {
			t.Fatal(err)
		}
		older := make(chan error, 1)
		go func() {
			_, err := pool.Exec(ctx, `/* concurrent-older-refund */ SELECT complete_paypal_refund(id,'concurrent-old-refund') FROM recharge_orders WHERE external_ref_id='concurrent-old'`)
			older <- err
		}()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.After(3 * time.Second)
		blocked := false
		for !blocked {
			select {
			case err := <-older:
				t.Fatalf("refund bypassed subscription lock: %v", err)
			case <-deadline:
				t.Fatal("older refund did not reach subscription lock")
			case <-ticker.C:
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE '/* concurrent-older-refund */%' AND wait_event_type='Lock')`).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := newest.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-older; err != nil {
			t.Fatal(err)
		}
		var cleared bool
		if err := pool.QueryRow(ctx, `SELECT current_period_end IS NULL FROM user_subscriptions WHERE subscription_id='I-CONCURRENT'`).Scan(&cleared); err != nil || !cleared {
			t.Fatalf("stale paid summary survived: %t %v", cleared, err)
		}
		active, err := n.HasActiveSubscription(ctx, 193)
		if err != nil || active {
			t.Fatalf("refunded periods still grant VIP: %t %v", active, err)
		}
	})
	t.Run("a later paid period does not grant VIP across a refund gap", func(t *testing.T) {
		exec(`INSERT INTO users(id) VALUES(194)`)
		exec(`INSERT INTO user_subscriptions(user_id,subscription_id,status,payment_method,last_payment_time,current_period_end)
   VALUES(194,'I-GAP','ACTIVE','paypal',now()-interval '2 hours',now()+interval '2 hours')`)
		exec(`INSERT INTO recharge_orders(user_id,external_ref_id,amount_cents,payment_method,status,order_type,subscription_id,paid_at,paid_period_start,paid_period_end)
   VALUES(194,'gap-old',999,'paypal','Refunded','subscription','I-GAP',now()-interval '2 hours',now()-interval '2 hours',now()+interval '1 hour'),
   (194,'gap-future',999,'paypal','Success','subscription','I-GAP',now()+interval '1 hour',now()+interval '1 hour',now()+interval '2 hours')`)
		active, err := n.HasActiveSubscription(ctx, 194)
		if err != nil || active {
			t.Fatalf("future payment bridged a refunded period: %t %v", active, err)
		}
	})
	t.Run("cancelled first payment grants its proven period", func(t *testing.T) {
		exec(`INSERT INTO users(id) VALUES(195)`)
		sub := &model.UserSubscription{UserID: 195, SubscriptionID: "I-CANCEL-FIRST", PlanID: "P-TEST", Status: "CANCELLED", Cycle: "month", PriceCents: 999, Currency: "USD", PaymentMethod: "paypal"}
		if err := r.UpsertUserSubscription(ctx, sub); err != nil {
			t.Fatal(err)
		}
		paidAt := time.Now().UTC().Truncate(time.Second)
		details := &payment.SubscriptionDetails{Status: "CANCELLED", CurrentPeriodStart: &paidAt, LastPaymentTime: &paidAt, LastPaymentCents: 999, LastPaymentCurrency: "USD"}
		transaction := payment.PayPalSubscriptionTransaction{ID: "cancel-first-sale", Status: "COMPLETED", Time: paidAt}
		transaction.Amount.Gross.Value = "9.99"
		transaction.Amount.Gross.Currency = "USD"
		service := NewService(r, n, nil, nil, nil).(*service)
		if err := service.recordPayPalSubscriptionTransaction(ctx, sub, details, transaction); err != nil {
			t.Fatal(err)
		}
		active, err := n.HasActiveSubscription(ctx, 195)
		if err != nil || !active {
			t.Fatalf("verified cancelled payment lost VIP: %t %v", active, err)
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM user_subscriptions WHERE subscription_id='I-CANCEL-FIRST'`).Scan(&status); err != nil || status != "CANCELLED" {
			t.Fatalf("cancellation overwritten: %s %v", status, err)
		}
	})

}

package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

func TestAgedLegacyPayPalBookRecovery(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	pool := testdb.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `SELECT assert_paypal_identity('r15-original','live');
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('paypal','r15-replacement','live','test','test');
		INSERT INTO users(id) VALUES(950),(951);
		INSERT INTO novels(id,title,author,publish_status) VALUES(950,'Book','Author','published');
		INSERT INTO recharge_orders(id,user_id,novel_id,amount_cents,payment_method,status,order_type,
			external_ref_id,checkout_id,created_at,checkout_environment,reconciliation_at)
		SELECT id,CASE WHEN id=953 THEN 951 ELSE 950 END,950,999,'paypal','Pending','whole_book',ref,gen_random_uuid(),now()-age,environment,now()
		FROM (VALUES (950,'r15-unknown',interval '31 days',NULL),
			(951,'r15-boundary',interval '30 days','production'),
			(952,'r15-other-environment',interval '31 days','sandbox'),
			(953,'r15-recent',interval '29 days 23 hours',NULL)) AS orders(id,ref,age,environment)`); err != nil {
		t.Fatal(err)
	}
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 950, TemplateID: 950, TemplateName: "Test", IsDefault: true, SlotIndex: 1,
		Code: "TEST_R15_950", ProductType: "whole_book", PriceCents: 999,
		Environment: "live", PayPalAccountKey: "r15-original",
	})
	repo := NewDBRepository(wallet.NewDBRepository())
	originalScope := payment.WebhookAccount{AccountKey: "r15-original", Environment: "live"}
	replacementScope := payment.WebhookAccount{AccountKey: "r15-replacement", Environment: "live"}
	original := &legacyBookPayPal{vipOfferAdapter: &vipOfferAdapter{account: originalScope}}
	replacement := &legacyBookPayPal{vipOfferAdapter: &vipOfferAdapter{account: replacementScope}, outcome: errors.New("wrong account queried")}
	adapters := payment.NewAdapters()
	if err := adapters.RegisterAccount(originalScope, original, false); err != nil {
		t.Fatal(err)
	}
	if err := adapters.RegisterAccount(replacementScope, replacement, true); err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, nil, adapters).(*service)

	purchases, err := svc.ListRecentPurchases(ctx, 950)
	if err != nil || len(purchases) != 2 {
		t.Fatalf("purchase history: %+v, %v", purchases, err)
	}
	for _, item := range purchases {
		detail, err := svc.GetPurchase(ctx, 950, item.ID, "")
		if err != nil || detail.Status != "review" || detail.ApprovalURL != "" || !strings.Contains(detail.ConfirmationMessage, "contact support") {
			t.Fatalf("reference-free recovery detail: %+v, %v", detail, err)
		}
		if _, err := svc.GetPurchase(ctx, 951, item.ID, ""); !errors.Is(err, ErrCheckoutUnavailable) {
			t.Fatalf("another buyer accessed an old checkout: %v", err)
		}
	}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE recharge_orders SET reconciliation_at=now()-interval '10 minutes' WHERE id IN (950,951,952,953)`)
	if err := RetryPaymentTask(ctx, "order", "950", nil); err != nil {
		t.Fatalf("aged retry: %v", err)
	}
	if err := RetryPaymentTask(ctx, "order", "952", nil); !errors.Is(err, ErrTaskNotRetryable) {
		t.Fatalf("wrong environment retry: %v", err)
	}

	checkoutState := func(id int, wantStatus string, wantReceipts int) {
		t.Helper()
		var status string
		var fulfilled bool
		var receipts int
		err := pool.QueryRow(ctx, `SELECT status,fulfilled_at IS NOT NULL,
			(SELECT count(*) FROM third_party_payment_orders WHERE order_id=o.id AND status='VERIFIED')
			FROM recharge_orders o WHERE id=$1`, id).Scan(&status, &fulfilled, &receipts)
		if err != nil || status != wantStatus || fulfilled || receipts != wantReceipts || original.writes != 0 || replacement.writes != 0 {
			t.Fatalf("order %d: status=%s fulfilled=%t receipts=%d writes=%d/%d %v",
				id, status, fulfilled, receipts, original.writes, replacement.writes, err)
		}
	}

	jobs, err := claimPayPalOrders(ctx)
	if err != nil || len(jobs) != 3 {
		t.Fatalf("bounded recovery claim: %+v, %v", jobs, err)
	}
	for _, job := range jobs {
		switch job.id {
		case "r15-unknown", "r15-boundary", "r15-recent":
		default:
			t.Fatalf("unexpected claim: %+v", job)
		}
	}
	original.capture = payment.OrderCapture{OrderID: "r15-unknown", Account: originalScope, CurrencyCode: "USD", GrossAmount: "9.99"}
	original.outcome = context.DeadlineExceeded
	if _, err := svc.CapturePayPalPayment(ctx, 950, "r15-unknown"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout must stay unresolved: %v", err)
	}
	checkoutState(950, "Pending", 0)
	original.outcome, original.capture.Account = paypal.ErrOrderClosed, replacementScope
	if _, err := svc.CapturePayPalPayment(ctx, 950, "r15-unknown"); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("different account must not release: %v", err)
	}
	checkoutState(950, "Pending", 0)
	original.outcome, original.capture.Account = paypal.ErrOrderApproved, originalScope
	if _, err := svc.CapturePayPalPayment(ctx, 950, "r15-unknown"); !errors.Is(err, ErrCheckoutPending) {
		t.Fatalf("approved order should remain lookup-only: %v", err)
	}
	checkoutState(950, "Pending", 0)
	request := purchaseRequest{key: uuid.New(), slotID: 950, novelID: 950}
	if _, err := repo.preparePurchase(ctx, 950, request, checkoutAttribution{}); !errors.Is(err, ErrCheckoutConflict) {
		t.Fatalf("unresolved old order should still reserve the book: %v", err)
	}

	exec(`UPDATE recharge_orders SET reconciliation_at=now(),user_id=951 WHERE id<>950`)
	exec(`UPDATE recharge_orders SET reconciliation_at=now()-interval '10 minutes' WHERE id=950`)
	original.outcome = paypal.ErrOrderClosed
	svc.reconcilePayPalOrders(ctx)
	checkoutState(950, "Failed", 0)
	if replacement, err := repo.preparePurchase(ctx, 950, request, checkoutAttribution{}); err != nil || replacement.Status != "ready" {
		t.Fatalf("verified closure must allow a fresh purchase: %+v, %v", replacement, err)
	}

	original.outcome = nil
	original.capture.CaptureID, original.capture.Status, original.capture.RawPayload = "r15-late-capture", "COMPLETED", "{}"
	event := &payment.WebhookEventResult{EventType: "capture_completed", ExternalRefID: "r15-unknown", Account: replacementScope}
	if err := svc.applyPayPalEvent(ctx, replacement, event); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("wrong account webhook must not recover the order: %v", err)
	}
	checkoutState(950, "Failed", 0)
	event.Account = originalScope
	if err := svc.applyPayPalEvent(ctx, original, event); !errors.Is(err, ErrCheckoutPending) {
		t.Fatalf("late payment should be held for review: %v", err)
	}
	checkoutState(950, "Paid", 1)
	purchases, err = svc.ListRecentPurchases(ctx, 950)
	if err != nil || len(purchases) != 2 || purchases[1].Status != "review" {
		t.Fatalf("late payment must stay visible for review: %+v, %v", purchases, err)
	}
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	purchases, err = svc.ListRecentPurchases(ctx, 950)
	if err != nil || len(purchases) != 0 {
		t.Fatalf("verified live payment leaked into sandbox history: %+v, %v", purchases, err)
	}
	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	if err := repo.CompletePayPalRefund(ctx, "r15-refunded", "r15-late-capture", 999, "USD", originalScope); err != nil {
		t.Fatalf("refund of unfulfilled late payment: %v", err)
	}
	checkoutState(950, "Refunded", 1)
}

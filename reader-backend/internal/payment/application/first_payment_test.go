package application

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"reader-backend/internal/conversion"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

func TestFirstPaymentFollowsFulfillmentOnEachPromotion(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "test")
	pool := testdb.Open(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`
		INSERT INTO novels(id,title,author) VALUES (11,'Book','Author');
		INSERT INTO promotion_links(id,novel_id,novel_title,generated_url,created_at)
		VALUES (11,11,'Book','https://example.invalid/11',clock_timestamp()),
		       (12,11,'Book','https://example.invalid/12',clock_timestamp()),
		       (13,11,'Book','https://example.invalid/13',clock_timestamp()-interval '1 day'),
		       (14,11,'Book','https://example.invalid/14',clock_timestamp());
		INSERT INTO users(id) VALUES (11),(12);
	`)
	makeCheckout := func(id, user, promotion int, pixel bool, occurred time.Time) *payment.OrderCapture {
		t.Helper()
		ref := fmt.Sprintf("promotion-order-%d", id)
		attribution := "{}"
		if pixel {
			attribution = `{"pixel_id":"123"}`
		}
		var checkout string
		err := pool.QueryRow(ctx, `
			INSERT INTO recharge_orders(id,user_id,promotion_link_id,payment_method,status,amount_cents,
				order_type,checkout_id,external_ref_id,fb_lead_metadata)
			VALUES ($1,$2,$3,'paypal','Pending',999,'single',gen_random_uuid(),$4,$5::jsonb)
			RETURNING checkout_id::text`, id, user, promotion, ref, attribution).Scan(&checkout)
		if err != nil {
			t.Fatal(err)
		}
		return &payment.OrderCapture{OrderID: ref, CaptureID: ref + "-capture", CustomID: checkout,
			CurrencyCode: "USD", GrossAmount: "9.99", FeeAmount: "0.50", NetAmount: "9.49",
			Status: "COMPLETED", RawPayload: "{}", OccurredAt: occurred}
	}
	repo := NewDBRepository(wallet.NewDBRepository())
	settle := func(capture *payment.OrderCapture) {
		t.Helper()
		if _, err := repo.settleCheckout(ctx, capture.OrderID, capture); err != nil {
			t.Fatal(err)
		}
	}
	earlier := time.Now().Add(-time.Hour)
	later := earlier.Add(time.Minute)
	captureA := makeCheckout(100, 11, 11, true, earlier)
	captureB := makeCheckout(101, 11, 11, false, later)
	// The later PayPal transaction fulfills first and has no target Pixel.
	settle(captureB)
	settle(captureA)
	settle(makeCheckout(102, 11, 12, true, later))
	settle(makeCheckout(103, 11, 13, true, later))

	assertFact := func(order int, want string) {
		t.Helper()
		var evidence string
		err := pool.QueryRow(ctx, `
			SELECT COALESCE(snapshot->>'first_paid_on_promotion','unknown')||':'||
			       (snapshot->>'payment_key')||':'||(snapshot->>'attribution_state')
			FROM reader_conversion_outbox WHERE order_id=$1 AND event_name='Purchase'`, order).Scan(&evidence)
		if evidence != want || err != nil {
			t.Fatalf("payment %d evidence=%s, want %s, err=%v", order, evidence, want, err)
		}
	}
	assertFact(100, "false:100:matched")
	assertFact(101, "true:101:missing_pixel")
	assertFact(102, "true:102:matched")
	assertFact(103, "unknown:103:matched")

	// A rolled back fulfillment must leave the promotion unclaimed.
	makeCheckout(104, 12, 14, true, earlier)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "UPDATE recharge_orders SET status='Success',fulfilled_at=clock_timestamp() WHERE id=104"); err != nil {
		t.Fatal(err)
	}
	if err := conversion.RecordPaymentTx(ctx, tx, 104, earlier, "paypal_capture_create_time", false, false); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var pendingClaims int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM reader_payment_first_on_promotion WHERE user_id=12 AND promotion_link_id=14").
		Scan(&pendingClaims); err != nil || pendingClaims != 0 {
		t.Fatalf("rolled back claim survived: %d, %v", pendingClaims, err)
	}

	firstConcurrent := makeCheckout(105, 12, 14, true, earlier)
	secondConcurrent := makeCheckout(106, 12, 14, true, later)
	var group sync.WaitGroup
	for _, capture := range []*payment.OrderCapture{firstConcurrent, secondConcurrent} {
		group.Go(func() {
			if _, err := repo.settleCheckout(ctx, capture.OrderID, capture); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	var firstRows, capturedRows int
	var firstOrder, claimant int64
	err = pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE snapshot->>'first_paid_on_promotion'='true'),count(*),
		       max(order_id) FILTER (WHERE snapshot->>'first_paid_on_promotion'='true'),
		       (SELECT order_id FROM reader_payment_first_on_promotion WHERE user_id=12 AND promotion_link_id=14)
		FROM reader_conversion_outbox WHERE order_id IN (105,106)`).Scan(&firstRows, &capturedRows, &firstOrder, &claimant)
	if err != nil || firstRows != 1 || capturedRows != 2 || claimant != firstOrder {
		t.Fatalf("concurrent first payment: count=%d/%d order=%d claim=%d err=%v", firstRows, capturedRows, firstOrder, claimant, err)
	}
}

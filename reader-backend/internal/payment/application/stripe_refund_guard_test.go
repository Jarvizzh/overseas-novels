package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"reader-backend/internal/conversion"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/testdb"
)

type stubStripeRefundGuardAdapter struct {
	account   payment.WebhookAccount
	resolveFn func(context.Context, payment.RefundRequest) (*payment.StripeRefundProof, error)
	readFn    func(context.Context, string) (*payment.StripeRefundProof, error)
}

func (a *stubStripeRefundGuardAdapter) AccountScope() payment.WebhookAccount { return a.account }
func (a *stubStripeRefundGuardAdapter) ResolveStripeRefund(ctx context.Context, req payment.RefundRequest) (*payment.StripeRefundProof, error) {
	return a.resolveFn(ctx, req)
}
func (a *stubStripeRefundGuardAdapter) ReadStripeRefund(ctx context.Context, id string) (*payment.StripeRefundProof, error) {
	if a.readFn != nil {
		return a.readFn(ctx, id)
	}
	return nil, errors.New("not implemented")
}
func (a *stubStripeRefundGuardAdapter) GetRefundLedgerEvidence(context.Context, string) ([]payment.LedgerEvidence, error) {
	return nil, nil
}
func (a *stubStripeRefundGuardAdapter) GetLedgerMovement(context.Context, string) (payment.LedgerEvidence, error) {
	return payment.LedgerEvidence{}, nil
}

func TestStripeRefundGuardConcurrencyTransitionsAndDuplicateWebhook(t *testing.T) {
	ctx := t.Context()
	pool := testdb.Open(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	account := payment.WebhookAccount{AccountKey: "acct_guard", Environment: "sandbox"}
	exec(`
		INSERT INTO payment_channel_accounts(id,provider,account_key,environment,status,credentials_ref,webhook_ref)
			OVERRIDING SYSTEM VALUE VALUES(8101,'stripe','acct_guard','sandbox','published','env:test','wh_test_8101');
		INSERT INTO users(id) VALUES(8201);
		INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(8201,100,20);
	`)
	exec(`INSERT INTO recharge_orders(id,user_id,amount_cents,currency,payment_method,status,order_type,coins,bonus_coins_credited,fulfilled_at,checkout_environment)
		VALUES(8301,8201,1000,'USD','stripe','Success','single',100,20,now(),$1)`, conversion.Environment())
	exec(`
		INSERT INTO third_party_payment_orders(id,order_id,payment_provider,external_order_id,capture_id,gross_amount,status,account_id,object_type,currency)
			VALUES(8401,8301,'stripe','cs_8301','ch_guard_1',10.00,'VERIFIED',8101,'charge','USD');
		INSERT INTO payment_fulfillment_funding(order_id,receipt_id,principal_minor,currency) VALUES(8301,8401,1000,'USD');
	`)

	// 1. Concurrent manual partial refund (400) + EFW stop-loss command (1000) -> EFW sees concurrent reservation and waits,
	// then after manual refund succeeds, EFW adjusts to exact remaining 600 without over-refunding.
	manualID := uuid.New()
	efwID := uuid.New()
	exec(`INSERT INTO payment_refund_commands(id,environment,actor_id,request_key,order_id,receipt_id,amount_minor,currency,status,next_check_at)
		VALUES($1,'sandbox','admin-1',$1,8301,8401,400,'USD','pending',now()-interval '2 seconds')`, manualID)
	exec(`INSERT INTO payment_refund_commands(id,environment,actor_id,request_key,order_id,receipt_id,amount_minor,currency,status,next_check_at,stop_loss_full_remaining)
		VALUES($1,'sandbox',$2,$1,8301,8401,1000,'USD','pending',now()-interval '1 second',true)`, efwID, aftercare.AutoMitigationActorID)

	jobs, err := claimStripeRefunds(ctx, account)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("claim jobs=%d err=%v", len(jobs), err)
	}
	// EFW job runs while manual job is still confirming (reserved=400, remaining=600 < 1000).
	efwJob := jobs[1]
	if err := verifyLocalStripeRefundEligibility(ctx, &efwJob); !errors.Is(err, payment.ErrStripeRefundVerificationPending) {
		t.Fatalf("expected EFW to wait on concurrent reservation, got err=%v", err)
	}
	if err := finishStripeRefund(ctx, efwJob, nil, payment.ErrStripeRefundVerificationPending); err != nil {
		t.Fatal(err)
	}
	// Verify first_sent_at was cleared on pre-send pending error so it can send on retry.
	var efwStatus string
	var efwFirstSent *time.Time
	if err := pool.QueryRow(ctx, `SELECT status,first_sent_at FROM payment_refund_commands WHERE id=$1`, efwID).Scan(&efwStatus, &efwFirstSent); err != nil {
		t.Fatal(err)
	}
	if efwStatus != "confirming" || efwFirstSent != nil {
		t.Fatalf("expected confirming with nil first_sent_at, got status=%q first_sent_at=%v", efwStatus, efwFirstSent)
	}

	// Complete the manual 400 refund.
	manualProof := &payment.StripeRefundProof{
		ID: "re_manual_400", ChargeID: "ch_guard_1", Status: "succeeded", Currency: "USD", AmountMinor: 400, CommandKey: manualID.String(),
	}
	if err := finishStripeRefund(ctx, jobs[0], manualProof, nil); err != nil {
		t.Fatal(err)
	}

	// Re-claim EFW job (after manual succeeded -> refunded=400, otherReserved=0, remaining=600).
	exec(`UPDATE payment_refund_commands SET next_check_at=now()-interval '1 second' WHERE id=$1`, efwID)
	jobs, err = claimStripeRefunds(ctx, account)
	if err != nil || len(jobs) != 1 || jobs[0].id != efwID.String() {
		t.Fatalf("reclaim efw jobs=%+v err=%v", jobs, err)
	}
	efwJob = jobs[0]
	if err := verifyLocalStripeRefundEligibility(ctx, &efwJob); err != nil {
		t.Fatalf("expected EFW stop-loss adjustment to succeed, got err=%v", err)
	}
	if efwJob.request.AmountMinor != 600 {
		t.Fatalf("expected EFW stop-loss amount adjusted to 600, got %d", efwJob.request.AmountMinor)
	}
	efwProof := &payment.StripeRefundProof{
		ID: "re_efw_600", ChargeID: "ch_guard_1", Status: "succeeded", Currency: "USD", AmountMinor: 600, CommandKey: efwID.String(),
	}
	if err := finishStripeRefund(ctx, efwJob, efwProof, nil); err != nil {
		t.Fatal(err)
	}

	// Verify total refunded = 1000 (no over-refund) and coins clawed back once.
	var totalRefunded int64
	var chargedCoins int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(amount_minor),0),(SELECT charged_coins FROM wallets WHERE user_id=8201)
		FROM payment_stripe_refund_items WHERE receipt_id=8401`).Scan(&totalRefunded, &chargedCoins); err != nil {
		t.Fatal(err)
	}
	if totalRefunded != 1000 || chargedCoins != 0 {
		t.Fatalf("totalRefunded=%d chargedCoins=%d, want 1000 and 0", totalRefunded, chargedCoins)
	}

	// 2. Duplicate webhook for re_efw_600 -> idempotent, zero duplicate clawback!
	exec(`UPDATE wallets SET charged_coins=250 WHERE user_id=8201`)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyStripeRefundTx(ctx, tx, account, efwProof); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT charged_coins FROM wallets WHERE user_id=8201`).Scan(&chargedCoins); err != nil {
		t.Fatal(err)
	}
	if chargedCoins != 250 {
		t.Fatalf("duplicate refund webhook caused duplicate clawback: wallet=%d, want 250", chargedCoins)
	}
}

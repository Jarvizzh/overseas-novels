package application

import (
	"errors"
	"testing"

	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/testdb"
)

func TestRefundCommandWorkerUsesLookupAfterAnUnknownResult(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`SELECT assert_paypal_identity('refund-scope','sandbox');
		INSERT INTO users(id) VALUES(816);
		INSERT INTO recharge_orders(id,user_id,amount_cents,currency,payment_method,status)
		VALUES (816,816,2000,'USD','paypal','Success');
		INSERT INTO third_party_payment_orders(id,order_id,payment_provider,capture_id,gross_amount,status,account_id,object_type)
		SELECT 816,816,'paypal','capture-816',20,'VERIFIED',id,'capture' FROM payment_channel_accounts WHERE account_key='refund-scope';
		INSERT INTO payment_refund_commands(id,environment,actor_id,request_key,order_id,receipt_id,amount_minor,currency,status,next_check_at)
		VALUES('218d95c5-9670-4790-9b60-71d753cdd6d1','sandbox','finance',
		       '1e874187-2ebb-4ab9-82b7-e97a4e3c9b15',816,816,500,'USD','pending',now())`)
	account := payment.WebhookAccount{AccountKey: "refund-scope", Environment: "sandbox"}
	wrong := payment.WebhookAccount{AccountKey: "wrong-scope", Environment: "sandbox"}
	if jobs, err := claimRefundCommands(ctx, wrong); err != nil || len(jobs) != 0 {
		t.Fatalf("wrong account claimed refund: %+v %v", jobs, err)
	}
	jobs, err := claimRefundCommands(ctx, account)
	if err != nil || len(jobs) != 1 || !jobs[0].request.Send {
		t.Fatalf("first claim: %+v %v", jobs, err)
	}
	original := jobs[0]
	exec(`INSERT INTO payment_cases(account_id,kind,external_id,receipt_refs,provider_status,is_open,observed_at)
		SELECT id,'dispute','dispute-816',ARRAY['capture-816'],'OPEN',true,now()
		FROM payment_channel_accounts WHERE account_key='refund-scope'`)
	guard := &service{}
	if err := guard.checkRefundDispute(ctx, original); !errors.Is(err, errRefundDispute) {
		t.Fatalf("active dispute did not stop new refund: %v", err)
	}
	if err := parkRefundBeforeSend(ctx, original, errRefundDispute); err != nil {
		t.Fatal(err)
	}
	if jobs, err := claimRefundCommands(ctx, account); err != nil || len(jobs) != 0 {
		t.Fatalf("open dispute left a sendable refund: %+v %v", jobs, err)
	}
	exec(`UPDATE payment_cases SET is_open=false,provider_status='RESOLVED' WHERE external_id='dispute-816'`)
	if err := aftercare.NewRepository(pool).Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, err = claimRefundCommands(ctx, account)
	if err != nil || len(jobs) != 1 || !jobs[0].request.Send || jobs[0].id != original.id {
		t.Fatalf("closed dispute did not resume the same refund: %+v %v", jobs, err)
	}
	original = jobs[0]

	if err := finishRefundCommand(ctx, original, nil, errors.New("response lost")); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE payment_refund_commands SET next_check_at=now()-interval '1 second'`)
	jobs, err = claimRefundCommands(ctx, account)
	if err != nil || len(jobs) != 1 || jobs[0].request.Send {
		t.Fatalf("unknown response was retried as a write: %+v %v", jobs, err)
	}
	if err := finishRefundCommand(ctx, jobs[0], nil, nil); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM payment_refund_commands WHERE id=$1`, original.id).Scan(&status); err != nil || status != "review" {
		t.Fatalf("unknown result was not preserved for review: %s %v", status, err)
	}
	exec(`INSERT INTO payment_refund_commands(id,environment,actor_id,request_key,order_id,receipt_id,amount_minor,currency,status,
			provider_refund_id,legacy_source,next_check_at)
		VALUES('5496df48-345b-4244-9868-95357b0457b2','sandbox','finance','1422b68a-f0ce-4234-b49f-6eebcddc4469',
		       816,816,500,'USD','confirming','known-refund',true,now())`)
	jobs, err = claimRefundCommands(ctx, account)
	if err != nil || len(jobs) != 1 || jobs[0].request.Send || jobs[0].request.RefundID != "known-refund" {
		t.Fatalf("legacy refund did not use original lookup: %+v %v", jobs, err)
	}
	verified := &payment.RefundResult{ID: "known-refund", Status: "COMPLETED", Currency: "USD", AmountMinor: 500}
	if err := finishRefundCommand(ctx, jobs[0], verified, nil); err != nil {
		t.Fatal(err)
	}
	if err := finishRefundCommand(ctx, jobs[0], verified, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM paypal_refund_items WHERE refund_id='known-refund' AND amount_cents=500`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("refund replay changed the amount ledger: %d %v", count, err)
	}
	exec(`UPDATE payment_refund_commands SET status='review',provider_refund_id='failed-refund',next_check_at=NULL WHERE id=$1`, original.id)
	event := &payment.WebhookEventResult{Account: account, EventType: "refund_status", ExternalRefID: "failed-refund", CaptureID: "capture-816"}
	for range 2 {
		if err := guard.applyPayPalEvent(ctx, nil, event); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err = claimRefundCommands(ctx, account)
	if err != nil || len(jobs) != 1 || jobs[0].request.Send || jobs[0].request.RefundID != "failed-refund" {
		t.Fatalf("event did not request the original refund read: %v %v", jobs, err)
	}
	if err := finishRefundCommand(ctx, jobs[0], &payment.RefundResult{ID: "failed-refund", Status: "FAILED"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM payment_refund_commands WHERE id=$1`, original.id).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("verified failure did not release reservation: %s %v", status, err)
	}
}

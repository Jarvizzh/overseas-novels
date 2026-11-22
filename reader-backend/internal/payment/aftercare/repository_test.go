package aftercare

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
)

func TestAftercarePrincipalAndEntitlementLifecycle(t *testing.T) {
	ctx := t.Context()
	pool := testdb.Open(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`SELECT assert_paypal_identity('aftercare-paypal','sandbox');
		INSERT INTO admins(id,username,password_hash,role) VALUES('reviewer','reviewer','test','Finance'),('finance','finance','test','Finance');
		INSERT INTO payment_aftercare_policies(account_id,hold_fulfillment,reviewer_id,finance_id,review_seconds,refund_seconds,reminder_seconds)
			SELECT id,true,'reviewer','finance',120,240,3600 FROM payment_channel_accounts WHERE account_key='aftercare-paypal';
		INSERT INTO users(id) VALUES(100);
		INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(100,34,9);
		INSERT INTO recharge_orders(id,user_id,amount_cents,currency,payment_method,status,order_type,coins,bonus_coins_credited,fulfilled_at)
			VALUES(101,100,999,'USD','paypal','Success','single',50,15,now()),(102,100,999,'USD','paypal','Paid','single',20,4,NULL);
		INSERT INTO third_party_payment_orders(id,order_id,payment_provider,external_order_id,capture_id,gross_amount,status,account_id,object_type)
			SELECT x.id,x.order_id,'paypal','ORDER-'||x.id,x.capture,9.99,'VERIFIED',a.id,'capture'
			FROM (VALUES (201,101,'CAPTURE-ORIGINAL'),(202,101,'CAPTURE-EXTRA'),(203,102,'CAPTURE-HELD'),(204,102,'CAPTURE-HELD-SECOND')) x(id,order_id,capture)
			CROSS JOIN payment_channel_accounts a WHERE a.account_key='aftercare-paypal';
		INSERT INTO payment_fulfillment_funding(order_id,receipt_id,principal_minor,currency) VALUES(101,201,999,'USD');`)
	repo := NewRepository(pool)
	now := time.Now().UTC().Truncate(time.Second)
	base := payment.CaseFact{Account: payment.WebhookAccount{AccountKey: "aftercare-paypal", Environment: "sandbox"},
		Provider: payment.ProviderPayPal, ObservedAt: now, Source: "webhook", Evidence: json.RawMessage(`{"status":"fixture"}`)}
	risk := base
	risk.Kind = payment.CaseRiskReview
	risk.ExternalID, risk.SourceRef, risk.Status = "risk-1", "wh-risk", "open"
	risk.Open, risk.ReceiptRefs = true, []string{"CAPTURE-HELD"}
	if err := repo.Record(ctx, risk); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := EvaluateFulfillment(ctx, tx, 102, 203)
	if err != nil || !hold.Deferred || hold.CaseID == nil {
		t.Fatalf("hold=%+v err=%v", hold, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	secondRisk := risk
	secondRisk.ExternalID, secondRisk.SourceRef, secondRisk.ReceiptRefs = "risk-2", "wh-second", []string{"CAPTURE-HELD-SECOND"}
	if err := repo.Record(ctx, secondRisk); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	secondHold, err := EvaluateFulfillment(ctx, tx, 102, 204)
	if err != nil || !secondHold.Deferred || secondHold.CaseID == nil || *secondHold.CaseID == *hold.CaseID {
		t.Fatalf("second receipt has no independent hold: %+v %v", secondHold, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var holds int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_deferred_fulfillment WHERE order_id=102 AND case_id IS NOT NULL`).Scan(&holds); err != nil || holds != 2 {
		t.Fatalf("persisted holds=%d: %v", holds, err)
	}
	exec(`UPDATE payment_cases SET work_due_at=now()-interval '1 second' WHERE id=$1`, *hold.CaseID)
	if err := repo.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := pool.QueryRow(ctx, `SELECT owner_id FROM payment_cases WHERE id=$1 AND escalated_at IS NOT NULL`, *hold.CaseID).Scan(&owner); err != nil || owner != "finance" {
		t.Fatalf("escalation owner=%q err=%v", owner, err)
	}

	dispute := base
	due := now.Add(30 * time.Minute)
	dispute.Kind, dispute.Open, dispute.DueAt = payment.CaseDispute, true, &due
	dispute.ExternalID, dispute.SourceRef, dispute.Status = "PP-DISPUTE", "wh-dispute", "OPEN"
	dispute.ReceiptRefs = []string{"CAPTURE-ORIGINAL"}
	if err := repo.Record(ctx, dispute); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := repo.Maintain(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_case_reminders`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("reminders=%d err=%v", count, err)
	}

	post := func(key, original string, amount int64) {
		t.Helper()
		kind := payment.MovementReversal
		_, err := pool.Exec(ctx, `WITH movement AS (
		 INSERT INTO payment_ledger_movements(account_id,movement_key,kind,provider_code,currency,occurred_at,posted_at)
		 SELECT id,$1,$4,'T1106','USD',now(),now() FROM payment_channel_accounts WHERE account_key='aftercare-paypal'
		 RETURNING id
		), evidence AS (
		 INSERT INTO payment_ledger_evidence(movement_id,source,source_ref,payload_hash,payload)
		 SELECT id,'paypal_transaction_search',$1,decode(repeat('aa',32),'hex'),'{}' FROM movement RETURNING id,movement_id
		), money AS (
		 INSERT INTO payment_ledger_amounts(movement_id,component,currency,amount_minor,evidence_id)
		 SELECT movement_id,'gross','USD',$3,id FROM evidence
		)
		 INSERT INTO payment_ledger_references(movement_id,object_type,object_id)
		 SELECT id,'paypal_transaction',$2 FROM movement UNION ALL SELECT id,'paypal_reversal',$1 FROM movement`, key, original, amount, kind)
		if err != nil {
			t.Fatal(err)
		}
	}
	post("REVERSED-EXTRA", "CAPTURE-EXTRA", -999)
	if err := repo.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_principal_loss WHERE order_id=101`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("extra capture revoked paid entitlement: count=%d err=%v", count, err)
	}

	exec(`SELECT save_paypal_refund_item('PARTIAL-REFUND','CAPTURE-ORIGINAL',400,'USD')`)
	post("REFUND-ALSO-REPORTED", "CAPTURE-ORIGINAL", -400)
	exec(`INSERT INTO payment_ledger_references(movement_id,object_type,object_id)
		SELECT id,'paypal_refund','PARTIAL-REFUND' FROM payment_ledger_movements WHERE movement_key='REFUND-ALSO-REPORTED'`)
	if err := repo.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	var partialLoss int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT loss_minor::bigint FROM payment_principal_loss_by_receipt WHERE receipt_id=201),0)`).Scan(&partialLoss); err != nil || partialLoss != 0 {
		t.Fatalf("refund and ledger duplicated a movement: %d %v", partialLoss, err)
	}
	post("REVERSED-ORIGINAL", "CAPTURE-ORIGINAL", -599)
	if err := repo.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	adjust := func(blocked bool) {
		t.Helper()
		exec(`INSERT INTO payment_loss_commands(id,case_id,order_id,requested_by,blocked,evidence_key)
			SELECT $1,c.id,l.order_id,'finance',l.target_blocked,l.evidence_key
			FROM payment_cases c CROSS JOIN payment_principal_loss l WHERE c.external_id='REVERSED-ORIGINAL'
				AND l.order_id=101 AND l.target_blocked=$2`, uuid.New(), blocked)
		did, err := repo.ApplyNextLoss(ctx)
		if err != nil || !did {
			t.Fatalf("apply adjustment blocked=%t: did=%t err=%v", blocked, did, err)
		}
	}
	balance := func(charged, bonus int) {
		t.Helper()
		var c, b int
		if err := pool.QueryRow(ctx, `SELECT charged_coins,bonus_coins FROM wallets WHERE user_id=100`).Scan(&c, &b); err != nil || c != charged || b != bonus {
			t.Fatal(fmt.Sprintf("wallet=(%d,%d), expected=(%d,%d), err=%v", c, b, charged, bonus, err))
		}
	}
	adjust(true)
	balance(0, 0)
	post("LATE-CREDIT", "CAPTURE-ORIGINAL", 599)
	adjust(false)
	balance(34, 9)
	post("REVERSED-AGAIN", "CAPTURE-ORIGINAL", -599)
	adjust(true)
	balance(0, 0)

	exec(`UPDATE wallets SET charged_coins=10,bonus_coins=3 WHERE user_id=100;
		SELECT settle_paypal_refund_item(101,'PAYPAL-REFUND','CAPTURE-ORIGINAL',599,'USD');`)
	balance(10, 3)
	var debits int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(amount),0) FROM transactions WHERE user_id=100 AND biz_type='refund'`).Scan(&debits); err != nil || debits != 0 {
		t.Fatalf("refund deducted a second principal amount: net=%d err=%v", debits, err)
	}
}

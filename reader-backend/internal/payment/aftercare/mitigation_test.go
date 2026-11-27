package aftercare

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
)

func TestStripeEFWAndDisputeAutoMitigation(t *testing.T) {
	ctx := t.Context()
	pool := testdb.Open(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	intentPrimary := uuid.New()
	intentOther := uuid.New()
	exec(`
		INSERT INTO admins(id,username,password_hash,role,role_id) VALUES
			('fin-owner','fin-owner','test','FinanceManager',(SELECT id FROM roles WHERE code='FinanceManager'));
		INSERT INTO payment_channel_accounts(id,provider,account_key,environment,status,credentials_ref,webhook_ref)
			OVERRIDING SYSTEM VALUE VALUES(9101,'stripe','acct_mitigation','sandbox','published','env:test','wh_test_9101');
		INSERT INTO payment_aftercare_policies(account_id,finance_id)
			VALUES(9101,'fin-owner');
		INSERT INTO users(id) VALUES(9201);
		INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(9201,100,20);
		INSERT INTO recharge_orders(id,user_id,amount_cents,currency,payment_method,status,order_type,coins,bonus_coins_credited,fulfilled_at,checkout_environment,subscription_id,paid_period_start,paid_period_end)
			VALUES
			(9301,9201,1000,'USD','stripe','Success','single',100,20,now(),'sandbox',NULL,NULL,NULL),
			(9302,9201,1500,'USD','stripe','Success','subscription',0,0,now(),'sandbox','sub_primary',now()-interval '1 day',now()+interval '29 days'),
			(9303,9201,1500,'USD','stripe','Success','subscription',0,0,now(),'sandbox','sub_other',now()-interval '1 day',now()+interval '29 days');
		INSERT INTO third_party_payment_orders(id,order_id,payment_provider,external_order_id,capture_id,gross_amount,status,account_id,object_type,currency)
			VALUES
			(9401,9301,'stripe','cs_9301','ch_efw_single',10.00,'VERIFIED',9101,'charge','USD'),
			(9402,9302,'stripe','in_9302','ch_efw_vip',15.00,'VERIFIED',9101,'charge','USD'),
			(9403,9303,'stripe','in_9303','ch_other_vip',15.00,'VERIFIED',9101,'charge','USD');
		INSERT INTO payment_vip_offer_versions(slot_id,template_id,slot_index,amount_minor,currency,cycle,name,description)
			VALUES(9501,1,1,1500,'USD','month','VIP Monthly','VIP Monthly') ON CONFLICT DO NOTHING;
		INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,payment_method,status,current_period_end,checkout_account_id)
			VALUES
			(9201,'sub_primary','price_1','stripe','ACTIVE',now()+interval '29 days',9101),
			(9201,'sub_other','price_1','stripe','ACTIVE',now()+interval '29 days',9101);
	`)
	exec(`INSERT INTO payment_stripe_subscription_intents(id,user_id,account_id,checkout_environment,offer_version_id,slot_id,template_id,product_ref,price_ref,amount_minor,cycle,sales_mode,allowed_countries,return_url,cancel_url,subscription_ref,customer_ref,status)
		VALUES
		($1,9201,9101,'sandbox',9501,9501,1,'prod_1','price_1',1500,'month','direct',ARRAY['US'],'https://example.com/ok','https://example.com/no','sub_primary','cus_1','bound'),
		($2,9201,9101,'sandbox',9501,9501,1,'prod_1','price_1',1500,'month','direct',ARRAY['US'],'https://example.com/ok','https://example.com/no','sub_other','cus_1','bound')`, intentPrimary, intentOther)
	exec(`INSERT INTO payment_stripe_invoice_periods(account_id,invoice_ref,intent_id,order_id,service_start,service_end,billed_minor,billing_reason,admitted)
		VALUES
		(9101,'in_9302',$1,9302,now()-interval '1 day',now()+interval '29 days',1500,'subscription_create',true),
		(9101,'in_9303',$2,9303,now()-interval '1 day',now()+interval '29 days',1500,'subscription_create',true)`, intentPrimary, intentOther)
	exec(`INSERT INTO payment_stripe_invoice_allocations(account_id,invoice_ref,allocation_ref,receipt_id,amount_minor)
		VALUES
		(9101,'in_9302','inpay_9302',9402,1500),
		(9101,'in_9303','inpay_9303',9403,1500)`)

	repo := NewRepository(pool)
	account := payment.WebhookAccount{AccountKey: "acct_mitigation", Environment: "sandbox"}
	now := time.Now().UTC().Truncate(time.Second)

	// 1. Default policy toggles are all FALSE -> EFW routes to finance_id & creates reminder, but does NOT enqueue refund or cancel.
	var autoRefund, autoCancel, disputeFreeze bool
	if err := pool.QueryRow(ctx, `SELECT auto_refund,auto_cancel_subscription,dispute_freeze FROM payment_aftercare_policies WHERE account_id=9101`).
		Scan(&autoRefund, &autoCancel, &disputeFreeze); err != nil {
		t.Fatal(err)
	}
	if autoRefund || autoCancel || disputeFreeze {
		t.Fatalf("expected all toggles false by default, got refund=%v cancel=%v freeze=%v", autoRefund, autoCancel, disputeFreeze)
	}

	efwVIP := &payment.CaseObservation{
		Fact: payment.CaseFact{
			Provider:    payment.ProviderStripe,
			Account:     account,
			Kind:        payment.CaseRiskSignal,
			ExternalID:  "issfr_vip_1",
			Stage:       "radar.early_fraud_warning.created",
			Status:      "observed",
			Reason:      "fraudulent",
			ReceiptRefs: []string{"ch_efw_vip"},
			ObservedAt:  now,
			Source:      "webhook",
			SourceRef:   "evt_efw_vip_1",
			Evidence:    json.RawMessage(`{"Actionable":true,"fraud_type":"fraudulent","charge_id":"ch_efw_vip"}`),
		},
	}
	if err := repo.RecordObservation(ctx, efwVIP); err != nil {
		t.Fatal(err)
	}
	var owner string
	var reminderCount, refundCmdCount int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(owner_id,''),
		(SELECT count(*) FROM payment_case_reminders r WHERE r.case_id=c.id),
		(SELECT count(*) FROM payment_refund_commands)
		FROM payment_cases c WHERE external_id='issfr_vip_1'`).Scan(&owner, &reminderCount, &refundCmdCount); err != nil {
		t.Fatal(err)
	}
	if owner != "fin-owner" || reminderCount != 1 || refundCmdCount != 0 {
		t.Fatalf("EFW routing/default-off failed: owner=%q reminders=%d refundCmds=%d", owner, reminderCount, refundCmdCount)
	}

	// 2. Enable auto_refund + auto_cancel_subscription independently; verify only sub_primary is queued for period-end cancel (sub_other untouched), and paid period stays intact.
	exec(`UPDATE payment_aftercare_policies SET auto_refund=true,auto_cancel_subscription=true WHERE account_id=9101`)
	efwVIP.Fact.ObservedAt = now.Add(time.Second)
	efwVIP.Fact.SourceRef = "evt_efw_vip_2"
	if err := repo.RecordObservation(ctx, efwVIP); err != nil {
		t.Fatal(err)
	}
	var primaryCancelQueued, otherCancelQueued bool
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT cancel_key IS NOT NULL AND cancel_next_at IS NOT NULL FROM payment_stripe_subscription_intents WHERE id=$1),
		(SELECT cancel_key IS NOT NULL FROM payment_stripe_subscription_intents WHERE id=$2)`,
		intentPrimary, intentOther).Scan(&primaryCancelQueued, &otherCancelQueued); err != nil {
		t.Fatal(err)
	}
	if !primaryCancelQueued || otherCancelQueued {
		t.Fatalf("auto-cancel isolation failed: primary=%v other=%v", primaryCancelQueued, otherCancelQueued)
	}
	// Verify paid period remains intact on user_subscriptions.
	var periodActive bool
	if err := pool.QueryRow(ctx, `SELECT current_period_end > now() FROM user_subscriptions WHERE subscription_id='sub_primary'`).Scan(&periodActive); err != nil || !periodActive {
		t.Fatalf("expected paid period to remain active after period-end auto-cancel, err=%v active=%v", err, periodActive)
	}

	// Verify Inquiry arriving after EFW reuses the exact same refund command per Charge (no duplicate command).
	inqVIP := &payment.CaseObservation{
		Fact: payment.CaseFact{
			Provider:    payment.ProviderStripe,
			Account:     account,
			Kind:        payment.CaseDispute,
			ExternalID:  "du_inq_vip",
			Stage:       "warning_needs_response",
			Status:      "warning_needs_response",
			Open:        true,
			ReceiptRefs: []string{"ch_efw_vip"},
			ObservedAt:  now.Add(2 * time.Second),
			Source:      "webhook",
			SourceRef:   "evt_inq_vip",
			Evidence:    json.RawMessage(`{"ChargeRefundable":true,"dispute_category":"inquiry_open"}`),
		},
	}
	if err := repo.RecordObservation(ctx, inqVIP); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_refund_commands WHERE receipt_id=9402`).Scan(&refundCmdCount); err != nil || refundCmdCount != 1 {
		t.Fatalf("expected single reused refund command for ch_efw_vip, got %d (err=%v)", refundCmdCount, err)
	}

	// 3. EFW arriving AFTER formal Dispute on ch_efw_single -> does NOT enqueue auto-refund; dispute_freeze freezes then unfreezes on won, and writes off on lost without double debit.
	exec(`UPDATE payment_aftercare_policies SET dispute_freeze=true WHERE account_id=9101`)
	gbpAmt := int64(820)
	formalDispute := &payment.CaseObservation{
		Fact: payment.CaseFact{
			Provider:            payment.ProviderStripe,
			Account:             account,
			Kind:                payment.CaseDispute,
			ExternalID:          "du_formal_single",
			Stage:               "needs_response",
			Status:              "needs_response",
			Open:                true,
			OriginalCurrency:    "GBP",
			OriginalAmountMinor: &gbpAmt,
			ReceiptRefs:         []string{"ch_efw_single"},
			ObservedAt:          now.Add(3 * time.Second),
			Source:              "webhook",
			SourceRef:           "evt_formal_single",
			Evidence:            json.RawMessage(`{"ChargeRefundable":false,"dispute_category":"formal_open"}`),
		},
	}
	if err := repo.RecordObservation(ctx, formalDispute); err != nil {
		t.Fatal(err)
	}
	// Late EFW arrives after formal dispute.
	lateEFW := &payment.CaseObservation{
		Fact: payment.CaseFact{
			Provider:    payment.ProviderStripe,
			Account:     account,
			Kind:        payment.CaseRiskSignal,
			ExternalID:  "issfr_late_single",
			Stage:       "radar.early_fraud_warning.created",
			Status:      "observed",
			Reason:      "fraudulent",
			ReceiptRefs: []string{"ch_efw_single"},
			ObservedAt:  now.Add(4 * time.Second),
			Source:      "webhook",
			SourceRef:   "evt_late_efw",
			Evidence:    json.RawMessage(`{"Actionable":true,"charge_id":"ch_efw_single"}`),
		},
	}
	if err := repo.RecordObservation(ctx, lateEFW); err != nil {
		t.Fatal(err)
	}
	var singleRefundCmds int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_refund_commands WHERE receipt_id=9401`).Scan(&singleRefundCmds); err != nil || singleRefundCmds != 0 {
		t.Fatalf("late EFW after formal dispute should not enqueue refund command, got %d (err=%v)", singleRefundCmds, err)
	}

	// Verify dispute_freeze froze order 9301 and debited wallet coins once.
	var chargedCoins, bonusCoins int
	var freezeStatus string
	var blocked bool
	if err := pool.QueryRow(ctx, `SELECT w.charged_coins,w.bonus_coins,adj.blocked,adj.freeze_status
		FROM wallets w JOIN payment_entitlement_adjustments adj ON adj.order_id=9301 WHERE w.user_id=9201`).
		Scan(&chargedCoins, &bonusCoins, &blocked, &freezeStatus); err != nil {
		t.Fatal(err)
	}
	if !blocked || freezeStatus != FreezeStatusFrozen || chargedCoins != 0 || bonusCoins != 0 {
		t.Fatalf("freeze state mismatch: blocked=%v status=%q charged=%d bonus=%d", blocked, freezeStatus, chargedCoins, bonusCoins)
	}

	// Transition dispute to WON -> unfreezes and restores coins.
	wonDispute := *formalDispute
	wonDispute.Fact.Stage, wonDispute.Fact.Status, wonDispute.Fact.Outcome = "won", "won", "won"
	wonDispute.Fact.Open = false
	wonDispute.Fact.ObservedAt = now.Add(5 * time.Second)
	wonDispute.Fact.SourceRef = "evt_won_single"
	if err := repo.RecordObservation(ctx, &wonDispute); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT w.charged_coins,w.bonus_coins,adj.blocked,adj.freeze_status
		FROM wallets w JOIN payment_entitlement_adjustments adj ON adj.order_id=9301 WHERE w.user_id=9201`).
		Scan(&chargedCoins, &bonusCoins, &blocked, &freezeStatus); err != nil {
		t.Fatal(err)
	}
	if blocked || freezeStatus != FreezeStatusNone || chargedCoins != 100 || bonusCoins != 20 {
		t.Fatalf("unfreeze state mismatch: blocked=%v status=%q charged=%d bonus=%d", blocked, freezeStatus, chargedCoins, bonusCoins)
	}

	// Now simulate refund already clawed back entitlement, then a second dispute is LOST -> zero double recovery!
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := FinalizeStripeRefund(ctx, tx, 9301, "re_already_clawed_back"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Top up user wallet with new coins from another purchase to prove lost dispute does NOT debit again.
	exec(`UPDATE wallets SET charged_coins=500 WHERE user_id=9201`)
	lostDispute := &payment.CaseObservation{
		Fact: payment.CaseFact{
			Provider:    payment.ProviderStripe,
			Account:     account,
			Kind:        payment.CaseDispute,
			ExternalID:  "du_lost_after_refund",
			Stage:       "lost",
			Status:      "lost",
			Outcome:     "lost",
			Open:        false,
			ReceiptRefs: []string{"ch_efw_single"},
			ObservedAt:  now.Add(6 * time.Second),
			Source:      "webhook",
			SourceRef:   "evt_lost_after_refund",
			Evidence:    json.RawMessage(`{"ChargeRefundable":false,"dispute_category":"closed_lost"}`),
		},
	}
	if err := repo.RecordObservation(ctx, lostDispute); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT charged_coins FROM wallets WHERE user_id=9201`).Scan(&chargedCoins); err != nil {
		t.Fatal(err)
	}
	if chargedCoins != 500 {
		t.Fatalf("lost dispute after refund double-debited wallet: got %d, want 500", chargedCoins)
	}
}

func TestReviewStaleOpenObservationDoesNotRefreezeWonDispute(t *testing.T) {
	ctx := t.Context()
	pool := testdb.Open(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	exec(`
		INSERT INTO admins(id,username,password_hash,role,role_id) VALUES
			('fin-r1','fin-r1','test','FinanceManager',(SELECT id FROM roles WHERE code='FinanceManager'));
		INSERT INTO payment_channel_accounts(id,provider,account_key,environment,status,credentials_ref,webhook_ref)
			OVERRIDING SYSTEM VALUE VALUES(9601,'stripe','acct_r1_stale','sandbox','published','env:test','wh_r1_9601');
		INSERT INTO payment_aftercare_policies(account_id,finance_id,auto_refund,auto_cancel_subscription,dispute_freeze)
			VALUES(9601,'fin-r1',true,true,true);
		INSERT INTO users(id) VALUES(9601),(9602),(9603);
		INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(9601,100,20),(9602,100,20),(9603,100,20);
		INSERT INTO recharge_orders(id,user_id,amount_cents,currency,payment_method,status,order_type,coins,bonus_coins_credited,fulfilled_at,checkout_environment)
			VALUES
			(9611,9601,1000,'USD','stripe','Success','single',100,20,now(),'sandbox'),
			(9612,9602,1000,'USD','stripe','Success','single',100,20,now(),'sandbox'),
			(9613,9603,1000,'USD','stripe','Success','single',100,20,now(),'sandbox');
		INSERT INTO third_party_payment_orders(id,order_id,payment_provider,external_order_id,capture_id,gross_amount,status,account_id,object_type,currency)
			VALUES
			(9621,9611,'stripe','cs_9611','ch_r1_won',10.00,'VERIFIED',9601,'charge','USD'),
			(9622,9612,'stripe','cs_9612','ch_r1_lost',10.00,'VERIFIED',9601,'charge','USD'),
			(9623,9613,'stripe','cs_9613','ch_r1_same_ts',10.00,'VERIFIED',9601,'charge','USD');
	`)

	repo := NewRepository(pool)
	account := payment.WebhookAccount{AccountKey: "acct_r1_stale", Environment: "sandbox"}
	t1 := time.Now().UTC().Truncate(time.Second)
	t2 := t1.Add(10 * time.Second)

	obs := func(extID, chargeID, status string, open bool, ts time.Time, srcRef string) *payment.CaseObservation {
		return &payment.CaseObservation{
			Fact: payment.CaseFact{
				Provider:    payment.ProviderStripe,
				Account:     account,
				Kind:        payment.CaseDispute,
				ExternalID:  extID,
				Stage:       status,
				Status:      status,
				Outcome:     status,
				Open:        open,
				ReceiptRefs: []string{chargeID},
				ObservedAt:  ts,
				Source:      "webhook",
				SourceRef:   srcRef,
				Evidence:    json.RawMessage(`{"ChargeRefundable":false}`),
			},
		}
	}

	// 1. open@T1 -> won@T2 -> stale open@T1 (different source_ref): must stay won and unfrozen.
	if err := repo.RecordObservation(ctx, obs("du_r1_won", "ch_r1_won", "needs_response", true, t1, "evt_r1_open_1")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObservation(ctx, obs("du_r1_won", "ch_r1_won", "won", false, t2, "evt_r1_won_2")); err != nil {
		t.Fatal(err)
	}
	var txBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE user_id=9601`).Scan(&txBefore); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObservation(ctx, obs("du_r1_won", "ch_r1_won", "needs_response", true, t1, "evt_r1_stale_open_3")); err != nil {
		t.Fatal(err)
	}
	var caseStatus, freezeStatus string
	var blocked bool
	var charged, bonus, txAfter int
	if err := pool.QueryRow(ctx, `SELECT c.provider_status, adj.blocked, adj.freeze_status, w.charged_coins, w.bonus_coins,
		(SELECT count(*) FROM transactions WHERE user_id=9601)
		FROM payment_cases c
		JOIN payment_entitlement_adjustments adj ON adj.order_id=9611
		JOIN wallets w ON w.user_id=9601
		WHERE c.external_id='du_r1_won'`).Scan(&caseStatus, &blocked, &freezeStatus, &charged, &bonus, &txAfter); err != nil {
		t.Fatal(err)
	}
	if caseStatus != "won" || blocked || freezeStatus != FreezeStatusNone || charged != 100 || bonus != 20 || txAfter != txBefore {
		t.Fatalf("stale open re-froze won dispute: status=%q blocked=%v freeze=%q charged=%d bonus=%d tx=%d->%d",
			caseStatus, blocked, freezeStatus, charged, bonus, txBefore, txAfter)
	}

	// 2. open@T1 -> lost@T2 -> stale won@T1: must stay lost and written_off (never unfrozen).
	if err := repo.RecordObservation(ctx, obs("du_r1_lost", "ch_r1_lost", "needs_response", true, t1, "evt_r1_lost_open")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObservation(ctx, obs("du_r1_lost", "ch_r1_lost", "lost", false, t2, "evt_r1_lost_final")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObservation(ctx, obs("du_r1_lost", "ch_r1_lost", "won", false, t1, "evt_r1_stale_won")); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT c.provider_status, adj.blocked, adj.freeze_status, w.charged_coins, w.bonus_coins
		FROM payment_cases c
		JOIN payment_entitlement_adjustments adj ON adj.order_id=9612
		JOIN wallets w ON w.user_id=9602
		WHERE c.external_id='du_r1_lost'`).Scan(&caseStatus, &blocked, &freezeStatus, &charged, &bonus); err != nil {
		t.Fatal(err)
	}
	if caseStatus != "lost" || !blocked || freezeStatus != FreezeStatusWrittenOff || charged != 0 || bonus != 0 {
		t.Fatalf("stale won mutated lost dispute: status=%q blocked=%v freeze=%q charged=%d bonus=%d",
			caseStatus, blocked, freezeStatus, charged, bonus)
	}

	// 3. Equal-timestamp terminal precedence: won@T1 then open@T1 must keep won and unfrozen.
	if err := repo.RecordObservation(ctx, obs("du_r1_same", "ch_r1_same_ts", "needs_response", true, t1.Add(-time.Second), "evt_r1_same_open0")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObservation(ctx, obs("du_r1_same", "ch_r1_same_ts", "won", false, t1, "evt_r1_same_won")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObservation(ctx, obs("du_r1_same", "ch_r1_same_ts", "needs_response", true, t1, "evt_r1_same_open1")); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT c.provider_status, adj.blocked, adj.freeze_status, w.charged_coins, w.bonus_coins
		FROM payment_cases c
		JOIN payment_entitlement_adjustments adj ON adj.order_id=9613
		JOIN wallets w ON w.user_id=9603
		WHERE c.external_id='du_r1_same'`).Scan(&caseStatus, &blocked, &freezeStatus, &charged, &bonus); err != nil {
		t.Fatal(err)
	}
	if caseStatus != "won" || blocked || freezeStatus != FreezeStatusNone || charged != 100 || bonus != 20 {
		t.Fatalf("equal-timestamp open regressed terminal won: status=%q blocked=%v freeze=%q charged=%d bonus=%d",
			caseStatus, blocked, freezeStatus, charged, bonus)
	}
}

func TestReviewDisablingPolicyStillReleasesExistingFreeze(t *testing.T) {
	ctx := t.Context()
	pool := testdb.Open(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	exec(`
		INSERT INTO admins(id,username,password_hash,role,role_id) VALUES
			('fin-r2','fin-r2','test','FinanceManager',(SELECT id FROM roles WHERE code='FinanceManager'));
		INSERT INTO payment_channel_accounts(id,provider,account_key,environment,status,credentials_ref,webhook_ref)
			OVERRIDING SYSTEM VALUE VALUES(9701,'stripe','acct_r2_policy','sandbox','published','env:test','wh_r2_9701');
		INSERT INTO payment_aftercare_policies(account_id,finance_id,auto_refund,auto_cancel_subscription,dispute_freeze)
			VALUES(9701,'fin-r2',true,true,true);
		INSERT INTO users(id) VALUES(9701),(9702),(9703);
		INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(9701,100,20),(9702,100,20),(9703,100,20);
		INSERT INTO recharge_orders(id,user_id,amount_cents,currency,payment_method,status,order_type,coins,bonus_coins_credited,fulfilled_at,checkout_environment)
			VALUES
			(9711,9701,1000,'USD','stripe','Success','single',100,20,now(),'sandbox'),
			(9712,9702,1000,'USD','stripe','Success','single',100,20,now(),'sandbox'),
			(9713,9703,1000,'USD','stripe','Success','single',100,20,now(),'sandbox');
		INSERT INTO third_party_payment_orders(id,order_id,payment_provider,external_order_id,capture_id,gross_amount,status,account_id,object_type,currency)
			VALUES
			(9721,9711,'stripe','cs_9711','ch_r2_won',10.00,'VERIFIED',9701,'charge','USD'),
			(9722,9712,'stripe','cs_9712','ch_r2_lost',10.00,'VERIFIED',9701,'charge','USD'),
			(9723,9713,'stripe','cs_9713','ch_r2_refunded',10.00,'VERIFIED',9701,'charge','USD');
	`)

	repo := NewRepository(pool)
	account := payment.WebhookAccount{AccountKey: "acct_r2_policy", Environment: "sandbox"}
	now := time.Now().UTC().Truncate(time.Second)

	obs := func(extID, chargeID, status string, open bool, ts time.Time, srcRef string) *payment.CaseObservation {
		return &payment.CaseObservation{
			Fact: payment.CaseFact{
				Provider:    payment.ProviderStripe,
				Account:     account,
				Kind:        payment.CaseDispute,
				ExternalID:  extID,
				Stage:       status,
				Status:      status,
				Outcome:     status,
				Open:        open,
				ReceiptRefs: []string{chargeID},
				ObservedAt:  ts,
				Source:      "webhook",
				SourceRef:   srcRef,
				Evidence:    json.RawMessage(`{"ChargeRefundable":false}`),
			},
		}
	}

	// Freeze all three orders while dispute_freeze=true.
	if err := repo.RecordObservation(ctx, obs("du_r2_won", "ch_r2_won", "needs_response", true, now, "evt_r2_open_1")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObservation(ctx, obs("du_r2_lost", "ch_r2_lost", "needs_response", true, now, "evt_r2_open_2")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObservation(ctx, obs("du_r2_ref", "ch_r2_refunded", "needs_response", true, now, "evt_r2_open_3")); err != nil {
		t.Fatal(err)
	}

	// 1. Disable ONLY dispute_freeze (keep other two enabled) -> won still releases freeze exactly once (incl. replay).
	exec(`UPDATE payment_aftercare_policies SET dispute_freeze=false WHERE account_id=9701`)
	if err := repo.RecordObservation(ctx, obs("du_r2_won", "ch_r2_won", "won", false, now.Add(time.Second), "evt_r2_won_1")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObservation(ctx, obs("du_r2_won", "ch_r2_won", "won", false, now.Add(2*time.Second), "evt_r2_won_replay")); err != nil {
		t.Fatal(err)
	}
	var blocked bool
	var freezeStatus string
	var charged, bonus int
	if err := pool.QueryRow(ctx, `SELECT adj.blocked, adj.freeze_status, w.charged_coins, w.bonus_coins
		FROM payment_entitlement_adjustments adj JOIN wallets w ON w.user_id=9701 WHERE adj.order_id=9711`).
		Scan(&blocked, &freezeStatus, &charged, &bonus); err != nil {
		t.Fatal(err)
	}
	if blocked || freezeStatus != FreezeStatusNone || charged != 100 || bonus != 20 {
		t.Fatalf("disabling dispute_freeze blocked won release: blocked=%v freeze=%q charged=%d bonus=%d", blocked, freezeStatus, charged, bonus)
	}

	// 2. Disable ALL THREE toggles -> lost still writes off existing freeze without double-debit.
	exec(`UPDATE payment_aftercare_policies SET auto_refund=false, auto_cancel_subscription=false, dispute_freeze=false WHERE account_id=9701`)
	if err := repo.RecordObservation(ctx, obs("du_r2_lost", "ch_r2_lost", "lost", false, now.Add(time.Second), "evt_r2_lost_1")); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT adj.blocked, adj.freeze_status, w.charged_coins, w.bonus_coins
		FROM payment_entitlement_adjustments adj JOIN wallets w ON w.user_id=9702 WHERE adj.order_id=9712`).
		Scan(&blocked, &freezeStatus, &charged, &bonus); err != nil {
		t.Fatal(err)
	}
	if !blocked || freezeStatus != FreezeStatusWrittenOff || charged != 0 || bonus != 0 {
		t.Fatalf("all-toggles-off blocked lost write-off: blocked=%v freeze=%q charged=%d bonus=%d", blocked, freezeStatus, charged, bonus)
	}

	// 3. Full refund finalized while all toggles off, then won arrives -> must NOT restore already-refunded balance.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := FinalizeStripeRefund(ctx, tx, 9713, "re_r2_full_refund"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObservation(ctx, obs("du_r2_ref", "ch_r2_refunded", "won", false, now.Add(time.Second), "evt_r2_ref_won")); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT adj.blocked, adj.freeze_status, w.charged_coins, w.bonus_coins
		FROM payment_entitlement_adjustments adj JOIN wallets w ON w.user_id=9703 WHERE adj.order_id=9713`).
		Scan(&blocked, &freezeStatus, &charged, &bonus); err != nil {
		t.Fatal(err)
	}
	if !blocked || freezeStatus != FreezeStatusNone || charged != 0 || bonus != 0 {
		t.Fatalf("won after full refund wrongly restored balance: blocked=%v freeze=%q charged=%d bonus=%d", blocked, freezeStatus, charged, bonus)
	}
}

package application

import (
	"context"
	"testing"

	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

func TestPayPalInboxScopesNewEventsAndPreservesLegacyReplay(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		SELECT assert_paypal_identity('account-a','sandbox');
		SELECT assert_paypal_identity('account-a','sandbox');
		INSERT INTO payment_channel_accounts (provider,environment,account_key,credentials_ref,webhook_ref)
		VALUES ('paypal','live','account-a','test:credentials','test:webhook'),
		       ('paypal','sandbox','account-b','test:credentials','test:webhook'),
		       ('stripe','sandbox','account-a','test:credentials','test:webhook');
		INSERT INTO payment_event_inbox (provider,account_id,event_id,event_type,amount_cents,currency,event)
		SELECT provider,id,'SHARED','ignored',0,'','{"EventID":"SHARED"}'
		FROM payment_channel_accounts WHERE (provider,environment,account_key)<>('paypal','sandbox','account-a');
		INSERT INTO payment_event_inbox (provider,event_id,event_type,amount_cents,currency,event,processed_at)
		VALUES ('paypal','LEGACY','ignored',0,'','{"EventID":"LEGACY"}',now());
	`); err != nil {
		t.Fatal(err)
	}

	repo := NewDBRepository(wallet.NewDBRepository())
	event := &payment.WebhookEventResult{EventID: "SHARED", EventType: "ignored",
		Account: payment.WebhookAccount{AccountKey: "account-a", Environment: "sandbox"}}
	wrongAccount := *event
	wrongAccount.Account.Environment = "live"
	wrongAccount.EventID = "WRONG-ACCOUNT"
	wrongAccount.EventType = "refund_completed"
	wrongAccount.ExternalRefID = "wrong-refund"
	wrongAccount.CaptureID = "wrong-capture"
	wrongAccount.AmountCents = 999
	wrongAccount.Currency = "USD"
	if err := repo.enqueuePayPalEvent(ctx, &wrongAccount); err == nil {
		t.Fatal("event verified under a different PayPal environment was accepted")
	}
	for range 2 {
		if err := repo.enqueuePayPalEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	conflictingReplay := wrongAccount
	conflictingReplay.Account = event.Account
	conflictingReplay.EventID = event.EventID
	if err := repo.enqueuePayPalEvent(ctx, &conflictingReplay); err != nil {
		t.Fatal(err)
	}
	legacy := *event
	legacy.EventID = "LEGACY"
	if err := repo.enqueuePayPalEvent(ctx, &legacy); err != nil {
		t.Fatal(err)
	}

	jobs, err := claimPayPalEvents(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].event.EventID != event.EventID {
		t.Fatalf("claim selected events outside the configured account: jobs=%+v, err=%v", jobs, err)
	}
	if err := finishInboxEvent(ctx, jobs[0], nil); err != nil {
		t.Fatal(err)
	}
	var sameID, completed, legacyRows, badRefunds int
	err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM payment_event_inbox WHERE event_id='SHARED'),
		(SELECT count(*) FROM payment_event_inbox WHERE event_id='SHARED' AND processed_at IS NOT NULL),
		(SELECT count(*) FROM payment_event_inbox WHERE event_id='LEGACY'),
		(SELECT count(*) FROM paypal_refund_items WHERE refund_id='wrong-refund')`).
		Scan(&sameID, &completed, &legacyRows, &badRefunds)
	if err != nil || sameID != 4 || completed != 1 || legacyRows != 1 || badRefunds != 0 {
		t.Fatalf("same ID=%d processed=%d legacy=%d unauthorized refunds=%d err=%v",
			sameID, completed, legacyRows, badRefunds, err)
	}
}

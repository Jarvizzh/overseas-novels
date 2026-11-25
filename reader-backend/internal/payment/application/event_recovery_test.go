package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/settlement"
	"reader-backend/internal/payment/stripe"
	"reader-backend/internal/testdb"
)

type recoveryFixture struct {
	account   payment.WebhookAccount
	canDecode bool
}

func (a *recoveryFixture) Provider() string                     { return payment.ProviderStripe }
func (a *recoveryFixture) AccountScope() payment.WebhookAccount { return a.account }
func (a *recoveryFixture) ListEvents(context.Context, stripe.EventQuery) (stripe.Page[stripe.Event], error) {
	return stripe.Page[stripe.Event]{Items: []stripe.Event{{ID: "evt_OldSnapshot"}, {ID: "evt_Valid"}}}, nil
}
func (a *recoveryFixture) PaymentNotice(e stripe.Event) (*payment.SingleCheckoutNotice, error) {
	if e.ID == "evt_OldSnapshot" && !a.canDecode {
		return nil, errors.New("unsupported fixture snapshot")
	}
	return &payment.SingleCheckoutNotice{EventID: e.ID, EventType: "invoice.paid", InvoiceID: "in_Recovery", Account: a.account}, nil
}

func (a *recoveryFixture) VerifySingleCheckoutEvent(_ []byte, signature string) (*payment.SingleCheckoutNotice, error) {
	if signature != a.account.AccountKey {
		return nil, errors.New("incorrect fixture signature")
	}
	return a.PaymentNotice(stripe.Event{ID: "evt_Historical"})
}

func TestStripeWebhookKeepsOriginalAccountAfterRotation(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('stripe','acct_Previous','sandbox','test','test'),('stripe','acct_Primary','sandbox','test','test')`); err != nil {
		t.Fatal(err)
	}
	previous := &recoveryFixture{account: payment.WebhookAccount{AccountKey: "acct_Previous", Environment: "sandbox"}}
	primary := &recoveryFixture{account: payment.WebhookAccount{AccountKey: "acct_Primary", Environment: "sandbox"}}
	adapters := payment.NewAdapters()
	if err := adapters.RegisterAccount(previous.account, previous, false); err != nil {
		t.Fatal(err)
	}
	if err := adapters.RegisterAccount(primary.account, primary, true); err != nil {
		t.Fatal(err)
	}
	s := &service{adapters: adapters}
	for range 2 {
		if err := s.ProcessSubscriptionWebhook(ctx, payment.ProviderStripe, []byte(`{}`),
			map[string]string{"Stripe-Signature": previous.account.AccountKey}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := claimStripeNotices(ctx, primary.account)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("primary claimed historical webhook: %+v %v", jobs, err)
	}
	jobs, err = claimStripeNotices(ctx, previous.account)
	if err != nil || len(jobs) != 1 || jobs[0].eventID != "evt_Historical" {
		t.Fatalf("historical inbox: %+v %v", jobs, err)
	}
}

func TestPaymentEventRecoveryIsolatesAndRetriesOneEvent(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
	 VALUES('stripe','acct_EventRetry','sandbox','test','test')`); err != nil {
		t.Fatal(err)
	}
	adapter := &recoveryFixture{account: payment.WebhookAccount{AccountKey: "acct_EventRetry", Environment: "sandbox"}}
	s := &service{adapters: payment.NewAdapters(adapter)}
	s.recoverStripeEventWindow(ctx)
	var valid, failed, resolved bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_event_inbox WHERE event_id='evt_Valid'),
	 EXISTS(SELECT 1 FROM payment_event_recovery_failures WHERE event_id='evt_OldSnapshot' AND next_attempt_at IS NULL)`).Scan(&valid, &failed); err != nil || !valid || !failed {
		t.Fatalf("valid=%v held=%v error=%v", valid, failed, err)
	}
	if err := ScheduleRecoveredPaymentEvent(ctx, payment.ProviderStripe, adapter.account, "evt_OldSnapshot"); err != nil {
		t.Fatal(err)
	}
	adapter.canDecode = true
	s.recoverStripeEventWindow(ctx)
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_event_inbox WHERE event_id='evt_OldSnapshot'),
	 EXISTS(SELECT 1 FROM payment_event_recovery_failures WHERE event_id='evt_OldSnapshot' AND resolved_at IS NOT NULL)`).Scan(&valid, &resolved); err != nil || !valid || !resolved {
		t.Fatalf("retried=%v resolved=%v error=%v", valid, resolved, err)
	}
}

func TestPaymentEventRecoveryKeepsScopeAndPageAcrossLeases(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('stripe','acct_Events','sandbox','test','test')`); err != nil {
		t.Fatal(err)
	}
	account := payment.WebhookAccount{AccountKey: "acct_Events", Environment: "sandbox"}
	from := time.Now().Truncate(time.Second).Add(-2 * time.Hour)
	if err := SchedulePaymentEventRecovery(ctx, payment.ProviderStripe, account, from); err != nil {
		t.Fatal(err)
	}
	first, err := claimPaymentEventWindow(ctx, payment.ProviderStripe, account)
	if err != nil || first.cursor != "" || !first.start.Equal(from) || !first.end.Equal(from.Add(time.Hour)) {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	if err := SchedulePaymentEventRecovery(ctx, payment.ProviderStripe, account, from.Add(-time.Hour)); err == nil {
		t.Fatal("recovery scheduling overwrote an active page lease")
	}
	if _, err := claimPaymentEventWindow(ctx, payment.ProviderStripe, account); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second lease: %v", err)
	}
	if err := finishPaymentEventWindow(ctx, first, "evt_Page", nil); err != nil {
		t.Fatal(err)
	}
	second, err := claimPaymentEventWindow(ctx, payment.ProviderStripe, account)
	if err != nil || second.cursor != "evt_Page" || second.start != first.start || second.end != first.end {
		t.Fatalf("lost saved window: %+v error=%v", second, err)
	}
	notice := payment.SingleCheckoutNotice{EventID: "evt_Radar", EventType: "review.opened", Account: account,
		Aftercare: []byte(`{"ID":"evt_Radar","Type":"review.opened"}`)}
	for range 2 {
		if err := queueStripeNotice(ctx, &notice, []byte(`{"id":"evt_Radar"}`)); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := claimStripeNotices(ctx, account)
	if err != nil || len(jobs) != 1 || len(jobs[0].aftercare) == 0 {
		t.Fatalf("Radar inbox=%+v error=%v", jobs, err)
	}
	payout := payment.SingleCheckoutNotice{EventID: "evt_Payout", EventType: "payout.paid", Account: account, SettlementReference: "po_Settled"}
	if err := queueStripeNotice(ctx, &payout, []byte(`{"id":"evt_Payout"}`)); err != nil {
		t.Fatal(err)
	}
	jobs, err = claimStripeNotices(ctx, account)
	if err != nil || len(jobs) != 1 || jobs[0].eventID != payout.EventID || jobs[0].settlementReference != payout.SettlementReference {
		t.Fatalf("settlement inbox=%+v error=%v", jobs, err)
	}
	settlements := settlement.NewRepository(pool)
	schedule := func() {
		t.Helper()
		if err := settlements.ScheduleStripeNotice(ctx, account, jobs[0].eventID, jobs[0].settlementReference); err != nil {
			t.Fatal(err)
		}
	}
	var commands int
	schedule()
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_settlement_commands`).Scan(&commands); err != nil || commands != 0 {
		t.Fatalf("disabled account accepted settlement command: %d %v", commands, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO admins(id,username,password_hash,role,role_id,status) VALUES('ledger-event','ledger-event','test','FinanceManager',(SELECT id FROM roles WHERE code='FinanceManager'),1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_ledger_sync_settings(account_id,actor_id,reason,enabled,window_start,window_end)
		VALUES($1,'ledger-event','reporting authorized',true,now()-interval '1 day',now())`, first.accountID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		schedule()
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_settlement_commands WHERE kind='stripe_payout'
		AND account_id=$1 AND environment='sandbox' AND parameters->>'reference'='po_Settled'`, first.accountID).Scan(&commands); err != nil || commands != 1 {
		t.Fatalf("scoped settlement command not deduplicated: %d %v", commands, err)
	}
	if err := finishPaymentEventWindow(ctx, second, "", nil); err != nil {
		t.Fatal(err)
	}
	var cursor string
	if err := pool.QueryRow(ctx, `SELECT page_cursor FROM payment_event_recovery WHERE account_id=$1`, first.accountID).Scan(&cursor); err != nil || cursor != "" {
		t.Fatalf("completed scan cursor=%q error=%v", cursor, err)
	}
}

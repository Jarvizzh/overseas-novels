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

type groupedRecoveryFixture struct {
	account  payment.WebhookAccount
	queries  []stripe.EventQuery
	listFunc func(context.Context, stripe.EventQuery) (stripe.Page[stripe.Event], error)
}

func (g *groupedRecoveryFixture) Provider() string                     { return payment.ProviderStripe }
func (g *groupedRecoveryFixture) AccountScope() payment.WebhookAccount { return g.account }
func (g *groupedRecoveryFixture) ListEvents(ctx context.Context, q stripe.EventQuery) (stripe.Page[stripe.Event], error) {
	g.queries = append(g.queries, q)
	return g.listFunc(ctx, q)
}
func (g *groupedRecoveryFixture) PaymentNotice(e stripe.Event) (*payment.SingleCheckoutNotice, error) {
	return &payment.SingleCheckoutNotice{EventID: e.ID, EventType: string(e.Type), InvoiceID: "in_" + e.ID, Account: g.account}, nil
}

func TestStripeGroupedEventRecoveryPaginatesIndependentlyWithoutMissingPages(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('stripe','acct_Grouped','sandbox','test','test')`); err != nil {
		t.Fatal(err)
	}
	account := payment.WebhookAccount{AccountKey: "acct_Grouped", Environment: "sandbox"}
	groups := stripe.RecoveryEventGroups()
	if len(groups) < 2 {
		t.Fatalf("expected >=2 groups for >20 event types, got %d", len(groups))
	}

	g0FirstType := groups[0].Types[0]
	g1FirstType := groups[1].Types[0]
	g1FailOnce := true

	adapter := &groupedRecoveryFixture{
		account: account,
		listFunc: func(_ context.Context, q stripe.EventQuery) (stripe.Page[stripe.Event], error) {
			if len(q.Types) == 0 || len(q.Types) > stripe.MaxEventTypesPerRequest {
				t.Fatalf("group query violated 20-type limit: %d", len(q.Types))
			}
			switch q.Types[0] {
			case g0FirstType:
				if q.StartingAfter == "" {
					return stripe.Page[stripe.Event]{
						Items:   []stripe.Event{{ID: "evt_g0_p1", Type: "invoice.paid"}},
						HasMore: true,
					}, nil
				}
				if q.StartingAfter == "evt_g0_p1" {
					return stripe.Page[stripe.Event]{
						Items:   []stripe.Event{{ID: "evt_g0_p2", Type: "invoice.paid"}},
						HasMore: false,
					}, nil
				}
				t.Fatalf("unexpected group_0 cursor %q", q.StartingAfter)
			case g1FirstType:
				if g1FailOnce {
					g1FailOnce = false
					return stripe.Page[stripe.Event]{}, errors.New("temporary 429 on group_1")
				}
				if q.StartingAfter == "" {
					return stripe.Page[stripe.Event]{
						Items:   []stripe.Event{{ID: "evt_g1_p1", Type: "payout.paid"}},
						HasMore: true,
					}, nil
				}
				if q.StartingAfter == "evt_g1_p1" {
					return stripe.Page[stripe.Event]{
						Items:   []stripe.Event{{ID: "evt_g1_p2", Type: "payout.paid"}},
						HasMore: false,
					}, nil
				}
				t.Fatalf("unexpected group_1 cursor %q", q.StartingAfter)
			}
			return stripe.Page[stripe.Event]{}, nil
		},
	}

	s := &service{adapters: payment.NewAdapters(adapter)}
	// Pass 1: group_0 fetches page 1 (cursor -> evt_g0_p1); group_1 fails transiently.
	s.recoverStripeEventWindow(ctx)
	var g0Cursor, g1Cursor string
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT page_cursor FROM payment_event_recovery_groups WHERE group_key='group_0'),
		(SELECT page_cursor FROM payment_event_recovery_groups WHERE group_key='group_1')`).Scan(&g0Cursor, &g1Cursor); err != nil {
		t.Fatal(err)
	}
	if g0Cursor != "evt_g0_p1" || g1Cursor != "" {
		t.Fatalf("after pass 1 cursors: g0=%q g1=%q", g0Cursor, g1Cursor)
	}

	// Make group_1 due again; Pass 2: group_0 finishes page 2 (cursor -> ''), group_1 fetches page 1 (cursor -> evt_g1_p1).
	if _, err := pool.Exec(ctx, `UPDATE payment_event_recovery_groups SET next_attempt_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	s.recoverStripeEventWindow(ctx)
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT page_cursor FROM payment_event_recovery_groups WHERE group_key='group_0'),
		(SELECT page_cursor FROM payment_event_recovery_groups WHERE group_key='group_1')`).Scan(&g0Cursor, &g1Cursor); err != nil {
		t.Fatal(err)
	}
	if g0Cursor != "" || g1Cursor != "evt_g1_p1" {
		t.Fatalf("after pass 2 cursors: g0=%q g1=%q", g0Cursor, g1Cursor)
	}

	// Pass 3: group_1 finishes page 2.
	s.recoverStripeEventWindow(ctx)

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_event_inbox
		WHERE event_id IN ('evt_g0_p1','evt_g0_p2','evt_g1_p1','evt_g1_p2')`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("recovered events=%d want 4, err=%v", count, err)
	}
}

func TestReviewStripeRecoveryKeepsLegacyBacklog(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()

	var legacyAccID, freshAccID, expiredAccID int64
	if err := pool.QueryRow(ctx, `INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('stripe','acct_LegacyBacklog','sandbox','test','test') RETURNING id`).Scan(&legacyAccID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('stripe','acct_FreshNoLegacy','sandbox','test','test') RETURNING id`).Scan(&freshAccID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('stripe','acct_ExpiredLegacy','sandbox','test','test') RETURNING id`).Scan(&expiredAccID); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyStart := now.Add(-48 * time.Hour)
	legacyEnd := now.Add(-47 * time.Hour)
	if _, err := pool.Exec(ctx, `INSERT INTO payment_event_recovery(account_id,window_start,window_end,page_cursor)
		VALUES($1,$2,$3,'evt_old_cursor')`, legacyAccID, legacyStart, legacyEnd); err != nil {
		t.Fatal(err)
	}

	legacyAccount := payment.WebhookAccount{AccountKey: "acct_LegacyBacklog", Environment: "sandbox"}
	groups := stripe.RecoveryEventGroups()
	if len(groups) < 2 {
		t.Fatalf("expected >=2 groups, got %d", len(groups))
	}

	// 1. First claim of each group must inherit the 48h legacy backlog start with empty page_cursor.
	var firstLeases []paymentEventWindowLease
	for _, g := range groups {
		lease, err := claimStripeEventRecoveryGroup(ctx, legacyAccount, g.Key)
		if err != nil {
			t.Fatalf("claim group %s: %v", g.Key, err)
		}
		if lease.cursor != "" {
			t.Fatalf("group %s copied legacy page_cursor %q; must start from empty cursor", g.Key, lease.cursor)
		}
		if delta := lease.start.Sub(legacyStart).Abs(); delta > 2*time.Second {
			t.Fatalf("group %s start=%s did not inherit legacy backlog start=%s", g.Key, lease.start, legacyStart)
		}
		if delta := lease.end.Sub(legacyEnd).Abs(); delta > 2*time.Second {
			t.Fatalf("group %s end=%s expected %s", g.Key, lease.end, legacyEnd)
		}
		firstLeases = append(firstLeases, lease)
	}

	// 2. Completing group_0 advances it; re-claiming must NOT reset it back to legacy start.
	if err := finishStripeEventRecoveryGroup(ctx, firstLeases[0], "", nil); err != nil {
		t.Fatal(err)
	}
	advanced, err := claimStripeEventRecoveryGroup(ctx, legacyAccount, groups[0].Key)
	if err != nil {
		t.Fatal(err)
	}
	wantNextStart := legacyEnd.Add(-paymentEventOverlap)
	if delta := advanced.start.Sub(wantNextStart).Abs(); delta > 2*time.Second {
		t.Fatalf("re-claim reset group_0 window: got %s want %s", advanced.start, wantNextStart)
	}

	// 3. Fresh account with no legacy row uses normal 15m initial lookback.
	freshAccount := payment.WebhookAccount{AccountKey: "acct_FreshNoLegacy", Environment: "sandbox"}
	freshLease, err := claimStripeEventRecoveryGroup(ctx, freshAccount, groups[0].Key)
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(freshLease.start); age < 14*time.Minute || age > 17*time.Minute {
		t.Fatalf("fresh account lookback age=%s, want ~15m", age)
	}

	// 4. Legacy backlog older than 30d Stripe retention inherits start and surfaces explicit retention error.
	expiredStart := now.Add(-31 * 24 * time.Hour)
	if _, err := pool.Exec(ctx, `INSERT INTO payment_event_recovery(account_id,window_start,window_end,page_cursor)
		VALUES($1,$2,$3,'')`, expiredAccID, expiredStart, expiredStart.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	expiredAccount := payment.WebhookAccount{AccountKey: "acct_ExpiredLegacy", Environment: "sandbox"}
	adapter := &groupedRecoveryFixture{
		account: expiredAccount,
		listFunc: func(context.Context, stripe.EventQuery) (stripe.Page[stripe.Event], error) {
			t.Fatal("ListEvents must not be called when window exceeds 30d retention")
			return stripe.Page[stripe.Event]{}, nil
		},
	}
	s := &service{adapters: payment.NewAdapters(adapter)}
	s.recoverStripeEventWindow(ctx)
	var lastErr string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(last_error,'') FROM payment_event_recovery_groups
		WHERE account_id=$1 AND group_key=$2`, expiredAccID, groups[0].Key).Scan(&lastErr); err != nil {
		t.Fatal(err)
	}
	if lastErr == "" {
		t.Fatal("expected explicit retention-exceeded error recorded on >30d legacy backlog")
	}
}

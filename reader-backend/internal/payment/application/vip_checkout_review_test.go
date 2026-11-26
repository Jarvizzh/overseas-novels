package application

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
)

func testUnifiedVIPReview(t *testing.T, svc *service, pool *pgxpool.Pool, pp *sixFlowPayPalAdapter, stripe *sixFlowStripeAdapter, origin string) {
	ctx := t.Context()
	repo := svc.repo
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	prepare := func(user int64, slot int, key string) *PurchaseIntent {
		t.Helper()
		intent, err := svc.PreparePurchase(ctx, user, 0, slot, 0, key, "", checkoutAttribution{})
		if err != nil {
			t.Fatal(err)
		}
		return intent
	}
	read := func(user int64, id string) vipCheckoutIntentRow {
		t.Helper()
		row, err := readVIPCheckoutIntent(ctx, user, id)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	startPayPal := func(user int64, id string) *PurchaseIntent {
		t.Helper()
		result, err := svc.StartPayPalCheckout(ctx, user, id, origin)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	startStripe := func(user int64, id string) *PurchaseIntent {
		t.Helper()
		result, err := svc.StartStripeCheckout(ctx, user, id, origin)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	exec(`INSERT INTO users(id) SELECT generate_series(610,629)`)
	exec(`INSERT INTO users(id) SELECT generate_series(680,711)`)

	t.Run("PayPal cancellation uncertainty keeps original checkout", func(t *testing.T) {
		intent := prepare(610, 605, uuid.NewString())
		old := startPayPal(610, intent.ID)
		before := stripe.vipSessionSeq
		pp.cancelFault = func(string) error { return context.DeadlineExceeded }
		defer func() { pp.cancelFault = nil }()
		if _, err := svc.StartStripeCheckout(ctx, 610, intent.ID, origin); !errors.Is(err, ErrCheckoutPending) {
			t.Fatalf("switch error: %v", err)
		}
		row := read(610, intent.ID)
		view, err := svc.GetPurchase(ctx, 610, intent.ID, "")
		if err != nil || view.Status != "confirming" || view.ApprovalURL != "" || stripe.vipSessionSeq != before ||
			row.paypalIntentID == "" || row.subscriptionID != old.ExternalOrderID || row.switchTo != payment.ProviderStripe {
			t.Fatalf("switch changed the original checkout: row=%+v view=%+v err=%v", row, view, err)
		}
		if _, err := svc.StartPayPalCheckout(ctx, 610, intent.ID, origin); !errors.Is(err, ErrCheckoutPending) {
			t.Fatalf("old checkout was reopened: %v", err)
		}

		// The provider closed the subscription, but the first cancel response was lost.
		pp.subDetailsByID[old.ExternalOrderID].Status = "CANCELLED"
		cancellations := len(pp.cancelledSubs)
		if got := startStripe(610, intent.ID); got.Status != "awaiting_approval" || stripe.vipSessionSeq != before+1 || len(pp.cancelledSubs) != cancellations {
			t.Fatalf("verified switch did not recover: %+v", got)
		}
	})

	t.Run("unapproved PayPal 404 cancel rejection allows VIP switch to Stripe", func(t *testing.T) {
		intent := prepare(628, 605, uuid.NewString())
		old := startPayPal(628, intent.ID)
		before := stripe.vipSessionSeq
		pp.cancelFault = func(string) error {
			return &paypal.APIError{
				Status: http.StatusNotFound,
				Name:   "RESOURCE_NOT_FOUND",
				Issues: []string{"INVALID_RESOURCE_ID"},
			}
		}
		defer func() { pp.cancelFault = nil }()
		got := startStripe(628, intent.ID)
		row := read(628, intent.ID)
		if got.Status != "awaiting_approval" || got.ApprovalURL == "" || stripe.vipSessionSeq != before+1 ||
			row.activeProvider != payment.ProviderStripe || row.switchTo != "" || row.paypalIntentID != "" {
			t.Fatalf("404 unapproved cancel did not switch to Stripe: got=%+v row=%+v", got, row)
		}
		if sub, err := repo.GetSubscriptionByID(ctx, old.ExternalOrderID); err != nil || sub == nil || sub.Status != "CANCELLED" {
			t.Fatalf("old unapproved PayPal subscription not marked CANCELLED: sub=%+v err=%v", sub, err)
		}
	})

	t.Run("stuck switch_to_provider recovers when buyer clicks original channel", func(t *testing.T) {
		intent := prepare(629, 605, uuid.NewString())
		old := startPayPal(629, intent.ID)
		pp.cancelFault = func(string) error { return context.DeadlineExceeded }
		if _, err := svc.StartStripeCheckout(ctx, 629, intent.ID, origin); !errors.Is(err, ErrCheckoutPending) {
			t.Fatalf("expected initial switch failure, got %v", err)
		}
		// Now simulate PayPal returning 404 RESOURCE_NOT_FOUND for the unapproved subscription
		// while the buyer retries PayPal directly (instead of clicking Stripe again).
		pp.cancelFault = func(string) error {
			return &paypal.APIError{
				Status: http.StatusNotFound,
				Name:   "RESOURCE_NOT_FOUND",
				Issues: []string{"INVALID_RESOURCE_ID"},
			}
		}
		defer func() { pp.cancelFault = nil }()
		got := startPayPal(629, intent.ID)
		row := read(629, intent.ID)
		if got.Status != "awaiting_approval" || got.ApprovalURL == "" || got.ExternalOrderID == old.ExternalOrderID ||
			row.activeProvider != payment.ProviderPayPal || row.switchTo != "" {
			t.Fatalf("stuck switch did not recover on PayPal retry: got=%+v row=%+v old=%s", got, row, old.ExternalOrderID)
		}
	})

	t.Run("late PayPal approval after switch requeues PayPal cancellation", func(t *testing.T) {
		intent := prepare(680, 605, uuid.NewString())
		old := startPayPal(680, intent.ID)
		pp.cancelFault = func(string) error {
			return &paypal.APIError{
				Status: http.StatusNotFound,
				Name:   "RESOURCE_NOT_FOUND",
				Issues: []string{"INVALID_RESOURCE_ID"},
			}
		}
		_ = startStripe(680, intent.ID)
		pp.cancelFault = nil

		// Simulate buyer later approving the old PayPal link in another tab.
		pp.subDetailsByID[old.ExternalOrderID].Status = "ACTIVE"
		if err := svc.applyPayPalEvent(ctx, pp, &payment.WebhookEventResult{
			EventType:      "subscription_activated",
			SubscriptionID: old.ExternalOrderID,
			Account:        pp.account,
		}); err != nil {
			t.Fatalf("applyPayPalEvent failed: %v", err)
		}
		if sub, err := repo.GetSubscriptionByID(ctx, old.ExternalOrderID); err != nil || sub == nil || sub.Status != "CANCEL_PENDING" {
			t.Fatalf("expected late-approved PayPal sub to enter CANCEL_PENDING: sub=%+v err=%v", sub, err)
		}
		svc.reconcilePayPalCancellations(ctx)
		if pp.subDetailsByID[old.ExternalOrderID].Status != "CANCELLED" {
			t.Fatalf("expected reconciliation to cancel late-approved PayPal sub remotely, got %s", pp.subDetailsByID[old.ExternalOrderID].Status)
		}
		if sub, err := repo.GetSubscriptionByID(ctx, old.ExternalOrderID); err != nil || sub == nil || sub.Status != "CANCELLED" {
			t.Fatalf("expected local sub to return to CANCELLED after remote cancel: sub=%+v err=%v", sub, err)
		}

		// Also verify the periodic reconciliation window catches late approvals without a webhook.
		intent2 := prepare(681, 605, uuid.NewString())
		old2 := startPayPal(681, intent2.ID)
		pp.cancelFault = func(string) error {
			return &paypal.APIError{
				Status: http.StatusNotFound,
				Name:   "RESOURCE_NOT_FOUND",
				Issues: []string{"INVALID_RESOURCE_ID"},
			}
		}
		_ = startStripe(681, intent2.ID)
		pp.cancelFault = nil
		pp.subDetailsByID[old2.ExternalOrderID].Status = "ACTIVE"
		now := time.Now().UTC()
		if err := svc.reconcileSubscriptionWindow(ctx, old2.ExternalOrderID, now.Add(-time.Hour), now); err != nil {
			t.Fatalf("reconcileSubscriptionWindow failed: %v", err)
		}
		if sub, err := repo.GetSubscriptionByID(ctx, old2.ExternalOrderID); err != nil || sub == nil || sub.Status != "CANCEL_PENDING" {
			t.Fatalf("expected reconcileSubscriptionWindow to requeue CANCEL_PENDING: sub=%+v err=%v", sub, err)
		}
	})

	t.Run("confirmPayPalCancellation resolves unapproved 404 cancel rejection", func(t *testing.T) {
		intent := prepare(682, 605, uuid.NewString())
		old := startPayPal(682, intent.ID)
		if err := svc.CancelSubscription(ctx, 682, old.ExternalOrderID, "changed mind"); err != nil {
			t.Fatalf("CancelSubscription failed: %v", err)
		}
		pp.cancelFault = func(string) error {
			return &paypal.APIError{
				Status: http.StatusNotFound,
				Name:   "RESOURCE_NOT_FOUND",
				Issues: []string{"INVALID_RESOURCE_ID"},
			}
		}
		defer func() { pp.cancelFault = nil }()
		if err := svc.confirmPayPalCancellation(ctx, old.ExternalOrderID, "changed mind"); err != nil {
			t.Fatalf("confirmPayPalCancellation should resolve unapproved 404 cleanly: %v", err)
		}
		if sub, err := repo.GetSubscriptionByID(ctx, old.ExternalOrderID); err != nil || sub == nil || sub.Status != "CANCELLED" {
			t.Fatalf("expected unapproved subscription to become CANCELLED: sub=%+v err=%v", sub, err)
		}
	})

	t.Run("expired PayPal VIP child detaches cleanly on same-channel retry", func(t *testing.T) {
		intent := prepare(683, 605, uuid.NewString())
		old := startPayPal(683, intent.ID)
		exec(`UPDATE user_subscriptions SET status='EXPIRED' WHERE subscription_id=$1`, old.ExternalOrderID)
		got := startPayPal(683, intent.ID)
		if got.Status != "awaiting_approval" || got.ApprovalURL == "" || got.ExternalOrderID == old.ExternalOrderID {
			t.Fatalf("same-channel retry after PayPal expiry did not create fresh checkout: got=%+v old=%s", got, old.ExternalOrderID)
		}
	})

	t.Run("closed Stripe VIP child detaches cleanly on same-channel retry", func(t *testing.T) {
		intent := prepare(684, 605, uuid.NewString())
		old := startStripe(684, intent.ID)
		before := stripe.vipSessionSeq
		row := read(684, intent.ID)
		exec(`UPDATE payment_stripe_subscription_intents SET status='closed', approval_url=NULL WHERE id=$1`, row.stripeIntentID)
		got := startStripe(684, intent.ID)
		if got.Status != "awaiting_approval" || got.ApprovalURL == "" || got.ApprovalURL == old.ApprovalURL || stripe.vipSessionSeq != before+1 {
			t.Fatalf("same-channel retry after Stripe session expiry did not create fresh checkout: got=%+v old=%+v seq=%d/%d",
				got, old, stripe.vipSessionSeq, before+1)
		}
	})

	t.Run("late PayPal approval on ActivateSubscription, payment_failed webhook, and APPROVED reconcile requeues cancel", func(t *testing.T) {
		notFound404 := func(string) error {
			return &paypal.APIError{
				Status: http.StatusNotFound,
				Name:   "RESOURCE_NOT_FOUND",
				Issues: []string{"INVALID_RESOURCE_ID"},
			}
		}

		// 1. ActivateSubscription on locally-CANCELLED sub with live APPROVED must requeue CANCEL_PENDING without activating.
		i1 := prepare(685, 605, uuid.NewString())
		old1 := startPayPal(685, i1.ID)
		pp.cancelFault = notFound404
		_ = startStripe(685, i1.ID)
		pp.cancelFault = nil
		pp.subDetailsByID[old1.ExternalOrderID].Status = "APPROVED"
		activatesBefore := pp.activateCalls
		if err := svc.ActivateSubscription(ctx, 685, payment.ProviderPayPal, old1.ExternalOrderID, "", "", "", "", "", "", ""); err != nil {
			t.Fatalf("ActivateSubscription failed: %v", err)
		}
		if pp.activateCalls != activatesBefore {
			t.Fatalf("ActivateSubscription must not activate a locally CANCELLED sub, calls=%d/%d", pp.activateCalls, activatesBefore)
		}
		if sub, err := repo.GetSubscriptionByID(ctx, old1.ExternalOrderID); err != nil || sub == nil || sub.Status != "CANCEL_PENDING" {
			t.Fatalf("expected ActivateSubscription to requeue CANCEL_PENDING: sub=%+v err=%v", sub, err)
		}

		// 2. subscription_payment_failed webhook on locally-CANCELLED sub with live SUSPENDED must requeue CANCEL_PENDING.
		i2 := prepare(686, 605, uuid.NewString())
		old2 := startPayPal(686, i2.ID)
		pp.cancelFault = notFound404
		_ = startStripe(686, i2.ID)
		pp.cancelFault = nil
		pp.subDetailsByID[old2.ExternalOrderID].Status = "SUSPENDED"
		if err := svc.applyPayPalEvent(ctx, pp, &payment.WebhookEventResult{
			EventType:      "subscription_payment_failed",
			SubscriptionID: old2.ExternalOrderID,
			PaidAt:         time.Now().UTC(),
			Account:        pp.account,
		}); err != nil {
			t.Fatalf("applyPayPalEvent(subscription_payment_failed) failed: %v", err)
		}
		if sub, err := repo.GetSubscriptionByID(ctx, old2.ExternalOrderID); err != nil || sub == nil || sub.Status != "CANCEL_PENDING" {
			t.Fatalf("expected payment_failed webhook to requeue CANCEL_PENDING: sub=%+v err=%v", sub, err)
		}

		// 3. reconcileSubscriptionWindow on locally-CANCELLED sub with live APPROVED must requeue without listing transactions.
		i3 := prepare(687, 605, uuid.NewString())
		old3 := startPayPal(687, i3.ID)
		pp.cancelFault = notFound404
		_ = startStripe(687, i3.ID)
		pp.cancelFault = nil
		pp.subDetailsByID[old3.ExternalOrderID].Status = "APPROVED"
		listTxBefore := pp.listTxCalls
		now := time.Now().UTC()
		if err := svc.reconcileSubscriptionWindow(ctx, old3.ExternalOrderID, now.Add(-time.Hour), now); err != nil {
			t.Fatalf("reconcileSubscriptionWindow failed: %v", err)
		}
		if pp.listTxCalls != listTxBefore {
			t.Fatalf("reconcileSubscriptionWindow must not list transactions for unactivated APPROVED sub, calls=%d/%d", pp.listTxCalls, listTxBefore)
		}
		if sub, err := repo.GetSubscriptionByID(ctx, old3.ExternalOrderID); err != nil || sub == nil || sub.Status != "CANCEL_PENDING" {
			t.Fatalf("expected reconcileSubscriptionWindow(APPROVED) to requeue CANCEL_PENDING: sub=%+v err=%v", sub, err)
		}
	})

	t.Run("VIP plan replacement succeeds after previous PayPal or Stripe child closed", func(t *testing.T) {
		// PayPal expired child on Month (605) -> buyer switches to Year (604).
		mPP := prepare(688, 605, uuid.NewString())
		oldPP := startPayPal(688, mPP.ID)
		exec(`UPDATE user_subscriptions SET status='EXPIRED' WHERE subscription_id=$1`, oldPP.ExternalOrderID)
		yPP := prepare(688, 604, uuid.NewString())
		if yPP.ID == mPP.ID || yPP.Cycle != "year" || yPP.Status != "ready" {
			t.Fatalf("expected new Year intent after expired PayPal Month child: %+v", yPP)
		}
		if oldRow := read(688, mPP.ID); oldRow.status != "Expired" || oldRow.activeProvider != "" {
			t.Fatalf("old Month intent not expired cleanly: %+v", oldRow)
		}
		if started := startPayPal(688, yPP.ID); started.Status != "awaiting_approval" || started.ApprovalURL == "" {
			t.Fatalf("could not start replacement Year PayPal checkout: %+v", started)
		}

		// Stripe closed child on Month (605) -> buyer switches to Year (604).
		mSt := prepare(689, 605, uuid.NewString())
		_ = startStripe(689, mSt.ID)
		mStRow := read(689, mSt.ID)
		exec(`UPDATE payment_stripe_subscription_intents SET status='closed', approval_url=NULL WHERE id=$1`, mStRow.stripeIntentID)
		ySt := prepare(689, 604, uuid.NewString())
		if ySt.ID == mSt.ID || ySt.Cycle != "year" || ySt.Status != "ready" {
			t.Fatalf("expected new Year intent after closed Stripe Month child: %+v", ySt)
		}
		if oldRow := read(689, mSt.ID); oldRow.status != "Expired" || oldRow.activeProvider != "" {
			t.Fatalf("old Month Stripe intent not expired cleanly: %+v", oldRow)
		}
		if started := startStripe(689, ySt.ID); started.Status != "awaiting_approval" || started.ApprovalURL == "" {
			t.Fatalf("could not start replacement Year Stripe checkout: %+v", started)
		}
	})

	t.Run("superseded PayPal late payment after Stripe fulfillment stays on ledger without double VIP fulfillment", func(t *testing.T) {
		intent := prepare(690, 605, uuid.NewString())
		oldPP := startPayPal(690, intent.ID)
		pp.cancelFault = func(string) error {
			return &paypal.APIError{
				Status: http.StatusNotFound,
				Name:   "RESOURCE_NOT_FOUND",
				Issues: []string{"INVALID_RESOURCE_ID"},
			}
		}
		_ = startStripe(690, intent.ID)
		pp.cancelFault = nil

		// Stripe completes and fulfills the VIP month purchase intent first.
		row := read(690, intent.ID)
		v, err := findStripeVIPIntent(ctx, pool, stripe.account, row.stripeIntentID)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Second)
		periodEnd := now.AddDate(0, 1, 0)
		stripeInvoice := payment.StripeVIPInvoice{
			ID: "in_dup_guard_690", BillingReason: "subscription_create", Currency: "USD", Country: "US", Status: "paid",
			DueMinor: 1499, StartsAt: now, EndsAt: periodEnd,
			Subscription: payment.StripeVIPSubscription{
				ID: "sub_DupGuard690", CustomerID: "cus_DupGuard690",
				IntentID: v.ID, PriceID: v.Offer.PriceID, Status: "active",
			},
			Allocations: []payment.StripeVIPAllocation{{
				ID: "inpay_dup_guard_690", AmountMinor: 1499,
				Receipt: payment.SingleChargeProof{
					ID: "ch_dup_guard_690", Currency: "USD", AmountMinor: 1499,
					PaidAt: now, EligibleTerms: true, Evidence: []byte("{}"),
				},
			}},
		}
		if err := settleStripeVIPInvoice(ctx, v, stripeInvoice, nil); err != nil {
			t.Fatalf("settleStripeVIPInvoice failed: %v", err)
		}

		// Now the superseded PayPal agreement is approved and billed late.
		details := *pp.subDetailsByID[oldPP.ExternalOrderID]
		details.Status = "ACTIVE"
		details.LastPaymentTime = &now
		details.LastPaymentCents = 1499
		details.LastPaymentCurrency = "USD"
		details.CurrentPeriodEnd = &periodEnd
		pp.subDetailsByID[oldPP.ExternalOrderID] = &details

		if err := svc.applyPayPalEvent(ctx, pp, &payment.WebhookEventResult{
			EventType:      "subscription_activated",
			SubscriptionID: oldPP.ExternalOrderID,
			Account:        pp.account,
		}); err != nil {
			t.Fatalf("applyPayPalEvent failed: %v", err)
		}
		oldSub, err := repo.GetSubscriptionByID(ctx, oldPP.ExternalOrderID)
		if err != nil || oldSub == nil {
			t.Fatalf("old PayPal sub: %+v %v", oldSub, err)
		}
		lateSale := payment.SubscriptionTransaction{ID: "SALE-LATE-DUP-690", Status: "COMPLETED", Time: now}
		lateSale.Amount.Gross.Currency, lateSale.Amount.Gross.Value = "USD", "14.99"
		if err := svc.recordPayPalSubscriptionTransaction(ctx, oldSub, &details, lateSale); err != nil {
			t.Fatalf("recordPayPalSubscriptionTransaction failed: %v", err)
		}

		// Verify: 2 real receipts kept on ledger, ONLY 1 VIP order fulfilled, duplicate PayPal flagged for review/refund.
		var totalOrders, fulfilledOrders, totalReceipts int
		if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE fulfilled_at IS NOT NULL)
			FROM recharge_orders WHERE user_id=690 AND order_type='subscription'`).Scan(&totalOrders, &fulfilledOrders); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM third_party_payment_orders tp
			JOIN recharge_orders o ON o.id=tp.order_id WHERE o.user_id=690`).Scan(&totalReceipts); err != nil {
			t.Fatal(err)
		}
		if totalOrders != 2 || totalReceipts != 2 || fulfilledOrders != 1 {
			t.Fatalf("expected 2 ledger orders/receipts and exactly 1 fulfilled VIP order, got orders=%d receipts=%d fulfilled=%d",
				totalOrders, totalReceipts, fulfilledOrders)
		}
		var dupUnfulfilled, dupFlagged, dupNoEvents bool
		if err := pool.QueryRow(ctx, `SELECT o.fulfilled_at IS NULL AND o.paid_period_end IS NULL,
			COALESCE(o.reconciliation_note,'') LIKE '%superseded VIP checkout%',
			NOT EXISTS(SELECT 1 FROM reader_conversion_outbox x WHERE x.order_id=o.id AND x.event_name IN ('Purchase','Subscribe'))
			FROM recharge_orders o WHERE o.external_ref_id=$1`, lateSale.ID).Scan(&dupUnfulfilled, &dupFlagged, &dupNoEvents); err != nil ||
			!dupUnfulfilled || !dupFlagged || !dupNoEvents {
			t.Fatalf("duplicate PayPal order state wrong: unfulfilled=%v flagged=%v noEvents=%v err=%v",
				dupUnfulfilled, dupFlagged, dupNoEvents, err)
		}
		svc.reconcilePayPalCancellations(ctx)
		if pp.subDetailsByID[oldPP.ExternalOrderID].Status != "CANCELLED" {
			t.Fatalf("expected superseded PayPal agreement to be cancelled remotely, got %s", pp.subDetailsByID[oldPP.ExternalOrderID].Status)
		}
	})

	t.Run("live PayPal EXPIRED agreement refreshes and returns fresh approval URL on retry", func(t *testing.T) {
		intent := prepare(691, 605, uuid.NewString())
		old := startPayPal(691, intent.ID)
		// Only change remote provider status to EXPIRED; keep local DB PENDING.
		pp.subDetailsByID[old.ExternalOrderID].Status = "EXPIRED"

		got := startPayPal(691, intent.ID)
		if got.Status != "awaiting_approval" || got.ApprovalURL == "" ||
			got.ApprovalURL == old.ApprovalURL || got.ExternalOrderID == old.ExternalOrderID {
			t.Fatalf("retry on live-EXPIRED PayPal sub did not issue a fresh approval URL: got=%+v old=%+v", got, old)
		}
		if oldSub, err := repo.GetSubscriptionByID(ctx, old.ExternalOrderID); err != nil || oldSub == nil || oldSub.Status != "EXPIRED" {
			t.Fatalf("old expired PayPal sub not persisted as EXPIRED: sub=%+v err=%v", oldSub, err)
		}
	})

	t.Run("#22 VIP plan switch from PayPal (with pending Stripe switch) succeeds in one click and is idempotent", func(t *testing.T) {
		oldIntent := prepare(692, 605, uuid.NewString())
		oldPP := startPayPal(692, oldIntent.ID)
		// Simulate an earlier failed PayPal->Stripe switch leaving switch_to_provider='stripe'.
		exec(`UPDATE payment_vip_checkout_intents SET switch_to_provider='stripe' WHERE intent_id=$1`, oldIntent.ID)

		reqKey := uuid.NewString()
		newIntent := prepare(692, 604, reqKey)
		if newIntent.ID == oldIntent.ID || newIntent.Cycle != "year" || newIntent.Status != "ready" {
			t.Fatalf("expected fresh Year VIP intent in one click: %+v", newIntent)
		}
		if oldRow := read(692, oldIntent.ID); oldRow.status != "Expired" || oldRow.activeProvider != "" || oldRow.switchTo != "" {
			t.Fatalf("old Month VIP intent not released cleanly: %+v", oldRow)
		}
		if pp.subDetailsByID[oldPP.ExternalOrderID].Status != "CANCELLED" {
			t.Fatalf("old PayPal agreement not cancelled remotely: %s", pp.subDetailsByID[oldPP.ExternalOrderID].Status)
		}
		started := startPayPal(692, newIntent.ID)
		if started.Status != "awaiting_approval" || started.ApprovalURL == "" || started.ExternalOrderID == oldPP.ExternalOrderID {
			t.Fatalf("could not start replacement Year checkout: %+v", started)
		}
		// Replaying same request key and clicking again with a new key both reuse the single Year intent.
		if replay := prepare(692, 604, reqKey); replay.ID != newIntent.ID {
			t.Fatalf("replayed key created a different intent: %+v vs %s", replay, newIntent.ID)
		}
		if again := prepare(692, 604, uuid.NewString()); again.ID != newIntent.ID {
			t.Fatalf("duplicate click created a second Year intent: %+v vs %s", again, newIntent.ID)
		}
		var pendingCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_vip_checkout_intents WHERE user_id=692 AND status='Pending'`).Scan(&pendingCount); err != nil || pendingCount != 1 {
			t.Fatalf("expected exactly 1 pending Year VIP intent, got %d err=%v", pendingCount, err)
		}
	})

	t.Run("#22 PayPal cancel refusal (approval still open), timeout, and concurrent approval block plan switch with structured error", func(t *testing.T) {
		// Case A: PayPal rejects /cancel with 404 and old approval link remains APPROVAL_PENDING.
		oldA := prepare(693, 605, uuid.NewString())
		oldPPA := startPayPal(693, oldA.ID)
		pp.cancelFault = func(string) error {
			return &paypal.APIError{
				Status: http.StatusNotFound,
				Name:   "RESOURCE_NOT_FOUND",
				Issues: []string{"INVALID_RESOURCE_ID"},
			}
		}
		beforePP, beforeSt := len(pp.agreements), stripe.vipSessionSeq
		_, err := svc.PreparePurchase(ctx, 693, 0, 604, 0, uuid.NewString(), "", checkoutAttribution{})
		var blocked *VIPPlanSwitchBlockedError
		if !errors.As(err, &blocked) || blocked.Code != VIPPlanSwitchCodeApprovalOpen ||
			blocked.CurrentIntentID != oldA.ID || blocked.CurrentSlotID != 605 ||
			len(pp.agreements) != beforePP || stripe.vipSessionSeq != beforeSt {
			t.Fatalf("expected approval_open block preserving old intent, got err=%v blocked=%+v", err, blocked)
		}
		// Verify HTTP handler exposes distinguishable code + current_intent payload.
		handler := NewHandler(svc)
		router := gin.New()
		router.POST("/purchases", func(c *gin.Context) {
			c.Set("user_id", int64(693))
			handler.PreparePurchase(c)
		})
		req := httptest.NewRequest(http.MethodPost, "/purchases", strings.NewReader(fmt.Sprintf(`{"slot_id":604,"request_key":%q}`, uuid.NewString())))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), VIPPlanSwitchCodeApprovalOpen) ||
			!strings.Contains(resp.Body.String(), oldA.ID) {
			t.Fatalf("HTTP plan-switch error response: %d %s", resp.Code, resp.Body.String())
		}
		if oldRow := read(693, oldA.ID); oldRow.status != "Pending" || oldRow.subscriptionID != oldPPA.ExternalOrderID {
			t.Fatalf("original intent mutated on blocked switch: %+v", oldRow)
		}
		// Once PayPal marks the old agreement EXPIRED, retrying Year succeeds immediately.
		pp.cancelFault = nil
		pp.subDetailsByID[oldPPA.ExternalOrderID].Status = "EXPIRED"
		yearA := prepare(693, 604, uuid.NewString())
		if yearA.ID == oldA.ID || yearA.Cycle != "year" {
			t.Fatalf("retry after expiry failed: %+v", yearA)
		}

		// Case B: PayPal verification/cancel timeout blocks with vip_plan_switch_pending and recovers once cancelled.
		oldB := prepare(694, 605, uuid.NewString())
		oldPPB := startPayPal(694, oldB.ID)
		pp.cancelFault = func(string) error { return context.DeadlineExceeded }
		beforePP, beforeSt = len(pp.agreements), stripe.vipSessionSeq
		_, err = svc.PreparePurchase(ctx, 694, 0, 604, 0, uuid.NewString(), "", checkoutAttribution{})
		blocked = nil
		if !errors.As(err, &blocked) || blocked.Code != VIPPlanSwitchCodePending ||
			blocked.CurrentIntentID != oldB.ID || len(pp.agreements) != beforePP || stripe.vipSessionSeq != beforeSt {
			t.Fatalf("expected timeout block, got err=%v blocked=%+v", err, blocked)
		}
		pp.cancelFault = nil
		pp.subDetailsByID[oldPPB.ExternalOrderID].Status = "CANCELLED"
		yearB := prepare(694, 604, uuid.NewString())
		if yearB.ID == oldB.ID || yearB.Cycle != "year" {
			t.Fatalf("retry after confirmed cancel failed: %+v", yearB)
		}

		// Case C: Concurrent approval wins -> vip_plan_switch_confirming, zero new creations.
		oldC := prepare(695, 605, uuid.NewString())
		oldPPC := startPayPal(695, oldC.ID)
		exec(`UPDATE payment_vip_checkout_intents SET switch_to_provider='stripe' WHERE intent_id=$1`, oldC.ID)
		pp.cancelFault = func(id string) error {
			pp.subDetailsByID[id].Status = "APPROVED"
			return errors.New("already approved")
		}
		defer func() { pp.cancelFault = nil }()
		beforePP, beforeSt = len(pp.agreements), stripe.vipSessionSeq
		_, err = svc.PreparePurchase(ctx, 695, 0, 604, 0, uuid.NewString(), "", checkoutAttribution{})
		blocked = nil
		if !errors.As(err, &blocked) || blocked.Code != VIPPlanSwitchCodeConfirming ||
			blocked.CurrentIntentID != oldC.ID || len(pp.agreements) != beforePP || stripe.vipSessionSeq != beforeSt {
			t.Fatalf("expected confirming block on concurrent approval, got err=%v blocked=%+v", err, blocked)
		}
		if oldRow := read(695, oldC.ID); oldRow.status != "Pending" || oldRow.switchTo != "" || oldRow.subscriptionID != oldPPC.ExternalOrderID {
			t.Fatalf("concurrent approval did not keep original intent and clear switchTo: %+v", oldRow)
		}
	})

	t.Run("#22 Stripe symmetric VIP plan switch succeeds, blocks on timeout or concurrent subscription, and recovers", func(t *testing.T) {
		// Case A: Open Stripe Month session (with switch_to_provider='paypal') closes and switches to Year in one click.
		oldA := prepare(696, 605, uuid.NewString())
		_ = startStripe(696, oldA.ID)
		exec(`UPDATE payment_vip_checkout_intents SET switch_to_provider='paypal' WHERE intent_id=$1`, oldA.ID)
		yearA := prepare(696, 604, uuid.NewString())
		if yearA.ID == oldA.ID || yearA.Cycle != "year" {
			t.Fatalf("Stripe Month -> Year switch failed: %+v", yearA)
		}
		if oldRow := read(696, oldA.ID); oldRow.status != "Expired" || oldRow.activeProvider != "" || oldRow.switchTo != "" {
			t.Fatalf("old Stripe intent not expired cleanly: %+v", oldRow)
		}
		if started := startStripe(696, yearA.ID); started.Status != "awaiting_approval" || started.ApprovalURL == "" {
			t.Fatalf("could not start replacement Year Stripe checkout: %+v", started)
		}

		// Case B: Stripe close timeout blocks with vip_plan_switch_pending and recovers once Stripe responds.
		oldB := prepare(697, 605, uuid.NewString())
		_ = startStripe(697, oldB.ID)
		beforeSt := stripe.vipSessionSeq
		stripe.closeVIPErr = context.DeadlineExceeded
		_, err := svc.PreparePurchase(ctx, 697, 0, 604, 0, uuid.NewString(), "", checkoutAttribution{})
		stripe.closeVIPErr = nil
		var blocked *VIPPlanSwitchBlockedError
		if !errors.As(err, &blocked) || blocked.Code != VIPPlanSwitchCodePending ||
			blocked.CurrentIntentID != oldB.ID || stripe.vipSessionSeq != beforeSt {
			t.Fatalf("expected Stripe timeout block, got err=%v blocked=%+v", err, blocked)
		}
		yearB := prepare(697, 604, uuid.NewString())
		if yearB.ID == oldB.ID || yearB.Cycle != "year" {
			t.Fatalf("Stripe retry after transient timeout failed: %+v", yearB)
		}

		// Case C: Concurrent Stripe subscription payment wins -> vip_plan_switch_confirming.
		oldC := prepare(698, 605, uuid.NewString())
		_ = startStripe(698, oldC.ID)
		beforeSt = stripe.vipSessionSeq
		stripe.closeVIPProof = func(proof payment.StripeVIPCheckout) payment.StripeVIPCheckout {
			proof.SubscriptionID, proof.Expired = "sub_ConcurrentPlanSwitch698", false
			return proof
		}
		defer func() { stripe.closeVIPProof = nil }()
		_, err = svc.PreparePurchase(ctx, 698, 0, 604, 0, uuid.NewString(), "", checkoutAttribution{})
		blocked = nil
		if !errors.As(err, &blocked) || blocked.Code != VIPPlanSwitchCodeConfirming ||
			blocked.CurrentIntentID != oldC.ID || stripe.vipSessionSeq != beforeSt {
			t.Fatalf("expected Stripe confirming block, got err=%v blocked=%+v", err, blocked)
		}
	})

	t.Run("#18 legacy pre-upgrade superseded PayPal agreement without marker never double-fulfills", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			userID      int64
			stripeFirst bool
		}{
			{"stripe settles before legacy late PayPal", 699, true},
			{"legacy late PayPal arrives before stripe", 700, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				intent := prepare(tc.userID, 605, uuid.NewString())
				oldPP := startPayPal(tc.userID, intent.ID)
				pp.cancelFault = func(string) error {
					return &paypal.APIError{
						Status: http.StatusNotFound,
						Name:   "RESOURCE_NOT_FOUND",
						Issues: []string{"INVALID_RESOURCE_ID"},
					}
				}
				_ = startStripe(tc.userID, intent.ID)
				pp.cancelFault = nil

				// Strip superseded_vip_intent_id to reproduce pre-upgrade legacy switched records.
				exec(`UPDATE user_subscriptions SET attribution = attribution - 'superseded_vip_intent_id', cancel_reason = NULL
					WHERE subscription_id = $1`, oldPP.ExternalOrderID)

				row := read(tc.userID, intent.ID)
				v, err := findStripeVIPIntent(ctx, pool, stripe.account, row.stripeIntentID)
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC().Truncate(time.Second)
				periodEnd := now.AddDate(0, 1, 0)
				settleStripe := func() {
					t.Helper()
					inv := payment.StripeVIPInvoice{
						ID: fmt.Sprintf("in_legacy_dup_%d", tc.userID), BillingReason: "subscription_create",
						Currency: "USD", Country: "US", Status: "paid",
						DueMinor: 1499, StartsAt: now, EndsAt: periodEnd,
						Subscription: payment.StripeVIPSubscription{
							ID: fmt.Sprintf("sub_LegacyDup%d", tc.userID), CustomerID: fmt.Sprintf("cus_LegacyDup%d", tc.userID),
							IntentID: v.ID, PriceID: v.Offer.PriceID, Status: "active",
						},
						Allocations: []payment.StripeVIPAllocation{{
							ID: fmt.Sprintf("inpay_legacy_dup_%d", tc.userID), AmountMinor: 1499,
							Receipt: payment.SingleChargeProof{
								ID: fmt.Sprintf("ch_legacy_dup_%d", tc.userID), Currency: "USD", AmountMinor: 1499,
								PaidAt: now, EligibleTerms: true, Evidence: []byte("{}"),
							},
						}},
					}
					if err := settleStripeVIPInvoice(ctx, v, inv, nil); err != nil {
						t.Fatalf("settleStripeVIPInvoice failed: %v", err)
					}
				}
				deliverLegacyPayPal := func() string {
					t.Helper()
					details := *pp.subDetailsByID[oldPP.ExternalOrderID]
					details.Status = "ACTIVE"
					details.LastPaymentTime = &now
					details.LastPaymentCents = 1499
					details.LastPaymentCurrency = "USD"
					details.CurrentPeriodEnd = &periodEnd
					pp.subDetailsByID[oldPP.ExternalOrderID] = &details
					if err := svc.applyPayPalEvent(ctx, pp, &payment.WebhookEventResult{
						EventType:      "subscription_activated",
						SubscriptionID: oldPP.ExternalOrderID,
						Account:        pp.account,
					}); err != nil {
						t.Fatalf("applyPayPalEvent failed: %v", err)
					}
					oldSub, err := repo.GetSubscriptionByID(ctx, oldPP.ExternalOrderID)
					if err != nil || oldSub == nil {
						t.Fatalf("old legacy PayPal sub: %+v %v", oldSub, err)
					}
					saleID := fmt.Sprintf("SALE-LEGACY-DUP-%d", tc.userID)
					sale := payment.SubscriptionTransaction{ID: saleID, Status: "COMPLETED", Time: now}
					sale.Amount.Gross.Currency, sale.Amount.Gross.Value = "USD", "14.99"
					if err := svc.recordPayPalSubscriptionTransaction(ctx, oldSub, &details, sale); err != nil {
						t.Fatalf("recordPayPalSubscriptionTransaction failed: %v", err)
					}
					return saleID
				}

				var saleID string
				if tc.stripeFirst {
					settleStripe()
					saleID = deliverLegacyPayPal()
				} else {
					saleID = deliverLegacyPayPal()
					settleStripe()
				}

				var totalOrders, fulfilledOrders, totalReceipts int
				if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE fulfilled_at IS NOT NULL)
					FROM recharge_orders WHERE user_id=$1 AND order_type='subscription'`, tc.userID).Scan(&totalOrders, &fulfilledOrders); err != nil {
					t.Fatal(err)
				}
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM third_party_payment_orders tp
					JOIN recharge_orders o ON o.id=tp.order_id WHERE o.user_id=$1`, tc.userID).Scan(&totalReceipts); err != nil {
					t.Fatal(err)
				}
				if totalOrders != 2 || totalReceipts != 2 || fulfilledOrders != 1 {
					t.Fatalf("expected 2 ledger orders/receipts and 1 fulfilled VIP order, got orders=%d receipts=%d fulfilled=%d",
						totalOrders, totalReceipts, fulfilledOrders)
				}
				var dupUnfulfilled, dupFlagged bool
				if err := pool.QueryRow(ctx, `SELECT fulfilled_at IS NULL AND paid_period_end IS NULL,
					COALESCE(reconciliation_note,'') LIKE '%superseded VIP checkout%'
					FROM recharge_orders WHERE external_ref_id=$1`, saleID).Scan(&dupUnfulfilled, &dupFlagged); err != nil || !dupUnfulfilled || !dupFlagged {
					t.Fatalf("legacy duplicate PayPal order not held for review: unfulfilled=%v flagged=%v err=%v", dupUnfulfilled, dupFlagged, err)
				}
			})
		}
	})

	t.Run("#19 PayPal retry when remote is ACTIVE with payment or APPROVED never returns stale approval URL", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		periodEnd := now.AddDate(0, 1, 0)

		// Case A: Remote is ACTIVE with payment, transaction not yet indexed -> enters confirming with empty ApprovalURL and probes ledger.
		iA := prepare(701, 605, uuid.NewString())
		oldA := startPayPal(701, iA.ID)
		dA := *pp.subDetailsByID[oldA.ExternalOrderID]
		dA.Status, dA.LastPaymentTime, dA.LastPaymentCents, dA.LastPaymentCurrency, dA.CurrentPeriodEnd = "ACTIVE", &now, 1499, "USD", &periodEnd
		pp.subDetailsByID[oldA.ExternalOrderID] = &dA
		listBefore := pp.listTxCalls
		gotA := startPayPal(701, iA.ID)
		if gotA.Status != "confirming" || gotA.ApprovalURL != "" || pp.listTxCalls <= listBefore {
			t.Fatalf("ACTIVE sub without indexed tx should return confirming with empty URL and probe tx: got=%+v listCalls=%d/%d",
				gotA, pp.listTxCalls, listBefore)
		}

		// Case B: Remote is ACTIVE with payment AND transaction visible in PayPal ledger -> settles immediately to completed with empty ApprovalURL.
		iB := prepare(702, 605, uuid.NewString())
		oldB := startPayPal(702, iB.ID)
		dB := *pp.subDetailsByID[oldB.ExternalOrderID]
		dB.Status, dB.LastPaymentTime, dB.LastPaymentCents, dB.LastPaymentCurrency, dB.CurrentPeriodEnd = "ACTIVE", &now, 1499, "USD", &periodEnd
		pp.subDetailsByID[oldB.ExternalOrderID] = &dB
		saleB := payment.SubscriptionTransaction{ID: "SALE-RETRY-ACTIVE-702", Status: "COMPLETED", Time: now}
		saleB.Amount.Gross.Currency, saleB.Amount.Gross.Value = "USD", "14.99"
		if pp.subTxByID == nil {
			pp.subTxByID = make(map[string][]payment.SubscriptionTransaction)
		}
		pp.subTxByID[oldB.ExternalOrderID] = []payment.SubscriptionTransaction{saleB}
		gotB := startPayPal(702, iB.ID)
		if gotB.Status != "completed" || gotB.ApprovalURL != "" {
			t.Fatalf("ACTIVE sub with visible tx should settle to completed with empty URL: got=%+v", gotB)
		}

		// Case C: Remote is APPROVED while local DB is PENDING -> activates and returns confirming with empty ApprovalURL.
		iC := prepare(703, 605, uuid.NewString())
		oldC := startPayPal(703, iC.ID)
		pp.subDetailsByID[oldC.ExternalOrderID].Status = "APPROVED"
		gotC := startPayPal(703, iC.ID)
		if gotC.Status != "confirming" || gotC.ApprovalURL != "" {
			t.Fatalf("APPROVED sub on retry should return confirming with empty URL: got=%+v", gotC)
		}
	})

	t.Run("#18 legitimate pre-cancel first PayPal payment still fulfills after cancel and unpaid reopen", func(t *testing.T) {
		for _, tc := range []struct {
			name         string
			userID       int64
			reopenStripe bool
		}{
			{"cancel then reopen PayPal unpaid", 704, false},
			{"cancel then reopen Stripe unpaid", 705, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				intent := prepare(tc.userID, 605, uuid.NewString())
				oldPP := startPayPal(tc.userID, intent.ID)

				// Buyer paid first cycle on PayPal before cancelling, but local ledger has not imported it yet.
				paidAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
				periodEnd := paidAt.AddDate(0, 1, 0)
				d := *pp.subDetailsByID[oldPP.ExternalOrderID]
				d.Status = "ACTIVE"
				d.CurrentPeriodStart = &paidAt
				d.LastPaymentTime = &paidAt
				d.LastPaymentCents = 1499
				d.LastPaymentCurrency = "USD"
				d.CurrentPeriodEnd = &periodEnd
				pp.subDetailsByID[oldPP.ExternalOrderID] = &d

				// User cancels renewal and PayPal confirms cancellation.
				if err := svc.CancelSubscription(ctx, tc.userID, oldPP.ExternalOrderID, "user cancelled renewal"); err != nil {
					t.Fatalf("CancelSubscription failed: %v", err)
				}
				if err := svc.confirmPayPalCancellation(ctx, oldPP.ExternalOrderID, "user cancelled renewal"); err != nil {
					t.Fatalf("confirmPayPalCancellation failed: %v", err)
				}
				dCancelled := *pp.subDetailsByID[oldPP.ExternalOrderID]
				dCancelled.Status = "CANCELLED"
				dCancelled.CurrentPeriodEnd = nil // PayPal clears next_billing_time after cancel; period comes from snapshot/regular cycle.
				pp.subDetailsByID[oldPP.ExternalOrderID] = &dCancelled

				// User reopens checkout (unpaid new agreement), deleting the old paypal_subscription_intents row.
				if tc.reopenStripe {
					reopenIntent := prepare(tc.userID, 604, uuid.NewString())
					_ = startStripe(tc.userID, reopenIntent.ID)
				} else {
					reopenIntent := prepare(tc.userID, 605, uuid.NewString())
					reopened := startPayPal(tc.userID, reopenIntent.ID)
					if reopened.ExternalOrderID == oldPP.ExternalOrderID {
						t.Fatalf("expected fresh unpaid PayPal agreement on reopen: %+v", reopened)
					}
				}

				// Now reconciliation imports the sole legitimate pre-cancel first payment.
				oldSub, err := repo.GetSubscriptionByID(ctx, oldPP.ExternalOrderID)
				if err != nil || oldSub == nil {
					t.Fatalf("GetSubscriptionByID: %+v %v", oldSub, err)
				}
				saleID := fmt.Sprintf("SALE-LEGIT-FIRST-%d", tc.userID)
				sale := payment.SubscriptionTransaction{ID: saleID, Status: "COMPLETED", Time: paidAt}
				sale.Amount.Gross.Currency, sale.Amount.Gross.Value = "USD", "14.99"
				if err := svc.recordPayPalSubscriptionTransaction(ctx, oldSub, &dCancelled, sale); err != nil {
					t.Fatalf("recordPayPalSubscriptionTransaction failed: %v", err)
				}

				var fulfilled bool
				var note string
				if err := pool.QueryRow(ctx, `SELECT fulfilled_at IS NOT NULL AND paid_period_end IS NOT NULL,
					COALESCE(reconciliation_note,'')
					FROM recharge_orders WHERE external_ref_id=$1`, saleID).Scan(&fulfilled, &note); err != nil || !fulfilled || note != "" {
					t.Fatalf("legitimate sole first payment was not fulfilled: fulfilled=%v note=%q err=%v", fulfilled, note, err)
				}
				hasAccess, err := svc.HasSubscriptionAccess(ctx, tc.userID)
				if err != nil || !hasAccess {
					t.Fatalf("user should have VIP access from sole legitimate first payment: access=%v err=%v", hasAccess, err)
				}
			})
		}
	})

	t.Run("#22 locally cancelled unapproved PayPal agreement still blocks plan switch while remote APPROVAL_PENDING is open", func(t *testing.T) {
		notFound404 := func(string) error {
			return &paypal.APIError{
				Status: http.StatusNotFound,
				Name:   "RESOURCE_NOT_FOUND",
				Issues: []string{"INVALID_RESOURCE_ID"},
			}
		}

		// Case A: CancelSubscription + confirmPayPalCancellation hits 404 -> local CANCELLED while remote stays APPROVAL_PENDING.
		oldA := prepare(706, 605, uuid.NewString())
		ppOldA := startPayPal(706, oldA.ID)
		if err := svc.CancelSubscription(ctx, 706, ppOldA.ExternalOrderID, "changed mind"); err != nil {
			t.Fatalf("CancelSubscription failed: %v", err)
		}
		pp.cancelFault = notFound404
		if err := svc.confirmPayPalCancellation(ctx, ppOldA.ExternalOrderID, "changed mind"); err != nil {
			pp.cancelFault = nil
			t.Fatalf("confirmPayPalCancellation failed: %v", err)
		}
		stripeBefore := stripe.vipSessionSeq
		_, err := svc.PreparePurchase(ctx, 706, 0, 604, 0, uuid.NewString(), "", checkoutAttribution{})
		pp.cancelFault = nil
		var blocked *VIPPlanSwitchBlockedError
		if !errors.As(err, &blocked) || blocked.Code != VIPPlanSwitchCodeApprovalOpen ||
			blocked.CurrentIntentID != oldA.ID || stripe.vipSessionSeq != stripeBefore {
			t.Fatalf("expected approval_open block on locally-cancelled unapproved PayPal sub, got err=%v blocked=%+v stripe=%d/%d",
				err, blocked, stripe.vipSessionSeq, stripeBefore)
		}
		if rowA := read(706, oldA.ID); rowA.status != "Pending" || rowA.subscriptionID != ppOldA.ExternalOrderID {
			t.Fatalf("original intent should be preserved while remote link is still open: %+v", rowA)
		}

		// Once PayPal remotely confirms EXPIRED, plan switch succeeds in one click and Stripe opens cleanly.
		pp.subDetailsByID[ppOldA.ExternalOrderID].Status = "EXPIRED"
		yearA := prepare(706, 604, uuid.NewString())
		if yearA.ID == oldA.ID || yearA.Cycle != "year" {
			t.Fatalf("expected Year intent after remote PayPal EXPIRED: %+v", yearA)
		}
		if started := startStripe(706, yearA.ID); started.Status != "awaiting_approval" || started.ApprovalURL == "" {
			t.Fatalf("expected fresh Stripe approval URL after remote PayPal EXPIRED: %+v", started)
		}

		// Case B: Locally-cancelled unapproved PayPal agreement with remote verification timeout -> blocks with pending.
		oldB := prepare(707, 605, uuid.NewString())
		ppOldB := startPayPal(707, oldB.ID)
		if err := svc.CancelSubscription(ctx, 707, ppOldB.ExternalOrderID, "changed mind"); err != nil {
			t.Fatalf("CancelSubscription failed: %v", err)
		}
		pp.cancelFault = notFound404
		if err := svc.confirmPayPalCancellation(ctx, ppOldB.ExternalOrderID, "changed mind"); err != nil {
			pp.cancelFault = nil
			t.Fatalf("confirmPayPalCancellation failed: %v", err)
		}
		pp.cancelFault = nil
		pp.getSubFault = func(string) error { return context.DeadlineExceeded }
		_, err = svc.PreparePurchase(ctx, 707, 0, 604, 0, uuid.NewString(), "", checkoutAttribution{})
		pp.getSubFault = nil
		blocked = nil
		if !errors.As(err, &blocked) || blocked.Code != VIPPlanSwitchCodePending || blocked.CurrentIntentID != oldB.ID {
			t.Fatalf("expected pending block when remote check times out on local cancel, got err=%v blocked=%+v", err, blocked)
		}
		if rowB := read(707, oldB.ID); rowB.status != "Pending" || rowB.subscriptionID != ppOldB.ExternalOrderID {
			t.Fatalf("original intent should be preserved on remote timeout: %+v", rowB)
		}
	})

	t.Run("#18 legacy PayPal -> Stripe -> PayPal without marker never double-fulfills in either arrival order", func(t *testing.T) {
		notFound404 := func(string) error {
			return &paypal.APIError{
				Status: http.StatusNotFound,
				Name:   "RESOURCE_NOT_FOUND",
				Issues: []string{"INVALID_RESOURCE_ID"},
			}
		}
		for _, tc := range []struct {
			name            string
			userID          int64
			oldArrivesFirst bool
		}{
			{"old PayPal arrives before new PayPal", 708, true},
			{"new PayPal arrives before old PayPal", 709, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				intent := prepare(tc.userID, 605, uuid.NewString())
				oldPP := startPayPal(tc.userID, intent.ID)

				// Switch PayPal -> Stripe (with 404 cancel rejection on unapproved oldPP), then Stripe -> PayPal (newPP).
				pp.cancelFault = notFound404
				_ = startStripe(tc.userID, intent.ID)
				pp.cancelFault = nil
				newPP := startPayPal(tc.userID, intent.ID)
				if newPP.ExternalOrderID == "" || newPP.ExternalOrderID == oldPP.ExternalOrderID {
					t.Fatalf("expected distinct new PayPal agreement after PayPal->Stripe->PayPal: old=%s new=%+v", oldPP.ExternalOrderID, newPP)
				}

				// Strip pre-upgrade markers on oldPP so it matches legacy data before superseded_vip_intent_id existed.
				exec(`UPDATE user_subscriptions SET attribution = attribution - 'superseded_vip_intent_id' - 'termination_source' - 'remote_termination_confirmed',
					cancel_reason = NULL WHERE subscription_id = $1`, oldPP.ExternalOrderID)

				now := time.Now().UTC().Truncate(time.Second)
				periodEnd := now.AddDate(0, 1, 0)
				deliverPayPal := func(subID, saleID string) {
					t.Helper()
					details := *pp.subDetailsByID[subID]
					details.Status = "ACTIVE"
					details.LastPaymentTime = &now
					details.LastPaymentCents = 1499
					details.LastPaymentCurrency = "USD"
					details.CurrentPeriodEnd = &periodEnd
					pp.subDetailsByID[subID] = &details
					sub, err := repo.GetSubscriptionByID(ctx, subID)
					if err != nil || sub == nil {
						t.Fatalf("GetSubscriptionByID(%s): %+v %v", subID, sub, err)
					}
					sale := payment.SubscriptionTransaction{ID: saleID, Status: "COMPLETED", Time: now}
					sale.Amount.Gross.Currency, sale.Amount.Gross.Value = "USD", "14.99"
					if err := svc.recordPayPalSubscriptionTransaction(ctx, sub, &details, sale); err != nil {
						t.Fatalf("recordPayPalSubscriptionTransaction(%s) failed: %v", subID, err)
					}
				}

				oldSale := fmt.Sprintf("SALE-P2S2P-OLD-%d", tc.userID)
				newSale := fmt.Sprintf("SALE-P2S2P-NEW-%d", tc.userID)
				if tc.oldArrivesFirst {
					deliverPayPal(oldPP.ExternalOrderID, oldSale)
					deliverPayPal(newPP.ExternalOrderID, newSale)
				} else {
					deliverPayPal(newPP.ExternalOrderID, newSale)
					deliverPayPal(oldPP.ExternalOrderID, oldSale)
				}

				var totalOrders, fulfilledOrders, totalReceipts int
				if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE fulfilled_at IS NOT NULL)
					FROM recharge_orders WHERE user_id=$1 AND order_type='subscription'`, tc.userID).Scan(&totalOrders, &fulfilledOrders); err != nil {
					t.Fatal(err)
				}
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM third_party_payment_orders tp
					JOIN recharge_orders o ON o.id=tp.order_id WHERE o.user_id=$1`, tc.userID).Scan(&totalReceipts); err != nil {
					t.Fatal(err)
				}
				if totalOrders != 2 || totalReceipts != 2 || fulfilledOrders != 1 {
					t.Fatalf("expected 2 real receipts/orders and 1 VIP fulfillment, got orders=%d receipts=%d fulfilled=%d",
						totalOrders, totalReceipts, fulfilledOrders)
				}
				// Verify even when an earlier agreement fulfilled first (no switch evidence on old), the new PayPal agreement's overlapping first payment is held.
				var heldCount int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM recharge_orders
					WHERE user_id=$1 AND order_type='subscription' AND fulfilled_at IS NULL AND paid_period_end IS NULL
					  AND COALESCE(reconciliation_note,'') LIKE '%superseded VIP checkout%'`, tc.userID).Scan(&heldCount); err != nil || heldCount != 1 {
					t.Fatalf("expected 1 duplicate order held for review, got %d err=%v", heldCount, err)
				}
			})
		}

		// Also verify the direct case where an overlapping PayPal subscription already fulfilled first and a new ACTIVE PayPal agreement receives an overlapping first payment.
		intentC := prepare(711, 605, uuid.NewString())
		oldC := startPayPal(711, intentC.ID)
		pp.cancelFault = notFound404
		_ = startStripe(711, intentC.ID)
		pp.cancelFault = nil
		newC := startPayPal(711, intentC.ID)
		exec(`UPDATE user_subscriptions SET attribution = attribution - 'superseded_vip_intent_id' - 'termination_source' - 'remote_termination_confirmed',
			cancel_reason = NULL WHERE subscription_id = $1`, oldC.ExternalOrderID)
		paidBeforeCancel := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
		periodEndC := paidBeforeCancel.AddDate(0, 1, 0)
		// Simulate oldC already legitimately fulfilled first (e.g. paid before cancel with CANCELLED status).
		dOldC := *pp.subDetailsByID[oldC.ExternalOrderID]
		dOldC.Status = "CANCELLED"
		dOldC.CurrentPeriodStart = &paidBeforeCancel
		dOldC.LastPaymentTime = &paidBeforeCancel
		dOldC.LastPaymentCents = 1499
		dOldC.LastPaymentCurrency = "USD"
		pp.subDetailsByID[oldC.ExternalOrderID] = &dOldC
		oldSubC, _ := repo.GetSubscriptionByID(ctx, oldC.ExternalOrderID)
		saleOldC := payment.SubscriptionTransaction{ID: "SALE-P2S2P-DIRECT-OLD-711", Status: "COMPLETED", Time: paidBeforeCancel}
		saleOldC.Amount.Gross.Currency, saleOldC.Amount.Gross.Value = "USD", "14.99"
		if err := svc.recordPayPalSubscriptionTransaction(ctx, oldSubC, &dOldC, saleOldC); err != nil {
			t.Fatalf("oldC settlement failed: %v", err)
		}
		// Now newC (status=PENDING/ACTIVE) receives an overlapping first payment -> MUST NOT double-fulfill!
		nowC := time.Now().UTC().Truncate(time.Second)
		dNewC := *pp.subDetailsByID[newC.ExternalOrderID]
		dNewC.Status = "ACTIVE"
		dNewC.LastPaymentTime = &nowC
		dNewC.LastPaymentCents = 1499
		dNewC.LastPaymentCurrency = "USD"
		endNewC := nowC.AddDate(0, 1, 0)
		dNewC.CurrentPeriodEnd = &endNewC
		pp.subDetailsByID[newC.ExternalOrderID] = &dNewC
		newSubC, _ := repo.GetSubscriptionByID(ctx, newC.ExternalOrderID)
		saleNewC := payment.SubscriptionTransaction{ID: "SALE-P2S2P-DIRECT-NEW-711", Status: "COMPLETED", Time: nowC}
		saleNewC.Amount.Gross.Currency, saleNewC.Amount.Gross.Value = "USD", "14.99"
		if err := svc.recordPayPalSubscriptionTransaction(ctx, newSubC, &dNewC, saleNewC); err != nil {
			t.Fatalf("newC settlement failed: %v", err)
		}
		_ = periodEndC
		var orders711, fulfilled711 int
		if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE fulfilled_at IS NOT NULL)
			FROM recharge_orders WHERE user_id=711 AND order_type='subscription'`).Scan(&orders711, &fulfilled711); err != nil || orders711 != 2 || fulfilled711 != 1 {
			t.Fatalf("new ACTIVE PayPal agreement double-fulfilled overlapping period: orders=%d fulfilled=%d err=%v", orders711, fulfilled711, err)
		}
	})

	t.Run("#19 locally cancelled unapproved PayPal sub that later expires remotely issues fresh link on retry", func(t *testing.T) {
		intent := prepare(710, 605, uuid.NewString())
		oldPP := startPayPal(710, intent.ID)
		if err := svc.CancelSubscription(ctx, 710, oldPP.ExternalOrderID, "changed mind"); err != nil {
			t.Fatalf("CancelSubscription failed: %v", err)
		}
		pp.cancelFault = func(string) error {
			return &paypal.APIError{
				Status: http.StatusNotFound,
				Name:   "RESOURCE_NOT_FOUND",
				Issues: []string{"INVALID_RESOURCE_ID"},
			}
		}
		if err := svc.confirmPayPalCancellation(ctx, oldPP.ExternalOrderID, "changed mind"); err != nil {
			pp.cancelFault = nil
			t.Fatalf("confirmPayPalCancellation failed: %v", err)
		}
		pp.cancelFault = nil

		// Remote PayPal later confirms EXPIRED with no payment.
		pp.subDetailsByID[oldPP.ExternalOrderID].Status = "EXPIRED"
		pp.subDetailsByID[oldPP.ExternalOrderID].LastPaymentTime = nil

		// Clicking PayPal again must confirm EXPIRED, detach old sub, and return a fresh approval URL in the same click.
		fresh := startPayPal(710, intent.ID)
		if fresh.Status != "awaiting_approval" || fresh.ApprovalURL == "" || fresh.ExternalOrderID == oldPP.ExternalOrderID {
			t.Fatalf("expected fresh approval URL after locally-cancelled sub remotely expired, got=%+v old=%s", fresh, oldPP.ExternalOrderID)
		}
		var oldStatus string
		var remoteConfirmed bool
		if err := pool.QueryRow(ctx, `SELECT status, COALESCE((attribution->>'remote_termination_confirmed')::boolean, false)
			FROM user_subscriptions WHERE subscription_id=$1`, oldPP.ExternalOrderID).Scan(&oldStatus, &remoteConfirmed); err != nil || oldStatus != "EXPIRED" || !remoteConfirmed {
			t.Fatalf("expected old sub persisted as provider-confirmed EXPIRED, got status=%s confirmed=%v err=%v", oldStatus, remoteConfirmed, err)
		}
	})

	t.Run("in-flight creation blocks switching until confirmed", func(t *testing.T) {
		intent := prepare(611, 605, uuid.NewString())
		started, unblock := make(chan struct{}), make(chan struct{})
		pp.beforeCreate = func() { close(started); <-unblock }
		result := make(chan error, 1)
		go func() {
			_, err := svc.StartPayPalCheckout(ctx, 611, intent.ID, origin)
			result <- err
		}()
		select {
		case <-started:
		case err := <-result:
			close(unblock)
			pp.beforeCreate = nil
			t.Fatalf("could not start the request: %v", err)
		}
		before := stripe.vipSessionSeq
		_, err := svc.StartStripeCheckout(ctx, 611, intent.ID, origin)
		close(unblock)
		createErr := <-result
		pp.beforeCreate = nil
		row := read(611, intent.ID)
		if !errors.Is(err, ErrSubscriptionPending) || createErr != nil || stripe.vipSessionSeq != before || row.subscriptionID == "" {
			t.Fatalf("in-flight switch: %+v start=%v switch=%v", row, createErr, err)
		}
		if got := startStripe(611, intent.ID); got.Status != "awaiting_approval" {
			t.Fatalf("could not resume switch after first create settled: %+v", got)
		}
	})

	t.Run("PayPal creation resumes original request and merchant", func(t *testing.T) {
		for i, stage := range []string{"before", "after"} {
			t.Run(stage, func(t *testing.T) {
				user := int64(612 + i)
				intent := prepare(user, 605, uuid.NewString())
				pp.createFault = stage
				requests := len(pp.agreements)
				approved := len(pp.subByRequest)
				if got := startPayPal(user, intent.ID); got.ApprovalURL != "" || got.Status != "retryable" {
					t.Fatalf("expected recoverable checkout: %+v", got)
				}
				// Pausing new sales must not redirect an already submitted request to another merchant.
				exec(`UPDATE payment_channel_accounts SET new_subscriptions_enabled=false WHERE provider='paypal'`)
				got := startPayPal(user, intent.ID)
				exec(`UPDATE payment_channel_accounts SET new_subscriptions_enabled=true WHERE provider='paypal'`)
				if got.Status != "awaiting_approval" || len(pp.agreements) != requests+2 ||
					pp.agreements[requests] != pp.agreements[requests+1] || len(pp.subByRequest) != approved+1 {
					t.Fatalf("lost create did not recover using the original request: %+v", got)
				}
			})
		}
		intent := prepare(614, 605, uuid.NewString())
		pp.createFault = "after"
		startPayPal(614, intent.ID)
		row := read(614, intent.ID)
		exec(`UPDATE paypal_subscription_intents SET submitted_at=now()-interval '25 hours' WHERE intent_id=$1`, row.paypalIntentID)
		requests := len(pp.agreements)
		if got := startPayPal(614, intent.ID); got.Status != "review" || len(pp.agreements) != requests {
			t.Fatalf("expired idempotency retry: %+v", got)
		}
	})

	t.Run("switch recovers a lost PayPal create before closing", func(t *testing.T) {
		intent := prepare(615, 605, uuid.NewString())
		pp.createFault = "after"
		startPayPal(615, intent.ID)
		row := read(615, intent.ID)
		ref := pp.subByRequest[row.paypalIntentID]
		if got := startStripe(615, intent.ID); got.Status != "awaiting_approval" || pp.subDetailsByID[ref].Status != "CANCELLED" {
			t.Fatalf("switched without closing recovered PayPal agreement: %+v", got)
		}
	})

	t.Run("approval wins the PayPal switch race", func(t *testing.T) {
		intent := prepare(616, 605, uuid.NewString())
		old := startPayPal(616, intent.ID)
		before := stripe.vipSessionSeq
		pp.cancelFault = func(id string) error {
			pp.subDetailsByID[id].Status = "APPROVED"
			return errors.New("subscription was approved")
		}
		defer func() { pp.cancelFault = nil }()
		if _, err := svc.StartStripeCheckout(ctx, 616, intent.ID, origin); !errors.Is(err, ErrCheckoutPending) {
			t.Fatalf("first switch error: %v", err)
		}
		if _, err := svc.StartStripeCheckout(ctx, 616, intent.ID, origin); !errors.Is(err, ErrSubscriptionConflict) {
			t.Fatalf("approved subscription should block the switch: %v", err)
		}
		row := read(616, intent.ID)
		if row.subscriptionID != old.ExternalOrderID || row.switchTo != "" || stripe.vipSessionSeq != before {
			t.Fatalf("approval was overwritten: %+v", row)
		}
	})

	t.Run("Stripe unknown session is looked up in the original scope", func(t *testing.T) {
		intent := prepare(617, 605, uuid.NewString())
		stripe.loseVIPReply = true
		startStripe(617, intent.ID)
		stripe.loseVIPReply = false
		row := read(617, intent.ID)
		session := stripe.vipByIntent[row.stripeIntentID]
		delete(stripe.vipByIntent, row.stripeIntentID)
		beforePayPal, beforeStripe := len(pp.agreements), stripe.vipSessionSeq
		if _, err := svc.StartPayPalCheckout(ctx, 617, intent.ID, origin); !errors.Is(err, ErrCheckoutPending) {
			t.Fatalf("unknown Stripe switch: %v", err)
		}
		if updated := read(617, intent.ID); updated.stripeIntentID != row.stripeIntentID ||
			updated.switchTo != payment.ProviderPayPal || len(pp.agreements) != beforePayPal {
			t.Fatalf("lost Stripe session was replaced: %+v", updated)
		}
		stripe.vipByIntent[row.stripeIntentID] = session
		if got := startPayPal(617, intent.ID); got.Status != "awaiting_approval" || stripe.vipSessionSeq != beforeStripe ||
			stripe.closedVIP[len(stripe.closedVIP)-1] != session {
			t.Fatalf("switch did not close the original Stripe session: %+v", got)
		}
	})

	t.Run("approval wins the Stripe switch race", func(t *testing.T) {
		intent := prepare(618, 605, uuid.NewString())
		startStripe(618, intent.ID)
		before := len(pp.agreements)
		stripe.closeVIPProof = func(proof payment.StripeVIPCheckout) payment.StripeVIPCheckout {
			proof.SubscriptionID, proof.Expired = "sub_ApprovedDuringSwitch", false
			return proof
		}
		defer func() { stripe.closeVIPProof = nil }()
		if _, err := svc.StartPayPalCheckout(ctx, 618, intent.ID, origin); !errors.Is(err, ErrSubscriptionConflict) {
			t.Fatalf("Stripe buyer approval was not honored: %v", err)
		}
		if row := read(618, intent.ID); row.activeProvider != payment.ProviderStripe || row.switchTo != "" || len(pp.agreements) != before {
			t.Fatalf("approved Stripe checkout was replaced: %+v", row)
		}
	})

	t.Run("Stripe purchase refresh observes the first-send gate", func(t *testing.T) {
		intent := prepare(619, 605, uuid.NewString())
		row := read(619, intent.ID)
		childID, err := reserveUnifiedStripeVIPIntentTx(ctx, row, origin, origin, stripe.account)
		if err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE payment_channel_accounts SET new_subscriptions_enabled=false WHERE provider='stripe'`)
		t.Cleanup(func() {
			exec(`UPDATE payment_channel_accounts SET new_subscriptions_enabled=true WHERE provider='stripe'`)
		})
		before := stripe.vipSessionSeq
		if _, err := svc.GetPurchase(ctx, 619, intent.ID, ""); err != nil || stripe.vipSessionSeq != before {
			t.Fatalf("purchase refresh created an unauthorized session: %v", err)
		}
		var sent bool
		if err := pool.QueryRow(ctx, `SELECT first_sent_at IS NOT NULL FROM payment_stripe_subscription_intents WHERE id=$1`, childID).Scan(&sent); err != nil || sent {
			t.Fatalf("paused first send was claimed: sent=%v %v", sent, err)
		}
		exec(`UPDATE payment_channel_accounts SET new_subscriptions_enabled=true WHERE provider='stripe'`)
		got, err := svc.GetPurchase(ctx, 619, intent.ID, "")
		if err != nil || got.Status != "awaiting_approval" || stripe.vipSessionSeq != before+1 {
			t.Fatalf("purchase refresh did not resume enabled checkout: %+v %v", got, err)
		}
		if err := pool.QueryRow(ctx, `SELECT first_sent_at IS NOT NULL AND retry_until>first_sent_at FROM payment_stripe_subscription_intents WHERE id=$1`, childID).Scan(&sent); err != nil || !sent {
			t.Fatalf("missing first-send evidence: sent=%v %v", sent, err)
		}
	})

	t.Run("expired request replay and inactive card slot", func(t *testing.T) {
		key := uuid.NewString()
		old := prepare(620, 604, key)
		prepare(620, 605, uuid.NewString())
		replay := prepare(620, 604, key)
		if replay.ID != old.ID || replay.Status != "closed" || replay.ConfirmationMessage == "" {
			t.Fatalf("replayed quote: %+v", replay)
		}
		for _, start := range []func(context.Context, int64, string, string) (*PurchaseIntent, error){
			svc.StartPayPalCheckout, svc.StartStripeCheckout,
		} {
			if _, err := start(ctx, 620, old.ID, origin); !errors.Is(err, ErrVIPQuoteChanged) {
				t.Fatalf("expired quote was started: %v", err)
			}
		}
		if row := read(620, old.ID); row.activeProvider != "" || row.paypalIntentID != "" || row.stripeIntentID != "" {
			t.Fatalf("expired quote reserved a provider: %+v", row)
		}

		existing := prepare(621, 605, uuid.NewString())
		submitted := startPayPal(621, existing.ID)
		unsubmitted := prepare(622, 605, uuid.NewString())
		exec(`INSERT INTO recharge_templates(id,name,is_default) VALUES(799,'Other template',false);
			UPDATE recharge_slots SET template_id=799 WHERE id=605`)
		defer exec(`UPDATE recharge_slots SET template_id=600 WHERE id=605`)
		if _, err := svc.PreparePurchase(ctx, 623, 0, 605, 0, uuid.NewString(), "", checkoutAttribution{}); !errors.Is(err, ErrCheckoutUnavailable) {
			t.Fatalf("cross-template stale card expected ErrCheckoutUnavailable, got: %v", err)
		}
		if _, err := svc.StartStripeCheckout(ctx, 622, unsubmitted.ID, origin); !errors.Is(err, ErrVIPQuoteChanged) {
			t.Fatalf("old template checkout started: %v", err)
		}
		if resumed := startPayPal(621, existing.ID); resumed.ExternalOrderID != submitted.ExternalOrderID || resumed.Status != "awaiting_approval" {
			t.Fatalf("submitted agreement could not resume: %+v", resumed)
		}
	})

	t.Run("concurrent plan replacement and provider start leave one pending checkout", func(t *testing.T) {
		year := prepare(626, 604, uuid.NewString())
		started := make(chan struct{})
		startResult := make(chan error, 1)
		prepareResult := make(chan error, 1)
		go func() {
			<-started
			_, err := svc.StartPayPalCheckout(ctx, 626, year.ID, origin)
			startResult <- err
		}()
		go func() {
			<-started
			_, err := svc.PreparePurchase(ctx, 626, 0, 605, 0, uuid.NewString(), "", checkoutAttribution{})
			prepareResult <- err
		}()
		close(started)
		startErr, prepErr := <-startResult, <-prepareResult
		row := read(626, year.ID)
		switch {
		case startErr == nil && errors.Is(prepErr, ErrSubscriptionConflict):
			if row.status != "Pending" || row.paypalIntentID == "" {
				t.Fatalf("started year plan lost its reservation: %+v", row)
			}
		case errors.Is(startErr, ErrVIPQuoteChanged) && prepErr == nil:
			if row.status != "Expired" || row.paypalIntentID != "" {
				t.Fatalf("superseded year plan started: %+v", row)
			}
		case startErr == nil && prepErr == nil:
			// StartPayPalCheckout completed first, then PreparePurchase safely cancelled
			// the unapproved Year agreement and replaced it with a single Pending Month checkout.
			if row.status != "Expired" || row.paypalIntentID != "" {
				t.Fatalf("replaced year plan kept active reservation: %+v", row)
			}
		default:
			t.Fatalf("replace and start: %v / %v", prepErr, startErr)
		}
		var pendingCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_vip_checkout_intents WHERE user_id=626 AND status='Pending'`).Scan(&pendingCount); err != nil || pendingCount != 1 {
			t.Fatalf("expected exactly one pending VIP checkout after race, got %d err=%v", pendingCount, err)
		}
	})

	t.Run("VIP conflict is returned and audited as a subscription", func(t *testing.T) {
		handler := NewHandler(svc)
		router := gin.New()
		router.POST("/purchases", func(c *gin.Context) {
			c.Set("user_id", int64(603)) // Already fulfilled the year VIP in the main fixture.
			handler.PreparePurchase(c)
		})
		body := fmt.Sprintf(`{"slot_id":605,"request_key":%q}`, uuid.NewString())
		req := httptest.NewRequest(http.MethodPost, "/purchases", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "subscription_conflict") ||
			!strings.Contains(resp.Body.String(), ErrSubscriptionConflict.Error()) {
			t.Fatalf("conflict response: %d %s", resp.Code, resp.Body.String())
		}
		var reason string
		if err := pool.QueryRow(ctx, `SELECT reason FROM payment_checkout_decisions
			WHERE user_id=603 AND stage='purchase_intent' AND outcome='block' ORDER BY id DESC LIMIT 1`).Scan(&reason); err != nil || reason != string(reasonVIPConflict) {
			t.Fatalf("conflict audit: %q %v", reason, err)
		}
	})

	t.Run("Stripe purchase history tracks the initial invoice independently of access", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		for _, tc := range []struct {
			name                                    string
			userID                                  int64
			initialStart, boundary, renewalEnd      time.Time
			refundRenewal, accessAfterInitialRefund bool
		}{
			{"only initial period covers today", 625, now.AddDate(0, -1, 1), now.AddDate(0, 0, 1), now.AddDate(0, 1, 1), true, false},
			{"a separate paid period still covers today", 627, now.AddDate(0, -2, 0), now.AddDate(0, -1, 0), now.AddDate(0, 0, 1), false, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				intent := prepare(tc.userID, 605, uuid.NewString())
				startStripe(tc.userID, intent.ID)
				row := read(tc.userID, intent.ID)
				v, err := findStripeVIPIntent(ctx, pool, stripe.account, row.stripeIntentID)
				if err != nil {
					t.Fatal(err)
				}
				pay := func(kind, name string, start, end time.Time) string {
					t.Helper()
					ref := fmt.Sprintf("%s_%d", name, tc.userID)
					charge := "ch_" + ref
					invoice := payment.StripeVIPInvoice{
						ID: "in_" + ref, BillingReason: kind, Currency: "USD", Country: "US", Status: "paid",
						DueMinor: 1499, StartsAt: start, EndsAt: end,
						Subscription: payment.StripeVIPSubscription{
							ID: fmt.Sprintf("sub_Review%d", tc.userID), CustomerID: fmt.Sprintf("cus_Review%d", tc.userID),
							IntentID: v.ID, PriceID: v.Offer.PriceID, Status: "active",
						},
						Allocations: []payment.StripeVIPAllocation{{
							ID: "inpay_" + ref, AmountMinor: 1499,
							Receipt: payment.SingleChargeProof{ID: charge, Currency: "USD", AmountMinor: 1499,
								PaidAt: start, EligibleTerms: true, Evidence: []byte("{}")},
						}},
					}
					if err := settleStripeVIPInvoice(ctx, v, invoice, nil); err != nil {
						t.Fatal(err)
					}
					return charge
				}
				refund := func(charge string) {
					t.Helper()
					tx, err := pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(ctx)
					if err := applyStripeRefundTx(ctx, tx, stripe.account, &payment.StripeRefundProof{
						ID: "re_" + charge, ChargeID: charge, AmountMinor: 1499, Currency: "USD", Status: "succeeded",
					}); err != nil {
						t.Fatal(err)
					}
					if err := tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}
				}
				check := func(status string, wantAccess bool) {
					t.Helper()
					list, err := svc.ListRecentPurchases(ctx, tc.userID)
					active, accessErr := svc.HasSubscriptionAccess(ctx, tc.userID)
					if err != nil || accessErr != nil || active != wantAccess || len(list) != 1 ||
						list[0].ID != intent.ID || list[0].Status != status {
						t.Fatalf("purchase history: %+v access=%v errors=%v/%v", list, active, err, accessErr)
					}
				}
				first := pay("subscription_create", "reviewFirst", tc.initialStart, tc.boundary)
				renewal := pay("subscription_cycle", "reviewRenewal", tc.boundary, tc.renewalEnd)
				check("completed", true)
				if tc.refundRenewal {
					refund(renewal)
					check("completed", true)
				}
				refund(first)
				check("refunded", tc.accessAfterInitialRefund)
			})
		}
	})

	t.Run("small PayPal payments remain on ledger without VIP fulfillment", func(t *testing.T) {
		intent := prepare(624, 604, uuid.NewString())
		started := startPayPal(624, intent.ID)
		sub, err := repo.GetSubscriptionByID(ctx, started.ExternalOrderID)
		if err != nil || sub == nil {
			t.Fatalf("subscription: %+v %v", sub, err)
		}
		now := time.Now().UTC().Truncate(time.Second)
		periodEnd := now.AddDate(1, 0, 0)
		details := *pp.subDetailsByID[sub.SubscriptionID]
		details.Status, details.LastPaymentTime, details.LastPaymentCents, details.LastPaymentCurrency = "ACTIVE", &now, 1, "USD"
		r := repo.(*dbRepository)
		snapshot := details
		snapshot.CurrentPeriodEnd = &periodEnd
		if err := r.savePayPalBillingSnapshot(ctx, &snapshot); err != nil {
			t.Fatal(err)
		}
		sale := payment.SubscriptionTransaction{ID: "review-1-cent", Status: "COMPLETED", Time: now}
		sale.Amount.Gross.Currency, sale.Amount.Gross.Value = "USD", "0.01"
		if err := svc.recordPayPalSubscriptionTransaction(ctx, sub, &details, sale); err != nil {
			t.Fatal(err)
		}
		var recorded, reviewed, noEvents bool
		err = pool.QueryRow(ctx, `SELECT o.amount_cents=1 AND EXISTS(SELECT 1 FROM third_party_payment_orders p WHERE p.order_id=o.id),
			o.reconciliation_note LIKE '%below the agreement price%' AND o.paid_period_end IS NULL AND o.fulfilled_at IS NULL,
			NOT EXISTS(SELECT 1 FROM reader_conversion_outbox x WHERE x.order_id=o.id AND x.event_name IN ('Purchase','Subscribe'))
			FROM recharge_orders o WHERE o.external_ref_id=$1`, sale.ID).Scan(&recorded, &reviewed, &noEvents)
		active, accessErr := svc.HasSubscriptionAccess(ctx, sub.UserID)
		if err != nil || accessErr != nil || !recorded || !reviewed || !noEvents || active {
			t.Fatalf("underpaid order: recorded=%v review=%v noEvents=%v access=%v err=%v/%v", recorded, reviewed, noEvents, active, err, accessErr)
		}
		// Financial and billing evidence for a full-price late payment can still arrive independently.
		sale.ID, sale.Amount.Gross.Value = "review-full-late", "99.99"
		snapshot.LastPaymentCents = 9999
		if err := r.savePayPalBillingSnapshot(ctx, &snapshot); err != nil {
			t.Fatal(err)
		}
		if err := svc.recordPayPalSubscriptionTransaction(ctx, sub, &details, sale); err != nil {
			t.Fatal(err)
		}
		active, err = svc.HasSubscriptionAccess(ctx, sub.UserID)
		if err != nil || !active {
			t.Fatalf("late paid period failed: %v %v", active, err)
		}
		if err := r.CompletePayPalRefund(ctx, "review-first-refund", sale.ID, 9999, "USD", pp.account); err != nil {
			t.Fatal(err)
		}
		list, err := svc.ListRecentPurchases(ctx, sub.UserID)
		active, accessErr = svc.HasSubscriptionAccess(ctx, sub.UserID)
		if err != nil || accessErr != nil || active || len(list) != 1 || list[0].ID != intent.ID || list[0].Status != "refunded" {
			t.Fatalf("PayPal refund history: %+v active=%v errors=%v/%v", list, active, err, accessErr)
		}

	})
}

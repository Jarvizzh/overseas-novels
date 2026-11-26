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
	exec(`INSERT INTO users(id) SELECT generate_series(680,684)`)

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
		default:
			t.Fatalf("replace and start: %v / %v", prepErr, startErr)
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

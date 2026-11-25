package application

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/payment"
)

func testUnifiedVIPRereview(t *testing.T, svc *service, pool *pgxpool.Pool, pp *sixFlowPayPalAdapter, stripe *sixFlowStripeAdapter, origin string) {
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	prepare := func(user int64, slot int, key string) (*PurchaseIntent, error) {
		return svc.PreparePurchase(ctx, user, 0, slot, 0, key, "", checkoutAttribution{})
	}
	month := func(user int64) (vipCheckoutIntentRow, string) {
		t.Helper()
		key := uuid.NewString()
		v, err := prepare(user, 605, key)
		if err != nil {
			t.Fatal(err)
		}
		row, err := readVIPCheckoutIntent(ctx, user, v.ID)
		if err != nil {
			t.Fatal(err)
		}
		return row, key
	}
	start := func(user int64, id, provider string) (*PurchaseIntent, error) {
		if provider == payment.ProviderPayPal {
			return svc.StartPayPalCheckout(ctx, user, id, origin)
		}
		return svc.StartStripeCheckout(ctx, user, id, origin)
	}
	reserve := func(t *testing.T, row vipCheckoutIntentRow, provider string) string {
		t.Helper()
		var id string
		var err error
		if provider == payment.ProviderPayPal {
			id, err = reserveUnifiedPayPalVIPIntentTx(ctx, row, origin, origin, pp.account)
		} else {
			id, err = reserveUnifiedStripeVIPIntentTx(ctx, row, origin, origin, stripe.account)
		}
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	setBinding := func(row vipCheckoutIntentRow, provider string, enabled bool, validation, price string) {
		t.Helper()
		exec(`UPDATE product_channel_bindings SET enabled=$3,validation_status=$4,external_price_ref=$5
			WHERE product_id=$1 AND provider=$2`, row.productID, provider, enabled, validation, price)
	}
	exec(`INSERT INTO users(id) SELECT generate_series(630,648)`)

	for _, channel := range []struct {
		provider, other, price string
		user                   int64
	}{
		{payment.ProviderPayPal, payment.ProviderStripe, "P-SIX-MONTH", 630},
		{payment.ProviderStripe, payment.ProviderPayPal, "price_VIP_MONTH", 635},
	} {
		t.Run(channel.provider, func(t *testing.T) {
			for i, change := range []struct {
				name              string
				enabled           bool
				validation, price string
			}{
				{"disabled", false, "verified", channel.price},
				{"invalidated", true, "failed", channel.price},
				{"price replaced", true, "verified", channel.price + "_new"},
			} {
				t.Run(change.name, func(t *testing.T) {
					user := channel.user + int64(i)
					row, _ := month(user)
					child := reserve(t, row, channel.provider)
					setBinding(row, channel.provider, change.enabled, change.validation, change.price)
					defer setBinding(row, channel.provider, true, "verified", channel.price)

					beforePayPal, beforeStripe := len(pp.agreements), len(stripe.vipCreates)
					view, err := svc.GetPurchase(ctx, user, row.intentID, "")
					if err != nil || view.ApprovalURL != "" || view.ConfirmationMessage != ErrCheckoutPaused.Error() {
						t.Fatalf("refresh did not report the blocked channel: %+v %v", view, err)
					}
					view, err = start(user, row.intentID, channel.provider)
					if err != nil && !errors.Is(err, ErrCheckoutPaused) || err == nil && (view.ApprovalURL != "" || view.ConfirmationMessage != ErrCheckoutPaused.Error()) {
						t.Fatalf("start did not report a blocked channel: %+v %v", view, err)
					}
					svc.reconcileStripeVIP(ctx)
					var sent bool
					query := `SELECT submitted_at IS NOT NULL FROM paypal_subscription_intents WHERE intent_id=$1`
					if channel.provider == payment.ProviderStripe {
						query = `SELECT first_sent_at IS NOT NULL FROM payment_stripe_subscription_intents WHERE id=$1`
					}
					if err := pool.QueryRow(ctx, query, child).Scan(&sent); err != nil || sent {
						t.Fatalf("unexpected first send: sent=%v err=%v", sent, err)
					}
					if len(pp.agreements) != beforePayPal || len(stripe.vipCreates) != beforeStripe {
						t.Fatal("a revoked binding sent a create")
					}
					view, err = start(user, row.intentID, channel.other)
					if err != nil || view.Status != "awaiting_approval" {
						t.Fatalf("alternate checkout: %+v %v", view, err)
					}
				})
			}

			t.Run("recover original request after binding revocation", func(t *testing.T) {
				user := channel.user + 3
				row, _ := month(user)
				pp.createFault, stripe.loseVIPReply = "", false
				if channel.provider == payment.ProviderPayPal {
					pp.createFault = "after"
				} else {
					stripe.loseVIPReply = true
				}
				beforePayPal, beforeStripe := len(pp.agreements), len(stripe.vipCreates)
				view, err := start(user, row.intentID, channel.provider)
				stripe.loseVIPReply = false
				if err != nil || view.ApprovalURL != "" {
					t.Fatalf("lost reply: %+v %v", view, err)
				}
				setBinding(row, channel.provider, false, "verified", channel.price)
				defer setBinding(row, channel.provider, true, "verified", channel.price)
				if channel.provider == payment.ProviderStripe {
					exec(`UPDATE payment_stripe_subscription_intents SET next_check_at=now()
						WHERE id=(SELECT stripe_intent_id FROM payment_vip_checkout_intents WHERE intent_id=$1)`, row.intentID)
				}
				view, err = start(user, row.intentID, channel.provider)
				if err != nil || view.Status != "awaiting_approval" {
					t.Fatalf("recover previously sent request: %+v %v", view, err)
				}
				if channel.provider == payment.ProviderPayPal {
					if len(pp.agreements) != beforePayPal+2 || pp.agreements[beforePayPal] != pp.agreements[beforePayPal+1] {
						t.Fatal("PayPal did not replay the original request")
					}
				} else {
					initial := stripe.vipCreates[beforeStripe]
					for _, retry := range stripe.vipCreates[beforeStripe:] {
						if retry.IntentID != initial.IntentID || retry.Offer.PriceID != initial.Offer.PriceID {
							t.Fatal("Stripe changed the original request")
						}
					}
					if stripe.vipByIntent[initial.IntentID] == "" || view.ApprovalURL != "https://checkout.stripe.com/c/pay/"+stripe.vipByIntent[initial.IntentID] {
						t.Fatalf("approval did not use the original session: %+v", view)
					}
				}
			})

			t.Run("resolve persisted switch before checking target", func(t *testing.T) {
				user := channel.user + 4
				row, _ := month(user)
				old, err := start(user, row.intentID, channel.other)
				if err != nil || old.Status != "awaiting_approval" {
					t.Fatalf("source checkout: %+v %v", old, err)
				}
				if channel.other == payment.ProviderPayPal {
					pp.cancelFault = func(string) error { return context.DeadlineExceeded }
				} else {
					stripe.closeVIPErr = context.DeadlineExceeded
				}
				defer func() { pp.cancelFault, stripe.closeVIPErr = nil, nil }()
				beforePayPal, beforeStripe := len(pp.agreements), len(stripe.vipCreates)
				if _, err := start(user, row.intentID, channel.provider); !errors.Is(err, ErrCheckoutPending) {
					t.Fatalf("initial switch: %v", err)
				}
				setBinding(row, channel.provider, false, "verified", channel.price)
				defer setBinding(row, channel.provider, true, "verified", channel.price)
				if _, err := start(user, row.intentID, channel.provider); !errors.Is(err, ErrCheckoutPending) {
					t.Fatalf("unconfirmed old checkout: %v", err)
				}
				if channel.other == payment.ProviderPayPal {
					pp.subDetailsByID[old.ExternalOrderID].Status = "CANCELLED"
				}
				stripe.closeVIPErr = nil
				if _, err := start(user, row.intentID, channel.provider); !errors.Is(err, ErrCheckoutPaused) {
					t.Fatalf("target became unavailable: %v", err)
				}
				ready, err := svc.GetPurchase(ctx, user, row.intentID, "")
				if err != nil || ready.Status != "ready" || ready.Provider != "" || len(pp.agreements) != beforePayPal || len(stripe.vipCreates) != beforeStripe {
					t.Fatalf("closed source did not release the checkout: %+v %v", ready, err)
				}
				year, err := prepare(user, 604, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				view, err := start(user, year.ID, channel.provider)
				if err != nil || view.Status != "awaiting_approval" {
					t.Fatalf("replacement checkout: %+v %v", view, err)
				}
			})
		})
	}

	t.Run("a stale Stripe quote is released only before the first claim", func(t *testing.T) {
		before := len(stripe.vipCreates)
		for _, test := range []struct {
			name                string
			user                int64
			firstClaim, refresh bool
		}{
			{"expiry by refresh", 640, false, true},
			{"expiry during plan replacement", 641, false, false},
			{"first claim wins", 642, true, false},
		} {
			t.Run(test.name, func(t *testing.T) {
				row, key := month(test.user)
				child := reserve(t, row, payment.ProviderStripe)
				var claimed stripeVIPIntent
				if test.firstClaim {
					jobs, err := claimStripeVIP(ctx, stripe.account, child, true)
					if err != nil || len(jobs) != 1 || !jobs[0].MayCreate {
						t.Fatalf("claim before quote was removed: %+v %v", jobs, err)
					}
					claimed = jobs[0]
				}

				exec(`UPDATE recharge_slots SET template_id=799 WHERE id=605`)
				defer exec(`UPDATE recharge_slots SET template_id=600 WHERE id=605`)
				if test.refresh || test.firstClaim {
					view, err := svc.GetPurchase(ctx, test.user, row.intentID, "")
					if err != nil || (view.Status == "closed") == test.firstClaim {
						t.Fatalf("old quote state: %+v %v", view, err)
					}
				}
				year, err := prepare(test.user, 604, uuid.NewString())
				if test.firstClaim {
					if !errors.Is(err, ErrSubscriptionConflict) {
						t.Fatalf("already claimed attempt did not block replacement: %+v %v", year, err)
					}
					if err := svc.syncStripeVIP(ctx, stripe, claimed, ""); err != nil {
						t.Fatal(err)
					}
					view, err := svc.GetPurchase(ctx, test.user, row.intentID, "")
					if err != nil || view.Status != "awaiting_approval" || len(stripe.vipCreates) != before+1 {
						t.Fatalf("original claim did not finish: %+v %v", view, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				closed, err := prepare(test.user, 605, key)
				if err != nil || closed.ID != row.intentID || closed.Status != "closed" || closed.ApprovalURL != "" {
					t.Fatalf("old request replay: %+v %v", closed, err)
				}
				jobs, err := claimStripeVIP(ctx, stripe.account, child, true)
				if err != nil || len(jobs) != 0 || len(stripe.vipCreates) != before {
					t.Fatalf("released attempt was claimed: %+v %v", jobs, err)
				}
				view, err := start(test.user, year.ID, payment.ProviderPayPal)
				if err != nil || view.Status != "awaiting_approval" {
					t.Fatalf("replacement checkout: %+v %v", view, err)
				}
			})
		}
	})
	t.Run("a stale PayPal quote releases only a never-sent idle checkout", func(t *testing.T) {
		for n, first := range []string{"refresh", "request replay", "plan replacement", "creation lease", "channel switch", "first send"} {
			t.Run(first, func(t *testing.T) {
				user := int64(643 + n)
				row, key := month(user)
				child := reserve(t, row, payment.ProviderPayPal)
				before := len(pp.agreements)
				if first == "first send" {
					pp.createFault = "after"
					view, err := start(user, row.intentID, payment.ProviderPayPal)
					if err != nil || view == nil || view.ApprovalURL != "" || len(pp.agreements) != before+1 {
						t.Fatalf("lost PayPal reply: %+v %v", view, err)
					}
				}

				exec(`UPDATE recharge_slots SET template_id=799 WHERE id=605`)
				defer exec(`UPDATE recharge_slots SET template_id=600 WHERE id=605`)
				switch first {
				case "refresh":
					view, err := svc.GetPurchase(ctx, user, row.intentID, "")
					if err != nil || view == nil || view.Status != "closed" {
						t.Fatalf("stale PayPal quote after refresh: %+v %v", view, err)
					}
				case "request replay":
					view, err := prepare(user, 605, key)
					if err != nil || view == nil || view.Status != "closed" {
						t.Fatalf("stale PayPal quote after replay: %+v %v", view, err)
					}
				case "creation lease":
					legacy, err := pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer legacy.Rollback(ctx)
					if _, err := legacy.Exec(ctx, `UPDATE paypal_subscription_intents
						SET lease_until=now()+interval '5 minutes' WHERE intent_id=$1`, child); err != nil {
						t.Fatal(err)
					}
					if _, err := prepare(user, 604, uuid.NewString()); !errors.Is(err, ErrSubscriptionConflict) {
						t.Fatalf("locked PayPal attempt did not block replacement: %v", err)
					}
					if err := legacy.Commit(ctx); err != nil {
						t.Fatal(err)
					}
					if _, err := prepare(user, 604, uuid.NewString()); !errors.Is(err, ErrSubscriptionConflict) {
						t.Fatalf("leased PayPal attempt did not block replacement: %v", err)
					}
					exec(`UPDATE paypal_subscription_intents SET lease_until=now()-interval '1 second' WHERE intent_id=$1`, child)
				case "channel switch":
					exec(`UPDATE payment_vip_checkout_intents SET switch_to_provider='stripe' WHERE intent_id=$1`, row.intentID)
					if _, err := prepare(user, 604, uuid.NewString()); !errors.Is(err, ErrSubscriptionConflict) {
						t.Fatalf("PayPal switch in progress did not block replacement: %v", err)
					}
					exec(`UPDATE payment_vip_checkout_intents SET switch_to_provider=NULL WHERE intent_id=$1`, row.intentID)
				case "first send":
					if _, err := prepare(user, 604, uuid.NewString()); !errors.Is(err, ErrSubscriptionConflict) {
						t.Fatalf("already sent PayPal attempt did not block replacement: %v", err)
					}
					view, err := start(user, row.intentID, payment.ProviderPayPal)
					if err != nil || view == nil || view.Status != "awaiting_approval" ||
						len(pp.agreements) != before+2 || pp.agreements[before] != pp.agreements[before+1] ||
						view.ExternalOrderID != pp.subByRequest[child] {
						t.Fatalf("original PayPal request did not recover: %+v %v", view, err)
					}
					return
				}

				year, err := prepare(user, 604, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				replay, err := prepare(user, 605, key)
				if err != nil || replay == nil || replay.ID != row.intentID || replay.Status != "closed" || replay.ApprovalURL != "" {
					t.Fatalf("expired PayPal quote replay: %+v %v", replay, err)
				}
				if _, err := start(user, row.intentID, payment.ProviderPayPal); !errors.Is(err, ErrVIPQuoteChanged) {
					t.Fatalf("expired PayPal quote could still start: %v", err)
				}
				var exists bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM paypal_subscription_intents WHERE intent_id=$1)`, child).Scan(&exists); err != nil || exists {
					t.Fatalf("old PayPal slot is still occupied: exists=%t %v", exists, err)
				}
				if len(pp.agreements) != before {
					t.Fatal("stale PayPal quote sent a create")
				}
				view, err := start(user, year.ID, payment.ProviderPayPal)
				if err != nil || view == nil || view.Status != "awaiting_approval" ||
					len(pp.agreements) != before+1 || pp.agreements[before].PlanID != "P-SIX-YEAR" {
					t.Fatalf("replacement PayPal checkout: %+v %v", view, err)
				}
			})
		}
	})

}

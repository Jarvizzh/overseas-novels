package wallet

import (
	"context"
	"testing"
	"time"

	"reader-backend/internal/model"
	"reader-backend/internal/testdb"
)

func TestSubscriptionClassificationUsesSettlementEnvironment(t *testing.T) {
	const environment = "settlement"
	t.Setenv("CONVERSION_ENVIRONMENT", environment)
	pool := testdb.Open(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO users(id) VALUES (1)")
	at := time.Now().UTC()
	writeVerifiedOrder := func(sub, ref string, paidAt time.Time, priorEnvironment string) int64 {
		t.Helper()
		var id int64
		err := pool.QueryRow(ctx, `
			INSERT INTO recharge_orders(user_id,external_ref_id,amount_cents,payment_method,status,order_type,subscription_id,
			                            paid_at,fulfilled_at,fb_lead_metadata,capi_expectation)
			VALUES(1,$1,999,'paypal','Success','subscription',$2,$3,now(),'{"pixel_id":"scope-pixel"}',
			       CASE WHEN $4::text<>'' THEN jsonb_build_object('environment',$4::text) END)
			RETURNING id`, ref, sub, paidAt, priorEnvironment).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO third_party_payment_orders(order_id,payment_provider,external_order_id,capture_id,gross_amount,status)
			VALUES($1,'paypal',$2,$3,9.99,'VERIFIED')`, id, sub, ref)
		return id
	}
	repo := &dbRepository{}
	for _, subscriptionState := range []string{"claimed", "historical"} {
		for _, marker := range []struct {
			name, environment string
			renewal           bool
		}{
			{"same", environment, true},
			{"other", "other-environment", false},
			{"unmarked", "", true},
		} {
			for _, entry := range []string{"wallet", "legacy"} {
				t.Run(subscriptionState+"/"+marker.name+"/"+entry, func(t *testing.T) {
					subID := "I-" + subscriptionState + "-" + marker.name + "-" + entry
					capture := "capture-" + subID
					previous := writeVerifiedOrder(subID, "prior-"+subID, at.AddDate(0, -1, 0), marker.environment)
					exec(`INSERT INTO user_subscriptions(user_id,subscription_id,capi_subscribe_state,capi_subscribe_order_id,attribution)
						VALUES(1,$1,$2,CASE WHEN $2='claimed' THEN $3::bigint END,'{"pixel_id":"scope-pixel"}')`,
						subID, subscriptionState, previous)

					if entry == "wallet" {
						sub, err := repo.GetSubscriptionByID(ctx, subID)
						if err != nil || sub == nil {
							t.Fatalf("subscription=%+v, err=%v", sub, err)
						}
						if _, err := repo.RecordSubscriptionRenewalOrderTx(ctx, sub, capture, 999, "USD", at, at.AddDate(0, 1, 0), "ACTIVE",
							&model.ThirdPartyPaymentOrder{Status: "COMPLETED", RawPayload: "{}"}); err != nil {
							t.Fatal(err)
						}
					} else {
						order := writeVerifiedOrder(subID, capture, at, "")
						exec(`SELECT record_reader_payment_conversion($1,$2,'paypal_subscription_ledger',$3,false,true)`, order, at, environment)
					}
					var kind, action, recovered, savedEnvironment string
					err := pool.QueryRow(ctx, `
						SELECT COALESCE(q.snapshot->>'payment_kind','unknown'),q.snapshot->>'action_source',
						       COALESCE(reader_verified_subscription_payment_kind($2,o.id,o.user_id),'unknown'),q.environment
						FROM recharge_orders o JOIN reader_conversion_outbox q ON q.order_id=o.id AND q.event_name='Purchase'
						WHERE o.external_ref_id=$1`, capture, environment).Scan(&kind, &action, &recovered, &savedEnvironment)
					wantKind, wantAction := "unknown", "website"
					if marker.renewal {
						wantKind, wantAction = "subscription_renewal", "system_generated"
					}
					if err != nil || kind != wantKind || action != wantAction || recovered != wantKind || savedEnvironment != environment {
						t.Fatalf("kind/action/recovered/env=%q/%q/%q/%q, want %q/%q/%q/%q: %v",
							kind, action, recovered, savedEnvironment, wantKind, wantAction, wantKind, environment, err)
					}
				})
			}
		}
	}
}

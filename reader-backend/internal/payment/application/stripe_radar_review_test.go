package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/stripe"
	"reader-backend/internal/testdb"
)

type radarReviewStub struct {
	account      payment.WebhookAccount
	getCalls     int
	approveCalls int
	lastKey      string
	getFn        func(ctx context.Context, id string) (*stripego.Review, error)
	approveFn    func(ctx context.Context, id, key string) (*stripego.Review, error)
	resolveFn    func(ctx context.Context, raw json.RawMessage) (*payment.CaseObservation, error)
}

func (r *radarReviewStub) Provider() string                     { return payment.ProviderStripe }
func (r *radarReviewStub) AccountScope() payment.WebhookAccount { return r.account }
func (r *radarReviewStub) GetReview(ctx context.Context, id string) (*stripego.Review, error) {
	r.getCalls++
	return r.getFn(ctx, id)
}
func (r *radarReviewStub) ApproveReview(ctx context.Context, id, key string) (*stripego.Review, error) {
	r.approveCalls++
	r.lastKey = key
	return r.approveFn(ctx, id, key)
}
func (r *radarReviewStub) ResolveQueuedAftercare(ctx context.Context, raw json.RawMessage) (*payment.CaseObservation, error) {
	if r.resolveFn != nil {
		return r.resolveFn(ctx, raw)
	}
	return nil, nil
}

type radarFixtureIDs struct {
	accountID int64
	orderID   int64
	receiptID int64
	caseID    int64
	attemptID uuid.UUID
	vipID     uuid.UUID
}

func setupRadarFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountKey, reviewID, chargeID string, includeVIP bool) radarFixtureIDs {
	t.Helper()
	var ids radarFixtureIDs

	if _, err := pool.Exec(ctx, `INSERT INTO admins(id,username,password_hash,role,role_id,status)
		VALUES('radar-admin','radar-admin','hash','FinanceManager',(SELECT id FROM roles WHERE code='FinanceManager'),1)
		ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('stripe',$1,'sandbox','test','test') RETURNING id`, accountKey).Scan(&ids.accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_aftercare_policies(account_id,hold_fulfillment,reviewer_id,finance_id,review_seconds,refund_seconds)
		VALUES($1,true,'radar-admin','radar-admin',3600,3600)`, ids.accountID); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users(email,password_hash,nickname) VALUES($1,'hash','reader') RETURNING id`,
		reviewID+"@example.com").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES($1,0,0) ON CONFLICT DO NOTHING`, userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO recharge_orders(user_id,external_ref_id,amount_cents,currency,coins,bonus_coins_credited,payment_method,status,order_type,paid_at,checkout_environment)
		VALUES($1,$2,1000,'USD',1000,0,'stripe','Paid','single',now(),'sandbox') RETURNING id`, userID, "ext_"+reviewID).Scan(&ids.orderID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO third_party_payment_orders(order_id,account_id,object_type,payment_provider,external_order_id,capture_id,currency,gross_amount,status)
		VALUES($1,$2,'stripe_charge','stripe',$3,$4,'USD',10.00,'VERIFIED') RETURNING id`,
		ids.orderID, ids.accountID, "pi_"+reviewID, chargeID).Scan(&ids.receiptID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO payment_cases(account_id,kind,external_id,receipt_refs,provider_status,is_open,work_state,observed_at)
		VALUES($1,'risk_review',$2,ARRAY[$3::text],'open',true,'pending',now()-interval '1 minute') RETURNING id`,
		ids.accountID, reviewID, chargeID).Scan(&ids.caseID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_deferred_fulfillment(order_id,receipt_id,case_id,next_check_at)
		VALUES($1,$2,$3,now()+interval '1 hour')`, ids.orderID, ids.receiptID, ids.caseID); err != nil {
		t.Fatal(err)
	}
	ids.attemptID = uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_checkout_attempts(id,order_id,provider,account_id,sales_mode,status,return_url,cancel_url,next_create_at)
		VALUES($1,$2,'stripe',$3,'direct','settled','https://example.com/r','https://example.com/c',now()+interval '1 hour')`,
		ids.attemptID, ids.orderID, ids.accountID); err != nil {
		t.Fatal(err)
	}
	if includeVIP {
		ids.vipID = uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO payment_vip_offer_versions(slot_id,template_id,slot_index,amount_minor,currency,cycle,name,description)
			VALUES(9101,1,1,1000,'USD','month','VIP Monthly','VIP') ON CONFLICT (slot_id) DO NOTHING`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO payment_stripe_subscription_intents(
			id,user_id,account_id,checkout_environment,offer_version_id,slot_id,template_id,product_ref,price_ref,amount_minor,cycle,sales_mode,allowed_countries,return_url,cancel_url,return_path,status,next_check_at)
			VALUES($1,$2,$3,'sandbox',9101,9101,1,'prod_vip','price_vip',1000,'month','direct',ARRAY['US'],'https://example.com/r','https://example.com/c','/','bound',now()+interval '1 hour')`,
			ids.vipID, userID, ids.accountID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO payment_stripe_invoice_periods(account_id,intent_id,invoice_ref,order_id,billed_minor,billing_reason,admitted)
			VALUES($1,$2,$3,$4,1000,'subscription_create',false)`,
			ids.accountID, ids.vipID, "in_"+reviewID, ids.orderID); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func evaluateHold(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, receiptID int64) aftercare.FulfillmentState {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	state, err := aftercare.EvaluateFulfillment(ctx, tx, orderID, receiptID)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestStripeRadarApprovalTimeoutThenLookupConvergesAndWakesOnce(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	ctx := t.Context()
	pool := testdb.Open(t)
	ids := setupRadarFixture(t, ctx, pool, "acct_radar_timeout", "prv_timeout", "ch_timeout", true)

	cmdID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_stripe_review_approvals(
		id,case_id,account_id,environment,review_id,idempotency_key,requested_by,reason,status)
		VALUES($1,$2,$3,'sandbox','prv_timeout','radar-approve:timeout','radar-admin','verified buyer','pending_confirmation')`,
		cmdID, ids.caseID, ids.accountID); err != nil {
		t.Fatal(err)
	}

	// Entitlement must remain held while approval is pending_confirmation.
	if hold := evaluateHold(t, ctx, pool, ids.orderID, ids.receiptID); !hold.Deferred {
		t.Fatal("entitlement was not held while Stripe approval was pending_confirmation")
	}

	account := payment.WebhookAccount{AccountKey: "acct_radar_timeout", Environment: "sandbox"}
	stub := &radarReviewStub{
		account: account,
		approveFn: func(_ context.Context, id, key string) (*stripego.Review, error) {
			// Remote approval succeeded on Stripe, but response timed out / dropped.
			return nil, &stripe.APIError{Disposition: stripe.LookupOnly}
		},
		getFn: func(_ context.Context, id string) (*stripego.Review, error) {
			return &stripego.Review{
				ID: id, Livemode: false, Open: false,
				ClosedReason: stripego.ReviewClosedReasonApproved,
				Charge:       &stripego.Charge{ID: "ch_timeout"},
			}, nil
		},
	}
	s := &service{adapters: payment.NewAdapters(stub), stripeEventsWake: make(chan struct{}, 1)}

	// Cycle 1: ApproveReview times out -> command stays pending_confirmation with lookup_only=true.
	s.reconcileStripeRadarApprovals(ctx)
	var status string
	var lookupOnly bool
	if err := pool.QueryRow(ctx, `SELECT status,lookup_only FROM payment_stripe_review_approvals WHERE id=$1`, cmdID).Scan(&status, &lookupOnly); err != nil || status != stripeReviewApprovalPending || !lookupOnly {
		t.Fatalf("after timeout status=%q lookupOnly=%v err=%v", status, lookupOnly, err)
	}
	if stub.approveCalls != 1 || stub.getCalls != 0 {
		t.Fatalf("unexpected calls after timeout: approve=%d get=%d", stub.approveCalls, stub.getCalls)
	}
	if hold := evaluateHold(t, ctx, pool, ids.orderID, ids.receiptID); !hold.Deferred {
		t.Fatal("entitlement released before Stripe confirmed approval")
	}

	// Make command due immediately for Cycle 2.
	if _, err := pool.Exec(ctx, `UPDATE payment_stripe_review_approvals SET next_attempt_at=now()-interval '1 second' WHERE id=$1`, cmdID); err != nil {
		t.Fatal(err)
	}
	s.reconcileStripeRadarApprovals(ctx)

	// Cycle 2 must re-fetch GetReview and converge WITHOUT calling ApproveReview again.
	if stub.getCalls != 1 || stub.approveCalls != 1 {
		t.Fatalf("expected 1 GetReview and no second ApproveReview: get=%d approve=%d", stub.getCalls, stub.approveCalls)
	}
	var workState string
	var attemptDue, vipDue bool
	if err := pool.QueryRow(ctx, `SELECT ra.status,c.work_state,
		(SELECT next_create_at<=now()+interval '5 seconds' FROM payment_checkout_attempts WHERE id=$3),
		(SELECT next_check_at<=now()+interval '5 seconds' FROM payment_stripe_subscription_intents WHERE id=$4)
		FROM payment_stripe_review_approvals ra JOIN payment_cases c ON c.id=ra.case_id WHERE ra.id=$1 AND c.id=$2`,
		cmdID, ids.caseID, ids.attemptID, ids.vipID).Scan(&status, &workState, &attemptDue, &vipDue); err != nil {
		t.Fatal(err)
	}
	if status != stripeReviewApprovalConfirmed || workState != "approved" || !attemptDue || !vipDue {
		t.Fatalf("post-convergence state: status=%q workState=%q attemptDue=%v vipDue=%v", status, workState, attemptDue, vipDue)
	}
	// Entitlement is now eligible to fulfill.
	if hold := evaluateHold(t, ctx, pool, ids.orderID, ids.receiptID); hold.Deferred {
		t.Fatal("entitlement stayed deferred after Stripe confirmed approval")
	}
}

func TestStripeRadarApprovalExplicitRejectionKeepsHold(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	ctx := t.Context()
	pool := testdb.Open(t)
	ids := setupRadarFixture(t, ctx, pool, "acct_radar_reject", "prv_reject", "ch_reject", false)

	cmdID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_stripe_review_approvals(
		id,case_id,account_id,environment,review_id,idempotency_key,requested_by,reason,status)
		VALUES($1,$2,$3,'sandbox','prv_reject','radar-approve:reject','radar-admin','try approve','pending_confirmation')`,
		cmdID, ids.caseID, ids.accountID); err != nil {
		t.Fatal(err)
	}

	account := payment.WebhookAccount{AccountKey: "acct_radar_reject", Environment: "sandbox"}
	stub := &radarReviewStub{
		account: account,
		approveFn: func(_ context.Context, _, _ string) (*stripego.Review, error) {
			return nil, &stripe.APIError{Disposition: stripe.Rejected, Status: http.StatusBadRequest}
		},
		getFn: func(_ context.Context, id string) (*stripego.Review, error) {
			return &stripego.Review{ID: id, Livemode: false, Open: true, Charge: &stripego.Charge{ID: "ch_reject"}}, nil
		},
	}
	s := &service{adapters: payment.NewAdapters(stub)}
	s.reconcileStripeRadarApprovals(ctx)

	var status, workState string
	if err := pool.QueryRow(ctx, `SELECT ra.status,c.work_state FROM payment_stripe_review_approvals ra JOIN payment_cases c ON c.id=ra.case_id WHERE ra.id=$1`,
		cmdID).Scan(&status, &workState); err != nil {
		t.Fatal(err)
	}
	if status != stripeReviewApprovalFailed || workState != "pending" {
		t.Fatalf("explicit rejection: status=%q workState=%q", status, workState)
	}
	if hold := evaluateHold(t, ctx, pool, ids.orderID, ids.receiptID); !hold.Deferred {
		t.Fatal("entitlement must remain held after explicit rejection")
	}
}

func TestStripeRadarSimultaneousCMSAndDashboardApprovalConverges(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	ctx := t.Context()
	pool := testdb.Open(t)
	ids := setupRadarFixture(t, ctx, pool, "acct_radar_dash", "prv_dash", "ch_dash", false)

	cmdID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_stripe_review_approvals(
		id,case_id,account_id,environment,review_id,idempotency_key,requested_by,reason,status)
		VALUES($1,$2,$3,'sandbox','prv_dash','radar-approve:dash','radar-admin','approve','pending_confirmation')`,
		cmdID, ids.caseID, ids.accountID); err != nil {
		t.Fatal(err)
	}

	account := payment.WebhookAccount{AccountKey: "acct_radar_dash", Environment: "sandbox"}
	stub := &radarReviewStub{
		account: account,
		approveFn: func(_ context.Context, _, _ string) (*stripego.Review, error) {
			// Stripe rejects ApproveReview because Dashboard already approved it.
			return nil, &stripe.APIError{Disposition: stripe.Rejected, Status: http.StatusBadRequest}
		},
		getFn: func(_ context.Context, id string) (*stripego.Review, error) {
			return &stripego.Review{
				ID: id, Livemode: false, Open: false,
				ClosedReason: stripego.ReviewClosedReasonApproved,
				Charge:       &stripego.Charge{ID: "ch_dash"},
			}, nil
		},
	}
	s := &service{adapters: payment.NewAdapters(stub), stripeEventsWake: make(chan struct{}, 1)}
	s.reconcileStripeRadarApprovals(ctx)

	var status, workState string
	if err := pool.QueryRow(ctx, `SELECT ra.status,c.work_state FROM payment_stripe_review_approvals ra JOIN payment_cases c ON c.id=ra.case_id WHERE ra.id=$1`,
		cmdID).Scan(&status, &workState); err != nil {
		t.Fatal(err)
	}
	if status != stripeReviewApprovalConfirmed || workState != "approved" {
		t.Fatalf("simultaneous Dashboard approval: status=%q workState=%q", status, workState)
	}
}

func TestStripeRadarReviewClosedReplayAndNonApprovedReasonAndRefundedAfterApproval(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	ctx := t.Context()
	pool := testdb.Open(t)

	// Case A: review.closed(refunded_as_fraud) keeps refund_due follow-up and fails pending approval.
	idsFraud := setupRadarFixture(t, ctx, pool, "acct_radar_fraud", "prv_fraud", "ch_fraud", false)
	fraudCmd := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_stripe_review_approvals(
		id,case_id,account_id,environment,review_id,idempotency_key,requested_by,reason,status)
		VALUES($1,$2,$3,'sandbox','prv_fraud','radar-approve:fraud','radar-admin','approve','pending_confirmation')`,
		fraudCmd, idsFraud.caseID, idsFraud.accountID); err != nil {
		t.Fatal(err)
	}
	s := &service{stripeEventsWake: make(chan struct{}, 1)}
	fraudAccount := payment.WebhookAccount{AccountKey: "acct_radar_fraud", Environment: "sandbox"}
	if err := s.convergeStripeRadarReview(ctx, stripeReviewObservation{
		Account: fraudAccount, ReviewID: "prv_fraud", ChargeID: "ch_fraud",
		Open: false, ClosedReason: "refunded_as_fraud", Source: "webhook", SourceRef: "evt_fraud_1", ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	var status, workState string
	if err := pool.QueryRow(ctx, `SELECT ra.status,c.work_state FROM payment_stripe_review_approvals ra JOIN payment_cases c ON c.id=ra.case_id WHERE ra.id=$1`,
		fraudCmd).Scan(&status, &workState); err != nil {
		t.Fatal(err)
	}
	if status != stripeReviewApprovalFailed || workState != "refund_due" {
		t.Fatalf("refunded_as_fraud: status=%q workState=%q", status, workState)
	}
	if hold := evaluateHold(t, ctx, pool, idsFraud.orderID, idsFraud.receiptID); !hold.Deferred {
		t.Fatal("entitlement must remain held when closed_reason != approved")
	}

	// Case B: review.closed(approved) replayed twice + already refunded after approval.
	idsReplay := setupRadarFixture(t, ctx, pool, "acct_radar_replay", "prv_replay", "ch_replay", false)
	replayAccount := payment.WebhookAccount{AccountKey: "acct_radar_replay", Environment: "sandbox"}
	stub := &radarReviewStub{
		account: replayAccount,
		resolveFn: func(_ context.Context, _ json.RawMessage) (*payment.CaseObservation, error) {
			return &payment.CaseObservation{
				Fact: payment.CaseFact{
					Provider: payment.ProviderStripe, Account: replayAccount, Kind: payment.CaseRiskReview,
					ExternalID: "prv_replay", ReceiptRefs: []string{"ch_replay"},
					Status: "closed", Stage: "review.closed", Outcome: stripeReviewClosedApproved,
					Open: false, ObservedAt: time.Now().UTC(), Source: "webhook", SourceRef: "evt_replay",
					Evidence: json.RawMessage(`{"Closed":"approved"}`),
				},
			}, nil
		},
	}
	s.adapters = payment.NewAdapters(stub)
	for range 2 {
		if err := s.processStripeAftercareNotice(ctx, replayAccount, json.RawMessage(`{"id":"evt_replay"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT work_state FROM payment_cases WHERE id=$1`, idsReplay.caseID).Scan(&workState); err != nil || workState != "approved" {
		t.Fatalf("review.closed replay workState=%q err=%v", workState, err)
	}
	// Simulate an active refund command placed right after Radar approval: fulfillment must stay blocked!
	if _, err := pool.Exec(ctx, `INSERT INTO payment_refund_commands(id,environment,actor_id,request_key,order_id,receipt_id,amount_minor,currency,status)
		VALUES($1,'sandbox','radar-admin',$2,$3,$4,1000,'USD','pending')`,
		uuid.New(), uuid.New(), idsReplay.orderID, idsReplay.receiptID); err != nil {
		t.Fatal(err)
	}
	if hold := evaluateHold(t, ctx, pool, idsReplay.orderID, idsReplay.receiptID); !hold.Deferred {
		t.Fatal("order with pending refund after Radar approval must not fulfill")
	}
	_ = errors.Is(io.EOF, io.EOF)
}

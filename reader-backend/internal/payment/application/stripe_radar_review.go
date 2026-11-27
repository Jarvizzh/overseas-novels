package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/stripe"
)

const (
	stripeReviewApprovalPending   = "pending_confirmation"
	stripeReviewApprovalConfirmed = "confirmed"
	stripeReviewApprovalFailed    = "failed"

	stripeReviewClosedApproved = stripe.ReviewClosedReasonApproved
	stripeReviewClosedDisputed = string(stripego.ReviewClosedReasonDisputed)

	stripeReviewApprovalBatchSize      = 5
	stripeReviewApprovalLeaseDuration  = 2 * time.Minute
	stripeReviewApprovalRetryBackoff   = time.Minute
	stripeReviewApprovalRequestTimeout = 30 * time.Second
)

type stripeReviewAdapter interface {
	AccountScope() payment.WebhookAccount
	GetReview(context.Context, string) (*stripego.Review, error)
	ApproveReview(context.Context, string, string) (*stripego.Review, error)
}

type stripeReviewApprovalCommand struct {
	id             uuid.UUID
	caseID         int64
	accountID      int64
	environment    string
	reviewID       string
	idempotencyKey string
	lookupOnly     bool
	firstSentAt    *time.Time
	retryUntil     *time.Time
	leaseToken     uuid.UUID
}

type stripeReviewObservation struct {
	Account      payment.WebhookAccount
	ReviewID     string
	ChargeID     string
	Open         bool
	Reason       string
	OpenedReason string
	ClosedReason string
	Source       string
	SourceRef    string
	ObservedAt   time.Time
	Evidence     json.RawMessage
}

func (o stripeReviewObservation) toCaseFact() payment.CaseFact {
	status := "closed"
	if o.Open {
		status = "open"
	}
	var refs []string
	if o.ChargeID != "" {
		refs = []string{o.ChargeID}
	}
	evidence := o.Evidence
	if !json.Valid(evidence) {
		evidence, _ = json.Marshal(struct {
			Opened string `json:"Opened"`
			Closed string `json:"Closed"`
		}{Opened: o.OpenedReason, Closed: o.ClosedReason})
	}
	observed := o.ObservedAt
	if observed.IsZero() {
		observed = time.Now().UTC()
	}
	return payment.CaseFact{
		Provider:    payment.ProviderStripe,
		Account:     o.Account,
		Kind:        payment.CaseRiskReview,
		ExternalID:  o.ReviewID,
		ReceiptRefs: refs,
		Status:      status,
		Stage:       "review." + status,
		Reason:      o.Reason,
		Outcome:     o.ClosedReason,
		Open:        o.Open,
		ObservedAt:  observed,
		Source:      o.Source,
		SourceRef:   o.SourceRef,
		Evidence:    evidence,
	}
}

func stripeReviewObservationFromAPI(account payment.WebhookAccount, review *stripego.Review, sourceRef string) (stripeReviewObservation, error) {
	if review == nil || review.ID == "" {
		return stripeReviewObservation{}, errors.New("empty Stripe review response")
	}
	expectLive := account.Environment == string(stripe.Live)
	if review.Livemode != expectLive {
		return stripeReviewObservation{}, fmt.Errorf("%w: review %s livemode=%t env=%s",
			stripe.ErrEventResourceMismatch, review.ID, review.Livemode, account.Environment)
	}
	var chargeID string
	if review.Charge != nil {
		chargeID = review.Charge.ID
	}
	evidence, err := json.Marshal(struct {
		Opened string `json:"Opened"`
		Closed string `json:"Closed"`
	}{Opened: string(review.OpenedReason), Closed: string(review.ClosedReason)})
	if err != nil {
		return stripeReviewObservation{}, err
	}
	return stripeReviewObservation{
		Account:      account,
		ReviewID:     review.ID,
		ChargeID:     chargeID,
		Open:         review.Open,
		Reason:       string(review.Reason),
		OpenedReason: string(review.OpenedReason),
		ClosedReason: string(review.ClosedReason),
		Source:       "provider_api",
		SourceRef:    sourceRef,
		ObservedAt:   time.Now().UTC(),
		Evidence:     evidence,
	}, nil
}

func stripeReviewObservationFromCaseFact(fact payment.CaseFact) stripeReviewObservation {
	var chargeID string
	if len(fact.ReceiptRefs) > 0 {
		chargeID = fact.ReceiptRefs[0]
	}
	return stripeReviewObservation{
		Account:      fact.Account,
		ReviewID:     fact.ExternalID,
		ChargeID:     chargeID,
		Open:         fact.Open,
		Reason:       fact.Reason,
		ClosedReason: fact.Outcome,
		Source:       fact.Source,
		SourceRef:    fact.SourceRef,
		ObservedAt:   fact.ObservedAt,
		Evidence:     fact.Evidence,
	}
}

// processStripeAftercareNotice routes queued Stripe aftercare observations
// through the shared Radar review convergence path when kind == risk_review.
func (s *service) processStripeAftercareNotice(ctx context.Context, account payment.WebhookAccount, snapshot json.RawMessage) error {
	adapter, err := payment.Capability[interface {
		ResolveQueuedAftercare(context.Context, json.RawMessage) (*payment.CaseObservation, error)
	}](s.adapters, payment.ProviderStripe)
	if err != nil {
		return err
	}
	evidence, err := adapter.ResolveQueuedAftercare(ctx, snapshot)
	if err != nil {
		return err
	}
	if evidence == nil {
		return nil
	}
	if evidence.Fact.Kind == payment.CaseRiskReview {
		obs := stripeReviewObservationFromCaseFact(evidence.Fact)
		if obs.Account == (payment.WebhookAccount{}) {
			obs.Account = account
		}
		return s.convergeStripeRadarReview(ctx, obs)
	}
	return aftercare.NewRepository(db.Pool(ctx)).RecordObservation(ctx, evidence)
}

// convergeStripeRadarReview is the single idempotent state transition shared by
// worker approval submissions, Stripe Dashboard approvals, and review.closed webhooks.
// Local work_state becomes "approved" and deferred orders wake up ONLY when
// Stripe confirms the Review is closed with closed_reason == "approved".
func (s *service) convergeStripeRadarReview(ctx context.Context, obs stripeReviewObservation) error {
	if obs.ReviewID == "" || obs.Account.AccountKey == "" || obs.Account.Environment == "" {
		return errors.New("incomplete Stripe review observation")
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if err := aftercare.RecordTx(ctx, tx, obs.toCaseFact()); err != nil {
		return err
	}
	var accountID, caseID int64
	var workState string
	err = tx.QueryRow(ctx, `SELECT c.account_id,c.id,c.work_state
		FROM payment_cases c JOIN payment_channel_accounts a ON a.id=c.account_id
		WHERE a.provider='stripe' AND a.account_key=$1 AND a.environment=$2
		  AND c.kind='risk_review' AND c.external_id=$3
		FOR UPDATE OF c`, obs.Account.AccountKey, obs.Account.Environment, obs.ReviewID).
		Scan(&accountID, &caseID, &workState)
	if err != nil {
		return err
	}

	wakeStripe := false
	switch {
	case obs.Open:
		// Still open on Stripe: hold entitlement and leave work_state untouched.
	case obs.ClosedReason == stripeReviewClosedApproved:
		if _, err := tx.Exec(ctx, `UPDATE payment_stripe_review_approvals
			SET status=$4,closed_reason=$5,lease_token=NULL,lease_until=NULL,last_error=NULL,
			    finished_at=COALESCE(finished_at,clock_timestamp()),updated_at=clock_timestamp()
			WHERE account_id=$1 AND environment=$2 AND review_id=$3`,
			accountID, obs.Account.Environment, obs.ReviewID,
			stripeReviewApprovalConfirmed, stripeReviewClosedApproved); err != nil {
			return err
		}
		if workState == "observed" || workState == "pending" || workState == "refund_due" || workState == "dispute_due" {
			if _, err := tx.Exec(ctx, `UPDATE payment_cases SET work_state='approved',updated_at=clock_timestamp() WHERE id=$1`, caseID); err != nil {
				return err
			}
		}
		if err := wakeStripeDeferredOrdersTx(ctx, tx, accountID, caseID); err != nil {
			return err
		}
		wakeStripe = true
	default:
		failNote := fmt.Sprintf("Stripe review closed with reason %q", obs.ClosedReason)
		if _, err := tx.Exec(ctx, `UPDATE payment_stripe_review_approvals
			SET status=$4,closed_reason=$5,lease_token=NULL,lease_until=NULL,last_error=left($6,500),
			    finished_at=COALESCE(finished_at,clock_timestamp()),updated_at=clock_timestamp()
			WHERE account_id=$1 AND environment=$2 AND review_id=$3 AND status=$7`,
			accountID, obs.Account.Environment, obs.ReviewID,
			stripeReviewApprovalFailed, obs.ClosedReason, failNote, stripeReviewApprovalPending); err != nil {
			return err
		}
		if workState == "pending" {
			nextState := "refund_due"
			if obs.ClosedReason == stripeReviewClosedDisputed {
				nextState = "dispute_due"
			} else {
				var hasOpenDispute bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(
					SELECT 1 FROM payment_case_receipts cr
					JOIN payment_case_receipts dr ON dr.receipt_id=cr.receipt_id
					JOIN payment_cases d ON d.id=dr.case_id AND d.kind='dispute' AND d.is_open
					WHERE cr.case_id=$1)`, caseID).Scan(&hasOpenDispute); err != nil {
					return err
				}
				if hasOpenDispute {
					nextState = "dispute_due"
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE payment_cases SET work_state=$2,updated_at=clock_timestamp() WHERE id=$1`, caseID, nextState); err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if wakeStripe {
		notifyWorker(s.stripeEventsWake)
	}
	return nil
}

// wakeStripeDeferredOrdersTx reschedules any single checkout attempt and/or VIP
// subscription intent linked to the approved Radar review case so normal
// verification (payment, dispute, refund, cycle) runs immediately.
func wakeStripeDeferredOrdersTx(ctx context.Context, tx pgx.Tx, accountID, caseID int64) error {
	if _, err := tx.Exec(ctx, `UPDATE payment_deferred_fulfillment
		SET next_check_at=now()
		WHERE case_id=$1 AND resolved_at IS NULL`, caseID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_checkout_attempts p
		SET next_create_at=now()
		WHERE p.provider='stripe' AND p.account_id=$1
		  AND p.order_id IN (
			SELECT h.order_id FROM payment_deferred_fulfillment h WHERE h.case_id=$2 AND h.resolved_at IS NULL
			UNION
			SELECT r.order_id FROM payment_case_receipts r WHERE r.case_id=$2
		  )`, accountID, caseID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_stripe_subscription_intents v
		SET next_check_at=now()
		WHERE v.account_id=$1
		  AND v.id IN (
			SELECT ip.intent_id FROM payment_stripe_invoice_periods ip
			WHERE ip.account_id=$1 AND ip.order_id IN (
				SELECT h.order_id FROM payment_deferred_fulfillment h WHERE h.case_id=$2 AND h.resolved_at IS NULL
				UNION
				SELECT r.order_id FROM payment_case_receipts r WHERE r.case_id=$2
			)
		  )`, accountID, caseID); err != nil {
		return err
	}
	return nil
}

func claimStripeReviewApprovals(ctx context.Context, account payment.WebhookAccount) ([]stripeReviewApprovalCommand, error) {
	lease := uuid.New()
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT ra.id FROM payment_stripe_review_approvals ra
		JOIN payment_channel_accounts a ON a.id=ra.account_id
		WHERE a.provider='stripe' AND a.account_key=$1 AND a.environment=$2
		  AND ra.status=$3 AND ra.next_attempt_at<=now()
		  AND (ra.lease_until IS NULL OR ra.lease_until<=now())
		ORDER BY ra.next_attempt_at,ra.created_at,ra.id
		LIMIT $4 FOR UPDATE OF ra SKIP LOCKED
	)
	UPDATE payment_stripe_review_approvals ra
	SET lease_token=$5,lease_until=now()+make_interval(secs=>$6::int),updated_at=clock_timestamp()
	FROM due WHERE ra.id=due.id
	RETURNING ra.id,ra.case_id,ra.account_id,ra.environment,ra.review_id,ra.idempotency_key,
	          ra.lookup_only,ra.first_sent_at,ra.retry_until`,
		account.AccountKey, account.Environment, stripeReviewApprovalPending,
		stripeReviewApprovalBatchSize, lease, int(stripeReviewApprovalLeaseDuration/time.Second))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (stripeReviewApprovalCommand, error) {
		var cmd stripeReviewApprovalCommand
		cmd.leaseToken = lease
		err := row.Scan(&cmd.id, &cmd.caseID, &cmd.accountID, &cmd.environment, &cmd.reviewID,
			&cmd.idempotencyKey, &cmd.lookupOnly, &cmd.firstSentAt, &cmd.retryUntil)
		return cmd, err
	})
}

func (s *service) reconcileStripeRadarApprovals(ctx context.Context) {
	adapter, err := payment.Capability[stripeReviewAdapter](s.adapters, payment.ProviderStripe)
	if err != nil {
		return
	}
	account := adapter.AccountScope()
	commands, err := claimStripeReviewApprovals(ctx, account)
	if err != nil {
		log.Printf("task=stripe_radar_approval action=claim error=%v", err)
		return
	}
	for _, cmd := range commands {
		reqCtx, cancel := context.WithTimeout(ctx, stripeReviewApprovalRequestTimeout)
		err := s.executeStripeReviewApproval(reqCtx, adapter, account, cmd)
		cancel()
		if err != nil {
			log.Printf("task=stripe_radar_approval action=execute review=%s error=%v", cmd.reviewID, err)
		}
	}
}

func (s *service) executeStripeReviewApproval(ctx context.Context, adapter stripeReviewAdapter, account payment.WebhookAccount, cmd stripeReviewApprovalCommand) error {
	// If a prior attempt was already sent or timed out, re-fetch the original Review
	// before deciding whether to retry ApproveReview.
	if cmd.lookupOnly || cmd.firstSentAt != nil {
		review, err := adapter.GetReview(ctx, cmd.reviewID)
		if err != nil {
			return rescheduleStripeReviewApproval(ctx, cmd, true, err)
		}
		obs, err := stripeReviewObservationFromAPI(account, review, "review-lookup:"+cmd.id.String())
		if err != nil {
			return failStripeReviewApproval(ctx, cmd, "", err)
		}
		if !obs.Open {
			return s.convergeStripeRadarReview(ctx, obs)
		}
		if cmd.retryUntil != nil && !time.Now().Before(*cmd.retryUntil) {
			return failStripeReviewApproval(ctx, cmd, "", errors.New("Stripe review approval idempotency window expired"))
		}
	} else {
		if err := markStripeReviewApprovalSent(ctx, cmd); err != nil {
			return err
		}
	}

	approved, err := adapter.ApproveReview(ctx, cmd.reviewID, cmd.idempotencyKey)
	if err == nil {
		obs, convErr := stripeReviewObservationFromAPI(account, approved, "review-approve:"+cmd.id.String())
		if convErr != nil {
			return failStripeReviewApproval(ctx, cmd, "", convErr)
		}
		return s.convergeStripeRadarReview(ctx, obs)
	}

	var apiErr *stripe.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Disposition {
		case stripe.RetrySameKey:
			return rescheduleStripeReviewApproval(ctx, cmd, false, err)
		case stripe.Rejected:
			// Handle simultaneous CMS + Stripe Dashboard approval/closure: re-fetch
			// the authoritative Review before declaring failure.
			review, fetchErr := adapter.GetReview(ctx, cmd.reviewID)
			if fetchErr == nil {
				obs, convErr := stripeReviewObservationFromAPI(account, review, "review-recheck:"+cmd.id.String())
				if convErr == nil && !obs.Open {
					return s.convergeStripeRadarReview(ctx, obs)
				}
			}
			return failStripeReviewApproval(ctx, cmd, "", err)
		}
	}
	// Timeout, lost response, or 5xx: switch to lookup-first before any retry.
	return rescheduleStripeReviewApproval(ctx, cmd, true, err)
}

func markStripeReviewApprovalSent(ctx context.Context, cmd stripeReviewApprovalCommand) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_stripe_review_approvals
		SET first_sent_at=COALESCE(first_sent_at,clock_timestamp()),
		    retry_until=COALESCE(retry_until,clock_timestamp()+make_interval(secs=>$3::int)),
		    updated_at=clock_timestamp()
		WHERE id=$1 AND lease_token=$2`,
		cmd.id, cmd.leaseToken, int(stripe.IdempotencyWindow/time.Second))
	return err
}

func rescheduleStripeReviewApproval(ctx context.Context, cmd stripeReviewApprovalCommand, lookupOnly bool, cause error) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_stripe_review_approvals
		SET lookup_only=lookup_only OR $3,
		    lease_token=NULL,lease_until=NULL,
		    last_error=left($4,500),
		    next_attempt_at=now()+make_interval(secs=>$5::int),
		    updated_at=clock_timestamp()
		WHERE id=$1 AND lease_token=$2 AND status=$6`,
		cmd.id, cmd.leaseToken, lookupOnly, cause.Error(),
		int(stripeReviewApprovalRetryBackoff/time.Second), stripeReviewApprovalPending)
	return err
}

func failStripeReviewApproval(ctx context.Context, cmd stripeReviewApprovalCommand, closedReason string, cause error) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_stripe_review_approvals
		SET status=$3,closed_reason=COALESCE(NULLIF($4,''),closed_reason),
		    lease_token=NULL,lease_until=NULL,
		    last_error=left($5,500),
		    finished_at=COALESCE(finished_at,clock_timestamp()),
		    updated_at=clock_timestamp()
		WHERE id=$1 AND lease_token=$2 AND status=$6`,
		cmd.id, cmd.leaseToken, stripeReviewApprovalFailed, closedReason,
		cause.Error(), stripeReviewApprovalPending)
	return err
}

package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
)

type checkoutDecisionStage string
type checkoutOutcome string
type checkoutReason string

const (
	purchaseIntentStage     checkoutDecisionStage = "purchase_intent"
	paymentAttemptStage     checkoutDecisionStage = "payment_attempt"
	subscriptionIntentStage checkoutDecisionStage = "subscription_intent"

	checkoutAllow  checkoutOutcome = "allow"
	checkoutBlock  checkoutOutcome = "block"
	checkoutVerify checkoutOutcome = "verify"
	checkoutReview checkoutOutcome = "review"

	reasonEligible           checkoutReason = "eligible"
	reasonExistingIntent     checkoutReason = "resume_existing_intent"
	reasonNotEligible        checkoutReason = "purchase_ineligible"
	reasonRequestKeyConflict checkoutReason = "request_key_conflict"
	reasonWholeBookConflict  checkoutReason = "whole_book_already_reserved_or_owned"
	reasonVIPConflict        checkoutReason = "vip_agreement_or_intent_in_progress"
	reasonVIPPreparing       checkoutReason = "vip_intent_preparing"
	reasonAttemptInProgress  checkoutReason = "payment_attempt_in_progress"
	reasonChannelPaused      checkoutReason = "channel_paused"
	reasonSubscriptionReview checkoutReason = "subscription_requires_reconciliation"
	reasonAttemptReview      checkoutReason = "payment_attempt_requires_reconciliation"

	checkoutRuleVersion = 1
)

type checkoutEvaluation struct {
	stage      checkoutDecisionStage
	userID     int64
	provider   string
	accountID  int64
	orderID    int64
	checkoutID string
	slotID     int
	resumed    bool
	assessment checkoutDecision
}

type checkoutDecision struct {
	outcome checkoutOutcome
	reason  checkoutReason
}

func checkoutDecisionFor(e checkoutEvaluation, issue error) checkoutDecision {
	switch {
	case issue == nil && e.assessment.outcome != "":
		return e.assessment
	case issue == nil && e.resumed:
		return checkoutDecision{checkoutAllow, reasonExistingIntent}
	case issue == nil:
		return checkoutDecision{checkoutAllow, reasonEligible}
	case errors.Is(issue, ErrCheckoutUnavailable):
		return checkoutDecision{checkoutBlock, reasonNotEligible}
	case errors.Is(issue, ErrCheckoutKeyConflict):
		return checkoutDecision{checkoutBlock, reasonRequestKeyConflict}
	case errors.Is(issue, ErrCheckoutConflict):
		return checkoutDecision{checkoutBlock, reasonWholeBookConflict}
	case errors.Is(issue, ErrSubscriptionConflict):
		return checkoutDecision{checkoutBlock, reasonVIPConflict}
	case errors.Is(issue, ErrCheckoutPaused):
		return checkoutDecision{checkoutBlock, reasonChannelPaused}
	case errors.Is(issue, ErrSubscriptionPending):
		return checkoutDecision{checkoutVerify, reasonVIPPreparing}
	case errors.Is(issue, errPaymentAttemptReview):
		return checkoutDecision{checkoutReview, reasonAttemptReview}
	case errors.Is(issue, ErrCheckoutPending):
		return checkoutDecision{checkoutVerify, reasonAttemptInProgress}
	case errors.Is(issue, ErrSubscriptionReview):
		return checkoutDecision{checkoutReview, reasonSubscriptionReview}
	default:
		// Infrastructure errors never become assertions about the user's eligibility.
		return checkoutDecision{}
	}
}

var errPaymentAttemptReview = fmt.Errorf("reconcile prior payment attempt: %w", ErrCheckoutPending)

// Reservations use one user lock across products and channels. A savepoint lets
// a denied reservation discard its tentative changes while preserving the decision.
func evaluateCheckout[T any](ctx context.Context, e checkoutEvaluation, reserve func(pgx.Tx, *checkoutEvaluation) (*T, error)) (*T, error) {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var userID int64
	err = tx.QueryRow(ctx, "SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE", e.userID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	if err != nil {
		return nil, err
	}

	reservation, err := tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	result, issue := reserve(reservation, &e)
	decision := checkoutDecisionFor(e, issue)
	if decision.outcome == "" {
		return nil, issue
	}
	if issue != nil {
		err = reservation.Rollback(ctx)
	} else {
		err = reservation.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_checkout_decisions
		(user_id,checkout_environment,stage,provider,account_id,order_id,checkout_id,slot_id,outcome,reason,rule_version)
		VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5::bigint,0),NULLIF($6::bigint,0),
			NULLIF($7,'')::uuid,NULLIF(GREATEST($8,0),0),$9,$10,$11)`,
		userID, conversion.Environment(), e.stage, e.provider, e.accountID, e.orderID, e.checkoutID,
		e.slotID, decision.outcome, decision.reason, checkoutRuleVersion)
	if err != nil {
		return nil, fmt.Errorf("record checkout decision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, issue
}

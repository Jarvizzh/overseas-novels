package application

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/ledger"
)

const (
	stripeRefundNonRefundableInquiryNote = "Stripe inquiry dispute marks charge non-refundable; routed to dispute workflow"
	stripeRefundDisputeLostNote          = "Stripe dispute was lost; receipt is not refundable"
	stripeRefundWonNotYetRefundableNote  = "Stripe dispute was won; waiting for remote charge refundability"
	stripeRefundVerificationPendingNote  = "Verifying Stripe charge balance and live dispute eligibility"
	stripeRefundInsufficientBalanceNote  = "Stripe charge remaining refundable balance is insufficient"
	stripeRefundRequiresReviewNote       = "Stripe receipt has a dispute or insufficient refundable balance"
	stripeRefundConfirmingNote           = "Checking the original Stripe refund request"
	stripeRefundRetryDelay               = time.Minute
	stripeRefundRequestTimeout           = 45 * time.Second
)

type stripeRefundAdapter interface {
	ResolveStripeRefund(context.Context, payment.RefundRequest) (*payment.StripeRefundProof, error)
	ReadStripeRefund(context.Context, string) (*payment.StripeRefundProof, error)
	GetRefundLedgerEvidence(context.Context, string) ([]payment.LedgerEvidence, error)
	GetLedgerMovement(context.Context, string) (payment.LedgerEvidence, error)
}

func claimStripeRefunds(ctx context.Context, account payment.WebhookAccount) ([]refundJob, error) {
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT c.id,p.capture_id,(c.first_sent_at IS NULL AND c.provider_refund_id IS NULL) AS send,
			c.stop_loss_full_remaining,
			EXISTS(SELECT 1 FROM payment_case_receipts r JOIN payment_cases pc ON pc.id=r.case_id
				WHERE r.receipt_id=c.receipt_id AND pc.kind='dispute') AS verify_disputes
		FROM payment_refund_commands c JOIN third_party_payment_orders p ON p.id=c.receipt_id
		JOIN payment_channel_accounts a ON a.id=p.account_id JOIN recharge_orders o ON o.id=c.order_id
		WHERE a.provider='stripe' AND a.account_key=$1 AND a.environment=$2 AND c.environment=$2 AND o.checkout_environment=$3
		  AND p.object_type='charge' AND p.status='VERIFIED' AND c.currency='USD' AND c.amount_minor>0
		  AND c.status IN ('pending','confirming') AND c.next_check_at<=now()
		  AND (c.claimed_until IS NULL OR c.claimed_until<=now())
		ORDER BY c.next_check_at,c.id LIMIT 5 FOR UPDATE OF c SKIP LOCKED
	)
	UPDATE payment_refund_commands c SET claimed_until=now()+interval '5 minutes',status='confirming',
		first_sent_at=CASE WHEN due.send THEN now() ELSE c.first_sent_at END,updated_at=now()
	FROM due WHERE c.id=due.id
	RETURNING c.id::text,c.order_id,c.receipt_id,c.claimed_until,due.capture_id,COALESCE(c.provider_refund_id,''),
		c.amount_minor,c.currency,due.send,due.verify_disputes,due.stop_loss_full_remaining`,
		account.AccountKey, account.Environment, conversion.Environment())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (refundJob, error) {
		job := refundJob{request: payment.RefundRequest{Account: account}}
		err := row.Scan(&job.id, &job.orderID, &job.receiptID, &job.lease, &job.request.ReceiptID, &job.request.RefundID,
			&job.request.AmountMinor, &job.request.Currency, &job.request.Send,
			&job.request.VerifyDisputes, &job.request.StopLossFullRemaining)
		job.request.RequestKey = job.id
		return job, err
	})
}

// verifyLocalStripeRefundEligibility checks local receipt gross, completed refunds,
// and concurrent local refund reservations before sending a Stripe refund request.
func verifyLocalStripeRefundEligibility(ctx context.Context, job *refundJob) error {
	if !job.request.Send {
		return nil
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return errors.Join(payment.ErrStripeRefundVerificationPending, err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stripe-capture:'||$1,0))`, job.request.ReceiptID); err != nil {
		return errors.Join(payment.ErrStripeRefundVerificationPending, err)
	}
	var gross, refunded, otherReserved int64
	err = tx.QueryRow(ctx, `SELECT (t.gross_amount*100)::bigint,
		COALESCE((SELECT sum(amount_minor)::bigint FROM payment_stripe_refund_items WHERE receipt_id=t.id),0),
		COALESCE((SELECT sum(c.amount_minor)::bigint FROM payment_refund_commands c
			WHERE c.receipt_id=t.id AND c.id<>$2::uuid AND c.status IN ('pending','confirming','review')
			  AND NOT EXISTS(SELECT 1 FROM payment_stripe_refund_items i
				WHERE i.receipt_id=c.receipt_id AND i.refund_ref=c.provider_refund_id)),0)
		FROM third_party_payment_orders t
		JOIN payment_channel_accounts a ON a.id=t.account_id
		WHERE t.id=$1 AND t.capture_id=$3 AND t.object_type='charge' AND t.status='VERIFIED' AND t.currency='USD'
		  AND a.provider='stripe' AND a.account_key=$4 AND a.environment=$5`,
		job.receiptID, job.id, job.request.ReceiptID, job.request.Account.AccountKey, job.request.Account.Environment).
		Scan(&gross, &refunded, &otherReserved)
	if err != nil {
		return errors.Join(payment.ErrStripeRefundVerificationPending, err)
	}
	availableAfterCompleted := gross - refunded
	availableAfterReserved := availableAfterCompleted - otherReserved
	if availableAfterCompleted <= 0 {
		return payment.ErrStripeRefundInsufficientBalance
	}
	if otherReserved > 0 && job.request.AmountMinor > availableAfterReserved {
		// Another concurrent refund command holds a reservation on this charge;
		// keep our reservation pending until concurrency resolves.
		return payment.ErrStripeRefundVerificationPending
	}
	if job.request.StopLossFullRemaining && availableAfterReserved < job.request.AmountMinor {
		if availableAfterReserved <= 0 {
			return payment.ErrStripeRefundInsufficientBalance
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_refund_commands SET amount_minor=$2,updated_at=now()
			WHERE id=$1::uuid AND claimed_until=$3 AND status='confirming' AND provider_refund_id IS NULL`,
			job.id, availableAfterReserved, job.lease); err != nil {
			return errors.Join(payment.ErrStripeRefundVerificationPending, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return errors.Join(payment.ErrStripeRefundVerificationPending, err)
		}
		job.request.AmountMinor = availableAfterReserved
		return nil
	}
	if job.request.AmountMinor > availableAfterReserved {
		return payment.ErrStripeRefundInsufficientBalance
	}
	return nil
}

func applyStripeRefundTx(ctx context.Context, tx pgx.Tx, account payment.WebhookAccount, proof *payment.StripeRefundProof) error {
	if proof.Currency != "USD" || proof.Status != "succeeded" || proof.AmountMinor <= 0 {
		return ErrCheckoutUnavailable
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stripe-capture:'||$1,0))`, proof.ChargeID); err != nil {
		return err
	}

	var accountID, receiptID, orderID, gross int64
	var wasFulfilled bool
	var orderType string
	err := tx.QueryRow(ctx, `SELECT a.id,t.id,t.order_id,(gross_amount*100)::bigint,o.fulfilled_at IS NOT NULL,o.order_type
		FROM third_party_payment_orders t JOIN payment_channel_accounts a ON a.id=t.account_id
		JOIN recharge_orders o ON o.id=t.order_id
		WHERE a.provider='stripe' AND a.account_key=$1 AND a.environment=$2 AND t.capture_id=$3
		 AND t.object_type='charge' AND t.currency='USD' AND t.status='VERIFIED' AND o.checkout_environment=$4
		FOR UPDATE OF o`, account.AccountKey, account.Environment, proof.ChargeID, conversion.Environment()).Scan(&accountID, &receiptID, &orderID, &gross, &wasFulfilled, &orderType)
	if errors.Is(err, pgx.ErrNoRows) {
		return errRefundUnmatched
	}
	if err != nil {
		return err
	}
	if proof.AmountMinor > gross {
		return ErrCheckoutUnavailable
	}
	var saved string
	err = tx.QueryRow(ctx, `INSERT INTO payment_stripe_refund_items(account_id,refund_ref,receipt_id,amount_minor)
		VALUES($1,$2,$3,$4) ON CONFLICT(account_id,refund_ref) DO UPDATE SET refund_ref=EXCLUDED.refund_ref
		WHERE payment_stripe_refund_items.receipt_id=EXCLUDED.receipt_id AND payment_stripe_refund_items.amount_minor=EXCLUDED.amount_minor
		RETURNING refund_ref`, accountID, proof.ID, receiptID, proof.AmountMinor).Scan(&saved)
	if err != nil {
		return err
	}
	var covered bool
	err = tx.QueryRow(ctx, `WITH amounts AS (
		SELECT t.gross_amount*100 AS gross,
		  COALESCE((SELECT sum(amount_minor) FROM payment_stripe_refund_items r WHERE r.receipt_id=t.id),0) AS refunded
		FROM payment_fulfillment_funding f JOIN third_party_payment_orders t ON t.id=f.receipt_id WHERE f.order_id=$1
	), deferred AS (
		SELECT EXISTS(SELECT 1 FROM recharge_orders o JOIN payment_checkout_attempts p ON p.order_id=o.id
		 JOIN third_party_payment_orders t ON t.order_id=o.id AND t.external_order_id=p.provider_order_id AND t.account_id=p.account_id
		 WHERE o.id=$1 AND t.id=$2 AND o.fulfilled_at IS NULL AND o.payment_method='stripe' AND (t.reconciliation_note IS DISTINCT FROM
		   'Stripe received a payment outside the active fulfillment attempt' OR o.reconciliation_note=$4)) AS original
	)
	SELECT CASE WHEN EXISTS(SELECT 1 FROM amounts) THEN
	  COALESCE((SELECT bool_and(refunded>=gross) FROM amounts),false)
	  ELSE original AND COALESCE((SELECT sum(amount_minor)>=$3 FROM payment_stripe_refund_items WHERE receipt_id=$2),false)
	END FROM deferred`, orderID, receiptID, gross, additionalWholeBookPaymentNote).Scan(&covered)
	if err != nil {
		return err
	}
	if covered {
		if err := aftercare.FinalizeStripeRefund(ctx, tx, orderID, proof.ID); err != nil {
			return err
		}
		if !wasFulfilled {
			if orderType == "subscription" {
				return conversion.RecordSubscriptionPaymentTx(ctx, tx, orderID, time.Now().UTC(), "stripe_pre_fulfillment_refund", conversion.Environment(), nil, false, false)
			}
			return conversion.RecordPaymentTx(ctx, tx, orderID, time.Now().UTC(), "stripe_pre_fulfillment_refund", false, false)
		}
		return nil
	}
	return finalizeUnfulfilledStripeVIPRefund(ctx, tx, orderID)
}

// A fully refunded collection can be closed before a period has any funding
// rows. Check the invoice allocations and each original gross charge instead.
func finalizeUnfulfilledStripeVIPRefund(ctx context.Context, tx pgx.Tx, orderID int64) error {
	var ref string
	err := tx.QueryRow(ctx, `WITH collections AS (
		SELECT a.amount_minor,t.gross_amount*100 AS gross,r.amount_minor AS refunded,r.ref
		FROM payment_stripe_invoice_periods p JOIN recharge_orders o ON o.id=p.order_id
		JOIN payment_stripe_invoice_allocations a ON a.account_id=p.account_id AND a.invoice_ref=p.invoice_ref
		JOIN third_party_payment_orders t ON t.id=a.receipt_id AND t.order_id=o.id AND t.account_id=p.account_id
		LEFT JOIN LATERAL (SELECT sum(r.amount_minor) AS amount_minor,max(r.refund_ref) AS ref
			FROM payment_stripe_refund_items r WHERE r.receipt_id=t.id AND r.account_id=p.account_id) r ON true
		WHERE p.order_id=$1 AND NOT p.admitted AND o.status='Paid' AND o.fulfilled_at IS NULL
			AND p.provider_status IN ('paid','void','uncollectible') AND t.status='VERIFIED' AND t.currency='USD'
	)
	SELECT max(ref) FROM collections HAVING count(*)>0 AND bool_and(COALESCE(refunded>=gross,false))
		AND EXISTS (SELECT 1 FROM payment_stripe_invoice_periods p WHERE p.order_id=$1
			AND (p.provider_status<>'paid' OR (SELECT sum(amount_minor) FROM collections)=p.billed_minor))`, orderID).Scan(&ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := aftercare.FinalizeStripeRefund(ctx, tx, orderID, ref); err != nil {
		return err
	}
	return conversion.RecordSubscriptionPaymentTx(ctx, tx, orderID, time.Now().UTC(), "stripe_pre_fulfillment_refund", conversion.Environment(), nil, false, false)
}

func stripeRefundStatus(proof *payment.StripeRefundProof, lookup error) (status, note string, next *time.Time, clearFirstSent, routeDispute bool) {
	var retry time.Duration
	status, note = "review", "Refund result needs provider evidence"
	switch {
	case errors.Is(lookup, payment.ErrStripeRefundOpenDispute):
		note, clearFirstSent, routeDispute = aftercare.RefundDisputeReviewNote, true, true
	case errors.Is(lookup, payment.ErrStripeRefundNonRefundableInquiry):
		note, clearFirstSent, routeDispute = stripeRefundNonRefundableInquiryNote, true, true
	case errors.Is(lookup, payment.ErrStripeRefundDisputeLost):
		note, clearFirstSent, routeDispute = stripeRefundDisputeLostNote, true, true
	case errors.Is(lookup, payment.ErrStripeRefundInsufficientBalance):
		note, clearFirstSent = stripeRefundInsufficientBalanceNote, true
	case errors.Is(lookup, payment.ErrStripeRefundRequiresReview):
		note, clearFirstSent = stripeRefundRequiresReviewNote, true
	case errors.Is(lookup, payment.ErrStripeRefundWonNotYetRefundable):
		status, note, retry, clearFirstSent = "confirming", stripeRefundWonNotYetRefundableNote, stripeRefundRetryDelay, true
	case errors.Is(lookup, payment.ErrStripeRefundVerificationPending):
		status, note, retry, clearFirstSent = "confirming", stripeRefundVerificationPendingNote, stripeRefundRetryDelay, true
	case lookup != nil:
		status, note, retry = "confirming", stripeRefundConfirmingNote, stripeRefundRetryDelay
	case proof != nil:
		switch proof.Status {
		case "succeeded":
			return "succeeded", "", nil, false, false
		case "failed", "canceled":
			return "failed", "Stripe confirmed refund failure", nil, false, false
		case "pending", "requires_action":
			status, note, retry = "confirming", "Stripe refund is pending", stripeRefundRetryDelay
		}
	}
	if retry == 0 {
		return status, note, nil, clearFirstSent, routeDispute
	}
	t := time.Now().Add(retry)
	return status, note, &t, clearFirstSent, routeDispute
}

func finishStripeRefund(ctx context.Context, job refundJob, proof *payment.StripeRefundProof, lookup error) error {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var id string
	err = tx.QueryRow(ctx, `SELECT id::text FROM payment_refund_commands WHERE id=$1 AND claimed_until=$2 AND status='confirming' FOR UPDATE`,
		job.id, job.lease).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var ref *string
	if proof != nil {
		if proof.ChargeID != job.request.ReceiptID || proof.AmountMinor != job.request.AmountMinor || proof.Currency != job.request.Currency ||
			(proof.CommandKey != job.id && proof.ID != job.request.RefundID) {
			proof = nil
			lookup = nil
		} else {
			ref = &proof.ID
			if proof.Status == "succeeded" {
				if err := applyStripeRefundTx(ctx, tx, job.request.Account, proof); err != nil {
					return err
				}
			}
		}
	}
	status, note, next, clearFirstSent, routeDispute := stripeRefundStatus(proof, lookup)
	resetSend := clearFirstSent && job.request.Send && job.request.RefundID == ""
	_, err = tx.Exec(ctx, `UPDATE payment_refund_commands SET status=$2,result_note=NULLIF($3,''),next_check_at=$4,
		claimed_until=NULL,first_sent_at=CASE WHEN $6 THEN NULL ELSE first_sent_at END,
		provider_refund_id=COALESCE($5,provider_refund_id),updated_at=now() WHERE id=$1`,
		job.id, status, note, next, ref, resetSend)
	if err != nil {
		return err
	}
	if routeDispute {
		auditReqID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("stripe-refund-dispute-route:"+job.id+":"+note))
		if _, err := tx.Exec(ctx, `INSERT INTO payment_case_audit(case_id,request_id,actor_id,action,reason,parameters)
			SELECT r.case_id,$1::uuid,NULL,'refund_routed_to_dispute',$2,
				jsonb_build_object('refund_command_id',$3::text,'charge_id',$4::text,'amount_minor',$5::bigint)
			FROM payment_case_receipts r JOIN payment_cases c ON c.id=r.case_id
			WHERE r.receipt_id=$6 AND c.kind='dispute'
			ORDER BY c.is_open DESC,c.id DESC LIMIT 1
			ON CONFLICT DO NOTHING`,
			auditReqID, note, job.id, job.request.ReceiptID, job.request.AmountMinor, job.receiptID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func syncStripeRefundEvidence(ctx context.Context, adapter stripeRefundAdapter, proof *payment.StripeRefundProof) error {
	if proof == nil || proof.Status != "succeeded" || proof.Currency != "USD" {
		return nil
	}
	entries, err := adapter.GetRefundLedgerEvidence(ctx, proof.ID)
	if err != nil {
		return err
	}
	repo := ledger.NewRepository(db.Pool(ctx))
	for _, entry := range entries {
		if _, err := repo.Record(ctx, entry); err != nil {
			return err
		}
		authoritative, err := adapter.GetLedgerMovement(ctx, entry.MovementKey)
		if err != nil {
			return err
		}
		if _, err := repo.Record(ctx, authoritative); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) reconcileStripeRefunds(ctx context.Context) {
	provider, available := s.stripeCheckoutAdapter()
	if !available {
		return
	}
	adapter, err := payment.Capability[stripeRefundAdapter](s.adapters, payment.ProviderStripe)
	if err != nil {
		return
	}
	jobs, err := claimStripeRefunds(ctx, provider.AccountScope())
	if err != nil {
		log.Printf("task=stripe_refund action=claim error=%v", err)
		return
	}
	for _, job := range jobs {
		request, cancel := context.WithTimeout(ctx, stripeRefundRequestTimeout)
		var proof *payment.StripeRefundProof
		issue := verifyLocalStripeRefundEligibility(request, &job)
		if issue == nil {
			proof, issue = adapter.ResolveStripeRefund(request, job.request)
		}
		if err := finishStripeRefund(request, job, proof, issue); err != nil {
			log.Printf("task=stripe_refund action=save id=%s error=%v", job.id, err)
		} else if err := syncStripeRefundEvidence(request, adapter, proof); err != nil {
			log.Printf("task=stripe_refund action=ledger id=%s error=%v", job.id, err)
		}
		cancel()
	}
}

func (s *service) syncStripeRefundNotice(ctx context.Context, account payment.WebhookAccount, id string) error {
	adapter, err := payment.Capability[stripeRefundAdapter](s.adapters, payment.ProviderStripe)
	if err != nil {
		return err
	}
	proof, err := adapter.ReadStripeRefund(ctx, id)
	if err != nil || proof.Status != "succeeded" {
		return err
	}
	if proof.Currency != "USD" {
		return nil
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := applyStripeRefundTx(ctx, tx, account, proof); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if proof.CommandKey != "" {
		_, err = db.Pool(ctx).Exec(ctx, `UPDATE payment_refund_commands c SET status='succeeded',provider_refund_id=$2,
			claimed_until=NULL,result_note=NULL,next_check_at=NULL,updated_at=now()
			FROM third_party_payment_orders t JOIN payment_channel_accounts a ON a.id=t.account_id
			WHERE c.id::text=$1 AND c.receipt_id=t.id AND c.amount_minor=$3 AND c.currency='USD'
			  AND a.provider='stripe' AND a.account_key=$4 AND a.environment=$5 AND t.capture_id=$6
			  AND c.status IN ('pending','confirming','review')`, proof.CommandKey, proof.ID, proof.AmountMinor, account.AccountKey, account.Environment, proof.ChargeID)
		if err != nil {
			return err
		}
	}

	return syncStripeRefundEvidence(ctx, adapter, proof)
}

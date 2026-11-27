package aftercare

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type FulfillmentState struct {
	Deferred bool
	CaseID   *int64
}

// EvaluateFulfillment runs after the receipt is verified while the order and
// original capture are locked. A notification alone never supplies a debit.
func EvaluateFulfillment(ctx context.Context, tx pgx.Tx, orderID, receiptID int64) (FulfillmentState, error) {
	var state FulfillmentState
	err := tx.QueryRow(ctx, `WITH risk AS (
		SELECT c.id FROM payment_cases c JOIN payment_case_receipts r ON r.case_id=c.id
		WHERE r.receipt_id=$1 AND c.kind='risk_review' AND c.work_state IN ('pending','refund_due','dispute_due')
		ORDER BY c.id LIMIT 1 FOR UPDATE OF c
	)
	SELECT EXISTS(SELECT 1 FROM risk) OR COALESCE(loss.confirmed_loss_minor,0)>=o.amount_cents OR
	    EXISTS(SELECT 1 FROM payment_stripe_review_approvals ra JOIN payment_case_receipts r ON r.case_id=ra.case_id
	        WHERE r.receipt_id=$1 AND ra.status<>'confirmed') OR
	    EXISTS(SELECT 1 FROM payment_refund_commands c WHERE c.receipt_id=$1 AND c.status IN ('pending','confirming','review')) OR
	    EXISTS(SELECT 1 FROM payment_deferred_fulfillment h JOIN payment_case_receipts r ON r.receipt_id=h.receipt_id
	        JOIN payment_cases c ON c.id=r.case_id AND c.kind='dispute' AND c.is_open
	        WHERE h.order_id=$2 AND h.receipt_id=$1 AND h.resolved_at IS NULL),(SELECT id FROM risk)
	FROM recharge_orders o LEFT JOIN payment_principal_loss_by_receipt loss ON loss.receipt_id=$1 WHERE o.id=$2`, receiptID, orderID).Scan(&state.Deferred, &state.CaseID)
	if err != nil || !state.Deferred {
		return state, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_deferred_fulfillment(order_id,receipt_id,case_id) VALUES($1,$2,$3)
		ON CONFLICT(order_id,receipt_id) DO UPDATE SET case_id=COALESCE(EXCLUDED.case_id,payment_deferred_fulfillment.case_id)`, orderID, receiptID, state.CaseID)
	return state, err
}

func FinishFulfillment(ctx context.Context, tx pgx.Tx, orderID int64) error {
	_, err := tx.Exec(ctx, `UPDATE payment_deferred_fulfillment SET resolved_at=now()
		WHERE order_id=$1 AND resolved_at IS NULL`, orderID)
	return err
}

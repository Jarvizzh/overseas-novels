package application

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

func (r *dbRepository) CompletePayPalRefund(ctx context.Context, refundID, captureID string, amount int64, currency string, accounts ...payment.WebhookAccount) error {
	var scope payment.WebhookAccount
	if len(accounts) != 0 {
		scope = accounts[0]
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if captureID != "" {
		if err := lockPayPalCapture(ctx, tx, captureID); err != nil {
			return err
		}
	}

	var orderID, receiptID int64
	var observedCapture string
	if amount <= 0 {
		return errRefundUnmatched
	}
	err = tx.QueryRow(ctx, `SELECT o.id,p.id,p.capture_id FROM recharge_orders o
		JOIN third_party_payment_orders p ON p.order_id=o.id AND p.payment_provider='paypal'
		LEFT JOIN payment_channel_accounts a ON a.id=p.account_id AND a.provider='paypal'
		LEFT JOIN payment_refunds r ON r.order_id=o.id
		WHERE p.gross_amount*100 >= $3 AND p.currency=$4
		AND ($5='' OR (a.account_key=$5 AND a.environment=$6) OR (p.account_id IS NULL AND EXISTS (
			SELECT 1 FROM paypal_identity_lock l WHERE l.client_id=$5 AND l.environment=$6)))
		AND (($2<>'' AND p.capture_id=$2 AND p.status='VERIFIED') OR
			(r.provider_refund_id=$1 AND ($2='' OR p.capture_id=$2) AND p.status IN ('VERIFIED','COMPLETED')) OR
			($2<>'' AND p.capture_id=$2 AND o.status='RefundPending' AND p.status='COMPLETED'))
		ORDER BY p.id DESC LIMIT 1`, refundID, captureID, amount, currency, scope.AccountKey, scope.Environment).Scan(&orderID, &receiptID, &observedCapture)
	if errors.Is(err, pgx.ErrNoRows) {
		return errRefundUnmatched
	}
	if err != nil {
		return err
	}
	if err := settlePayPalReceiptRefundTx(ctx, tx, orderID, refundID, observedCapture, amount, currency); err != nil {
		return err
	}
	// A signed completion can close a command already linked to this refund.
	// An unknown refund reference remains reserved for financial review.
	_, err = tx.Exec(ctx, `UPDATE payment_refund_commands SET status='succeeded',next_check_at=NULL,
		claimed_until=NULL,result_note=NULL,updated_at=now()
		WHERE receipt_id=$1 AND provider_refund_id=$2 AND amount_minor=$3 AND currency=$4
		AND status IN ('pending','confirming','review')`, receiptID, refundID, amount, currency)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// The receipt owns the refund. Only the receipt funding this order can change its entitlement.
func settlePayPalReceiptRefundTx(ctx context.Context, tx pgx.Tx, orderID int64, refundID, captureID string, amount int64, currency string) error {
	if err := lockPayPalCapture(ctx, tx, captureID); err != nil {
		return err
	}
	var provider, state string
	var extra bool
	err := tx.QueryRow(ctx, `SELECT o.payment_method,o.status,
		EXISTS(SELECT 1 FROM third_party_payment_orders p JOIN payment_checkout_attempts a ON a.order_id=o.id
		 AND a.provider='paypal' AND a.provider_order_id=p.external_order_id AND a.account_id=p.account_id
		 WHERE p.order_id=o.id AND p.payment_provider='paypal' AND p.capture_id=$2 AND a.status='closed')
		FROM recharge_orders o WHERE o.id=$1 FOR UPDATE`, orderID, captureID).Scan(&provider, &state, &extra)
	if err != nil {
		return err
	}
	if provider != "paypal" || extra {
		_, err = tx.Exec(ctx, `SELECT save_paypal_refund_item($1,$2,$3,$4)`, refundID, captureID, amount, currency)
		return err
	}
	if state == "Pending" {
		return errRefundUnmatched
	}
	_, err = tx.Exec(ctx, `SELECT settle_paypal_refund_item($1,$2,$3,$4,$5)`, orderID, refundID, captureID, amount, currency)
	return err
}

package application

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
)

// Unscoped pending PayPal orders keep their reservation until their channel
// outcome is known. This is independent of the stricter entitlement rule.
func legacyWholeBookReservation(environment string) string {
	return `payment_method='paypal' AND status='Pending' AND external_ref_id IS NOT NULL
		AND external_ref_id NOT LIKE 'mock%' AND NOT EXISTS(SELECT 1 FROM payment_checkout_attempts p
			WHERE p.order_id=recharge_orders.id AND p.provider='paypal' AND p.provider_order_id=recharge_orders.external_ref_id)
		AND (payment_order_environment(recharge_orders) IS NULL OR
			payment_order_environment(recharge_orders)=CASE ` + environment + ` WHEN 'production' THEN 'live' WHEN 'sandbox' THEN 'sandbox' END)`
}

// The target is a trusted SQL alias and environment is a bind parameter.
func wholeBookWriteBlocked(target, environment string) string {
	return `EXISTS(SELECT 1 FROM recharge_orders
		WHERE user_id=` + target + `.user_id AND novel_id=` + target + `.novel_id AND order_type='whole_book'
		AND id<>` + target + `.id AND ((` + legacyWholeBookReservation(environment) + `) OR
			(` + novel.WholeBookEnvironmentPredicate(environment) + ` AND (
				(` + novel.WholeBookEntitlementPredicate + `) OR
				(status IN ('Paid','Success','RefundPending') AND fulfilled_at IS NULL)
			))))`
}

func (r *dbRepository) wholeBookWriteAllowed(ctx context.Context, orderID int64) (bool, error) {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := lockWholeBookBuyer(ctx, tx, orderID); err != nil {
		return false, err
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT o.status='Pending' AND (o.order_type<>'whole_book' OR
		NOT `+wholeBookWriteBlocked("o", "$2")+`) FROM recharge_orders o WHERE o.id=$1`,
		orderID, conversion.Environment()).Scan(&allowed)
	if err != nil {
		return false, err
	}
	return allowed, tx.Commit(ctx)
}

func checkOtherWholeBookReservation(ctx context.Context, tx pgx.Tx, orderID int64, environment string) error {
	var blocked bool
	err := tx.QueryRow(ctx, `SELECT `+wholeBookWriteBlocked("o", "$2")+` OR EXISTS(
		SELECT 1 FROM recharge_orders WHERE id=o.id AND (`+legacyWholeBookReservation("$2")+`))
		FROM recharge_orders o WHERE id=$1`, orderID, environment).Scan(&blocked)
	if err == nil && blocked {
		return ErrCheckoutConflict
	}
	return err
}

func anotherWholeBookPaid(ctx context.Context, q checkoutQueryer, orderID int64) (bool, error) {
	var paid bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM recharge_orders
		WHERE (user_id,novel_id)=(SELECT user_id,novel_id FROM recharge_orders WHERE id=$1)
		AND id<>$1 AND order_type='whole_book' AND `+novel.WholeBookEnvironmentPredicate("$2")+`
		AND ((`+novel.WholeBookEntitlementPredicate+`) OR
			(status IN ('Paid','Success','RefundPending') AND fulfilled_at IS NULL)))`,
		orderID, conversion.Environment()).Scan(&paid)
	return paid, err
}

func anotherWholeBookFulfilled(ctx context.Context, tx pgx.Tx, orderID int64, environment string) (bool, error) {
	var fulfilled bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM recharge_orders
		WHERE (user_id,novel_id)=(SELECT user_id,novel_id FROM recharge_orders WHERE id=$1)
		AND id<>$1 AND `+novel.WholeBookEnvironmentPredicate("$2")+` AND `+novel.WholeBookEntitlementPredicate+`)`,
		orderID, environment).Scan(&fulfilled)
	return fulfilled, err
}

// Acquisition precedes order row locks, matching evaluateCheckout's lock order.
func lockWholeBookBuyer(ctx context.Context, tx pgx.Tx, orderID int64) error {
	_, err := tx.Exec(ctx, `SELECT u.id FROM users u JOIN recharge_orders o ON o.user_id=u.id
		WHERE o.id=$1 AND o.order_type='whole_book' FOR NO KEY UPDATE OF u`, orderID)
	return err
}

func (r *dbRepository) closeLegacyWholeBookOrder(ctx context.Context, orderID int64, account payment.WebhookAccount) error {
	if !paymentEnvironmentMatches(account) {
		return ErrCheckoutUnavailable
	}
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE recharge_orders SET status='Failed',updated_at=now()
		WHERE id=$1 AND order_type='whole_book' AND payment_method='paypal' AND status='Pending'
		AND COALESCE(payment_order_environment(recharge_orders),$2)=$2
		AND NOT EXISTS(SELECT 1 FROM payment_checkout_attempts p WHERE p.order_id=$1)
		AND NOT EXISTS(SELECT 1 FROM third_party_payment_orders r WHERE r.order_id=$1 AND r.status IN ('VERIFIED','COMPLETED'))`,
		orderID, account.Environment)
	return err
}

const additionalWholeBookPaymentNote = "Additional payment for an already reserved whole-book purchase"

func parkAdditionalWholeBookPayment(ctx context.Context, tx pgx.Tx, orderID int64, provider string, paidAt *time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE recharge_orders SET status='Paid',paid_at=COALESCE(paid_at,$2,now()),payment_method=$4,
		reconciliation_note=$3,updated_at=now()
		WHERE id=$1 AND status<>'Refunded' AND fulfilled_at IS NULL`, orderID, paidAt, additionalWholeBookPaymentNote, provider)
	return err
}

package application

import (
	"context"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
)

// A stopped agreement can retain paid access, but no longer reserves a new VIP
// checkout. Keep unknown and in-flight agreement states in the conflict set.
func checkVIPCheckout(ctx context.Context, tx pgx.Tx, e *checkoutEvaluation, current subscriptionIntent, slotID int) error {
	var conflicting bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_subscriptions
		WHERE user_id=$1 AND payment_subscription_environment(user_subscriptions)=$4 AND payment_method IN ('paypal','stripe') AND status NOT IN ('CANCELLED','EXPIRED')
		  AND subscription_id NOT LIKE 'I-MOCK%'
		  AND NOT (payment_method=$2 AND subscription_id=$3 AND status='PENDING'))
		OR ($2='paypal' AND EXISTS(SELECT 1 FROM payment_stripe_subscription_intents
			WHERE user_id=$1 AND checkout_environment=$4 AND status IN ('creating','buyer_action')))`,
		e.userID, e.provider, current.subscriptionID, conversion.Environment()).Scan(&conflicting)
	if err != nil {
		return err
	}
	if conflicting || (current.id != "" && current.slotID != slotID) {
		return ErrSubscriptionConflict
	}
	return nil
}

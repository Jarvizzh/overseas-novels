package aftercare

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (r *Repository) ApplyNextLoss(ctx context.Context) (bool, error) {
	tx, err := r.store.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var id, key string
	var orderID int64
	var blocked bool
	err = tx.QueryRow(ctx, `SELECT id::text,order_id,blocked,evidence_key FROM payment_loss_commands
		WHERE status='pending' ORDER BY requested_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &orderID, &blocked, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	status, err := applyLoss(ctx, tx, orderID, blocked, key)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE payment_loss_commands SET status=$2,finished_at=now() WHERE id=$1`, id, status)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func applyLoss(ctx context.Context, tx pgx.Tx, orderID int64, blocked bool, key string) (string, error) {
	var userID int64
	var charged, bonus int
	var status string
	var fulfilled bool
	err := tx.QueryRow(ctx, `SELECT user_id,coins,bonus_coins_credited,status,fulfilled_at IS NOT NULL
		FROM recharge_orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&userID, &charged, &bonus, &status, &fulfilled)
	if err != nil {
		return "", err
	}
	if status == "Refunded" || !fulfilled {
		return "stale", nil
	}
	var current bool
	err = tx.QueryRow(ctx, `SELECT count(*)=1 AND COALESCE(bool_and(target_blocked=$2 AND evidence_key=$3),false)
		FROM payment_principal_loss WHERE order_id=$1`, orderID, blocked, key).Scan(&current)
	if err != nil {
		return "", err
	}
	if !current {
		return "stale", nil
	}

	return "applied", setEntitlementAdjustmentTx(ctx, tx, orderID, userID, charged, bonus, blocked, key, "payment_loss")
}

func setEntitlementAdjustmentTx(ctx context.Context, tx pgx.Tx, orderID, userID int64, charged, bonus int, blocked bool, key, businessType string) error {

	var priorCharged, priorBonus int
	var wasBlocked bool
	err := tx.QueryRow(ctx, `SELECT blocked,charged_coins,bonus_coins FROM payment_entitlement_adjustments WHERE order_id=$1`,
		orderID).Scan(&wasBlocked, &priorCharged, &priorBonus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if blocked == wasBlocked {
		return nil
	}
	kind := "debit"
	if blocked && charged+bonus > 0 {
		err = tx.QueryRow(ctx, `SELECT least(charged_coins,$2),least(bonus_coins,$3)
			FROM wallets WHERE user_id=$1 FOR UPDATE`, userID, charged, bonus).Scan(&charged, &bonus)
		if err != nil {
			return err
		}
	} else if !blocked {
		kind = "credit"
		charged, bonus = priorCharged, priorBonus
	}
	if charged+bonus > 0 {
		sign := -1
		if !blocked {
			sign = 1
		}
		if _, err = tx.Exec(ctx, `UPDATE wallets SET charged_coins=charged_coins+$2,bonus_coins=bonus_coins+$3,updated_at=now()
			WHERE user_id=$1`, userID, sign*charged, sign*bonus); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO transactions(id,user_id,type,biz_type,amount,charged_amount,bonus_amount,description)
			VALUES(gen_random_uuid()::text,$1,$2,$7,$3,$4,$5,'Payment principal adjustment for order #'||$6::bigint::text)`,
			userID, kind, charged+bonus, charged, bonus, orderID, businessType); err != nil {
			return err
		}
	}
	if !blocked {
		charged, bonus = 0, 0
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_entitlement_adjustments(order_id,blocked,charged_coins,bonus_coins,policy_evidence)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(order_id) DO UPDATE SET blocked=EXCLUDED.blocked,
		charged_coins=EXCLUDED.charged_coins,bonus_coins=EXCLUDED.bonus_coins,policy_evidence=EXCLUDED.policy_evidence,updated_at=now()`,
		orderID, blocked, charged, bonus, key)
	return err
}

// FinalizeStripeRefund closes a fully refunded order and any grant it funded.
// Receipt and cumulative amount checks belong to the caller.
func FinalizeStripeRefund(ctx context.Context, tx pgx.Tx, orderID int64, refundRef string) error {
	var userID int64
	var charged, bonus int
	var status string
	var fulfilled bool
	err := tx.QueryRow(ctx, `SELECT user_id,coins,bonus_coins_credited,status,fulfilled_at IS NOT NULL
		FROM recharge_orders WHERE id=$1 AND payment_method='stripe' FOR UPDATE`, orderID).Scan(&userID, &charged, &bonus, &status, &fulfilled)
	if err != nil {
		return err
	}
	if status == "Refunded" {
		return nil
	}
	if fulfilled {
		if err := setEntitlementAdjustmentTx(ctx, tx, orderID, userID, charged, bonus, true, refundRef, "refund"); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_orders SET status='Refunded',updated_at=now() WHERE id=$1`, orderID)
	if err != nil {
		return err
	}
	if err := FinishFulfillment(ctx, tx, orderID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE user_subscriptions s SET current_period_end=(
		SELECT max(o.paid_period_end) FROM recharge_orders o WHERE o.subscription_id=s.subscription_id
		    AND o.fulfilled_at IS NOT NULL AND o.status IN ('Success','Paid','RefundPending')
	),updated_at=now() FROM recharge_orders refunded WHERE refunded.id=$1
	  AND s.subscription_id=refunded.subscription_id AND s.payment_method='stripe'`, orderID)
	if err != nil {
		return err
	}
	return FinishFulfillment(ctx, tx, orderID)
}

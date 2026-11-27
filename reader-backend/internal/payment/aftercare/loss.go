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
	orderCharged, orderBonus := charged, bonus
	var shortfallCharged, shortfallBonus int
	kind := "debit"
	if blocked && charged+bonus > 0 {
		err = tx.QueryRow(ctx, `SELECT least(charged_coins,$2),least(bonus_coins,$3)
			FROM wallets WHERE user_id=$1 FOR UPDATE`, userID, charged, bonus).Scan(&charged, &bonus)
		if errors.Is(err, pgx.ErrNoRows) {
			charged, bonus = 0, 0
		} else if err != nil {
			return err
		}
		shortfallCharged = max(0, orderCharged-charged)
		shortfallBonus = max(0, orderBonus-bonus)
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
		shortfallCharged, shortfallBonus = 0, 0
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_entitlement_adjustments(order_id,blocked,charged_coins,bonus_coins,charged_coin_shortfall,bonus_coin_shortfall,policy_evidence)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(order_id) DO UPDATE SET blocked=EXCLUDED.blocked,
		charged_coins=EXCLUDED.charged_coins,bonus_coins=EXCLUDED.bonus_coins,
		charged_coin_shortfall=EXCLUDED.charged_coin_shortfall,bonus_coin_shortfall=EXCLUDED.bonus_coin_shortfall,
		policy_evidence=EXCLUDED.policy_evidence,updated_at=now()`,
		orderID, blocked, charged, bonus, shortfallCharged, shortfallBonus, key)
	return err
}

const (
	FreezeStatusNone       = "none"
	FreezeStatusFrozen     = "frozen"
	FreezeStatusWrittenOff = "written_off"
)

// ApplyDisputeFreezeTx freezes confirmed affected entitlement for an open formal
// dispute without double-recovering if already refunded or blocked.
func ApplyDisputeFreezeTx(ctx context.Context, tx pgx.Tx, caseID, orderID int64, disputeExternalID string) (bool, error) {
	var userID int64
	var charged, bonus int
	var status string
	var fulfilled bool
	err := tx.QueryRow(ctx, `SELECT user_id,coins,bonus_coins_credited,status,fulfilled_at IS NOT NULL
		FROM recharge_orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&userID, &charged, &bonus, &status, &fulfilled)
	if err != nil {
		return false, err
	}
	if status == "Refunded" || !fulfilled {
		return false, nil
	}
	var wasBlocked bool
	var freezeStatus string
	err = tx.QueryRow(ctx, `SELECT blocked,freeze_status FROM payment_entitlement_adjustments WHERE order_id=$1`, orderID).
		Scan(&wasBlocked, &freezeStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if wasBlocked {
		// Already blocked (either by a prior freeze, refund, or confirmed loss); do not double-debit.
		return false, nil
	}
	evidenceKey := "dispute_freeze:" + disputeExternalID
	if err := setEntitlementAdjustmentTx(ctx, tx, orderID, userID, charged, bonus, true, evidenceKey, "dispute_freeze"); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE payment_entitlement_adjustments
		SET freeze_status=$2,freeze_case_id=$3,updated_at=now() WHERE order_id=$1`,
		orderID, FreezeStatusFrozen, caseID)
	if err != nil {
		return false, err
	}
	return true, nil
}

// ReleaseDisputeFreezeTx unfreezes entitlement when a dispute is won or closed
// without loss, provided the order has not been refunded and no other open formal
// dispute exists on the order.
func ReleaseDisputeFreezeTx(ctx context.Context, tx pgx.Tx, caseID, orderID int64, disputeExternalID string) (bool, error) {
	var userID int64
	var charged, bonus int
	var status string
	var fulfilled bool
	err := tx.QueryRow(ctx, `SELECT user_id,coins,bonus_coins_credited,status,fulfilled_at IS NOT NULL
		FROM recharge_orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&userID, &charged, &bonus, &status, &fulfilled)
	if err != nil {
		return false, err
	}
	if status == "Refunded" || !fulfilled {
		return false, nil
	}
	var wasBlocked bool
	var freezeStatus string
	err = tx.QueryRow(ctx, `SELECT blocked,freeze_status FROM payment_entitlement_adjustments WHERE order_id=$1`, orderID).
		Scan(&wasBlocked, &freezeStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !wasBlocked || freezeStatus != FreezeStatusFrozen {
		return false, nil
	}
	var otherOpenFormal bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM payment_case_receipts r JOIN payment_cases c ON c.id=r.case_id
		WHERE r.order_id=$1 AND c.id<>$2 AND c.kind='dispute' AND c.is_open
		  AND c.provider_status IN ('needs_response','under_review')
	)`, orderID, caseID).Scan(&otherOpenFormal)
	if err != nil {
		return false, err
	}
	if otherOpenFormal {
		return false, nil
	}
	evidenceKey := "dispute_unfreeze:" + disputeExternalID
	if err := setEntitlementAdjustmentTx(ctx, tx, orderID, userID, charged, bonus, false, evidenceKey, "dispute_unfreeze"); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE payment_entitlement_adjustments
		SET freeze_status=$2,freeze_case_id=NULL,updated_at=now() WHERE order_id=$1`,
		orderID, FreezeStatusNone)
	if err != nil {
		return false, err
	}
	return true, nil
}

// WriteOffDisputeFreezeTx marks a previously frozen entitlement as written off
// when a dispute is lost, without double-debiting coins or duplicating clawback.
func WriteOffDisputeFreezeTx(ctx context.Context, tx pgx.Tx, caseID, orderID int64, disputeExternalID string) (bool, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM recharge_orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&status)
	if err != nil {
		return false, err
	}
	if status == "Refunded" {
		// Refund already clawed back the entitlement; clear any stale freeze marker without touching wallet.
		_, err = tx.Exec(ctx, `UPDATE payment_entitlement_adjustments
			SET freeze_status=$2,freeze_case_id=NULL,updated_at=now()
			WHERE order_id=$1 AND freeze_status=$3`,
			orderID, FreezeStatusNone, FreezeStatusFrozen)
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE payment_entitlement_adjustments
		SET freeze_status=$2,freeze_case_id=$3,policy_evidence=$4,updated_at=now()
		WHERE order_id=$1 AND blocked AND freeze_status=$5`,
		orderID, FreezeStatusWrittenOff, caseID, "dispute_lost:"+disputeExternalID, FreezeStatusFrozen)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
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
		if _, err := tx.Exec(ctx, `UPDATE payment_entitlement_adjustments
			SET freeze_status=$2,freeze_case_id=NULL,updated_at=now()
			WHERE order_id=$1 AND freeze_status<>$2`, orderID, FreezeStatusNone); err != nil {
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

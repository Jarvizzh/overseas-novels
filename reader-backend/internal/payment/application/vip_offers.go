package application

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

const vipPlanPublishTimeout = 45 * time.Second

func (s *service) reconcilePayPalVIPOffers(ctx context.Context) {
	if s.paypalClient == nil {
		return
	}
	account, err := s.paypalClient.CheckoutAccount(ctx)
	if err != nil || !paymentEnvironmentMatches(account) {
		return
	}
	if err := preparePayPalVIPPlans(ctx, account); err != nil {
		log.Printf("task=vip_plan action=prepare error=%v", err)
		return
	}
	jobs, err := claimPayPalVIPPlans(ctx, account)
	if err != nil {
		log.Printf("task=vip_plan action=claim error=%v", err)
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		req, cancel := context.WithTimeout(ctx, vipPlanPublishTimeout)
		planID, err := s.paypalClient.CreatePlan(req, job)
		cancel()
		if err != nil {
			log.Printf("task=vip_plan request=%s action=publish error=%v", job.RequestID, err)
			planID = ""
		}
		if err := finishPayPalVIPPlan(ctx, job.RequestID, planID); err != nil {
			log.Printf("task=vip_plan request=%s action=save error=%v", job.RequestID, err)
		}
	}
}

func preparePayPalVIPPlans(ctx context.Context, account payment.WebhookAccount) error {
	_, err := db.Pool(ctx).Exec(ctx, `INSERT INTO payment_paypal_vip_plans(slot_id,account_id)
		SELECT s.vip_offer_version_id,a.id
		FROM recharge_slots s
		JOIN payment_channel_accounts a ON a.provider='paypal' AND a.account_key=$1 AND a.environment=$2
		WHERE s.vip_offer_version_id IS NOT NULL AND NOT EXISTS (
			SELECT 1 FROM payment_paypal_vip_plans bound WHERE bound.slot_id=s.vip_offer_version_id AND bound.account_id=a.id
		)
		ON CONFLICT (slot_id,account_id) DO NOTHING`, account.AccountKey, account.Environment)
	return err
}

func claimPayPalVIPPlans(ctx context.Context, account payment.WebhookAccount) ([]payment.CreateSubscriptionPlanParam, error) {
	// A claimed request can have reached either PayPal write endpoint. Without
	// verified results the binding stays unavailable for manual reconciliation.
	rows, err := db.Pool(ctx).Query(ctx, `WITH expired AS (
		UPDATE payment_paypal_vip_plans p SET review_required=true FROM payment_channel_accounts a
		WHERE a.id=p.account_id AND a.account_key=$1 AND a.environment=$2
		  AND p.plan_id IS NULL AND NOT p.review_required AND p.first_sent_at<now()-make_interval(secs=>$3)
	), due AS (
		SELECT p.slot_id,p.account_id FROM payment_paypal_vip_plans p
		JOIN payment_channel_accounts a ON a.id=p.account_id
		WHERE a.provider='paypal' AND a.account_key=$1 AND a.environment=$2
		  AND p.plan_id IS NULL AND p.first_sent_at IS NULL AND NOT p.review_required
		  AND EXISTS (SELECT 1 FROM recharge_slots s WHERE s.vip_offer_version_id=p.slot_id)
		ORDER BY p.slot_id LIMIT 1 FOR UPDATE OF p SKIP LOCKED
	)
	UPDATE payment_paypal_vip_plans p SET first_sent_at=now()
	FROM due,payment_vip_offer_versions v
	WHERE p.slot_id=due.slot_id AND p.account_id=due.account_id AND v.slot_id=p.slot_id
	RETURNING p.request_key::text,v.cycle,v.amount_minor,v.currency,v.name,v.description`,
		account.AccountKey, account.Environment, (2 * vipPlanPublishTimeout).Seconds())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (payment.CreateSubscriptionPlanParam, error) {
		job := payment.CreateSubscriptionPlanParam{Account: account}
		err := row.Scan(&job.RequestID, &job.Cycle, &job.PriceCents, &job.Currency, &job.PlanName, &job.Description)
		return job, err
	})
}

func finishPayPalVIPPlan(ctx context.Context, requestKey, planID string) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_paypal_vip_plans
		SET plan_id=NULLIF($2,''), review_required=$2=''
		WHERE request_key=$1 AND first_sent_at IS NOT NULL AND plan_id IS NULL AND NOT review_required`, requestKey, planID)
	return err
}

type VIPPlanVerifier interface {
	CheckoutAccount(context.Context) (payment.WebhookAccount, error)
	VerifyVIPPlan(context.Context, string, payment.CreateSubscriptionPlanParam) error
}

// RecoverPayPalVIPPlan binds an already existing Plan after a read-only
// verification with the same PayPal account.
func RecoverPayPalVIPPlan(ctx context.Context, verifier VIPPlanVerifier, requestKey, planID string) error {
	account, err := verifier.CheckoutAccount(ctx)
	if err != nil {
		return err
	}
	offer := payment.CreateSubscriptionPlanParam{Account: account}
	err = db.Pool(ctx).QueryRow(ctx, `SELECT v.cycle,v.amount_minor,v.currency
		FROM payment_paypal_vip_plans p JOIN payment_vip_offer_versions v ON v.slot_id=p.slot_id
		JOIN payment_channel_accounts a ON a.id=p.account_id
		WHERE p.request_key=$1 AND p.review_required AND p.plan_id IS NULL
		  AND a.account_key=$2 AND a.environment=$3`, requestKey, account.AccountKey, account.Environment).
		Scan(&offer.Cycle, &offer.PriceCents, &offer.Currency)
	if err == pgx.ErrNoRows {
		return ErrTaskNotRetryable
	}
	if err != nil {
		return err
	}
	if err := verifier.VerifyVIPPlan(ctx, planID, offer); err != nil {
		return err
	}
	result, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_paypal_vip_plans p SET plan_id=$2,review_required=false
		FROM payment_channel_accounts a
		WHERE p.request_key=$1 AND p.review_required AND p.plan_id IS NULL AND a.id=p.account_id
		  AND a.account_key=$3 AND a.environment=$4`, requestKey, planID, account.AccountKey, account.Environment)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrTaskNotRetryable
	}
	return nil
}

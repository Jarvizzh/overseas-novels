package aftercare

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/payment"
)

const (
	// AutoMitigationActorID identifies system-initiated refund commands created by
	// Stripe EFW/Dispute auto-mitigation policies.
	AutoMitigationActorID = "system:stripe_auto_mitigation"

	salesModeMerchantOfRecord = "merchant_of_record"
)

type stripeMitigationPolicy struct {
	accountID              int64
	caseID                 int64
	autoRefund             bool
	autoCancelSubscription bool
	disputeFreeze          bool
}

type stripeMitigationReceipt struct {
	receiptID      int64
	orderID        int64
	captureID      string
	grossMinor     int64
	orderStatus    string
	fulfilled      bool
	salesMode      string
	subscriptionID string
	intentID       string
}

type efwEvidence struct {
	Actionable bool `json:"Actionable"`
}

type disputeEvidence struct {
	ChargeRefundable bool   `json:"ChargeRefundable"`
	DisputeCategory  string `json:"dispute_category"`
}

// ApplyStripeMitigationTx evaluates account-level Stripe auto-mitigation toggles
// (auto_refund, auto_cancel_subscription, dispute_freeze) for recorded EFW and
// Dispute observations. All toggles default to false and operate independently.
func ApplyStripeMitigationTx(ctx context.Context, tx pgx.Tx, f payment.CaseFact) error {
	if f.Provider != payment.ProviderStripe {
		return nil
	}
	isEFW := f.Kind == payment.CaseRiskSignal && strings.HasPrefix(f.Stage, stripeEFWStagePrefix)
	isDispute := f.Kind == payment.CaseDispute
	if !isEFW && !isDispute {
		return nil
	}

	var policy stripeMitigationPolicy
	err := tx.QueryRow(ctx, `SELECT c.account_id,c.id,
		COALESCE(p.auto_refund,false),COALESCE(p.auto_cancel_subscription,false),COALESCE(p.dispute_freeze,false)
		FROM payment_cases c
		JOIN payment_channel_accounts a ON a.id=c.account_id
		LEFT JOIN payment_aftercare_policies p ON p.account_id=c.account_id
		WHERE a.provider='stripe' AND a.account_key=$1 AND a.environment=$2
		  AND c.kind=$3 AND c.external_id=$4`,
		f.Account.AccountKey, f.Account.Environment, f.Kind, f.ExternalID).
		Scan(&policy.accountID, &policy.caseID, &policy.autoRefund, &policy.autoCancelSubscription, &policy.disputeFreeze)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !policy.autoRefund && !policy.autoCancelSubscription && !policy.disputeFreeze {
		return nil
	}

	receipts, err := loadStripeMitigationReceiptsTx(ctx, tx, policy.caseID, policy.accountID)
	if err != nil || len(receipts) == 0 {
		return err
	}

	if isEFW {
		var ev efwEvidence
		if len(f.Evidence) > 0 {
			_ = json.Unmarshal(f.Evidence, &ev)
		}
		if !ev.Actionable {
			return nil
		}
		for _, receipt := range receipts {
			if policy.autoRefund {
				if err := enqueueStripeAutoRefundTx(ctx, tx, policy, f, receipt, "efw"); err != nil {
					return err
				}
			}
			if policy.autoCancelSubscription {
				if err := enqueueStripeAutoCancelTx(ctx, tx, policy, f, receipt, "efw"); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// Dispute handling.
	var ev disputeEvidence
	if len(f.Evidence) > 0 {
		_ = json.Unmarshal(f.Evidence, &ev)
	}
	status := f.Status
	isInquiryOpen := status == "warning_needs_response" || status == "warning_under_review"
	isFormalOpen := status == "needs_response" || status == "under_review"
	isWonOrClosedNoLoss := status == "won" || status == "warning_closed" || status == "prevented"
	isLost := status == "lost"

	for _, receipt := range receipts {
		if policy.autoRefund && isInquiryOpen && ev.ChargeRefundable {
			if err := enqueueStripeAutoRefundTx(ctx, tx, policy, f, receipt, "inquiry"); err != nil {
				return err
			}
		}
		if policy.autoCancelSubscription && (isInquiryOpen || isFormalOpen) {
			if err := enqueueStripeAutoCancelTx(ctx, tx, policy, f, receipt, "dispute"); err != nil {
				return err
			}
		}
		if policy.disputeFreeze {
			switch {
			case isFormalOpen:
				frozen, err := ApplyDisputeFreezeTx(ctx, tx, policy.caseID, receipt.orderID, f.ExternalID)
				if err != nil {
					return err
				}
				if frozen {
					if err := recordMitigationAuditTx(ctx, tx, policy.caseID,
						"dispute-freeze:"+f.ExternalID+":"+receipt.captureID,
						"dispute_entitlement_frozen",
						"Dispute-period freeze policy froze entitlement during open formal dispute",
						map[string]any{"order_id": receipt.orderID, "charge_id": receipt.captureID}); err != nil {
						return err
					}
				}
			case isWonOrClosedNoLoss:
				unfrozen, err := ReleaseDisputeFreezeTx(ctx, tx, policy.caseID, receipt.orderID, f.ExternalID)
				if err != nil {
					return err
				}
				if unfrozen {
					if err := recordMitigationAuditTx(ctx, tx, policy.caseID,
						"dispute-unfreeze:"+f.ExternalID+":"+receipt.captureID,
						"dispute_entitlement_unfrozen",
						"Dispute closed without loss; unfroze previously frozen entitlement",
						map[string]any{"order_id": receipt.orderID, "charge_id": receipt.captureID, "outcome": status}); err != nil {
						return err
					}
				}
			case isLost:
				writtenOff, err := WriteOffDisputeFreezeTx(ctx, tx, policy.caseID, receipt.orderID, f.ExternalID)
				if err != nil {
					return err
				}
				if writtenOff {
					if err := recordMitigationAuditTx(ctx, tx, policy.caseID,
						"dispute-writeoff:"+f.ExternalID+":"+receipt.captureID,
						"dispute_entitlement_written_off",
						"Dispute lost; wrote off previously frozen entitlement without duplicate debit",
						map[string]any{"order_id": receipt.orderID, "charge_id": receipt.captureID}); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func loadStripeMitigationReceiptsTx(ctx context.Context, tx pgx.Tx, caseID, accountID int64) ([]stripeMitigationReceipt, error) {
	rows, err := tx.Query(ctx, `SELECT r.receipt_id,r.order_id,r.capture_id,(t.gross_amount*100)::bigint,
		o.status,o.fulfilled_at IS NOT NULL,
		COALESCE(
			(SELECT p.sales_mode FROM payment_checkout_attempts p
			 WHERE p.order_id=r.order_id AND p.account_id=$2 AND p.status='settled'
			 ORDER BY p.created_at DESC LIMIT 1),
			(SELECT v.sales_mode FROM payment_stripe_invoice_periods ip
			 JOIN payment_stripe_subscription_intents v ON v.id=ip.intent_id
			 WHERE ip.order_id=r.order_id AND ip.account_id=$2 LIMIT 1),
			'direct'
		) AS sales_mode,
		COALESCE(o.subscription_id,''),
		COALESCE((SELECT ip.intent_id::text
			FROM payment_stripe_invoice_allocations alloc
			JOIN payment_stripe_invoice_periods ip ON ip.account_id=alloc.account_id AND ip.invoice_ref=alloc.invoice_ref
			JOIN payment_stripe_subscription_intents v ON v.id=ip.intent_id
			WHERE alloc.account_id=$2 AND alloc.receipt_id=r.receipt_id AND v.status='bound' AND v.subscription_ref IS NOT NULL
			LIMIT 1),'') AS intent_id
		FROM payment_case_receipts r
		JOIN third_party_payment_orders t ON t.id=r.receipt_id
		JOIN recharge_orders o ON o.id=r.order_id
		WHERE r.case_id=$1 AND t.account_id=$2 AND t.payment_provider='stripe' AND t.object_type='charge' AND t.currency='USD'
		ORDER BY r.receipt_id`, caseID, accountID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (stripeMitigationReceipt, error) {
		var item stripeMitigationReceipt
		err := row.Scan(&item.receiptID, &item.orderID, &item.captureID, &item.grossMinor,
			&item.orderStatus, &item.fulfilled, &item.salesMode, &item.subscriptionID, &item.intentID)
		return item, err
	})
}

func enqueueStripeAutoRefundTx(ctx context.Context, tx pgx.Tx, policy stripeMitigationPolicy, f payment.CaseFact, receipt stripeMitigationReceipt, trigger string) error {
	if receipt.salesMode == salesModeMerchantOfRecord {
		return recordMitigationAuditTx(ctx, tx, policy.caseID,
			"auto-refund-mor:"+receipt.captureID+":"+trigger,
			"auto_refund_skipped_mor",
			"Skipped auto-refund because Stripe Merchant of Record manages dispute/fraud refunds for this charge",
			map[string]any{"charge_id": receipt.captureID, "trigger": trigger})
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stripe-capture:'||$1,0))`, receipt.captureID); err != nil {
		return err
	}
	// If a formal open dispute or lost dispute already exists for this receipt
	// (e.g. out-of-order EFW arriving after Dispute), do not issue a new refund.
	var hasBlockingDispute bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM payment_case_receipts r JOIN payment_cases c ON c.id=r.case_id
		WHERE r.receipt_id=$1 AND c.kind='dispute'
		  AND c.provider_status IN ('needs_response','under_review','lost')
	)`, receipt.receiptID).Scan(&hasBlockingDispute)
	if err != nil {
		return err
	}
	if hasBlockingDispute {
		return recordMitigationAuditTx(ctx, tx, policy.caseID,
			"auto-refund-blocked-dispute:"+receipt.captureID+":"+trigger,
			"auto_refund_routed_to_dispute",
			"Skipped auto-refund because receipt already has a formal or lost dispute",
			map[string]any{"charge_id": receipt.captureID, "trigger": trigger})
	}

	// Reuse existing auto-mitigation command per Charge (handles out-of-order EFW & Inquiry).
	requestKey := uuid.NewSHA1(uuid.NameSpaceOID, []byte("stripe-auto-refund:"+f.Account.AccountKey+":"+f.Account.Environment+":"+receipt.captureID))
	var existingID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM payment_refund_commands
		WHERE environment=$1 AND actor_id=$2 AND request_key=$3`,
		f.Account.Environment, AutoMitigationActorID, requestKey).Scan(&existingID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	var available int64
	err = tx.QueryRow(ctx, `SELECT $1::bigint
		- COALESCE((SELECT sum(amount_minor)::bigint FROM payment_stripe_refund_items WHERE receipt_id=$2),0)
		- COALESCE((SELECT sum(c.amount_minor)::bigint FROM payment_refund_commands c
			WHERE c.receipt_id=$2 AND c.status IN ('pending','confirming','review')
			  AND NOT EXISTS(SELECT 1 FROM payment_stripe_refund_items i
				WHERE i.receipt_id=c.receipt_id AND i.refund_ref=c.provider_refund_id)),0)`,
		receipt.grossMinor, receipt.receiptID).Scan(&available)
	if err != nil {
		return err
	}
	if available <= 0 {
		return nil
	}
	cmdID := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO payment_refund_commands
		(id,environment,actor_id,request_key,order_id,receipt_id,amount_minor,currency,status,next_check_at,stop_loss_full_remaining)
		VALUES($1,$2,$3,$4,$5,$6,$7,'USD','pending',now(),true)
		ON CONFLICT (environment,actor_id,request_key) DO NOTHING`,
		cmdID, f.Account.Environment, AutoMitigationActorID, requestKey, receipt.orderID, receipt.receiptID, available)
	if err != nil {
		return err
	}
	return recordMitigationAuditTx(ctx, tx, policy.caseID,
		"auto-refund-queued:"+receipt.captureID,
		"auto_refund_queued",
		"Queued stop-loss auto-refund command for remaining balance via P1-2 eligibility guard",
		map[string]any{"charge_id": receipt.captureID, "amount_minor": available, "trigger": trigger})
}

func enqueueStripeAutoCancelTx(ctx context.Context, tx pgx.Tx, policy stripeMitigationPolicy, f payment.CaseFact, receipt stripeMitigationReceipt, trigger string) error {
	if receipt.intentID == "" {
		return nil
	}
	if receipt.salesMode == salesModeMerchantOfRecord {
		return recordMitigationAuditTx(ctx, tx, policy.caseID,
			"auto-cancel-mor:"+receipt.intentID+":"+trigger,
			"auto_cancel_skipped_mor",
			"Skipped auto-cancel because Stripe Merchant of Record manages subscription cancellations for this agreement",
			map[string]any{"intent_id": receipt.intentID, "charge_id": receipt.captureID, "trigger": trigger})
	}
	cancelKey := uuid.NewSHA1(uuid.NameSpaceOID, []byte("stripe-auto-cancel:"+f.Account.AccountKey+":"+f.Account.Environment+":"+receipt.intentID))
	tag, err := tx.Exec(ctx, `UPDATE payment_stripe_subscription_intents
		SET cancel_key=COALESCE(cancel_key,$2::uuid),
		    cancel_next_at=COALESCE(cancel_next_at,now())
		WHERE id=$1::uuid AND account_id=$3 AND status='bound' AND subscription_ref IS NOT NULL
		  AND cancelled_at IS NULL AND cancel_key IS NULL`,
		receipt.intentID, cancelKey, policy.accountID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	return recordMitigationAuditTx(ctx, tx, policy.caseID,
		"auto-cancel-queued:"+receipt.intentID,
		"auto_cancel_queued",
		"Queued period-end subscription cancellation for agreement tied to disputed/EFW charge",
		map[string]any{"intent_id": receipt.intentID, "subscription_id": receipt.subscriptionID, "charge_id": receipt.captureID, "trigger": trigger})
}

func recordMitigationAuditTx(ctx context.Context, tx pgx.Tx, caseID int64, keySeed, action, reason string, params map[string]any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	reqID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("stripe-mitigation:"+keySeed))
	_, err = tx.Exec(ctx, `INSERT INTO payment_case_audit(case_id,request_id,actor_id,action,reason,parameters)
		VALUES($1,$2::uuid,NULL,$3,$4,$5::jsonb) ON CONFLICT DO NOTHING`,
		caseID, reqID, action, reason, raw)
	return err
}

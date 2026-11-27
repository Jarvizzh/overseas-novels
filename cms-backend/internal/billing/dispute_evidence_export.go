package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/db"
)

const (
	evidenceExportSchemaVersion             = "digital_delivery_evidence.v1"
	visaCE3StatusRequiresStripeConfirmation = "requires_stripe_confirmation"
	visaCE3ConfirmationNote                 = "Visa Compelling Evidence 3.0 eligibility and required fields must be confirmed from the live Stripe Dispute response before submission."

	evidenceExportMaxUnlockSamples = 20
	evidenceExportMaxDecisionRows  = 20
	evidenceExportMaxEvidenceRows  = 20
	evidenceExportMaxAuditRows     = 50
)

// DigitalDeliveryEvidenceExport is the structured digital-delivery evidence package
// aggregated for a dispute or risk case. Visa CE 3.0 fields remain explicitly
// gated on Stripe response confirmation.
type DigitalDeliveryEvidenceExport struct {
	SchemaVersion      string             `json:"schema_version"`
	GeneratedAt        time.Time          `json:"generated_at"`
	Case               json.RawMessage    `json:"case"`
	Orders             json.RawMessage    `json:"orders"`
	CaseEvidence       json.RawMessage    `json:"case_evidence"`
	CaseAudit          json.RawMessage    `json:"case_audit"`
	VisaCE3Eligibility VisaCE3Eligibility `json:"visa_ce3_eligibility"`
}

// VisaCE3Eligibility records local prior-order signals while keeping CE 3.0
// submission eligibility gated by Stripe's authoritative dispute response.
type VisaCE3Eligibility struct {
	Status                  string `json:"status"`
	StripeConfirmed         bool   `json:"stripe_confirmed"`
	EligibleForSubmission   bool   `json:"eligible_for_submission"`
	PriorUndisputedOrders   int    `json:"prior_undisputed_orders"`
	StripeNetworkReasonCode string `json:"stripe_network_reason_code,omitempty"`
	ConfirmationNote        string `json:"confirmation_note"`
}

func (h *Handler) exportPaymentCaseEvidence(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payment case ID"})
		return
	}
	principal, err := resolvePrincipal(c)
	if err != nil {
		if errors.Is(err, auth.ErrAccountInactive) {
			c.JSON(http.StatusUnauthorized, gin.H{"code": "account_inactive", "error": "Account is disabled or does not exist"})
			return
		}
		writeCaseResult(c, nil, err)
		return
	}
	kind, payload, err := buildDigitalDeliveryEvidenceExport(c.Request.Context(), id)
	if err != nil {
		writeCaseResult(c, nil, err)
		return
	}
	reqPerm := caseReadPermission(kind)
	if reqPerm == "" || !principal.HasPermission(reqPerm) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Payment case access denied"})
		return
	}
	slog.InfoContext(c.Request.Context(), "payment case evidence exported",
		"admin_id", c.GetString("admin_id"), "case_id", id, "case_kind", kind)
	if c.Query("download") == "1" {
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="payment-case-%d-evidence.json"`, id))
	}
	c.JSON(http.StatusOK, payload)
}

func buildDigitalDeliveryEvidenceExport(ctx context.Context, caseID int64) (string, DigitalDeliveryEvidenceExport, error) {
	var kind string
	var caseJSON, ordersJSON, evidenceJSON, auditJSON json.RawMessage
	var priorUndisputed int
	var networkReason string

	err := db.DB.QueryRow(ctx, `WITH target AS (
		SELECT c.id,a.provider,a.environment,a.id AS account_id,c.kind,c.external_id,c.provider_status,
			c.stage,c.reason,c.outcome,c.is_open,c.work_state,c.amount_minor,c.observed_at,c.due_at,
			ra.status AS approval_state
		FROM payment_cases c
		JOIN payment_channel_accounts a ON a.id=c.account_id
		LEFT JOIN payment_stripe_review_approvals ra ON ra.case_id=c.id
		WHERE c.id=$1
	), linked_orders AS (
		SELECT r.receipt_id,r.capture_id,r.order_id,
			o.user_id,o.order_type,o.status AS order_status,o.amount_cents,o.currency AS order_currency,
			o.coins,o.bonus_coins_credited,o.novel_id,o.subscription_id,o.checkout_environment,
			o.created_at AS order_created_at,o.paid_at,o.fulfilled_at,o.paid_period_start,o.paid_period_end,
			t.payment_provider,t.external_order_id,t.status AS receipt_status,
			t.currency AS receipt_currency,t.gross_amount::text AS receipt_gross_amount,
			t.payer_country,t.created_at AS receipt_recorded_at,
			u.created_at AS user_created_at,
			COALESCE(adj.blocked,false) AS entitlement_blocked,
			adj.updated_at AS entitlement_adjusted_at,
			(SELECT count(*)::int FROM recharge_orders prior
			 WHERE prior.user_id=o.user_id AND prior.id<>o.id
			   AND prior.status IN ('Paid','Success')
			   AND prior.created_at<o.created_at
			   AND NOT EXISTS (
				SELECT 1 FROM payment_case_receipts cr
				JOIN payment_cases dc ON dc.id=cr.case_id AND dc.kind IN ('dispute','reversal')
				WHERE cr.order_id=prior.id
			   )) AS prior_undisputed_orders
		FROM payment_case_receipts r
		JOIN recharge_orders o ON o.id=r.order_id
		JOIN third_party_payment_orders t ON t.id=r.receipt_id
		LEFT JOIN users u ON u.id=o.user_id
		LEFT JOIN payment_entitlement_adjustments adj ON adj.order_id=o.id
		WHERE r.case_id=$1
		ORDER BY r.receipt_id
	)
	SELECT target.kind,
		jsonb_build_object(
			'id',target.id::text,
			'provider',target.provider,
			'environment',target.environment,
			'account_id',target.account_id::text,
			'kind',target.kind,
			'external_id',target.external_id,
			'provider_status',target.provider_status,
			'stage',target.stage,
			'reason',target.reason,
			'outcome',target.outcome,
			'is_open',target.is_open,
			'work_state',target.work_state,
			'approval_state',target.approval_state,
			'amount_minor',target.amount_minor::text,
			'observed_at',target.observed_at,
			'due_at',target.due_at
		),
		COALESCE((SELECT jsonb_agg(jsonb_build_object(
			'order',jsonb_build_object(
				'order_id',lo.order_id::text,
				'user_id',lo.user_id::text,
				'order_type',lo.order_type,
				'status',lo.order_status,
				'amount_cents',lo.amount_cents,
				'currency',lo.order_currency,
				'coins',lo.coins,
				'bonus_coins_credited',lo.bonus_coins_credited,
				'novel_id',lo.novel_id,
				'subscription_id',lo.subscription_id,
				'checkout_environment',lo.checkout_environment,
				'created_at',lo.order_created_at,
				'paid_at',lo.paid_at,
				'fulfilled_at',lo.fulfilled_at,
				'paid_period_start',lo.paid_period_start,
				'paid_period_end',lo.paid_period_end
			),
			'payment_receipt',jsonb_build_object(
				'receipt_id',lo.receipt_id::text,
				'provider',lo.payment_provider,
				'capture_id',lo.capture_id,
				'external_order_id',lo.external_order_id,
				'status',lo.receipt_status,
				'currency',lo.receipt_currency,
				'gross_amount',lo.receipt_gross_amount,
				'payer_country',lo.payer_country,
				'recorded_at',lo.receipt_recorded_at
			),
			'entitlement_delivery',jsonb_build_object(
				'delivered',lo.fulfilled_at IS NOT NULL,
				'delivered_at',lo.fulfilled_at,
				'coins_granted',lo.coins,
				'bonus_coins_granted',lo.bonus_coins_credited,
				'entitlement_blocked',lo.entitlement_blocked,
				'entitlement_adjusted_at',lo.entitlement_adjusted_at,
				'unlock_count',(SELECT count(*) FROM unlock_records ur
					WHERE ur.user_id=lo.user_id
					  AND (lo.paid_at IS NULL OR ur.created_at>=lo.paid_at)
					  AND (lo.order_type<>'whole_book' OR ur.novel_id=lo.novel_id)
					  AND (lo.paid_period_end IS NULL OR ur.created_at<lo.paid_period_end)),
				'unlock_samples',COALESCE((SELECT jsonb_agg(to_jsonb(us) ORDER BY us.created_at DESC) FROM (
					SELECT ur.novel_id,ur.chapter_index,ur.created_at
					FROM unlock_records ur
					WHERE ur.user_id=lo.user_id
					  AND (lo.paid_at IS NULL OR ur.created_at>=lo.paid_at)
					  AND (lo.order_type<>'whole_book' OR ur.novel_id=lo.novel_id)
					  AND (lo.paid_period_end IS NULL OR ur.created_at<lo.paid_period_end)
					ORDER BY ur.created_at DESC LIMIT $2
				) us),'[]')
			),
			'verifiable_activity',jsonb_build_object(
				'account_created_at',lo.user_created_at,
				'prior_undisputed_orders',lo.prior_undisputed_orders,
				'wallet_transactions_count',(SELECT count(*) FROM transactions tx
					WHERE tx.user_id=lo.user_id
					  AND (lo.paid_at IS NULL OR tx.created_at>=lo.paid_at)),
				'checkout_decisions',COALESCE((SELECT jsonb_agg(to_jsonb(cd) ORDER BY cd.evaluated_at DESC) FROM (
					SELECT d.stage,d.reason,d.outcome,d.rule_version,d.evaluated_at
					FROM payment_checkout_decisions d
					WHERE d.order_id=lo.order_id
					ORDER BY d.evaluated_at DESC LIMIT $3
				) cd),'[]')
			),
			'refunds',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',refund_ref,'amount_minor',amount_minor)) FROM (
				SELECT f.refund_id AS refund_ref,f.amount_cents::text AS amount_minor
				FROM paypal_refund_items f
				WHERE lo.payment_provider='paypal' AND f.capture_id=lo.capture_id AND f.currency=lo.receipt_currency
				UNION ALL
				SELECT f.refund_ref,f.amount_minor::text
				FROM payment_stripe_refund_items f
				WHERE lo.payment_provider='stripe' AND f.receipt_id=lo.receipt_id
			) known_refunds),'[]')
		) ORDER BY lo.receipt_id) FROM linked_orders lo),'[]'),
		COALESCE((SELECT jsonb_agg(to_jsonb(ev) ORDER BY ev.observed_at DESC) FROM (
			SELECT e.source,e.source_ref,e.observed_at,e.evidence - 'document' AS codes
			FROM payment_case_evidence e
			WHERE e.case_id=$1
			ORDER BY e.observed_at DESC LIMIT $4
		) ev),'[]'),
		COALESCE((SELECT jsonb_agg(to_jsonb(au) ORDER BY au.recorded_at DESC) FROM (
			SELECT COALESCE(a.username,'system') AS actor,x.action,x.reason,x.recorded_at
			FROM payment_case_audit x LEFT JOIN admins a ON a.id=x.actor_id
			WHERE x.case_id=$1
			ORDER BY x.recorded_at DESC LIMIT $5
		) au),'[]'),
		COALESCE((SELECT max(prior_undisputed_orders) FROM linked_orders),0),
		COALESCE((SELECT e.evidence->>'NetworkReason' FROM payment_case_evidence e
			WHERE e.case_id=$1 AND COALESCE(e.evidence->>'NetworkReason','')<>''
			ORDER BY e.observed_at DESC LIMIT 1),'')
	FROM target`,
		caseID,
		evidenceExportMaxUnlockSamples,
		evidenceExportMaxDecisionRows,
		evidenceExportMaxEvidenceRows,
		evidenceExportMaxAuditRows,
	).Scan(&kind, &caseJSON, &ordersJSON, &evidenceJSON, &auditJSON, &priorUndisputed, &networkReason)
	if err != nil {
		return "", DigitalDeliveryEvidenceExport{}, err
	}

	return kind, DigitalDeliveryEvidenceExport{
		SchemaVersion: evidenceExportSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Case:          caseJSON,
		Orders:        ordersJSON,
		CaseEvidence:  evidenceJSON,
		CaseAudit:     auditJSON,
		VisaCE3Eligibility: VisaCE3Eligibility{
			Status:                  visaCE3StatusRequiresStripeConfirmation,
			StripeConfirmed:         false,
			EligibleForSubmission:   false,
			PriorUndisputedOrders:   priorUndisputed,
			StripeNetworkReasonCode: networkReason,
			ConfirmationNote:        visaCE3ConfirmationNote,
		},
	}, nil
}

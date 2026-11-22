package billing

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

var (
	errCaseConflict  = errors.New("payment case action is stale or inconsistent")
	errCaseForbidden = errors.New("payment case action requires finance permission")
)

// These predicates are used both to project available actions and to recheck
// eligibility under the case lock at submission time.
const caseCanApproveSQL = `c.kind='risk_review' AND c.work_state IN ('pending','refund_due','dispute_due')
	AND NOT EXISTS(SELECT 1 FROM payment_case_receipts r JOIN payment_case_receipts d ON d.receipt_id=r.receipt_id
		JOIN payment_cases dc ON dc.id=d.case_id AND dc.kind='dispute' AND dc.is_open WHERE r.case_id=c.id)`

const caseCanRejectSQL = `c.kind='risk_review' AND c.work_state='pending'
	AND EXISTS(SELECT 1 FROM payment_aftercare_policies p JOIN admins finance ON finance.id=p.finance_id AND finance.status=1
		AND finance.role IN ('SuperAdmin','Finance') WHERE p.account_id=c.account_id AND p.refund_seconds IS NOT NULL)
	AND EXISTS(SELECT 1 FROM payment_deferred_fulfillment h JOIN recharge_orders o ON o.id=h.order_id
		WHERE h.case_id=c.id AND h.resolved_at IS NULL AND o.fulfilled_at IS NULL AND o.status IN ('Success','Paid'))`

const caseCanAdjustEntitlementSQL = `l.evidence_key IS NOT NULL AND COALESCE(adj.blocked,false)<>l.target_blocked
	AND o.fulfilled_at IS NOT NULL AND o.status<>'Refunded'
	AND NOT EXISTS(SELECT 1 FROM payment_loss_commands pending WHERE pending.order_id=o.id AND pending.status='pending')`

func applyCaseAction(ctx context.Context, actor string, id int64, input caseAction) error {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" {
		return errCaseConflict
	}
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = lockCaseOperator(ctx, tx, actor); err != nil {
		return err
	}
	var kind string
	err = tx.QueryRow(ctx, `SELECT kind FROM payment_cases WHERE id=$1 FOR UPDATE`, id).Scan(&kind)
	if err != nil {
		return err
	}
	var priorAction, priorActor, priorReason string
	var matches bool
	err = tx.QueryRow(ctx, `SELECT action,COALESCE(actor_id,''),reason,
  parameters=jsonb_build_object('owner',$3::text,'evidence_key',$4::text) FROM payment_case_audit WHERE case_id=$1 AND request_id=$2`,
		id, input.RequestID, input.Owner, input.EvidenceKey).Scan(&priorAction, &priorActor, &priorReason, &matches)
	switch {
	case err == nil && priorAction == input.Action && priorActor == actor && priorReason == input.Reason && matches:
		return tx.Commit(ctx)
	case err == nil:
		return errCaseConflict
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	switch input.Action {
	case "assign":
		if err = lockCaseOperator(ctx, tx, input.Owner); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE payment_cases SET owner_id=$2,updated_at=now() WHERE id=$1`, id, input.Owner)
	case "approve", "reject":
		err = reviewCase(ctx, tx, id, input.Action)
	case "apply_loss", "restore":
		if kind != "dispute" && kind != "reversal" {
			return errCaseConflict
		}
		err = submitLossAction(ctx, tx, id, actor, input)
	default:
		return errCaseConflict
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_case_audit(case_id,request_id,actor_id,action,reason,parameters)
 VALUES($1,$2,$3,$4,$5,jsonb_build_object('owner',$6::text,'evidence_key',$7::text))`, id, input.RequestID, actor, input.Action, input.Reason, input.Owner, input.EvidenceKey)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockCaseOperator(ctx context.Context, tx pgx.Tx, actor string) error {
	var active bool
	err := tx.QueryRow(ctx, `SELECT true FROM admins WHERE id=$1 AND status=1 AND role IN ('SuperAdmin','Finance') FOR SHARE`, actor).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return errCaseForbidden
	}
	return err
}

func reviewCase(ctx context.Context, tx pgx.Tx, id int64, decision string) error {
	if decision == "approve" {
		tag, err := tx.Exec(ctx, `UPDATE payment_cases c SET work_state='approved',updated_at=now()
			WHERE c.id=$1 AND `+caseCanApproveSQL, id)
		if err == nil && tag.RowsAffected() != 1 {
			return errCaseConflict
		}
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE payment_cases c SET
		work_state=CASE WHEN EXISTS(SELECT 1 FROM payment_deferred_fulfillment h
			JOIN payment_case_receipts r ON r.receipt_id=h.receipt_id
			JOIN payment_cases d ON d.id=r.case_id AND d.kind='dispute' AND d.is_open
			WHERE h.case_id=c.id AND h.resolved_at IS NULL) THEN 'dispute_due' ELSE 'refund_due' END,
		owner_id=p.finance_id,work_due_at=now()+make_interval(secs=>p.refund_seconds),updated_at=now()
		FROM payment_aftercare_policies p WHERE p.account_id=c.account_id AND c.id=$1 AND `+caseCanRejectSQL, id)
	if err == nil && tag.RowsAffected() != 1 {
		return errCaseConflict
	}
	return err
}

func submitLossAction(ctx context.Context, tx pgx.Tx, id int64, actor string, input caseAction) error {
	tag, err := tx.Exec(ctx, `INSERT INTO payment_loss_commands(id,case_id,order_id,requested_by,blocked,evidence_key)
		SELECT DISTINCT $1::uuid,r.case_id,l.order_id,$3::text,l.target_blocked,l.evidence_key
		FROM payment_principal_loss l JOIN payment_fulfillment_funding funding ON funding.order_id=l.order_id
		JOIN payment_case_receipts r ON r.receipt_id=funding.receipt_id AND r.order_id=funding.order_id
		JOIN recharge_orders o ON o.id=l.order_id
		LEFT JOIN payment_entitlement_adjustments adj ON adj.order_id=l.order_id
		WHERE r.case_id=$2 AND l.target_blocked=$4 AND l.evidence_key=$5
		  AND `+caseCanAdjustEntitlementSQL+`
		ON CONFLICT DO NOTHING`, input.RequestID, id, actor, input.Action == "apply_loss", input.EvidenceKey)
	if err == nil && tag.RowsAffected() != 1 {
		return errCaseConflict
	}
	return err
}

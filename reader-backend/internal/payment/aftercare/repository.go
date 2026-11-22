package aftercare

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/payment"
)

const RefundDisputeReviewNote = "Active dispute on original receipt"

type Store interface {
	Begin(context.Context) (pgx.Tx, error)
}

type Repository struct{ store Store }

func NewRepository(store Store) *Repository { return &Repository{store: store} }

func (r *Repository) Record(ctx context.Context, fact payment.CaseFact) error {
	tx, err := r.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := RecordTx(ctx, tx, fact); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func RecordTx(ctx context.Context, tx pgx.Tx, f payment.CaseFact) error {
	if f.ExternalID == "" || f.SourceRef == "" || f.ObservedAt.IsZero() || !json.Valid(f.Evidence) {
		return errors.New("incomplete payment case evidence")
	}
	var accountID, caseID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM payment_channel_accounts WHERE provider=$1 AND account_key=$2 AND environment=$3`,
		f.Provider, f.Account.AccountKey, f.Account.Environment).Scan(&accountID); err != nil {
		return err
	}
	if f.ReceiptRefs == nil {
		f.ReceiptRefs = []string{}
	}
	err := tx.QueryRow(ctx, `
		INSERT INTO payment_cases(account_id,kind,external_id,receipt_refs,provider_status,stage,reason,outcome,is_open,due_at,amount_minor,observed_at,order_ref)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,''))
		ON CONFLICT(account_id,kind,external_id) DO UPDATE SET
			receipt_refs=CASE WHEN cardinality(EXCLUDED.receipt_refs)>0 AND EXCLUDED.observed_at>=payment_cases.observed_at
			                  THEN EXCLUDED.receipt_refs ELSE payment_cases.receipt_refs END,
			provider_status=EXCLUDED.provider_status,stage=EXCLUDED.stage,reason=EXCLUDED.reason,outcome=EXCLUDED.outcome,
			is_open=EXCLUDED.is_open,due_at=EXCLUDED.due_at,amount_minor=EXCLUDED.amount_minor,
			order_ref=COALESCE(EXCLUDED.order_ref,payment_cases.order_ref),observed_at=EXCLUDED.observed_at,updated_at=clock_timestamp()
		WHERE payment_cases.provider_status<>'ledger_confirmed' AND (EXCLUDED.observed_at>payment_cases.observed_at OR
		    (EXCLUDED.observed_at=payment_cases.observed_at AND NOT EXCLUDED.is_open AND payment_cases.is_open) OR
            (EXCLUDED.observed_at=payment_cases.observed_at AND cardinality(payment_cases.receipt_refs)=0
                AND cardinality(EXCLUDED.receipt_refs)>0))
		RETURNING id`, accountID, f.Kind, f.ExternalID, f.ReceiptRefs, f.Status, f.Stage, f.Reason, f.Outcome, f.Open, f.DueAt, f.AmountMinor, f.ObservedAt, f.OrderRef).Scan(&caseID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id FROM payment_cases WHERE account_id=$1 AND kind=$2 AND external_id=$3`, accountID, f.Kind, f.ExternalID).Scan(&caseID)
	}
	if err != nil {
		return err
	}
	digest := sha256.Sum256(f.Evidence)
	_, err = tx.Exec(ctx, `INSERT INTO payment_case_evidence(case_id,source,source_ref,digest,observed_at,evidence)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, caseID, f.Source, f.SourceRef, digest[:], f.ObservedAt, f.Evidence)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE payment_cases c SET work_state='pending',owner_id=p.reviewer_id,
		work_due_at=COALESCE(c.due_at,now()+make_interval(secs=>p.review_seconds))
		FROM payment_aftercare_policies p WHERE c.id=$1 AND p.account_id=c.account_id AND c.kind='risk_review'
		 AND c.is_open AND c.work_state='observed' AND p.hold_fulfillment`, caseID)
	if err != nil {
		return err
	}
	if f.Kind == payment.CaseDispute {
		_, err = tx.Exec(ctx, `UPDATE payment_cases c SET owner_id=p.finance_id
			FROM payment_aftercare_policies p JOIN admins finance ON finance.id=p.finance_id AND finance.status=1
			  AND finance.role IN ('SuperAdmin','Finance')
			WHERE c.id=$1 AND c.account_id=p.account_id AND c.owner_id IS NULL`, caseID)
	}

	return err
}

// MaintainWork persists due reminders, so replaying the worker is harmless.
func MaintainWork(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `WITH incoming AS (
		SELECT m.account_id,r.object_id,cr.object_id AS receipt,m.occurred_at,g.amount_minor
		FROM payment_ledger_movements m JOIN payment_channel_accounts a ON a.id=m.account_id AND a.provider='paypal'
		JOIN payment_ledger_references r ON r.movement_id=m.id AND r.object_type='paypal_reversal'
		JOIN payment_ledger_references cr ON cr.movement_id=m.id AND cr.object_type='paypal_transaction'
		JOIN payment_ledger_receipt_matches x ON x.movement_id=m.id
		JOIN payment_ledger_amounts g ON g.movement_id=m.id AND g.component='gross' AND g.currency='USD'
		WHERE m.kind='reversal' AND m.provider_code='T1106' AND m.currency='USD' AND g.amount_minor<0
		AND NOT EXISTS(SELECT 1 FROM payment_cases c WHERE c.account_id=m.account_id AND c.kind='reversal' AND c.external_id=r.object_id
			AND c.provider_status='ledger_confirmed')
		ORDER BY m.id LIMIT 100
	)
	INSERT INTO payment_cases(account_id,kind,external_id,receipt_refs,provider_status,stage,is_open,observed_at,amount_minor)
	SELECT account_id,object_kind,object_id,ARRAY[receipt],'ledger_confirmed','T1106',false,COALESCE(occurred_at,now()),-amount_minor
	FROM (SELECT DISTINCT ON (account_id,object_id) *, 'reversal' AS object_kind
	      FROM incoming ORDER BY account_id,object_id,occurred_at DESC NULLS LAST) latest
	ON CONFLICT(account_id,kind,external_id) DO UPDATE SET provider_status=EXCLUDED.provider_status,
		receipt_refs=EXCLUDED.receipt_refs,amount_minor=EXCLUDED.amount_minor,is_open=false,
		observed_at=GREATEST(EXCLUDED.observed_at,payment_cases.observed_at),updated_at=now()`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `INSERT INTO payment_case_reminders(case_id,due_at)
		SELECT c.id,c.due_at FROM payment_cases c JOIN payment_aftercare_policies p ON p.account_id=c.account_id
		WHERE c.kind='dispute' AND c.is_open AND c.due_at IS NOT NULL
		 AND c.due_at<=now()+make_interval(secs=>p.reminder_seconds)
		 AND NOT EXISTS(SELECT 1 FROM payment_case_reminders r WHERE r.case_id=c.id AND r.due_at=c.due_at)
		ORDER BY c.due_at,c.id LIMIT 100 ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH due AS (
		SELECT c.id,p.finance_id FROM payment_cases c JOIN payment_aftercare_policies p ON p.account_id=c.account_id
		WHERE c.work_state='pending' AND c.work_due_at<=now() AND c.escalated_at IS NULL
		ORDER BY c.work_due_at,c.id LIMIT 100 FOR UPDATE OF c SKIP LOCKED
	)
	UPDATE payment_cases c SET owner_id=due.finance_id,escalated_at=now() FROM due WHERE c.id=due.id`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH due AS (
		SELECT c.id,c.work_state,p.refund_seconds,
			EXISTS(SELECT 1 FROM payment_case_receipts cr JOIN payment_case_receipts dr ON dr.receipt_id=cr.receipt_id
				JOIN payment_cases dispute ON dispute.id=dr.case_id AND dispute.kind='dispute' AND dispute.is_open
				WHERE cr.case_id=c.id) AS dispute_exists,
			EXISTS(SELECT 1 FROM payment_deferred_fulfillment h WHERE h.case_id=c.id) AND NOT EXISTS(SELECT 1 FROM payment_deferred_fulfillment h JOIN recharge_orders o ON o.id=h.order_id
				WHERE h.case_id=c.id AND h.resolved_at IS NULL AND o.status<>'Refunded' AND o.fulfilled_at IS NULL) AS resolved
		FROM payment_cases c LEFT JOIN payment_aftercare_policies p ON p.account_id=c.account_id
		WHERE c.work_state IN ('approved','refund_due','dispute_due')
		ORDER BY c.updated_at,c.id LIMIT 100 FOR UPDATE OF c SKIP LOCKED
	), updates AS (
		SELECT id,refund_seconds,CASE WHEN resolved THEN 'closed'
		    WHEN work_state='approved' THEN work_state
		    WHEN dispute_exists THEN 'dispute_due' ELSE 'refund_due' END AS state FROM due
	)
	UPDATE payment_cases c SET work_state=u.state,updated_at=now(),
		work_due_at=CASE WHEN u.state='refund_due' THEN now()+make_interval(secs=>u.refund_seconds) ELSE c.work_due_at END
	FROM updates u WHERE u.id=c.id AND u.state<>c.work_state`)

	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH ready AS (
		SELECT cmd.id FROM payment_refund_commands cmd
		WHERE cmd.status='review' AND cmd.result_note=$1 AND cmd.first_sent_at IS NULL
			AND cmd.claimed_until IS NULL AND cmd.provider_refund_id IS NULL
			AND NOT EXISTS(SELECT 1 FROM payment_case_receipts r JOIN payment_cases c ON c.id=r.case_id
				WHERE r.receipt_id=cmd.receipt_id AND c.kind='dispute' AND c.is_open)
			AND EXISTS(SELECT 1 FROM payment_case_receipts r JOIN payment_cases c ON c.id=r.case_id
				WHERE r.receipt_id=cmd.receipt_id AND c.kind='dispute' AND NOT c.is_open)
		ORDER BY cmd.updated_at,cmd.id LIMIT 100 FOR UPDATE OF cmd SKIP LOCKED
	) UPDATE payment_refund_commands cmd SET status='pending',next_check_at=now(),result_note=NULL,updated_at=now()
	FROM ready WHERE cmd.id=ready.id`, RefundDisputeReviewNote)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH done AS (
		SELECT h.order_id,h.receipt_id FROM payment_deferred_fulfillment h JOIN recharge_orders o ON o.id=h.order_id
		WHERE h.resolved_at IS NULL AND o.status='Refunded' ORDER BY h.order_id LIMIT 100 FOR UPDATE OF h SKIP LOCKED
	) UPDATE payment_deferred_fulfillment h SET resolved_at=now() FROM done WHERE h.order_id=done.order_id AND h.receipt_id=done.receipt_id`)

	return err
}

func (r *Repository) Maintain(ctx context.Context) error {
	tx, err := r.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := MaintainWork(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

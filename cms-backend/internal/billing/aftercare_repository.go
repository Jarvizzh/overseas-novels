package billing

import (
	"context"
	"encoding/json"

	"star-novel-cms/internal/db"
)

const caseColumns = `c.id::text AS id,a.provider,a.environment,a.id::text AS account_id,c.kind,c.external_id,c.receipt_refs,
	c.provider_status,c.stage,c.reason,c.outcome,c.is_open,c.due_at,c.work_due_at,c.work_state,c.escalated_at,c.updated_at,
	c.owner_id,owner.username AS owner,c.amount_minor::text AS amount_minor,
	CASE WHEN a.provider='paypal' THEN 'direct' ELSE modes.mode END AS sales_mode,
	EXISTS(SELECT 1 FROM payment_case_reminders r WHERE r.case_id=c.id AND r.due_at=c.due_at) AS reminder,
	ARRAY(SELECT order_id FROM (
		SELECT r.order_id::text FROM payment_case_receipts r WHERE r.case_id=c.id
		UNION SELECT p.order_id::text FROM payment_checkout_attempts p
		WHERE p.account_id=c.account_id AND p.provider_order_id=c.order_ref
	) ids ORDER BY order_id) AS order_ids`

const caseJoins = ` FROM payment_cases c JOIN payment_channel_accounts a ON a.id=c.account_id
	LEFT JOIN admins owner ON owner.id=c.owner_id
	LEFT JOIN LATERAL (
		SELECT CASE WHEN count(DISTINCT source.sales_mode)=1 THEN min(source.sales_mode) END AS mode FROM (
			SELECT p.sales_mode FROM payment_case_receipts r JOIN payment_checkout_attempts p
				ON p.order_id=r.order_id AND p.account_id=c.account_id WHERE r.case_id=c.id AND p.status='settled'
			UNION ALL
			SELECT v.sales_mode FROM payment_case_receipts r JOIN payment_stripe_invoice_periods p
				ON p.order_id=r.order_id AND p.account_id=c.account_id
				JOIN payment_stripe_subscription_intents v ON v.id=p.intent_id WHERE r.case_id=c.id
		) source
	) modes ON true`

func listCases(ctx context.Context, f caseFilter) (json.RawMessage, error) {
	var result json.RawMessage
	err := db.DB.QueryRow(ctx, `WITH scope AS (
		SELECT id,provider,status,credentials_ref,active_version FROM payment_channel_accounts
		WHERE $10='risk_signal' AND environment=$1 AND ($2='' OR provider=$2) AND ($12::bigint=0 OR id=$12)
	), candidates AS (
	SELECT `+caseColumns+`,c.id AS sort_id`+caseJoins+`
	WHERE a.environment=$1 AND ($2='' OR a.provider=$2)
	 AND (($3='risk' AND c.kind IN ('risk_signal','risk_review')) OR ($3='disputes' AND c.kind IN ('dispute','reversal')))
	 AND ($4::bigint=0 OR EXISTS(SELECT 1 FROM payment_case_receipts r WHERE r.case_id=c.id AND r.order_id=$4) OR EXISTS(SELECT 1 FROM payment_checkout_attempts p
      WHERE p.order_id=$4 AND p.account_id=c.account_id AND p.provider_order_id=c.order_ref))
	 AND ($5='' OR c.receipt_refs @> ARRAY[$5::text]) AND ($6='' OR c.external_id=$6)
	 AND ($7::bigint=0 OR c.id<$7)
	 AND ($10='' OR c.kind=$10) AND ($11='' OR c.owner_id=$11)
	 AND ($12::bigint=0 OR a.id=$12)
	 AND ($13='' OR c.work_state=$13)
	 AND ($14='' OR (
		 (c.kind='dispute' AND c.is_open OR c.kind='risk_review' AND c.work_state IN ('pending','refund_due','dispute_due'))
		 AND CASE $14
			WHEN 'overdue' THEN COALESCE(c.due_at,c.work_due_at)<statement_timestamp()
			WHEN 'upcoming' THEN COALESCE(c.due_at,c.work_due_at)>=statement_timestamp()
				AND COALESCE(c.due_at,c.work_due_at)<statement_timestamp()+interval '24 hours'
		 END
	 ))
 AND (NOT $9 OR (c.work_state IN ('pending','refund_due','dispute_due') OR c.is_open AND EXISTS(
     SELECT 1 FROM payment_case_reminders rem WHERE rem.case_id=c.id AND rem.due_at=c.due_at)))
	ORDER BY c.id DESC LIMIT ($8::integer+1)
	), page AS (SELECT * FROM candidates ORDER BY sort_id DESC LIMIT $8)
	SELECT jsonb_build_object('items',COALESCE(jsonb_agg(to_jsonb(page)-'sort_id' ORDER BY sort_id DESC),'[]'),
		'next_cursor',CASE WHEN (SELECT count(*) FROM candidates)>$8 THEN min(sort_id)::text END,
		'signal_state',CASE WHEN $10='risk_signal' THEN CASE
				WHEN EXISTS (SELECT 1 FROM payment_cases signal JOIN scope ON scope.id=signal.account_id WHERE signal.kind='risk_signal')
					THEN 'collected'
				WHEN EXISTS (
					SELECT 1 FROM scope a WHERE a.status IN ('verified','published','disabled')
						AND (a.credentials_ref<>'db:encrypted' OR EXISTS (
							SELECT 1 FROM payment_channel_credentials cred WHERE cred.account_id=a.id AND cred.version=a.active_version
								AND cred.purpose=CASE a.provider WHEN 'stripe' THEN 'webhook_secret' ELSE 'webhook_id' END
						))
				) THEN 'awaiting_signals'
				ELSE 'unconfigured'
			END END) FROM page`,
		f.environment(), f.Provider, f.View, f.OrderID, f.Receipt, f.ExternalID, f.Before,
		reconciliationPageSize, f.Attention, f.Kind, f.Owner, f.AccountID, f.WorkState, f.Deadline).Scan(&result)
	return result, err
}

func getCase(ctx context.Context, id int64) (json.RawMessage, error) {
	var result json.RawMessage
	err := db.DB.QueryRow(ctx, `WITH detail AS (SELECT `+caseColumns+`,
		array_remove(ARRAY['assign',CASE WHEN `+caseCanApproveSQL+` THEN 'approve' END,
			CASE WHEN `+caseCanRejectSQL+` THEN 'reject' END],NULL) AS available_actions`+caseJoins+` WHERE c.id=$1)
	SELECT to_jsonb(detail)||jsonb_build_object(
		'receipts',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.receipt_id::text,'receipt',r.capture_id,
			'order_id',r.order_id::text,'type',o.order_type,'state',o.status,'paid_at',o.paid_at,'fulfilled_at',o.fulfilled_at,
			'paid_period_start',o.paid_period_start,'paid_period_end',o.paid_period_end,
			'usage_count',(SELECT count(*) FROM unlock_records u WHERE u.user_id=o.user_id AND u.created_at>=o.paid_at
				AND (o.order_type<>'whole_book' OR u.novel_id=o.novel_id)
				AND (o.paid_period_end IS NULL OR u.created_at<o.paid_period_end)),
			 'refunds',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',refund_ref,'amount_minor',amount_minor))
                FROM (
                    SELECT f.refund_id AS refund_ref,f.amount_cents::text AS amount_minor FROM paypal_refund_items f
                    WHERE t.payment_provider='paypal' AND f.capture_id=r.capture_id AND f.currency=t.currency
                    UNION ALL
                    SELECT f.refund_ref,f.amount_minor::text FROM payment_stripe_refund_items f
                    WHERE t.payment_provider='stripe' AND f.receipt_id=r.receipt_id
                ) known_refunds),'[]'),
			'loss_minor',l.loss_minor::text,'target_blocked',l.target_blocked,'evidence_key',l.evidence_key,
			'blocked',COALESCE(adj.blocked,false),'adjustment_pending',EXISTS(
				SELECT 1 FROM payment_loss_commands cmd WHERE cmd.order_id=o.id AND cmd.status='pending'),
			'entitlement_action',CASE WHEN detail.kind IN ('dispute','reversal') AND `+caseCanAdjustEntitlementSQL+`
				THEN CASE WHEN l.target_blocked THEN 'apply_loss' ELSE 'restore' END END,
			'entitlement_result',last_command.result
			) ORDER BY r.receipt_id) FROM payment_case_receipts r JOIN recharge_orders o ON o.id=r.order_id
			JOIN third_party_payment_orders t ON t.id=r.receipt_id
			LEFT JOIN payment_fulfillment_funding funding ON funding.order_id=r.order_id AND funding.receipt_id=r.receipt_id
            LEFT JOIN payment_principal_loss l ON l.order_id=funding.order_id
			LEFT JOIN payment_entitlement_adjustments adj ON adj.order_id=r.order_id
			LEFT JOIN LATERAL (
				SELECT jsonb_build_object('id',cmd.id::text,'status',cmd.status,'blocked',cmd.blocked,
					'requested_at',cmd.requested_at,'finished_at',cmd.finished_at) AS result
				FROM payment_loss_commands cmd WHERE cmd.case_id=r.case_id AND cmd.order_id=r.order_id
				ORDER BY cmd.requested_at DESC,cmd.id DESC LIMIT 1
			) last_command ON true WHERE r.case_id=$1),'[]'),
		'audit',COALESCE((SELECT jsonb_agg(to_jsonb(audit_row) ORDER BY recorded_at DESC) FROM (
			SELECT coalesce(a.username,'system') AS actor,x.action,x.reason,x.recorded_at
			FROM payment_case_audit x LEFT JOIN admins a ON a.id=x.actor_id WHERE x.case_id=$1
			ORDER BY x.recorded_at DESC LIMIT 50
		) audit_row),'[]'),
		'sources',COALESCE((SELECT jsonb_agg(to_jsonb(item) ORDER BY observed_at DESC) FROM (
			SELECT source,source_ref,observed_at,evidence - 'document' AS codes
			FROM payment_case_evidence WHERE case_id=$1 AND (SELECT kind FROM detail)='risk_signal'
			ORDER BY observed_at DESC LIMIT 20
		) item),'[]')
	) FROM detail`, id).Scan(&result)
	return result, err
}

func listDecisions(ctx context.Context, orderID, before int64, environment, provider string) (json.RawMessage, error) {
	var data json.RawMessage
	err := db.DB.QueryRow(ctx, `WITH candidates AS (
		SELECT id::text AS id,order_id::text AS order_id,provider,checkout_environment,stage,reason,outcome,rule_version,evaluated_at,
			id AS sort_id FROM payment_checkout_decisions WHERE ($1::bigint=0 OR order_id=$1) AND ($2::bigint=0 OR id<$2)
				AND ($3='' OR checkout_environment=$3) AND ($5='' OR provider=$5) ORDER BY id DESC LIMIT ($4::integer+1)
	), page AS (SELECT * FROM candidates ORDER BY sort_id DESC LIMIT $4)
	SELECT jsonb_build_object('items',COALESCE(jsonb_agg(to_jsonb(page)-'sort_id' ORDER BY sort_id DESC),'[]'),
		'next_cursor',CASE WHEN (SELECT count(*) FROM candidates)>$4 THEN min(sort_id)::text END) FROM page`,
		orderID, before, environment, reconciliationPageSize, provider).Scan(&data)
	return data, err
}

func listFinanceOwners(ctx context.Context) (json.RawMessage, error) {
	var data json.RawMessage
	err := db.DB.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id,'name',username) ORDER BY username),'[]')
		FROM admins WHERE status=1 AND role IN ('SuperAdmin','Finance')`).Scan(&data)
	return data, err
}

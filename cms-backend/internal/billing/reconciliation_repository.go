package billing

import (
	"context"
	"time"

	"star-novel-cms/internal/db"
)

// Payout membership comes from the recorded transfer/reversal and the verified
// Stripe allocation references used by payment_settlement_payout_checks.
const reconciliationMembershipSQL = `
	(p.transfer_key=m.movement_key OR p.reversal_key=m.movement_key OR
	 (a.provider='stripe' AND EXISTS (
		SELECT 1 FROM payment_ledger_references r WHERE r.movement_id=m.id
			AND r.object_type='stripe_payout_allocation' AND r.object_id=p.external_ref
	 )))`

func (r *dbBillingRepository) ListLedgerMovements(ctx context.Context, f movementFilter) (movementPage, error) {
	var data movementPage
	var before *time.Time
	if f.Through != nil {
		nextDay := f.Through.AddDate(0, 0, 1)
		before = &nextDay
	}
	err := db.DB.QueryRow(ctx, `
		WITH filtered AS MATERIALIZED (
			SELECT m.*,a.provider, a.environment
			FROM payment_ledger_movements m JOIN payment_channel_accounts a ON a.id=m.account_id
			WHERE m.currency=$1 AND a.environment=$2 AND ($3='' OR a.provider=$3)
				AND (NOT $4 OR m.review_required)
				AND ($5::bigint=0 OR EXISTS (
					SELECT 1 FROM payment_ledger_receipt_matches x
					JOIN third_party_payment_orders t ON t.id=x.receipt_id
					WHERE x.movement_id=m.id AND t.order_id=$5
				))
				AND ($9::bigint=0 OR m.id=$9) AND ($10::bigint=0 OR a.id=$10)
				AND ($11='' OR m.movement_key=$11) AND ($12='' OR m.kind=$12)
				AND ($13::timestamptz IS NULL OR m.occurred_at >= $13)
				AND ($14::timestamptz IS NULL OR m.occurred_at < $14)
				AND ($6::bigint=0 OR EXISTS (
					SELECT 1 FROM payment_settlement_payouts p WHERE p.id=$6 AND p.account_id=m.account_id
						AND p.currency=m.currency AND `+reconciliationMembershipSQL+`
				))
		), totals AS (
			SELECT jsonb_object_agg(component, total::text) AS amounts
			FROM (
				SELECT v.component,sum(v.amount_minor) AS total
				FROM filtered m JOIN payment_ledger_amounts v ON v.movement_id=m.id AND v.currency=m.currency
				WHERE m.posted_at IS NOT NULL AND NOT m.review_required
				GROUP BY v.component
			) amounts
		), page AS (
			SELECT m.id::text AS id,m.movement_key,m.kind,m.provider,m.environment,m.account_id::text AS account_id,
				m.provider_code,m.occurred_at,m.review_required,
				COALESCE(amounts.values,'{}') AS amounts,COALESCE(refs.values,'[]') AS refs,
				COALESCE(orders.ids,'[]') AS order_ids,COALESCE(payouts.items,'[]') AS payouts,
				m.id AS sort_id
			FROM (SELECT * FROM filtered ORDER BY occurred_at DESC NULLS LAST,id DESC LIMIT $7 OFFSET $8) m
			JOIN payment_channel_accounts a ON a.id=m.account_id
			LEFT JOIN LATERAL (
				SELECT jsonb_object_agg(component,amount_minor::text) AS values FROM payment_ledger_amounts
				WHERE movement_id=m.id AND currency=m.currency
			) amounts ON true
			LEFT JOIN LATERAL (
				SELECT jsonb_agg(jsonb_build_object('type',object_type,'id',object_id) ORDER BY object_type,object_id) AS values
				FROM payment_ledger_references WHERE movement_id=m.id
			) refs ON true
			LEFT JOIN LATERAL (
				SELECT jsonb_agg(DISTINCT t.order_id::text) FILTER (WHERE t.order_id IS NOT NULL) AS ids
				FROM payment_ledger_receipt_matches x JOIN third_party_payment_orders t ON t.id=x.receipt_id
				WHERE x.movement_id=m.id
			) orders ON true
			LEFT JOIN LATERAL (
				SELECT jsonb_agg(jsonb_build_object('id',p.id::text,'external_ref',p.external_ref) ORDER BY p.id) AS items
				FROM payment_settlement_payouts p WHERE p.account_id=m.account_id AND p.currency=m.currency
					AND `+reconciliationMembershipSQL+`
			) payouts ON true
		)
		SELECT jsonb_build_object('currency',$1::text,'total',(SELECT count(*) FROM filtered),
			'totals',COALESCE((SELECT amounts FROM totals),'{}'),
			'items',COALESCE((SELECT jsonb_agg(to_jsonb(page)-'sort_id' ORDER BY occurred_at DESC NULLS LAST,sort_id DESC) FROM page),'[]'))`,
		reconciliationCurrency, f.environment(), f.Provider, f.ReviewRequired, f.OrderID, f.PayoutID,
		reconciliationPageSize, f.offset(), f.MovementID, f.AccountID, f.MovementKey, f.Kind, f.From, before).Scan(&data)
	return reconciliationResult(data, err)
}

func (r *dbBillingRepository) ListSettlementPayouts(ctx context.Context, f payoutFilter) (payoutPage, error) {
	var data payoutPage
	err := db.DB.QueryRow(ctx, `
		WITH filtered AS MATERIALIZED (
			SELECT p.*,a.provider,a.environment FROM payment_settlement_reconciliation p
			JOIN payment_channel_accounts a ON a.id=p.account_id
			WHERE p.currency=$1 AND a.environment=$2 AND ($3='' OR a.provider=$3)
				AND ($7::bigint=0 OR p.id=$7) AND ($8::bigint=0 OR a.id=$8)
				AND ($9='' OR p.external_ref=$9)
				AND (NOT $4 OR p.status NOT IN ('matched','awaiting_channel','awaiting_bank'))
		), page AS (
			SELECT id::text AS id,account_id::text AS account_id,provider,environment,external_ref,channel_status,
				expected_minor::text AS expected_minor, expected_at, bank_reference, allocation_required,
				allocation_complete,row_count,net_total_minor::text AS net_total_minor,transfer_minor::text AS transfer_minor,
				channel_check,bank_amount_minor::text AS bank_amount_minor,booked_at,bank_transaction_id,
				difference_minor::text AS difference_minor,status,id AS sort_id
			FROM filtered ORDER BY expected_at DESC NULLS LAST,id DESC LIMIT $5 OFFSET $6
		)
		SELECT jsonb_build_object('currency',$1::text,'total',(SELECT count(*) FROM filtered),
			'items',COALESCE((SELECT jsonb_agg(to_jsonb(page)-'sort_id' ORDER BY expected_at DESC NULLS LAST,sort_id DESC) FROM page),'[]'))`,
		reconciliationCurrency, f.environment(), f.Provider, f.DifferencesOnly, reconciliationPageSize, f.offset(), f.PayoutID, f.AccountID, f.PayoutRef).Scan(&data)
	return reconciliationResult(data, err)
}

func (r *dbBillingRepository) ListSettlementReportIssues(ctx context.Context, f reconciliationPage) (reportIssuePage, error) {
	var data reportIssuePage
	err := db.DB.QueryRow(ctx, `
		WITH filtered AS MATERIALIZED (
			SELECT i.id::text AS import_id,i.id AS sort_id,i.external_ref,i.imported_at,i.source,
				a.provider,a.environment,a.id::text AS account_id,
				r.row_number,r.source_file,r.review_reason,r.movement_id::text AS movement_id
			FROM payment_settlement_rows r JOIN payment_settlement_imports i ON i.id=r.import_id
			JOIN payment_channel_accounts a ON a.id=i.account_id
			WHERE a.environment=$1 AND ($2='' OR a.provider=$2) AND r.review_reason IS NOT NULL
				AND ($5::bigint=0 OR a.id=$5)
		), page AS (
			SELECT * FROM filtered ORDER BY sort_id DESC,source_file,row_number LIMIT $3 OFFSET $4
		)
		SELECT jsonb_build_object('total',(SELECT count(*) FROM filtered),
			'items',COALESCE((SELECT jsonb_agg(to_jsonb(page)-'sort_id' ORDER BY sort_id DESC,source_file,row_number) FROM page),'[]'))`,
		f.environment(), f.Provider, reconciliationPageSize, f.offset(), f.AccountID).Scan(&data)
	return reconciliationResult(data, err)
}

func (r *dbBillingRepository) GetPaymentActivity(ctx context.Context, s reconciliationScope) (paymentActivity, error) {
	var data paymentActivity
	err := db.DB.QueryRow(ctx, `
		WITH movements AS MATERIALIZED (
			SELECT m.id,m.kind,a.provider FROM payment_ledger_movements m
			JOIN payment_channel_accounts a ON a.id=m.account_id
			WHERE m.currency=$1 AND a.environment=$2 AND ($3='' OR a.provider=$3) AND ($4::bigint=0 OR a.id=$4)
				AND m.posted_at IS NOT NULL AND NOT m.review_required AND m.kind IN ('refund','dispute_debit')
		), refunds AS (
			SELECT m.provider,r.object_id FROM movements m JOIN payment_ledger_references r ON r.movement_id=m.id
			WHERE m.kind='refund' AND ((m.provider='paypal' AND r.object_type='paypal_refund') OR
				(m.provider='stripe' AND r.object_type='stripe_refund'))
				AND NOT EXISTS (SELECT 1 FROM payment_ledger_references x WHERE x.movement_id=m.id
					AND x.object_type=r.object_type AND x.object_id<>r.object_id)
			UNION
			SELECT 'paypal',f.refund_id FROM paypal_refund_items f
			WHERE f.currency=$1 AND $3 IN ('','paypal') AND EXISTS (
				SELECT 1 FROM third_party_payment_orders t JOIN payment_channel_accounts a ON a.id=t.account_id
				WHERE t.capture_id=f.capture_id AND t.currency=f.currency
					AND a.provider='paypal' AND a.environment=$2 AND t.status='VERIFIED' AND ($4::bigint=0 OR a.id=$4)
			)
			UNION
			SELECT 'stripe',refund.refund_ref FROM payment_stripe_refund_items refund
			JOIN payment_channel_accounts a ON a.id=refund.account_id
			WHERE a.environment=$2 AND a.provider='stripe' AND $3 IN ('','stripe') AND ($4::bigint=0 OR a.id=$4)
		)
		SELECT jsonb_build_object('refund_count',(SELECT count(*) FROM refunds),
			'chargeback_debit_count',(SELECT count(*) FROM movements WHERE kind='dispute_debit'))`,
		reconciliationCurrency, s.environment(), s.Provider, s.AccountID).Scan(&data)
	return reconciliationResult(data, err)
}

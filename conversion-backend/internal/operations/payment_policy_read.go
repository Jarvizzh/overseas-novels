package operations

// paymentPolicyDetails projects only decision evidence. The immutable version
// keeps the matched branch and interval available after configuration changes.
const paymentPolicyDetails = `
	LEFT JOIN LATERAL (
		SELECT jsonb_strip_nulls(jsonb_build_object(
			'id',p.id::text,'state',e.payment_policy_state,'outcome',p.outcome,'reason',COALESCE(p.reason,e.reason),
			'app_id',COALESCE(p.app_scope,e.payment_app_scope),'promotion_id',COALESCE(NULLIF(p.promotion_key,''),e.promotion_id),
			'scope_kind',COALESCE(p.scope_kind,CASE WHEN e.promotion_id IS NULL THEN 'default' ELSE 'promotion' END),
			'version_id',p.policy_version_id::text,'version_number',v.version_no::text,
			'config_source',CASE WHEN p.id IS NULL THEN NULL WHEN v.id IS NULL THEN 'implicit_all'
				WHEN s.scope_kind='default' AND p.scope_kind='promotion' THEN 'inherited' ELSE 'own' END,
			'branch_id',p.branch_id::text,'mode',p.mode,'cycle_sequence',p.cycle_sequence::text,
			'amount_minor',p.amount_minor::text,'currency',p.currency,'payment_kind',p.payment_kind,
			'match',b.details,'decided_at',p.decided_at)) AS item
		FROM (VALUES(1)) anchor(n)
		LEFT JOIN conversion.payment_policy_decisions p ON p.id=e.payment_policy_decision_id
		LEFT JOIN conversion.payment_policy_versions v ON v.id=p.policy_version_id
		LEFT JOIN conversion.payment_policy_scopes s ON s.id=v.scope_id
		LEFT JOIN LATERAL (
			SELECT jsonb_build_object('kind',g.value->>'payment_kind','is_fallback',branch.fallback,
				'currency',branch.value->>'currency','min_minor',branch.value->>'min_minor',
				'max_minor',branch.value->>'max_minor','rate_bps',branch.value->'rate_bps',
				'send_count',branch.value->'send_count','skip_count',branch.value->'skip_count') AS details
			FROM jsonb_array_elements(v.config->'groups') g
			CROSS JOIN LATERAL (
				SELECT band.value,false AS fallback FROM jsonb_array_elements(g.value->'bands') band
				UNION ALL SELECT g.value->'fallback',true
			) branch
			WHERE branch.value->>'branch_id'=p.branch_id::text
			LIMIT 1
		) b ON p.branch_id IS NOT NULL
		WHERE e.event_name IN ('Purchase','Subscribe') AND e.payment_policy_state<>'not_applicable'
	) policy ON true
`

func orderPaymentPolicySQL(events string) string {
	return `(SELECT policy.item FROM (SELECT * FROM ` + events + `
		WHERE event_name IN ('Purchase','Subscribe')
		ORDER BY (payment_policy_state='decided') DESC,(payment_policy_state='pending') DESC,id LIMIT 1) e
		` + paymentPolicyDetails + `)`
}

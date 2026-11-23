package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

type SummaryFilter struct {
	Hours               int
	Source, EventName   string
	Integration, Target int64
}

func (r Repository) handleSummary(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	f := SummaryFilter{Hours: 24, Source: q.Get("source"), EventName: q.Get("event_name")}
	var err error
	if v := q.Get("hours"); v != "" {
		f.Hours, err = strconv.Atoi(v)
	}
	if err != nil || f.Hours < 1 || f.Hours > 168 || len(f.Source) > 100 || len(f.EventName) > 100 {
		writeError(w, http.StatusBadRequest, "invalid_summary_filter")
		return
	}
	for key, destination := range map[string]*int64{"integration_id": &f.Integration, "destination_id": &f.Target} {
		if v := q.Get(key); v != "" {
			if n, ok := positiveID(v); ok {
				*destination = n
			} else {
				writeError(w, http.StatusBadRequest, "invalid_summary_filter")
				return
			}
		}
	}
	var response json.RawMessage
	err = r.Pool.QueryRow(req.Context(), summarySQL, r.Environment, time.Duration(f.Hours)*time.Hour,
		f.Source, f.EventName, f.Integration, f.Target).Scan(&response)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "summary_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

type AlertChange struct {
	Kind, Subject, State string
}

// Collect is suitable for a separate process with its own connection pool.
func (r Repository) Collect(ctx context.Context) ([]AlertChange, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1930718,hashtext($1))", r.Environment); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, alertSQL, r.Environment)
	if err != nil {
		return nil, err
	}
	changes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (AlertChange, error) {
		var a AlertChange
		err := row.Scan(&a.Kind, &a.Subject, &a.State)
		return a, err
	})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO conversion.collector_samples(environment,sampled_at) VALUES ($1,clock_timestamp())
        ON CONFLICT (environment) DO UPDATE SET sampled_at=EXCLUDED.sampled_at`, r.Environment); err != nil {
		return nil, err
	}
	return changes, tx.Commit(ctx)
}

var summarySQL = `
	WITH period AS (
		SELECT now()-$2::interval AS from_at, now() AS to_at
	), accounts AS (
		SELECT id,source FROM conversion.integrations
		WHERE environment=$1 AND ($3='' OR source=$3) AND ($5::bigint=0 OR id=$5)
	), payments AS (
		SELECT o.id,o.subscription_id,a.id AS integration_id,a.source,
			o.capi_expectation->>'reason' AS reason,o.capi_expectation->>'pixel_id' AS pixel,
			o.capi_expectation->'events' AS expected,o.capi_expectation IS NULL AS legacy_order,
			o.capi_expectation->>'payment_key' AS payment_key,
            conversion.reader_order_environment(o.id,o.capi_expectation->>'environment',o.checkout_environment) AS environment,
			NULLIF(o.capi_expectation->>'occurred_at','')::timestamptz AS occurred_at,
			COALESCE(o.fulfilled_at,CASE WHEN o.capi_expectation->>'reason'='refunded_before_fulfillment' THEN o.paid_at END) AS fulfilled_at
		FROM recharge_orders o CROSS JOIN period w
		LEFT JOIN accounts a ON a.source='starnovel'
		  AND a.id=(SELECT id FROM conversion.integrations WHERE environment=$1 AND source='starnovel' AND account_key='reader')
		WHERE (o.fulfilled_at BETWEEN w.from_at AND w.to_at OR (
			o.fulfilled_at IS NULL AND o.capi_expectation->>'reason'='refunded_before_fulfillment' AND o.paid_at BETWEEN w.from_at AND w.to_at))

		  AND ($3='' OR $3='starnovel') AND ($5=0 OR a.id IS NOT NULL)
	), historical_facts AS (
		SELECT p.id AS order_id,e.id AS event_id,e.event_name,e.occurred_at,
			(d.state='succeeded' OR (l.legacy_state='delivered' AND l.legacy_delivered_at IS NOT NULL)) AS confirmed,
			d.id IS NULL AS missing_target
		FROM payments p JOIN conversion.events e ON e.integration_id=p.integration_id AND e.source_order_id=p.id::text
		JOIN conversion.legacy_outbox_imports l ON l.event_id=e.id AND l.legacy_event_id=e.source_event_id
		LEFT JOIN conversion.deliveries d ON d.event_id=e.id AND d.role='original'
		WHERE p.legacy_order AND p.environment IN ($1,'unknown')
		  AND ((e.event_name='Purchase' AND e.source_event_id='payment:'||p.id||':Purchase')
		    OR (e.event_name='Subscribe' AND e.source_event_id='subscription:'||p.subscription_id||':Subscribe'
		        AND EXISTS(SELECT 1 FROM user_subscriptions s WHERE s.subscription_id=p.subscription_id
		                   AND s.capi_subscribe_order_id=p.id)))
		  AND ((l.legacy_state='delivered' AND l.legacy_delivered_at IS NOT NULL)
		    OR (l.environment_evidence IS NOT NULL AND (l.legacy_pixel_id IS NOT NULL OR e.attribution_state='missing_pixel')))
	), payment_facts AS (
		SELECT COALESCE(p.source,'starnovel') AS source,p.integration_id,
			COALESCE(spec->>'name','Purchase') AS event_name,
			CASE WHEN p.environment IN ('unknown','conflict') THEN 'environment_'||p.environment
                 WHEN e.reason='event_not_allowed' THEN 'configuration_skipped'
                 WHEN decision.outcome='skipped' THEN 'policy_skipped' WHEN spec IS NOT NULL THEN 'expected'
			     WHEN p.reason='organic' THEN 'organic' WHEN p.reason IS NULL THEN 'historical_unknown' ELSE 'not_required' END AS eligibility,
			p.environment=$1 AND p.occurred_at IS NULL AND spec IS NOT NULL
                AND e.reason IS DISTINCT FROM 'event_not_allowed' AND decision.outcome IS DISTINCT FROM 'skipped' AS missing_time,
			e.id AS event_id, q.business_key IS NOT NULL AS queued,
			original.state='succeeded' AS confirmed, original.id IS NULL AS missing_target
		FROM payments p CROSS JOIN period w
		LEFT JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(p.expected)='array' THEN p.expected ELSE '[]' END) spec ON true
		LEFT JOIN conversion.events e ON e.integration_id=p.integration_id AND e.source_event_type=spec->>'name'
			AND e.source_event_id=spec->>'business_key'
		LEFT JOIN conversion.payment_policy_decisions decision ON decision.environment=$1
			AND decision.integration_id=p.integration_id AND decision.app_scope='reader'
			AND decision.payment_key=p.payment_key AND p.payment_key=p.id::text
		LEFT JOIN reader_conversion_outbox q ON q.business_key=spec->>'business_key' AND q.environment=$1
		LEFT JOIN conversion.deliveries original ON original.event_id=e.id AND original.role='original'
		WHERE p.environment IN ($1,'unknown','conflict') AND COALESCE(p.occurred_at,p.fulfilled_at)>=w.from_at AND COALESCE(p.occurred_at,p.fulfilled_at)<=w.to_at
		  AND ($4='' OR COALESCE(spec->>'name','Purchase')=$4)
		  AND (spec->>'name' IS DISTINCT FROM 'Subscribe' OR EXISTS (SELECT 1 FROM user_subscriptions s
		      WHERE s.subscription_id=p.subscription_id AND s.capi_subscribe_order_id=p.id))
		  AND NOT EXISTS (SELECT 1 FROM historical_facts h WHERE h.order_id=p.id AND h.event_name='Purchase')
		UNION ALL
		SELECT 'starnovel',p.integration_id,h.event_name,'expected',h.occurred_at IS NULL,
		       h.event_id,true,h.confirmed,h.missing_target
		FROM historical_facts h JOIN payments p ON p.id=h.order_id CROSS JOIN period w
		WHERE COALESCE(h.occurred_at,p.fulfilled_at) BETWEEN w.from_at AND w.to_at
		  AND ($4='' OR h.event_name=$4)
	), other_facts AS (
		SELECT a.source,a.id AS integration_id,e.event_name,
			CASE WHEN e.reason='event_not_allowed' THEN 'configuration_skipped'
                 WHEN decision.outcome='skipped' THEN 'policy_skipped'
			     WHEN e.attribution_state='organic' THEN 'organic' WHEN e.attribution_state='unknown' THEN 'historical_unknown'
			     ELSE 'expected' END AS eligibility,
			false AS missing_time,e.id AS event_id,true AS queued,
			d.state='succeeded' AS confirmed,d.id IS NULL AS missing_target
		FROM accounts a JOIN conversion.events e ON e.integration_id=a.id CROSS JOIN period w
		LEFT JOIN conversion.payment_policy_decisions decision ON decision.id=e.payment_policy_decision_id
		LEFT JOIN conversion.deliveries d ON d.event_id=e.id AND d.role='original'
		WHERE e.occurred_at>=w.from_at AND e.occurred_at<=w.to_at AND ($4='' OR e.event_name=$4)
		  AND (a.source<>'starnovel' OR e.event_name NOT IN ('Purchase','Subscribe'))
	), population AS (
		SELECT source,integration_id,event_name,eligibility,count(*) AS events,
			count(*) FILTER (WHERE eligibility='expected' AND confirmed) AS confirmed,
			count(*) FILTER (WHERE eligibility='expected' AND NOT queued AND event_id IS NULL) AS missing_outbox,
			count(*) FILTER (WHERE eligibility='expected' AND event_id IS NOT NULL AND missing_target) AS missing_target,
			count(*) FILTER (WHERE missing_time) AS missing_time
		FROM (SELECT * FROM payment_facts UNION ALL SELECT * FROM other_facts) facts
		WHERE $6::bigint=0
		GROUP BY source,integration_id,event_name,eligibility
	), target_metrics AS (
		SELECT a.source,a.id::text AS integration_id,e.event_name,d.destination_id::text,x.pixel_id,d.role,
			CASE WHEN d.state='pending' AND d.lease_until>now() THEN 'leased'
			     WHEN d.state='pending' AND d.next_attempt_at>now() THEN 'backoff' ELSE d.state END AS state,
			d.reason, count(*) AS deliveries,
			min(d.next_attempt_at) FILTER (WHERE d.state='pending' AND d.lease_token IS NULL AND d.next_attempt_at<=now()) AS oldest_ready_at,
			min(d.created_at) FILTER (WHERE d.state='pending' AND d.lease_token IS NULL AND d.next_attempt_at<=now()) AS oldest_ready_queued_at,
			avg(extract(epoch FROM first.started_at-d.created_at)) AS queue_wait_seconds,
			avg(extract(epoch FROM d.finalized_at-e.occurred_at)) FILTER (WHERE d.state='succeeded') AS business_confirmation_seconds,
			avg(extract(epoch FROM d.finalized_at-d.created_at)) FILTER (WHERE d.state='succeeded') AS queue_confirmation_seconds
		FROM conversion.deliveries d JOIN conversion.destinations x ON x.id=d.destination_id
		JOIN conversion.events e ON e.id=d.event_id JOIN accounts a ON a.id=e.integration_id CROSS JOIN period w
		LEFT JOIN conversion.delivery_attempts first ON first.delivery_id=d.id AND first.attempt_no=1
		WHERE d.environment=$1 AND (d.finalized_at IS NULL OR (e.occurred_at>=w.from_at AND e.occurred_at<=w.to_at))
		  AND ($4='' OR e.event_name=$4) AND ($6=0 OR d.destination_id=$6)
		GROUP BY a.source,a.id,e.event_name,d.destination_id,x.pixel_id,d.role,7,d.reason
    ), request_metrics AS (
        SELECT x.pixel_id,count(*) AS requests,sum(matched.members) AS sent,
            avg(b.sent_count)::float8 AS mean_batch_size,
            count(*) FILTER (WHERE b.receipt->>'HTTPStatus'='429') AS http_429,
            count(*) FILTER (WHERE b.receipt IS NOT NULL AND b.receipt->>'HTTPStatus' IS NOT NULL) AS http_responses,
            count(*) FILTER (WHERE unresolved.unknown) AS unknown_requests,
            COALESCE(sum(matched.members) FILTER (WHERE unresolved.unknown),0) AS unknown_deliveries,
            COALESCE(sum(matched.members) FILTER (WHERE b.confirmed),0) AS confirmed_members
        FROM conversion.meta_batches b JOIN conversion.destinations x ON x.id=b.destination_id CROSS JOIN period w
        CROSS JOIN LATERAL (
            SELECT b.receipt->>'Outcome'='outcome_unknown' OR (b.receipt IS NULL AND b.finished_at IS NOT NULL) AS unknown
        ) unresolved
        ` + matchingBatchMembers("$3<>'' OR $4<>'' OR $5<>0", "$4='' OR e.event_name=$4") + `
        WHERE b.environment=$1 AND b.started_at BETWEEN w.from_at AND w.to_at AND ($6=0 OR b.destination_id=$6)
          AND matched.members>0
        GROUP BY x.pixel_id
	), report_metrics AS (
		SELECT a.source,a.id::text AS integration_id,e.event_name,d.destination_id::text,p.state,p.reason,count(*) AS reports,
		       min(p.created_at) FILTER (WHERE p.state<>'confirmed') AS oldest_pending_at
		FROM conversion.partner_reports p JOIN accounts a ON a.id=p.integration_id
		JOIN conversion.events e ON e.id=p.event_id JOIN conversion.deliveries d ON d.id=p.original_delivery_id CROSS JOIN period w
		WHERE (p.state<>'confirmed' OR (e.occurred_at>=w.from_at AND e.occurred_at<=w.to_at))
		  AND ($4='' OR e.event_name=$4) AND ($6=0 OR d.destination_id=$6)
		GROUP BY a.source,a.id,e.event_name,d.destination_id,p.state,p.reason
	), policy_cohort AS MATERIALIZED (
		SELECT p.id,p.outcome,p.integration_id,p.payment_key,p.app_scope,p.original_destination_id,a.source
		FROM conversion.payment_policy_decisions p
		JOIN accounts a ON a.id=p.integration_id CROSS JOIN period w
		WHERE p.environment=$1 AND p.decided_at BETWEEN w.from_at AND w.to_at
		  AND ($6=0 OR p.original_destination_id=$6)
	), policy_events AS (
		-- Reader expectations come from orders in the decision cohort, independent of the business-time population.
		SELECT p.id AS decision_id,p.outcome,p.original_destination_id,
		       spec->>'name' AS event_name,e.id AS event_id
		FROM policy_cohort p
		JOIN recharge_orders o ON o.id=CASE
			WHEN p.payment_key ~ '^[1-9][0-9]{0,18}$'
			     AND (length(p.payment_key)<19 OR p.payment_key COLLATE "C"<='9223372036854775807')
			THEN p.payment_key::bigint END
		CROSS JOIN LATERAL jsonb_array_elements(CASE
			WHEN jsonb_typeof(o.capi_expectation->'events')='array' THEN o.capi_expectation->'events' ELSE '[]' END) spec
		LEFT JOIN conversion.events e ON e.integration_id=p.integration_id AND e.source_event_type=spec->>'name'
            AND e.source_event_id=spec->>'business_key'
		WHERE p.source='starnovel' AND p.app_scope='reader'
		  AND conversion.reader_order_environment(o.id,o.capi_expectation->>'environment',o.checkout_environment)=$1
          AND o.capi_expectation->>'payment_key'=p.payment_key
          AND e.reason IS DISTINCT FROM 'event_not_allowed'
		  AND ($4='' OR spec->>'name'=$4)
		  AND (spec->>'name'='Purchase' OR (spec->>'name'='Subscribe' AND EXISTS (
		      SELECT 1 FROM user_subscriptions s WHERE s.subscription_id=o.subscription_id
		          AND s.capi_subscribe_order_id=o.id AND s.capi_subscribe_state='claimed')))
		UNION ALL
		SELECT p.id,p.outcome,p.original_destination_id,e.event_name,e.id
		FROM policy_cohort p JOIN conversion.events e ON e.payment_policy_decision_id=p.id
		WHERE (p.source<>'starnovel' OR p.app_scope<>'reader') AND ($4='' OR e.event_name=$4)
	), payment_coverage AS (
		SELECT count(*) AS evaluated,count(*) FILTER(WHERE p.outcome='selected') AS selected
		FROM policy_cohort p
		WHERE $4='' OR EXISTS(SELECT 1 FROM policy_events e WHERE e.decision_id=p.id)
	), selected_confirmation AS (
		SELECT e.event_name,count(*) AS expected,count(*) FILTER(WHERE d.state='succeeded') AS confirmed
		FROM policy_events e
		LEFT JOIN conversion.deliveries d ON d.event_id=e.event_id AND d.role='original'
		    AND d.destination_id=e.original_destination_id
		WHERE e.outcome='selected'
		GROUP BY e.event_name
	)
	SELECT jsonb_build_object('environment',$1,'from_at',w.from_at,'to_at',w.to_at,
		'payment_coverage',(SELECT to_jsonb(c) FROM payment_coverage c),
        'collector',jsonb_build_object(
            'sampled_at',sample.sampled_at,
            'state',CASE WHEN sample.sampled_at IS NULL THEN 'waiting'
                         WHEN sample.sampled_at<=now()-interval '90 seconds' THEN 'stale' ELSE 'fresh' END),
		'selected_confirmation',(SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY c.event_name),'[]') FROM selected_confirmation c),
		'population',(SELECT COALESCE(jsonb_agg(jsonb_set(to_jsonb(p),'{integration_id}',COALESCE(to_jsonb(p.integration_id::text),'null'))),'[]') FROM population p),
		'targets',(SELECT COALESCE(jsonb_agg(to_jsonb(t)),'[]') FROM target_metrics t),
        'request_counts_additive_across_filters',false,
        'requests',(SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY pixel_id),'[]') FROM request_metrics t),
		'reports',(SELECT COALESCE(jsonb_agg(to_jsonb(r)),'[]') FROM report_metrics r),
		'alerts',(SELECT COALESCE(jsonb_agg(jsonb_build_object('kind',kind,'subject',subject,'detail',detail,'opened_at',opened_at)
			ORDER BY opened_at DESC),'[]') FROM (SELECT * FROM conversion.alerts WHERE environment=$1 AND resolved_at IS NULL
				ORDER BY opened_at DESC LIMIT 100) active),
		'heartbeats',(SELECT COALESCE(jsonb_agg(jsonb_build_object('worker_type',worker_type,'instance_id',instance_id,
		    'heartbeat_at',heartbeat_at,'last_progress_at',last_progress_at,'paused',paused)),'[]')
		    FROM (SELECT * FROM conversion.worker_heartbeats WHERE environment=$1 AND heartbeat_at>now()-interval '24 hours'
		          ORDER BY heartbeat_at DESC LIMIT 100) live))
	FROM period w LEFT JOIN conversion.collector_samples sample ON sample.environment=$1
`

const alertSQL = `
	WITH ingress AS (
		SELECT 'inbox_delayed' AS kind,i.id::text AS subject,jsonb_build_object('count',count(*),'oldest_at',min(b.received_at)) AS detail
		FROM conversion.inbox b JOIN conversion.integrations i ON i.id=b.integration_id
		WHERE i.environment=$1 AND b.state IN ('pending','quarantined') AND b.received_at<now()-interval '5 minutes'
		GROUP BY i.id
		UNION ALL
		SELECT 'reader_normalization_delayed','reader',jsonb_build_object('count',count(*),'oldest_at',min(created_at))
		FROM reader_conversion_outbox WHERE environment=$1 AND (consumed_at IS NULL OR processing_error IS NOT NULL)
		  AND created_at<now()-interval '5 minutes' HAVING count(*)>0
	), missed AS (
		SELECT 'purchase_missing_outbox' AS kind,'reader' AS subject,
		       jsonb_build_object('count',count(*),'oldest_at',min(o.fulfilled_at)) AS detail
		FROM recharge_orders o
		CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(o.capi_expectation->'events')='array' THEN
			o.capi_expectation->'events' ELSE '[]' END) spec
		WHERE o.fulfilled_at BETWEEN now()-interval '7 days' AND now()-interval '1 minute'
		  AND o.capi_expectation->>'environment'=$1 AND spec->>'name'='Purchase'
		  AND NOT EXISTS(SELECT 1 FROM reader_conversion_outbox q WHERE q.business_key=spec->>'business_key' AND q.environment=$1)
		  AND NOT EXISTS(SELECT 1 FROM conversion.events e JOIN conversion.integrations i ON i.id=e.integration_id
		     WHERE i.environment=$1 AND i.source='starnovel' AND i.account_key='reader'
		       AND e.source_event_type='Purchase' AND e.source_event_id=spec->>'business_key')
		HAVING count(*)>0
	), missing_original AS (
		SELECT 'missing_original_target' AS kind,e.integration_id::text AS subject,
		       jsonb_build_object('count',count(*),'oldest_at',min(e.received_at),
		           'sample_event_id',min(e.id)::text) AS detail
		FROM conversion.events e
		WHERE e.environment=$1 AND e.reason IS DISTINCT FROM 'event_not_allowed'
          AND (e.attribution_state='missing_pixel' OR e.reason='original_target_unavailable')
		  AND e.received_at>=now()-interval '7 days'
		  AND NOT EXISTS(SELECT 1 FROM conversion.deliveries d WHERE d.event_id=e.id AND d.role='original')
		  AND NOT EXISTS(SELECT 1 FROM conversion.legacy_outbox_imports m WHERE m.event_id=e.id
		      AND m.legacy_state='delivered' AND m.legacy_delivered_at IS NOT NULL)
		GROUP BY e.integration_id
	), missing_rule_facts AS (
		SELECT 'rule_fact_missing' AS kind,e.integration_id::text AS subject,
		       jsonb_build_object('count',count(*),'oldest_at',min(e.received_at),'sample_event_id',min(e.id)::text) AS detail
		FROM conversion.events e WHERE e.environment=$1 AND e.reason='rule_fact_missing'
		  AND e.received_at>=now()-interval '7 days' GROUP BY e.integration_id
	), meta AS (
		SELECT 'meta_queue_delayed' AS kind,d.destination_id::text AS subject,
		       jsonb_build_object('count',count(*),'oldest_at',min(d.next_attempt_at),'pixel_id',x.pixel_id) AS detail
		FROM conversion.deliveries d JOIN conversion.destinations x ON x.id=d.destination_id
		WHERE d.environment=$1 AND d.state='pending' AND d.lease_token IS NULL AND d.deadline_at>now()
		  AND d.next_attempt_at<now()-interval '5 minutes' GROUP BY d.destination_id,x.pixel_id
		UNION ALL
		SELECT CASE WHEN d.state='blocked' THEN 'meta_credentials' ELSE 'meta_outcome_unknown' END,
		       d.destination_id::text,jsonb_build_object('count',count(*),'pixel_id',x.pixel_id)
		FROM conversion.deliveries d JOIN conversion.destinations x ON x.id=d.destination_id
		WHERE d.environment=$1 AND (d.state='outcome_unknown' OR (d.state='blocked' AND
		   d.reason IN ('destination_or_credential_unavailable','credential_decryption_failed','meta_credentials_or_permissions','destination_disabled','promotion_pixel_mismatch','partner_route_not_authorized','reader_route_not_authorized')))
		GROUP BY 1,d.destination_id,x.pixel_id
        UNION ALL
        SELECT CASE WHEN d.deadline_at<=d.finalized_at OR d.reason='deadline_exhausted' THEN 'meta_delivery_expired'
                    WHEN d.reason='meta_invalid_parameter' THEN 'meta_parameter_rejected'
                    WHEN d.unknown_attempt_no IS NOT NULL THEN 'meta_outcome_unknown_recent'
                    WHEN d.reason LIKE 'meta_%' THEN 'meta_delivery_failed'
                    ELSE 'meta_local_validation_failed' END,
               d.destination_id::text,jsonb_build_object('count',count(*),'pixel_id',x.pixel_id,
                   'sample_event_id',min(d.event_id)::text)
        FROM conversion.deliveries d JOIN conversion.destinations x ON x.id=d.destination_id
        WHERE d.environment=$1 AND d.state='failed' AND d.finalized_at>now()-interval '15 minutes'
        GROUP BY 1,d.destination_id,x.pixel_id
        UNION ALL
        SELECT 'meta_rate_limited',limits.destination_id::text,jsonb_build_object(
            'count',count(*),'pixel_id',x.pixel_id,'sample_event_id',min(limits.event_id)::text)
        FROM (
            SELECT d.destination_id,d.event_id
            FROM conversion.delivery_attempts a JOIN conversion.deliveries d ON d.id=a.delivery_id
            WHERE d.environment=$1 AND a.batch_id IS NULL AND a.reason='meta_rate_limited'
              AND a.responded_at>now()-interval '15 minutes'
            UNION ALL
            SELECT b.destination_id,NULL::bigint FROM conversion.meta_batches b
            WHERE b.environment=$1 AND b.started_at>now()-interval '15 minutes'
              AND (COALESCE(b.receipt,b.late_receipt)->>'RateLimited')::boolean
        ) limits JOIN conversion.destinations x ON x.id=limits.destination_id
        GROUP BY limits.destination_id,x.pixel_id
        UNION ALL
        SELECT 'meta_credential_paused',x.id::text,jsonb_build_object('pixel_id',x.pixel_id)
        FROM conversion.destinations x
        WHERE x.environment=$1 AND x.blocked_credential_id=x.credential_id
        UNION ALL
        SELECT 'meta_batch_no_confirmation',b.destination_id::text,jsonb_build_object(
            'count',count(*),'affected_deliveries',sum(b.sent_count),'pixel_id',x.pixel_id)
        FROM conversion.meta_batches b JOIN conversion.destinations x ON x.id=b.destination_id
        WHERE b.environment=$1 AND b.started_at>now()-interval '15 minutes'
          AND (b.receipt->>'Outcome'='outcome_unknown' OR (b.receipt IS NULL AND b.finished_at IS NOT NULL))
        GROUP BY b.destination_id,x.pixel_id
	), receipts AS (
		SELECT 'partner_report_delayed' AS kind,i.id::text AS subject,jsonb_build_object('count',count(*),'oldest_at',min(p.created_at)) AS detail
		FROM conversion.partner_reports p JOIN conversion.integrations i ON i.id=p.integration_id
		WHERE i.environment=$1 AND p.state<>'confirmed' AND
		  (p.state IN ('blocked','outcome_unknown') OR p.created_at<now()-make_interval(secs=>CASE
		     WHEN i.report_config->>'alert_after_seconds' ~ '^[1-9][0-9]{0,5}$' THEN (i.report_config->>'alert_after_seconds')::int ELSE 300 END))
		GROUP BY i.id
	), waiting AS (
		SELECT 'normalize' AS worker_type, min(o.created_at) AS oldest
		FROM reader_conversion_outbox o WHERE environment=$1 AND consumed_at IS NULL
		UNION ALL
		SELECT 'normalize_inbox',min(b.received_at)
		FROM conversion.inbox b JOIN conversion.integrations i ON i.id=b.integration_id
		WHERE i.environment=$1 AND i.source='sinan' AND b.state='pending'
		UNION ALL
		SELECT 'meta',min(d.next_attempt_at) FROM conversion.deliveries d
		WHERE environment=$1 AND d.state='pending' AND d.next_attempt_at<=now() AND d.deadline_at>now()
		UNION ALL
		SELECT 'partner_report',min(p.next_attempt_at) FROM conversion.partner_reports p JOIN conversion.integrations i ON i.id=p.integration_id
		WHERE i.environment=$1 AND p.state='pending' AND p.next_attempt_at<=now()
	), heartbeat AS (
		SELECT w.worker_type, max(h.heartbeat_at) AS heartbeat,
		       max(h.last_progress_at) FILTER (WHERE NOT h.paused AND h.heartbeat_at>now()-interval '1 minute') AS progress,
		       COALESCE(bool_and(h.paused) FILTER (WHERE h.heartbeat_at>now()-interval '1 minute'),false) AS paused,min(w.oldest) AS oldest
		FROM waiting w LEFT JOIN conversion.worker_heartbeats h ON h.environment=$1 AND h.worker_type=w.worker_type
		WHERE w.oldest IS NOT NULL GROUP BY w.worker_type
	), stalled AS (
		SELECT CASE WHEN heartbeat IS NULL OR heartbeat<=now()-interval '1 minute' THEN 'worker_heartbeat_missing'
		            ELSE 'worker_stalled' END AS kind,worker_type AS subject,
		       jsonb_build_object('oldest_at',oldest,'heartbeat_at',heartbeat,'last_progress_at',progress) AS detail
		FROM heartbeat WHERE NOT paused AND
		    (heartbeat IS NULL OR heartbeat<=now()-interval '1 minute'
		     OR (oldest<now()-interval '5 minutes' AND (progress IS NULL OR progress<now()-interval '5 minutes')))
	), active AS (
		SELECT * FROM ingress UNION ALL SELECT * FROM missed UNION ALL SELECT * FROM missing_original UNION ALL SELECT * FROM meta
		UNION ALL SELECT * FROM receipts UNION ALL SELECT * FROM stalled UNION ALL SELECT * FROM missing_rule_facts
	), observed AS (
		INSERT INTO conversion.alerts(environment,kind,subject,detail)
		SELECT $1,kind,subject,detail FROM active
		ON CONFLICT (environment,kind,subject) DO UPDATE SET
		  detail=excluded.detail,last_seen_at=now(),resolved_at=NULL,
		  opened_at=CASE WHEN alerts.resolved_at IS NOT NULL THEN now() ELSE alerts.opened_at END
		RETURNING kind,subject,opened_at
	), resolved AS (
		UPDATE conversion.alerts a SET resolved_at=now()
		WHERE environment=$1 AND resolved_at IS NULL AND NOT EXISTS(
		    SELECT 1 FROM active s WHERE s.kind=a.kind AND s.subject=a.subject)
		RETURNING kind,subject
	)
	SELECT kind,subject,'opened' FROM observed WHERE opened_at=now()
	UNION ALL SELECT kind,subject,'resolved' FROM resolved
`

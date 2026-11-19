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
	return changes, tx.Commit(ctx)
}

const summarySQL = `
	WITH period AS (
		SELECT now()-$2::interval AS from_at, now() AS to_at
	), accounts AS (
		SELECT id,source FROM conversion.integrations
		WHERE environment=$1 AND ($3='' OR source=$3) AND ($5::bigint=0 OR id=$5)
	), payments AS (
		SELECT o.id,o.subscription_id,a.id AS integration_id,a.source,
			o.capi_expectation->>'reason' AS reason,o.capi_expectation->>'pixel_id' AS pixel,
			o.capi_expectation->'events' AS expected,o.capi_expectation IS NULL AS legacy_order,
			NULLIF(o.capi_expectation->>'occurred_at','')::timestamptz AS occurred_at,
			COALESCE(o.fulfilled_at,CASE WHEN o.capi_expectation->>'reason'='refunded_before_fulfillment' THEN o.paid_at END) AS fulfilled_at
		FROM recharge_orders o CROSS JOIN period w
		LEFT JOIN accounts a ON a.source='starnovel'
		  AND a.id=(SELECT id FROM conversion.integrations WHERE environment=$1 AND source='starnovel' AND account_key='reader')
		WHERE (o.fulfilled_at BETWEEN w.from_at AND w.to_at OR (
			o.fulfilled_at IS NULL AND o.capi_expectation->>'reason'='refunded_before_fulfillment' AND o.paid_at BETWEEN w.from_at AND w.to_at))
		  AND (o.capi_expectation->>'environment'=$1 OR o.capi_expectation IS NULL)
		  AND ($3='' OR $3='starnovel') AND ($5=0 OR a.id IS NOT NULL)
	), historical_facts AS (
		SELECT p.id AS order_id,e.id AS event_id,e.event_name,e.occurred_at,
			(d.state='succeeded' OR (l.legacy_state='delivered' AND l.legacy_delivered_at IS NOT NULL)) AS confirmed,
			d.id IS NULL AS missing_target
		FROM payments p JOIN conversion.events e ON e.integration_id=p.integration_id AND e.source_order_id=p.id::text
		JOIN conversion.legacy_outbox_imports l ON l.event_id=e.id AND l.legacy_event_id=e.source_event_id
		LEFT JOIN conversion.deliveries d ON d.event_id=e.id AND d.role='original'
		WHERE p.legacy_order
		  AND ((e.event_name='Purchase' AND e.source_event_id='payment:'||p.id||':Purchase')
		    OR (e.event_name='Subscribe' AND e.source_event_id='subscription:'||p.subscription_id||':Subscribe'
		        AND EXISTS(SELECT 1 FROM user_subscriptions s WHERE s.subscription_id=p.subscription_id
		                   AND s.capi_subscribe_order_id=p.id)))
		  AND ((l.legacy_state='delivered' AND l.legacy_delivered_at IS NOT NULL)
		    OR (l.environment_evidence IS NOT NULL AND (l.legacy_pixel_id IS NOT NULL OR e.attribution_state='missing_pixel')))
	), payment_facts AS (
		SELECT COALESCE(p.source,'starnovel') AS source,p.integration_id,
			COALESCE(spec->>'name','Purchase') AS event_name,
			CASE WHEN spec IS NOT NULL THEN 'expected'
			     WHEN p.reason='organic' THEN 'organic' WHEN p.reason IS NULL THEN 'historical_unknown' ELSE 'not_required' END AS eligibility,
			p.occurred_at IS NULL AND spec IS NOT NULL AS missing_time,
			e.id AS event_id, q.business_key IS NOT NULL AS queued,
			original.state='succeeded' AS confirmed, original.id IS NULL AS missing_target
		FROM payments p CROSS JOIN period w
		LEFT JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(p.expected)='array' THEN p.expected ELSE '[]' END) spec ON true
		LEFT JOIN conversion.events e ON e.integration_id=p.integration_id AND e.source_event_type=spec->>'name'
			AND e.source_event_id=spec->>'business_key'
		LEFT JOIN reader_conversion_outbox q ON q.business_key=spec->>'business_key' AND q.environment=$1
		LEFT JOIN conversion.deliveries original ON original.event_id=e.id AND original.role='original'
		WHERE COALESCE(p.occurred_at,p.fulfilled_at)>=w.from_at AND COALESCE(p.occurred_at,p.fulfilled_at)<=w.to_at
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
			CASE e.attribution_state WHEN 'organic' THEN 'organic' WHEN 'unknown' THEN 'historical_unknown'
			     ELSE 'expected' END AS eligibility,
			false AS missing_time,e.id AS event_id,true AS queued,
			d.state='succeeded' AS confirmed,d.id IS NULL AS missing_target
		FROM accounts a JOIN conversion.events e ON e.integration_id=a.id CROSS JOIN period w
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
	), report_metrics AS (
		SELECT a.source,a.id::text AS integration_id,e.event_name,d.destination_id::text,p.state,p.reason,count(*) AS reports,
		       min(p.created_at) FILTER (WHERE p.state<>'confirmed') AS oldest_pending_at
		FROM conversion.partner_reports p JOIN accounts a ON a.id=p.integration_id
		JOIN conversion.events e ON e.id=p.event_id JOIN conversion.deliveries d ON d.id=p.original_delivery_id CROSS JOIN period w
		WHERE (p.state<>'confirmed' OR (e.occurred_at>=w.from_at AND e.occurred_at<=w.to_at))
		  AND ($4='' OR e.event_name=$4) AND ($6=0 OR d.destination_id=$6)
		GROUP BY a.source,a.id,e.event_name,d.destination_id,p.state,p.reason
	)
	SELECT jsonb_build_object('environment',$1,'from_at',w.from_at,'to_at',w.to_at,
		'population',(SELECT COALESCE(jsonb_agg(jsonb_set(to_jsonb(p),'{integration_id}',COALESCE(to_jsonb(p.integration_id::text),'null'))),'[]') FROM population p),
		'targets',(SELECT COALESCE(jsonb_agg(to_jsonb(t)),'[]') FROM target_metrics t),
		'reports',(SELECT COALESCE(jsonb_agg(to_jsonb(r)),'[]') FROM report_metrics r),
		'alerts',(SELECT COALESCE(jsonb_agg(jsonb_build_object('kind',kind,'subject',subject,'detail',detail,'opened_at',opened_at)
			ORDER BY opened_at DESC),'[]') FROM (SELECT * FROM conversion.alerts WHERE environment=$1 AND resolved_at IS NULL
				ORDER BY opened_at DESC LIMIT 100) active),
		'heartbeats',(SELECT COALESCE(jsonb_agg(jsonb_build_object('worker_type',worker_type,'instance_id',instance_id,
		    'heartbeat_at',heartbeat_at,'last_progress_at',last_progress_at,'paused',paused)),'[]')
		    FROM (SELECT * FROM conversion.worker_heartbeats WHERE environment=$1 AND heartbeat_at>now()-interval '24 hours'
		          ORDER BY heartbeat_at DESC LIMIT 100) live))
	FROM period w
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
		WHERE e.environment=$1 AND (e.attribution_state='missing_pixel' OR e.reason='original_target_unavailable')
		  AND e.received_at>=now()-interval '7 days'
		  AND NOT EXISTS(SELECT 1 FROM conversion.deliveries d WHERE d.event_id=e.id AND d.role='original')
		  AND NOT EXISTS(SELECT 1 FROM conversion.legacy_outbox_imports m WHERE m.event_id=e.id
		      AND m.legacy_state='delivered' AND m.legacy_delivered_at IS NOT NULL)
		GROUP BY e.integration_id
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
		   d.reason IN ('destination_or_credential_unavailable','credential_decryption_failed','meta_credentials_or_permissions','destination_disabled','promotion_pixel_mismatch','partner_route_not_authorized')))
		GROUP BY 1,d.destination_id,x.pixel_id
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
		UNION ALL SELECT * FROM receipts UNION ALL SELECT * FROM stalled
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

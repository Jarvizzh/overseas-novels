package operations

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

func (r Repository) handleThroughput(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	minutes := 15
	var err error
	if value := q.Get("minutes"); value != "" {
		minutes, err = strconv.Atoi(value)
	}
	source, pixel := q.Get("source"), q.Get("pixel_id")
	if err != nil || minutes < 1 || minutes > 60 || len(source) > 100 || len(pixel) > 50 {
		writeError(w, http.StatusBadRequest, "invalid_filter")
		return
	}
	var result json.RawMessage
	if err := r.Pool.QueryRow(req.Context(), throughputSQL, r.Environment, time.Duration(minutes)*time.Minute,
		source, pixel).Scan(&result); err != nil {
		writeError(w, http.StatusServiceUnavailable, "throughput_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Use distinct source and target populations: the ingress volume is not
// multiplied by Pixel fan-out. Every stage counts its own activity window.
var throughputSQL = `
	WITH period AS (
		SELECT now()-$2::interval AS since,now() AS sampled_at
	), accounts AS (
		SELECT id,source FROM conversion.integrations WHERE environment=$1 AND ($3='' OR source=$3)
	), input AS (
		SELECT 'starnovel' AS source,q.event_name AS event_type,q.created_at AS received_at,
			q.consumed_at AS processed_at,CASE WHEN q.consumed_at IS NULL THEN 'pending'
			    WHEN q.processing_error IS NOT NULL THEN 'rejected' ELSE 'normalized' END AS result
		FROM reader_conversion_outbox q CROSS JOIN period p
		WHERE q.environment=$1 AND ($3='' OR $3='starnovel')
			AND (q.created_at>=p.since OR q.consumed_at>=p.since OR q.consumed_at IS NULL)
		UNION ALL
		SELECT i.source,b.source_event_type,b.received_at,b.processed_at,CASE
			WHEN b.state='quarantined' THEN 'quarantined'
			WHEN b.state='pending' THEN 'pending'
			WHEN b.reason='duplicate_payment_event' THEN 'merged'
			WHEN b.reason='payment_policy_fact_conflict' THEN 'conflict'
			ELSE 'normalized' END
		FROM accounts i JOIN conversion.inbox b ON b.integration_id=i.id CROSS JOIN period p
		WHERE b.received_at>=p.since OR b.state='pending' OR b.processed_at>=p.since
	), ingress AS (
		SELECT source,event_type,
			count(*) FILTER (WHERE received_at>=p.since) AS received,
			count(*) FILTER (WHERE processed_at>=p.since) AS processed,
			count(*) FILTER (WHERE processed_at>=p.since AND result IN ('normalized','merged')) AS normalized,
			count(*) FILTER (WHERE processed_at>=p.since AND result='normalized') AS new_events,
			count(*) FILTER (WHERE processed_at>=p.since AND result='merged') AS merged,
			count(*) FILTER (WHERE processed_at>=p.since AND result='conflict') AS conflicts,
			count(*) FILTER (WHERE processed_at>=p.since AND result='quarantined') AS quarantined,
			count(*) FILTER (WHERE processed_at>=p.since AND result IN ('rejected','quarantined','conflict')) AS rejected,
			count(*) FILTER (WHERE received_at>=p.since AND processed_at IS NULL AND result<>'pending') AS unknown_completion_time,
			count(*) FILTER (WHERE result='pending') AS pending,
			min(received_at) FILTER (WHERE result='pending') AS oldest_pending_at,
			percentile_cont(ARRAY[0.5,0.95,0.99]) WITHIN GROUP (ORDER BY
				extract(epoch FROM processed_at-received_at)*1000) FILTER (
					WHERE processed_at>=p.since AND result IN ('normalized','merged') AND processed_at>=received_at) AS normalize_ms_p50_p95_p99,
			percentile_cont(ARRAY[0.5,0.95,0.99]) WITHIN GROUP (ORDER BY
				extract(epoch FROM processed_at-received_at)*1000) FILTER (
					WHERE processed_at>=p.since AND processed_at>=received_at) AS processing_ms_p50_p95_p99
		FROM input CROSS JOIN period p GROUP BY source,event_type
	), eligible AS (
		SELECT x.id,x.pixel_id FROM conversion.destinations x WHERE x.environment=$1 AND ($4='' OR x.pixel_id=$4)
	), targets AS (
		SELECT i.source,e.event_name,x.pixel_id,
			count(*) FILTER (WHERE d.created_at>=p.since) AS created,
			count(*) FILTER (WHERE d.state='succeeded' AND d.finalized_at>=p.since) AS confirmed,
			count(*) FILTER (WHERE d.role='original' AND d.state='succeeded' AND d.finalized_at>=p.since) AS original_confirmed,
			count(*) FILTER (WHERE d.state='failed' AND d.finalized_at>=p.since) AS failed,
			count(*) FILTER (WHERE d.state='failed' AND d.finalized_at>=p.since
				AND (d.reason='deadline_exhausted' OR d.finalized_at>=d.deadline_at)) AS expired,
			count(*) FILTER (WHERE d.state='pending' AND d.lease_until IS NULL
				AND d.next_attempt_at<=p.sampled_at AND d.deadline_at>p.sampled_at) AS ready,
			min(d.next_attempt_at) FILTER (WHERE d.state='pending' AND d.lease_until IS NULL
				AND d.next_attempt_at<=p.sampled_at AND d.deadline_at>p.sampled_at) AS oldest_ready_at,
			percentile_cont(ARRAY[0.5,0.95,0.99]) WITHIN GROUP (ORDER BY
				extract(epoch FROM d.created_at-e.received_at)*1000) FILTER (
					WHERE d.created_at>=p.since AND d.created_at>=e.received_at) AS source_to_created_ms_p50_p95_p99,
			percentile_cont(ARRAY[0.5,0.95,0.99]) WITHIN GROUP (ORDER BY
				extract(epoch FROM d.finalized_at-e.received_at)*1000) FILTER (
					WHERE d.state='succeeded' AND d.finalized_at>=p.since
						AND d.finalized_at>=e.received_at) AS source_to_confirm_ms_p50_p95_p99,
			percentile_cont(ARRAY[0.5,0.95,0.99]) WITHIN GROUP (ORDER BY
				extract(epoch FROM first.sending_at-d.created_at)*1000) FILTER (
					WHERE first.sending_at>=p.since) AS created_to_first_send_ms_p50_p95_p99
		FROM conversion.deliveries d JOIN eligible x ON x.id=d.destination_id
		JOIN conversion.events e ON e.id=d.event_id JOIN accounts i ON i.id=e.integration_id
		LEFT JOIN LATERAL (
			SELECT a.sending_at FROM conversion.delivery_attempts a
			WHERE a.delivery_id=d.id AND a.sending_at IS NOT NULL
			ORDER BY a.attempt_no LIMIT 1
		) first ON true
		CROSS JOIN period p
		WHERE d.environment=$1 AND (d.created_at>=p.since OR d.finalized_at>=p.since
			OR (d.state='pending' AND d.lease_until IS NULL AND d.deadline_at>p.sampled_at))
		GROUP BY i.source,e.event_name,x.pixel_id
	), attempts AS (
		SELECT i.source,e.event_name,x.pixel_id,count(*) AS sending_marked,
			count(*) FILTER (WHERE a.reason='meta_rate_limited') AS rate_limited,
			count(*) FILTER (WHERE a.outcome='outcome_unknown') AS outcome_unknown,
			percentile_cont(ARRAY[0.5,0.95,0.99]) WITHIN GROUP (ORDER BY
				extract(epoch FROM a.responded_at-a.sending_at)*1000) FILTER (
					WHERE a.responded_at>=a.sending_at) AS response_ms_p50_p95_p99
		FROM conversion.delivery_attempts a JOIN conversion.deliveries d ON d.id=a.delivery_id
		JOIN eligible x ON x.id=d.destination_id JOIN conversion.events e ON e.id=d.event_id
		JOIN accounts i ON i.id=e.integration_id CROSS JOIN period p
		WHERE d.environment=$1 AND a.sending_at>=p.since
		GROUP BY i.source,e.event_name,x.pixel_id
    ), requests AS (
        SELECT x.pixel_id,count(*) AS requests,sum(matched.members) AS sent,
            avg(b.sent_count) AS mean_batch_size,
            percentile_cont(ARRAY[0.5,0.95,0.99]) WITHIN GROUP (ORDER BY b.sent_count) AS batch_size_p50_p95_p99,
            count(*) FILTER (WHERE b.receipt->>'HTTPStatus'='429') AS http_429,
            avg(CASE WHEN b.receipt IS NOT NULL THEN (b.receipt->>'HTTPStatus'='429')::int END) AS http_429_ratio,
            count(*) FILTER (WHERE (b.receipt->>'RateLimited')::boolean) AS rate_limited,
            count(*) FILTER (WHERE unknown.ambiguous) AS unknown_requests,
            COALESCE(sum(matched.members) FILTER (WHERE unknown.ambiguous),0) AS unknown_deliveries,
            COALESCE(sum(matched.members) FILTER (WHERE b.confirmed),0) AS confirmed_members,
            percentile_cont(ARRAY[0.5,0.95,0.99]) WITHIN GROUP (ORDER BY
                extract(epoch FROM (b.receipt->>'RespondedAt')::timestamptz-b.started_at)*1000)
                FILTER (WHERE (b.receipt->>'RespondedAt')::timestamptz>=b.started_at) AS response_ms_p50_p95_p99
        FROM conversion.meta_batches b JOIN eligible x ON x.id=b.destination_id CROSS JOIN period p
        CROSS JOIN LATERAL (
            SELECT b.receipt->>'Outcome'='outcome_unknown' OR (b.receipt IS NULL AND b.finished_at IS NOT NULL) AS ambiguous
        ) unknown
        ` + matchingBatchMembers("$3<>''", "true") + `
        WHERE b.environment=$1 AND b.started_at>=p.since AND matched.members>0
        GROUP BY x.pixel_id
	)
	SELECT jsonb_build_object('environment',$1,'from_at',p.since,'to_at',p.sampled_at,
		'partner_completion_recording_since',(SELECT applied_at FROM reader_schema_migrations WHERE version=2026111933),
        'request_counts_additive_across_filters',false,
        'ingress',COALESCE((SELECT jsonb_agg(to_jsonb(i) ORDER BY source,event_type) FROM ingress i),'[]'),
		'targets',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY source,event_name,pixel_id) FROM targets t),'[]'),
		'attempts',COALESCE((SELECT jsonb_agg(to_jsonb(a) ORDER BY source,event_name,pixel_id) FROM attempts a),'[]'),
        'requests',COALESCE((SELECT jsonb_agg(to_jsonb(b) ORDER BY pixel_id) FROM requests b),'[]'))
	FROM period p
`

// Keep request counts at one row per batch while applying filters to member
// counts. The CASE skips member joins when no member filters are selected.
func matchingBatchMembers(filtered, eventFilter string) string {
	return `CROSS JOIN LATERAL (
        SELECT CASE WHEN ` + filtered + ` THEN (
            SELECT count(*) FROM conversion.delivery_attempts a
            JOIN conversion.deliveries d ON d.id=a.delivery_id
            JOIN conversion.events e ON e.id=d.event_id JOIN accounts i ON i.id=e.integration_id
            WHERE a.batch_id=b.id AND a.batch_position IS NOT NULL AND (` + eventFilter + `)
        ) ELSE b.sent_count END AS members
    ) matched`
}

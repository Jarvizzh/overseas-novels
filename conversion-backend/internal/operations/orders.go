package operations

import (
	"context"
	"encoding/json"
	"strconv"

	"conversion-backend/internal/delivery"
	"conversion-backend/internal/partner"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	Pool              *pgxpool.Pool
	Environment       string
	EncryptCredential delivery.EncryptCredential
}

type OrderKey struct {
	ID            string
	IntegrationID int64
}

func ParseOrderKey(id, integration string) (OrderKey, bool) {
	key := OrderKey{ID: id}
	if integration == "" {
		order, ok := positiveID(id)
		if !ok {
			return OrderKey{}, false
		}
		key.ID = strconv.FormatInt(order, 10)
		return key, true
	}
	n, ok := positiveID(integration)
	if !ok || !partner.ValidOrderID(id) {
		return OrderKey{}, false
	}
	key.IntegrationID = n
	return key, true
}

func positiveID(value string) (int64, bool) {
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil && n > 0 && strconv.FormatInt(n, 10) == value
}

func (r Repository) Order(ctx context.Context, key OrderKey) (json.RawMessage, error) {
	var result json.RawMessage
	if key.IntegrationID == 0 {
		err := r.Pool.QueryRow(ctx, readerOrderSQL, key.ID, r.Environment).Scan(&result)
		return result, err
	}
	err := r.Pool.QueryRow(ctx, partnerOrderSQL, key.ID, r.Environment, key.IntegrationID).Scan(&result)
	return result, err
}

// Delivery projection excludes matching data, frozen routes, credentials and raw responses.
const deliveryDetails = `
	LEFT JOIN LATERAL (
		SELECT COALESCE(jsonb_agg(item ORDER BY rn) FILTER (WHERE rn <= 50),'[]') AS items,
		       count(*)>50 AS truncated, bool_or(role='original' AND state='succeeded') AS confirmed,
		       bool_or(role='original') AS has_original
		FROM (
			SELECT d.id,d.role,d.state,row_number() OVER (ORDER BY (d.role='original') DESC,d.id) AS rn,
				jsonb_strip_nulls(jsonb_build_object(
					'id',d.id::text,'pixel_id',p.pixel_id,'role',d.role,'state',d.state,'reason',d.reason,
					'meta_event_id',d.meta_event_id,'attempts',d.attempts,'created_at',d.created_at,
					'last_attempt_at',d.last_attempt_at,'next_attempt_at',CASE WHEN d.state='pending' THEN d.next_attempt_at END,
					'deadline_at',d.deadline_at,'finalized_at',d.finalized_at,
					'confirmed_at',CASE WHEN d.state='succeeded' THEN d.finalized_at END,
					'last_attempt',CASE WHEN a.attempt_no IS NOT NULL THEN jsonb_strip_nulls(jsonb_build_object(
						'number',a.attempt_no,'outcome',a.outcome,'reason',a.reason,'http_status',a.http_status,
						'graph_code',a.graph_code,'graph_subcode',a.graph_subcode,'events_received',a.events_received,
						'fbtrace_id',a.fbtrace_id,'recorded_at',a.recorded_at)) END)) AS item
			FROM conversion.deliveries d JOIN conversion.destinations p ON p.id=d.destination_id
			LEFT JOIN LATERAL (SELECT attempt_no,outcome,reason,http_status,graph_code,graph_subcode,events_received,fbtrace_id,recorded_at
				FROM conversion.delivery_attempts WHERE delivery_id=d.id ORDER BY attempt_no DESC LIMIT 1) a ON true
			WHERE d.event_id=e.id
			ORDER BY (d.role='original') DESC,d.id LIMIT 51
		) targets
	) outbound ON true
`

const readerOrderSQL = `
	WITH local_order AS (
		SELECT id,status,order_type,subscription_id,fulfilled_at,capi_expectation,
			COALESCE(reconciliation_note,'') <> '' AS review,
			'payment:'||id||':Purchase' AS purchase_key,
			'subscription:'||subscription_id||':Subscribe' AS subscribe_key
		FROM recharge_orders
		WHERE id=$1::text::bigint
		  AND COALESCE(capi_expectation->>'environment',$2)=$2
	), expected AS (
		SELECT spec->>'business_key' AS key,spec->>'name' AS name
		FROM local_order o CROSS JOIN LATERAL jsonb_array_elements(
			CASE WHEN jsonb_typeof(o.capi_expectation->'events')='array' THEN o.capi_expectation->'events' ELSE '[]' END) spec
		WHERE spec->>'name'<>'Subscribe' OR EXISTS (
			SELECT 1 FROM user_subscriptions s WHERE s.subscription_id=o.subscription_id AND s.capi_subscribe_order_id=o.id)
	), inbound AS (
		SELECT q.business_key,q.event_name,q.occurred_at,q.created_at,q.consumed_at,q.processing_error
		FROM local_order o JOIN reader_conversion_outbox q ON q.order_id=o.id AND q.environment=$2
	), normalized AS (
		SELECT e.* FROM local_order o JOIN conversion.integrations i ON i.environment=$2 AND i.source='starnovel'
		JOIN conversion.events e ON e.integration_id=i.id AND e.source_order_id=o.id::text
	), legacy AS (
		SELECT m.legacy_event_id AS event_id,e.event_name,m.legacy_state AS state,
		       m.legacy_delivered_at AS delivered_at,m.resolution,m.environment_evidence IS NOT NULL AS verified,
		       o.capi_expectation IS NULL
		         AND ((e.event_name='Purchase' AND e.source_event_id=o.purchase_key)
		            OR (e.event_name='Subscribe' AND e.source_event_id=o.subscribe_key AND EXISTS(
		               SELECT 1 FROM user_subscriptions s WHERE s.subscription_id=o.subscription_id AND s.capi_subscribe_order_id=o.id)))
		         AND ((m.legacy_state='delivered' AND m.legacy_delivered_at IS NOT NULL)
		           OR (m.environment_evidence IS NOT NULL AND (m.legacy_pixel_id IS NOT NULL OR e.attribution_state='missing_pixel')))
		         AS qualifies
		FROM normalized e JOIN conversion.legacy_outbox_imports m ON m.event_id=e.id
		JOIN local_order o ON o.id::text=e.source_order_id
		UNION ALL
		SELECT p.event_id,p.event_name,p.state,p.delivered_at,NULL,NULL,false
		FROM local_order o JOIN payment_event_outbox p ON p.event_id IN (o.purchase_key,o.subscribe_key) AND p.order_id=o.id
		WHERE NOT EXISTS (SELECT 1 FROM conversion.legacy_outbox_imports m WHERE m.legacy_event_id=p.event_id)
	), event_keys AS (
		SELECT key FROM expected UNION SELECT business_key FROM inbound
		UNION SELECT source_event_id FROM normalized UNION SELECT event_id FROM legacy
	), event_rows AS (
		SELECT k.key, jsonb_strip_nulls(jsonb_build_object(
			'id',e.id::text,'business_key',k.key,'event_name',COALESCE(e.event_name,x.name,q.event_name,l.event_name),
			'expected',x.key IS NOT NULL OR COALESCE(l.qualifies,false),'occurred_at',COALESCE(e.occurred_at,q.occurred_at),
			'enqueued_at',COALESCE(q.created_at,e.received_at),'attribution_state',e.attribution_state,
			'reason',COALESCE(q.processing_error,e.reason),
			'state',CASE WHEN outbound.confirmed OR (l.state='delivered' AND l.delivered_at IS NOT NULL) THEN 'confirmed'
				WHEN e.id IS NOT NULL AND outbound.has_original THEN 'delivery_created'
				WHEN e.id IS NOT NULL AND e.attribution_state='organic' THEN 'not_required'
				WHEN e.id IS NOT NULL THEN 'missing_target'
				WHEN q.processing_error IS NOT NULL THEN 'normalization_failed'
				WHEN q.consumed_at IS NOT NULL THEN 'missing_event'
				WHEN q.business_key IS NOT NULL THEN 'awaiting_normalization'
				WHEN l.event_id IS NOT NULL THEN 'legacy_record'
				ELSE 'missing_outbox' END,
			'legacy',CASE WHEN l.event_id IS NOT NULL THEN jsonb_strip_nulls(jsonb_build_object(
				'state',l.state,'resolution',l.resolution,'environment_verified',l.verified,
				'confirmed_at',CASE WHEN l.state='delivered' THEN l.delivered_at END)) END,
			'deliveries',outbound.items,'deliveries_truncated',outbound.truncated)) AS item
		FROM event_keys k LEFT JOIN expected x ON x.key=k.key
		LEFT JOIN inbound q ON q.business_key=k.key
		LEFT JOIN normalized e ON e.source_event_id=k.key
		LEFT JOIN legacy l ON l.event_id=k.key
		` + deliveryDetails + `
	)
	SELECT jsonb_strip_nulls(jsonb_build_object('order_id',o.id::text,'source','starnovel','environment',$2,
		'order_type',o.order_type,'order_status',o.status,
		'eligibility',jsonb_strip_nulls(jsonb_build_object(
			'state',CASE WHEN EXISTS(SELECT 1 FROM legacy WHERE qualifies) THEN 'expected'
			WHEN o.capi_expectation IS NULL THEN
				CASE WHEN o.review AND o.fulfilled_at IS NULL THEN 'pending_review'
				WHEN o.status='Pending' THEN 'pending'
				ELSE 'unknown' END
			WHEN EXISTS(SELECT 1 FROM expected) THEN 'expected' ELSE 'not_required' END,
			'reason',COALESCE(o.capi_expectation->>'reason',CASE WHEN EXISTS(SELECT 1 FROM legacy WHERE qualifies) THEN 'legacy_verified' END),'pixel_id',o.capi_expectation->>'pixel_id',
			'occurred_at',o.capi_expectation->'occurred_at','time_source',o.capi_expectation->>'time_source',
			'fulfilled_at',o.fulfilled_at)),
		'events',(SELECT COALESCE(jsonb_agg(item ORDER BY key),'[]') FROM event_rows)))
	FROM local_order o
`

const partnerOrderSQL = `
	WITH account AS (
		SELECT id,source FROM conversion.integrations WHERE id=$3 AND environment=$2 AND source<>'starnovel'
	), source_events AS (
		SELECT e.* FROM account i JOIN conversion.events e ON e.integration_id=i.id
		WHERE e.source_order_id=$1 ORDER BY e.id DESC LIMIT 101
	), received AS (
		SELECT o.id,o.source_event_id,o.source_event_type,o.received_at,o.state,o.reason
		FROM account i JOIN conversion.inbox o ON o.integration_id=i.id
		WHERE o.source_order_id=$1 ORDER BY o.received_at DESC,o.id DESC LIMIT 101
	), event_rows AS (
		SELECT e.id,jsonb_strip_nulls(jsonb_build_object(
			'id',e.id::text,'business_key',e.source_event_id,'source_event_type',e.source_event_type,'event_name',e.event_name,
			'occurred_at',e.occurred_at,'enqueued_at',e.received_at,'attribution_state',e.attribution_state,'reason',e.reason,
			'state',CASE WHEN outbound.confirmed THEN 'confirmed' WHEN outbound.has_original THEN 'delivery_created' ELSE 'missing_target' END,
			'deliveries',outbound.items,'deliveries_truncated',outbound.truncated,
			'partner_report',receipt.item)) AS item
		FROM (SELECT * FROM source_events ORDER BY id DESC LIMIT 100) e
		` + deliveryDetails + `
		LEFT JOIN LATERAL (
			SELECT jsonb_strip_nulls(jsonb_build_object('id',r.id::text,'status',r.report_status,'state',r.state,
				'reason',r.reason,'next_attempt_at',CASE WHEN r.state='pending' THEN r.next_attempt_at END)) AS item
			FROM conversion.partner_reports r WHERE r.integration_id=e.integration_id AND r.event_id=e.id
		) receipt ON true
	)
	SELECT jsonb_build_object('order_id',$1,'source',a.source,'environment',$2,'integration_id',a.id::text,
		'inbox',(SELECT COALESCE(jsonb_agg(jsonb_strip_nulls(jsonb_build_object('id',id::text,
			'source_event_id',source_event_id,'source_event_type',source_event_type,'received_at',received_at,
			'state',state,'reason',reason)) ORDER BY received_at DESC,id DESC),'[]')
			FROM (SELECT * FROM received ORDER BY received_at DESC,id DESC LIMIT 100) recent),
		'inbox_truncated',(SELECT count(*)>100 FROM received),
		'events',(SELECT COALESCE(jsonb_agg(item ORDER BY id DESC),'[]') FROM event_rows),
		'events_truncated',(SELECT count(*)>100 FROM source_events))
	FROM account a WHERE EXISTS(SELECT 1 FROM received) OR EXISTS(SELECT 1 FROM source_events)
`

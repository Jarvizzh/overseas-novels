package operations

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const eventProjection = `jsonb_strip_nulls(jsonb_build_object(
	'id',e.id::text,'integration_id',e.integration_id::text,'source',i.source,'source_event_type',e.source_event_type,
	'source_event_id',e.source_event_id,'event_name',e.event_name,'order_id',e.source_order_id,'promotion_id',e.promotion_id,
	'attribution_state',e.attribution_state,'reason',e.reason,'occurred_at',e.occurred_at,'created_at',e.created_at,
	'enqueued_at',e.received_at,'deliveries',outbound.items,'deliveries_truncated',outbound.truncated))`

func (repo Repository) handleEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := positiveID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_event_id")
		return
	}
	q := r.URL.Query()
	var deliveryID, before int64
	if v := q.Get("delivery_id"); v != "" {
		deliveryID, ok = positiveID(v)
	}
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_delivery_id")
		return
	}
	if v := q.Get("before_attempt"); v != "" {
		before, ok = positiveID(v)
	}
	if !ok || before > 2147483647 {
		writeError(w, http.StatusBadRequest, "invalid_attempt_cursor")
		return
	}
	var data json.RawMessage
	err := repo.Pool.QueryRow(r.Context(), eventDetailsSQL, repo.Environment, id, deliveryID, before).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "resource_not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "reconciliation_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, data)
}

const eventDetailsSQL = `
	SELECT ` + eventProjection + ` || jsonb_build_object(
		'partner_reports',COALESCE((SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
		    'id',r.id::text,'state',r.state,'reason',r.reason,'status',r.report_status,'next_attempt_at',
		    CASE WHEN r.state='pending' THEN r.next_attempt_at END,
		    'attempt_count',r.attempts,'attempts',COALESCE(history.items,'[]'))) ORDER BY r.id)
		    FROM conversion.partner_reports r
		    LEFT JOIN LATERAL (
		        SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
		            'number',a.attempt_no,'started_at',a.started_at,'sending_at',a.sending_at,
		            'responded_at',a.responded_at,'recorded_at',a.recorded_at,'outcome',a.outcome,'reason',a.reason,
		            'http_status',a.http_status,'business_code',a.business_code::text,'retry_at',a.retry_at))
		            ORDER BY a.attempt_no DESC) AS items
		        FROM (
		            SELECT attempt_no,started_at,sending_at,responded_at,recorded_at,outcome,reason,http_status,business_code,retry_at
		            FROM conversion.partner_report_attempts WHERE report_id=r.id ORDER BY attempt_no DESC LIMIT 20
		        ) a
		    ) history ON true
		    WHERE r.event_id=e.id AND r.integration_id=e.integration_id),'[]'),
		'attempts',COALESCE((SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
			'number',a.attempt_no,'started_at',a.started_at,'sending_at',a.sending_at,'responded_at',a.responded_at,
			'recorded_at',a.recorded_at,'outcome',a.outcome,'reason',a.reason,'http_status',a.http_status,
			'graph_code',a.graph_code,'graph_subcode',a.graph_subcode,'events_received',a.events_received,'fbtrace_id',a.fbtrace_id))
			ORDER BY a.attempt_no DESC) FROM (
			   SELECT a.* FROM conversion.delivery_attempts a JOIN conversion.deliveries d ON d.id=a.delivery_id
			   WHERE d.event_id=e.id AND a.delivery_id=$3 AND ($4::int=0 OR a.attempt_no<$4)
			   ORDER BY a.attempt_no DESC LIMIT 50) a),'[]'))
	FROM conversion.events e JOIN conversion.integrations i ON i.id=e.integration_id
	` + deliveryDetails + `
	WHERE e.environment=$1 AND e.id=$2
`

func (repo Repository) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	var err error
	if v := q.Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
	}
	if err != nil || limit < 1 || limit > 100 {
		writeError(w, http.StatusBadRequest, "invalid_limit")
		return
	}

	var from, to, cursorAt *time.Time
	now := time.Now()
	fallback := now.Add(-24 * time.Hour)
	if q.Get("order_id") == "" && q.Get("event_id") == "" {
		from = &fallback
	}
	for key, target := range map[string]**time.Time{"from": &from, "to": &to} {
		if raw := q.Get(key); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_time")
				return
			}
			*target = &parsed
		}
	}
	var cursorID, integration, eventID int64
	if raw := q.Get("cursor"); raw != "" {
		ts, id, ok := strings.Cut(raw, "|")
		when, err := time.Parse(time.RFC3339Nano, ts)
		parsedID, valid := positiveID(id)
		if !ok || err != nil || !valid {
			writeError(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
		cursorAt, cursorID = &when, parsedID
	}
	for key, target := range map[string]*int64{"integration_id": &integration, "event_id": &eventID} {
		if v := q.Get(key); v != "" {
			n, ok := positiveID(v)
			if !ok {
				writeError(w, http.StatusBadRequest, "invalid_id")
				return
			}
			*target = n
		}
	}
	if from != nil && to != nil && from.After(*to) {
		writeError(w, http.StatusBadRequest, "invalid_time")
		return
	}
	var out json.RawMessage
	err = repo.Pool.QueryRow(r.Context(), eventListSQL, repo.Environment, from, to, cursorAt, cursorID, limit,
		q.Get("source"), integration, q.Get("event_name"), q.Get("order_id"), q.Get("promotion_id"),
		q.Get("pixel_id"), q.Get("delivery_state"), q.Get("attribution_state"), eventID).Scan(&out)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "reconciliation_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

const eventListSQL = `
	WITH matched AS (
		SELECT e.id,e.created_at FROM conversion.events e JOIN conversion.integrations i ON i.id=e.integration_id
		WHERE e.environment=$1 AND ($2::timestamptz IS NULL OR e.created_at>=$2)
			AND ($3::timestamptz IS NULL OR e.created_at<$3)
			AND ($4::timestamptz IS NULL OR (e.created_at,e.id)<($4,$5))
			AND ($7='' OR i.source=$7) AND ($8::bigint=0 OR e.integration_id=$8)
			AND ($9='' OR e.event_name=$9) AND ($10='' OR e.source_order_id=$10)
			AND ($11='' OR e.promotion_id=$11) AND ($14='' OR e.attribution_state=$14)
			AND ($15::bigint=0 OR e.id=$15)
			AND (($12='' AND $13='') OR EXISTS (
				SELECT 1 FROM conversion.deliveries d JOIN conversion.destinations x ON x.id=d.destination_id
				WHERE d.event_id=e.id AND ($12='' OR x.pixel_id=$12) AND ($13='' OR d.state=$13)))
		ORDER BY e.created_at DESC,e.id DESC LIMIT $6::integer+1
	), page AS (
		SELECT m.id,m.created_at FROM matched m ORDER BY m.created_at DESC,m.id DESC LIMIT $6
	), projected AS (
		SELECT p.id,p.created_at,` + eventProjection + ` AS item
		FROM page p JOIN conversion.events e ON e.id=p.id JOIN conversion.integrations i ON i.id=e.integration_id
		` + deliveryDetails + `
	)
	SELECT jsonb_build_object('events',COALESCE((SELECT jsonb_agg(item ORDER BY created_at DESC,id DESC) FROM projected),'[]'),
		'next_cursor',CASE WHEN (SELECT count(*) FROM matched)>$6 THEN
		    (SELECT to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')||'|'||id
		     FROM page ORDER BY created_at,id LIMIT 1) END)
`

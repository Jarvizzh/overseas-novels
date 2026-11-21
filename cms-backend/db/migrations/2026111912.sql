ALTER TABLE conversion.inbox
    ADD COLUMN source_app_id text,
    ADD COLUMN normalized_event_id bigint,
    ADD FOREIGN KEY (normalized_event_id, integration_id) REFERENCES conversion.events (id, integration_id);

-- Preserve app scope even after retention redacts the partner receipt.
UPDATE conversion.inbox b
SET source_app_id=COALESCE(NULLIF(b.payload->'callback'->>'app_id',''),
    CASE WHEN b.state='consumed' THEN (
        SELECT e.partner_fields->>'app_id' FROM conversion.events e
        WHERE e.integration_id=b.integration_id AND e.source_event_type=b.source_event_type
          AND e.source_event_id=b.source_event_id) END)
FROM conversion.integrations i
WHERE i.id=b.integration_id AND i.source='sinan';

UPDATE conversion.events e
SET payment_app_scope=NULLIF(e.partner_fields->>'app_id','')
FROM conversion.integrations i
WHERE i.id=e.integration_id AND i.source='sinan'
  AND e.event_name='Purchase' AND e.source_order_id IS NOT NULL;

-- Historic duplicates remain queryable. New events reserve their app/order
-- identity even when route authorization prevents a decision.
CREATE UNIQUE INDEX conversion_events_partner_payment_key
    ON conversion.events (integration_id, environment, payment_app_scope, source_order_id)
    WHERE source_event_type='recharge-paid' AND payment_policy_state IN ('pending','decided');

CREATE INDEX conversion_events_partner_payment_lookup
    ON conversion.events (integration_id, environment, payment_app_scope, source_order_id, id)
    WHERE source_event_type='recharge-paid';

CREATE INDEX conversion_inbox_normalized_event
    ON conversion.inbox (normalized_event_id, integration_id) WHERE normalized_event_id IS NOT NULL;

INSERT INTO reader_schema_migrations (version) VALUES (2026111912);

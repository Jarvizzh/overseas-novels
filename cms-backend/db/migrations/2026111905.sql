CREATE TABLE conversion.operation_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    environment text NOT NULL,
    action text NOT NULL,
    subject_id text NOT NULL,
    operator_id text NOT NULL,
    operator_role text NOT NULL,
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 500),
    evidence_reference text,
    before_state jsonb NOT NULL,
    after_state jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX conversion_audit_subject ON conversion.operation_audit
    (environment, action, subject_id, id DESC);

-- Independent evidence survives removal of queue and delivery rows.
CREATE TABLE reader_conversion_repair_facts (
    business_key text PRIMARY KEY,
    event_name text NOT NULL,
    order_id bigint NOT NULL,
    environment text NOT NULL,
    occurred_at timestamptz,
    created_at timestamptz NOT NULL,
    snapshot jsonb NOT NULL,
    normalized_at timestamptz,
    original_send_started_at timestamptz
);

CREATE FUNCTION capture_reader_conversion_repair_fact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO reader_conversion_repair_facts
        (business_key,event_name,order_id,environment,occurred_at,created_at,snapshot)
    VALUES (NEW.business_key,NEW.event_name,NEW.order_id,NEW.environment,NEW.occurred_at,NEW.created_at,NEW.snapshot)
    ON CONFLICT (business_key) DO NOTHING;
    RETURN NULL;
END $$;

CREATE TRIGGER capture_reader_conversion_repair_fact AFTER INSERT ON reader_conversion_outbox
FOR EACH ROW WHEN (NEW.order_id IS NOT NULL AND NEW.event_name IN ('Purchase','Subscribe'))
EXECUTE FUNCTION capture_reader_conversion_repair_fact();

CREATE FUNCTION conversion.mark_original_send_started() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE reader_conversion_repair_facts f SET original_send_started_at=NEW.sending_at
    FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id
    JOIN conversion.integrations i ON i.id=e.integration_id
    WHERE d.id=NEW.delivery_id AND d.role='original' AND i.source='starnovel' AND i.account_key='reader'
      AND f.environment=e.environment AND f.business_key=e.source_event_id AND f.event_name=e.source_event_type
      AND f.original_send_started_at IS NULL;
    RETURN NULL;
END $$;

CREATE TRIGGER conversion_original_send_started AFTER UPDATE OF sending_at ON conversion.delivery_attempts
FOR EACH ROW WHEN (OLD.sending_at IS NULL AND NEW.sending_at IS NOT NULL)
EXECUTE FUNCTION conversion.mark_original_send_started();

CREATE TABLE conversion.alerts (
    environment text NOT NULL,
    kind text NOT NULL,
    subject text NOT NULL,
    detail jsonb NOT NULL,
    opened_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    PRIMARY KEY (environment,kind,subject)
);
-- Serialize new promotion bindings with destination retirement.
CREATE FUNCTION conversion.guard_promotion_pixel() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    active boolean;
BEGIN
    IF NEW.fb_pixel_id IS NULL THEN RETURN NEW; END IF;
    IF TG_OP='UPDATE' AND NEW.fb_pixel_id IS NOT DISTINCT FROM OLD.fb_pixel_id THEN RETURN NEW; END IF;
    SELECT enabled AND retired_at IS NULL INTO active
    FROM conversion.destinations WHERE legacy_fb_pixel_id=NEW.fb_pixel_id FOR SHARE;
    IF FOUND AND NOT active THEN
        RAISE EXCEPTION 'Conversion destination cannot be bound' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER guard_promotion_pixel BEFORE INSERT OR UPDATE OF fb_pixel_id ON promotion_links
FOR EACH ROW EXECUTE FUNCTION conversion.guard_promotion_pixel();

INSERT INTO reader_schema_migrations (version) VALUES (2026111905);

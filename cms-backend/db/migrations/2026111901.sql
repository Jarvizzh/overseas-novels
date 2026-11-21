CREATE SCHEMA conversion;

ALTER TABLE recharge_orders ADD COLUMN capi_expectation jsonb;
ALTER TABLE user_subscriptions
    ADD COLUMN capi_subscribe_state text NOT NULL DEFAULT 'unknown'
        CHECK (capi_subscribe_state IN ('eligible', 'claimed', 'historical', 'unknown')),
    ADD COLUMN capi_subscribe_order_id bigint REFERENCES recharge_orders(id),
    ADD CHECK ((capi_subscribe_state = 'claimed') = (capi_subscribe_order_id IS NOT NULL));

CREATE TABLE reader_conversion_outbox (
    business_key text PRIMARY KEY,
    event_name text NOT NULL,
    order_id bigint,
    intent_id text,
    occurred_at timestamptz,
    snapshot jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    consumed_at timestamptz
);
CREATE INDEX reader_conversion_outbox_pending
    ON reader_conversion_outbox (created_at, business_key) WHERE consumed_at IS NULL;
CREATE INDEX reader_conversion_outbox_order ON reader_conversion_outbox (order_id) WHERE order_id IS NOT NULL;

CREATE TABLE conversion.credentials (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    ciphertext bytea NOT NULL,
    key_version text NOT NULL,
    rotated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE conversion.integrations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source text NOT NULL,
    account_key text NOT NULL,
    environment text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    allowed_apps text[] NOT NULL DEFAULT '{}',
    allowed_events text[] NOT NULL DEFAULT '{}',
    contract_version text NOT NULL,
    inbound_credential_id bigint REFERENCES conversion.credentials,
    report_credential_id bigint REFERENCES conversion.credentials,
    report_config jsonb NOT NULL DEFAULT '{}',
    UNIQUE (environment, source, account_key),
    UNIQUE (id, environment)
);

CREATE TABLE conversion.destinations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    environment text NOT NULL,
    media text NOT NULL CHECK (media = 'meta'),
    pixel_id text NOT NULL CHECK (pixel_id ~ '^[0-9]+$'),
    display_name text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    retired_at timestamptz,
    next_claim_at timestamptz NOT NULL DEFAULT '-infinity',
    credential_id bigint REFERENCES conversion.credentials,
    legacy_fb_pixel_id bigint UNIQUE,
    UNIQUE (environment, media, pixel_id),
    UNIQUE (id, environment)
);

CREATE TABLE conversion.integration_destinations (
    integration_id bigint NOT NULL,
    destination_id bigint NOT NULL,
    environment text NOT NULL,
    default_route boolean NOT NULL DEFAULT false,
    PRIMARY KEY (integration_id, destination_id),
    FOREIGN KEY (integration_id, environment) REFERENCES conversion.integrations (id, environment),
    FOREIGN KEY (destination_id, environment) REFERENCES conversion.destinations (id, environment)
);

CREATE TABLE conversion.inbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    integration_id bigint NOT NULL REFERENCES conversion.integrations,
    source_event_type text NOT NULL,
    source_event_id text,
    received_at timestamptz NOT NULL DEFAULT now(),
    payload_digest bytea,
    payload jsonb,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'consumed', 'quarantined')),
    reason text,
    CHECK (state = 'quarantined' OR (source_event_id IS NOT NULL AND payload_digest IS NOT NULL))
);
CREATE UNIQUE INDEX conversion_inbox_source_event ON conversion.inbox
    (integration_id, source_event_type, source_event_id) WHERE state <> 'quarantined';
CREATE INDEX conversion_inbox_pending ON conversion.inbox (received_at, id) WHERE state = 'pending';

CREATE TABLE conversion.events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    integration_id bigint NOT NULL,
    environment text NOT NULL,
    source_event_type text NOT NULL,
    source_event_id text NOT NULL,
    event_name text NOT NULL CHECK (event_name IN ('CompleteRegistration','InitiateCheckout','Purchase','Subscribe')),
    source_order_id text,
    promotion_id text,
    attribution_state text NOT NULL CHECK (attribution_state IN ('matched','organic','missing_pixel','unknown')),
    reason text,
    occurred_at timestamptz,
    received_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    amount numeric,
    currency varchar(3),
    action_source text,
    source_url text,
    matching_data jsonb NOT NULL DEFAULT '{}',
    partner_fields jsonb,
    FOREIGN KEY (integration_id, environment) REFERENCES conversion.integrations (id, environment),
    UNIQUE (integration_id, source_event_type, source_event_id),
    UNIQUE (id, integration_id),
    UNIQUE (id, environment)
);
CREATE INDEX conversion_events_created ON conversion.events (created_at DESC, id DESC);
CREATE INDEX conversion_events_integration_order ON conversion.events (integration_id, source_order_id);
CREATE INDEX conversion_events_integration_time ON conversion.events (integration_id, occurred_at);
CREATE INDEX conversion_events_promotion ON conversion.events (promotion_id, created_at DESC) WHERE promotion_id IS NOT NULL;

CREATE TABLE conversion.deliveries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id bigint NOT NULL,
    destination_id bigint NOT NULL,
    environment text NOT NULL,
    meta_event_id text NOT NULL CHECK (meta_event_id <> ''),
    role text NOT NULL CHECK (role IN ('original', 'additional')),
    frozen_route jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    deadline_at timestamptz NOT NULL,
    finalized_at timestamptz,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','blocked','outcome_unknown','succeeded','failed','skipped')),
    reason text,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_attempt_at timestamptz,
    lease_token uuid,
    lease_until timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    retry_budget_base_attempt integer NOT NULL DEFAULT 0 CHECK (retry_budget_base_attempt BETWEEN 0 AND attempts),
    budget_reset_used boolean NOT NULL DEFAULT false,
    unknown_retry_used boolean NOT NULL DEFAULT false,
    unknown_attempt_no integer CHECK (unknown_attempt_no > 0 AND unknown_attempt_no <= attempts),
    FOREIGN KEY (event_id, environment) REFERENCES conversion.events (id, environment),
    FOREIGN KEY (destination_id, environment) REFERENCES conversion.destinations (id, environment),
    UNIQUE (event_id, destination_id),
    UNIQUE (id, event_id, role),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK ((finalized_at IS NOT NULL) = (state IN ('succeeded','failed','skipped'))),
    CHECK (lease_token IS NULL OR state = 'pending')
);
CREATE UNIQUE INDEX conversion_deliveries_original ON conversion.deliveries (event_id) WHERE role = 'original';
CREATE INDEX conversion_deliveries_pending ON conversion.deliveries (environment, next_attempt_at, id)
    WHERE state = 'pending' AND lease_until IS NULL;
CREATE INDEX conversion_deliveries_deadline ON conversion.deliveries (environment, deadline_at, id)
    WHERE finalized_at IS NULL;
CREATE INDEX conversion_deliveries_lease ON conversion.deliveries (environment, lease_until, id)
    WHERE lease_until IS NOT NULL;
CREATE INDEX conversion_deliveries_destination ON conversion.deliveries (destination_id, state, id);

CREATE TABLE conversion.delivery_attempts (
    delivery_id bigint NOT NULL REFERENCES conversion.deliveries,
    attempt_no integer NOT NULL CHECK (attempt_no > 0),
    lease_token uuid NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    sending_at timestamptz,
    responded_at timestamptz,
    recorded_at timestamptz,
    outcome text CHECK (outcome IN ('accepted','not_sent','retryable_rejection','permanent_rejection','outcome_unknown')),
    reason text,
    http_status integer,
    graph_code integer,
    graph_subcode integer,
    events_received integer,
    fbtrace_id text,
    PRIMARY KEY (delivery_id, attempt_no),
    CHECK ((recorded_at IS NULL) = (outcome IS NULL)),
    CHECK (outcome IS DISTINCT FROM 'accepted' OR
           (sending_at IS NOT NULL AND http_status IS NOT DISTINCT FROM 200 AND events_received IS NOT DISTINCT FROM 1))
);

CREATE TABLE conversion.delivery_diagnostics (
    delivery_id bigint NOT NULL,
    attempt_no integer NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    late boolean NOT NULL,
    evidence jsonb NOT NULL,
    PRIMARY KEY (delivery_id, attempt_no, late),
    FOREIGN KEY (delivery_id, attempt_no) REFERENCES conversion.delivery_attempts
);
CREATE INDEX conversion_delivery_diagnostics_expiry ON conversion.delivery_diagnostics (recorded_at);

CREATE TABLE conversion.partner_reports (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    integration_id bigint NOT NULL,
    event_id bigint NOT NULL,
    original_delivery_id bigint NOT NULL,
    delivery_role text NOT NULL DEFAULT 'original' CHECK (delivery_role = 'original'),
    report_status smallint NOT NULL CHECK (report_status IN (-1, 0, 1)),
    contract_version text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','blocked','outcome_unknown','confirmed')),
    reason text,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0,
    lease_token uuid,
    lease_until timestamptz,
    FOREIGN KEY (event_id, integration_id) REFERENCES conversion.events (id, integration_id),
    FOREIGN KEY (original_delivery_id, event_id, delivery_role) REFERENCES conversion.deliveries (id, event_id, role),
    UNIQUE (integration_id, event_id, original_delivery_id)
);
CREATE INDEX conversion_partner_reports_pending ON conversion.partner_reports (next_attempt_at, id)
    WHERE state = 'pending' AND lease_until IS NULL;

CREATE TABLE conversion.partner_report_attempts (
    report_id bigint NOT NULL REFERENCES conversion.partner_reports,
    attempt_no integer NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    sending_at timestamptz,
    recorded_at timestamptz,
    outcome text,
    reason text,
    retry_at timestamptz,
    PRIMARY KEY (report_id, attempt_no)
);

CREATE TABLE conversion.worker_heartbeats (
    environment text NOT NULL,
    worker_type text NOT NULL,
    instance_id text NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    paused boolean NOT NULL,
    last_progress_at timestamptz,
    PRIMARY KEY (environment, worker_type, instance_id)
);

INSERT INTO reader_schema_migrations (version) VALUES (2026111901);

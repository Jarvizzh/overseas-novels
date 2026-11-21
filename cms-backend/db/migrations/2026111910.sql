CREATE SEQUENCE conversion.payment_policy_branch_ids AS bigint;

CREATE TABLE conversion.payment_policy_scopes (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    environment text NOT NULL,
    integration_id bigint NOT NULL,
    app_scope text NOT NULL CHECK (app_scope <> ''),
    scope_kind text NOT NULL,
    promotion_key text NOT NULL,
    active_version_id bigint,
    CHECK ((scope_kind = 'default' AND promotion_key = '') OR
           (scope_kind = 'promotion' AND promotion_key <> '')),
    UNIQUE (environment, integration_id, app_scope, scope_kind, promotion_key),
    UNIQUE (id, environment, integration_id, app_scope),
    FOREIGN KEY (integration_id, environment) REFERENCES conversion.integrations (id, environment)
);

CREATE TABLE conversion.payment_policy_versions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope_id bigint NOT NULL REFERENCES conversion.payment_policy_scopes,
    version_no bigint NOT NULL CHECK (version_no > 0),
    config jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    published_by text NOT NULL,
    published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (scope_id, version_no),
    UNIQUE (scope_id, id)
);

ALTER TABLE conversion.payment_policy_scopes ADD FOREIGN KEY (id, active_version_id)
    REFERENCES conversion.payment_policy_versions (scope_id, id);

CREATE TABLE conversion.payment_policy_decisions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    environment text NOT NULL,
    integration_id bigint NOT NULL,
    app_scope text NOT NULL CHECK (app_scope <> ''),
    payment_key text NOT NULL CHECK (payment_key <> ''),
    scope_kind text NOT NULL,
    promotion_key text NOT NULL,
    original_destination_id bigint NOT NULL,
    policy_scope_id bigint,
    policy_version_id bigint,
    branch_id bigint CHECK (branch_id > 0),
    mode text CHECK (mode IN ('all', 'ratio', 'cycle')),
    cycle_sequence bigint CHECK (cycle_sequence BETWEEN 1 AND 9007199254740991),
    sample_bucket integer CHECK (sample_bucket BETWEEN 0 AND 9999),
    sample_version smallint CHECK (sample_version > 0),
    outcome text CHECK (outcome IN ('selected', 'skipped')),
    reason text,
    amount_minor bigint CHECK (amount_minor >= 0),
    currency text CHECK (currency ~ '^[A-Z]{3}$'),
    payment_kind text NOT NULL,
    decided_at timestamptz,
    CHECK ((scope_kind = 'default' AND promotion_key = '') OR
           (scope_kind = 'promotion' AND promotion_key <> '')),
    CHECK ((policy_scope_id IS NULL) = (policy_version_id IS NULL)),
    CHECK (branch_id IS NULL OR policy_version_id IS NOT NULL),
    CHECK ((mode IS NOT DISTINCT FROM 'cycle') = (cycle_sequence IS NOT NULL)),
    CHECK ((sample_bucket IS NULL) = (sample_version IS NULL)),
    CHECK ((mode IS NOT DISTINCT FROM 'ratio') = (sample_bucket IS NOT NULL)),
    CHECK ((outcome IS NULL) = (decided_at IS NULL) AND (outcome IS NULL) = (reason IS NULL)),
    UNIQUE (environment, integration_id, app_scope, payment_key),
    UNIQUE (id, environment, integration_id, app_scope, payment_key),
    FOREIGN KEY (integration_id, environment) REFERENCES conversion.integrations (id, environment),
    FOREIGN KEY (original_destination_id, environment) REFERENCES conversion.destinations (id, environment),
    FOREIGN KEY (policy_scope_id, environment, integration_id, app_scope)
        REFERENCES conversion.payment_policy_scopes (id, environment, integration_id, app_scope),
    FOREIGN KEY (policy_scope_id, policy_version_id)
        REFERENCES conversion.payment_policy_versions (scope_id, id)
);

-- The transaction can use a provisional row to arbitrate the payment key.
-- It must either complete the decision or roll back before releasing it.
CREATE FUNCTION conversion.require_complete_payment_policy_decision() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM conversion.payment_policy_decisions WHERE id = NEW.id AND outcome IS NULL) THEN
        RAISE EXCEPTION 'unfinished payment policy decision %', NEW.id USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER require_complete_payment_policy_decision
    AFTER INSERT ON conversion.payment_policy_decisions DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (NEW.outcome IS NULL)
    EXECUTE FUNCTION conversion.require_complete_payment_policy_decision();

CREATE INDEX payment_policy_decisions_environment_time
    ON conversion.payment_policy_decisions (environment, decided_at, id);

CREATE TABLE conversion.payment_policy_counters (
    environment text NOT NULL,
    integration_id bigint NOT NULL,
    app_scope text NOT NULL CHECK (app_scope <> ''),
    scope_kind text NOT NULL,
    promotion_key text NOT NULL,
    branch_id bigint NOT NULL CHECK (branch_id > 0),
    last_sequence bigint NOT NULL CHECK (last_sequence BETWEEN 1 AND 9007199254740991),
    CHECK ((scope_kind = 'default' AND promotion_key = '') OR
           (scope_kind = 'promotion' AND promotion_key <> '')),
    PRIMARY KEY (environment, integration_id, app_scope, scope_kind, promotion_key, branch_id),
    FOREIGN KEY (integration_id, environment) REFERENCES conversion.integrations (id, environment)
);

ALTER TABLE conversion.events
    ADD COLUMN payment_app_scope text,
    ADD COLUMN payment_policy_decision_id bigint,
    ADD COLUMN payment_policy_state text NOT NULL DEFAULT 'historical',
    ADD CHECK (payment_policy_state IN ('historical', 'not_applicable', 'pending', 'decided')),
    ADD CHECK ((payment_policy_state = 'decided') = (payment_policy_decision_id IS NOT NULL)),
    ADD CHECK (payment_policy_state NOT IN ('pending', 'decided') OR (
        event_name IN ('Purchase', 'Subscribe') AND
        payment_app_scope IS NOT NULL AND payment_app_scope <> '' AND
        source_order_id IS NOT NULL AND source_order_id <> '')),
    ADD FOREIGN KEY (payment_policy_decision_id, environment, integration_id, payment_app_scope, source_order_id)
        REFERENCES conversion.payment_policy_decisions (id, environment, integration_id, app_scope, payment_key);

CREATE UNIQUE INDEX conversion_events_payment_decision_name
    ON conversion.events (payment_policy_decision_id, event_name) WHERE payment_policy_decision_id IS NOT NULL;

CREATE TABLE reader_payment_promotion_coverage (
    environment text NOT NULL,
    promotion_link_id integer NOT NULL REFERENCES promotion_links ON DELETE CASCADE,
    verified_at timestamptz NOT NULL,
    evidence_reference text NOT NULL CHECK (evidence_reference <> ''),
    PRIMARY KEY (environment, promotion_link_id)
);

INSERT INTO reader_schema_migrations (version) VALUES (2026111910);

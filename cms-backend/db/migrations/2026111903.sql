CREATE TABLE conversion.legacy_outbox_imports (
    legacy_event_id text PRIMARY KEY,
    event_id bigint NOT NULL UNIQUE REFERENCES conversion.events,
    legacy_state text NOT NULL,
    legacy_attempts integer NOT NULL CHECK (legacy_attempts >= 0),
    legacy_delivered_at timestamptz,
    legacy_pixel_id text,
    legacy_next_attempt_at timestamptz NOT NULL,
    legacy_had_lease boolean NOT NULL,
    resolution text NOT NULL,
    environment_evidence text,
    imported_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO reader_schema_migrations (version) VALUES (2026111903);

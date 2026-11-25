CREATE TABLE conversion.meta_batches (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    environment text NOT NULL,
    destination_id bigint NOT NULL,
    lease_token uuid NOT NULL DEFAULT gen_random_uuid(),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    lease_until timestamptz NOT NULL,
    started_at timestamptz,
    sent_count integer,
    credential_id bigint REFERENCES conversion.credentials,
    key_version text,
    receipt_at timestamptz,
    receipt jsonb,
    late_receipt_at timestamptz,
    late_receipt jsonb,
    finished_at timestamptz,
    confirmed boolean GENERATED ALWAYS AS (
        COALESCE(receipt->>'Outcome'='accepted' AND receipt->>'HTTPStatus'='200'
            AND receipt->>'GraphCode' IS NULL AND receipt->>'EventsReceived'=sent_count::text,false)
    ) STORED,
    FOREIGN KEY (destination_id,environment) REFERENCES conversion.destinations (id,environment),
    UNIQUE (id,lease_token),
    UNIQUE (id,confirmed),
    CHECK ((started_at IS NULL) = (sent_count IS NULL)),
    CHECK (started_at IS NULL OR (sent_count BETWEEN 1 AND 100 AND credential_id IS NOT NULL AND key_version IS NOT NULL)),
    CHECK ((receipt_at IS NULL) = (receipt IS NULL)),
    CHECK ((late_receipt_at IS NULL) = (late_receipt IS NULL)),
    CHECK (receipt IS NULL OR (started_at IS NOT NULL AND (receipt->>'Outcome' IS DISTINCT FROM 'accepted' OR confirmed)))
);
CREATE UNIQUE INDEX conversion_meta_batch_active_pixel ON conversion.meta_batches (destination_id) WHERE finished_at IS NULL;
CREATE INDEX conversion_meta_batch_expiry ON conversion.meta_batches (environment,lease_until,id) WHERE finished_at IS NULL;
CREATE INDEX conversion_meta_batch_started ON conversion.meta_batches (environment,started_at) WHERE started_at IS NOT NULL;

ALTER TABLE conversion.deliveries
    ADD COLUMN max_batch_size integer NOT NULL DEFAULT 100 CHECK (max_batch_size BETWEEN 1 AND 100);

ALTER TABLE conversion.delivery_attempts
    ADD COLUMN batch_id bigint,
    ADD COLUMN batch_position integer,
    ADD COLUMN batch_confirmed boolean GENERATED ALWAYS AS (
        CASE WHEN batch_id IS NOT NULL AND outcome='accepted' THEN true END
    ) STORED,
    ADD FOREIGN KEY (batch_id,lease_token) REFERENCES conversion.meta_batches (id,lease_token),
    ADD FOREIGN KEY (batch_id,batch_confirmed) REFERENCES conversion.meta_batches (id,confirmed),
    ADD UNIQUE (batch_id,delivery_id),
    ADD UNIQUE (batch_id,batch_position),
    ADD CHECK (batch_position IS NULL OR (batch_id IS NOT NULL AND batch_position BETWEEN 1 AND 100 AND sending_at IS NOT NULL)),
    ADD CHECK (batch_id IS NULL OR (events_received IS NULL AND (sending_at IS NULL) = (batch_position IS NULL)));

-- Locate the original per-event confirmation constraint by its definition.
DO $$
DECLARE old_constraint name;
BEGIN
    SELECT conname INTO STRICT old_constraint FROM pg_constraint
    WHERE conrelid='conversion.delivery_attempts'::regclass AND contype='c'
      AND pg_get_constraintdef(oid) LIKE '%events_received%'
      AND pg_get_constraintdef(oid) LIKE '%accepted%';
    EXECUTE format('ALTER TABLE conversion.delivery_attempts DROP CONSTRAINT %I',old_constraint);
END $$;
ALTER TABLE conversion.delivery_attempts ADD CONSTRAINT delivery_attempt_accepted_evidence CHECK (
    outcome IS DISTINCT FROM 'accepted' OR (
        sending_at IS NOT NULL AND http_status IS NOT DISTINCT FROM 200
        AND (batch_id IS NOT NULL OR events_received IS NOT DISTINCT FROM 1)
    )
);

CREATE INDEX conversion_delivery_pixel_ready ON conversion.deliveries (destination_id,next_attempt_at,id)
    WHERE state='pending' AND lease_until IS NULL;

INSERT INTO reader_schema_migrations(version) VALUES (2026111932);

ALTER TABLE conversion.destinations
    ADD COLUMN blocked_credential_id bigint REFERENCES conversion.credentials,
    ADD COLUMN nonpayment_claim_streak integer NOT NULL DEFAULT 0 CHECK (nonpayment_claim_streak BETWEEN 0 AND 4);

-- Restore known credential faults from the batch ledger on upgrade. A later
-- successful request with the same credential supersedes an older fault.
WITH credential_history AS (
    SELECT b.destination_id,b.credential_id,
        max(b.id) FILTER (WHERE b.confirmed) AS last_success,
        max(b.id) FILTER (
            WHERE COALESCE(b.receipt,b.late_receipt)->>'GraphCode' IN ('190','10','200')
              AND COALESCE(b.receipt,b.late_receipt)->>'HTTPStatus' ~ '^[45][0-9]{2}$'
        ) AS last_fault
    FROM conversion.meta_batches b JOIN conversion.destinations x
        ON x.id=b.destination_id AND x.credential_id=b.credential_id
    GROUP BY b.destination_id,b.credential_id
)
UPDATE conversion.destinations x
SET blocked_credential_id=h.credential_id
FROM credential_history h
WHERE x.id=h.destination_id AND h.last_fault>COALESCE(h.last_success,0);

ALTER TABLE conversion.inbox ADD COLUMN processed_at timestamptz;

-- Stamp both ingestion-time quarantine and worker processing in the same
-- transaction as the state change. Historical completion times stay unknown.
CREATE FUNCTION conversion.stamp_inbox_processed_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.state='pending' THEN
        NEW.processed_at := NULL;
    ELSIF TG_OP='INSERT' THEN
        NEW.processed_at := clock_timestamp();
    ELSIF OLD.state='pending' OR OLD.state<>NEW.state THEN
        NEW.processed_at := clock_timestamp();
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER conversion_inbox_processed_at
BEFORE INSERT OR UPDATE OF state ON conversion.inbox
FOR EACH ROW EXECUTE FUNCTION conversion.stamp_inbox_processed_at();

CREATE INDEX conversion_inbox_processed_at
    ON conversion.inbox (integration_id,processed_at) WHERE processed_at IS NOT NULL;

INSERT INTO reader_schema_migrations(version) VALUES (2026111933);

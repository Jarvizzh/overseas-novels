-- Order credential evidence by batch, independently of when a receipt arrives.
ALTER TABLE conversion.destinations ADD COLUMN credential_health_batch_id bigint;

-- Rebuild health for the current credential, including Pixels incorrectly
-- paused by a late error after a newer batch had already succeeded.
WITH evidence AS (
    SELECT b.destination_id,b.id,b.sent_count,COALESCE(b.receipt,b.late_receipt) AS receipt
    FROM conversion.meta_batches b
    JOIN conversion.destinations x ON x.id=b.destination_id AND x.credential_id=b.credential_id
    WHERE b.receipt IS NOT NULL OR b.late_receipt IS NOT NULL
), classified AS (
    SELECT destination_id,id,
        receipt->>'GraphCode' IN ('190','10','200') AND receipt->>'HTTPStatus' ~ '^[45][0-9]{2}$' AS failed,
        receipt->>'Outcome'='accepted' AND receipt->>'HTTPStatus'='200'
            AND receipt->>'GraphCode' IS NULL AND receipt->>'EventsReceived'=sent_count::text AS accepted
    FROM evidence
), latest AS (
    SELECT DISTINCT ON (destination_id) destination_id,id,failed
    FROM classified WHERE failed OR accepted ORDER BY destination_id,id DESC
)
UPDATE conversion.destinations x
SET credential_health_batch_id=latest.id,
    blocked_credential_id=CASE WHEN latest.failed THEN x.credential_id END
FROM latest WHERE x.id=latest.destination_id;

INSERT INTO reader_schema_migrations(version) VALUES (2026111934);

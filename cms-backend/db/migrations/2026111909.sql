-- Marker 1 identifies pre-versioning digests, which may use either field set.
ALTER TABLE conversion.inbox ADD COLUMN digest_version smallint NOT NULL DEFAULT 1
    CHECK (digest_version > 0);

-- Keep the attempt's identity after its report lease is cleared or replaced.
ALTER TABLE conversion.partner_report_attempts ADD COLUMN lease_token uuid;
UPDATE conversion.partner_report_attempts a SET lease_token=r.lease_token
FROM conversion.partner_reports r
WHERE r.lease_until IS NOT NULL AND a.report_id=r.id AND a.attempt_no=r.attempts;

INSERT INTO reader_schema_migrations (version) VALUES (2026111909);

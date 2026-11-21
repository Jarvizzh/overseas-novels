ALTER TABLE conversion.inbox ADD COLUMN source_order_id text;

CREATE INDEX conversion_inbox_integration_order
    ON conversion.inbox (integration_id, source_order_id, received_at DESC, id DESC)
    WHERE source_order_id IS NOT NULL;

INSERT INTO reader_schema_migrations (version) VALUES (2026111904);

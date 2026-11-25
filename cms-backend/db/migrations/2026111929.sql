ALTER TABLE payment_vip_checkout_intents
    ADD COLUMN switch_to_provider TEXT CHECK (
        switch_to_provider IS NULL OR (active_provider IS NOT NULL
            AND switch_to_provider IN ('paypal', 'stripe') AND switch_to_provider <> active_provider)
    );

INSERT INTO reader_schema_migrations(version) VALUES (2026111929);

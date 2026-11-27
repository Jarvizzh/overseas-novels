-- Relax fixed tax_liability_account CHECK constraints on Stripe catalog bindings and legacy offer tables.
-- Historical tax_liability_account columns and snapshots are preserved for compatibility and audit;
-- new bindings and checkout/VIP snapshots no longer require or pre-fill a fixed tax_liability_account.

DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conrelid::regclass AS tbl, conname
        FROM pg_constraint
        WHERE conrelid IN (
            'product_channel_bindings'::regclass,
            'payment_stripe_one_time_offers'::regclass,
            'payment_stripe_vip_offers'::regclass
        )
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%tax_liability_account%'
    LOOP
        EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', c.tbl, c.conname);
    END LOOP;
END $$;

INSERT INTO reader_schema_migrations(version) VALUES (2026111937)
ON CONFLICT (version) DO NOTHING;

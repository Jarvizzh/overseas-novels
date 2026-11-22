CREATE TABLE payment_channel_accounts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider TEXT NOT NULL CHECK (provider IN ('paypal', 'stripe')),
    environment TEXT NOT NULL CHECK (environment IN ('sandbox', 'live')),
    account_key TEXT NOT NULL CHECK (btrim(account_key) <> ''),
    credentials_ref TEXT NOT NULL CHECK (btrim(credentials_ref) <> ''),
    webhook_ref TEXT NOT NULL CHECK (btrim(webhook_ref) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, environment, account_key),
    UNIQUE (provider, id)
);

-- Use the existing PayPal application binding as the account key. The legacy
-- configuration resolver remains the source of credentials.
INSERT INTO payment_channel_accounts (provider, environment, account_key, credentials_ref, webhook_ref)
SELECT 'paypal', environment, client_id, 'legacy:paypal', 'env:PAYPAL_WEBHOOK_ID'
FROM paypal_identity_lock
WHERE client_id NOT IN ('', 'paypal_client_id_placeholder');

CREATE FUNCTION record_paypal_channel_account() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.client_id NOT IN ('', 'paypal_client_id_placeholder') THEN
        INSERT INTO payment_channel_accounts (provider, environment, account_key, credentials_ref, webhook_ref)
        VALUES ('paypal', NEW.environment, NEW.client_id, 'legacy:paypal', 'env:PAYPAL_WEBHOOK_ID')
        ON CONFLICT (provider, environment, account_key) DO NOTHING;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER record_paypal_channel_account
AFTER INSERT OR UPDATE OF client_id, environment ON paypal_identity_lock
FOR EACH ROW EXECUTE FUNCTION record_paypal_channel_account();

-- Carry over lease counters, due times, event IDs and completion evidence.
-- A historical webhook has no recorded verification account.
ALTER TABLE paypal_event_inbox RENAME TO payment_event_inbox;
ALTER TABLE payment_event_inbox
    DROP CONSTRAINT paypal_event_inbox_pkey,
    ADD COLUMN id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    ADD COLUMN provider TEXT NOT NULL DEFAULT 'paypal' CHECK (provider IN ('paypal', 'stripe')),
    ADD COLUMN account_id BIGINT,
    ADD CONSTRAINT payment_event_account FOREIGN KEY (provider, account_id)
        REFERENCES payment_channel_accounts (provider, id),
    ADD CONSTRAINT payment_event_identity UNIQUE NULLS NOT DISTINCT (provider, account_id, event_id),
    ADD CONSTRAINT payment_event_account_required CHECK (provider = 'paypal' OR account_id IS NOT NULL);

ALTER TABLE payment_event_inbox ALTER COLUMN provider DROP DEFAULT;

DROP INDEX idx_paypal_inbox_due;
CREATE INDEX idx_payment_inbox_due ON payment_event_inbox (provider, next_attempt_at, id)
    WHERE processed_at IS NULL;

INSERT INTO reader_schema_migrations (version) VALUES (2026111916);

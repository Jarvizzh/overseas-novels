-- Stripe single-purchase catalog mapping. A checkout copies the approved
-- terms before any provider write so reconfiguration cannot change a retry.
CREATE TABLE payment_stripe_one_time_offers (
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    slot_id INT NOT NULL REFERENCES recharge_slots(id) ON DELETE CASCADE,
    product_ref TEXT NOT NULL CHECK (product_ref ~ '^prod_[a-zA-Z0-9]+$'),
    price_ref TEXT NOT NULL CHECK (price_ref ~ '^price_[a-zA-Z0-9]+$'),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    allowed_countries TEXT[] NOT NULL CHECK (cardinality(allowed_countries)>0),
    enabled BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY(account_id,slot_id)
);

CREATE TABLE payment_stripe_checkout_terms (
    attempt_id UUID PRIMARY KEY REFERENCES payment_checkout_attempts(id),
    product_ref TEXT NOT NULL,
    price_ref TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor>0),
    allowed_countries TEXT[] NOT NULL,
    observed_country TEXT,
    country_checked_at TIMESTAMPTZ,
    ledger_observed_at TIMESTAMPTZ
);

CREATE INDEX idx_stripe_checkout_due ON payment_checkout_attempts(account_id,next_create_at,id)
    WHERE provider='stripe' AND status IN ('creating','buyer_action','capturing');
CREATE INDEX idx_stripe_checkout_inbox_due ON payment_event_inbox(account_id,next_attempt_at,id)
    WHERE provider='stripe' AND processed_at IS NULL;

CREATE TABLE payment_stripe_refund_items (
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    refund_ref TEXT NOT NULL,
    receipt_id BIGINT NOT NULL REFERENCES third_party_payment_orders(id),
    amount_minor BIGINT NOT NULL CHECK (amount_minor>0),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(account_id,refund_ref)
);
CREATE INDEX idx_stripe_refunds_receipt ON payment_stripe_refund_items(receipt_id);

INSERT INTO reader_schema_migrations(version) VALUES (2026111923);

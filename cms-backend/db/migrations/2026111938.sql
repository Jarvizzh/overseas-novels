-- P0-3 (Local currency payment & USD settlement) and P2-1 (Customer reuse & address simplification).
-- Idempotent migration adding presentment evidence, paid-pending-settlement state, USD settlement
-- breakdown columns, frozen attempt parameters, per-account feature toggles (default OFF), and
-- verified Stripe Customer mapping keyed by (user_id, account_id, environment).

ALTER TABLE payment_channel_accounts
    ADD COLUMN IF NOT EXISTS adaptive_pricing_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS saved_payment_method_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS simplified_address_collection_enabled BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE third_party_payment_orders
    ADD COLUMN IF NOT EXISTS presentment_currency TEXT,
    ADD COLUMN IF NOT EXISTS presentment_amount_minor BIGINT;

ALTER TABLE payment_stripe_checkout_terms
    ADD COLUMN IF NOT EXISTS presentment_currency TEXT,
    ADD COLUMN IF NOT EXISTS presentment_amount_minor BIGINT,
    ADD COLUMN IF NOT EXISTS settlement_state TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN IF NOT EXISTS settlement_retry_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS settlement_movement_ref TEXT,
    ADD COLUMN IF NOT EXISTS settlement_currency TEXT,
    ADD COLUMN IF NOT EXISTS settlement_gross_minor BIGINT,
    ADD COLUMN IF NOT EXISTS settlement_fee_minor BIGINT,
    ADD COLUMN IF NOT EXISTS settlement_withheld_tax_minor BIGINT,
    ADD COLUMN IF NOT EXISTS settlement_net_minor BIGINT,
    ADD COLUMN IF NOT EXISTS settlement_exchange_rate TEXT,
    ADD COLUMN IF NOT EXISTS frozen_checkout_params JSONB;

ALTER TABLE payment_stripe_subscription_intents
    ADD COLUMN IF NOT EXISTS frozen_checkout_params JSONB;

ALTER TABLE payment_stripe_invoice_periods
    ADD COLUMN IF NOT EXISTS settlement_state TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN IF NOT EXISTS settlement_retry_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS settlement_next_check_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS settlement_verified_at TIMESTAMPTZ;

ALTER TABLE payment_stripe_invoice_allocations
    ADD COLUMN IF NOT EXISTS presentment_currency TEXT,
    ADD COLUMN IF NOT EXISTS presentment_amount_minor BIGINT,
    ADD COLUMN IF NOT EXISTS settlement_movement_ref TEXT,
    ADD COLUMN IF NOT EXISTS settlement_currency TEXT,
    ADD COLUMN IF NOT EXISTS settlement_gross_minor BIGINT,
    ADD COLUMN IF NOT EXISTS settlement_fee_minor BIGINT,
    ADD COLUMN IF NOT EXISTS settlement_withheld_tax_minor BIGINT,
    ADD COLUMN IF NOT EXISTS settlement_net_minor BIGINT,
    ADD COLUMN IF NOT EXISTS settlement_exchange_rate TEXT;

CREATE INDEX IF NOT EXISTS idx_stripe_checkout_pending_settlement
    ON payment_stripe_checkout_terms(attempt_id)
    WHERE settlement_state = 'pending_settlement';

CREATE INDEX IF NOT EXISTS idx_stripe_vip_invoice_pending_settlement
    ON payment_stripe_invoice_periods(account_id, settlement_next_check_at, invoice_ref)
    WHERE settlement_state = 'pending_settlement';

CREATE TABLE IF NOT EXISTS payment_stripe_customers (
    user_id BIGINT NOT NULL REFERENCES users(id),
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    environment TEXT NOT NULL CHECK (environment IN ('sandbox', 'live')),
    stripe_customer_id TEXT NOT NULL CHECK (stripe_customer_id ~ '^cus_[a-zA-Z0-9_-]+$'),
    verified_billing_country TEXT,
    tax_evidence_verified BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, account_id, environment),
    UNIQUE (account_id, environment, stripe_customer_id)
);

INSERT INTO reader_schema_migrations(version) VALUES (2026111938)
ON CONFLICT (version) DO NOTHING;

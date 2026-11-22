CREATE TABLE payment_event_recovery (
    account_id BIGINT PRIMARY KEY REFERENCES payment_channel_accounts(id),
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL CHECK (window_end>window_start),
    page_cursor TEXT NOT NULL DEFAULT '',
    last_complete_at TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token UUID,
    lease_until TIMESTAMPTZ,
    last_error TEXT
);

ALTER TABLE payment_stripe_one_time_offers
    ADD COLUMN validated_catalog JSONB,
    ADD COLUMN release_evidence TEXT,
    ADD COLUMN market_policy TEXT;
ALTER TABLE payment_stripe_vip_offers
    ADD COLUMN validated_catalog JSONB,
    ADD COLUMN release_evidence TEXT,
    ADD COLUMN market_policy TEXT;
ALTER TABLE payment_stripe_checkout_terms
    ADD COLUMN validated_catalog JSONB,
    ADD COLUMN release_evidence TEXT,
    ADD COLUMN market_policy TEXT;
ALTER TABLE payment_stripe_subscription_intents
    ADD COLUMN validated_catalog JSONB,
    ADD COLUMN release_evidence TEXT,
    ADD COLUMN market_policy TEXT;

ALTER TABLE payment_stripe_checkout_terms
    ADD COLUMN expected_country TEXT,
    ADD COLUMN market_basis TEXT NOT NULL DEFAULT 'stripe_confirmation_required';
ALTER TABLE payment_stripe_subscription_intents
    ADD COLUMN expected_country TEXT,
    ADD COLUMN market_basis TEXT NOT NULL DEFAULT 'stripe_confirmation_required',
    ADD COLUMN provider_observed_at TIMESTAMPTZ;

ALTER TABLE payment_stripe_invoice_periods
    ADD COLUMN provider_status TEXT,
    ADD COLUMN revises_invoice_ref TEXT,
    ADD COLUMN revision_verified BOOLEAN NOT NULL DEFAULT false;

CREATE FUNCTION payment_last_stripe_billing_country(target_account bigint,target_user bigint)
RETURNS text LANGUAGE sql STABLE AS $$
    SELECT t.payer_country FROM third_party_payment_orders t
    JOIN recharge_orders o ON o.id=t.order_id
    JOIN payment_fulfillment_funding f ON f.order_id=o.id AND f.receipt_id=t.id
    WHERE t.account_id=target_account AND o.user_id=target_user AND t.payment_provider='stripe'
      AND o.fulfilled_at IS NOT NULL AND t.status='VERIFIED' AND t.payer_country ~ '^[A-Z]{2}$'
    ORDER BY o.paid_at DESC NULLS LAST,t.id DESC LIMIT 1
$$;

CREATE TABLE payment_stripe_market_reviews (
    order_id BIGINT NOT NULL REFERENCES recharge_orders(id),
    evidence_key TEXT NOT NULL,
    evidence JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    approved_by TEXT REFERENCES admins(id),
    approval_reason TEXT,
    approved_at TIMESTAMPTZ,
    PRIMARY KEY (order_id,evidence_key)
);
CREATE INDEX idx_stripe_market_review_recent ON payment_stripe_market_reviews(order_id,updated_at DESC);

CREATE TABLE payment_settlement_commands (
    id UUID PRIMARY KEY,
    request_key UUID NOT NULL,
    actor_id TEXT NOT NULL REFERENCES admins(id),
    kind TEXT NOT NULL CHECK (kind IN ('paypal_report','stripe_payout','stripe_report','bank_statement','bank_match')),
    account_id BIGINT REFERENCES payment_channel_accounts(id),
    environment TEXT NOT NULL CHECK (environment IN ('sandbox','live')),
    reason TEXT NOT NULL,
    parameters JSONB NOT NULL,
    upload BYTEA,
    request_digest TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','succeeded','failed')),
    result JSONB,
    error TEXT,
    lease_token UUID,
    lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (actor_id,request_key)
);
CREATE INDEX idx_payment_settlement_commands_due ON payment_settlement_commands(environment,created_at)
    WHERE status IN ('pending','running');

CREATE TABLE payment_ledger_sync_settings (
    account_id BIGINT PRIMARY KEY REFERENCES payment_channel_accounts(id),
    enabled BOOLEAN NOT NULL DEFAULT false,
    actor_id TEXT NOT NULL REFERENCES admins(id),
    reason TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL CHECK (window_end>window_start),
    page_cursor TEXT NOT NULL DEFAULT '',
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_complete_at TIMESTAMPTZ,
    last_error TEXT,
    lease_token UUID,
    lease_until TIMESTAMPTZ
);

INSERT INTO reader_schema_migrations(version) VALUES(2026111925);

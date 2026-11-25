-- Reader appends versioned checkout rule outcomes in the reservation transaction.
CREATE TABLE payment_checkout_decisions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    checkout_environment TEXT NOT NULL CHECK (btrim(checkout_environment) <> ''),
    stage TEXT NOT NULL CHECK (stage IN ('purchase_intent','payment_attempt','subscription_intent')),
    provider TEXT CHECK (provider IN ('paypal','stripe')),
    account_id BIGINT,
    order_id BIGINT REFERENCES recharge_orders(id) ON DELETE SET NULL,
    checkout_id UUID,
    slot_id INTEGER CHECK (slot_id > 0),
    outcome TEXT NOT NULL CHECK (outcome IN ('allow','block','verify','review')),
    reason TEXT NOT NULL CHECK (btrim(reason) <> ''),
    rule_version INTEGER NOT NULL CHECK (rule_version > 0),
    evaluated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (provider,account_id) REFERENCES payment_channel_accounts(provider,id),
    CHECK (account_id IS NULL OR provider IS NOT NULL)
);

CREATE INDEX idx_checkout_decisions_user ON payment_checkout_decisions(user_id,id DESC);
CREATE INDEX idx_checkout_decisions_order ON payment_checkout_decisions(order_id,id DESC) WHERE order_id IS NOT NULL;
CREATE INDEX idx_checkout_decisions_review ON payment_checkout_decisions(checkout_environment,id DESC) WHERE outcome IN ('block','verify','review');

-- Public confirmation codes are separate from the attempt lifecycle and funds.
ALTER TABLE payment_checkout_attempts ADD COLUMN capture_outcome TEXT
    CHECK (capture_outcome IN ('payment_blocked','payment_declined','payment_action_required',
        'payment_unavailable','payment_unconfirmed'));

INSERT INTO reader_schema_migrations (version) VALUES (2026111921);

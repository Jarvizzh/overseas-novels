-- CMS recharge slot IDs identify immutable versions for VIP. Versions outlive
-- the current template so existing intents and agreements remain resolvable.
CREATE TABLE payment_vip_offer_versions (
    slot_id INT PRIMARY KEY,
    template_id INT NOT NULL,
    slot_index INT NOT NULL,
    amount_minor INT NOT NULL CHECK (amount_minor > 0),
    currency VARCHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    cycle TEXT NOT NULL CHECK (cycle IN ('day', 'week', 'month')),
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    UNIQUE (template_id, slot_index, amount_minor, currency, cycle, name, description)
);

CREATE TABLE payment_paypal_vip_plans (
    slot_id INT NOT NULL REFERENCES payment_vip_offer_versions(slot_id),
    provider TEXT NOT NULL DEFAULT 'paypal' CHECK (provider = 'paypal'),
    account_id BIGINT NOT NULL,
    request_key UUID NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    plan_id TEXT CHECK (btrim(plan_id) <> ''),
    first_sent_at TIMESTAMPTZ,
    review_required BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (slot_id, account_id),
    FOREIGN KEY (provider, account_id) REFERENCES payment_channel_accounts(provider, id),
    CHECK (plan_id IS NULL OR NOT review_required)
);

CREATE INDEX idx_paypal_vip_plan_pending ON payment_paypal_vip_plans(account_id, slot_id)
    WHERE plan_id IS NULL AND first_sent_at IS NULL AND NOT review_required;

-- The current template editor publishes by replacing its live slots. Keep
-- the exact terms of each new VIP slot in the same transaction as that save.
ALTER TABLE recharge_slots
    ADD COLUMN vip_offer_version_id INT REFERENCES payment_vip_offer_versions(slot_id);
CREATE INDEX idx_live_vip_offer_version ON recharge_slots(vip_offer_version_id) WHERE vip_offer_version_id IS NOT NULL;

CREATE FUNCTION snapshot_vip_offer(s recharge_slots) RETURNS INT LANGUAGE sql AS $$
    INSERT INTO payment_vip_offer_versions (slot_id, template_id, slot_index, amount_minor, currency, cycle, name, description)
    SELECT s.id, s.template_id, s.slot_index, s.price_cents, 'USD', offer.cycle,
        COALESCE(NULLIF(s.vip_name, ''), 'Star Novel VIP ' || initcap(offer.cycle)),
        COALESCE(NULLIF(s.vip_desc, ''), 'Unlimited reading access for VIP members')
    FROM (SELECT COALESCE(NULLIF(s.subscription_cycle, ''), NULLIF(s.vip_duration, ''), 'month') AS cycle) offer
    WHERE s.type IN ('vip', 'subscription') AND s.price_cents > 0 AND offer.cycle IN ('day', 'week', 'month')
    ON CONFLICT (template_id, slot_index, amount_minor, currency, cycle, name, description)
        DO UPDATE SET name = EXCLUDED.name
    RETURNING slot_id
$$;

UPDATE recharge_slots s SET vip_offer_version_id = snapshot_vip_offer(s) WHERE s.type IN ('vip', 'subscription');

INSERT INTO payment_paypal_vip_plans (slot_id, account_id, plan_id)
SELECT v.slot_id, a.id, p.external_plan_id
FROM payment_vip_offer_versions v
JOIN payment_provider_plans p ON p.provider = 'paypal' AND p.slot_id = v.slot_id
    AND p.price_cents = v.amount_minor AND p.currency = v.currency AND p.cycle = v.cycle
    AND p.status = 'ACTIVE' AND btrim(p.external_plan_id) <> ''
JOIN payment_channel_accounts a ON a.provider = p.provider
    AND a.account_key = p.identity_client_id AND a.environment = p.identity_environment;

ALTER TABLE paypal_subscription_intents
    ADD COLUMN vip_offer_version_id INT REFERENCES payment_vip_offer_versions(slot_id);
ALTER TABLE user_subscriptions
    ADD COLUMN vip_offer_version_id INT REFERENCES payment_vip_offer_versions(slot_id);

INSERT INTO reader_schema_migrations (version) VALUES (2026111918);

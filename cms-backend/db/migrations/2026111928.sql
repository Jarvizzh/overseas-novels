-- Phase 3.6 (CFG-04) & Phase 3.7 (CAT-01 ~ CAT-04):
-- Channel account lifecycle + encrypted credentials, immutable product catalog,
-- dual-channel bindings, template slot decoupling, VIP annual cycle & unified VIP intents.

-- 1. Extend payment_channel_accounts for full CFG-04 lifecycle & per-account webhook routing
ALTER TABLE payment_channel_accounts
    ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS merchant_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'draft',
    ADD COLUMN IF NOT EXISTS is_primary BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS active_version INT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS pending_version INT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS webhook_token UUID NOT NULL DEFAULT gen_random_uuid(),
    ADD COLUMN IF NOT EXISTS last_validated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS validation_summary JSONB,
    ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS published_by VARCHAR(36) REFERENCES admins(id),
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE payment_channel_accounts
SET display_name = COALESCE(NULLIF(display_name, ''), account_key),
    status = CASE
        WHEN new_orders_enabled OR new_subscriptions_enabled THEN 'published'
        WHEN credentials_ref LIKE 'legacy:%' OR credentials_ref LIKE 'env:%' THEN 'verified'
        ELSE 'draft'
    END,
    published_at = CASE
        WHEN new_orders_enabled OR new_subscriptions_enabled THEN COALESCE(published_at, created_at, now())
        ELSE published_at
    END;

WITH ranked_published AS (
    SELECT id,
           row_number() OVER (PARTITION BY provider, environment ORDER BY id ASC) AS rn
    FROM payment_channel_accounts
    WHERE status = 'published'
)
UPDATE payment_channel_accounts a
SET is_primary = true
FROM ranked_published r
WHERE a.id = r.id AND r.rn = 1;

DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname FROM pg_constraint
        WHERE conrelid = 'payment_channel_accounts'::regclass
          AND conname IN ('chk_channel_status', 'chk_channel_primary_published', 'chk_channel_switches_published')
    LOOP
        EXECUTE format('ALTER TABLE payment_channel_accounts DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;

ALTER TABLE payment_channel_accounts
    ALTER COLUMN new_orders_enabled SET DEFAULT false,
    ALTER COLUMN new_subscriptions_enabled SET DEFAULT false,
    ADD CONSTRAINT chk_channel_status CHECK (status IN ('draft', 'verified', 'published', 'disabled')),
    ADD CONSTRAINT chk_channel_primary_published CHECK ((NOT is_primary) OR status = 'published'),
    ADD CONSTRAINT chk_channel_switches_published CHECK ((NOT new_orders_enabled AND NOT new_subscriptions_enabled) OR status = 'published');

CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_channel_accounts_single_primary
    ON payment_channel_accounts (provider, environment)
    WHERE is_primary = true;

CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_channel_accounts_webhook_token
    ON payment_channel_accounts (webhook_token);

CREATE TABLE IF NOT EXISTS payment_channel_account_versions (
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id) ON DELETE CASCADE,
    version INT NOT NULL CHECK (version > 0),
    display_name TEXT NOT NULL DEFAULT '',
    merchant_id TEXT NOT NULL DEFAULT '',
    account_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'verified', 'published', 'superseded')),
    validation_status TEXT NOT NULL DEFAULT 'pending' CHECK (validation_status IN ('pending', 'verified', 'failed')),
    validation_summary JSONB,
    validated_at TIMESTAMPTZ,
    created_by VARCHAR(36) REFERENCES admins(id),
    published_by VARCHAR(36) REFERENCES admins(id),
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, version)
);

INSERT INTO payment_channel_account_versions (
    account_id, version, display_name, merchant_id, account_key, status, validation_status,
    validated_at, published_at, created_at, updated_at
)
SELECT id, active_version, display_name, merchant_id, account_key,
       CASE WHEN status = 'published' THEN 'published'
            WHEN status = 'verified' THEN 'verified'
            ELSE 'draft' END,
       CASE WHEN status IN ('published', 'verified') THEN 'verified' ELSE 'pending' END,
       last_validated_at, published_at, created_at, updated_at
FROM payment_channel_accounts
ON CONFLICT (account_id, version) DO NOTHING;

CREATE TABLE IF NOT EXISTS payment_channel_credentials (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL,
    version INT NOT NULL,
    purpose TEXT NOT NULL CHECK (purpose IN (
        'client_id', 'api_secret', 'webhook_id', 'previous_webhook_id',
        'api_key', 'webhook_secret', 'previous_webhook_secret'
    )),
    key_version TEXT NOT NULL,
    ciphertext TEXT NOT NULL,
    hint TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ,
    created_by VARCHAR(36) REFERENCES admins(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (account_id, version, purpose),
    FOREIGN KEY (account_id, version) REFERENCES payment_channel_account_versions(account_id, version) ON DELETE CASCADE
);

-- 2. Immutable Unified Product Catalog & Environment Publication (CAT-01)
CREATE TABLE IF NOT EXISTS products (
    id BIGSERIAL PRIMARY KEY,
    code TEXT NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    source_product_id BIGINT REFERENCES products(id),
    product_type TEXT NOT NULL CHECK (product_type IN ('single', 'whole_book', 'vip')),
    name TEXT NOT NULL CHECK (btrim(name) <> ''),
    description TEXT NOT NULL DEFAULT '',
    price_cents INT NOT NULL CHECK (price_cents > 0),
    currency TEXT NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
    coins INT NOT NULL DEFAULT 0 CHECK (coins >= 0),
    bonus INT NOT NULL DEFAULT 0 CHECK (bonus >= 0),
    vip_cycle TEXT CHECK (vip_cycle IS NULL OR vip_cycle IN ('day', 'week', 'month', 'year')),
    novel_scope_type TEXT NOT NULL DEFAULT 'none' CHECK (novel_scope_type IN ('none', 'all_published', 'promoted_novel', 'specific_novels')),
    novel_scope_ids BIGINT[] NOT NULL DEFAULT '{}',
    created_by VARCHAR(36) REFERENCES admins(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_products_single_rules CHECK (
        product_type <> 'single' OR (
            coins > 0 AND bonus * 10 <= coins * 15 AND vip_cycle IS NULL
            AND novel_scope_type = 'none' AND cardinality(novel_scope_ids) = 0
        )
    ),
    CONSTRAINT chk_products_whole_book_rules CHECK (
        product_type <> 'whole_book' OR (
            coins = 0 AND bonus = 0 AND vip_cycle IS NULL
            AND novel_scope_type IN ('all_published', 'promoted_novel', 'specific_novels')
            AND (
                (novel_scope_type = 'specific_novels' AND cardinality(novel_scope_ids) > 0) OR
                (novel_scope_type <> 'specific_novels' AND cardinality(novel_scope_ids) = 0)
            )
        )
    ),
    CONSTRAINT chk_products_vip_rules CHECK (
        product_type <> 'vip' OR (
            coins = 0 AND bonus = 0 AND vip_cycle IN ('day', 'week', 'month', 'year')
            AND novel_scope_type = 'none' AND cardinality(novel_scope_ids) = 0
        )
    )
);

CREATE OR REPLACE FUNCTION forbid_product_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'products rows are immutable; create a copied version instead';
END;
$$;

DROP TRIGGER IF EXISTS trg_products_immutable ON products;
CREATE TRIGGER trg_products_immutable
    BEFORE UPDATE OR DELETE ON products
    FOR EACH ROW EXECUTE FUNCTION forbid_product_mutation();

CREATE TABLE IF NOT EXISTS product_publications (
    product_id BIGINT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    environment TEXT NOT NULL CHECK (environment IN ('sandbox', 'live')),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'paused', 'retired')),
    published_at TIMESTAMPTZ,
    published_by VARCHAR(36) REFERENCES admins(id),
    paused_at TIMESTAMPTZ,
    retired_at TIMESTAMPTZ,
    updated_by VARCHAR(36) REFERENCES admins(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (product_id, environment)
);

CREATE INDEX IF NOT EXISTS idx_product_publications_env_status
    ON product_publications (environment, status, product_id);

-- 3. Dual-Channel Product Bindings (CAT-02)
CREATE TABLE IF NOT EXISTS product_channel_bindings (
    id BIGSERIAL PRIMARY KEY,
    product_id BIGINT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL CHECK (provider IN ('paypal', 'stripe')),
    environment TEXT NOT NULL CHECK (environment IN ('sandbox', 'live')),
    external_product_ref TEXT NOT NULL DEFAULT '',
    external_price_ref TEXT NOT NULL DEFAULT '',
    request_key UUID NOT NULL DEFAULT gen_random_uuid(),
    first_sent_at TIMESTAMPTZ,
    review_required BOOLEAN NOT NULL DEFAULT false,
    sales_mode TEXT NOT NULL DEFAULT 'direct' CHECK (sales_mode IN ('direct', 'merchant_of_record')),
    allowed_countries TEXT[] NOT NULL DEFAULT ARRAY['US']::text[] CHECK (cardinality(allowed_countries) > 0),
    tax_liability_account TEXT,
    validated_catalog JSONB,
    release_evidence TEXT,
    market_policy TEXT,
    validation_status TEXT NOT NULL DEFAULT 'pending' CHECK (validation_status IN ('pending', 'verified', 'failed', 'review')),
    validation_error TEXT,
    validated_at TIMESTAMPTZ,
    enabled BOOLEAN NOT NULL DEFAULT false,
    created_by VARCHAR(36) REFERENCES admins(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by VARCHAR(36) REFERENCES admins(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (product_id, account_id),
    FOREIGN KEY (provider, account_id) REFERENCES payment_channel_accounts(provider, id),
    CONSTRAINT chk_binding_tax_liability CHECK (
        (provider <> 'stripe') OR
        (sales_mode = 'direct' AND tax_liability_account IS NULL) OR
        (sales_mode = 'merchant_of_record' AND tax_liability_account ~ '^acct_[a-zA-Z0-9]+$')
    )
);

CREATE INDEX IF NOT EXISTS idx_product_channel_bindings_lookup
    ON product_channel_bindings (environment, provider, product_id, enabled, validation_status);

-- 4. Update VIP cycle CHECK constraints to support 'year' alongside day/week/month
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conrelid::regclass AS tbl, conname
        FROM pg_constraint
        WHERE conrelid IN ('payment_vip_offer_versions'::regclass, 'payment_stripe_subscription_intents'::regclass)
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%cycle%'
    LOOP
        EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', c.tbl, c.conname);
    END LOOP;
END $$;

ALTER TABLE payment_vip_offer_versions
    ADD CONSTRAINT payment_vip_offer_versions_cycle_check CHECK (cycle IN ('day', 'week', 'month', 'year'));

ALTER TABLE payment_stripe_subscription_intents
    ADD CONSTRAINT payment_stripe_subscription_intents_cycle_check CHECK (cycle IN ('day', 'week', 'month', 'year')),
    ALTER COLUMN offer_version_id DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS product_id BIGINT REFERENCES products(id),
    ADD COLUMN IF NOT EXISTS product_snapshot JSONB;

-- 5. Decouple recharge_slots from commercial pricing; seed new clean default products + template #1 (CAT-03)
ALTER TABLE recharge_slots
    ADD COLUMN IF NOT EXISTS product_id BIGINT REFERENCES products(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS badge TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS is_highlighted BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS display_alias TEXT NOT NULL DEFAULT '';

ALTER TABLE recharge_slots
    ALTER COLUMN type DROP NOT NULL,
    ALTER COLUMN price DROP NOT NULL,
    ALTER COLUMN price_cents DROP NOT NULL,
    ALTER COLUMN type SET DEFAULT '',
    ALTER COLUMN price SET DEFAULT '',
    ALTER COLUMN price_cents SET DEFAULT 0;

-- Reset template #1 legacy slots per clean catalog rollout and install the 8 standard immutable products
DELETE FROM recharge_slots WHERE template_id = 1;

INSERT INTO products (
    code, product_type, name, description, price_cents, currency, coins, bonus, vip_cycle, novel_scope_type, novel_scope_ids
) VALUES
    ('COIN_STARTER_499', 'single', 'Starter Coins Pack', '499 Coins + 50 Bonus Coins', 499, 'USD', 499, 50, NULL, 'none', '{}'),
    ('COIN_POPULAR_999', 'single', 'Reader Choice Coins Pack', '999 Coins + 150 Bonus Coins', 999, 'USD', 999, 150, NULL, 'none', '{}'),
    ('COIN_VALUE_1999', 'single', 'Value Bundle Coins Pack', '1999 Coins + 400 Bonus Coins', 1999, 'USD', 1999, 400, NULL, 'none', '{}'),
    ('WHOLE_BOOK_999', 'whole_book', 'Full Book Unlock Pass', 'Permanent full access to the selected novel', 999, 'USD', 0, 0, NULL, 'all_published', '{}'),
    ('VIP_DAY_99', 'vip', 'VIP Daily Pass', 'Unlimited reading across all titles for 24 hours, renews daily', 99, 'USD', 0, 0, 'day', 'none', '{}'),
    ('VIP_WEEK_499', 'vip', 'VIP Weekly Pass', 'Unlimited reading across all titles for 7 days, renews weekly', 499, 'USD', 0, 0, 'week', 'none', '{}'),
    ('VIP_MONTH_1499', 'vip', 'VIP Monthly Pass', 'Unlimited reading across all titles for 30 days, renews monthly', 1499, 'USD', 0, 0, 'month', 'none', '{}'),
    ('VIP_YEAR_9999', 'vip', 'VIP Annual Pass', 'Unlimited reading across all titles for 365 days, renews annually', 9999, 'USD', 0, 0, 'year', 'none', '{}')
ON CONFLICT (code) DO NOTHING;

INSERT INTO product_publications (product_id, environment, status)
SELECT p.id, env.environment, 'draft'
FROM products p
CROSS JOIN (VALUES ('sandbox'), ('live')) AS env(environment)
WHERE p.code IN (
    'COIN_STARTER_499', 'COIN_POPULAR_999', 'COIN_VALUE_1999',
    'WHOLE_BOOK_999', 'VIP_DAY_99', 'VIP_WEEK_499', 'VIP_MONTH_1499', 'VIP_YEAR_9999'
)
ON CONFLICT (product_id, environment) DO NOTHING;

DO $$
DECLARE
    r RECORD;
    arch_id BIGINT;
    arch_type TEXT;
    arch_cycle TEXT;
    arch_scope TEXT;
    arch_coins INT;
    arch_bonus INT;
    arch_price INT;
BEGIN
    IF EXISTS (SELECT 1 FROM recharge_templates WHERE id = 1) THEN
        INSERT INTO recharge_slots (template_id, slot_index, product_id, badge, is_highlighted, display_alias)
        SELECT 1, v.slot_index, p.id, v.badge, v.is_highlighted, v.display_alias
        FROM (VALUES
            (1, 'COIN_STARTER_499', '', false, 'Starter Pack'),
            (2, 'COIN_POPULAR_999', 'POPULAR', true, 'Reader''s Choice'),
            (3, 'COIN_VALUE_1999', 'BEST VALUE', false, 'Value Bundle'),
            (4, 'WHOLE_BOOK_999', 'FULL BOOK', false, 'Unlock This Book'),
            (5, 'VIP_DAY_99', '', false, 'Daily Pass'),
            (6, 'VIP_WEEK_499', 'HOT', false, 'Weekly Pass'),
            (7, 'VIP_MONTH_1499', 'RECOMMENDED', false, 'Monthly VIP'),
            (8, 'VIP_YEAR_9999', 'SAVE 45%', false, 'Annual VIP')
        ) AS v(slot_index, code, badge, is_highlighted, display_alias)
        JOIN products p ON p.code = v.code
        ON CONFLICT (template_id, slot_index) DO NOTHING;
    END IF;

    -- Archive any pre-existing non-default upgrade slots (e.g. historical migration fixtures) as retired products
    -- so recharge_slots.product_id NOT NULL invariant can be strictly enforced.
    FOR r IN SELECT * FROM recharge_slots WHERE product_id IS NULL LOOP
        arch_type := CASE WHEN r.type IN ('vip', 'subscription') THEN 'vip'
                          WHEN r.type = 'whole_book' THEN 'whole_book'
                          ELSE 'single' END;
        arch_price := GREATEST(COALESCE(r.price_cents, 0), 99);
        arch_cycle := NULL;
        arch_scope := 'none';
        arch_coins := GREATEST(COALESCE(r.coins, 0), 0);
        arch_bonus := GREATEST(COALESCE(r.bonus, 0), 0);
        IF arch_type = 'vip' THEN
            arch_coins := 0;
            arch_bonus := 0;
            arch_cycle := CASE WHEN COALESCE(NULLIF(r.subscription_cycle, ''), NULLIF(r.vip_duration, ''), 'month') IN ('day', 'week', 'month', 'year')
                               THEN COALESCE(NULLIF(r.subscription_cycle, ''), NULLIF(r.vip_duration, ''), 'month')
                               ELSE 'month' END;
        ELSIF arch_type = 'whole_book' THEN
            arch_coins := 0;
            arch_bonus := 0;
            arch_scope := 'all_published';
        ELSE
            IF arch_coins <= 0 THEN arch_coins := arch_price; END IF;
            IF arch_bonus * 10 > arch_coins * 15 THEN arch_bonus := (arch_coins * 15) / 10; END IF;
        END IF;

        INSERT INTO products (
            code, product_type, name, description, price_cents, currency, coins, bonus, vip_cycle, novel_scope_type, novel_scope_ids
        ) VALUES (
            'LEGACY_ARCHIVED_' || r.id, arch_type,
            COALESCE(NULLIF(r.vip_name, ''), 'Archived Slot #' || r.id),
            COALESCE(r.vip_desc, ''),
            arch_price, 'USD', arch_coins, arch_bonus, arch_cycle, arch_scope, '{}'
        ) ON CONFLICT (code) DO NOTHING;

        SELECT id INTO STRICT arch_id FROM products WHERE code = 'LEGACY_ARCHIVED_' || r.id;

        INSERT INTO product_publications (product_id, environment, status, retired_at)
        VALUES (arch_id, 'sandbox', 'retired', now()), (arch_id, 'live', 'retired', now())
        ON CONFLICT (product_id, environment) DO NOTHING;

        UPDATE recharge_slots SET product_id = arch_id WHERE id = r.id;
    END LOOP;
END $$;

ALTER TABLE recharge_slots
    ALTER COLUMN product_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_recharge_slots_product ON recharge_slots(product_id);

-- 6. Trade & Subscription Traceability + Unified VIP Checkout Intent + Shortfall Audit (CAT-04)
ALTER TABLE recharge_orders
    ADD COLUMN IF NOT EXISTS product_id BIGINT REFERENCES products(id),
    ADD COLUMN IF NOT EXISTS product_snapshot JSONB;

ALTER TABLE paypal_subscription_intents
    ADD COLUMN IF NOT EXISTS product_id BIGINT REFERENCES products(id),
    ADD COLUMN IF NOT EXISTS product_snapshot JSONB;

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS product_id BIGINT REFERENCES products(id),
    ADD COLUMN IF NOT EXISTS product_snapshot JSONB;

CREATE TABLE IF NOT EXISTS payment_vip_checkout_intents (
    intent_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    product_id BIGINT NOT NULL REFERENCES products(id),
    slot_id INT NOT NULL DEFAULT 0,
    template_id INT NOT NULL DEFAULT 0,
    price_cents INT NOT NULL CHECK (price_cents > 0),
    currency TEXT NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
    cycle TEXT NOT NULL CHECK (cycle IN ('day', 'week', 'month', 'year')),
    checkout_environment TEXT NOT NULL CHECK (checkout_environment IN ('sandbox', 'production')),
    product_snapshot JSONB NOT NULL,
    return_path TEXT NOT NULL DEFAULT '',
    active_provider TEXT CHECK (active_provider IS NULL OR active_provider IN ('paypal', 'stripe')),
    paypal_intent_id UUID,
    stripe_intent_id UUID,
    status TEXT NOT NULL DEFAULT 'Pending' CHECK (status IN ('Pending', 'Completed', 'Canceled', 'Expired')),
    subscription_id VARCHAR(100),
    request_key UUID,
    attribution JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_vip_checkout_intents_pending_user_env
    ON payment_vip_checkout_intents (user_id, checkout_environment)
    WHERE status = 'Pending';

CREATE UNIQUE INDEX IF NOT EXISTS idx_vip_checkout_intents_req
    ON payment_vip_checkout_intents (checkout_environment, user_id, request_key)
    WHERE request_key IS NOT NULL;

ALTER TABLE payment_checkout_requests
    ADD COLUMN IF NOT EXISTS product_id BIGINT REFERENCES products(id),
    ADD COLUMN IF NOT EXISTS vip_intent_id UUID REFERENCES payment_vip_checkout_intents(intent_id) ON DELETE CASCADE,
    ALTER COLUMN order_id DROP NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'payment_checkout_requests'::regclass AND conname = 'chk_checkout_request_target'
    ) THEN
        ALTER TABLE payment_checkout_requests
            ADD CONSTRAINT chk_checkout_request_target CHECK ((order_id IS NOT NULL) <> (vip_intent_id IS NOT NULL));
    END IF;
END $$;

ALTER TABLE payment_entitlement_adjustments
    ADD COLUMN IF NOT EXISTS charged_coin_shortfall INT NOT NULL DEFAULT 0 CHECK (charged_coin_shortfall >= 0),
    ADD COLUMN IF NOT EXISTS bonus_coin_shortfall INT NOT NULL DEFAULT 0 CHECK (bonus_coin_shortfall >= 0);

INSERT INTO reader_schema_migrations(version) VALUES (2026111928)
ON CONFLICT (version) DO NOTHING;

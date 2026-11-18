-- =========================================================================
-- STAR NOVEL - 生产级统一数据库表结构 (Unified Database Schema)
-- 包含：用户/鉴权、小说/章节、钱包/交易、充值/模板、投放/像素、反馈等
-- 安全原则：全部采用 IF NOT EXISTS / ADD COLUMN IF NOT EXISTS，严禁 DROP TABLE
-- =========================================================================

-- 1. 扩展与全局序列
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE SEQUENCE IF NOT EXISTS novels_id_seq START WITH 10000001;
CREATE SEQUENCE IF NOT EXISTS recharge_orders_id_seq START WITH 10000;

-- 2. 系统全局配置表 (System Configs)
CREATE TABLE IF NOT EXISTS system_configs (
    key VARCHAR(100) PRIMARY KEY,
    value VARCHAR(255) NOT NULL
);

-- 3. 管理员表 (Admins for CMS)
CREATE TABLE IF NOT EXISTS admins (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(50) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    nickname VARCHAR(50),
    role VARCHAR(20) NOT NULL, -- SuperAdmin, Editor, Support, Finance
    status SMALLINT DEFAULT 1, -- 1-Active, 2-Suspended
    tfa_secret VARCHAR(100),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 4. 管理员操作审计日志表 (Admin Audit Logs)
CREATE TABLE IF NOT EXISTS admin_audit_logs (
    id BIGSERIAL PRIMARY KEY,
    admin_id VARCHAR(36) REFERENCES admins(id) ON DELETE SET NULL,
    action VARCHAR(50) NOT NULL,
    target_id VARCHAR(100),
    before_data JSONB,
    after_data JSONB,
    ip_address VARCHAR(45),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 5. 读者用户表 (Users)
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    email VARCHAR(100) UNIQUE,
    password_hash VARCHAR(255),
    nickname VARCHAR(50),
    avatar_url VARCHAR(255),
    status SMALLINT DEFAULT 1, -- 1-Normal, 2-Banned
    device VARCHAR(255) DEFAULT '',
    ip_address VARCHAR(45) DEFAULT '',
    utm_source VARCHAR(100) DEFAULT '',
    utm_campaign VARCHAR(100) DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 6. 小说主表 (Novels)
CREATE TABLE IF NOT EXISTS novels (
    id INT PRIMARY KEY DEFAULT nextval('novels_id_seq'),
    title VARCHAR(150) NOT NULL,
    author VARCHAR(100) NOT NULL,
    cover_url VARCHAR(255),
    rating DECIMAL(2,1) DEFAULT 0.0,
    status VARCHAR(20) DEFAULT 'Ongoing', -- Ongoing / Completed / archived
    publish_status VARCHAR(20) NOT NULL DEFAULT 'published', -- draft / published / offline
    synopsis TEXT,
    genres VARCHAR(2048)[] DEFAULT '{}',
    word_count INT DEFAULT 0,
    view_count BIGINT DEFAULT 0,
    coin_cost_per_thousand INT DEFAULT 500,
    start_pay_chapter_index INT DEFAULT 3,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 增量补齐字段
ALTER TABLE novels ADD COLUMN IF NOT EXISTS coin_cost_per_thousand INT DEFAULT 500;
ALTER TABLE novels ADD COLUMN IF NOT EXISTS start_pay_chapter_index INT DEFAULT 3;
ALTER TABLE novels ADD COLUMN IF NOT EXISTS publish_status VARCHAR(20) NOT NULL DEFAULT 'published';
ALTER TABLE novels ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP;
UPDATE novels SET updated_at = COALESCE(created_at, CURRENT_TIMESTAMP) WHERE updated_at IS NULL;
UPDATE novels SET publish_status = 'offline' WHERE status = 'archived' AND publish_status != 'offline';
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_attribute a
        JOIN pg_class c ON a.attrelid = c.oid
        JOIN pg_namespace n ON c.relnamespace = n.oid
        WHERE n.nspname = current_schema()
          AND c.relname = 'novels'
          AND a.attname = 'genres'
          AND NOT a.attisdropped
          AND a.atttypmod > 0
          AND a.atttypmod < 2052
    ) THEN
        ALTER TABLE novels ALTER COLUMN genres TYPE VARCHAR(2048)[] USING genres::VARCHAR(2048)[];
    END IF;
END $$;

-- 7. 章节表 (Chapters)
CREATE TABLE IF NOT EXISTS chapters (
    id VARCHAR(64) PRIMARY KEY,
    novel_id INT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    chapter_index INT NOT NULL,
    title VARCHAR(150) NOT NULL,
    content TEXT NOT NULL,
    word_count INT DEFAULT 0,
    is_paid BOOLEAN DEFAULT FALSE,
    price INT DEFAULT 50,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_novel_chapter_index UNIQUE (novel_id, chapter_index)
);

-- 8. 读者书架与阅读进度表 (Bookshelves)
CREATE TABLE IF NOT EXISTS bookshelves (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    novel_id INT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    chapter_index INT DEFAULT 0,
    scroll_offset_percentage DECIMAL(5,4) DEFAULT 0.0000,
    in_shelf BOOLEAN DEFAULT TRUE,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_user_novel UNIQUE (user_id, novel_id)
);

-- 9. 钱包表 (Wallets)
CREATE TABLE IF NOT EXISTS wallets (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    charged_coins INT DEFAULT 0,
    bonus_coins INT DEFAULT 0,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 10. 金币账本与流水记录表 (Transactions)
CREATE TABLE IF NOT EXISTS transactions (
    id VARCHAR(36) PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(10) NOT NULL, -- credit (income) / debit (expense)
    biz_type VARCHAR(20) NOT NULL, -- recharge, checkin, unlock, reward_task
    amount INT NOT NULL,
    charged_amount INT DEFAULT 0,
    bonus_amount INT DEFAULT 0,
    description VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 11. 章节解锁记录表 (Unlock Records)
CREATE TABLE IF NOT EXISTS unlock_records (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    novel_id INT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    chapter_index INT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_user_novel_chapter_unlock UNIQUE (user_id, novel_id, chapter_index)
);

-- 12. 充值模板表 (Recharge Templates)
CREATE TABLE IF NOT EXISTS recharge_templates (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    is_default BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 13. 充值模板卡位表 (Recharge Slots - 6 Slots)
CREATE TABLE IF NOT EXISTS recharge_slots (
    id SERIAL PRIMARY KEY,
    template_id INT NOT NULL REFERENCES recharge_templates(id) ON DELETE CASCADE,
    slot_index INT NOT NULL,
    type VARCHAR(20) NOT NULL, -- 'single', 'subscription', 'vip', 'whole_book'
    coins INT DEFAULT 0,
    bonus INT DEFAULT 0,
    vip_duration VARCHAR(10) DEFAULT '', -- 'day', 'week', 'month', 'year'
    subscription_cycle VARCHAR(20) DEFAULT '', -- 'day', 'week', 'month'
    vip_name VARCHAR(50) DEFAULT '',
    vip_desc VARCHAR(255) DEFAULT '',
    price VARCHAR(20) NOT NULL,
    price_cents INT NOT NULL,
    CONSTRAINT unique_template_slot UNIQUE (template_id, slot_index)
);

ALTER TABLE recharge_slots ADD COLUMN IF NOT EXISTS subscription_cycle VARCHAR(20) DEFAULT '';

-- 13b. 支付渠道计划映射表 (Payment Provider Plans)
CREATE TABLE IF NOT EXISTS payment_provider_plans (
    id SERIAL PRIMARY KEY,
    provider VARCHAR(32) NOT NULL,          -- 'paypal', 'stripe', etc.
    slot_id INT NOT NULL,                   -- 关联 recharge_slots.id
    cycle VARCHAR(16) NOT NULL,             -- 'day', 'week', 'month'
    price_cents INT NOT NULL,               -- 计费金额（美分）
    currency VARCHAR(8) NOT NULL DEFAULT 'USD',
    external_plan_id VARCHAR(128) NOT NULL, -- 第三方计划 ID (如 PayPal 的 P-XXXXX)
    status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
    raw_payload JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uk_provider_slot UNIQUE (provider, slot_id)
);

CREATE INDEX IF NOT EXISTS idx_provider_plans_slot ON payment_provider_plans(slot_id);
CREATE INDEX IF NOT EXISTS idx_provider_plans_lookup ON payment_provider_plans(provider, cycle, price_cents);


-- 14. 落地页域名管理表 (System Domains)
CREATE TABLE IF NOT EXISTS system_domains (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    domain VARCHAR(255) NOT NULL UNIQUE,
    type VARCHAR(20) NOT NULL DEFAULT 'sub', -- 'main' / 'sub'
    status SMALLINT DEFAULT 1,             -- 1-Active, 2-Disabled
    is_default BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 15. Facebook 像素管理表 (FB Pixels)
CREATE TABLE IF NOT EXISTS fb_pixels (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    pixel_id VARCHAR(100) NOT NULL UNIQUE,
    access_token TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 16. 推广链接表 (Promotion Links)
CREATE TABLE IF NOT EXISTS promotion_links (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) DEFAULT '',
    novel_id INT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    novel_title VARCHAR(255) NOT NULL,
    chapter_index INT,
    utm_source VARCHAR(100),
    utm_campaign VARCHAR(100),
    generated_url TEXT NOT NULL,
    fb_pixel_id INT REFERENCES fb_pixels(id) ON DELETE SET NULL,
    recharge_template_id INT REFERENCES recharge_templates(id) ON DELETE SET NULL,
    domain_id INT REFERENCES system_domains(id) ON DELETE SET NULL,
    domain VARCHAR(255) DEFAULT '',
    coin_cost_per_thousand INT DEFAULT NULL,
    start_pay_chapter_index INT DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS name VARCHAR(255) DEFAULT '';
ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS fb_pixel_id INT REFERENCES fb_pixels(id) ON DELETE SET NULL;
ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS recharge_template_id INT REFERENCES recharge_templates(id) ON DELETE SET NULL;
ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS domain_id INT REFERENCES system_domains(id) ON DELETE SET NULL;
ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS domain VARCHAR(255) DEFAULT '';
ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS coin_cost_per_thousand INT DEFAULT NULL;
ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS start_pay_chapter_index INT DEFAULT NULL;

-- 17. 充值订单表 (Recharge Orders)
CREATE TABLE IF NOT EXISTS recharge_orders (
    id BIGINT PRIMARY KEY DEFAULT nextval('recharge_orders_id_seq'),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    external_ref_id VARCHAR(100) UNIQUE, -- Stripe payment intent ID or PayPal order ID
    amount_cents INT NOT NULL,           -- Payment amount in cents (e.g. 999 for $9.99)
    currency VARCHAR(3) DEFAULT 'USD',
    coins INT DEFAULT 0,
    bonus_coins_credited INT DEFAULT 0,
    payment_method VARCHAR(20) NOT NULL, -- stripe / paypal
    status VARCHAR(20) DEFAULT 'Pending',-- Pending, Success, Failed, Refunded
    utm_source VARCHAR(50),              -- UA source tracing
    utm_campaign VARCHAR(50),
    fb_lead_metadata JSONB,              -- Stores fbp, fbc, user_agent, ip for Conversions API
    paid_at TIMESTAMP WITH TIME ZONE,
    order_type VARCHAR(20) DEFAULT 'single',
    subscription_id VARCHAR(100),
    recharge_template_id INT REFERENCES recharge_templates(id) ON DELETE SET NULL,
    recharge_slot_id INT,
    promotion_link_id INT REFERENCES promotion_links(id) ON DELETE SET NULL,
    novel_id INT REFERENCES novels(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS subscription_id VARCHAR(100);

-- 17b. 用户周期订阅表 (User Subscriptions)
CREATE TABLE IF NOT EXISTS user_subscriptions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subscription_id VARCHAR(100) UNIQUE NOT NULL, -- PayPal Subscription ID (e.g. I-XXXXX)
    plan_id VARCHAR(100),
    slot_id INT,
    template_id INT,
    status VARCHAR(30) NOT NULL DEFAULT 'ACTIVE', -- 'PENDING', 'ACTIVE', 'CANCELLED', 'SUSPENDED', 'EXPIRED'
    cycle VARCHAR(20) NOT NULL DEFAULT 'month',   -- 'day', 'week', 'month'
    price_cents INT NOT NULL DEFAULT 0,
    currency VARCHAR(10) DEFAULT 'USD',
    payment_method VARCHAR(30) DEFAULT 'paypal',
    current_period_start TIMESTAMP WITH TIME ZONE,
    current_period_end TIMESTAMP WITH TIME ZONE,
    next_billing_time TIMESTAMP WITH TIME ZONE,
    last_payment_time TIMESTAMP WITH TIME ZONE,
    cancelled_at TIMESTAMP WITH TIME ZONE,
    raw_payload JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_user ON user_subscriptions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_status ON user_subscriptions(status);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_sub_id ON user_subscriptions(subscription_id);


-- 18. 首页推荐位表 (Recommendations)
CREATE TABLE IF NOT EXISTS recommendations (
    id SERIAL PRIMARY KEY,
    group_name VARCHAR(50) NOT NULL,    -- editor_pick, hot_romance, trending
    novel_id INT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    sort_order INT DEFAULT 0,
    scheduled_start TIMESTAMP WITH TIME ZONE,
    scheduled_end TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_group_novel UNIQUE (group_name, novel_id)
);

-- 19. 礼包兑换码表 (Gift Codes)
CREATE TABLE IF NOT EXISTS gift_codes (
    id SERIAL PRIMARY KEY,
    code VARCHAR(50) UNIQUE NOT NULL,
    reward_type VARCHAR(20) NOT NULL,      -- bonus_coins / charged_coins
    reward_amount INT NOT NULL,
    max_redemptions INT DEFAULT 1000,
    current_redemptions INT DEFAULT 0,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 20. 礼包码兑换记录表 (Gift Code Redemptions)
CREATE TABLE IF NOT EXISTS gift_code_redemptions (
    id SERIAL PRIMARY KEY,
    gift_code_id INT NOT NULL REFERENCES gift_codes(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redeemed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_user_gift_code UNIQUE (user_id, gift_code_id)
);

-- 21. 用户反馈表 (Feedback)
CREATE TABLE IF NOT EXISTS feedback (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    email VARCHAR(100),
    subject VARCHAR(150),
    content TEXT NOT NULL,
    status VARCHAR(20) DEFAULT 'pending', -- pending, replied, resolved
    admin_reply TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 22. Facebook CAPI 事件日志表 (Facebook CAPI Logs)
CREATE TABLE IF NOT EXISTS facebook_capi_logs (
    id BIGSERIAL PRIMARY KEY,
    pixel_id VARCHAR(50) NOT NULL,
    event_name VARCHAR(50) NOT NULL,
    user_id BIGINT,
    value DECIMAL(10,2),
    currency VARCHAR(10),
    test_event_code VARCHAR(50),
    status_code INT,
    payload TEXT,
    response TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 23. 性能索引构建 (Indexes)
CREATE INDEX IF NOT EXISTS idx_novels_status_publish_views ON novels(status, publish_status, view_count DESC, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_novels_status_publish_created ON novels(status, publish_status, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_novels_genres_gin ON novels USING GIN (genres);
CREATE INDEX IF NOT EXISTS idx_novels_title_trgm ON novels USING GIN (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_novels_author_trgm ON novels USING GIN (author gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_novels_synopsis_trgm ON novels USING GIN (synopsis gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_chapters_novel_index ON chapters(novel_id, chapter_index);
CREATE INDEX IF NOT EXISTS idx_bookshelves_user ON bookshelves(user_id);
CREATE INDEX IF NOT EXISTS idx_unlock_records_user_novel ON unlock_records(user_id, novel_id);
CREATE INDEX IF NOT EXISTS idx_transactions_user ON transactions(user_id);
CREATE INDEX IF NOT EXISTS idx_recharge_orders_user ON recharge_orders(user_id);
CREATE INDEX IF NOT EXISTS idx_recharge_orders_status ON recharge_orders(status);
CREATE INDEX IF NOT EXISTS idx_recommendations_group ON recommendations(group_name);
CREATE INDEX IF NOT EXISTS idx_gift_code_redemptions_user ON gift_code_redemptions(user_id);
CREATE INDEX IF NOT EXISTS idx_admin_audit_logs_admin ON admin_audit_logs(admin_id);
CREATE INDEX IF NOT EXISTS idx_feedback_user ON feedback(user_id);
CREATE INDEX IF NOT EXISTS idx_feedback_status ON feedback(status);

-- 每日签到防刷唯一偏索引 (使用 UTC 转换保证 IMMUTABLE)
CREATE UNIQUE INDEX IF NOT EXISTS idx_unique_user_daily_checkin 
ON transactions(user_id, ((created_at AT TIME ZONE 'UTC')::date)) 
WHERE biz_type = 'checkin';

-- 24. 第三方支付订单详情表 (Third Party Payment Orders)
CREATE TABLE IF NOT EXISTS third_party_payment_orders (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES recharge_orders(id) ON DELETE CASCADE,
    payment_provider VARCHAR(30) NOT NULL,       -- 'paypal', 'stripe'
    external_order_id VARCHAR(100),               -- PayPal Order ID or Stripe PaymentIntent ID
    capture_id VARCHAR(100),                      -- PayPal Capture ID or Stripe Charge ID
    payer_id VARCHAR(100),                        -- PayPal Payer/Account ID
    payer_email VARCHAR(255),                     -- Payer Email
    payer_name VARCHAR(150),                      -- Payer Name
    payer_country VARCHAR(10),                    -- Payer Country Code (US, C2, etc.)
    currency VARCHAR(10) DEFAULT 'USD',
    gross_amount DECIMAL(10, 2) NOT NULL,         -- Total amount paid (e.g. 49.99)
    fee_amount DECIMAL(10, 2) DEFAULT 0.00,       -- Payment fee charged by provider (e.g. 2.00)
    net_amount DECIMAL(10, 2) DEFAULT 0.00,       -- Net amount received (e.g. 47.99)
    status VARCHAR(30) NOT NULL,                  -- 'COMPLETED', 'PENDING', 'FAILED', 'REFUNDED'
    seller_protection_status VARCHAR(50),         -- 'ELIGIBLE', 'NOT_ELIGIBLE', etc.
    raw_payload JSONB,                            -- Complete raw JSON response
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_tp_provider_external_capture UNIQUE (payment_provider, external_order_id, capture_id)
);

CREATE INDEX IF NOT EXISTS idx_tp_payment_order_id ON third_party_payment_orders(order_id);
CREATE INDEX IF NOT EXISTS idx_tp_payment_ext_id ON third_party_payment_orders(external_order_id);
CREATE INDEX IF NOT EXISTS idx_tp_payment_capture_id ON third_party_payment_orders(capture_id);

-- Payment checkout snapshots and verified fulfillment.
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS checkout_id UUID;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS fulfilled_at TIMESTAMPTZ;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS paid_period_start TIMESTAMPTZ;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS paid_period_end TIMESTAMPTZ;
CREATE UNIQUE INDEX IF NOT EXISTS idx_recharge_checkout_id ON recharge_orders(checkout_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_paypal_verified_capture
ON third_party_payment_orders(capture_id)
WHERE payment_provider = 'paypal' AND status = 'VERIFIED';
CREATE INDEX IF NOT EXISTS idx_whole_book_entitlement
ON recharge_orders(user_id, novel_id)
WHERE order_type = 'whole_book' AND status IN ('Success','RefundPending') AND fulfilled_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS paypal_processed_events (
    id TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS payment_refunds (
    order_id BIGINT PRIMARY KEY REFERENCES recharge_orders(id) ON DELETE CASCADE,
    request_id UUID NOT NULL UNIQUE,
    provider_refund_id VARCHAR(100) UNIQUE,
    status VARCHAR(20) NOT NULL DEFAULT 'Pending',
    admin_id VARCHAR(100),
    charged_coin_shortfall INT NOT NULL DEFAULT 0,
    bonus_coin_shortfall INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS paypal_refund_reported BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS prior_order_status VARCHAR(20) NOT NULL DEFAULT 'Success';
ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS requested_amount_cents INT;
ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS channel_lookup_only BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS paypal_refund_items (
    refund_id TEXT PRIMARY KEY,
    capture_id TEXT NOT NULL CHECK (capture_id <> ''),
    currency VARCHAR(3) NOT NULL,
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_paypal_refund_capture ON paypal_refund_items(capture_id,currency);

CREATE OR REPLACE FUNCTION save_paypal_refund_item(refund_ref TEXT, capture_ref TEXT, cents BIGINT, currency_code TEXT)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
    IF refund_ref = '' THEN RAISE EXCEPTION 'Missing PayPal refund ID'; END IF;
    INSERT INTO paypal_refund_items(refund_id,capture_id,amount_cents,currency)
        VALUES(refund_ref,capture_ref,cents,currency_code) ON CONFLICT(refund_id) DO NOTHING;
    IF NOT EXISTS(SELECT 1 FROM paypal_refund_items
        WHERE refund_id=refund_ref AND capture_id=capture_ref AND amount_cents=cents AND currency=currency_code) THEN
        RAISE EXCEPTION 'Conflicting PayPal refund evidence';
    END IF;
END;
$$;

-- Called after confirming a full channel refund. The order, wallet and audit
-- entries commit together, including when a provider or admin retries.
CREATE OR REPLACE FUNCTION complete_paypal_refund(target_order BIGINT, provider_refund TEXT)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE
    purchase recharge_orders%ROWTYPE;
    refund payment_refunds%ROWTYPE;
    charged_balance INT;
    bonus_balance INT;
    charged_debit INT;
    bonus_debit INT;
BEGIN
    SELECT * INTO STRICT purchase FROM recharge_orders WHERE id = target_order FOR UPDATE;
    SELECT * INTO STRICT refund FROM payment_refunds WHERE order_id = target_order FOR UPDATE;
    IF refund.status = 'Completed' THEN
        IF refund.provider_refund_id <> provider_refund THEN RAISE EXCEPTION 'Refund ID mismatch'; END IF;
        RETURN;
    END IF;
    IF purchase.status <> 'RefundPending' OR purchase.payment_method <> 'paypal'
        OR refund.status <> 'Pending'
        OR (refund.provider_refund_id IS NOT NULL AND refund.provider_refund_id <> provider_refund)
        OR provider_refund = '' THEN
        RAISE EXCEPTION 'Invalid refund transition';
    END IF;
    SELECT charged_coins, bonus_coins INTO charged_balance, bonus_balance
        FROM wallets WHERE user_id = purchase.user_id FOR UPDATE;
    charged_debit := LEAST(GREATEST(0, COALESCE(charged_balance,0)), GREATEST(0,purchase.coins));
    bonus_debit := LEAST(GREATEST(0, COALESCE(bonus_balance,0)), GREATEST(0,purchase.bonus_coins_credited));
    IF charged_debit + bonus_debit > 0 THEN
        UPDATE wallets SET charged_coins = charged_coins - charged_debit,
            bonus_coins = bonus_coins - bonus_debit, updated_at = now() WHERE user_id = purchase.user_id;
        INSERT INTO transactions (id,user_id,type,biz_type,amount,charged_amount,bonus_amount,description)
            VALUES (gen_random_uuid()::text,purchase.user_id,'debit','refund',charged_debit+bonus_debit,
                    charged_debit,bonus_debit,'Refund for order #' || target_order);
    END IF;
    UPDATE payment_refunds SET status='Completed', provider_refund_id=provider_refund,
        charged_coin_shortfall=GREATEST(0,purchase.coins)-charged_debit,
        bonus_coin_shortfall=GREATEST(0,purchase.bonus_coins_credited)-bonus_debit,
        updated_at=now() WHERE order_id=target_order;
    UPDATE recharge_orders SET status='Refunded', updated_at=now() WHERE id=target_order;
    IF purchase.subscription_id IS NOT NULL AND purchase.paid_period_end IS NOT NULL THEN
        PERFORM 1 FROM user_subscriptions WHERE subscription_id=purchase.subscription_id FOR UPDATE;
        UPDATE user_subscriptions SET current_period_end = (
            SELECT max(paid_period_end) FROM recharge_orders
            WHERE subscription_id = purchase.subscription_id AND status IN ('Success','Paid','RefundPending')
              AND NOT paypal_refund_reported
        ), updated_at = now()
        WHERE subscription_id = purchase.subscription_id AND current_period_end <= purchase.paid_period_end;
    END IF;
END;
$$;

-- Both the CMS response and webhook use this operation after verifying a refund.
CREATE OR REPLACE FUNCTION settle_paypal_refund_item(target_order BIGINT, refund_ref TEXT, capture_ref TEXT, cents BIGINT, currency_code TEXT)
RETURNS BOOLEAN LANGUAGE plpgsql AS $$
DECLARE
    purchase recharge_orders%ROWTYPE;
    refunded NUMERIC;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('paypal-capture:' || capture_ref,0));
    SELECT * INTO STRICT purchase FROM recharge_orders WHERE id=target_order FOR UPDATE;
    IF purchase.payment_method <> 'paypal' OR purchase.currency <> currency_code
        OR purchase.status NOT IN ('Success','Paid','RefundPending','Refunded') THEN
        RAISE EXCEPTION 'Invalid PayPal refund target';
    END IF;
    PERFORM save_paypal_refund_item(refund_ref,capture_ref,cents,currency_code);
    IF purchase.status='Refunded' THEN RETURN true; END IF;
    SELECT SUM(amount_cents) INTO refunded FROM paypal_refund_items
        WHERE capture_id=capture_ref AND currency=currency_code;
    IF refunded < purchase.amount_cents THEN RETURN false; END IF;
    IF refunded <> purchase.amount_cents THEN RAISE EXCEPTION 'PayPal refund total differs from order'; END IF;

    INSERT INTO payment_refunds(order_id,request_id,provider_refund_id,status,prior_order_status)
        VALUES(target_order,gen_random_uuid(),refund_ref,'Pending',purchase.status)
        ON CONFLICT (order_id) DO UPDATE SET provider_refund_id=EXCLUDED.provider_refund_id,status='Pending'
        WHERE payment_refunds.status IN ('Pending','Failed');
    UPDATE recharge_orders SET status='RefundPending',updated_at=now() WHERE id=target_order;
    PERFORM complete_paypal_refund(target_order,refund_ref);
    RETURN true;
END;
$$;

ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS reconciliation_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_paypal_pending_reconciliation
ON recharge_orders(COALESCE(reconciliation_at, created_at))
WHERE payment_method='paypal' AND status='Pending' AND external_ref_id IS NOT NULL AND checkout_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS payment_refund_failures (
    request_id UUID PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES recharge_orders(id) ON DELETE CASCADE,
    provider_refund_id VARCHAR(100) NOT NULL,
    provider_status TEXT NOT NULL,
    admin_id VARCHAR(100),
    failed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS paypal_event_inbox (
    event_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    capture_id TEXT,
    refund_id TEXT,
    amount_cents BIGINT NOT NULL,
    currency TEXT NOT NULL,
    event JSONB NOT NULL,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT
);
CREATE INDEX IF NOT EXISTS idx_paypal_inbox_due ON paypal_event_inbox(next_attempt_at,event_id)
    WHERE processed_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_paypal_inbox_refund_capture ON paypal_event_inbox(capture_id,amount_cents,currency)
    WHERE event_type='refund_completed';

-- Restore completed refund evidence kept in the durable inbox before this ledger was added.
INSERT INTO paypal_refund_items(refund_id,capture_id,currency,amount_cents)
SELECT DISTINCT ON (refund_id) refund_id,capture_id,currency,amount_cents
FROM paypal_event_inbox WHERE event_type='refund_completed' AND refund_id<>'' AND capture_id<>''
    AND length(currency)=3 AND amount_cents>0
ORDER BY refund_id,event_id ON CONFLICT(refund_id) DO NOTHING;

ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS reconciliation_note TEXT;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS paypal_transaction_cursor TIMESTAMPTZ;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS paypal_next_check_at TIMESTAMPTZ;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS cancel_next_attempt_at TIMESTAMPTZ;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS cancel_attempts INT NOT NULL DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS cancel_reason TEXT;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS cancel_last_error TEXT;

CREATE INDEX IF NOT EXISTS idx_paypal_cancel_due ON user_subscriptions(cancel_next_attempt_at,id)
    WHERE payment_method='paypal' AND status='CANCEL_PENDING';
CREATE INDEX IF NOT EXISTS idx_paypal_subscription_due ON user_subscriptions(paypal_next_check_at,id)
    WHERE payment_method='paypal';

CREATE TABLE IF NOT EXISTS paypal_subscription_billing_snapshots (
    subscription_id VARCHAR(100) NOT NULL REFERENCES user_subscriptions(subscription_id) ON DELETE CASCADE,
    paid_at TIMESTAMPTZ NOT NULL,
    currency VARCHAR(3) NOT NULL,
    amount_cents BIGINT NOT NULL CHECK(amount_cents>0),
    paid_period_end TIMESTAMPTZ NOT NULL CHECK(paid_period_end>paid_at),
    PRIMARY KEY(subscription_id,paid_at,currency,amount_cents)
);

-- A historical receipt is insufficient to prove which product was sold.
CREATE TABLE IF NOT EXISTS legacy_whole_book_proofs (
    order_id BIGINT PRIMARY KEY REFERENCES recharge_orders(id) ON DELETE CASCADE,
    novel_id INT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    evidence_reference TEXT NOT NULL CHECK(length(btrim(evidence_reference))>0),
    verified_by VARCHAR(36) NOT NULL REFERENCES admins(id),
    verified_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS paypal_subscription_intents (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    intent_id UUID NOT NULL UNIQUE,
    slot_id INT NOT NULL,
    template_id INT NOT NULL,
    cycle TEXT NOT NULL,
    price_cents INT NOT NULL CHECK(price_cents>0),
    return_url TEXT NOT NULL,
    cancel_url TEXT NOT NULL,
    plan_id TEXT,
    subscription_id TEXT UNIQUE,
    approval_url TEXT,
    lease_until TIMESTAMPTZ,
    submitted_at TIMESTAMPTZ
);

ALTER TABLE users ADD COLUMN IF NOT EXISTS promotion_link_id INTEGER REFERENCES promotion_links(id) ON DELETE SET NULL;
ALTER TABLE users ADD COLUMN IF NOT EXISTS promotion_resolved BOOLEAN NOT NULL DEFAULT false;
CREATE OR REPLACE FUNCTION resolve_reader_promotion(link INTEGER, source TEXT, campaign TEXT) RETURNS INTEGER LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN link>0 THEN (SELECT p.id FROM promotion_links p JOIN novels n ON n.id=p.novel_id WHERE p.id=link AND n.publish_status='published' AND n.status<>'archived')
 ELSE (SELECT min(id) FROM promotion_links WHERE utm_source=source AND utm_campaign=campaign AND source<>'' AND campaign<>'' HAVING count(*)=1) END
$$;
UPDATE users SET promotion_link_id=resolve_reader_promotion(0,utm_source,utm_campaign), promotion_resolved=true WHERE NOT promotion_resolved;
CREATE OR REPLACE FUNCTION reader_promotion_for_user(uid BIGINT) RETURNS SETOF promotion_links LANGUAGE sql STABLE AS $$
 SELECT p.* FROM users u JOIN promotion_links p ON p.id=COALESCE(u.promotion_link_id,CASE WHEN NOT u.promotion_resolved THEN resolve_reader_promotion(0,u.utm_source,u.utm_campaign) END) WHERE u.id=uid
$$;
-- Both the offer list and checkout use this rule for the current novel.
CREATE OR REPLACE FUNCTION whole_book_offer_available(p_user_id BIGINT,p_template_id INTEGER,p_novel_id BIGINT)
RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(
  SELECT 1 FROM users u
  LEFT JOIN LATERAL(SELECT id,novel_id,recharge_template_id FROM reader_promotion_for_user(u.id)) p ON TRUE
  WHERE u.id=p_user_id AND p_novel_id>0
  AND EXISTS(SELECT 1 FROM novels WHERE id=p_novel_id AND status<>'archived' AND publish_status='published')
  AND p_template_id=COALESCE(p.recharge_template_id,(SELECT id FROM recharge_templates ORDER BY is_default DESC NULLS LAST,id LIMIT 1))
  AND (p.novel_id=p_novel_id OR (p.id IS NULL AND EXISTS(SELECT 1 FROM recharge_templates WHERE id=p_template_id AND is_default)))
 )
$$;

-- Reader availability fixes: preserve accepted attribution and separate progress ordering.
ALTER TABLE recharge_orders ALTER COLUMN utm_source TYPE VARCHAR(100);
ALTER TABLE recharge_orders ALTER COLUMN utm_campaign TYPE VARCHAR(100);
UPDATE bookshelves SET updated_at=now() WHERE updated_at>now()+interval '5 minutes';

CREATE TABLE IF NOT EXISTS reader_sessions(token_hash TEXT PRIMARY KEY,user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,expires_at TIMESTAMPTZ NOT NULL);
CREATE INDEX IF NOT EXISTS reader_sessions_expiry ON reader_sessions(expires_at);

-- Published content cache generations are owned by the database, not Redis invalidations.
ALTER TABLE novels ADD COLUMN IF NOT EXISTS content_version BIGINT NOT NULL DEFAULT 1;
CREATE OR REPLACE FUNCTION bump_reader_novel_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.content_version:=OLD.content_version+1; RETURN NEW; END $$;
DROP TRIGGER IF EXISTS reader_novel_version ON novels;
CREATE TRIGGER reader_novel_version BEFORE UPDATE ON novels FOR EACH ROW EXECUTE FUNCTION bump_reader_novel_version();
CREATE OR REPLACE FUNCTION bump_reader_chapter_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN UPDATE novels SET content_version=content_version WHERE id=OLD.novel_id; END IF;
 IF TG_OP='INSERT' OR (TG_OP='UPDATE' AND NEW.novel_id<>OLD.novel_id) THEN UPDATE novels SET content_version=content_version WHERE id=NEW.novel_id; END IF;
 RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS reader_chapter_version ON chapters;
CREATE TRIGGER reader_chapter_version AFTER INSERT OR UPDATE OR DELETE ON chapters FOR EACH ROW EXECUTE FUNCTION bump_reader_chapter_version();

ALTER TABLE payment_provider_plans ADD COLUMN IF NOT EXISTS identity_client_id TEXT;
ALTER TABLE payment_provider_plans ADD COLUMN IF NOT EXISTS identity_environment TEXT;

-- The current installation is bound to one PayPal merchant/environment. Secret rotation is allowed.
CREATE TABLE IF NOT EXISTS paypal_identity_lock(id BOOLEAN PRIMARY KEY DEFAULT true CHECK(id),client_id TEXT NOT NULL,environment TEXT NOT NULL);
CREATE OR REPLACE FUNCTION assert_paypal_identity(cid TEXT, mode TEXT) RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE environment_name TEXT;
BEGIN
 IF cid='' THEN RAISE EXCEPTION 'PayPal client ID required'; END IF;
 environment_name:=CASE WHEN mode IN ('live','production') THEN 'live' ELSE 'sandbox' END;
 INSERT INTO paypal_identity_lock(id,client_id,environment) VALUES(true,cid,environment_name) ON CONFLICT DO NOTHING;
 IF NOT EXISTS(SELECT 1 FROM paypal_identity_lock WHERE id AND client_id=cid AND environment=environment_name) THEN
 RAISE EXCEPTION 'PayPal merchant/environment change requires an audited migration'; END IF;
END $$;
CREATE OR REPLACE FUNCTION guard_paypal_identity_settings() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF COALESCE(NEW.key,OLD.key) IN ('paypal_client_id','paypal_mode','payment_sandbox_mode') AND EXISTS(SELECT 1 FROM paypal_identity_lock) THEN
  IF TG_OP='INSERT' AND EXISTS(SELECT 1 FROM system_configs WHERE key=NEW.key AND value=NEW.value) THEN RETURN NEW; END IF;
  IF TG_OP='INSERT' OR TG_OP='DELETE' THEN RAISE EXCEPTION 'PayPal identity settings are locked'; END IF;
  IF NEW.value IS DISTINCT FROM OLD.value THEN RAISE EXCEPTION 'PayPal identity settings are locked'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS paypal_identity_settings ON system_configs;
CREATE TRIGGER paypal_identity_settings BEFORE INSERT OR UPDATE OR DELETE ON system_configs FOR EACH ROW EXECUTE FUNCTION guard_paypal_identity_settings();

ALTER TABLE paypal_subscription_intents ADD COLUMN IF NOT EXISTS attribution JSONB NOT NULL DEFAULT '{}';
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS attribution JSONB NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS payment_event_outbox(
 event_id TEXT PRIMARY KEY,order_id BIGINT NOT NULL REFERENCES recharge_orders(id),event_name TEXT NOT NULL,
 payload JSONB NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 state TEXT NOT NULL DEFAULT 'pending',attempts INT NOT NULL DEFAULT 0,lease_token UUID,lease_until TIMESTAMPTZ,delivered_at TIMESTAMPTZ,last_error TEXT
);
CREATE INDEX IF NOT EXISTS payment_event_outbox_due ON payment_event_outbox(next_attempt_at) WHERE delivered_at IS NULL;
CREATE OR REPLACE FUNCTION enqueue_payment_event(purchase recharge_orders,name TEXT) RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO payment_event_outbox(event_id,order_id,event_name,payload,state)
 SELECT CASE WHEN name='Subscribe' THEN 'subscription:'||purchase.subscription_id||':Subscribe' ELSE 'payment:'||purchase.id||':'||name END,purchase.id,name,jsonb_build_object(
 'event_id',CASE WHEN name='Subscribe' THEN 'subscription:'||purchase.subscription_id||':Subscribe' ELSE 'payment:'||purchase.id||':'||name END,'event_name',name,'event_time',extract(epoch FROM COALESCE(purchase.paid_at,now()))::bigint,
 'user_id',purchase.user_id::text,'email',COALESCE(NULLIF(u.email,''),NULLIF(purchase.fb_lead_metadata->>'payer_email',''),(SELECT payer_email FROM third_party_payment_orders WHERE order_id=purchase.id ORDER BY id DESC LIMIT 1),''),'amount_cents',purchase.amount_cents,'currency',purchase.currency,
 'attribution',COALESCE(purchase.fb_lead_metadata,'{}')),CASE WHEN COALESCE(purchase.fb_lead_metadata->>'pixel_id','')='' THEN 'unattributed' ELSE 'pending' END FROM users u WHERE u.id=purchase.user_id
 ON CONFLICT(event_id) DO NOTHING;
END $$;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS payment_events_enabled BOOLEAN NOT NULL DEFAULT false;
CREATE OR REPLACE FUNCTION capture_payment_events() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='Refunded' AND NEW.fulfilled_at IS NULL THEN DELETE FROM payment_event_outbox WHERE order_id=NEW.id AND delivered_at IS NULL; END IF;
 IF TG_OP='UPDATE' THEN IF OLD.fulfilled_at IS NOT NULL THEN RETURN NEW; END IF; END IF;
 IF NEW.payment_events_enabled AND NEW.status IN ('Success','Paid') AND NEW.fulfilled_at IS NOT NULL AND NOT NEW.paypal_refund_reported THEN
  PERFORM enqueue_payment_event(NEW,'Purchase');
  IF NEW.order_type='subscription' AND NOT EXISTS(
   SELECT 1 FROM recharge_orders o WHERE o.subscription_id=NEW.subscription_id AND o.id<>NEW.id AND o.fulfilled_at IS NOT NULL AND o.status IN ('Success','Paid','RefundPending')
  ) THEN PERFORM enqueue_payment_event(NEW,'Subscribe'); END IF;
 END IF;RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS payment_events ON recharge_orders;
CREATE TRIGGER payment_events AFTER INSERT OR UPDATE ON recharge_orders FOR EACH ROW EXECUTE FUNCTION capture_payment_events();

-- Reader release ledger: append one immutable milestone per schema release.
-- Applying this file directly must run inside a transaction (CMS does so).
ALTER TABLE paypal_event_inbox ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS reconciliation_attempts INT NOT NULL DEFAULT 0;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS reconciliation_error TEXT;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS paypal_recovery_requested BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS paypal_check_attempts INT NOT NULL DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS paypal_check_error TEXT;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS cancel_requested_at TIMESTAMPTZ;
UPDATE user_subscriptions SET cancel_requested_at=updated_at WHERE status='CANCEL_PENDING' AND cancel_requested_at IS NULL;
CREATE OR REPLACE FUNCTION mark_cancel_requested() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='CANCEL_PENDING' AND OLD.status IS DISTINCT FROM NEW.status THEN NEW.cancel_requested_at=now(); END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS mark_cancel_requested ON user_subscriptions;
CREATE TRIGGER mark_cancel_requested BEFORE UPDATE ON user_subscriptions FOR EACH ROW EXECUTE FUNCTION mark_cancel_requested();
CREATE TABLE IF NOT EXISTS reader_schema_migrations(version BIGINT PRIMARY KEY,applied_at TIMESTAMPTZ NOT NULL DEFAULT now());
INSERT INTO reader_schema_migrations(version) VALUES(2026111701) ON CONFLICT(version) DO NOTHING;

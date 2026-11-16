-- =========================================================================
-- STAR NOVEL - 生产级统一数据库表结构 (Unified Database Schema)
-- 包含：用户/鉴权、小说/章节、钱包/交易、充值/模板、投放/像素、Meta大盘、反馈等
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
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 增量补齐字段
ALTER TABLE novels ADD COLUMN IF NOT EXISTS coin_cost_per_thousand INT DEFAULT 500;
ALTER TABLE novels ADD COLUMN IF NOT EXISTS start_pay_chapter_index INT DEFAULT 3;
ALTER TABLE novels ADD COLUMN IF NOT EXISTS publish_status VARCHAR(20) NOT NULL DEFAULT 'published';
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

-- 22. Meta 广告大盘表群 (Meta Marketing API & Daily Insights)
CREATE TABLE IF NOT EXISTS meta_accounts (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    currency VARCHAR(10) DEFAULT 'USD',
    timezone VARCHAR(50) DEFAULT 'UTC',
    status VARCHAR(50) DEFAULT 'ACTIVE',
    bm_id VARCHAR(64) DEFAULT '',
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS meta_campaigns (
    id VARCHAR(64) PRIMARY KEY,
    account_id VARCHAR(64) NOT NULL,
    name VARCHAR(255) NOT NULL,
    objective VARCHAR(64) DEFAULT '',
    status VARCHAR(50) DEFAULT 'ACTIVE',
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS meta_adsets (
    id VARCHAR(64) PRIMARY KEY,
    campaign_id VARCHAR(64) NOT NULL,
    account_id VARCHAR(64) NOT NULL,
    name VARCHAR(255) NOT NULL,
    daily_budget NUMERIC(12,4) DEFAULT 0,
    status VARCHAR(50) DEFAULT 'ACTIVE',
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS meta_ads (
    id VARCHAR(64) PRIMARY KEY,
    ad_set_id VARCHAR(64) NOT NULL,
    campaign_id VARCHAR(64) NOT NULL,
    account_id VARCHAR(64) NOT NULL,
    name VARCHAR(255) NOT NULL,
    status VARCHAR(50) DEFAULT 'ACTIVE',
    created_time TIMESTAMP WITH TIME ZONE,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS meta_daily_insights (
    id VARCHAR(128) PRIMARY KEY,
    entity_level VARCHAR(20) NOT NULL,
    entity_id VARCHAR(64) NOT NULL,
    stat_date VARCHAR(10) NOT NULL,
    spend NUMERIC(12,4) DEFAULT 0,
    impressions BIGINT DEFAULT 0,
    reach BIGINT DEFAULT 0,
    frequency NUMERIC(8,4) DEFAULT 0,
    cpm NUMERIC(12,4) DEFAULT 0,
    clicks BIGINT DEFAULT 0,
    cpc NUMERIC(12,4) DEFAULT 0,
    ctr NUMERIC(8,4) DEFAULT 0,
    link_clicks BIGINT DEFAULT 0,
    cost_per_link_click NUMERIC(12,4) DEFAULT 0,
    link_ctr NUMERIC(8,4) DEFAULT 0,
    landing_page_views BIGINT DEFAULT 0,
    cost_per_landing_page_view NUMERIC(12,4) DEFAULT 0,
    view_content_count BIGINT DEFAULT 0,
    cost_per_view_content NUMERIC(12,4) DEFAULT 0,
    add_to_cart_count BIGINT DEFAULT 0,
    cost_per_add_to_cart NUMERIC(12,4) DEFAULT 0,
    initiate_checkout_count BIGINT DEFAULT 0,
    cost_per_initiate_checkout NUMERIC(12,4) DEFAULT 0,
    complete_registration_count BIGINT DEFAULT 0,
    cost_per_complete_registration NUMERIC(12,4) DEFAULT 0,
    purchase_count BIGINT DEFAULT 0,
    cost_per_purchase NUMERIC(12,4) DEFAULT 0,
    purchase_value NUMERIC(14,4) DEFAULT 0,
    purchase_roas NUMERIC(10,4) DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS meta_configs (
    key VARCHAR(64) PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 23. Facebook CAPI 事件日志表 (Facebook CAPI Logs)
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

-- 24. 性能索引构建 (Indexes)
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
CREATE INDEX IF NOT EXISTS idx_meta_camp_acc ON meta_campaigns(account_id);
CREATE INDEX IF NOT EXISTS idx_meta_adset_camp ON meta_adsets(campaign_id);
CREATE INDEX IF NOT EXISTS idx_meta_adset_acc ON meta_adsets(account_id);
CREATE INDEX IF NOT EXISTS idx_meta_ad_adset ON meta_ads(ad_set_id);
CREATE INDEX IF NOT EXISTS idx_meta_ad_camp ON meta_ads(campaign_id);
CREATE INDEX IF NOT EXISTS idx_meta_ad_acc ON meta_ads(account_id);
CREATE INDEX IF NOT EXISTS idx_meta_insight_entity_date ON meta_daily_insights(entity_level, entity_id, stat_date);

-- 每日签到防刷唯一偏索引 (使用 UTC 转换保证 IMMUTABLE)
CREATE UNIQUE INDEX IF NOT EXISTS idx_unique_user_daily_checkin 
ON transactions(user_id, ((created_at AT TIME ZONE 'UTC')::date)) 
WHERE biz_type = 'checkin';

-- 25. 第三方支付订单详情表 (Third Party Payment Orders)
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
        UPDATE user_subscriptions SET current_period_end = (
            SELECT max(paid_period_end) FROM recharge_orders
            WHERE subscription_id = purchase.subscription_id AND status IN ('Success','Paid','RefundPending')
              AND NOT paypal_refund_reported
        ), updated_at = now()
        WHERE subscription_id = purchase.subscription_id AND current_period_end <= purchase.paid_period_end;
    END IF;
END;
$$;

ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS reconciliation_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_paypal_pending_reconciliation
ON recharge_orders(COALESCE(reconciliation_at, created_at))
WHERE payment_method='paypal' AND status='Pending' AND external_ref_id IS NOT NULL AND checkout_id IS NOT NULL;

ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS prior_order_status VARCHAR(20) NOT NULL DEFAULT 'Success';
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

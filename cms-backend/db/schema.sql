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
    status VARCHAR(20) DEFAULT 'Ongoing', -- Ongoing / Completed
    synopsis TEXT,
    genres VARCHAR(50)[] DEFAULT '{}',
    word_count INT DEFAULT 0,
    view_count BIGINT DEFAULT 0,
    coin_cost_per_thousand INT DEFAULT 500,
    start_pay_chapter_index INT DEFAULT 3,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 增量补齐字段
ALTER TABLE novels ADD COLUMN IF NOT EXISTS coin_cost_per_thousand INT DEFAULT 500;
ALTER TABLE novels ADD COLUMN IF NOT EXISTS start_pay_chapter_index INT DEFAULT 3;

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
    type VARCHAR(20) NOT NULL, -- 'single', 'vip', 'whole_book'
    coins INT DEFAULT 0,
    bonus INT DEFAULT 0,
    vip_duration VARCHAR(10) DEFAULT '', -- 'day', 'week', 'month', 'year'
    vip_name VARCHAR(50) DEFAULT '',
    vip_desc VARCHAR(255) DEFAULT '',
    price VARCHAR(20) NOT NULL,
    price_cents INT NOT NULL,
    CONSTRAINT unique_template_slot UNIQUE (template_id, slot_index)
);

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
    recharge_template_id INT REFERENCES recharge_templates(id) ON DELETE SET NULL,
    recharge_slot_id INT,
    promotion_link_id INT REFERENCES promotion_links(id) ON DELETE SET NULL,
    novel_id INT REFERENCES novels(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

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

-- GIN 模糊搜索三元索引
CREATE INDEX IF NOT EXISTS idx_novels_title_trgm ON novels USING gin (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_novels_author_trgm ON novels USING gin (author gin_trgm_ops);

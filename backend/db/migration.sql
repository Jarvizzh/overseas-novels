-- Drop tables if exist
DROP SEQUENCE IF EXISTS novels_id_seq CASCADE;
DROP TABLE IF EXISTS unlock_records CASCADE;
DROP TABLE IF EXISTS transactions CASCADE;
DROP TABLE IF EXISTS wallets CASCADE;
DROP TABLE IF EXISTS bookshelves CASCADE;
DROP TABLE IF EXISTS chapters CASCADE;
DROP TABLE IF EXISTS novels CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS recharge_orders CASCADE;
DROP TABLE IF EXISTS gift_code_redemptions CASCADE;
DROP TABLE IF EXISTS gift_codes CASCADE;
DROP TABLE IF EXISTS recommendations CASCADE;
DROP TABLE IF EXISTS promotion_links CASCADE;
DROP TABLE IF EXISTS fb_pixels CASCADE;
DROP TABLE IF EXISTS admin_audit_logs CASCADE;
DROP TABLE IF EXISTS admins CASCADE;
DROP TABLE IF EXISTS system_configs CASCADE;

-- Users Table
CREATE TABLE users (
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

CREATE SEQUENCE novels_id_seq START WITH 10000001;

-- Novels Table
CREATE TABLE novels (
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
    coin_cost_per_thousand INT DEFAULT NULL,
    start_pay_chapter_index INT DEFAULT 10,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Chapters Table
CREATE TABLE chapters (
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

-- Bookshelves (Shelf + Reading Progress) Table
CREATE TABLE bookshelves (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    novel_id INT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    chapter_index INT DEFAULT 0,
    scroll_offset_percentage DECIMAL(5,4) DEFAULT 0.0000,
    in_shelf BOOLEAN DEFAULT TRUE,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_user_novel UNIQUE (user_id, novel_id)
);

-- Wallets Table
CREATE TABLE wallets (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    charged_coins INT DEFAULT 0,
    bonus_coins INT DEFAULT 0,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Gold Coin Transactions Table
CREATE TABLE transactions (
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

-- Chapter Unlock Records Table
CREATE TABLE unlock_records (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    novel_id INT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    chapter_index INT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_user_novel_chapter_unlock UNIQUE (user_id, novel_id, chapter_index)
);

-- Indexes for performance (especially high concurrent read queries)
CREATE INDEX idx_chapters_novel_index ON chapters(novel_id, chapter_index);
CREATE INDEX idx_bookshelves_user ON bookshelves(user_id);
CREATE INDEX idx_unlock_records_user_novel ON unlock_records(user_id, novel_id);
CREATE INDEX idx_transactions_user ON transactions(user_id);

-- =========================================================================
-- CMS & Operational Schema Enhancements
-- =========================================================================

-- 1. Admin Users Table (For CMS authentication)
CREATE TABLE IF NOT EXISTS admins (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(50) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    nickname VARCHAR(50),
    role VARCHAR(20) NOT NULL, -- SuperAdmin, Editor, Support, Finance
    status SMALLINT DEFAULT 1, -- 1-Active, 2-Suspended
    tfa_secret VARCHAR(100),   -- 2FA Authentication secret
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 2. Recharge Orders Table (Stripe & PayPal tracking + UTM/FB metadata)
CREATE SEQUENCE IF NOT EXISTS recharge_orders_id_seq START WITH 10000;

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
    promotion_link_id INT REFERENCES promotion_links(id) ON DELETE SET NULL,
    novel_id INT REFERENCES novels(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 3. Promotion Recommendation Lists (Homepage Layouts)
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

-- 4. Gift Codes Table
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

-- 5. Gift Code Redemptions Log (Prevent double-dipping)
CREATE TABLE IF NOT EXISTS gift_code_redemptions (
    id SERIAL PRIMARY KEY,
    gift_code_id INT NOT NULL REFERENCES gift_codes(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redeemed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_user_gift_code UNIQUE (user_id, gift_code_id)
);

-- 6. Admin Audit Logs (Trace operations changes)
CREATE TABLE IF NOT EXISTS admin_audit_logs (
    id BIGSERIAL PRIMARY KEY,
    admin_id VARCHAR(36) NOT NULL REFERENCES admins(id) ON DELETE SET NULL,
    action VARCHAR(50) NOT NULL,         -- novel_edit, refund_order, ban_user, wallet_grant
    target_id VARCHAR(100),              -- ID of the affected record
    before_data JSONB,
    after_data JSONB,
    ip_address VARCHAR(45),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Indexing for CMS quick lookups
CREATE INDEX IF NOT EXISTS idx_recharge_orders_user ON recharge_orders(user_id);
CREATE INDEX IF NOT EXISTS idx_recharge_orders_status ON recharge_orders(status);
CREATE INDEX IF NOT EXISTS idx_recommendations_group ON recommendations(group_name);
CREATE INDEX IF NOT EXISTS idx_gift_code_redemptions_user ON gift_code_redemptions(user_id);
CREATE INDEX IF NOT EXISTS idx_admin_audit_logs_admin ON admin_audit_logs(admin_id);

-- System Configs Table
CREATE TABLE IF NOT EXISTS system_configs (
    key VARCHAR(100) PRIMARY KEY,
    value VARCHAR(255) NOT NULL
);

INSERT INTO system_configs (key, value) VALUES ('global_coin_cost_per_thousand', '5') ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS fb_pixels (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    pixel_id VARCHAR(100) NOT NULL UNIQUE,
    access_token TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

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
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

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



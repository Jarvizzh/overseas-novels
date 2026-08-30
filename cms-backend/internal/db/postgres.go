package db

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"star-novel-cms/internal/config"
)

var DB *pgxpool.Pool

func InitDB() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(config.AppConfig.DatabaseURL)
	if err != nil {
		log.Fatalf("Unable to parse database URL: %v", err)
	}

	cfg.MaxConns = 15
	cfg.MinConns = 2
	cfg.MaxConnIdleTime = 15 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Database ping failed: %v", err)
	}

	log.Println("CMS Database connection pool initialized successfully")
	DB = pool

	// Auto-migrate recharge_templates and recharge_slots tables
	// Drop old flat structure if it exists to allow reconstruction
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS recharge_slots CASCADE;")
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS recharge_templates CASCADE;")

	// Add coin_cost_per_thousand and start_pay_chapter_index columns to novels if they don't exist
	_, _ = pool.Exec(ctx, `
		ALTER TABLE novels 
		ADD COLUMN IF NOT EXISTS coin_cost_per_thousand INT DEFAULT NULL,
		ADD COLUMN IF NOT EXISTS start_pay_chapter_index INT DEFAULT 3;
		ALTER TABLE novels ALTER COLUMN coin_cost_per_thousand SET DEFAULT NULL;
		ALTER TABLE novels ALTER COLUMN start_pay_chapter_index SET DEFAULT 3;
	`)

	// Create system_configs table and seed global billing settings
	_, _ = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS system_configs (
			key VARCHAR(100) PRIMARY KEY,
			value VARCHAR(255) NOT NULL
		);
		INSERT INTO system_configs (key, value) VALUES ('global_coin_cost_per_thousand', '5') ON CONFLICT DO NOTHING;
		INSERT INTO system_configs (key, value) VALUES ('global_start_pay_chapter_index', '3') ON CONFLICT DO NOTHING;
	`)

	// 1. Create recharge_templates table
	createTemplatesQuery := `
	CREATE TABLE IF NOT EXISTS recharge_templates (
		id SERIAL PRIMARY KEY,
		name VARCHAR(100) NOT NULL,
		is_default BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = pool.Exec(ctx, createTemplatesQuery)
	if err != nil {
		log.Fatalf("Failed to create recharge_templates table: %v", err)
	}

	// 2. Create recharge_slots table (Exactly 6 slots per template)
	createSlotsQuery := `
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
	);`
	_, err = pool.Exec(ctx, createSlotsQuery)
	if err != nil {
		log.Fatalf("Failed to create recharge_slots table: %v", err)
	}

	// 3. Create promotion_links table
	createPromoQuery := `
	CREATE TABLE IF NOT EXISTS promotion_links (
		id SERIAL PRIMARY KEY,
		name VARCHAR(255) DEFAULT '',
		novel_id INT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
		novel_title VARCHAR(255) NOT NULL,
		chapter_index INT,
		utm_source VARCHAR(100),
		utm_campaign VARCHAR(100),
		generated_url TEXT NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = pool.Exec(ctx, createPromoQuery)
	if err != nil {
		log.Fatalf("Failed to create promotion_links table: %v", err)
	}

	// Add name column if it doesn't exist
	_, _ = pool.Exec(ctx, "ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS name VARCHAR(255) DEFAULT ''")

	// 4. Create fb_pixels table
	createPixelsQuery := `
	CREATE TABLE IF NOT EXISTS fb_pixels (
		id SERIAL PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		pixel_id VARCHAR(100) NOT NULL UNIQUE,
		access_token TEXT NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = pool.Exec(ctx, createPixelsQuery)
	if err != nil {
		log.Fatalf("Failed to create fb_pixels table: %v", err)
	}

	// 5. Link promotion_links to fb_pixels
	_, _ = pool.Exec(ctx, "ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS fb_pixel_id INT REFERENCES fb_pixels(id) ON DELETE SET NULL")
	_, _ = pool.Exec(ctx, "ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS recharge_template_id INT REFERENCES recharge_templates(id) ON DELETE SET NULL")
	_, _ = pool.Exec(ctx, "ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS coin_cost_per_thousand INT DEFAULT NULL")
	_, _ = pool.Exec(ctx, "ALTER TABLE promotion_links ADD COLUMN IF NOT EXISTS start_pay_chapter_index INT DEFAULT NULL")

	// Seed templates if table is empty
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM recharge_templates").Scan(&count)
	if err == nil && count == 0 {
		var templateID int
		err = pool.QueryRow(ctx, `
			INSERT INTO recharge_templates (name, is_default)
			VALUES ('默认充值模板', TRUE)
			RETURNING id
		`).Scan(&templateID)
		if err != nil {
			log.Printf("Warning: Failed to seed default recharge template: %v", err)
		} else {
			seedSlotsQuery := `
			INSERT INTO recharge_slots (template_id, slot_index, type, coins, bonus, vip_duration, vip_name, vip_desc, price, price_cents)
			VALUES
			($1, 1, 'single', 499, 50, '', '', '', '$4.99', 499),
			($1, 2, 'single', 999, 150, '', '', '', '$9.99', 999),
			($1, 3, 'single', 1999, 400, '', '', '', '$19.99', 1999),
			($1, 4, 'single', 4999, 1200, '', '', '', '$49.99', 4999),
			($1, 5, 'vip', 299, 0, 'week', 'VIP Weekly', 'Get 299 Coins + 50/day', '$2.99', 299),
			($1, 6, 'vip', 999, 0, 'month', 'VIP Monthly', 'Get 999 Coins + 80/day', '$9.99', 999);`
			_, err = pool.Exec(ctx, seedSlotsQuery, templateID)
			if err != nil {
				log.Printf("Warning: Failed to seed default slots: %v", err)
			} else {
				log.Println("Seeded default recharge templates and 6 slots successfully")
			}
		}
	}

	// 6. Create system_domains table
	createDomainsQuery := `
	CREATE TABLE IF NOT EXISTS system_domains (
		id SERIAL PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		domain VARCHAR(255) NOT NULL UNIQUE,
		type VARCHAR(20) NOT NULL DEFAULT 'sub',
		status SMALLINT DEFAULT 1,
		is_default BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = pool.Exec(ctx, createDomainsQuery)
	if err != nil {
		log.Fatalf("Failed to create system_domains table: %v", err)
	}

	// Seed default domain if table is empty
	var domainCount int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM system_domains").Scan(&domainCount)
	if err == nil && domainCount == 0 {
		_, err = pool.Exec(ctx, `
			INSERT INTO system_domains (name, domain, type, status, is_default)
			VALUES ('主站默认落地页', 'h5.star-novel.com', 'main', 1, TRUE)
		`)
		if err != nil {
			log.Printf("Warning: Failed to seed default domain: %v", err)
		} else {
			log.Println("Seeded default domain h5.star-novel.com successfully")
		}
	}

	// 7. Create Meta Ad Statistic tables
	createMetaTablesQuery := `
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
	CREATE INDEX IF NOT EXISTS idx_meta_camp_acc ON meta_campaigns(account_id);

	CREATE TABLE IF NOT EXISTS meta_adsets (
		id VARCHAR(64) PRIMARY KEY,
		campaign_id VARCHAR(64) NOT NULL,
		account_id VARCHAR(64) NOT NULL,
		name VARCHAR(255) NOT NULL,
		daily_budget NUMERIC(12,4) DEFAULT 0,
		status VARCHAR(50) DEFAULT 'ACTIVE',
		updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_meta_adset_camp ON meta_adsets(campaign_id);
	CREATE INDEX IF NOT EXISTS idx_meta_adset_acc ON meta_adsets(account_id);

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
	CREATE INDEX IF NOT EXISTS idx_meta_ad_adset ON meta_ads(ad_set_id);
	CREATE INDEX IF NOT EXISTS idx_meta_ad_camp ON meta_ads(campaign_id);
	CREATE INDEX IF NOT EXISTS idx_meta_ad_acc ON meta_ads(account_id);

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
	CREATE INDEX IF NOT EXISTS idx_meta_insight_entity_date ON meta_daily_insights(entity_level, entity_id, stat_date);

	CREATE TABLE IF NOT EXISTS meta_configs (
		key VARCHAR(64) PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = pool.Exec(ctx, createMetaTablesQuery)
	if err != nil {
		log.Printf("Warning: Failed to create meta tables: %v", err)
	}
}

func CloseDB() {
	if DB != nil {
		DB.Close()
		log.Println("CMS Database connection pool closed")
	}
}

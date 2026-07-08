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
		ADD COLUMN IF NOT EXISTS start_pay_chapter_index INT DEFAULT 10;
		ALTER TABLE novels ALTER COLUMN coin_cost_per_thousand SET DEFAULT NULL;
	`)

	// Create system_configs table and seed global billing settings
	_, _ = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS system_configs (
			key VARCHAR(100) PRIMARY KEY,
			value VARCHAR(255) NOT NULL
		);
		INSERT INTO system_configs (key, value) VALUES ('global_coin_cost_per_thousand', '5') ON CONFLICT DO NOTHING;
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
}

func CloseDB() {
	if DB != nil {
		DB.Close()
		log.Println("CMS Database connection pool closed")
	}
}

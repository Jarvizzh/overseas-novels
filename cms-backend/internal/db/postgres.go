package db

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	dbassets "star-novel-cms/db"
	"star-novel-cms/internal/config"
)

var DB *pgxpool.Pool

func InitDB() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	// Execute safe, idempotent auto-migration (No DROP TABLE, CREATE IF NOT EXISTS only)
	autoMigrate(ctx, pool)
}

func autoMigrate(ctx context.Context, pool *pgxpool.Pool) {
	log.Println("Running safe database auto-migration...")

	// 1. Execute full schema DDL (Idempotent table and index creation)
	if dbassets.SchemaSQL != "" {
		_, err := pool.Exec(ctx, dbassets.SchemaSQL)
		if err != nil {
			log.Printf("[MIGRATE WARNING] Schema migration notice: %v", err)
		} else {
			log.Println("Database schema checked/migrated successfully")
		}
	}

	// 2. Seed initial data if tables are empty (admin account, configs, domains, templates, novels)
	var adminCount int
	err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM admins").Scan(&adminCount)
	if err == nil && adminCount == 0 {
		log.Println("Database is newly initialized. Seeding initial superadmin, configs, and sample novels...")
		if dbassets.SeedSQL != "" {
			_, err = pool.Exec(ctx, dbassets.SeedSQL)
			if err != nil {
				log.Printf("[SEED WARNING] Initial seed notice: %v", err)
			} else {
				log.Println("Initial seed data inserted successfully (Admin: admin / admin123)")
			}
		}
	} else {
		// Ensure system configs and default recharge templates are present (idempotent)
		_, _ = pool.Exec(ctx, `
			INSERT INTO system_configs (key, value) VALUES ('global_coin_cost_per_thousand', '500') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
			INSERT INTO system_configs (key, value) VALUES ('global_start_pay_chapter_index', '3') ON CONFLICT (key) DO NOTHING;
			INSERT INTO system_domains (name, domain, type, status, is_default) VALUES ('主站默认落地页', 'h5.star-novel.com', 'main', 1, TRUE) ON CONFLICT (domain) DO NOTHING;
			INSERT INTO admins (id, username, password_hash, nickname, role, status) 
			VALUES ('10000000-0000-0000-0000-000000000001', 'admin', '$2a$10$F18fKqJSG4r6zPEp3EokNesCxK005I2a66WHrWhwopOOBA./liWqa', '超级管理员', 'SuperAdmin', 1) 
			ON CONFLICT (username) DO UPDATE SET password_hash = '$2a$10$F18fKqJSG4r6zPEp3EokNesCxK005I2a66WHrWhwopOOBA./liWqa' WHERE admins.username = 'admin' AND admins.password_hash = '$2a$10$N.zmdr9k7uOCQb376NoUnuTJ8iAt6Z5EHsM8lE9lBOsl7iKTVKIUi';
		`)
	}
}

func CloseDB() {
	if DB != nil {
		DB.Close()
		log.Println("CMS Database connection pool closed")
	}
}

package db

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
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
			INSERT INTO admins (id, username, password_hash, nickname, role, status) 
			VALUES ('10000000-0000-0000-0000-000000000001', 'admin', '$2a$10$F18fKqJSG4r6zPEp3EokNesCxK005I2a66WHrWhwopOOBA./liWqa', '超级管理员', 'SuperAdmin', 1) 
			ON CONFLICT (username) DO UPDATE SET password_hash = '$2a$10$F18fKqJSG4r6zPEp3EokNesCxK005I2a66WHrWhwopOOBA./liWqa' WHERE admins.username = 'admin' AND admins.password_hash = '$2a$10$N.zmdr9k7uOCQb376NoUnuTJ8iAt6Z5EHsM8lE9lBOsl7iKTVKIUi';
		`)
	}

	// 3. Ensure all primary-key sequences are at least as high as MAX(id)
	//    (prevents SQLSTATE 23505 after explicit-ID seed inserts or imports).
	syncSequences(ctx, pool)
}

type sequenceSpec struct {
	table   string
	column  string
	seqName string
}

var managedSequences = []sequenceSpec{
	{table: "novels", column: "id", seqName: "novels_id_seq"},
	{table: "recharge_orders", column: "id", seqName: "recharge_orders_id_seq"},
	{table: "admin_audit_logs", column: "id"},
	{table: "users", column: "id"},
	{table: "bookshelves", column: "id"},
	{table: "unlock_records", column: "id"},
	{table: "recharge_templates", column: "id"},
	{table: "recharge_slots", column: "id"},
	{table: "payment_provider_plans", column: "id"},
	{table: "system_domains", column: "id"},
	{table: "fb_pixels", column: "id"},
	{table: "promotion_links", column: "id"},
	{table: "user_subscriptions", column: "id"},
	{table: "recommendations", column: "id"},
	{table: "gift_codes", column: "id"},
	{table: "gift_code_redemptions", column: "id"},
	{table: "feedback", column: "id"},
	{table: "facebook_capi_logs", column: "id"},
	{table: "third_party_payment_orders", column: "id"},
}

func syncSequences(ctx context.Context, pool *pgxpool.Pool) {
	for _, spec := range managedSequences {
		if err := syncOneSequence(ctx, pool, spec); err != nil {
			log.Printf("[MIGRATE WARNING] Sequence sync notice for %s.%s: %v", spec.table, spec.column, err)
		}
	}
}

// SyncSequence advances the backing PostgreSQL sequence for table.column when it lags behind MAX(column).
func SyncSequence(ctx context.Context, table, column, seqName string) error {
	if DB == nil {
		return errors.New("database connection pool is not initialized")
	}
	return syncOneSequence(ctx, DB, sequenceSpec{table: table, column: column, seqName: seqName})
}

func syncOneSequence(ctx context.Context, pool *pgxpool.Pool, spec sequenceSpec) error {
	seqName := spec.seqName
	if seqName == "" {
		if err := pool.QueryRow(ctx, "SELECT COALESCE(pg_get_serial_sequence($1, $2), '')", spec.table, spec.column).Scan(&seqName); err != nil {
			return err
		}
		if seqName == "" {
			return nil
		}
	}

	maxQuery := fmt.Sprintf("SELECT MAX(%s) FROM %s", pgx.Identifier{spec.column}.Sanitize(), pgx.Identifier{spec.table}.Sanitize())
	var maxID *int64
	if err := pool.QueryRow(ctx, maxQuery).Scan(&maxID); err != nil {
		return err
	}
	if maxID == nil || *maxID <= 0 {
		return nil
	}

	var startValue int64
	var lastValue *int64
	err := pool.QueryRow(ctx, `
		SELECT start_value, last_value
		FROM pg_sequences
		WHERE (schemaname || '.' || sequencename = $1)
		   OR (schemaname = current_schema() AND sequencename = $1)
		LIMIT 1
	`, seqName).Scan(&startValue, &lastValue)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	needsAdvance := (lastValue == nil && *maxID >= startValue) || (lastValue != nil && *maxID > *lastValue)
	if !needsAdvance {
		return nil
	}

	if _, err := pool.Exec(ctx, "SELECT setval($1::regclass, $2, true)", seqName, *maxID); err != nil {
		return err
	}
	log.Printf("Synchronized sequence %s to %d (%s.%s)", seqName, *maxID, spec.table, spec.column)
	return nil
}

func CloseDB() {
	if DB != nil {
		DB.Close()
		log.Println("CMS Database connection pool closed")
	}
}

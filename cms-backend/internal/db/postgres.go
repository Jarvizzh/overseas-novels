package db

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	dbassets "star-novel-cms/db"
	"star-novel-cms/internal/config"
)

var DB *pgxpool.Pool

func InitDB(migrate bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(config.AppConfig.DatabaseURL)
	if err != nil {
		log.Fatalf("Unable to parse database URL: %v", err)
	}

	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
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

	check := CheckSchema
	if migrate {
		check = func(ctx context.Context) error { return autoMigrate(ctx, pool) }
	}
	if err := check(ctx); err != nil {
		pool.Close()
		log.Fatalf("Database schema check failed: %v", err)
	}
}

const (
	baselineSchemaVersion       int64 = 2026111701
	chapterSchemaVersion        int64 = 2026111702
	readerPerformanceVersion    int64 = 2026111703
	readerQueueVersion          int64 = 2026111704
	conversionSchemaVersion     int64 = 2026111901
	readerConversionVersion     int64 = 2026111902
	legacyConversionVersion     int64 = 2026111903
	conversionReconcileVersion  int64 = 2026111904
	conversionOperationsVersion int64 = 2026111905
	conversionReviewVersion     int64 = 2026111906
	sinanConversionVersion      int64 = 2026111907
	sinanReviewVersion          int64 = 2026111908
	RequiredSchemaVersion       int64 = 2026111909
)

func autoMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '60s'"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(2026111701)"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '3s'"); err != nil {
		return err
	}
	var ledger *string
	if err := tx.QueryRow(ctx, "SELECT to_regclass(format('%I.reader_schema_migrations', current_schema()))::text").Scan(&ledger); err != nil {
		return err
	}
	var version int64
	if ledger != nil {
		if err := tx.QueryRow(ctx, "SELECT COALESCE(max(version),0) FROM reader_schema_migrations").Scan(&version); err != nil {
			return err
		}
		if version > RequiredSchemaVersion {
			return fmt.Errorf("schema %d newer than CMS %d", version, RequiredSchemaVersion)
		}
	}

	if version == RequiredSchemaVersion {
		return tx.Commit(ctx)
	}
	if version < baselineSchemaVersion {
		if err := applyBaseline(ctx, tx); err != nil {
			return err
		}
	}
	if version < chapterSchemaVersion {
		if _, err := tx.Exec(ctx, dbassets.ChapterPublicationSQL); err != nil {
			return fmt.Errorf("schema migration %d: %w", chapterSchemaVersion, err)
		}
	}
	if version < readerPerformanceVersion {
		if _, err := tx.Exec(ctx, dbassets.ReaderPerformanceSQL); err != nil {
			return fmt.Errorf("schema migration %d: %w", readerPerformanceVersion, err)
		}
	}
	if version < readerQueueVersion {
		if _, err := tx.Exec(ctx, dbassets.ReaderQueueAndShelfSQL); err != nil {
			return fmt.Errorf("schema migration %d: %w", readerQueueVersion, err)
		}
	}
	if version < conversionSchemaVersion {
		if _, err := tx.Exec(ctx, dbassets.ConversionReportingSQL); err != nil {
			return fmt.Errorf("schema migration %d: %w", conversionSchemaVersion, err)
		}
	}
	if version < readerConversionVersion {
		if _, err := tx.Exec(ctx, dbassets.ReaderConversionSQL); err != nil {
			return fmt.Errorf("schema migration %d: %w", readerConversionVersion, err)
		}
	}
	if version < legacyConversionVersion {
		if _, err := tx.Exec(ctx, dbassets.LegacyConversionSQL); err != nil {
			return fmt.Errorf("schema migration %d: %w", legacyConversionVersion, err)
		}
	}
	if version < conversionReconcileVersion {
		if _, err := tx.Exec(ctx, dbassets.ConversionReconciliationSQL); err != nil {
			return fmt.Errorf("schema migration %d: %w", conversionReconcileVersion, err)
		}
	}
	if version < conversionOperationsVersion {
		if _, err := tx.Exec(ctx, dbassets.ConversionOperationsSQL); err != nil {
			return fmt.Errorf("schema migration %d: %w", conversionOperationsVersion, err)
		}
	}
	if version < conversionReviewVersion {
		if _, err := tx.Exec(ctx, dbassets.ConversionReviewSQL); err != nil {
			return fmt.Errorf("schema migration %d: %w", conversionReviewVersion, err)
		}
	}
	if version < sinanConversionVersion {
		if _, err := tx.Exec(ctx, dbassets.SinanConversionSQL); err != nil {
			return fmt.Errorf("schema migration %d: %w", sinanConversionVersion, err)
		}
	}
	if version < sinanReviewVersion {
		if _, err := tx.Exec(ctx, dbassets.SinanReviewSQL); err != nil {
			return fmt.Errorf("schema migration %d: %w", sinanReviewVersion, err)
		}
	}
	if _, err := tx.Exec(ctx, dbassets.SinanRecheckSQL); err != nil {
		return fmt.Errorf("schema migration %d: %w", RequiredSchemaVersion, err)
	}
	return tx.Commit(ctx)
}

func applyBaseline(ctx context.Context, tx pgx.Tx) error {
	log.Println("Running safe database auto-migration...")

	// 1. Execute full schema DDL (Idempotent table and index creation)
	if dbassets.SchemaSQL != "" {
		_, err := tx.Exec(ctx, dbassets.SchemaSQL)
		if err != nil {
			return fmt.Errorf("schema migration: %w", err)
		} else {
			log.Println("Database schema checked/migrated successfully")
		}
	}

	// 2. Seed initial data if tables are empty (admin account, configs, domains, templates, novels)
	var adminCount int
	err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM admins").Scan(&adminCount)
	if err != nil {
		return fmt.Errorf("read admins: %w", err)
	}
	if adminCount == 0 {
		log.Println("Database is newly initialized. Seeding initial superadmin, configs, and sample novels...")
		if dbassets.SeedSQL != "" {
			_, err = tx.Exec(ctx, dbassets.SeedSQL)
			if err != nil {
				return fmt.Errorf("seed: %w", err)
			} else {
				log.Println("Initial seed data inserted successfully (Admin: admin / admin123)")
			}
		}
	} else {
		// Ensure system configs and default recharge templates are present (idempotent)
		_, err = tx.Exec(ctx, `
			INSERT INTO system_configs (key, value) VALUES ('global_coin_cost_per_thousand', '500') ON CONFLICT (key) DO NOTHING;
			INSERT INTO system_configs (key, value) VALUES ('global_start_pay_chapter_index', '3') ON CONFLICT (key) DO NOTHING;
			INSERT INTO admins (id, username, password_hash, nickname, role, status) 
			VALUES ('10000000-0000-0000-0000-000000000001', 'admin', '$2a$10$F18fKqJSG4r6zPEp3EokNesCxK005I2a66WHrWhwopOOBA./liWqa', '超级管理员', 'SuperAdmin', 1) 
			ON CONFLICT (username) DO UPDATE SET password_hash = '$2a$10$F18fKqJSG4r6zPEp3EokNesCxK005I2a66WHrWhwopOOBA./liWqa' WHERE admins.username = 'admin' AND admins.password_hash = '$2a$10$N.zmdr9k7uOCQb376NoUnuTJ8iAt6Z5EHsM8lE9lBOsl7iKTVKIUi';
		`)
		if err != nil {
			return fmt.Errorf("ensure defaults: %w", err)
		}
	}

	// 3. Ensure all primary-key sequences are at least as high as MAX(id)
	//    (prevents SQLSTATE 23505 after explicit-ID seed inserts or imports).
	if err := syncSequences(ctx, tx); err != nil {
		return err
	}
	return nil
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
	{table: "third_party_payment_orders", column: "id"},
}

type migrationDB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func syncSequences(ctx context.Context, pool migrationDB) error {
	for _, spec := range managedSequences {
		if err := syncOneSequence(ctx, pool, spec); err != nil {
			return fmt.Errorf("sequence %s.%s: %w", spec.table, spec.column, err)
		}
	}
	return nil
}

// SyncSequence advances the backing PostgreSQL sequence for table.column when it lags behind MAX(column).
func SyncSequence(ctx context.Context, table, column, seqName string) error {
	if DB == nil {
		return errors.New("database connection pool is not initialized")
	}
	return syncOneSequence(ctx, DB, sequenceSpec{table: table, column: column, seqName: seqName})
}

func syncOneSequence(ctx context.Context, pool migrationDB, spec sequenceSpec) error {
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

func CheckSchema(ctx context.Context) error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	var version int64
	if err := DB.QueryRow(ctx, "SELECT COALESCE(max(version),0) FROM reader_schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version != RequiredSchemaVersion {
		return fmt.Errorf("schema %d, required %d", version, RequiredSchemaVersion)
	}
	return nil
}

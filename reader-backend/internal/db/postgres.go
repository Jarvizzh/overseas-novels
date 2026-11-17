package db

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/config"
)

var DB *pgxpool.Pool

func InitDB() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(config.AppConfig.DatabaseURL)
	if err != nil {
		log.Fatalf("Unable to parse database URL: %v", err)
	}

	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.Tracer = resourceBudgets{acquire: 3 * time.Second, query: 10 * time.Second}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "15000"
	// Optimize connection pool for high-concurrency reader traffic
	cfg.MaxConns = 25
	cfg.MinConns = 5
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}

	// Ping connection
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Database ping failed: %v", err)
	}

	log.Println("Reader Database connection pool initialized successfully")
	DB = pool
	if err := CheckSchema(ctx); err != nil {
		pool.Close()
		log.Fatalf("Reader schema incompatible: %v", err)
	}
}

func CloseDB() {
	if DB != nil {
		DB.Close()
		log.Println("Database connection pool closed")
	}
}

// RequiredSchemaVersion is advanced with each reviewed unified schema release.
const RequiredSchemaVersion int64 = 2026111702

func CheckSchema(ctx context.Context) error {
	if DB == nil {
		return fmt.Errorf("database is not initialized")
	}
	var version int64
	if err := DB.QueryRow(ctx, "SELECT COALESCE(max(version),0) FROM reader_schema_migrations").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version != RequiredSchemaVersion {
		return fmt.Errorf("schema version %d, required %d", version, RequiredSchemaVersion)
	}
	return nil
}

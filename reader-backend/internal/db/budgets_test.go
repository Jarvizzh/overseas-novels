package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConnectionWaitAndQueryBudgets(t *testing.T) {
	dsn := os.Getenv("STAR_NOVEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.Tracer = resourceBudgets{acquire: 50 * time.Millisecond, query: 100 * time.Millisecond}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	started := time.Now()
	if _, err := pool.Acquire(context.Background()); err == nil {
		conn.Release()
		t.Fatal("unbounded pool wait accepted")
	}
	if time.Since(started) > time.Second {
		t.Fatal("pool wait exceeded budget")
	}
	conn.Release()
	if _, err := pool.Exec(context.Background(), "SELECT pg_sleep(1)"); err == nil {
		t.Fatal("unbounded query accepted")
	}
	// Ending an acquire must not cancel the later query's original context.
	var answer int
	if err := pool.QueryRow(context.Background(), "SELECT 42").Scan(&answer); err != nil || answer != 42 {
		t.Fatalf("pool unusable after timeout: %d %v", answer, err)
	}
}

func TestBackgroundPoolSaturationPreservesForegroundCapacity(t *testing.T) {
	dsn := os.Getenv("STAR_NOVEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	ctx := context.Background()
	beforeForeground, beforeBackground := DB, background
	defer func() { DB, background = beforeForeground, beforeBackground }()
	DB, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer DB.Close()
	background, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer background.Close()
	worker := BackgroundContext(ctx)
	conn, err := Pool(worker).Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	short, cancel := context.WithTimeout(worker, 30*time.Millisecond)
	defer cancel()
	if err := Pool(short).Ping(short); err == nil {
		t.Fatal("background exceeded its pool budget")
	}
	var got int
	if err := Pool(ctx).QueryRow(ctx, "SELECT 42").Scan(&got); err != nil || got != 42 {
		t.Fatalf("background saturated foreground: %d %v", got, err)
	}
	stats := ConnectionStats()
	if stats["background"].Canceled != 1 || stats["foreground"].Canceled != 0 {
		t.Fatalf("unexpected pool metrics: %+v", stats)
	}
}

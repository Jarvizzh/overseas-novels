package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"star-novel-cms/internal/config"
	"star-novel-cms/internal/db"
	"star-novel-cms/internal/novel"
	"star-novel-cms/internal/redis"
	"star-novel-cms/internal/storage"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "preview recalculated word counts and chapter prices without writing to PostgreSQL or Redis")
	novelID := flag.Int64("novel-id", 0, "optional novel ID to backfill (0 processes all novels)")
	concurrency := flag.Int("concurrency", 8, "concurrent chapter body reads per novel")
	skipRedis := flag.Bool("skip-redis", false, "skip connecting to Redis for cache invalidation")
	flag.Parse()

	config.LoadConfig()
	db.InitDB(false)
	defer db.CloseDB()

	if !*dryRun && !*skipRedis {
		redis.InitRedis()
		defer redis.CloseRedis()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	contentStorage, err := storage.NewContentStorage(config.AppConfig, db.DB)
	if err != nil {
		log.Fatalf("initialize content storage: %v", err)
	}
	log.Printf("Starting word-count backfill (storage=%s, dry_run=%t, novel_id=%d, concurrency=%d)",
		contentStorage.StorageType(), *dryRun, *novelID, *concurrency)

	_, err = novel.BackfillWordCounts(ctx, db.DB, contentStorage, novel.WordCountBackfillOptions{
		DryRun:      *dryRun,
		NovelID:     *novelID,
		Concurrency: *concurrency,
	})
	if err != nil {
		log.Fatalf("word-count backfill failed: %v", err)
	}
}

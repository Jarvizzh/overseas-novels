package redis

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"reader-backend/internal/config"
)

var RDB *redis.Client

func buildRedisOptions(raw string) *redis.Options {
	trimmed := strings.TrimSpace(raw)
	options := &redis.Options{Addr: trimmed}
	if strings.Contains(trimmed, "://") {
		if parsed, err := redis.ParseURL(trimmed); err == nil && parsed.Addr != "" {
			options = parsed
		}
	}
	options.ContextTimeoutEnabled = true
	options.PoolSize = 32
	options.MaxActiveConns = 32
	options.DialTimeout = 150 * time.Millisecond
	options.ReadTimeout = 120 * time.Millisecond
	options.WriteTimeout = 120 * time.Millisecond
	options.PoolTimeout = 100 * time.Millisecond
	options.MaxRetries = -1
	options.DialerRetries = 1
	return options
}

func InitRedis() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rdb := redis.NewClient(buildRedisOptions(config.AppConfig.RedisURL))

	// Test connection
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Redis connection failed: %v", err)
	}

	log.Println("Redis client initialized successfully")
	RDB = rdb
}

func CloseRedis() {
	if RDB != nil {
		RDB.Close()
		log.Println("Redis client closed")
	}
}

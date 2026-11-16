package redis

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"star-novel-cms/internal/config"
)

var RDB *redis.Client

func buildRedisOptions(raw string) *redis.Options {
	trimmed := strings.TrimSpace(raw)
	if strings.Contains(trimmed, "://") {
		if opt, err := redis.ParseURL(trimmed); err == nil && opt.Addr != "" {
			return opt
		}
	}
	return &redis.Options{
		Addr: trimmed,
		DB:   0,
	}
}

func InitRedis() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rdb := redis.NewClient(buildRedisOptions(config.AppConfig.RedisURL))

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Redis connection failed: %v", err)
	}

	log.Println("CMS Redis client initialized successfully")
	RDB = rdb
}

func CloseRedis() {
	if RDB != nil {
		RDB.Close()
		log.Println("CMS Redis client closed")
	}
}

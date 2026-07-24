package redis

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"reader-backend/internal/config"
)

var RDB *redis.Client

func InitRedis() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var rdb *redis.Client
	opt, err := redis.ParseURL(config.AppConfig.RedisURL)
	if err == nil {
		rdb = redis.NewClient(opt)
	} else {
		rdb = redis.NewClient(&redis.Options{
			Addr: config.AppConfig.RedisURL,
			DB:   0,
		})
	}

	// Test connection
	_, err = rdb.Ping(ctx).Result()
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

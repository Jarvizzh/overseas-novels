package redis

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"star-novel-cms/internal/config"
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

	_, err = rdb.Ping(ctx).Result()
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

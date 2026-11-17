package novel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"reader-backend/internal/model"
	redisclient "reader-backend/internal/redis"
)

type Cache interface {
	GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error)
	SetChaptersList(ctx context.Context, novelID int64, chapters []model.Chapter) error
	GetBody(ctx context.Context, key string) (string, error)
	SetBody(ctx context.Context, key, body string) error
}

var errCacheCoolingDown = errors.New("reading cache temporarily unavailable")

type redisCache struct {
	retryAfter atomic.Int64
}

func NewRedisCache() Cache {
	return &redisCache{}
}

func (c *redisCache) command(ctx context.Context, run func(context.Context) error) error {
	if time.Now().UnixNano() < c.retryAfter.Load() {
		return errCacheCoolingDown
	}
	if redisclient.RDB == nil {
		return errCacheCoolingDown
	}
	operation, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	err := run(operation)
	if err != nil && !errors.Is(err, redis.Nil) && ctx.Err() == nil {
		c.retryAfter.Store(time.Now().Add(time.Second).UnixNano())
	}
	return err
}

func (c *redisCache) get(ctx context.Context, key string) (string, error) {
	var data string
	err := c.command(ctx, func(ctx context.Context) error {
		var err error
		data, err = redisclient.RDB.Get(ctx, key).Result()
		return err
	})
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return data, err
}

func (c *redisCache) set(ctx context.Context, key string, data any, ttl time.Duration) error {
	return c.command(ctx, func(ctx context.Context) error {
		return redisclient.RDB.Set(ctx, key, data, ttl+time.Duration(rand.Int63n(int64(ttl/5)))).Err()
	})
}

func (c *redisCache) GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error) {
	key := fmt.Sprintf("reader:catalog:%d:%d", novelID, contentVersion(ctx))
	data, err := c.get(ctx, key)
	if err != nil || data == "" {
		return nil, err
	}
	var chapters []model.Chapter
	if err := json.Unmarshal([]byte(data), &chapters); err != nil {
		return nil, err
	}
	return chapters, nil
}

func (c *redisCache) SetChaptersList(ctx context.Context, novelID int64, chapters []model.Chapter) error {
	key := fmt.Sprintf("reader:catalog:%d:%d", novelID, contentVersion(ctx))
	data, err := json.Marshal(chapters)
	if err != nil {
		return err
	}
	return c.set(ctx, key, data, 30*time.Minute)
}

func (c *redisCache) GetBody(ctx context.Context, key string) (string, error) {
	return c.get(ctx, "reader:body:"+key)
}

func (c *redisCache) SetBody(ctx context.Context, key, body string) error {
	return c.set(ctx, "reader:body:"+key, body, 2*time.Hour)
}

type contentVersionKey struct{}

func withContentVersion(ctx context.Context, v int64) context.Context {
	return context.WithValue(ctx, contentVersionKey{}, v)
}
func contentVersion(ctx context.Context) int64 {
	v, _ := ctx.Value(contentVersionKey{}).(int64)
	return v
}

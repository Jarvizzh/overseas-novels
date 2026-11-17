package novel

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/redis/go-redis/v9"
	"reader-backend/internal/model"
	redisclient "reader-backend/internal/redis"
)

func getJitterTTL(baseDuration time.Duration) time.Duration {
	if baseDuration <= 0 {
		return baseDuration
	}
	// Add 0 ~ 20% random jitter to TTL to prevent cache avalanche
	jitter := time.Duration(rand.Int63n(int64(baseDuration / 5)))
	return baseDuration + jitter
}

type Cache interface {
	GetNovel(ctx context.Context, id int64) (*model.Novel, error)
	SetNovel(ctx context.Context, id int64, novel *model.Novel) error
	GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error)
	SetChaptersList(ctx context.Context, novelID int64, chapters []model.Chapter) error
	GetChapter(ctx context.Context, novelID int64, chapterIndex int) (*model.Chapter, error)
	SetChapter(ctx context.Context, novelID int64, chapterIndex int, chapter *model.Chapter) error

	IsChapterUnlocked(ctx context.Context, userID, novelID int64, chapterID string) (bool, error)
	SetChapterUnlocked(ctx context.Context, userID, novelID int64, chapterID string) error
}

type redisCache struct{}

func NewRedisCache() Cache {
	return &redisCache{}
}

func (c *redisCache) GetNovel(ctx context.Context, id int64) (*model.Novel, error) {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	key := fmt.Sprintf("novel:v%d:detail:%d", contentVersion(ctx), id)
	val, err := redisclient.RDB.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var novel model.Novel
	if err := json.Unmarshal([]byte(val), &novel); err != nil {
		return nil, err
	}
	return &novel, nil
}

func (c *redisCache) SetNovel(ctx context.Context, id int64, novel *model.Novel) error {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	key := fmt.Sprintf("novel:v%d:detail:%d", contentVersion(ctx), id)
	data, err := json.Marshal(novel)
	if err != nil {
		return err
	}
	// Expire novel detail in Redis after 6 hours + random jitter
	return redisclient.RDB.Set(ctx, key, data, getJitterTTL(6*time.Hour)).Err()
}

func (c *redisCache) GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error) {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	key := fmt.Sprintf("novel:v%d:chapters:%d", contentVersion(ctx), novelID)
	val, err := redisclient.RDB.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var chapters []model.Chapter
	if err := json.Unmarshal([]byte(val), &chapters); err != nil {
		return nil, err
	}
	return chapters, nil
}

func (c *redisCache) SetChaptersList(ctx context.Context, novelID int64, chapters []model.Chapter) error {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	key := fmt.Sprintf("novel:v%d:chapters:%d", contentVersion(ctx), novelID)
	data, err := json.Marshal(chapters)
	if err != nil {
		return err
	}
	// Expire chapters list in Redis after 6 hours + random jitter
	return redisclient.RDB.Set(ctx, key, data, getJitterTTL(6*time.Hour)).Err()
}

func (c *redisCache) GetChapter(ctx context.Context, novelID int64, chapterIndex int) (*model.Chapter, error) {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	key := fmt.Sprintf("novel:v%d:chapter-revision:%d:%d", contentVersion(ctx), novelID, chapterIndex)
	val, err := redisclient.RDB.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var chapter model.Chapter
	if err := json.Unmarshal([]byte(val), &chapter); err != nil {
		return nil, err
	}
	return &chapter, nil
}

func (c *redisCache) SetChapter(ctx context.Context, novelID int64, chapterIndex int, chapter *model.Chapter) error {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	key := fmt.Sprintf("novel:v%d:chapter-revision:%d:%d", contentVersion(ctx), novelID, chapterIndex)
	data, err := json.Marshal(chapter)
	if err != nil {
		return err
	}
	// Expire full chapter text in Redis after 2 hours + random jitter
	return redisclient.RDB.Set(ctx, key, data, getJitterTTL(2*time.Hour)).Err()
}

func (c *redisCache) IsChapterUnlocked(ctx context.Context, userID, novelID int64, chapterID string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	key := fmt.Sprintf("user:unlocks:chapter-id:%d:%d", userID, novelID)
	isMember, err := redisclient.RDB.SIsMember(ctx, key, chapterID).Result()
	if err != nil {
		return false, err
	}
	return isMember, nil
}

func (c *redisCache) SetChapterUnlocked(ctx context.Context, userID, novelID int64, chapterID string) error {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	key := fmt.Sprintf("user:unlocks:chapter-id:%d:%d", userID, novelID)
	pipe := redisclient.RDB.Pipeline()
	pipe.SAdd(ctx, key, chapterID)
	pipe.Expire(ctx, key, 24*time.Hour) // Extend expiration on activity
	_, err := pipe.Exec(ctx)
	return err
}

type contentVersionKey struct{}

func withContentVersion(ctx context.Context, v int64) context.Context {
	return context.WithValue(ctx, contentVersionKey{}, v)
}
func contentVersion(ctx context.Context) int64 {
	v, _ := ctx.Value(contentVersionKey{}).(int64)
	return v
}

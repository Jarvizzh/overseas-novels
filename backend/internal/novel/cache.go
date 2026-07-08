package novel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"novel-backend/internal/model"
	redisclient "novel-backend/internal/redis"
)

type Cache interface {
	GetNovel(ctx context.Context, id int64) (*model.Novel, error)
	SetNovel(ctx context.Context, id int64, novel *model.Novel) error
	GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error)
	SetChaptersList(ctx context.Context, novelID int64, chapters []model.Chapter) error
	GetChapter(ctx context.Context, novelID int64, chapterIndex int) (*model.Chapter, error)
	SetChapter(ctx context.Context, novelID int64, chapterIndex int, chapter *model.Chapter) error

	IsChapterUnlocked(ctx context.Context, userID, novelID int64, chapterIndex int) (bool, error)
	SetChapterUnlocked(ctx context.Context, userID, novelID int64, chapterIndex int) error
	SetUnlockedChaptersBatch(ctx context.Context, userID, novelID int64, chapterIndices []int) error
}

type redisCache struct{}

func NewRedisCache() Cache {
	return &redisCache{}
}

func (c *redisCache) GetNovel(ctx context.Context, id int64) (*model.Novel, error) {
	key := fmt.Sprintf("novel:detail:%d", id)
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
	key := fmt.Sprintf("novel:detail:%d", id)
	data, err := json.Marshal(novel)
	if err != nil {
		return err
	}
	// Expire novel detail in Redis after 6 hours
	return redisclient.RDB.Set(ctx, key, data, 6*time.Hour).Err()
}

func (c *redisCache) GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error) {
	key := fmt.Sprintf("novel:chapters:%d", novelID)
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
	key := fmt.Sprintf("novel:chapters:%d", novelID)
	data, err := json.Marshal(chapters)
	if err != nil {
		return err
	}
	// Expire chapters list in Redis after 6 hours
	return redisclient.RDB.Set(ctx, key, data, 6*time.Hour).Err()
}

func (c *redisCache) GetChapter(ctx context.Context, novelID int64, chapterIndex int) (*model.Chapter, error) {
	key := fmt.Sprintf("novel:chapter:%d:%d", novelID, chapterIndex)
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
	key := fmt.Sprintf("novel:chapter:%d:%d", novelID, chapterIndex)
	data, err := json.Marshal(chapter)
	if err != nil {
		return err
	}
	// Expire full chapter text in Redis after 2 hours
	return redisclient.RDB.Set(ctx, key, data, 2*time.Hour).Err()
}

func (c *redisCache) IsChapterUnlocked(ctx context.Context, userID, novelID int64, chapterIndex int) (bool, error) {
	key := fmt.Sprintf("user:unlocks:%d:%d", userID, novelID)
	isMember, err := redisclient.RDB.SIsMember(ctx, key, chapterIndex).Result()
	if err != nil {
		return false, err
	}
	return isMember, nil
}

func (c *redisCache) SetChapterUnlocked(ctx context.Context, userID, novelID int64, chapterIndex int) error {
	key := fmt.Sprintf("user:unlocks:%d:%d", userID, novelID)
	pipe := redisclient.RDB.Pipeline()
	pipe.SAdd(ctx, key, chapterIndex)
	pipe.Expire(ctx, key, 24*time.Hour) // Extend expiration on activity
	_, err := pipe.Exec(ctx)
	return err
}

func (c *redisCache) SetUnlockedChaptersBatch(ctx context.Context, userID, novelID int64, chapterIndices []int) error {
	if len(chapterIndices) == 0 {
		return nil
	}
	key := fmt.Sprintf("user:unlocks:%d:%d", userID, novelID)
	
	members := make([]interface{}, len(chapterIndices))
	for i, v := range chapterIndices {
		members[i] = v
	}

	pipe := redisclient.RDB.Pipeline()
	pipe.SAdd(ctx, key, members...)
	pipe.Expire(ctx, key, 24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

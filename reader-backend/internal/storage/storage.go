package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/config"
)

var (
	ErrContentNotFound = errors.New("chapter content not found")
)

// ChapterContentItem holds the chapter index and its full text content for batch operations.
type ChapterContentItem struct {
	ChapterIndex int
	Content      string
}

// ContentStorage defines the unified abstraction for chapter text storage.
type ContentStorage interface {
	// PutContent stores or updates the chapter text content.
	PutContent(ctx context.Context, novelID int64, chapterIndex int, content string) error

	// GetContent retrieves the chapter text content.
	GetContent(ctx context.Context, novelID int64, chapterIndex int) (string, error)

	// DeleteContent removes the chapter text content.
	DeleteContent(ctx context.Context, novelID int64, chapterIndex int) error

	// DeleteNovelContent removes all chapter content for a novel.
	DeleteNovelContent(ctx context.Context, novelID int64) error

	// BatchPutContent stores multiple chapters' content in batch or concurrently.
	BatchPutContent(ctx context.Context, novelID int64, items []ChapterContentItem) error

	// StorageType returns the name of the current storage implementation.
	StorageType() string
}

// NewContentStorage initializes the appropriate ContentStorage provider based on config.
func NewContentStorage(cfg *config.Config, dbPool *pgxpool.Pool) (ContentStorage, error) {
	storageType := strings.ToLower(strings.TrimSpace(cfg.StorageType))
	switch storageType {
	case "oss", "aliyun_oss", "aliyun":
		return NewAliyunOSSStorage(
			cfg.OSSEndpoint,
			cfg.OSSAccessKeyID,
			cfg.OSSAccessKeySecret,
			cfg.OSSBucket,
			cfg.OSSBasePath,
			dbPool,
		)
	case "postgres", "pg", "db", "database", "":
		if dbPool == nil {
			return nil, errors.New("database connection pool cannot be nil for postgres content storage")
		}
		return NewPostgresStorage(dbPool), nil
	default:
		return nil, fmt.Errorf("unsupported STORAGE_TYPE: %q (supported: 'postgres', 'oss')", cfg.StorageType)
	}
}

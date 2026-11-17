package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AliyunOSSStorage struct {
	client     *oss.Client
	bucket     *oss.Bucket
	bucketName string
	basePath   string
	fallbackDB *pgxpool.Pool
}

// NewAliyunOSSStorage constructs a new AliyunOSSStorage instance.
func NewAliyunOSSStorage(
	endpoint string,
	accessKeyID string,
	accessKeySecret string,
	bucketName string,
	basePath string,
	fallbackDB *pgxpool.Pool,
) (*AliyunOSSStorage, error) {
	if endpoint == "" {
		return nil, errors.New("OSS_ENDPOINT is required when STORAGE_TYPE=oss")
	}
	if accessKeyID == "" {
		return nil, errors.New("OSS_ACCESS_KEY_ID is required when STORAGE_TYPE=oss")
	}
	if accessKeySecret == "" {
		return nil, errors.New("OSS_ACCESS_KEY_SECRET is required when STORAGE_TYPE=oss")
	}
	if bucketName == "" {
		return nil, errors.New("OSS_BUCKET is required when STORAGE_TYPE=oss")
	}

	if basePath == "" {
		basePath = "novels"
	}
	basePath = strings.Trim(basePath, "/")

	client, err := oss.New(endpoint, accessKeyID, accessKeySecret, oss.Timeout(5, 10))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Aliyun OSS client: %w", err)
	}

	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to get Aliyun OSS bucket %q: %w", bucketName, err)
	}

	return &AliyunOSSStorage{
		client:     client,
		bucket:     bucket,
		bucketName: bucketName,
		basePath:   basePath,
		fallbackDB: fallbackDB,
	}, nil
}

func (s *AliyunOSSStorage) StorageType() string {
	return "oss"
}

func (s *AliyunOSSStorage) formatObjectKey(novelID int64, chapterIndex int) string {
	return fmt.Sprintf("%s/%d/%d.txt", s.basePath, novelID, chapterIndex)
}

func (s *AliyunOSSStorage) formatNovelPrefix(novelID int64) string {
	return fmt.Sprintf("%s/%d/", s.basePath, novelID)
}

func (s *AliyunOSSStorage) PutContent(ctx context.Context, novelID int64, chapterIndex int, content string) error {
	objectKey := s.formatObjectKey(novelID, chapterIndex)
	reader := strings.NewReader(content)
	return s.bucket.PutObject(objectKey, reader, oss.WithContext(ctx))
}

func (s *AliyunOSSStorage) GetContent(ctx context.Context, novelID int64, chapterIndex int) (string, error) {
	objectKey := s.formatObjectKey(novelID, chapterIndex)
	body, err := s.bucket.GetObject(objectKey, oss.WithContext(ctx))
	if err != nil {
		// If object is not found in OSS and a fallback DB is provided, check PostgreSQL
		var ossErr oss.ServiceError
		if errors.As(err, &ossErr) && (ossErr.StatusCode == 404 || ossErr.Code == "NoSuchKey") {
			if s.fallbackDB != nil {
				var dbContent string
				dbErr := s.fallbackDB.QueryRow(ctx, "SELECT content FROM chapters WHERE novel_id = $1 AND chapter_index = $2", novelID, chapterIndex).Scan(&dbContent)
				if dbErr == nil && dbContent != "" {
					return dbContent, nil
				}
			}
			return "", ErrContentNotFound
		}
		return "", fmt.Errorf("failed to get chapter content from OSS (%s): %w", objectKey, err)
	}
	defer body.Close()

	data, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("failed to read chapter content from OSS (%s): %w", objectKey, err)
	}

	return string(data), nil
}

func (s *AliyunOSSStorage) DeleteContent(ctx context.Context, novelID int64, chapterIndex int) error {
	objectKey := s.formatObjectKey(novelID, chapterIndex)
	return s.bucket.DeleteObject(objectKey, oss.WithContext(ctx))
}

func (s *AliyunOSSStorage) DeleteNovelContent(ctx context.Context, novelID int64) error {
	prefix := s.formatNovelPrefix(novelID)
	marker := ""

	for {
		lor, err := s.bucket.ListObjects(oss.Prefix(prefix), oss.Marker(marker), oss.MaxKeys(500), oss.WithContext(ctx))
		if err != nil {
			return fmt.Errorf("failed to list objects with prefix %q: %w", prefix, err)
		}

		if len(lor.Objects) > 0 {
			var keys []string
			for _, obj := range lor.Objects {
				keys = append(keys, obj.Key)
			}
			if _, err := s.bucket.DeleteObjects(keys, oss.WithContext(ctx)); err != nil {
				return fmt.Errorf("failed to delete OSS objects for novel %d: %w", novelID, err)
			}
		}

		if !lor.IsTruncated {
			break
		}
		marker = lor.NextMarker
	}

	return nil
}

func (s *AliyunOSSStorage) BatchPutContent(ctx context.Context, novelID int64, items []ChapterContentItem) error {
	if len(items) == 0 {
		return nil
	}

	const workerCount = 10
	itemChan := make(chan ChapterContentItem, len(items))
	for _, item := range items {
		itemChan <- item
	}
	close(itemChan)

	var wg sync.WaitGroup
	errChan := make(chan error, len(items))

	numWorkers := workerCount
	if len(items) < numWorkers {
		numWorkers = len(items)
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range itemChan {
				select {
				case <-ctx.Done():
					errChan <- ctx.Err()
					return
				default:
				}

				if err := s.PutContent(ctx, novelID, item.ChapterIndex, item.Content); err != nil {
					errChan <- fmt.Errorf("failed to upload chapter %d to OSS: %w", item.ChapterIndex, err)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return err
		}
	}

	return nil
}

// ReadChapterRevision reads only the referenced object and verifies its published digest.
func (s *AliyunOSSStorage) ReadChapterRevision(ctx context.Context, key, digest string) (string, error) {
	objectKey := s.basePath + "/" + key
	body, err := s.bucket.GetObject(objectKey, oss.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("read chapter object %q: %w", key, err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("read chapter object %q: %w", key, err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
		return "", fmt.Errorf("chapter object %q failed content verification", key)
	}
	return string(data), nil
}

// ReadLegacyChapter reads an index-keyed object used by chapters published before
// immutable references. The caller already holds the corresponding DB row.
func (s *AliyunOSSStorage) ReadLegacyChapter(ctx context.Context, novelID int64, chapterIndex int) (string, error) {
	key := s.formatObjectKey(novelID, chapterIndex)
	body, err := s.bucket.GetObject(key, oss.WithContext(ctx))
	if err != nil {
		var serviceErr oss.ServiceError
		if errors.As(err, &serviceErr) && serviceErr.StatusCode == 404 {
			return "", ErrContentNotFound
		}
		return "", fmt.Errorf("read legacy chapter %q: %w", key, err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("read legacy chapter %q: %w", key, err)
	}
	return string(data), nil
}

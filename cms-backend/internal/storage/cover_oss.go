package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	_ "golang.org/x/image/webp"
	"star-novel-cms/internal/config"
)

const (
	defaultCoverPrefix         = "covers"
	defaultCoverMaxUploadBytes = 5 * 1024 * 1024
	defaultCoverSignedTTLSec   = 3600
	maxCoverImageDimension     = 4096
	maxCoverImagePixels        = 4096 * 4096
)

var (
	ErrCoverOSSDisabled     = errors.New("封面 OSS 存储未启用")
	ErrCoverEmpty           = errors.New("请选择要上传的封面图片")
	ErrCoverTooLarge        = errors.New("封面图片大小不能超过 5MB")
	ErrCoverUnsupportedType = errors.New("仅支持 JPG、PNG、WebP 格式的封面图片")
	ErrCoverInvalidImage    = errors.New("无效或损坏的图片文件，或尺寸超过 4096x4096 限制")
)

type CoverSigner interface {
	SignCoverURL(raw string) string
}

type CoverStore interface {
	CoverSigner
	IsEnabled() bool
	Prefix() string
	MaxUploadBytes() int64
	IsValidCoverRef(ref string) bool
	UploadCover(ctx context.Context, data []byte) (coverKey string, previewURL string, err error)
	DeleteCover(ctx context.Context, rawRef string) error
}

type CoverOSSStore struct {
	enabled          bool
	prefix           string
	maxUploadBytes   int64
	signedTTL        int
	writeBucket      *oss.Bucket
	publicBucket     *oss.Bucket
	putObjectFunc    func(ctx context.Context, objectKey string, data []byte, contentType string) error
	deleteObjectFunc func(ctx context.Context, objectKey string) error
	signURLFunc      func(objectKey string, ttlSeconds int64) (string, error)
	nowFunc          func() time.Time
}

func NewCoverOSSStore(cfg *config.Config) (*CoverOSSStore, error) {
	prefix := normalizeCoverPrefix(cfg.CoverOSSPrefix)
	maxBytes := cfg.CoverMaxUploadBytes
	if maxBytes <= 0 {
		maxBytes = defaultCoverMaxUploadBytes
	}
	ttl := cfg.CoverOSSSignedURLTTL
	if ttl <= 0 {
		ttl = defaultCoverSignedTTLSec
	}

	if !cfg.CoverOSSEnabled {
		return &CoverOSSStore{
			enabled:        false,
			prefix:         prefix,
			maxUploadBytes: maxBytes,
			signedTTL:      ttl,
			nowFunc:        time.Now,
		}, nil
	}

	if strings.TrimSpace(cfg.CoverOSSRegion) == "" {
		return nil, errors.New("COVER_OSS_REGION is required when COVER_OSS_ENABLED=true")
	}
	if strings.TrimSpace(cfg.CoverOSSEndpoint) == "" {
		return nil, errors.New("COVER_OSS_ENDPOINT is required when COVER_OSS_ENABLED=true")
	}
	if strings.TrimSpace(cfg.CoverOSSBucket) == "" {
		return nil, errors.New("COVER_OSS_BUCKET is required when COVER_OSS_ENABLED=true")
	}
	if strings.TrimSpace(cfg.CoverOSSPublicBaseURL) == "" {
		return nil, errors.New("COVER_OSS_PUBLIC_BASE_URL is required when COVER_OSS_ENABLED=true")
	}
	if strings.TrimSpace(cfg.CoverOSSAccessKeyID) == "" || strings.TrimSpace(cfg.CoverOSSAccessKeySecret) == "" {
		return nil, errors.New("ALIBABA_CLOUD_ACCESS_KEY_ID and ALIBABA_CLOUD_ACCESS_KEY_SECRET are required when COVER_OSS_ENABLED=true")
	}

	opts := []oss.ClientOption{
		oss.AuthVersion(oss.AuthV4),
		oss.Region(strings.TrimSpace(cfg.CoverOSSRegion)),
		oss.Timeout(15, 30),
	}
	if token := strings.TrimSpace(cfg.CoverOSSSecurityToken); token != "" {
		opts = append(opts, oss.SecurityToken(token))
	}

	endpoint := ensureHTTPScheme(cfg.CoverOSSEndpoint)
	writeClient, err := oss.New(endpoint, cfg.CoverOSSAccessKeyID, cfg.CoverOSSAccessKeySecret, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cover OSS write client: %w", err)
	}
	writeBucket, err := writeClient.Bucket(cfg.CoverOSSBucket)
	if err != nil {
		return nil, fmt.Errorf("failed to get cover OSS write bucket %q: %w", cfg.CoverOSSBucket, err)
	}

	publicBase := strings.TrimRight(ensureHTTPScheme(cfg.CoverOSSPublicBaseURL), "/")
	publicOpts := append(append([]oss.ClientOption{}, opts...), oss.UseCname(true))
	publicClient, err := oss.New(publicBase, cfg.CoverOSSAccessKeyID, cfg.CoverOSSAccessKeySecret, publicOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cover OSS public CNAME client: %w", err)
	}
	publicBucket, err := publicClient.Bucket(cfg.CoverOSSBucket)
	if err != nil {
		return nil, fmt.Errorf("failed to get cover OSS public bucket %q: %w", cfg.CoverOSSBucket, err)
	}

	store := &CoverOSSStore{
		enabled:        true,
		prefix:         prefix,
		maxUploadBytes: maxBytes,
		signedTTL:      ttl,
		writeBucket:    writeBucket,
		publicBucket:   publicBucket,
		nowFunc:        time.Now,
	}
	store.putObjectFunc = func(ctx context.Context, objectKey string, data []byte, contentType string) error {
		return store.writeBucket.PutObject(
			objectKey,
			bytes.NewReader(data),
			oss.WithContext(ctx),
			oss.ContentLength(int64(len(data))),
			oss.ContentType(contentType),
			oss.CacheControl("public, max-age=31536000, immutable"),
		)
	}
	store.deleteObjectFunc = func(ctx context.Context, objectKey string) error {
		return store.writeBucket.DeleteObject(objectKey, oss.WithContext(ctx))
	}
	store.signURLFunc = func(objectKey string, ttlSeconds int64) (string, error) {
		return store.publicBucket.SignURL(objectKey, oss.HTTPGet, ttlSeconds)
	}
	return store, nil
}

func (s *CoverOSSStore) IsEnabled() bool {
	return s != nil && s.enabled
}

func (s *CoverOSSStore) Prefix() string {
	if s == nil || s.prefix == "" {
		return defaultCoverPrefix
	}
	return s.prefix
}

func (s *CoverOSSStore) MaxUploadBytes() int64 {
	if s == nil || s.maxUploadBytes <= 0 {
		return defaultCoverMaxUploadBytes
	}
	return s.maxUploadBytes
}

func (s *CoverOSSStore) IsValidCoverRef(ref string) bool {
	key, ok := extractValidCoverKey(ref, s.Prefix())
	if !ok || len(ref) > 255 {
		return false
	}
	ext := strings.ToLower(path.Ext(key))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	default:
		return false
	}
}

func (s *CoverOSSStore) SignCoverURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if s == nil || !s.enabled || trimmed == "" || !strings.HasPrefix(trimmed, "oss://") {
		return raw
	}
	objectKey, ok := extractValidCoverKey(trimmed, "")
	if !ok || s.signURLFunc == nil {
		return raw
	}
	signed, err := s.signURLFunc(objectKey, int64(s.signedTTL))
	if err != nil {
		return raw
	}
	return signed
}

func (s *CoverOSSStore) UploadCover(ctx context.Context, data []byte) (string, string, error) {
	if s == nil || !s.enabled {
		return "", "", ErrCoverOSSDisabled
	}
	ct, ext, err := ValidateCoverImage(data, s.MaxUploadBytes())
	if err != nil {
		return "", "", err
	}

	sum := sha256.Sum256(data)
	now := time.Now
	if s.nowFunc != nil {
		now = s.nowFunc
	}
	objectKey := fmt.Sprintf("%s/%s/%s%s", s.Prefix(), now().UTC().Format("2006/01"), hex.EncodeToString(sum[:12]), ext)

	if err := s.putObjectFunc(ctx, objectKey, data, ct); err != nil {
		return "", "", fmt.Errorf("上传封面到 OSS 失败: %w", err)
	}

	coverRef := "oss://" + objectKey
	return coverRef, s.SignCoverURL(coverRef), nil
}

func (s *CoverOSSStore) DeleteCover(ctx context.Context, rawRef string) error {
	if s == nil || !s.enabled || s.deleteObjectFunc == nil {
		return nil
	}
	objectKey, ok := extractValidCoverKey(strings.TrimSpace(rawRef), s.Prefix())
	if !ok {
		return nil
	}
	return s.deleteObjectFunc(ctx, objectKey)
}

// ValidateCoverImage validates raw bytes are a real JPEG, PNG, or WebP image within size/dimension limits.
func ValidateCoverImage(data []byte, maxBytes int64) (contentType string, ext string, err error) {
	if len(data) == 0 {
		return "", "", ErrCoverEmpty
	}
	if maxBytes <= 0 {
		maxBytes = defaultCoverMaxUploadBytes
	}
	if int64(len(data)) > maxBytes {
		return "", "", ErrCoverTooLarge
	}

	ct := http.DetectContentType(data)
	switch ct {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	default:
		return "", "", ErrCoverUnsupportedType
	}

	cfg, format, decodeErr := image.DecodeConfig(bytes.NewReader(data))
	if decodeErr != nil || cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > maxCoverImageDimension || cfg.Height > maxCoverImageDimension ||
		int64(cfg.Width)*int64(cfg.Height) > maxCoverImagePixels {
		return "", "", ErrCoverInvalidImage
	}

	expectedFormat := strings.TrimPrefix(ct, "image/")
	if format != expectedFormat {
		return "", "", ErrCoverUnsupportedType
	}

	return ct, ext, nil
}

func normalizeCoverPrefix(prefix string) string {
	cleaned := strings.Trim(strings.TrimSpace(prefix), "/")
	if cleaned == "" {
		return defaultCoverPrefix
	}
	return cleaned
}

func ensureHTTPScheme(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if !strings.Contains(trimmed, "://") {
		return "https://" + trimmed
	}
	return trimmed
}

func extractValidCoverKey(ref, requiredPrefix string) (string, bool) {
	if !strings.HasPrefix(ref, "oss://") {
		return "", false
	}
	key := strings.TrimLeft(strings.TrimPrefix(ref, "oss://"), "/")
	if key == "" || strings.ContainsAny(key, " \t\r\n?#\\") {
		return "", false
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", false
		}
	}
	if requiredPrefix != "" && !strings.HasPrefix(key, requiredPrefix+"/") {
		return "", false
	}
	return key, true
}

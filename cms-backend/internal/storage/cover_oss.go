package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"star-novel-cms/internal/config"
)

const (
	defaultCoverPrefix       = "covers"
	defaultCoverSignedTTLSec = 3600
)

var ErrCoverOSSDisabled = errors.New("封面 OSS 存储未启用")

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
	publicRead       bool
	publicBaseURL    string
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
	publicBase, err := normalizePublicBaseURL(cfg.CoverOSSPublicBaseURL)
	if err != nil {
		return nil, err
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

	store := &CoverOSSStore{
		enabled:        true,
		publicRead:     cfg.CoverOSSPublicRead,
		publicBaseURL:  publicBase,
		prefix:         prefix,
		maxUploadBytes: maxBytes,
		signedTTL:      ttl,
		writeBucket:    writeBucket,
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

	if !cfg.CoverOSSPublicRead {
		publicOpts := append(append([]oss.ClientOption{}, opts...), oss.UseCname(true))
		publicClient, err := oss.New(publicBase, cfg.CoverOSSAccessKeyID, cfg.CoverOSSAccessKeySecret, publicOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize cover OSS public CNAME client: %w", err)
		}
		publicBucket, err := publicClient.Bucket(cfg.CoverOSSBucket)
		if err != nil {
			return nil, fmt.Errorf("failed to get cover OSS public bucket %q: %w", cfg.CoverOSSBucket, err)
		}
		store.publicBucket = publicBucket
		store.signURLFunc = func(objectKey string, ttlSeconds int64) (string, error) {
			return store.publicBucket.SignURL(objectKey, oss.HTTPGet, ttlSeconds)
		}
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
	if !ok {
		return raw
	}
	if s.publicRead {
		return s.publicBaseURL + "/" + objectKey
	}
	if s.signURLFunc == nil {
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
	encoded, ct, ext, err := ProcessCoverImage(data, s.MaxUploadBytes())
	if err != nil {
		return "", "", err
	}

	sum := sha256.Sum256(encoded)
	now := time.Now
	if s.nowFunc != nil {
		now = s.nowFunc
	}
	objectKey := fmt.Sprintf("%s/%s/%s%s", s.Prefix(), now().UTC().Format("2006/01"), hex.EncodeToString(sum[:12]), ext)

	if err := s.putObjectFunc(ctx, objectKey, encoded, ct); err != nil {
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

func normalizePublicBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("COVER_OSS_PUBLIC_BASE_URL is required when COVER_OSS_ENABLED=true")
	}
	base := strings.TrimRight(ensureHTTPScheme(trimmed), "/")
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("COVER_OSS_PUBLIC_BASE_URL must be a valid http:// or https:// URL")
	}
	return base, nil
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

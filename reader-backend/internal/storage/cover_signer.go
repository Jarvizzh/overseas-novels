package storage

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"reader-backend/internal/config"
)

const defaultCoverSignedTTLSec = 3600

type CoverSigner interface {
	IsEnabled() bool
	SignCoverURL(raw string) string
}

type CoverOSSSigner struct {
	enabled      bool
	signedTTL    int
	publicBucket *oss.Bucket
	signURLFunc  func(objectKey string, ttlSeconds int64) (string, error)
}

func NewCoverOSSSigner(cfg *config.Config) (*CoverOSSSigner, error) {
	ttl := cfg.CoverOSSSignedURLTTL
	if ttl <= 0 {
		ttl = defaultCoverSignedTTLSec
	}

	if !cfg.CoverOSSEnabled {
		return &CoverOSSSigner{
			enabled:   false,
			signedTTL: ttl,
		}, nil
	}

	if strings.TrimSpace(cfg.CoverOSSRegion) == "" {
		return nil, errors.New("COVER_OSS_REGION is required when COVER_OSS_ENABLED=true")
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
		oss.Timeout(10, 15),
		oss.UseCname(true),
	}
	if token := strings.TrimSpace(cfg.CoverOSSSecurityToken); token != "" {
		opts = append(opts, oss.SecurityToken(token))
	}

	publicBase := strings.TrimRight(ensureHTTPScheme(cfg.CoverOSSPublicBaseURL), "/")
	publicClient, err := oss.New(publicBase, cfg.CoverOSSAccessKeyID, cfg.CoverOSSAccessKeySecret, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize reader cover OSS signer client: %w", err)
	}
	publicBucket, err := publicClient.Bucket(cfg.CoverOSSBucket)
	if err != nil {
		return nil, fmt.Errorf("failed to get reader cover OSS bucket %q: %w", cfg.CoverOSSBucket, err)
	}

	signer := &CoverOSSSigner{
		enabled:      true,
		signedTTL:    ttl,
		publicBucket: publicBucket,
	}
	signer.signURLFunc = func(objectKey string, ttlSeconds int64) (string, error) {
		return signer.publicBucket.SignURL(objectKey, oss.HTTPGet, ttlSeconds)
	}
	return signer, nil
}

func (s *CoverOSSSigner) IsEnabled() bool {
	return s != nil && s.enabled
}

func (s *CoverOSSSigner) SignCoverURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if s == nil || !s.enabled || trimmed == "" || !strings.HasPrefix(trimmed, "oss://") {
		return raw
	}
	objectKey := strings.TrimLeft(strings.TrimPrefix(trimmed, "oss://"), "/")
	if objectKey == "" || strings.ContainsAny(objectKey, " \t\r\n?#\\") {
		return raw
	}
	for _, seg := range strings.Split(objectKey, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return raw
		}
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

func ensureHTTPScheme(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if !strings.Contains(trimmed, "://") {
		return "https://" + trimmed
	}
	return trimmed
}

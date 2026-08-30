package storage

import (
	"testing"

	"star-novel-cms/internal/config"
)

func TestNewContentStorage_PostgresDefault(t *testing.T) {
	cfg := &config.Config{
		StorageType: "postgres",
	}

	// dbPool nil check
	_, err := NewContentStorage(cfg, nil)
	if err == nil {
		t.Fatalf("expected error when dbPool is nil for postgres storage, got nil")
	}
}

func TestNewContentStorage_OSS_MissingConfig(t *testing.T) {
	testCases := []struct {
		name string
		cfg  *config.Config
	}{
		{
			name: "missing endpoint",
			cfg: &config.Config{
				StorageType: "oss",
			},
		},
		{
			name: "missing access key id",
			cfg: &config.Config{
				StorageType: "oss",
				OSSEndpoint: "oss-cn-hangzhou.aliyuncs.com",
			},
		},
		{
			name: "missing access key secret",
			cfg: &config.Config{
				StorageType:    "oss",
				OSSEndpoint:    "oss-cn-hangzhou.aliyuncs.com",
				OSSAccessKeyID: "test-key-id",
			},
		},
		{
			name: "missing bucket",
			cfg: &config.Config{
				StorageType:        "oss",
				OSSEndpoint:        "oss-cn-hangzhou.aliyuncs.com",
				OSSAccessKeyID:     "test-key-id",
				OSSAccessKeySecret: "test-secret",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewContentStorage(tc.cfg, nil)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestNewContentStorage_InvalidStorageType(t *testing.T) {
	cfg := &config.Config{
		StorageType: "s3_unknown",
	}

	_, err := NewContentStorage(cfg, nil)
	if err == nil {
		t.Fatalf("expected error for unsupported storage type, got nil")
	}
}

func TestAliyunOSSStorage_PathFormatting(t *testing.T) {
	ossStorage := &AliyunOSSStorage{
		basePath: "novels",
	}

	key := ossStorage.formatObjectKey(10000001, 15)
	expectedKey := "novels/10000001/15.txt"
	if key != expectedKey {
		t.Errorf("expected %s, got %s", expectedKey, key)
	}

	prefix := ossStorage.formatNovelPrefix(10000001)
	expectedPrefix := "novels/10000001/"
	if prefix != expectedPrefix {
		t.Errorf("expected %s, got %s", expectedPrefix, prefix)
	}
}

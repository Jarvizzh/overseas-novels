package main

import (
	"strings"
	"testing"
)

func TestWorkerConsumerBudget(t *testing.T) {
	for _, key := range []string{
		"CONVERSION_WORKER_DB_MAX_CONNS", "CONVERSION_NORMALIZER_LIMIT", "CONVERSION_READER_NORMALIZERS",
		"CONVERSION_INBOX_NORMALIZERS", "CONVERSION_SINAN_MOCK_ENABLED",
		"CONVERSION_META_GLOBAL_LIMIT", "CONVERSION_META_CONCURRENCY", "CONVERSION_META_BATCH_SIZE",
		"CONVERSION_NORMALIZE_BATCH", "CONVERSION_META_PIXEL_INTERVAL", "CONVERSION_WORKER_POLL_INTERVAL",
		"CONVERSION_WORKER_QUEUE_PROBE_INTERVAL", "CONVERSION_META_REQUEST_TIMEOUT",
	} {
		t.Setenv(key, "")
	}
	t.Run("isolated two plus two", func(t *testing.T) {
		t.Setenv("CONVERSION_SINAN_MOCK_ENABLED", "true")
		w, err := loadWorkerSettings("test")
		if err != nil || w.poolSize != 16 || !w.inboxEnabled || w.readerNormalizers != 2 || w.inboxNormalizers != 2 {
			t.Fatalf("worker settings: %+v, %v", w, err)
		}
		t.Setenv("CONVERSION_INBOX_NORMALIZERS", "3")
		if _, err := loadWorkerSettings("test"); err == nil || !strings.Contains(err.Error(), "CONVERSION_NORMALIZER_LIMIT") {
			t.Fatalf("5 consumers accepted under a limit of 4: %v", err)
		}
		t.Setenv("CONVERSION_NORMALIZER_LIMIT", "5")
		if _, err := loadWorkerSettings("test"); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CONVERSION_WORKER_DB_MAX_CONNS", "8")
		if _, err := loadWorkerSettings("test"); err == nil {
			t.Fatal("did not reserve connections for the sender and recovery")
		}
	})
	t.Run("validate only registered consumers", func(t *testing.T) {
		t.Setenv("CONVERSION_NORMALIZER_LIMIT", "2")
		w, err := loadWorkerSettings("production")
		if err != nil || w.inboxEnabled {
			t.Fatalf("production settings: %+v, %v", w, err)
		}
		t.Setenv("CONVERSION_SINAN_MOCK_ENABLED", "true")
		if _, err := loadWorkerSettings("production"); err == nil {
			t.Fatal("production accepted the isolated mock contract")
		}
	})
}

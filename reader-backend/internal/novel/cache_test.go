package novel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	redisclient "reader-backend/internal/redis"
)

func TestReadingCacheCoolingDownAndRecovery(t *testing.T) {
	previous := redisclient.RDB
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	redisclient.RDB = client
	defer func() { redisclient.RDB = previous; _ = client.Close() }()
	cache := &redisCache{}
	calls := 0
	failure := errors.New("cache socket failed")
	failed := func(context.Context) error { calls++; return failure }
	if err := cache.command(context.Background(), failed); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	for range 100 {
		if err := cache.command(context.Background(), failed); !errors.Is(err, errCacheCoolingDown) {
			t.Fatalf("failed cache was retried during cooldown: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("unexpected Redis attempts %d", calls)
	}
	cache.retryAfter.Store(time.Now().Add(-time.Second).UnixNano())
	if err := cache.command(context.Background(), func(context.Context) error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("recovery did not attempt the cache")
	}
	if err := cache.command(context.Background(), func(context.Context) error { return redis.Nil }); !errors.Is(err, redis.Nil) {
		t.Fatal(err)
	}
	if cache.retryAfter.Load() > time.Now().UnixNano() {
		t.Fatal("a cache miss opened the circuit")
	}
}

func TestReadingAdmissionBoundsSimultaneousRequests(t *testing.T) {
	s := NewService(nil, nil, nil, nil, ReadLimits{Requests: 3, Loads: 1}).(*service)
	for range 3 {
		if !s.admit() {
			t.Fatal("configured admission was unavailable")
		}
	}
	if s.admit() {
		t.Fatal("unbounded read request admission")
	}
	<-s.requests
	if !s.admit() {
		t.Fatal("read admission did not recover")
	}
}

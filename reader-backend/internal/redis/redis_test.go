package redis

import "testing"

func TestBuildRedisOptions(t *testing.T) {
	if opt := buildRedisOptions("redis:6379"); opt.Addr != "redis:6379" || opt.DB != 0 {
		t.Fatalf("expected Addr=redis:6379 DB=0, got Addr=%q DB=%d", opt.Addr, opt.DB)
	}
	if opt := buildRedisOptions("redis://:secret@10.0.0.5:6379/2"); opt.Addr != "10.0.0.5:6379" || opt.Password != "secret" || opt.DB != 2 {
		t.Fatalf("unexpected URL options: %+v", opt)
	}
}

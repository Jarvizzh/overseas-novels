package redis

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func TestBuildRedisOptions(t *testing.T) {
	if opt := buildRedisOptions("redis:6379"); opt.Addr != "redis:6379" || opt.DB != 0 {
		t.Fatalf("expected Addr=redis:6379 DB=0, got Addr=%q DB=%d", opt.Addr, opt.DB)
	}
	if opt := buildRedisOptions("redis://:secret@10.0.0.5:6379/2"); opt.Addr != "10.0.0.5:6379" || opt.Password != "secret" || opt.DB != 2 {
		t.Fatalf("unexpected URL options: %+v", opt)
	}
}

func TestRedisDeadlinePoolWaitAndRecovery(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var requests atomic.Int64
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	defer close(release)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveRedisProbe(conn, &requests, entered, release)
		}
	}()

	options := buildRedisOptions(listener.Addr().String())
	options.PoolSize, options.MaxActiveConns = 1, 1
	options.Protocol, options.DisableIdentity = 2, true
	client := goredis.NewClient(options)
	defer client.Close()
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = client.Get(ctx, "slow").Err()
	if err == nil || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("caller deadline was ignored: %v (%v)", err, time.Since(start))
	}
	<-entered
	if requests.Load() != 1 {
		t.Fatalf("timed-out cache operation was retried: %d", requests.Load())
	}

	blocking := make(chan error, 1)
	go func() { blocking <- client.Get(context.Background(), "slow").Err() }()
	<-entered
	ctx, cancelWait := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancelWait()
	start = time.Now()
	if err := client.Ping(ctx).Err(); err == nil || time.Since(start) > 90*time.Millisecond {
		t.Fatalf("pool wait exceeded caller deadline: %v (%v)", err, time.Since(start))
	}
	if err := <-blocking; err == nil {
		t.Fatal("expected timed-out request")
	}
	ctx, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	if err := client.Ping(ctx).Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller was not honored: %v", err)
	}
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("failed to recover: %v", err)
	}
}

func serveRedisProbe(conn net.Conn, requests *atomic.Int64, entered chan<- struct{}, release <-chan struct{}) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		count, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
		if err != nil || count == 0 {
			return
		}
		args := make([]string, count)
		for i := range args {
			line, err = reader.ReadString('\n')
			if err != nil {
				return
			}
			length, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "$")))
			if err != nil || length < 0 || length > 1024 {
				return
			}
			data := make([]byte, length+2)
			if _, err = io.ReadFull(reader, data); err != nil {
				return
			}
			args[i] = string(data[:length])
		}
		switch strings.ToUpper(args[0]) {
		case "HELLO":
			_, _ = io.WriteString(conn, "-ERR unknown command 'hello'\r\n")
		case "GET":
			requests.Add(1)
			entered <- struct{}{}
			<-release
			return
		default:
			_, _ = io.WriteString(conn, "+PONG\r\n")
		}
	}
}

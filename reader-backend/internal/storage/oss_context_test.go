package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

func TestOSSReadHonorsCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { started <- struct{}{}; <-r.Context().Done() }))
	defer server.Close()
	client, err := oss.New(server.URL, "test-id", "test-secret", oss.UseCname(true))
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := client.Bucket("test-bucket")
	if err != nil {
		t.Fatal(err)
	}
	s := &AliyunOSSStorage{bucket: bucket, basePath: "novels"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.GetContent(ctx, 1, 1); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("OSS request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("OSS call ignored canceled request")
	}
}

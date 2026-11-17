package workerpool

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestShutdownRejectsConcurrentSubmissionAndHonorsBudget(t *testing.T) {
	InitPool(1, 10)
	started, release := make(chan struct{}), make(chan struct{})
	if !Submit(func() { close(started); <-release }) {
		t.Fatal("job rejected")
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := ShutdownContext(ctx); err == nil {
		t.Fatal("shutdown did not honor deadline")
	}
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() {
			if Submit(func() {}) {
				t.Error("job accepted after stop")
			}
		})
	}
	group.Wait()
	close(release)
	if err := ShutdownContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

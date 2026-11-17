package tracking

import (
	"context"
	"sync"
	"testing"
)

func TestLogShutdownIsIdempotentAndRaceSafe(t *testing.T) {
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() { saveCAPILog("test", "Purchase", "1", 1, "USD", "", 200, "{}", "{}") })
	}
	Shutdown(context.Background())
	group.Wait()
	Shutdown(context.Background())
	saveCAPILog("test", "Purchase", "1", 1, "USD", "", 200, "{}", "{}")
}

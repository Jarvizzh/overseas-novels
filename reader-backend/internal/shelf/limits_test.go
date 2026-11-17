package shelf

import (
	"context"
	"errors"
	"testing"
)

func TestInvalidBatchesCannotStartRepositoryWork(t *testing.T) {
	svc := NewService(nil, nil)
	ctx := context.Background()
	for _, ids := range [][]int64{nil, {}, make([]int64, MaxBatchSize+1), {1, 0}, {-1}} {
		if err := svc.RemoveFromShelf(ctx, 1, ids); !errors.Is(err, ErrInvalidBatch) {
			t.Fatalf("batch accepted: %v", err)
		}
	}
	for _, updates := range [][]ProgressUpdate{nil, {}, make([]ProgressUpdate, MaxBatchSize+1)} {
		if err := svc.SyncProgress(ctx, 1, updates); !errors.Is(err, ErrInvalidBatch) {
			t.Fatalf("progress batch accepted: %v", err)
		}
	}
}

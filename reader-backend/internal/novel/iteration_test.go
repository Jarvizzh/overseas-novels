package novel

import (
	"context"
	"testing"

	"reader-backend/internal/testdb"
)

func TestUnlockIndexScanFailureIsNotPartialSuccess(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	// A malformed imported row must fail the complete result, not disappear.
	if _, err := pool.Exec(ctx, `ALTER TABLE unlock_records RENAME TO original_unlock_records;
 CREATE VIEW unlock_records AS SELECT 1::bigint user_id,1::bigint novel_id,NULL::integer chapter_index`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDBRepository().GetUnlockedChapterIndices(ctx, 1, 1); err == nil {
		t.Fatal("malformed unlock row silently ignored")
	}
}

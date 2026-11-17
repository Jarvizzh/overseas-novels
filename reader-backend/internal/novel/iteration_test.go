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
	if _, err := pool.Exec(ctx, `ALTER TABLE chapters RENAME TO original_chapters;
	 CREATE VIEW chapters AS SELECT 1::integer novel_id, 'broken'::text chapter_index, 'id'::varchar id;
	 CREATE OR REPLACE FUNCTION reader_has_chapter_unlock(reader_id BIGINT, target_id VARCHAR) RETURNS BOOLEAN
	 LANGUAGE sql STABLE AS $$ SELECT true $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDBRepository().GetUnlockedChapterIndices(ctx, 1, 1); err == nil {
		t.Fatal("malformed unlock row silently ignored")
	}
}

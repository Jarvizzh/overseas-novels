package storage

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunChapterCleanup reclaims unpublished uploads and retired revisions. Claiming
// an object permanently fences publication before issuing the external delete.
func (s *AliyunOSSStorage) RunChapterCleanup(ctx context.Context, pool *pgxpool.Pool) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		for range 20 {
			if ctx.Err() != nil || !s.cleanChapterObject(ctx, pool) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *AliyunOSSStorage) cleanChapterObject(ctx context.Context, pool *pgxpool.Pool) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var key string
	var attempt int
	err := pool.QueryRow(ctx, `
		WITH due AS (
			SELECT object_key FROM chapter_content_objects
			WHERE state IN ('staged', 'retired', 'deleting') AND next_attempt_at <= now()
			ORDER BY next_attempt_at LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		UPDATE chapter_content_objects o SET state = 'deleting', attempts = attempts + 1,
			next_attempt_at = now() + interval '1 minute' * LEAST(60, (1 << LEAST(attempts, 6)))
		FROM due WHERE o.object_key = due.object_key RETURNING o.object_key, o.attempts`).Scan(&key, &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		log.Printf("claim retired chapter object: %v", err)
		return false
	}
	if err := s.DeleteChapterRevision(ctx, key); err != nil {
		log.Printf("delete chapter object %q: %v", key, err)
		_, err = pool.Exec(ctx, `UPDATE chapter_content_objects SET last_error = left($3, 1000)
			WHERE object_key = $1 AND attempts = $2 AND state = 'deleting'`, key, attempt, err.Error())
		if err != nil {
			log.Printf("record cleanup error for chapter object %q: %v", key, err)
		}
		return true
	}
	if _, err := pool.Exec(ctx, `UPDATE chapter_content_objects SET state = 'deleted', deleted_at = now(), last_error = NULL
		WHERE object_key = $1 AND attempts = $2 AND state = 'deleting'`, key, attempt); err != nil {
		log.Printf("record cleanup of chapter object %q: %v", key, err)
	}
	return true
}

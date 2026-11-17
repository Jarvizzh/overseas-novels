package storage

import (
	"context"
	"fmt"
	"log"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ChapterPreview matches reader_chapter_preview in the database migration.
func ChapterPreview(body string) string {
	if body == "" {
		return ""
	}
	limit := min(250, utf8.RuneCountInString(body)/2)
	for offset := range body {
		if limit == 0 {
			return body[:offset] + "..."
		}
		limit--
	}
	return "..."
}

type previewCandidate struct {
	id        string
	novelID   int64
	index     int
	key, hash *string
}

// BackfillChapterPreviews reads historical OSS content outside database
// transactions and uses the original reference to fence concurrent publication.
func (s *AliyunOSSStorage) BackfillChapterPreviews(ctx context.Context, pool *pgxpool.Pool) error {
	var cursor string
	var failed, updated int
	for {
		rows, err := pool.Query(ctx, `
			SELECT id,novel_id,chapter_index,content_object_key,content_sha256 FROM chapters
			WHERE id > $1 AND chapter_index IS NOT NULL AND content_preview IS NULL AND content=''
			ORDER BY id LIMIT 100`, cursor)
		if err != nil {
			return err
		}
		batch, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (previewCandidate, error) {
			var c previewCandidate
			err := row.Scan(&c.id, &c.novelID, &c.index, &c.key, &c.hash)
			return c, err
		})
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, c := range batch {
			tag, err := s.backfillPreview(ctx, pool, c)
			if err != nil {
				failed++
				log.Printf("chapter preview backfill chapter=%q error=%v", c.id, err)
			} else if tag {
				updated++
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		cursor = batch[len(batch)-1].id
	}
	log.Printf("chapter preview backfill updated=%d failed=%d", updated, failed)
	if failed > 0 {
		return fmt.Errorf("%d chapter previews need a retry", failed)
	}
	return nil
}

func (s *AliyunOSSStorage) backfillPreview(ctx context.Context, pool *pgxpool.Pool, c previewCandidate) (bool, error) {
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var body string
	var err error
	if c.key != nil && c.hash != nil {
		body, err = s.ReadChapterRevision(readCtx, *c.key, *c.hash)
	} else {
		body, err = s.ReadLegacyChapter(readCtx, c.novelID, c.index)
	}
	if err != nil {
		return false, err
	}
	if body == "" || !utf8.ValidString(body) {
		return false, fmt.Errorf("invalid chapter body")
	}
	tag, err := pool.Exec(ctx, `
		UPDATE chapters SET content_preview=$6 WHERE id=$1 AND novel_id=$2 AND chapter_index=$3
		AND content_object_key IS NOT DISTINCT FROM $4 AND content_sha256 IS NOT DISTINCT FROM $5
		AND content='' AND content_preview IS NULL`, c.id, c.novelID, c.index, c.key, c.hash, ChapterPreview(body))
	return tag.RowsAffected() > 0, err
}

package novel

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

// ChapterIdentity describes a current or retired chapter used for import mappings.
type ChapterIdentity struct {
	ID    string `json:"id"`
	Index *int   `json:"chapter_index"`
	Title string `json:"title"`
	Hash  string `json:"-"`
}

func (r *dbNovelRepository) LockPublication(ctx context.Context, tx pgx.Tx, novelID, version int64) error {
	var current int64
	err := tx.QueryRow(ctx, "SELECT content_version FROM novels WHERE id=$1 AND status <> 'archived' FOR UPDATE", novelID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNovelNotFound
	}
	if err != nil {
		return err
	}
	if current != version {
		return ErrPublicationChanged
	}
	return nil
}

func (r *dbNovelRepository) ListChapterIdentities(ctx context.Context, novelID int64) ([]ChapterIdentity, error) {
	rows, err := db.DB.Query(ctx, `
		SELECT id, chapter_index, title, COALESCE(content_sha256, '')
		FROM chapters WHERE novel_id = $1 ORDER BY chapter_index, id`, novelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ChapterIdentity])
	return items, err
}

func (r *dbNovelRepository) StageChapterObjects(ctx context.Context, chapters []Chapter) error {
	keys := make([]string, 0, len(chapters))
	ids := make([]string, 0, len(chapters))
	hashes := make([]string, 0, len(chapters))
	for _, ch := range chapters {
		keys = append(keys, ch.ObjectKey)
		ids = append(ids, ch.ID)
		hashes = append(hashes, ch.ContentHash)
	}
	_, err := db.DB.Exec(ctx, `
		INSERT INTO chapter_content_objects (object_key, chapter_id, sha256)
		SELECT * FROM unnest($1::text[], $2::text[], $3::text[])`, keys, ids, hashes)
	return err
}

func (r *dbNovelRepository) ReplaceChaptersTx(ctx context.Context, tx pgx.Tx, novelID int64, chapters []Chapter, retired []ChapterIdentity, splits map[string]string) error {
	// Release the unique positions first. Other transactions continue to see the old catalog.
	if _, err := tx.Exec(ctx, "UPDATE chapters SET chapter_index = NULL WHERE novel_id = $1 AND chapter_index IS NOT NULL", novelID); err != nil {
		return err
	}
	batch := &pgx.Batch{}
	for _, old := range retired {
		batch.Queue(`UPDATE chapters SET content = '', content_object_key = NULL, content_sha256 = NULLIF($2, '') WHERE id = $1`, old.ID, old.Hash)
	}
	for _, ch := range chapters {
		batch.Queue(insertChapter+`
			ON CONFLICT (id) DO UPDATE SET chapter_index = excluded.chapter_index, title = excluded.title,
			  content = excluded.content, word_count = excluded.word_count, is_paid = excluded.is_paid,
			  price = excluded.price, content_object_key = excluded.content_object_key,
			  content_sha256 = excluded.content_sha256`, chapterArgs(&ch)...)
	}
	for child, source := range splits {
		batch.Queue("INSERT INTO chapter_entitlement_links(chapter_id, source_chapter_id, novel_id) VALUES ($1, $2, $3)", child, source, novelID)
	}
	return tx.SendBatch(ctx, batch).Close()
}

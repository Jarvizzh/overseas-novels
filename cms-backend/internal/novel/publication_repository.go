package novel

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

// ChapterIdentity describes a current or retired chapter used for import mappings.
type ChapterIdentity struct {
	ID        string `json:"id"`
	Index     *int   `json:"chapter_index"`
	Title     string `json:"title"`
	Hash      string `json:"-"`
	ObjectKey string `json:"-"`
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
	return markChapterPublication(ctx, tx)
}

func (r *dbNovelRepository) ListChapterIdentities(ctx context.Context, novelID int64) ([]ChapterIdentity, error) {
	rows, err := db.DB.Query(ctx, `
		SELECT c.id, c.chapter_index, c.title, COALESCE(c.content_sha256, ''),
		    CASE WHEN o.state='published' AND o.chapter_id=c.id AND o.sha256=c.content_sha256
		        THEN COALESCE(c.content_object_key,'') ELSE '' END
		FROM chapters c LEFT JOIN chapter_content_objects o ON o.object_key=c.content_object_key
		WHERE c.novel_id=$1 ORDER BY c.chapter_index,c.id`, novelID)
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

func markChapterPublication(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "SET LOCAL star_novel.publishing_chapters = 'on'")
	return err
}

type chapterImportRow struct {
	ID      string `json:"id"`
	Index   int    `json:"position"`
	Title   string `json:"title"`
	Content string `json:"body"`
	Words   int    `json:"words"`
	Paid    bool   `json:"paid"`
	Price   int    `json:"price"`
	Key     string `json:"object_key"`
	Hash    string `json:"hash"`
	Preview string `json:"preview"`
}

func (r *dbNovelRepository) ReplaceChaptersTx(ctx context.Context, tx pgx.Tx, novelID int64, chapters []Chapter, retired []ChapterIdentity, splits map[string]string) error {
	if err := markChapterPublication(ctx, tx); err != nil {
		return err
	}
	rows := make([]chapterImportRow, len(chapters))
	ids, positions := make([]string, len(chapters)), make([]int32, len(chapters))
	for i, ch := range chapters {
		rows[i] = chapterImportRow{ch.ID, ch.ChapterIndex, ch.Title, ch.Content, ch.WordCount, ch.IsPaid, ch.Price, ch.ObjectKey, ch.ContentHash, ch.Preview}
		ids[i], positions[i] = ch.ID, int32(ch.ChapterIndex)
	}
	// Only clear positions that move or disappear. The transaction retains unique
	// indexes for existing positions and protects readers from intermediate rows.
	if _, err := tx.Exec(ctx, `
		UPDATE chapters c SET chapter_index=NULL WHERE c.novel_id=$1 AND c.chapter_index IS NOT NULL
		AND NOT EXISTS(SELECT 1 FROM unnest($2::text[],$3::int[]) next(id,position)
		               WHERE next.id=c.id AND next.position=c.chapter_index)`, novelID, ids, positions); err != nil {
		return err
	}
	if len(retired) > 0 {
		ids, hashes := make([]string, len(retired)), make([]string, len(retired))
		for i, old := range retired {
			ids[i], hashes[i] = old.ID, old.Hash
		}
		if _, err := tx.Exec(ctx, `
			UPDATE chapters c SET content='', content_object_key=NULL, content_preview=NULL, content_sha256=NULLIF(old.hash,'')
			FROM unnest($2::text[], $3::text[]) old(id, hash) WHERE c.novel_id=$1 AND c.id=old.id`, novelID, ids, hashes); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, insertChapter+`
		SELECT id,$1,position,title,body,words,paid,price,NULLIF(object_key,''),NULLIF(hash,''),preview
		FROM jsonb_to_recordset($2::jsonb) AS rows(id text, position int, title text, body text, words int,
		    paid bool, price int, object_key text, hash text, preview text)
		ON CONFLICT (id) DO UPDATE SET chapter_index=excluded.chapter_index, title=excluded.title,
		    content=excluded.content, word_count=excluded.word_count, is_paid=excluded.is_paid,
		    price=excluded.price, content_object_key=excluded.content_object_key,
		    content_sha256=excluded.content_sha256, content_preview=excluded.content_preview
		WHERE (chapters.chapter_index, chapters.title, chapters.word_count, chapters.is_paid, chapters.price,
		    chapters.content_object_key, chapters.content_sha256, chapters.content_preview)
		    IS DISTINCT FROM (excluded.chapter_index, excluded.title, excluded.word_count, excluded.is_paid, excluded.price,
		    excluded.content_object_key, excluded.content_sha256, excluded.content_preview)`, novelID, rows); err != nil {
		return err
	}
	if len(splits) == 0 {
		return nil
	}
	children, parents := make([]string, 0, len(splits)), make([]string, 0, len(splits))
	for child, parent := range splits {
		children, parents = append(children, child), append(parents, parent)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO chapter_entitlement_links(chapter_id, source_chapter_id, novel_id)
		SELECT child, parent, $1 FROM unnest($2::text[], $3::text[]) split(child, parent)`, novelID, children, parents)
	return err
}

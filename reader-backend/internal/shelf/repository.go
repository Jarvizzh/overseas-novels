package shelf

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
)

var ErrNovelUnavailable = errors.New("novel not found or unavailable")

type Repository interface {
	GetShelfItemsWithNovel(ctx context.Context, userID int64) ([]model.BookshelfWithNovel, error)
	GetShelfPage(ctx context.Context, userID int64, limit int, after *shelfCursor) ([]model.BookshelfWithNovel, error)
	GetShelfIDs(ctx context.Context, userID int64) ([]int64, error)
	AddToShelf(ctx context.Context, userID int64, novelID int64) error
	RemoveFromShelf(ctx context.Context, userID int64, novelIDs []int64) error
	GetProgress(ctx context.Context, userID int64, novelID int64) (*model.Bookshelf, error)
	SaveProgress(ctx context.Context, userID int64, novelID int64, chapterIndex int, offset float64, updatedAt time.Time) error
	SaveProgressBatch(ctx context.Context, userID int64, updates []ProgressUpdate) error
}

type dbRepository struct{}

func NewDBRepository() Repository {
	return &dbRepository{}
}

const shelfActivity = `COALESCE(GREATEST(b.updated_at, b.shelf_added_at), TIMESTAMPTZ 'epoch')`
const shelfFilter = `FROM bookshelves b JOIN novels n ON b.novel_id=n.id
	WHERE b.user_id=$1 AND b.in_shelf=TRUE AND n.status<>'archived' AND n.publish_status='published'`
const shelfSelect = `SELECT b.chapter_index, b.scroll_offset_percentage, b.in_shelf,
	COALESCE(b.updated_at, TIMESTAMPTZ 'epoch'), b.progress_version,
	n.id, n.title, n.author, n.cover_url, n.rating, n.status, n.publish_status, n.synopsis, n.genres, n.word_count, n.view_count,
	` + shelfActivity + ` ` + shelfFilter
const shelfOrder = ` ORDER BY ` + shelfActivity + ` DESC, b.novel_id DESC`

func (r *dbRepository) GetShelfItemsWithNovel(ctx context.Context, userID int64) ([]model.BookshelfWithNovel, error) {
	return queryShelf(ctx, userID, shelfSelect+shelfOrder)
}

func (r *dbRepository) GetShelfPage(ctx context.Context, userID int64, limit int, after *shelfCursor) ([]model.BookshelfWithNovel, error) {
	if after == nil {
		return queryShelf(ctx, userID, shelfSelect+shelfOrder+` LIMIT $2`, limit)
	}
	return queryShelf(ctx, userID, shelfSelect+` AND (`+shelfActivity+`, b.novel_id)<($3,$4)`+shelfOrder+` LIMIT $2`,
		limit, after.activityAt, after.novelID)
}

func (r *dbRepository) GetShelfIDs(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := db.DB.Query(ctx, `SELECT b.novel_id `+shelfFilter, userID)
	if err != nil {
		return nil, err
	}
	return pgx.AppendRows(make([]int64, 0), rows, pgx.RowTo[int64])
}

func queryShelf(ctx context.Context, userID int64, query string, args ...any) ([]model.BookshelfWithNovel, error) {
	rows, err := db.DB.Query(ctx, query, append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []model.BookshelfWithNovel{}
	for rows.Next() {
		var item model.BookshelfWithNovel
		item.UserID = userID
		item.InShelf = true
		err = rows.Scan(
			&item.ChapterIndex, &item.ScrollOffsetPercentage, &item.InShelf, &item.UpdatedAt, &item.ProgressVersion,
			&item.Novel.ID, &item.Novel.Title, &item.Novel.Author, &item.Novel.CoverURL,
			&item.Novel.Rating, &item.Novel.Status, &item.Novel.PublishStatus, &item.Novel.Synopsis, &item.Novel.Genres,
			&item.Novel.WordCount, &item.Novel.ViewCount, &item.ActivityAt,
		)
		if err != nil {
			return nil, err
		}
		if item.Novel.Genres == nil {
			item.Novel.Genres = []string{}
		}
		item.NovelID = item.Novel.ID
		items = append(items, item)
	}

	return items, rows.Err()
}

func (r *dbRepository) AddToShelf(ctx context.Context, userID int64, novelID int64) error {
	var published bool
	err := db.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM novels WHERE id = $1 AND status != 'archived' AND publish_status = 'published')`, novelID).Scan(&published)
	if err != nil {
		return err
	}
	if !published {
		return ErrNovelUnavailable
	}

	query := `
		INSERT INTO bookshelves (user_id, novel_id, in_shelf, updated_at, shelf_added_at)
		VALUES ($1, $2, TRUE, 'epoch', CURRENT_TIMESTAMP)
		ON CONFLICT (user_id, novel_id)
		DO UPDATE SET in_shelf = TRUE,
		    shelf_added_at = CASE WHEN bookshelves.in_shelf THEN bookshelves.shelf_added_at ELSE CURRENT_TIMESTAMP END`

	_, err = db.DB.Exec(ctx, query, userID, novelID)
	return err
}

func (r *dbRepository) RemoveFromShelf(ctx context.Context, userID int64, novelIDs []int64) error {
	query := `
		UPDATE bookshelves
		SET in_shelf = FALSE
		WHERE user_id = $1 AND novel_id = ANY($2)`

	_, err := db.DB.Exec(ctx, query, userID, novelIDs)
	return err
}

func (r *dbRepository) GetProgress(ctx context.Context, userID int64, novelID int64) (*model.Bookshelf, error) {
	query := `
		SELECT id, user_id, novel_id, chapter_index, scroll_offset_percentage, in_shelf, updated_at, progress_version
		FROM bookshelves
		WHERE user_id = $1 AND novel_id = $2`

	var b model.Bookshelf
	err := db.DB.QueryRow(ctx, query, userID, novelID).Scan(
		&b.ID, &b.UserID, &b.NovelID, &b.ChapterIndex, &b.ScrollOffsetPercentage, &b.InShelf, &b.UpdatedAt, &b.ProgressVersion,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &b, nil
}

func (r *dbRepository) SaveProgress(ctx context.Context, userID int64, novelID int64, chapterIndex int, offset float64, updatedAt time.Time) error {
	return r.SaveProgressBatch(ctx, userID, []ProgressUpdate{{
		NovelID: novelID, ChapterIndex: chapterIndex, ScrollOffsetPercentage: offset, UpdatedAt: updatedAt,
	}})
}

// SaveProgressBatch receives one validated update per novel from the service.
func (r *dbRepository) SaveProgressBatch(ctx context.Context, userID int64, updates []ProgressUpdate) error {
	ids := make([]int64, len(updates))
	chapters := make([]int32, len(updates))
	offsets := make([]float64, len(updates))
	timestamps := make([]time.Time, len(updates))
	versions := make([]string, len(updates))
	for i, update := range updates {
		ids[i], chapters[i] = update.NovelID, int32(update.ChapterIndex)
		offsets[i], timestamps[i], versions[i] = update.ScrollOffsetPercentage, update.UpdatedAt, update.Version
	}
	query := `
		INSERT INTO bookshelves (user_id, novel_id, chapter_index, scroll_offset_percentage, in_shelf, updated_at, progress_version)
		SELECT $1, novel_id, chapter_index, scroll_offset, false, updated_at, version
		FROM unnest($2::bigint[], $3::integer[], $4::double precision[], $5::timestamptz[], $6::text[])
		  AS updates(novel_id, chapter_index, scroll_offset, updated_at, version)
		ORDER BY novel_id
		ON CONFLICT (user_id, novel_id)
		DO UPDATE SET 
			chapter_index = EXCLUDED.chapter_index, 
			scroll_offset_percentage = EXCLUDED.scroll_offset_percentage, 
			updated_at = EXCLUDED.updated_at,
			progress_version = EXCLUDED.progress_version
		WHERE (EXCLUDED.updated_at, EXCLUDED.progress_version COLLATE "C") >
		      (bookshelves.updated_at, bookshelves.progress_version COLLATE "C")`

	_, err := db.DB.Exec(ctx, query, userID, ids, chapters, offsets, timestamps, versions)
	return err
}

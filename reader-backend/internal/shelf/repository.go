package shelf

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
)

type Repository interface {
	GetShelfItemsWithNovel(ctx context.Context, userID int64) ([]model.BookshelfWithNovel, error)
	AddToShelf(ctx context.Context, userID int64, novelID int64) error
	RemoveFromShelf(ctx context.Context, userID int64, novelIDs []int64) error
	GetProgress(ctx context.Context, userID int64, novelID int64) (*model.Bookshelf, error)
	SaveProgress(ctx context.Context, userID int64, novelID int64, chapterIndex int, offset float64, updatedAt time.Time) error
}

type dbRepository struct{}

func NewDBRepository() Repository {
	return &dbRepository{}
}

func (r *dbRepository) GetShelfItemsWithNovel(ctx context.Context, userID int64) ([]model.BookshelfWithNovel, error) {
	query := `
		SELECT 
			b.chapter_index, b.scroll_offset_percentage, b.in_shelf, b.updated_at,
			n.id, n.title, n.author, n.cover_url, n.rating, n.status, n.synopsis, n.genres, n.word_count, n.view_count
		FROM bookshelves b
		INNER JOIN novels n ON b.novel_id = n.id
		WHERE b.user_id = $1 AND b.in_shelf = TRUE
		ORDER BY b.updated_at DESC`

	rows, err := db.DB.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []model.BookshelfWithNovel
	for rows.Next() {
		var item model.BookshelfWithNovel
		item.UserID = userID
		item.InShelf = true
		err = rows.Scan(
			&item.ChapterIndex, &item.ScrollOffsetPercentage, &item.InShelf, &item.UpdatedAt,
			&item.Novel.ID, &item.Novel.Title, &item.Novel.Author, &item.Novel.CoverURL,
			&item.Novel.Rating, &item.Novel.Status, &item.Novel.Synopsis, &item.Novel.Genres,
			&item.Novel.WordCount, &item.Novel.ViewCount,
		)
		if err != nil {
			return nil, err
		}
		item.NovelID = item.Novel.ID
		items = append(items, item)
	}

	return items, nil
}

func (r *dbRepository) AddToShelf(ctx context.Context, userID int64, novelID int64) error {
	query := `
		INSERT INTO bookshelves (user_id, novel_id, in_shelf, updated_at)
		VALUES ($1, $2, TRUE, CURRENT_TIMESTAMP)
		ON CONFLICT (user_id, novel_id)
		DO UPDATE SET in_shelf = TRUE, updated_at = CURRENT_TIMESTAMP`

	_, err := db.DB.Exec(ctx, query, userID, novelID)
	return err
}

func (r *dbRepository) RemoveFromShelf(ctx context.Context, userID int64, novelIDs []int64) error {
	query := `
		UPDATE bookshelves
		SET in_shelf = FALSE, updated_at = CURRENT_TIMESTAMP
		WHERE user_id = $1 AND novel_id = ANY($2)`

	_, err := db.DB.Exec(ctx, query, userID, novelIDs)
	return err
}

func (r *dbRepository) GetProgress(ctx context.Context, userID int64, novelID int64) (*model.Bookshelf, error) {
	query := `
		SELECT id, user_id, novel_id, chapter_index, scroll_offset_percentage, in_shelf, updated_at
		FROM bookshelves
		WHERE user_id = $1 AND novel_id = $2`

	var b model.Bookshelf
	err := db.DB.QueryRow(ctx, query, userID, novelID).Scan(
		&b.ID, &b.UserID, &b.NovelID, &b.ChapterIndex, &b.ScrollOffsetPercentage, &b.InShelf, &b.UpdatedAt,
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
	query := `
		INSERT INTO bookshelves (user_id, novel_id, chapter_index, scroll_offset_percentage, in_shelf, updated_at)
		VALUES ($1, $2, $3, $4, FALSE, $5)
		ON CONFLICT (user_id, novel_id)
		DO UPDATE SET 
			chapter_index = EXCLUDED.chapter_index, 
			scroll_offset_percentage = EXCLUDED.scroll_offset_percentage, 
			updated_at = EXCLUDED.updated_at`

	_, err := db.DB.Exec(ctx, query, userID, novelID, chapterIndex, offset, updatedAt)
	return err
}

package novel

import (
	"context"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
)

type Repository interface {
	GetNovels(ctx context.Context, genre string, limit, offset int) ([]model.Novel, error)
	SearchNovels(ctx context.Context, search string, limit, offset int) ([]model.Novel, error)
	GetByID(ctx context.Context, id int64) (*model.Novel, error)
	GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error)
	GetChapter(ctx context.Context, novelID int64, chapterIndex int) (*model.Chapter, error)
	CheckChapterUnlocked(ctx context.Context, userID, novelID int64, chapterIndex int) (bool, error)
	GetUnlockedChapterIndices(ctx context.Context, userID, novelID int64) ([]int, error)
}

type dbRepository struct{}

func NewDBRepository() Repository {
	return &dbRepository{}
}

func (r *dbRepository) GetNovels(ctx context.Context, genre string, limit, offset int) ([]model.Novel, error) {
	var rows pgx.Rows
	var err error

	if genre != "" {
		query := `
			SELECT id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, COALESCE(coin_cost_per_thousand, 5) AS coin_cost_per_thousand, start_pay_chapter_index, created_at
			FROM novels
			WHERE genres @> ARRAY[$1]::VARCHAR[] AND status != 'archived'
			ORDER BY view_count DESC
			LIMIT $2 OFFSET $3`
		rows, err = db.DB.Query(ctx, query, genre, limit, offset)
	} else {
		query := `
			SELECT id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, COALESCE(coin_cost_per_thousand, 5) AS coin_cost_per_thousand, start_pay_chapter_index, created_at
			FROM novels
			WHERE status != 'archived'
			ORDER BY view_count DESC
			LIMIT $1 OFFSET $2`
		rows, err = db.DB.Query(ctx, query, limit, offset)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var novels []model.Novel
	for rows.Next() {
		var n model.Novel
		err = rows.Scan(
			&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating, &n.Status,
			&n.Synopsis, &n.Genres, &n.WordCount, &n.ViewCount,
			&n.CoinCostPerThousand, &n.StartPayChapterIndex, &n.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		novels = append(novels, n)
	}

	return novels, nil
}

func (r *dbRepository) SearchNovels(ctx context.Context, search string, limit, offset int) ([]model.Novel, error) {
	likeSearch := "%" + search + "%"
	query := `
		SELECT id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, COALESCE(coin_cost_per_thousand, 5) AS coin_cost_per_thousand, start_pay_chapter_index, created_at
		FROM novels
		WHERE (title ILIKE $1 OR author ILIKE $1 OR synopsis ILIKE $1) AND status != 'archived'
		ORDER BY view_count DESC
		LIMIT $2 OFFSET $3`

	rows, err := db.DB.Query(ctx, query, likeSearch, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var novels []model.Novel
	for rows.Next() {
		var n model.Novel
		err = rows.Scan(
			&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating, &n.Status,
			&n.Synopsis, &n.Genres, &n.WordCount, &n.ViewCount,
			&n.CoinCostPerThousand, &n.StartPayChapterIndex, &n.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		novels = append(novels, n)
	}

	return novels, nil
}

func (r *dbRepository) GetByID(ctx context.Context, id int64) (*model.Novel, error) {
	query := `
		SELECT id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, COALESCE(coin_cost_per_thousand, 5) AS coin_cost_per_thousand, start_pay_chapter_index, created_at
		FROM novels
		WHERE id = $1`

	var n model.Novel
	err := db.DB.QueryRow(ctx, query, id).Scan(
		&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating, &n.Status,
		&n.Synopsis, &n.Genres, &n.WordCount, &n.ViewCount,
		&n.CoinCostPerThousand, &n.StartPayChapterIndex, &n.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &n, nil
}

// GetChaptersList fetches chapters without content to optimize read performance
func (r *dbRepository) GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error) {
	query := `
		SELECT id, novel_id, chapter_index, title, word_count, is_paid, price, created_at
		FROM chapters
		WHERE novel_id = $1
		ORDER BY chapter_index ASC`

	rows, err := db.DB.Query(ctx, query, novelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chapters []model.Chapter
	for rows.Next() {
		var c model.Chapter
		err = rows.Scan(
			&c.ID, &c.NovelID, &c.ChapterIndex, &c.Title, &c.WordCount, &c.IsPaid, &c.Price, &c.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		chapters = append(chapters, c)
	}

	return chapters, nil
}

func (r *dbRepository) GetChapter(ctx context.Context, novelID int64, chapterIndex int) (*model.Chapter, error) {
	query := `
		SELECT id, novel_id, chapter_index, title, content, word_count, is_paid, price, created_at
		FROM chapters
		WHERE novel_id = $1 AND chapter_index = $2`

	var c model.Chapter
	err := db.DB.QueryRow(ctx, query, novelID, chapterIndex).Scan(
		&c.ID, &c.NovelID, &c.ChapterIndex, &c.Title, &c.Content, &c.WordCount, &c.IsPaid, &c.Price, &c.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &c, nil
}

// CheckChapterUnlocked queries the DB directly to verify if the user has unlocked the chapter
func (r *dbRepository) CheckChapterUnlocked(ctx context.Context, userID, novelID int64, chapterIndex int) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM unlock_records 
			WHERE user_id = $1 AND novel_id = $2 AND chapter_index = $3
		)`
	var exists bool
	err := db.DB.QueryRow(ctx, query, userID, novelID, chapterIndex).Scan(&exists)
	return exists, err
}

func (r *dbRepository) GetUnlockedChapterIndices(ctx context.Context, userID, novelID int64) ([]int, error) {
	query := `
		SELECT chapter_index 
		FROM unlock_records 
		WHERE user_id = $1 AND novel_id = $2
		ORDER BY chapter_index ASC`
	rows, err := db.DB.Query(ctx, query, userID, novelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indices []int
	for rows.Next() {
		var idx int
		if err := rows.Scan(&idx); err == nil {
			indices = append(indices, idx)
		}
	}
	return indices, nil
}

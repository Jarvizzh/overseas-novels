package novel

import (
	"context"
	"fmt"

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
	GetEffectivePricingRule(ctx context.Context, userID int64, novelID int64, promoID int, utmSource, utmCampaign string) (int, int, error)
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

func (r *dbRepository) GetEffectivePricingRule(ctx context.Context, userID int64, novelID int64, promoID int, utmSource, utmCampaign string) (int, int, error) {
	// 1. Try finding promotion link
	var promoStartPay, promoCost *int
	if promoID > 0 {
		_ = db.DB.QueryRow(ctx, "SELECT start_pay_chapter_index, coin_cost_per_thousand FROM promotion_links WHERE id = $1 AND (novel_id = $2 OR novel_id = 0)", promoID, novelID).Scan(&promoStartPay, &promoCost)
	}
	if (promoStartPay == nil && promoCost == nil) && utmSource != "" && utmCampaign != "" {
		_ = db.DB.QueryRow(ctx, "SELECT start_pay_chapter_index, coin_cost_per_thousand FROM promotion_links WHERE utm_source = $1 AND utm_campaign = $2 AND (novel_id = $3 OR novel_id = 0) ORDER BY id DESC LIMIT 1", utmSource, utmCampaign, novelID).Scan(&promoStartPay, &promoCost)
	}
	if (promoStartPay == nil && promoCost == nil) && userID > 0 {
		_ = db.DB.QueryRow(ctx, "SELECT pl.start_pay_chapter_index, pl.coin_cost_per_thousand FROM users u JOIN promotion_links pl ON (u.utm_source = pl.utm_source AND u.utm_campaign = pl.utm_campaign) WHERE u.id = $1 AND u.utm_source IS NOT NULL AND u.utm_source != '' AND (pl.novel_id = $2 OR pl.novel_id = 0) ORDER BY pl.id DESC LIMIT 1", userID, novelID).Scan(&promoStartPay, &promoCost)
	}

	// 2. Fetch novel settings
	var novelStartPay, novelCost *int
	_ = db.DB.QueryRow(ctx, "SELECT start_pay_chapter_index, coin_cost_per_thousand FROM novels WHERE id = $1", novelID).Scan(&novelStartPay, &novelCost)

	// 3. Fetch global settings
	globalStartPay := 3
	globalCost := 5
	var gStartStr, gCostStr string
	_ = db.DB.QueryRow(ctx, "SELECT value FROM system_configs WHERE key = 'global_start_pay_chapter_index'").Scan(&gStartStr)
	if gStartStr != "" {
		_, _ = fmt.Sscanf(gStartStr, "%d", &globalStartPay)
	}
	_ = db.DB.QueryRow(ctx, "SELECT value FROM system_configs WHERE key = 'global_coin_cost_per_thousand'").Scan(&gCostStr)
	if gCostStr != "" {
		_, _ = fmt.Sscanf(gCostStr, "%d", &globalCost)
	}

	// 4. Cascading Priority: Promotion Link > Single Novel > Global Config
	effectiveStartPay := globalStartPay
	if novelStartPay != nil && *novelStartPay > 0 {
		effectiveStartPay = *novelStartPay
	}
	if promoStartPay != nil && *promoStartPay > 0 {
		effectiveStartPay = *promoStartPay
	}

	effectiveCost := globalCost
	if novelCost != nil && *novelCost > 0 {
		effectiveCost = *novelCost
	}
	if promoCost != nil && *promoCost > 0 {
		effectiveCost = *promoCost
	}

	return effectiveStartPay, effectiveCost, nil
}

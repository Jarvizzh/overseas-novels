package novel

import (
	"context"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

type NovelRepository interface {
	BeginTx(ctx context.Context) (pgx.Tx, error)
	CountNovels(ctx context.Context, whereSQL string, args []interface{}) (int, error)
	ListNovels(ctx context.Context, selectQuery string, args []interface{}) ([]Novel, error)
	GetNovel(ctx context.Context, id int64) (*Novel, error)
	CreateNovel(ctx context.Context, n *Novel) error
	UpdateNovel(ctx context.Context, n *Novel) error
	DeleteNovel(ctx context.Context, id int64) (int64, error)
	UpdateChapterPrices(ctx context.Context, startIdx int, cost *int, novelID int64) error
	BatchUpdateAllNovelChapterPrices(ctx context.Context, startPayChapterIndex int, coinCostPerThousand int) error

	ListChapters(ctx context.Context, novelID int64) ([]Chapter, error)
	GetChapter(ctx context.Context, novelID int64, chapterIndex int) (*Chapter, error)
	CreateChapter(ctx context.Context, c *Chapter) error
	CreateChapterTx(ctx context.Context, tx pgx.Tx, c *Chapter) error
	UpdateChapter(ctx context.Context, c *Chapter) error
	DeleteChaptersByNovelIDTx(ctx context.Context, tx pgx.Tx, novelID int64) error

	GetSettings(ctx context.Context) (map[string]string, error)
	UpdateSettingTx(ctx context.Context, tx pgx.Tx, key, value string) error

	ListPromotionLinks(ctx context.Context) ([]PromotionLink, error)
	CreatePromotionLink(ctx context.Context, link *PromotionLink) (int, error)
	UpdatePromotionLink(ctx context.Context, id int, name string, chapterIndex int, source, campaign, url string, pixelID, templateID, domainID, coinCost, startPayIndex *int, domain string) error
	DeletePromotionLink(ctx context.Context, id int) error

	ListFBPixels(ctx context.Context) ([]FBPixel, error)
	CreateFBPixel(ctx context.Context, pixel *FBPixel) (int, error)
	UpdateFBPixel(ctx context.Context, id int, name, pixelID, token string) error
	DeleteFBPixel(ctx context.Context, id int) error
}

type dbNovelRepository struct{}

func NewNovelRepository() NovelRepository {
	return &dbNovelRepository{}
}

func (r *dbNovelRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return db.DB.Begin(ctx)
}

func (r *dbNovelRepository) CountNovels(ctx context.Context, whereSQL string, args []interface{}) (int, error) {
	var total int
	err := db.DB.QueryRow(ctx, whereSQL, args...).Scan(&total)
	return total, err
}

func (r *dbNovelRepository) ListNovels(ctx context.Context, selectQuery string, args []interface{}) ([]Novel, error) {
	rows, err := db.DB.Query(ctx, selectQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var novels []Novel
	for rows.Next() {
		var n Novel
		err := rows.Scan(
			&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating,
			&n.Status, &n.Synopsis, &n.Genres, &n.WordCount, &n.ViewCount,
			&n.CoinCostPerThousand, &n.StartPayChapterIndex, &n.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		if n.Genres == nil {
			n.Genres = []string{}
		}
		novels = append(novels, n)
	}
	return novels, nil
}

func (r *dbNovelRepository) GetNovel(ctx context.Context, id int64) (*Novel, error) {
	query := `
		SELECT id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, COALESCE(coin_cost_per_thousand, (SELECT value::integer FROM system_configs WHERE key = 'global_coin_cost_per_thousand'), 500) AS coin_cost_per_thousand, start_pay_chapter_index, created_at
		FROM novels
		WHERE id = $1`

	var n Novel
	err := db.DB.QueryRow(ctx, query, id).Scan(
		&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating,
		&n.Status, &n.Synopsis, &n.Genres, &n.WordCount, &n.ViewCount,
		&n.CoinCostPerThousand, &n.StartPayChapterIndex, &n.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if n.Genres == nil {
		n.Genres = []string{}
	}
	return &n, nil
}

func (r *dbNovelRepository) CreateNovel(ctx context.Context, n *Novel) error {
	query := `
		INSERT INTO novels (title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 0, $8, $9)
		RETURNING id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, COALESCE(coin_cost_per_thousand, (SELECT value::integer FROM system_configs WHERE key = 'global_coin_cost_per_thousand'), 500) AS coin_cost_per_thousand, start_pay_chapter_index, created_at`

	return db.DB.QueryRow(ctx, query, n.Title, n.Author, n.CoverURL, n.Rating, n.Status, n.Synopsis, n.Genres, n.CoinCostPerThousand, n.StartPayChapterIndex).Scan(
		&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating,
		&n.Status, &n.Synopsis, &n.Genres, &n.WordCount, &n.ViewCount,
		&n.CoinCostPerThousand, &n.StartPayChapterIndex, &n.CreatedAt,
	)
}

func (r *dbNovelRepository) UpdateNovel(ctx context.Context, n *Novel) error {
	query := `
		UPDATE novels
		SET title = $1, author = $2, cover_url = $3, rating = $4, status = $5, synopsis = $6, genres = $7, coin_cost_per_thousand = $8, start_pay_chapter_index = $9
		WHERE id = $10
		RETURNING id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, COALESCE(coin_cost_per_thousand, (SELECT value::integer FROM system_configs WHERE key = 'global_coin_cost_per_thousand'), 500) AS coin_cost_per_thousand, start_pay_chapter_index, created_at`

	return db.DB.QueryRow(ctx, query, n.Title, n.Author, n.CoverURL, n.Rating, n.Status, n.Synopsis, n.Genres, n.CoinCostPerThousand, n.StartPayChapterIndex, n.ID).Scan(
		&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating,
		&n.Status, &n.Synopsis, &n.Genres, &n.WordCount, &n.ViewCount,
		&n.CoinCostPerThousand, &n.StartPayChapterIndex, &n.CreatedAt,
	)
}

func (r *dbNovelRepository) DeleteNovel(ctx context.Context, id int64) (int64, error) {
	tag, err := db.DB.Exec(ctx, "UPDATE novels SET status = 'archived' WHERE id = $1 AND status != 'archived'", id)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *dbNovelRepository) UpdateChapterPrices(ctx context.Context, startIdx int, cost *int, novelID int64) error {
	// startIdx is 1-based (e.g. Chapter 3 means chapter_index >= 2 is paid)
	_, err := db.DB.Exec(ctx, `
		UPDATE chapters
		SET 
			is_paid = (chapter_index >= ($1 - 1)),
			price = CASE 
				WHEN (chapter_index >= ($1 - 1)) 
				THEN ROUND((word_count::decimal / 1000.0) * COALESCE($2, (SELECT value::integer FROM system_configs WHERE key = 'global_coin_cost_per_thousand'), 500))::integer 
				ELSE 0 
			END
		WHERE novel_id = $3
	`, startIdx, cost, novelID)
	return err
}

func (r *dbNovelRepository) BatchUpdateAllNovelChapterPrices(ctx context.Context, startPayChapterIndex int, coinCostPerThousand int) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE novels 
		SET 
			start_pay_chapter_index = $1, 
			coin_cost_per_thousand = $2
	`, startPayChapterIndex, coinCostPerThousand)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE chapters c
		SET 
			is_paid = (c.chapter_index >= ($1 - 1)),
			price = CASE 
				WHEN (c.chapter_index >= ($1 - 1)) 
				THEN ROUND((c.word_count::decimal / 1000.0) * $2)::integer 
				ELSE 0 
			END
		FROM novels n
		WHERE c.novel_id = n.id
	`, startPayChapterIndex, coinCostPerThousand)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *dbNovelRepository) ListChapters(ctx context.Context, novelID int64) ([]Chapter, error) {
	query := `
		SELECT id, novel_id, chapter_index, title, '' AS content, word_count, is_paid, price, created_at
		FROM chapters
		WHERE novel_id = $1
		ORDER BY chapter_index ASC`
	rows, err := db.DB.Query(ctx, query, novelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chapters []Chapter
	for rows.Next() {
		var c Chapter
		err = rows.Scan(&c.ID, &c.NovelID, &c.ChapterIndex, &c.Title, &c.Content, &c.WordCount, &c.IsPaid, &c.Price, &c.CreatedAt)
		if err != nil {
			return nil, err
		}
		chapters = append(chapters, c)
	}
	return chapters, nil
}

func (r *dbNovelRepository) GetChapter(ctx context.Context, novelID int64, chapterIndex int) (*Chapter, error) {
	query := `
		SELECT id, novel_id, chapter_index, title, content, word_count, is_paid, price, created_at
		FROM chapters
		WHERE novel_id = $1 AND chapter_index = $2`

	var c Chapter
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

func (r *dbNovelRepository) CreateChapter(ctx context.Context, c *Chapter) error {
	query := `
		INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`
	return db.DB.QueryRow(ctx, query, c.ID, c.NovelID, c.ChapterIndex, c.Title, c.Content, c.WordCount, c.IsPaid, c.Price).Scan(&c.ID, &c.CreatedAt)
}

func (r *dbNovelRepository) CreateChapterTx(ctx context.Context, tx pgx.Tx, c *Chapter) error {
	query := `
		INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`
	return tx.QueryRow(ctx, query, c.ID, c.NovelID, c.ChapterIndex, c.Title, c.Content, c.WordCount, c.IsPaid, c.Price).Scan(&c.ID, &c.CreatedAt)
}

func (r *dbNovelRepository) UpdateChapter(ctx context.Context, c *Chapter) error {
	query := `
		UPDATE chapters
		SET title = $1, content = $2, word_count = $3, is_paid = $4, price = $5
		WHERE novel_id = $6 AND chapter_index = $7`
	_, err := db.DB.Exec(ctx, query, c.Title, c.Content, c.WordCount, c.IsPaid, c.Price, c.NovelID, c.ChapterIndex)
	return err
}

func (r *dbNovelRepository) DeleteChaptersByNovelIDTx(ctx context.Context, tx pgx.Tx, novelID int64) error {
	_, err := tx.Exec(ctx, "DELETE FROM chapters WHERE novel_id = $1", novelID)
	return err
}

func (r *dbNovelRepository) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := db.DB.Query(ctx, "SELECT key, value FROM system_configs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var key, val string
		if err := rows.Scan(&key, &val); err != nil {
			return nil, err
		}
		settings[key] = val
	}
	return settings, nil
}

func (r *dbNovelRepository) UpdateSettingTx(ctx context.Context, tx pgx.Tx, key, value string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO system_configs (key, value) 
		VALUES ($2, $1) 
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, value, key)
	return err
}

func (r *dbNovelRepository) ListPromotionLinks(ctx context.Context) ([]PromotionLink, error) {
	query := `
		SELECT pl.id, pl.name, pl.novel_id, pl.novel_title, pl.chapter_index, pl.utm_source, pl.utm_campaign, 
		       pl.generated_url, pl.fb_pixel_id, pl.recharge_template_id, pl.domain_id, 
		       COALESCE(sd.domain, pl.domain, '') AS domain, 
		       pl.coin_cost_per_thousand, pl.start_pay_chapter_index, pl.created_at
		FROM promotion_links pl
		LEFT JOIN system_domains sd ON pl.domain_id = sd.id
		ORDER BY pl.created_at DESC`
	rows, err := db.DB.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []PromotionLink
	for rows.Next() {
		var link PromotionLink
		err = rows.Scan(
			&link.ID, &link.Name, &link.NovelID, &link.NovelTitle, &link.ChapterIndex,
			&link.UtmSource, &link.UtmCampaign, &link.GeneratedURL, &link.FBPixelID, &link.RechargeTemplateID,
			&link.DomainID, &link.Domain,
			&link.CoinCostPerThousand, &link.StartPayChapterIndex, &link.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, nil
}

func (r *dbNovelRepository) CreatePromotionLink(ctx context.Context, link *PromotionLink) (int, error) {
	query := `
		INSERT INTO promotion_links (name, novel_id, novel_title, chapter_index, utm_source, utm_campaign, generated_url, fb_pixel_id, recharge_template_id, domain_id, domain, coin_cost_per_thousand, start_pay_chapter_index)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at`
	var id int
	err := db.DB.QueryRow(ctx, query, link.Name, link.NovelID, link.NovelTitle, link.ChapterIndex, link.UtmSource, link.UtmCampaign, link.GeneratedURL, link.FBPixelID, link.RechargeTemplateID, link.DomainID, link.Domain, link.CoinCostPerThousand, link.StartPayChapterIndex).Scan(&id, &link.CreatedAt)
	return id, err
}

func (r *dbNovelRepository) UpdatePromotionLink(ctx context.Context, id int, name string, chapterIndex int, source, campaign, url string, pixelID, templateID, domainID, coinCost, startPayIndex *int, domain string) error {
	query := `
		UPDATE promotion_links
		SET name = $1, chapter_index = $2, utm_source = $3, utm_campaign = $4, generated_url = $5, fb_pixel_id = $6, recharge_template_id = $7, domain_id = $8, domain = $9, coin_cost_per_thousand = $10, start_pay_chapter_index = $11
		WHERE id = $12`
	_, err := db.DB.Exec(ctx, query, name, chapterIndex, source, campaign, url, pixelID, templateID, domainID, domain, coinCost, startPayIndex, id)
	return err
}

func (r *dbNovelRepository) DeletePromotionLink(ctx context.Context, id int) error {
	_, err := db.DB.Exec(ctx, "DELETE FROM promotion_links WHERE id = $1", id)
	return err
}

func (r *dbNovelRepository) ListFBPixels(ctx context.Context) ([]FBPixel, error) {
	query := `SELECT id, name, pixel_id, access_token, created_at FROM fb_pixels ORDER BY created_at DESC`
	rows, err := db.DB.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pixels []FBPixel
	for rows.Next() {
		var pixel FBPixel
		err = rows.Scan(&pixel.ID, &pixel.Name, &pixel.PixelID, &pixel.AccessToken, &pixel.CreatedAt)
		if err != nil {
			return nil, err
		}
		pixels = append(pixels, pixel)
	}
	return pixels, nil
}

func (r *dbNovelRepository) CreateFBPixel(ctx context.Context, pixel *FBPixel) (int, error) {
	query := `
		INSERT INTO fb_pixels (name, pixel_id, access_token)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`
	var id int
	err := db.DB.QueryRow(ctx, query, pixel.Name, pixel.PixelID, pixel.AccessToken).Scan(&id, &pixel.CreatedAt)
	return id, err
}

func (r *dbNovelRepository) UpdateFBPixel(ctx context.Context, id int, name, pixelID, token string) error {
	query := `
		UPDATE fb_pixels
		SET name = $1, pixel_id = $2, access_token = $3
		WHERE id = $4`
	_, err := db.DB.Exec(ctx, query, name, pixelID, token, id)
	return err
}

func (r *dbNovelRepository) DeleteFBPixel(ctx context.Context, id int) error {
	_, err := db.DB.Exec(ctx, "DELETE FROM fb_pixels WHERE id = $1", id)
	return err
}

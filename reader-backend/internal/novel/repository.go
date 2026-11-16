package novel

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
)

type Repository interface {
	GetNovels(ctx context.Context, genre, sortBy string, limit, offset int) ([]model.Novel, error)
	SearchNovels(ctx context.Context, search string, limit, offset int) ([]model.Novel, error)
	GetByID(ctx context.Context, id int64) (*model.Novel, error)
	GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error)
	GetChapter(ctx context.Context, novelID int64, chapterIndex int) (*model.Chapter, error)
	GetUnlockedChapterIndices(ctx context.Context, userID, novelID int64) ([]int, error)
	HasActiveSubscription(ctx context.Context, userID int64) (bool, error)
	HasWholeBook(context.Context, int64, int64) (bool, error)
	GetEffectivePricingRule(ctx context.Context, userID int64, novelID int64, promoID int, utmSource, utmCampaign string) (int, int, error)
}

type dbRepository struct{}

const PaidSubscriptionPredicate = `status IN ('ACTIVE','CANCELLED','CANCEL_PENDING') AND current_period_end > now() AND last_payment_time IS NOT NULL AND subscription_id NOT LIKE 'I-MOCK%'
 AND (
  NOT EXISTS(SELECT 1 FROM recharge_orders paid WHERE paid.subscription_id=user_subscriptions.subscription_id AND paid.paid_period_end IS NOT NULL)
  OR EXISTS(SELECT 1 FROM recharge_orders paid WHERE paid.subscription_id=user_subscriptions.subscription_id
   AND paid.status IN ('Success','Paid','RefundPending') AND NOT paid.paypal_refund_reported
   AND COALESCE(paid.paid_period_start,paid.paid_at)<=now() AND paid.paid_period_end>now())
 )`

const WholeBookEntitlementPredicate = `order_type='whole_book' AND status IN ('Success','Paid','RefundPending') AND fulfilled_at IS NOT NULL
 AND (checkout_id IS NOT NULL OR EXISTS(SELECT 1 FROM legacy_whole_book_proofs proof
  WHERE proof.order_id=recharge_orders.id AND proof.novel_id=recharge_orders.novel_id))`

func (r *dbRepository) HasWholeBook(ctx context.Context, userID, novelID int64) (bool, error) {
	var ok bool
	err := db.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM recharge_orders WHERE user_id=$1 AND novel_id=$2 AND `+WholeBookEntitlementPredicate+`)`, userID, novelID).Scan(&ok)
	return ok, err
}

func HasReadingPass(ctx context.Context, repo Repository, userID, novelID int64) (bool, error) {
	if userID <= 0 {
		return false, nil
	}
	vip, err := repo.HasActiveSubscription(ctx, userID)
	if vip || err != nil {
		return vip, err
	}
	return repo.HasWholeBook(ctx, userID, novelID)
}

func NewDBRepository() Repository {
	return &dbRepository{}
}

const readerNovelSelectColumns = `id, title, author, cover_url, rating, status, publish_status, synopsis, genres, word_count, view_count, COALESCE(coin_cost_per_thousand, (SELECT value::integer FROM system_configs WHERE key = 'global_coin_cost_per_thousand'), 500) AS coin_cost_per_thousand, start_pay_chapter_index, created_at`

func scanReaderNovel(scanner interface{ Scan(dest ...any) error }, n *model.Novel) error {
	err := scanner.Scan(
		&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating, &n.Status, &n.PublishStatus,
		&n.Synopsis, &n.Genres, &n.WordCount, &n.ViewCount,
		&n.CoinCostPerThousand, &n.StartPayChapterIndex, &n.CreatedAt,
	)
	if err != nil {
		return err
	}
	if n.Genres == nil {
		n.Genres = []string{}
	}
	return nil
}

func (r *dbRepository) GetNovels(ctx context.Context, genre, sortBy string, limit, offset int) ([]model.Novel, error) {
	var rows pgx.Rows
	var err error

	orderSQL := "ORDER BY view_count DESC, created_at DESC"
	if sortBy == "latest" {
		orderSQL = "ORDER BY created_at DESC, id DESC"
	}

	if genre != "" {
		query := fmt.Sprintf(`
			SELECT %s
			FROM novels
			WHERE genres @> ARRAY[$1]::VARCHAR[] AND status != 'archived' AND publish_status = 'published'
			%s
			LIMIT $2 OFFSET $3`, readerNovelSelectColumns, orderSQL)
		rows, err = db.DB.Query(ctx, query, genre, limit, offset)
	} else {
		query := fmt.Sprintf(`
			SELECT %s
			FROM novels
			WHERE status != 'archived' AND publish_status = 'published'
			%s
			LIMIT $1 OFFSET $2`, readerNovelSelectColumns, orderSQL)
		rows, err = db.DB.Query(ctx, query, limit, offset)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	novels := []model.Novel{}
	for rows.Next() {
		var n model.Novel
		if err := scanReaderNovel(rows, &n); err != nil {
			return nil, err
		}
		novels = append(novels, n)
	}

	return novels, nil
}

func (r *dbRepository) SearchNovels(ctx context.Context, search string, limit, offset int) ([]model.Novel, error) {
	likeSearch := "%" + search + "%"
	query := fmt.Sprintf(`
		SELECT %s
		FROM novels
		WHERE (title ILIKE $1 OR author ILIKE $1 OR synopsis ILIKE $1) AND status != 'archived' AND publish_status = 'published'
		ORDER BY view_count DESC, created_at DESC
		LIMIT $2 OFFSET $3`, readerNovelSelectColumns)

	rows, err := db.DB.Query(ctx, query, likeSearch, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	novels := []model.Novel{}
	for rows.Next() {
		var n model.Novel
		if err := scanReaderNovel(rows, &n); err != nil {
			return nil, err
		}
		novels = append(novels, n)
	}

	return novels, nil
}

func (r *dbRepository) GetByID(ctx context.Context, id int64) (*model.Novel, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM novels
		WHERE id = $1 AND status != 'archived' AND publish_status = 'published'`, readerNovelSelectColumns)

	var n model.Novel
	err := scanReaderNovel(db.DB.QueryRow(ctx, query, id), &n)
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
		SELECT c.id, c.novel_id, c.chapter_index, c.title, c.word_count, c.is_paid, c.price, c.created_at
		FROM chapters c
		INNER JOIN novels n ON c.novel_id = n.id
		WHERE c.novel_id = $1 AND n.status != 'archived' AND n.publish_status = 'published'
		ORDER BY c.chapter_index ASC`

	rows, err := db.DB.Query(ctx, query, novelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chapters := []model.Chapter{}
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
		SELECT c.id, c.novel_id, c.chapter_index, c.title, c.content, c.word_count, c.is_paid, c.price, c.created_at
		FROM chapters c
		INNER JOIN novels n ON c.novel_id = n.id
		WHERE c.novel_id = $1 AND c.chapter_index = $2 AND n.status != 'archived' AND n.publish_status = 'published'`

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

func (r *dbRepository) HasActiveSubscription(ctx context.Context, userID int64) (bool, error) {
	if userID <= 0 {
		return false, nil
	}
	var exists bool
	query := `
		SELECT EXISTS(
			SELECT 1 FROM user_subscriptions 
			WHERE user_id = $1 AND ` + PaidSubscriptionPredicate + `
		)`
	err := db.DB.QueryRow(ctx, query, userID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (r *dbRepository) GetEffectivePricingRule(ctx context.Context, userID int64, novelID int64, promoID int, utmSource, utmCampaign string) (int, int, error) {
	var start, cost int
	err := db.DB.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(pl.start_pay_chapter_index, 0), NULLIF(n.start_pay_chapter_index, 0),
			(SELECT value::int FROM system_configs WHERE key = 'global_start_pay_chapter_index'), 3),
		       COALESCE(NULLIF(pl.coin_cost_per_thousand, 0), NULLIF(n.coin_cost_per_thousand, 0),
			(SELECT value::int FROM system_configs WHERE key = 'global_coin_cost_per_thousand'), 500)
		FROM novels n
		LEFT JOIN LATERAL (
			SELECT p.start_pay_chapter_index, p.coin_cost_per_thousand
			FROM users u JOIN promotion_links p ON p.utm_source = u.utm_source AND p.utm_campaign = u.utm_campaign
			WHERE u.id = $1 AND p.novel_id = n.id AND u.utm_source <> '' AND u.utm_campaign <> ''
			ORDER BY p.id DESC LIMIT 1
		) pl ON TRUE
		WHERE n.id = $2 AND n.status <> 'archived' AND n.publish_status = 'published'`, userID, novelID).Scan(&start, &cost)
	if err != nil {
		return 0, 0, err
	}
	if start <= 0 || cost < 0 {
		return 0, 0, fmt.Errorf("invalid chapter pricing configuration")
	}
	return start, cost, nil
}

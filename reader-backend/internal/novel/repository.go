package novel

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
)

type OperationsCatalog struct {
	Featured    []model.Novel `json:"featured"`
	Curated     []model.Novel `json:"curated"`
	Rankings    []model.Novel `json:"rankings"`
	RankingRule string        `json:"ranking_rule"`
}

type Repository interface {
	ReadSnapshot(ctx context.Context, userID, novelID int64, promoID int, index *int) (ReadSnapshot, error)
	GetContentVersion(context.Context, int64) (int64, error)
	GetNovels(ctx context.Context, genre, sortBy string, limit, offset int) ([]model.Novel, error)
	SearchNovels(ctx context.Context, search string, limit, offset int) ([]model.Novel, error)
	GetByID(ctx context.Context, id int64) (*model.Novel, error)
	GetOperationsCatalog(ctx context.Context) (*OperationsCatalog, error)
	GetChaptersList(ctx context.Context, novelID int64) ([]model.Chapter, error)
	GetChapter(ctx context.Context, novelID int64, chapterIndex int) (*model.Chapter, error)
	GetUnlockedChapterIndices(ctx context.Context, userID, novelID int64) ([]int, error)
	CheckChapterAccess(ctx context.Context, userID, novelID int64, chapterID string) (bool, bool, error)
	HasActiveSubscription(ctx context.Context, userID int64) (bool, error)
	HasWholeBook(context.Context, int64, int64) (bool, error)
	GetEffectivePricingRule(ctx context.Context, userID int64, novelID int64, promoID int, utmSource, utmCampaign string) (int, int, error)
}

type dbRepository struct{}

const PaidSubscriptionPredicate = `(status IN ('ACTIVE','CANCELLED','CANCEL_PENDING','SUSPENDED')
  OR (payment_method='stripe' AND status IN ('SUSPENDED','PENDING'))) AND current_period_end > now() AND last_payment_time IS NOT NULL AND subscription_id NOT LIKE 'I-MOCK%'
 AND (
  (payment_method IS DISTINCT FROM 'stripe' AND NOT EXISTS(
   SELECT 1 FROM recharge_orders paid WHERE paid.subscription_id=user_subscriptions.subscription_id AND paid.paid_period_end IS NOT NULL))
  OR EXISTS(SELECT 1 FROM recharge_orders paid WHERE paid.subscription_id=user_subscriptions.subscription_id
   AND paid.status IN ('Success','Paid','RefundPending') AND NOT paid.paypal_refund_reported
   AND (user_subscriptions.payment_method IS DISTINCT FROM 'stripe' OR (paid.payment_method='stripe' AND paid.fulfilled_at IS NOT NULL
       AND EXISTS(SELECT 1 FROM payment_stripe_invoice_periods p WHERE p.order_id=paid.id AND p.admitted AND p.intent_id=user_subscriptions.checkout_intent_id)))
   AND NOT EXISTS(SELECT 1 FROM payment_entitlement_adjustments adj WHERE adj.order_id=paid.id AND adj.blocked)
   AND NOT EXISTS(SELECT 1 FROM payment_deferred_fulfillment h WHERE h.order_id=paid.id AND h.resolved_at IS NULL)
   AND COALESCE(paid.paid_period_start,paid.paid_at)<=now() AND paid.paid_period_end>now())
 )`

const WholeBookEntitlementPredicate = `order_type='whole_book' AND status IN ('Success','Paid','RefundPending') AND fulfilled_at IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM payment_entitlement_adjustments adj WHERE adj.order_id=recharge_orders.id AND adj.blocked)
 AND (checkout_id IS NOT NULL OR EXISTS(SELECT 1 FROM legacy_whole_book_proofs proof
  WHERE proof.order_id=recharge_orders.id AND proof.novel_id=recharge_orders.novel_id))`

// WholeBookEnvironmentPredicate compares the deployment environment with the
// order snapshot and verified channel receipts. The argument is a SQL parameter.
func WholeBookEnvironmentPredicate(environmentParam string) string {
	return `payment_order_environment(recharge_orders)=CASE ` + environmentParam + `
		WHEN 'production' THEN 'live' WHEN 'sandbox' THEN 'sandbox' END
		AND (checkout_environment=` + environmentParam + ` OR (checkout_environment IS NULL AND EXISTS(
			SELECT 1 FROM third_party_payment_orders receipt JOIN payment_channel_accounts account ON account.id=receipt.account_id
			WHERE receipt.order_id=recharge_orders.id AND receipt.status='VERIFIED'
			AND receipt.payment_provider=recharge_orders.payment_method
		)))`
}

func (r *dbRepository) HasWholeBook(ctx context.Context, userID, novelID int64) (bool, error) {
	var ok bool
	err := db.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM recharge_orders WHERE user_id=$1 AND novel_id=$2 AND `+WholeBookEntitlementPredicate+` AND `+WholeBookEnvironmentPredicate("$3")+`)`, userID, novelID, conversion.Environment()).Scan(&ok)
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

const readerNovelSelectColumns = `id, title, author, COALESCE(cover_url, ''), COALESCE(rating, 0), COALESCE(status, 'Ongoing'), publish_status, COALESCE(synopsis, ''), COALESCE(genres, '{}'), COALESCE(word_count, 0), COALESCE(view_count, 0), COALESCE(coin_cost_per_thousand, (SELECT value::integer FROM system_configs WHERE key = 'global_coin_cost_per_thousand'), 500) AS coin_cost_per_thousand, COALESCE(start_pay_chapter_index, 3), created_at, content_version`

func scanReaderNovel(scanner interface{ Scan(dest ...any) error }, n *model.Novel) error {
	err := scanner.Scan(
		&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating, &n.Status, &n.PublishStatus,
		&n.Synopsis, &n.Genres, &n.WordCount, &n.ViewCount,
		&n.CoinCostPerThousand, &n.StartPayChapterIndex, &n.CreatedAt, &n.ContentVersion,
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

	orderSQL := "ORDER BY view_count DESC, created_at DESC, id DESC"
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

	return novels, rows.Err()
}

func (r *dbRepository) SearchNovels(ctx context.Context, search string, limit, offset int) ([]model.Novel, error) {
	likeSearch := "%" + search + "%"
	query := fmt.Sprintf(`
		SELECT %s
		FROM novels
		WHERE (title ILIKE $1 OR author ILIKE $1 OR synopsis ILIKE $1) AND status != 'archived' AND publish_status = 'published'
		ORDER BY view_count DESC, created_at DESC, id DESC
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

	return novels, rows.Err()
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
		SELECT c.id, c.novel_id, c.chapter_index, c.title, c.word_count, c.created_at
		FROM chapters c
		INNER JOIN novels n ON c.novel_id = n.id
		WHERE c.novel_id = $1 AND c.chapter_index IS NOT NULL AND ($2::bigint = 0 OR n.content_version = $2) AND n.status != 'archived' AND n.publish_status = 'published'
		ORDER BY c.chapter_index ASC`

	rows, err := db.DB.Query(ctx, query, novelID, contentVersion(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chapters := []model.Chapter{}
	for rows.Next() {
		var c model.Chapter
		err = rows.Scan(
			&c.ID, &c.NovelID, &c.ChapterIndex, &c.Title, &c.WordCount, &c.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		chapters = append(chapters, c)
	}

	return chapters, rows.Err()
}

func (r *dbRepository) GetChapter(ctx context.Context, novelID int64, chapterIndex int) (*model.Chapter, error) {
	query := `
		SELECT c.id, c.novel_id, c.chapter_index, c.title, c.content, c.word_count, c.is_paid, c.price, c.created_at, COALESCE(c.content_object_key, ''), COALESCE(c.content_sha256, ''), c.body_revision
		FROM chapters c
		INNER JOIN novels n ON c.novel_id = n.id
		WHERE c.novel_id = $1 AND c.chapter_index = $2 AND ($3::bigint = 0 OR n.content_version = $3) AND n.status != 'archived' AND n.publish_status = 'published'`

	var c model.Chapter
	err := db.DB.QueryRow(ctx, query, novelID, chapterIndex, contentVersion(ctx)).Scan(
		&c.ID, &c.NovelID, &c.ChapterIndex, &c.Title, &c.Content, &c.WordCount, &c.IsPaid, &c.Price, &c.CreatedAt, &c.ObjectKey, &c.ContentHash, &c.BodyRevision,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &c, nil
}

// CheckChapterAccess reports a resolved entitlement and whether older purchases still need an audit.
func (r *dbRepository) CheckChapterAccess(ctx context.Context, userID, novelID int64, chapterID string) (bool, bool, error) {
	var unlocked, needsReview bool
	err := db.DB.QueryRow(ctx, `SELECT reader_has_chapter_unlock($1, $3),
		EXISTS(SELECT 1 FROM unlock_records WHERE user_id=$1 AND novel_id=$2 AND chapter_id IS NULL)`,
		userID, novelID, chapterID).Scan(&unlocked, &needsReview)
	return unlocked, needsReview, err
}

func (r *dbRepository) GetUnlockedChapterIndices(ctx context.Context, userID, novelID int64) ([]int, error) {
	query := `
		SELECT chapter_index FROM chapters
		WHERE novel_id = $2 AND chapter_index IS NOT NULL AND reader_has_chapter_unlock($1, id)
		ORDER BY chapter_index`
	rows, err := db.DB.Query(ctx, query, userID, novelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indices []int
	for rows.Next() {
		var idx int
		if err := rows.Scan(&idx); err != nil {
			return nil, err
		}
		indices = append(indices, idx)
	}
	return indices, rows.Err()
}

func (r *dbRepository) HasActiveSubscription(ctx context.Context, userID int64) (bool, error) {
	if userID <= 0 {
		return false, nil
	}
	var exists bool
	query := `
		SELECT EXISTS(
			SELECT 1 FROM user_subscriptions 
			WHERE user_id = $1 AND payment_subscription_environment(user_subscriptions)=$2 AND ` + PaidSubscriptionPredicate + `
		)`
	err := db.DB.QueryRow(ctx, query, userID, conversion.Environment()).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (r *dbRepository) GetEffectivePricingRule(ctx context.Context, userID int64, novelID int64, promoID int, utmSource, utmCampaign string) (int, int, error) {
	return ReadChapterPricing(ctx, db.DB, userID, novelID, promoID)
}

// ReadChapterPricing resolves the same rule for quotes and transactional settlement.
func ReadChapterPricing(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID, novelID int64, promoID int) (int, int, error) {
	var start, cost int
	err := q.QueryRow(ctx, `
		SELECT `+chapterPricingColumns+`
		FROM novels n CROSS JOIN (SELECT $1::bigint user_id, $3::int promo_id) reader
		`+chapterPricingJoin+`
		WHERE n.id=$2 AND n.status<>'archived' AND n.publish_status='published'
		  AND ($4::bigint=0 OR n.content_version=$4)`, userID, novelID, promoID, contentVersion(ctx)).Scan(&start, &cost)
	if err == pgx.ErrNoRows && contentVersion(ctx) != 0 {
		return 0, 0, ErrContentChanged
	}
	if err != nil {
		return 0, 0, err
	}
	if start <= 0 || cost < 0 {
		return 0, 0, fmt.Errorf("invalid chapter pricing configuration")
	}
	return start, cost, nil
}

func (r *dbRepository) GetContentVersion(ctx context.Context, id int64) (int64, error) {
	var version int64
	err := db.DB.QueryRow(ctx, `SELECT content_version FROM novels WHERE id=$1 AND status<>'archived' AND publish_status='published'`, id).Scan(&version)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	return version, err
}

func (r *dbRepository) GetOperationsCatalog(ctx context.Context) (*OperationsCatalog, error) {
	rankingRule := "views"
	rankingLimit := 20

	cfgRows, err := db.DB.Query(ctx, `SELECT key, value FROM system_configs WHERE key IN ('ranking_sort_rule', 'ranking_limit')`)
	if err != nil {
		return nil, err
	}
	for cfgRows.Next() {
		var k, v string
		if err := cfgRows.Scan(&k, &v); err != nil {
			cfgRows.Close()
			return nil, err
		}
		switch k {
		case "ranking_sort_rule":
			switch v {
			case "views", "rating", "latest", "manual":
				rankingRule = v
			}
		case "ranking_limit":
			var parsed int
			if _, scanErr := fmt.Sscanf(v, "%d", &parsed); scanErr == nil && parsed >= 1 && parsed <= 100 {
				rankingLimit = parsed
			}
		}
	}
	if err := cfgRows.Err(); err != nil {
		cfgRows.Close()
		return nil, err
	}
	cfgRows.Close()

	recQuery := `
		SELECT r.group_name, c.id, c.title, c.author, c.cover_url_safe, c.rating_safe, c.status_safe, c.publish_status,
			c.synopsis_safe, c.genres_safe, c.word_count_safe, c.view_count_safe, c.coin_cost_per_thousand, c.start_pay_chapter_index_safe,
			c.created_at, c.content_version
		FROM recommendations r
		JOIN (
			SELECT id, title, author, COALESCE(cover_url, '') AS cover_url_safe, COALESCE(rating, 0) AS rating_safe,
				COALESCE(status, 'Ongoing') AS status_safe, publish_status, COALESCE(synopsis, '') AS synopsis_safe,
				COALESCE(genres, '{}') AS genres_safe, COALESCE(word_count, 0) AS word_count_safe,
				COALESCE(view_count, 0) AS view_count_safe,
				COALESCE(coin_cost_per_thousand, (SELECT value::integer FROM system_configs WHERE key = 'global_coin_cost_per_thousand'), 500) AS coin_cost_per_thousand,
				COALESCE(start_pay_chapter_index, 3) AS start_pay_chapter_index_safe,
				created_at, content_version
			FROM novels WHERE status != 'archived' AND publish_status = 'published'
		) c ON c.id = r.novel_id
		WHERE r.group_name IN ('editor_pick', 'curated', 'ranking')
		  AND (r.scheduled_start IS NULL OR r.scheduled_start <= NOW())
		  AND (r.scheduled_end IS NULL OR r.scheduled_end >= NOW())
		ORDER BY r.group_name ASC, r.sort_order ASC, r.id ASC
	`

	recRows, err := db.DB.Query(ctx, recQuery)
	if err != nil {
		return nil, err
	}
	defer recRows.Close()

	catalog := &OperationsCatalog{
		Featured:    []model.Novel{},
		Curated:     []model.Novel{},
		Rankings:    []model.Novel{},
		RankingRule: rankingRule,
	}
	rankedIDs := make(map[int64]bool)

	for recRows.Next() {
		var group string
		var n model.Novel
		if err := recRows.Scan(
			&group,
			&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating, &n.Status, &n.PublishStatus,
			&n.Synopsis, &n.Genres, &n.WordCount, &n.ViewCount,
			&n.CoinCostPerThousand, &n.StartPayChapterIndex, &n.CreatedAt, &n.ContentVersion,
		); err != nil {
			return nil, err
		}
		if n.Genres == nil {
			n.Genres = []string{}
		}
		switch group {
		case "editor_pick":
			catalog.Featured = append(catalog.Featured, n)
		case "curated":
			catalog.Curated = append(catalog.Curated, n)
		case "ranking":
			if len(catalog.Rankings) < rankingLimit {
				catalog.Rankings = append(catalog.Rankings, n)
				rankedIDs[n.ID] = true
			}
		}
	}
	if err := recRows.Err(); err != nil {
		return nil, err
	}

	if len(catalog.Rankings) < rankingLimit && (rankingRule != "manual" || len(catalog.Rankings) == 0) {
		orderSQL := "ORDER BY view_count DESC, created_at DESC, id DESC"
		switch rankingRule {
		case "rating":
			orderSQL = "ORDER BY rating DESC, view_count DESC, id DESC"
		case "latest":
			orderSQL = "ORDER BY created_at DESC, id DESC"
		}
		remaining := rankingLimit - len(catalog.Rankings)
		excluded := make([]int64, 0, len(rankedIDs))
		for id := range rankedIDs {
			excluded = append(excluded, id)
		}
		backfillQuery := fmt.Sprintf(`
			SELECT %s
			FROM novels
			WHERE status != 'archived' AND publish_status = 'published'
			  AND (cardinality($1::int[]) = 0 OR NOT (id = ANY($1::int[])))
			%s
			LIMIT $2
		`, readerNovelSelectColumns, orderSQL)
		fillRows, err := db.DB.Query(ctx, backfillQuery, excluded, remaining)
		if err != nil {
			return nil, err
		}
		defer fillRows.Close()

		for fillRows.Next() {
			var n model.Novel
			if err := scanReaderNovel(fillRows, &n); err != nil {
				return nil, err
			}
			catalog.Rankings = append(catalog.Rankings, n)
		}
		if err := fillRows.Err(); err != nil {
			return nil, err
		}
	}

	return catalog, nil
}

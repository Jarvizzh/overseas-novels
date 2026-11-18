package operations

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

type Repository interface {
	GetRankingConfig(ctx context.Context) (RankingConfig, error)
	SaveRankingConfig(ctx context.Context, cfg RankingConfig) error
	SaveRanking(ctx context.Context, cfg *RankingConfig, items *[]GroupItemInput) error
	GetCheckInConfig(ctx context.Context) (CheckInConfig, error)
	SaveCheckInConfig(ctx context.Context, cfg CheckInConfig) error
	ListRecommendations(ctx context.Context) ([]RecommendationEntry, error)
	ListGroup(ctx context.Context, groupName string) ([]RecommendationEntry, error)
	SaveGroup(ctx context.Context, groupName string, items []GroupItemInput) error
	ListPublishedGenres(ctx context.Context) ([]string, error)
	GetNovelsByIDs(ctx context.Context, ids []int64, publishedOnly bool) (map[int64]RecommendationNovel, error)
	ListTopPublishedNovels(ctx context.Context, sortRule string, excludeIDs []int64, limit int) ([]RecommendationNovel, error)
}

type dbRepository struct{}

func NewRepository() Repository {
	return &dbRepository{}
}

const recommendationNovelSelectCols = `
	n.id, n.title, n.author, COALESCE(n.cover_url, ''), COALESCE(n.rating, 0),
	COALESCE(n.status, 'Ongoing'), COALESCE(n.publish_status, 'published'),
	COALESCE(n.synopsis, ''), COALESCE(n.view_count, 0), COALESCE(n.genres, '{}')
`

func scanRecommendationNovel(scanner interface{ Scan(dest ...any) error }, n *RecommendationNovel) error {
	if err := scanner.Scan(
		&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating,
		&n.Status, &n.PublishStatus, &n.Synopsis, &n.ViewCount, &n.Genres,
	); err != nil {
		return err
	}
	if n.Genres == nil {
		n.Genres = []string{}
	}
	return nil
}

func (r *dbRepository) GetRankingConfig(ctx context.Context) (RankingConfig, error) {
	cfg := RankingConfig{
		SortRule: RankingSortViews,
		Limit:    20,
	}
	rows, err := db.DB.Query(ctx, `SELECT key, value FROM system_configs WHERE key = ANY($1)`, []string{configKeyRankingSortRule, configKeyRankingLimit})
	if err != nil {
		return cfg, err
	}
	defer rows.Close()

	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return cfg, err
		}
		switch k {
		case configKeyRankingSortRule:
			if isValidRankingSortRule(v) {
				cfg.SortRule = v
			}
		case configKeyRankingLimit:
			if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
				cfg.Limit = n
			}
		}
	}
	return cfg, rows.Err()
}

func saveRankingConfigTx(ctx context.Context, tx pgx.Tx, cfg RankingConfig) error {
	upsert := `
		INSERT INTO system_configs (key, value)
		VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`
	if _, err := tx.Exec(ctx, upsert, configKeyRankingSortRule, cfg.SortRule); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, upsert, configKeyRankingLimit, strconv.Itoa(cfg.Limit)); err != nil {
		return err
	}
	return nil
}

func (r *dbRepository) SaveRankingConfig(ctx context.Context, cfg RankingConfig) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := saveRankingConfigTx(ctx, tx, cfg); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *dbRepository) SaveRanking(ctx context.Context, cfg *RankingConfig, items *[]GroupItemInput) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if cfg != nil {
		if err := saveRankingConfigTx(ctx, tx, *cfg); err != nil {
			return err
		}
	}
	if items != nil {
		if err := saveGroupTx(ctx, tx, GroupRanking, *items); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

var defaultCheckInRewards = []int{10, 15, 20, 25, 30, 35, 50}

func parseCheckInGradient(raw string) ([]int, bool) {
	parts := strings.Split(raw, ",")
	if len(parts) != 7 {
		return nil, false
	}
	out := make([]int, 7)
	for i, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > 10000 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

func (r *dbRepository) GetCheckInConfig(ctx context.Context) (CheckInConfig, error) {
	fallback := make([]int, len(defaultCheckInRewards))
	copy(fallback, defaultCheckInRewards)
	cfg := CheckInConfig{Rewards: fallback}

	var raw string
	err := db.DB.QueryRow(ctx, `SELECT value FROM system_configs WHERE key = $1`, configKeyCheckInGradient).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return cfg, nil
		}
		return cfg, err
	}
	if parsed, ok := parseCheckInGradient(raw); ok {
		cfg.Rewards = parsed
	}
	return cfg, nil
}

func (r *dbRepository) SaveCheckInConfig(ctx context.Context, cfg CheckInConfig) error {
	parts := make([]string, len(cfg.Rewards))
	for i, v := range cfg.Rewards {
		parts[i] = strconv.Itoa(v)
	}
	_, err := db.DB.Exec(ctx, `
		INSERT INTO system_configs (key, value)
		VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, configKeyCheckInGradient, strings.Join(parts, ","))
	return err
}

func (r *dbRepository) ListRecommendations(ctx context.Context) ([]RecommendationEntry, error) {
	return r.queryRecommendations(ctx, "")
}

func (r *dbRepository) ListGroup(ctx context.Context, groupName string) ([]RecommendationEntry, error) {
	return r.queryRecommendations(ctx, groupName)
}

func (r *dbRepository) queryRecommendations(ctx context.Context, groupName string) ([]RecommendationEntry, error) {
	base := `
		SELECT
			r.id, r.group_name, r.novel_id, r.sort_order, r.scheduled_start, r.scheduled_end, r.created_at,
			` + recommendationNovelSelectCols + `
		FROM recommendations r
		JOIN novels n ON n.id = r.novel_id
		WHERE n.status != 'archived'
	`
	var rows pgx.Rows
	var err error
	if groupName != "" {
		rows, err = db.DB.Query(ctx, base+` AND r.group_name = $1 ORDER BY r.sort_order ASC, r.id ASC`, groupName)
	} else {
		rows, err = db.DB.Query(ctx, base+` ORDER BY r.group_name ASC, r.sort_order ASC, r.id ASC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]RecommendationEntry, 0)
	for rows.Next() {
		var entry RecommendationEntry
		var n RecommendationNovel
		if err := rows.Scan(
			&entry.ID, &entry.GroupName, &entry.NovelID, &entry.SortOrder, &entry.ScheduledStart, &entry.ScheduledEnd, &entry.CreatedAt,
			&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating, &n.Status, &n.PublishStatus, &n.Synopsis, &n.ViewCount, &n.Genres,
		); err != nil {
			return nil, err
		}
		if n.Genres == nil {
			n.Genres = []string{}
		}
		entry.Novel = &n
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func saveGroupTx(ctx context.Context, tx pgx.Tx, groupName string, items []GroupItemInput) error {
	if len(items) > 0 {
		ids := make([]int64, len(items))
		for i, item := range items {
			ids[i] = item.NovelID
		}
		rows, err := tx.Query(ctx, `SELECT id FROM novels WHERE id = ANY($1::int[]) AND status != 'archived'`, ids)
		if err != nil {
			return err
		}
		found := make(map[int64]bool, len(ids))
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			found[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		for _, id := range ids {
			if !found[id] {
				return fmt.Errorf("%w: novel_id=%d", ErrNovelNotFound, id)
			}
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM recommendations WHERE group_name = $1`, groupName); err != nil {
		return err
	}

	for idx, item := range items {
		_, err := tx.Exec(ctx, `
			INSERT INTO recommendations (group_name, novel_id, sort_order, scheduled_start, scheduled_end)
			VALUES ($1, $2, $3, $4, $5)
		`, groupName, item.NovelID, idx, item.ScheduledStart, item.ScheduledEnd)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *dbRepository) SaveGroup(ctx context.Context, groupName string, items []GroupItemInput) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := saveGroupTx(ctx, tx, groupName, items); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *dbRepository) ListPublishedGenres(ctx context.Context) ([]string, error) {
	rows, err := db.DB.Query(ctx, `
		SELECT DISTINCT btrim(g) AS genre
		FROM novels, unnest(COALESCE(genres, '{}')) AS g
		WHERE status != 'archived' AND publish_status = 'published' AND btrim(g) != ''
		ORDER BY genre ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	genres := make([]string, 0)
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		genres = append(genres, g)
	}
	return genres, rows.Err()
}

func (r *dbRepository) GetNovelsByIDs(ctx context.Context, ids []int64, publishedOnly bool) (map[int64]RecommendationNovel, error) {
	result := make(map[int64]RecommendationNovel, len(ids))
	if len(ids) == 0 {
		return result, nil
	}

	query := `SELECT ` + recommendationNovelSelectCols + ` FROM novels n WHERE n.id = ANY($1::int[]) AND n.status != 'archived'`
	if publishedOnly {
		query += ` AND n.publish_status = 'published'`
	}
	rows, err := db.DB.Query(ctx, query, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var n RecommendationNovel
		if err := scanRecommendationNovel(rows, &n); err != nil {
			return nil, err
		}
		result[n.ID] = n
	}
	return result, rows.Err()
}

func (r *dbRepository) ListTopPublishedNovels(ctx context.Context, sortRule string, excludeIDs []int64, limit int) ([]RecommendationNovel, error) {
	if limit <= 0 {
		return []RecommendationNovel{}, nil
	}
	orderSQL := "ORDER BY n.view_count DESC, n.created_at DESC, n.id DESC"
	switch sortRule {
	case RankingSortRating:
		orderSQL = "ORDER BY n.rating DESC, n.view_count DESC, n.id DESC"
	case RankingSortLatest:
		orderSQL = "ORDER BY n.created_at DESC, n.id DESC"
	}

	if excludeIDs == nil {
		excludeIDs = []int64{}
	}
	query := fmt.Sprintf(`
		SELECT %s
		FROM novels n
		WHERE n.status != 'archived' AND n.publish_status = 'published'
		  AND (cardinality($1::int[]) = 0 OR NOT (n.id = ANY($1::int[])))
		%s
		LIMIT $2
	`, recommendationNovelSelectCols, orderSQL)

	rows, err := db.DB.Query(ctx, query, excludeIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]RecommendationNovel, 0, limit)
	for rows.Next() {
		var n RecommendationNovel
		if err := scanRecommendationNovel(rows, &n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

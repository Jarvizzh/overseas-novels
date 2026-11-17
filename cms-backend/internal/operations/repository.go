package operations

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

type Repository interface {
	GetRankingConfig(ctx context.Context) (RankingConfig, error)
	SaveRankingConfig(ctx context.Context, cfg RankingConfig) error
	ListRecommendations(ctx context.Context) ([]RecommendationEntry, error)
	ListGroup(ctx context.Context, groupName string) ([]RecommendationEntry, error)
	SaveGroup(ctx context.Context, groupName string, items []GroupItemInput) error
}

type dbRepository struct{}

func NewRepository() Repository {
	return &dbRepository{}
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

func (r *dbRepository) SaveRankingConfig(ctx context.Context, cfg RankingConfig) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

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
	return tx.Commit(ctx)
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
			n.id, n.title, n.author, COALESCE(n.cover_url, ''), COALESCE(n.rating, 0),
			COALESCE(n.status, 'Ongoing'), COALESCE(n.publish_status, 'published'),
			COALESCE(n.view_count, 0), COALESCE(n.genres, '{}')
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
			&n.ID, &n.Title, &n.Author, &n.CoverURL, &n.Rating, &n.Status, &n.PublishStatus, &n.ViewCount, &n.Genres,
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

func (r *dbRepository) SaveGroup(ctx context.Context, groupName string, items []GroupItemInput) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

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

	return tx.Commit(ctx)
}

package tracking

import (
	"context"
	"fmt"
	"strconv"
	"star-novel-cms/internal/db"
)

type Repository interface {
	ListLogs(ctx context.Context, page, pageSize int, pixelID, eventName string, statusCode string) ([]*FacebookCAPILog, int, error)
}

type pgRepository struct{}

func NewRepository() Repository {
	return &pgRepository{}
}

func (r *pgRepository) ListLogs(ctx context.Context, page, pageSize int, pixelID, eventName string, statusCode string) ([]*FacebookCAPILog, int, error) {
	if db.DB == nil {
		return nil, 0, fmt.Errorf("database connection is not initialized")
	}

	offset := (page - 1) * pageSize
	whereClause := "WHERE 1=1"
	args := []interface{}{}
	argIdx := 1

	if pixelID != "" {
		whereClause += fmt.Sprintf(" AND pixel_id = $%d", argIdx)
		args = append(args, pixelID)
		argIdx++
	}

	if eventName != "" {
		whereClause += fmt.Sprintf(" AND event_name = $%d", argIdx)
		args = append(args, eventName)
		argIdx++
	}

	if statusCode != "" {
		if code, err := strconv.Atoi(statusCode); err == nil {
			whereClause += fmt.Sprintf(" AND status_code = $%d", argIdx)
			args = append(args, code)
			argIdx++
		}
	}

	// 1. Get total count
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM facebook_capi_logs %s", whereClause)
	var total int
	err := db.DB.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// 2. Query page logs
	query := fmt.Sprintf(`
		SELECT id, pixel_id, event_name, user_id, value, currency, test_event_code, status_code, payload, response, created_at
		FROM facebook_capi_logs
		%s
		ORDER BY id DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)

	args = append(args, pageSize, offset)

	rows, err := db.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	logs := []*FacebookCAPILog{}
	for rows.Next() {
		log := &FacebookCAPILog{}
		err := rows.Scan(
			&log.ID,
			&log.PixelID,
			&log.EventName,
			&log.UserID,
			&log.Value,
			&log.Currency,
			&log.TestEventCode,
			&log.StatusCode,
			&log.Payload,
			&log.Response,
			&log.CreatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		logs = append(logs, log)
	}

	return logs, total, nil
}

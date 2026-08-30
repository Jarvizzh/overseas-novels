package feedback

import (
	"context"
	"fmt"
	"strings"

	"star-novel-cms/internal/db"
)

type Repository interface {
	List(ctx context.Context, page, limit int, status, keyword string) ([]Feedback, int, error)
	GetByID(ctx context.Context, id int64) (*Feedback, error)
	Update(ctx context.Context, id int64, status, reply string) error
}

type repository struct{}

func NewRepository() Repository {
	return &repository{}
}

func (r *repository) List(ctx context.Context, page, limit int, status, keyword string) ([]Feedback, int, error) {
	whereClauses := []string{"1=1"}
	args := []interface{}{}
	argIdx := 1

	if status != "" && status != "all" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, status)
		argIdx++
	}

	if keyword != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("(email ILIKE $%d OR content ILIKE $%d OR subject ILIKE $%d)", argIdx, argIdx, argIdx))
		args = append(args, "%"+keyword+"%")
		argIdx++
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM feedback WHERE %s", whereSQL)
	var total int
	err := db.DB.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	selectQuery := fmt.Sprintf(`
		SELECT id, user_id, email, subject, content, status, admin_reply, created_at, updated_at
		FROM feedback
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereSQL, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := db.DB.Query(ctx, selectQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var list []Feedback
	for rows.Next() {
		var fb Feedback
		if err := rows.Scan(
			&fb.ID,
			&fb.UserID,
			&fb.Email,
			&fb.Subject,
			&fb.Content,
			&fb.Status,
			&fb.AdminReply,
			&fb.CreatedAt,
			&fb.UpdatedAt,
		); err != nil {
			return nil, 0, err
		}
		list = append(list, fb)
	}

	return list, total, nil
}

func (r *repository) GetByID(ctx context.Context, id int64) (*Feedback, error) {
	query := `
		SELECT id, user_id, email, subject, content, status, admin_reply, created_at, updated_at
		FROM feedback
		WHERE id = $1
	`
	var fb Feedback
	err := db.DB.QueryRow(ctx, query, id).Scan(
		&fb.ID,
		&fb.UserID,
		&fb.Email,
		&fb.Subject,
		&fb.Content,
		&fb.Status,
		&fb.AdminReply,
		&fb.CreatedAt,
		&fb.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &fb, nil
}

func (r *repository) Update(ctx context.Context, id int64, status, reply string) error {
	query := `
		UPDATE feedback
		SET status = $1, admin_reply = $2, updated_at = NOW()
		WHERE id = $3
	`
	_, err := db.DB.Exec(ctx, query, status, reply, id)
	return err
}

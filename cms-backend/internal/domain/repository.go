package domain

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"star-novel-cms/internal/db"
)

type Repository interface {
	ListDomains(ctx context.Context) ([]*SystemDomain, error)
	CreateDomain(ctx context.Context, name, domain, domainType string, isDefault bool) (*SystemDomain, error)
	UpdateDomainStatus(ctx context.Context, id int, status int16) error
	SetDefaultDomain(ctx context.Context, id int) error
	DeleteDomain(ctx context.Context, id int) error
}

type pgRepository struct {
	pool *pgxpool.Pool
}

func NewRepository() Repository {
	return &pgRepository{
		pool: db.DB,
	}
}

func (r *pgRepository) ListDomains(ctx context.Context) ([]*SystemDomain, error) {
	query := `
		SELECT id, name, domain, type, status, is_default, created_at
		FROM system_domains
		ORDER BY is_default DESC, type ASC, id DESC
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*SystemDomain
	for rows.Next() {
		var d SystemDomain
		err := rows.Scan(&d.ID, &d.Name, &d.Domain, &d.Type, &d.Status, &d.IsDefault, &d.CreatedAt)
		if err != nil {
			return nil, err
		}
		list = append(list, &d)
	}
	return list, nil
}

func (r *pgRepository) CreateDomain(ctx context.Context, name, domainStr, domainType string, isDefault bool) (*SystemDomain, error) {
	domainStr = strings.TrimSpace(strings.ToLower(domainStr))
	// Strip http:// or https:// if user pasted a full URL
	domainStr = strings.TrimPrefix(domainStr, "https://")
	domainStr = strings.TrimPrefix(domainStr, "http://")
	domainStr = strings.TrimSuffix(domainStr, "/")

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if isDefault {
		_, err = tx.Exec(ctx, "UPDATE system_domains SET is_default = FALSE WHERE is_default = TRUE")
		if err != nil {
			return nil, fmt.Errorf("failed to reset default domains: %w", err)
		}
	}

	var d SystemDomain
	query := `
		INSERT INTO system_domains (name, domain, type, status, is_default)
		VALUES ($1, $2, $3, 1, $4)
		RETURNING id, name, domain, type, status, is_default, created_at
	`
	err = tx.QueryRow(ctx, query, name, domainStr, domainType, isDefault).Scan(
		&d.ID, &d.Name, &d.Domain, &d.Type, &d.Status, &d.IsDefault, &d.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &d, nil
}

func (r *pgRepository) UpdateDomainStatus(ctx context.Context, id int, status int16) error {
	query := `UPDATE system_domains SET status = $1 WHERE id = $2`
	_, err := r.pool.Exec(ctx, query, status, id)
	return err
}

func (r *pgRepository) SetDefaultDomain(ctx context.Context, id int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, "UPDATE system_domains SET is_default = FALSE WHERE is_default = TRUE")
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, "UPDATE system_domains SET is_default = TRUE, status = 1 WHERE id = $1", id)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *pgRepository) DeleteDomain(ctx context.Context, id int) error {
	query := `DELETE FROM system_domains WHERE id = $1 AND is_default = FALSE`
	cmd, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("cannot delete default domain or domain not found")
	}
	return nil
}

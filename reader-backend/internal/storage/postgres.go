package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresContentStorage struct {
	pool *pgxpool.Pool
}

func NewPostgresStorage(pool *pgxpool.Pool) *PostgresContentStorage {
	return &PostgresContentStorage{pool: pool}
}

func (s *PostgresContentStorage) StorageType() string {
	return "postgres"
}

func (s *PostgresContentStorage) PutContent(ctx context.Context, novelID int64, chapterIndex int, content string) error {
	query := `UPDATE chapters SET content = $1 WHERE novel_id = $2 AND chapter_index = $3`
	_, err := s.pool.Exec(ctx, query, content, novelID, chapterIndex)
	return err
}

func (s *PostgresContentStorage) GetContent(ctx context.Context, novelID int64, chapterIndex int) (string, error) {
	query := `SELECT content FROM chapters WHERE novel_id = $1 AND chapter_index = $2`
	var content string
	err := s.pool.QueryRow(ctx, query, novelID, chapterIndex).Scan(&content)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrContentNotFound
		}
		return "", err
	}
	return content, nil
}

func (s *PostgresContentStorage) DeleteContent(ctx context.Context, novelID int64, chapterIndex int) error {
	query := `UPDATE chapters SET content = '' WHERE novel_id = $1 AND chapter_index = $2`
	_, err := s.pool.Exec(ctx, query, novelID, chapterIndex)
	return err
}

func (s *PostgresContentStorage) DeleteNovelContent(ctx context.Context, novelID int64) error {
	return nil
}

func (s *PostgresContentStorage) BatchPutContent(ctx context.Context, novelID int64, items []ChapterContentItem) error {
	if len(items) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	batch := &pgx.Batch{}
	query := `UPDATE chapters SET content = $1 WHERE novel_id = $2 AND chapter_index = $3`
	for _, item := range items {
		batch.Queue(query, item.Content, novelID, item.ChapterIndex)
	}

	br := tx.SendBatch(ctx, batch)
	for range items {
		if _, err := br.Exec(); err != nil {
			br.Close()
			return err
		}
	}
	if err := br.Close(); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

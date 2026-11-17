package auth

import (
	"context"
	"time"

	"reader-backend/internal/db"
)

type sessionRepository struct{}

func (sessionRepository) save(ctx context.Context, hash string, userID int64) error {
	_, err := db.DB.Exec(ctx, `WITH expired AS (
 DELETE FROM reader_sessions WHERE token_hash IN (
  SELECT token_hash FROM reader_sessions WHERE expires_at<=now()
  ORDER BY expires_at LIMIT 100 FOR UPDATE SKIP LOCKED
 )
) INSERT INTO reader_sessions(token_hash,user_id,expires_at)
VALUES($1,$2,now()+interval '180 days')`, hash, userID)
	return err
}
func (sessionRepository) renew(ctx context.Context, hash string) (int64, error) {
	var id int64
	err := db.DB.QueryRow(ctx, `UPDATE reader_sessions s SET expires_at=now()+interval '180 days'
FROM users u WHERE s.token_hash=$1 AND s.user_id=u.id
AND u.status=1 AND s.expires_at>now() RETURNING s.user_id`, hash).Scan(&id)
	return id, err
}
func (sessionRepository) revoke(ctx context.Context, hash string) error {
	_, err := db.DB.Exec(ctx, `DELETE FROM reader_sessions WHERE token_hash=$1`, hash)
	return err
}

func loadTokenCutover(ctx context.Context) (time.Time, error) {
	var cutover time.Time
	err := db.DB.QueryRow(ctx, "SELECT applied_at FROM reader_schema_migrations WHERE version=$1", db.RequiredSchemaVersion).Scan(&cutover)
	return cutover, err
}

func (sessionRepository) valid(ctx context.Context, id int64, hash string) (bool, error) {
	var valid bool
	err := db.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u WHERE u.id=$1 AND u.status=1 AND ($2::text='' OR EXISTS(SELECT 1 FROM reader_sessions s WHERE s.user_id=u.id AND s.token_hash=$2 AND s.expires_at>now())))`, id, hash).Scan(&valid)
	return valid, err
}

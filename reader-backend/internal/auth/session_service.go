package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5"
)

type sessionService struct{ repo sessionRepository }

func sessionHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func (s sessionService) create(ctx context.Context, userID int64) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	raw := hex.EncodeToString(b)
	return raw, s.repo.save(ctx, sessionHash(raw), userID)
}
func (s sessionService) refresh(ctx context.Context, raw string) (int64, error) {
	id, err := s.repo.renew(ctx, sessionHash(raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInvalidAuth
	}
	return id, err
}

func (s sessionService) logout(ctx context.Context, raw string) error {
	return s.repo.revoke(ctx, sessionHash(raw))
}

func (sessionService) validate(ctx context.Context, id int64, hash string) (bool, error) {
	return (sessionRepository{}).valid(ctx, id, hash)
}

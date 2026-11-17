package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"reader-backend/internal/config"
)

const tokenIssuer = "star-novel-reader"
const tokenAudience = "star-novel-reader-api"

var legacyIssuedBefore atomic.Int64

// InitTokenPolicy loads the persisted cutover, so restarting cannot extend the
// compatibility window for pre-issuer tokens. New installs have no old tokens.
func InitTokenPolicy(ctx context.Context) error {
	cutover, err := loadTokenCutover(ctx)
	if err != nil {
		return err
	}
	legacyIssuedBefore.Store(cutover.Unix())
	return nil
}

type JWTClaims struct {
	UserID      int64  `json:"user_id"`
	SessionHash string `json:"session_hash,omitempty"`
	jwt.RegisteredClaims
}

func GenerateToken(userID int64) (string, error) {
	return generateToken(userID, "")
}

func generateToken(userID int64, hash string) (string, error) {
	if userID <= 0 {
		return "", errors.New("invalid user ID")
	}
	claims := JWTClaims{SessionHash: hash,
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(72 * time.Hour)), // 3 days expiry
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.AppConfig.JWTSecret))
}

func ParseToken(tokenStr string) (int64, error) {
	claims, err := parseClaims(tokenStr)
	if err != nil {
		return 0, err
	}
	return claims.UserID, nil
}

func parseClaims(tokenStr string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(config.AppConfig.JWTSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid && claims.UserID > 0 {
		if claims.Issuer == tokenIssuer && len(claims.Audience) == 1 && claims.Audience[0] == tokenAudience {
			return claims, nil
		}
		cutover := legacyIssuedBefore.Load()
		if cutover > 0 && claims.Issuer == "" && len(claims.Audience) == 0 && claims.IssuedAt != nil &&
			claims.IssuedAt.Unix() <= cutover && claims.ExpiresAt.Unix() <= cutover+int64((72*time.Hour)/time.Second) &&
			time.Now().Unix() < cutover+int64((72*time.Hour)/time.Second) {
			return claims, nil
		}
	}

	return nil, errors.New("invalid token identity or audience")
}

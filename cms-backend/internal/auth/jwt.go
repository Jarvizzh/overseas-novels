package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"star-novel-cms/internal/config"
)

const adminTokenIssuer = "star-novel-cms"
const adminTokenAudience = "star-novel-admin"

type AdminClaims struct {
	AdminID string `json:"admin_id"`
	Role    string `json:"role"`
	jwt.RegisteredClaims
}

func GenerateToken(adminID, role string) (string, error) {
	claims := AdminClaims{
		AdminID: adminID,
		Role:    role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    adminTokenIssuer,
			Audience:  jwt.ClaimStrings{adminTokenAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)), // 24 hours expiry for security
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.AppConfig.JWTSecret))
}

func ParseToken(tokenStr string) (*AdminClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &AdminClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(config.AppConfig.JWTSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(adminTokenIssuer), jwt.WithAudience(adminTokenAudience), jwt.WithExpirationRequired())

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*AdminClaims); ok && token.Valid && strings.TrimSpace(claims.AdminID) != "" && strings.TrimSpace(claims.Role) != "" {
		return claims, nil
	}

	return nil, errors.New("invalid admin token")
}

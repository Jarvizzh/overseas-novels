package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/httpx"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header format must be Bearer {token}"})
			c.Abort()
			return
		}

		claims, err := parseClaims(parts[1])
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		active, err := (sessionService{}).validate(c.Request.Context(), claims.UserID, claims.SessionHash)
		if err != nil {
			httpx.Fail(c, 503, "account_status_unavailable", "Account status unavailable", err)
			return
		}
		if !active {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Account unavailable"})
			return
		}

		// Store user ID in context
		c.Set("user_id", claims.UserID)
		c.Next()
	}
}

// OptionalClaims verifies a bearer token before reading the account and its
// session in the same database snapshot as the chapter.
func OptionalClaims(header string) (*JWTClaims, error) {
	if header == "" {
		return nil, nil
	}
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || scheme != "Bearer" || token == "" {
		return nil, errors.New("invalid bearer credentials")
	}
	claims, err := parseClaims(token)
	if err != nil {
		return nil, err
	}
	return claims, nil
}

func OptionalAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			c.Next()
			return
		}

		claims, err := parseClaims(parts[1])
		if err == nil {
			active, err := (sessionService{}).validate(c.Request.Context(), claims.UserID, claims.SessionHash)
			if err != nil {
				httpx.Fail(c, 503, "account_status_unavailable", "Account status unavailable", err)
				return
			}
			if active {
				c.Set("user_id", claims.UserID)
			}
		}
		c.Next()
	}
}

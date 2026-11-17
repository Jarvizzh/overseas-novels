package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"reader-backend/internal/config"
)

func TestTokenIdentityAndFiniteLegacyWindow(t *testing.T) {
	previousConfig := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "jwt-policy-test"}
	defer func() { config.AppConfig = previousConfig }()
	old := legacyIssuedBefore.Load()
	defer legacyIssuedBefore.Store(old)
	now := time.Now().Truncate(time.Second)
	legacyIssuedBefore.Store(now.Unix())
	base := func() JWTClaims {
		return JWTClaims{UserID: 1, RegisteredClaims: jwt.RegisteredClaims{Issuer: tokenIssuer, Audience: jwt.ClaimStrings{tokenAudience}, IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}}
	}
	for _, tc := range []struct {
		name   string
		change func(*JWTClaims)
		method jwt.SigningMethod
		valid  bool
	}{
		{"current", func(*JWTClaims) {}, jwt.SigningMethodHS256, true},
		{"wrong algorithm", func(*JWTClaims) {}, jwt.SigningMethodHS384, false},
		{"missing expiration", func(c *JWTClaims) { c.ExpiresAt = nil }, jwt.SigningMethodHS256, false},
		{"nonpositive user", func(c *JWTClaims) { c.UserID = 0 }, jwt.SigningMethodHS256, false},
		{"other issuer", func(c *JWTClaims) { c.Issuer = "cms" }, jwt.SigningMethodHS256, false},
		{"other audience", func(c *JWTClaims) { c.Audience = jwt.ClaimStrings{"cms-api"} }, jwt.SigningMethodHS256, false},
		{"partial identity", func(c *JWTClaims) { c.Issuer = "" }, jwt.SigningMethodHS256, false},
		{"legacy before cutover", func(c *JWTClaims) { c.Issuer = ""; c.Audience = nil }, jwt.SigningMethodHS256, true},
		{"legacy missing issued time", func(c *JWTClaims) { c.Issuer = ""; c.Audience = nil; c.IssuedAt = nil }, jwt.SigningMethodHS256, false},
		{"legacy lifetime exceeds cutoff", func(c *JWTClaims) {
			c.Issuer = ""
			c.Audience = nil
			c.ExpiresAt = jwt.NewNumericDate(now.Add(73 * time.Hour))
		}, jwt.SigningMethodHS256, false},
		{"future issued time", func(c *JWTClaims) { c.IssuedAt = jwt.NewNumericDate(now.Add(time.Hour)) }, jwt.SigningMethodHS256, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.change(&c)
			signed, err := jwt.NewWithClaims(tc.method, c).SignedString([]byte(config.AppConfig.JWTSecret))
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseToken(signed)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
	legacyIssuedBefore.Store(now.Add(-73 * time.Hour).Unix())
	c := base()
	c.Issuer = ""
	c.Audience = nil
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(config.AppConfig.JWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseToken(signed); err == nil {
		t.Fatal("expired legacy compatibility window accepted")
	}
}

package novel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/auth"
	"reader-backend/internal/config"
	"reader-backend/internal/db"
	"reader-backend/internal/testdb"
)

func TestReadingRoutesRefreshExpiredCredentialsAndPreserveAccountIsolation(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	previous := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "reading-auth-test"}
	t.Cleanup(func() { config.AppConfig = previous })
	gin.SetMode(gin.TestMode)
	raw := strings.Repeat("a", 64)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,nickname,avatar_url) VALUES(1,'Reader',''),(2,'Other','');
		INSERT INTO novels(id,title,author,coin_cost_per_thousand,start_pay_chapter_index)
		    VALUES(1,'Book','Author',50,1);
		INSERT INTO chapters(id,novel_id,chapter_index,title,content,word_count,content_preview)
		    VALUES('chapter',1,0,'Chapter','Full paid body',1000,'Full...');
		INSERT INTO unlock_records(user_id,novel_id,chapter_index,chapter_id,identity_evidence,identity_resolved_at)
		    VALUES(1,1,0,'chapter','fixture',now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO reader_sessions(user_id,token_hash,expires_at) VALUES(1,$1,now()+interval '1 day')", hash); err != nil {
		t.Fatal(err)
	}
	counter := &queryCount{}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = counter
	tracked, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	db.DB = tracked
	t.Cleanup(func() { db.DB = pool; tracked.Close() })
	router := gin.New()
	api := router.Group("/api/v1")
	NewHandler(NewService(NewDBRepository(), &snapshotCache{bodies: make(map[string]string)}, nil, nil)).RegisterRoutes(api)
	auth.NewAuthHandler(auth.NewAuthService(auth.NewUserRepository())).RegisterRoutes(api)
	token := func(id int64, lifetime time.Duration) string {
		t.Helper()
		claims := auth.JWTClaims{UserID: id, SessionHash: hash, RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "star-novel-reader", Audience: jwt.ClaimStrings{"star-novel-reader-api"},
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(lifetime)),
		}}
		value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(config.AppConfig.JWTSecret))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	request := func(method, path, bearer, cookie string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "reader-session", Value: cookie})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s returned %d want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	expired := token(1, -time.Minute)
	counter.count.Store(0)
	for _, path := range []string{"/novels/1/chapters", "/novels/1/chapters/0?user_id=1"} {
		request("GET", "/api/v1"+path, expired, "", 401)
	}
	if counter.count.Load() != 0 {
		t.Fatal("expired JWT queried the database")
	}
	w := request("POST", "/api/v1/auth/refresh", "", raw, 200)
	var renewed auth.AuthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &renewed); err != nil || renewed.User.ID != 1 {
		t.Fatalf("refresh result: %+v %v", renewed, err)
	}
	path := "/api/v1/novels/1/chapters/0?user_id=1"
	request("GET", path, renewed.Token, "", 200)
	counter.count.Store(0)
	request("GET", path, renewed.Token, "", 200)
	if counter.count.Load() != 1 {
		t.Fatalf("valid hot read SQL count %d", counter.count.Load())
	}
	request("GET", "/api/v1/novels/1/chapters/0?user_id=2", renewed.Token, "", 409)
	anon := request("GET", "/api/v1/novels/1/chapters/0", "", "", 200)
	var preview ChapterReadResult
	if err := json.Unmarshal(anon.Body.Bytes(), &preview); err != nil || !preview.Locked || preview.Chapter.Content != "Full..." {
		t.Fatalf("anonymous preview: %+v %v", preview, err)
	}
	request("GET", path, "invalid", "", 401)
	request("POST", "/api/v1/auth/refresh", "", strings.Repeat("b", 64), 401)
	if _, err := pool.Exec(ctx, "DELETE FROM reader_sessions WHERE token_hash=$1", hash); err != nil {
		t.Fatal(err)
	}
	request("GET", path, renewed.Token, "", 401)
	request("POST", "/api/v1/auth/refresh", "", raw, 401)
	if _, err := pool.Exec(ctx, "UPDATE users SET status=2 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	request("GET", path, renewed.Token, "", 403)
}

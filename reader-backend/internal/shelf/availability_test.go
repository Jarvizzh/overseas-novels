package shelf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/testdb"
)

func TestProgressWritesAreMonotonic(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO users(id) VALUES(1);INSERT INTO novels(id,title,author) VALUES(1,'Book','Author')`); err != nil {
		t.Fatal(err)
	}
	r := NewDBRepository()
	now := time.Now().UTC()
	if err := r.SaveProgress(ctx, 1, 1, 2, 0.5, now); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveProgress(ctx, 1, 1, 1, 0.1, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetProgress(ctx, 1, 1)
	if err != nil || got.ChapterIndex != 2 {
		t.Fatalf("progress regressed: %+v %v", got, err)
	}
	if err := NewService(r, nil).SyncProgress(ctx, 1, []ProgressUpdate{{NovelID: 1, UpdatedAt: now.AddDate(1, 0, 0)}}); err == nil {
		t.Fatal("future progress accepted")
	}
}

func TestProgressBatchDeduplicatesAndCommitsAtomically(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO users(id) VALUES(1); INSERT INTO novels(id,title,author) VALUES(1,'A','A'),(2,'B','B'); INSERT INTO bookshelves(user_id,novel_id,in_shelf,updated_at) VALUES(1,1,true,now()-interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	r := NewDBRepository()
	svc := NewService(r, nil)
	now := time.Now().UTC()
	updates := []ProgressUpdate{
		{NovelID: 1, ChapterIndex: 5, ScrollOffsetPercentage: .75, UpdatedAt: now},
		{NovelID: 1, ChapterIndex: 3, UpdatedAt: now.Add(-time.Minute)},
		{NovelID: 2, ChapterIndex: 8, UpdatedAt: now},
	}
	if err := svc.SyncProgress(ctx, 1, updates); err != nil {
		t.Fatal(err)
	}
	first, err := r.GetProgress(ctx, 1, 1)
	if err != nil || first == nil || first.ChapterIndex != 5 || first.ScrollOffsetPercentage != .75 || !first.InShelf {
		t.Fatalf("progress/collection changed incorrectly: %+v %v", first, err)
	}
	second, err := r.GetProgress(ctx, 1, 2)
	if err != nil || second == nil || second.ChapterIndex != 8 || second.InShelf {
		t.Fatalf("new progress: %+v %v", second, err)
	}
	if err := svc.SyncProgress(ctx, 1, []ProgressUpdate{
		{NovelID: 1, ChapterIndex: 10, UpdatedAt: now.Add(time.Minute)},
		{NovelID: 99, ChapterIndex: 10, UpdatedAt: now},
	}); err == nil {
		t.Fatal("expected foreign key error")
	}
	first, err = r.GetProgress(ctx, 1, 1)
	if err != nil || first == nil || first.ChapterIndex != 5 {
		t.Fatalf("partially committed failing batch: %+v %v", first, err)
	}
}

func TestProgressEndpointDoesNotRequireFavoriteOrAcceptAnotherAccount(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO users(id) VALUES(1),(2); INSERT INTO novels(id,title,author,cover_url,synopsis)
		VALUES(1,'Book','Author','','')`); err != nil {
		t.Fatal(err)
	}
	repo := NewDBRepository()
	service := NewService(repo, nil)
	handler := NewHandler(service)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", int64(1)) })
	router.POST("/sync", handler.SyncProgress)
	router.GET("/progress/:novel_id", handler.GetProgress)

	now := time.Now().Truncate(time.Millisecond)
	request := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	post := func(owner, chapter int, version string, want int) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"user_id": owner, "progress_updates": []ProgressUpdate{{
			NovelID: 1, ChapterIndex: chapter, ScrollOffsetPercentage: .6, UpdatedAt: now, Version: version,
		}}})
		if err != nil {
			t.Fatal(err)
		}
		request("POST", "/sync", string(body), want)
	}
	post(2, 9, "9", http.StatusConflict)
	post(1, 8, "2", http.StatusOK)
	post(1, 2, "1", http.StatusOK)
	if items, err := service.GetShelf(ctx, 1); err != nil || len(items) != 0 {
		t.Fatalf("reading created a favorite: %v %v", items, err)
	}
	request("GET", "/progress/1?user_id=2", "", http.StatusConflict)
	check := func() {
		t.Helper()
		response := request("GET", "/progress/1?user_id=1", "", http.StatusOK)
		var saved ProgressUpdate
		if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil ||
			saved.ChapterIndex != 8 || saved.ScrollOffsetPercentage != .6 || saved.Version != "2" {
			t.Fatalf("wrong restored progress: %+v %v", saved, err)
		}
	}
	check()
	if err := service.AddToShelf(ctx, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveFromShelf(ctx, 1, []int64{1}); err != nil {
		t.Fatal(err)
	}
	check()
}

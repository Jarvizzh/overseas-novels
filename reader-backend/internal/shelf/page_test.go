package shelf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/testdb"
)

func TestShelfCursorUsesActivityAndKeepsMembershipIndependent(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(1),(2);
		INSERT INTO novels(id,title,author,cover_url,synopsis)
		SELECT id, 'Book '||id, 'Author', '', '' FROM generate_series(1,7) id;
		UPDATE novels SET publish_status='offline' WHERE id=6;
		INSERT INTO bookshelves(user_id,novel_id,in_shelf,updated_at,shelf_added_at) VALUES
		(1,1,true,'2026-01-03','2026-01-01'),
		(1,2,true,'2026-01-01','2026-01-03'),
		(1,3,true,'2026-01-02',NULL),
		(1,4,true,NULL,NULL),
		(1,5,false,'2026-01-05',NULL),
		(1,6,true,'2026-01-05',NULL),
		(2,3,true,'2026-01-10',NULL)`)
	handler := NewHandler(NewService(NewDBRepository(), nil))
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", int64(1)) })
	router.GET("/cursor", handler.GetShelfPage)
	router.GET("/ids", handler.GetShelfIDs)
	request := func(path string, expected int, target any) {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != expected {
			t.Fatalf("%s: %d %s, want %d", path, response.Code, response.Body, expected)
		}
		if response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("shelf response cache policy missing")
		}
		if target != nil {
			if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
				t.Fatal(err)
			}
		}
	}
	var first ShelfPage
	request("/cursor?user_id=1&limit=2", 200, &first)
	if len(first.Items) != 2 || first.Items[0].NovelID != 2 || first.Items[1].NovelID != 1 || first.NextCursor == "" {
		t.Fatalf("first page: %+v", first)
	}
	var ids []int64
	request("/ids?user_id=1", 200, &ids)
	slices.Sort(ids)
	if !slices.Equal(ids, []int64{1, 2, 3, 4}) {
		t.Fatalf("membership must include both pages: %v", ids)
	}
	exec(`INSERT INTO bookshelves(user_id,novel_id) VALUES(1,7)`)
	var next ShelfPage
	request("/cursor?user_id=1&limit=2&cursor="+first.NextCursor, 200, &next)
	if len(next.Items) != 2 || next.Items[0].NovelID != 3 || next.Items[1].NovelID != 4 || next.NextCursor != "" {
		t.Fatalf("next page changed after insertion ahead of the cursor: %+v", next)
	}
	request("/cursor?user_id=1&limit=0", 400, nil)
	request("/cursor?user_id=1&cursor=!", 400, nil)
	request("/cursor?user_id=2", 409, nil)
	request("/ids?user_id=2", 409, nil)
}

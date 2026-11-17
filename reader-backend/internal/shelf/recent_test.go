package shelf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/model"
	"reader-backend/internal/testdb"
)

func TestGetRecentReturnsReadBooksIncludingNonShelf(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO users(id) VALUES(1);
		INSERT INTO novels(id, title, author, cover_url, synopsis, publish_status) VALUES
		(1, 'Recent Non-Shelf', 'A', NULL, NULL, 'published'),
		(2, 'Older On-Shelf', 'B', '', '', 'published'),
		(3, 'Saved But Unread', 'C', '', '', 'published'),
		(4, 'Offline Hidden', 'D', '', '', 'offline');
		INSERT INTO bookshelves(user_id, novel_id, chapter_index, scroll_offset_percentage, in_shelf, updated_at, shelf_added_at) VALUES
		(1, 1, 4, 0.5, false, '2026-01-03T10:00:00Z', 'epoch'),
		(1, 2, 1, 0.2, true,  '2026-01-02T10:00:00Z', '2026-01-02T10:00:00Z'),
		(1, 3, 0, 0.0, true,  'epoch',                '2026-01-04T10:00:00Z'),
		(1, 4, 2, 0.4, false, '2026-01-05T10:00:00Z', 'epoch');
	`)
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	handler := NewHandler(NewService(NewDBRepository(), nil))
	r := gin.New()
	r.GET("/shelf/recent", func(c *gin.Context) {
		c.Set("user_id", int64(1))
		handler.GetRecent(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/shelf/recent?user_id=1&limit=10", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var items []model.BookshelfWithNovel
	if err := json.Unmarshal(res.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].NovelID != 1 || items[1].NovelID != 2 {
		t.Fatalf("expected recently read [1, 2], got %+v", items)
	}
	if items[0].ChapterIndex != 4 || items[0].InShelf {
		t.Fatalf("expected non-shelf chapter 4 on item 0, got %+v", items[0])
	}
}

package httpx

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestBodyAndDeadlineContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		path         string
		size, status int
		budget       time.Duration
	}{
		{"/api/v1/shelf/sync", MaxBodyBytes, 200, 10 * time.Second},
		{"/api/v1/shelf/sync", MaxBodyBytes + 1, 413, 0},
		{"/api/v1/wallet/webhook/paypal", MaxWebhookBytes, 200, 30 * time.Second},
		{"/api/v1/wallet/webhook/paypal", MaxWebhookBytes + 1, 413, 0},
		{"/api/v1/wallet/recharge/paypal/capture", 1, 200, 60 * time.Second},
		{"/healthz", 0, 200, 2 * time.Second},
	} {
		t.Run(tc.path+"/"+strings.Repeat("x", tc.status/100), func(t *testing.T) {
			r := gin.New()
			r.Use(Policy())
			r.POST(tc.path, func(c *gin.Context) {
				deadline, ok := c.Request.Context().Deadline()
				if !ok || time.Until(deadline) > tc.budget || time.Until(deadline) < tc.budget-time.Second {
					t.Error("wrong request budget")
				}
				body, err := io.ReadAll(c.Request.Body)
				if err != nil || len(body) != tc.size || string(body) != strings.Repeat("a", tc.size) {
					t.Error("body changed")
				}
				c.Status(200)
			})
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(strings.Repeat("a", tc.size)))
			req.ContentLength = -1 // Chunked/unknown length must have the same bound.
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPaginationRejectsOverflowAndExcessWork(t *testing.T) {
	for _, query := range []string{"page=9223372036854775807", "page=99999999999999999999999", "limit=101", "limit=0", "limit=-1", "page=0", "page=100002&limit=1", "page=1002&limit=100"} {
		r := gin.New()
		r.GET("/", func(c *gin.Context) {
			if _, _, ok := Pagination(c); ok {
				t.Errorf("accepted %s", query)
			}
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/?"+query, nil))
		if w.Code != 400 {
			t.Fatalf("%s: %d", query, w.Code)
		}
	}
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		limit, offset, ok := Pagination(c)
		if !ok || limit != 100 || offset != 100000 {
			t.Error("valid boundary rejected")
		}
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/?page=1001&limit=100", nil))
}

func TestInternalErrorDoesNotExposeCause(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) { Fail(c, 500, "internal_error", "Request failed", io.ErrUnexpectedEOF) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(w.Body.String(), io.ErrUnexpectedEOF.Error()) || !strings.Contains(w.Body.String(), "internal_error") {
		t.Fatal(w.Body.String())
	}
}

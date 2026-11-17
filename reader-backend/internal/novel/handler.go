package novel

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/auth"
	"reader-backend/internal/httpx"
)

type Handler struct {
	service Service
}

func NewHandler(service Service) *Handler {
	return &Handler{
		service: service,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	novelGroup := r.Group("/novels")
	readingLimit := auth.IPRateLimitMiddleware(40, 80, "Too many reading requests")
	{
		novelGroup.GET("", h.GetNovels)
		novelGroup.GET("/search", h.SearchNovels)
		novelGroup.GET("/:id", readingLimit, h.GetNovelDetail)
		novelGroup.GET("/:id/chapters", readingLimit, h.GetChaptersList)
		novelGroup.GET("/:id/chapters/:index", readingLimit, h.GetChapterContent)
	}
}

func (h *Handler) GetNovels(c *gin.Context) {
	genre := c.Query("genre")
	sortBy := c.DefaultQuery("sort", "popular")
	limit, offset, ok := httpx.Pagination(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	novels, err := h.service.GetNovels(ctx, genre, sortBy, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch novels"})
		return
	}

	c.JSON(http.StatusOK, novels)
}

func (h *Handler) SearchNovels(c *gin.Context) {
	search := strings.TrimSpace(c.Query("q"))
	if search == "" || !utf8.ValidString(search) || utf8.RuneCountInString(search) > 128 {
		httpx.Fail(c, http.StatusBadRequest, "invalid_search", "Search must contain 1–128 characters", nil)
		return
	}

	limit, offset, ok := httpx.Pagination(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	novels, err := h.service.SearchNovels(ctx, search, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to search novels"})
		return
	}

	c.JSON(http.StatusOK, novels)
}

func (h *Handler) GetNovelDetail(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 || id > math.MaxInt32 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	ctx := c.Request.Context()

	novel, err := h.service.GetNovelDetail(ctx, id)
	if err != nil {
		if errors.Is(err, ErrContentChanged) || errors.Is(err, ErrContentBusy) {
			httpx.Fail(c, http.StatusServiceUnavailable, "content_unavailable", err.Error(), nil)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch novel details"})
		return
	}

	if novel == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Novel not found"})
		return
	}

	c.JSON(http.StatusOK, novel)
}

func (h *Handler) GetChaptersList(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 || id > math.MaxInt32 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}

	ctx, userID, promoID, ok := readingRequest(c)
	if !ok {
		return
	}

	chapters, err := h.service.GetChaptersList(ctx, userID, id, promoID, "", "")
	if err != nil {
		if errors.Is(err, errReadSessionExpired) {
			httpx.Fail(c, http.StatusUnauthorized, "session_expired", "Session expired. Sign in again.", nil)
			return
		}
		if errors.Is(err, errReadAccountUnavailable) {
			httpx.Fail(c, http.StatusForbidden, "account_unavailable", "Account unavailable", nil)
			return
		}
		if errors.Is(err, errReadAccountChanged) {
			httpx.Fail(c, http.StatusConflict, "account_changed", "The signed-in account changed. Reload before reading.", nil)
			return
		}
		if errors.Is(err, ErrContentChanged) || errors.Is(err, ErrContentBusy) {
			httpx.Fail(c, http.StatusServiceUnavailable, "content_unavailable", err.Error(), nil)
			return
		}
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Novel not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch chapters list"})
		return
	}

	c.JSON(http.StatusOK, chapters)
}

func (h *Handler) GetChapterContent(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 || id > math.MaxInt32 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	indexStr := c.Param("index")

	chapterIndex, err := strconv.Atoi(indexStr)
	if err != nil || chapterIndex < 0 || chapterIndex > math.MaxInt32 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter index"})
		return
	}

	ctx, userID, promoID, ok := readingRequest(c)
	if !ok {
		return
	}

	result, err := h.service.GetChapterContent(ctx, userID, id, chapterIndex, promoID, "", "")
	if err != nil {
		if errors.Is(err, errReadSessionExpired) {
			httpx.Fail(c, http.StatusUnauthorized, "session_expired", "Session expired. Sign in again.", nil)
			return
		}
		if errors.Is(err, errReadAccountUnavailable) {
			httpx.Fail(c, http.StatusForbidden, "account_unavailable", "Account unavailable", nil)
			return
		}
		if errors.Is(err, errReadAccountChanged) {
			httpx.Fail(c, http.StatusConflict, "account_changed", "The signed-in account changed. Reload before reading.", nil)
			return
		}
		if errors.Is(err, ErrContentChanged) || errors.Is(err, ErrContentBusy) {
			httpx.Fail(c, http.StatusServiceUnavailable, "content_unavailable", err.Error(), nil)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch chapter content"})
		return
	}

	if result == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chapter not found"})
		return
	}

	c.JSON(http.StatusOK, result)
}

func readingRequest(c *gin.Context) (context.Context, int64, int, bool) {
	var userID int64
	var identity readIdentity
	claims, err := auth.OptionalClaims(c.GetHeader("Authorization"))
	if err != nil {
		httpx.Fail(c, http.StatusUnauthorized, "invalid_credentials", "Invalid or expired credentials", nil)
		return nil, 0, 0, false
	}
	if claims != nil {
		userID, identity.sessionHash = claims.UserID, claims.SessionHash
	}
	if expected, present := c.GetQuery("user_id"); present {
		identity.expected = &expected
	}
	c.Header("Cache-Control", "private, no-store")

	promoIDStr := c.GetHeader("X-Promo-ID")
	if promoIDStr == "" {
		promoIDStr = c.Query("link_id")
		if promoIDStr == "" {
			promoIDStr = c.Query("promo_id")
		}
	}
	promoID, _ := strconv.Atoi(promoIDStr)

	ctx := context.WithValue(c.Request.Context(), readIdentityKey{}, identity)
	return ctx, userID, promoID, true
}

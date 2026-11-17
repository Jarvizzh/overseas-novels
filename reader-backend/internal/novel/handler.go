package novel

import (
	"errors"
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
	{
		novelGroup.GET("", h.GetNovels)
		novelGroup.GET("/search", h.SearchNovels)
		novelGroup.GET("/:id", h.GetNovelDetail)
		novelGroup.GET("/:id/chapters", auth.OptionalAuthMiddleware(), h.GetChaptersList)
		novelGroup.GET("/:id/chapters/:index", auth.OptionalAuthMiddleware(), h.GetChapterContent)
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
	if err != nil {
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
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}

	var userID int64
	if val, exists := c.Get("user_id"); exists {
		userID = val.(int64)
	}

	promoIDStr := c.GetHeader("X-Promo-ID")
	if promoIDStr == "" {
		promoIDStr = c.Query("link_id")
		if promoIDStr == "" {
			promoIDStr = c.Query("promo_id")
		}
	}
	promoID, _ := strconv.Atoi(promoIDStr)

	utmSource := c.GetHeader("X-UTM-Source")
	if utmSource == "" {
		utmSource = c.Query("utm_source")
	}
	utmCampaign := c.GetHeader("X-UTM-Campaign")
	if utmCampaign == "" {
		utmCampaign = c.Query("utm_campaign")
	}

	ctx := c.Request.Context()

	chapters, err := h.service.GetChaptersList(ctx, userID, id, promoID, utmSource, utmCampaign)
	if err != nil {
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
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	indexStr := c.Param("index")

	chapterIndex, err := strconv.Atoi(indexStr)
	if err != nil || chapterIndex < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter index"})
		return
	}

	var userID int64
	if val, exists := c.Get("user_id"); exists {
		userID = val.(int64)
	}

	if expected, present := c.GetQuery("user_id"); present && expected != strconv.FormatInt(userID, 10) {
		httpx.Fail(c, http.StatusConflict, "account_changed", "The signed-in account changed. Reload before reading.", nil)
		return
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

	utmSource := c.GetHeader("X-UTM-Source")
	if utmSource == "" {
		utmSource = c.Query("utm_source")
	}
	utmCampaign := c.GetHeader("X-UTM-Campaign")
	if utmCampaign == "" {
		utmCampaign = c.Query("utm_campaign")
	}

	ctx := c.Request.Context()
	result, err := h.service.GetChapterContent(ctx, userID, id, chapterIndex, promoID, utmSource, utmCampaign)
	if err != nil {
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

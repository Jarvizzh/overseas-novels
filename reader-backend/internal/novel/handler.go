package novel

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/auth"
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
		novelGroup.GET("/:id/chapters", h.GetChaptersList)
		novelGroup.GET("/:id/chapters/:index", auth.OptionalAuthMiddleware(), h.GetChapterContent)
	}
}

func (h *Handler) GetNovels(c *gin.Context) {
	genre := c.Query("genre")
	pageStr := c.DefaultQuery("page", "1")
	limitStr := c.DefaultQuery("limit", "10")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 {
		limit = 10
	}

	offset := (page - 1) * limit
	ctx := c.Request.Context()

	novels, err := h.service.GetNovels(ctx, genre, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch novels"})
		return
	}

	c.JSON(http.StatusOK, novels)
}

func (h *Handler) SearchNovels(c *gin.Context) {
	search := c.Query("q")
	if search == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Query parameter 'q' is required"})
		return
	}

	pageStr := c.DefaultQuery("page", "1")
	limitStr := c.DefaultQuery("limit", "10")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 {
		limit = 10
	}

	offset := (page - 1) * limit
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
	ctx := c.Request.Context()

	chapters, err := h.service.GetChaptersList(ctx, id)
	if err != nil {
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

	ctx := c.Request.Context()
	result, err := h.service.GetChapterContent(ctx, userID, id, chapterIndex)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch chapter content"})
		return
	}

	if result == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chapter not found"})
		return
	}

	c.JSON(http.StatusOK, result)
}

package operations

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/auth"
)

type Handler struct {
	service Service
}

func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	opsRead := auth.RequirePermission(auth.PermContentOperationsRead)
	opsWrite := auth.RequirePermission(auth.PermContentOperationsWrite)
	ops := r.Group("/operations", auth.AuthMiddleware())
	{
		ops.GET("", opsRead, h.GetConfig)
		ops.POST("/preview", opsRead, h.Preview)
		ops.GET("/novel-options", opsRead, h.ListNovelOptions)
		ops.PUT("/ranking-config", opsWrite, h.SaveRankingConfig)
		ops.PUT("/ranking", opsWrite, h.SaveRanking)
		ops.PUT("/campaigns/checkin", opsWrite, h.SaveCheckInConfig)
		ops.PUT("/groups/:group", opsWrite, h.SaveGroup)
	}
}

func handleServiceError(c *gin.Context, err error, fallbackMsg string) {
	if errors.Is(err, ErrInvalidGroup) ||
		errors.Is(err, ErrInvalidNovelID) ||
		errors.Is(err, ErrDuplicateNovelInGroup) ||
		errors.Is(err, ErrInvalidScheduleRange) ||
		errors.Is(err, ErrInvalidRankingSort) ||
		errors.Is(err, ErrInvalidRankingLimit) ||
		errors.Is(err, ErrEmptyRankingUpdate) ||
		errors.Is(err, ErrInvalidCheckInRewards) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if errors.Is(err, ErrNovelNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": fallbackMsg})
}

func (h *Handler) GetConfig(c *gin.Context) {
	cfg, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load operations configuration"})
		return
	}
	c.JSON(http.StatusOK, cfg)
}

func (h *Handler) SaveRankingConfig(c *gin.Context) {
	var req SaveRankingConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg, err := h.service.SaveRankingConfig(c.Request.Context(), req)
	if err != nil {
		handleServiceError(c, err, "Failed to save ranking configuration")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ranking_config": cfg})
}

func (h *Handler) SaveRanking(c *gin.Context) {
	var req SaveRankingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	res, err := h.service.SaveRanking(c.Request.Context(), req)
	if err != nil {
		handleServiceError(c, err, "Failed to save ranking configuration")
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) SaveGroup(c *gin.Context) {
	groupName := c.Param("group")
	var req SaveGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	items, err := h.service.SaveGroup(c.Request.Context(), groupName, req.Items)
	if err != nil {
		handleServiceError(c, err, "Failed to save recommendation group")
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": groupName, "items": items})
}

func (h *Handler) SaveCheckInConfig(c *gin.Context) {
	var req CheckInConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg, err := h.service.SaveCheckInConfig(c.Request.Context(), req)
	if err != nil {
		handleServiceError(c, err, "Failed to save check-in configuration")
		return
	}
	c.JSON(http.StatusOK, gin.H{"checkin_config": cfg})
}

func (h *Handler) Preview(c *gin.Context) {
	var req PreviewRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	res, err := h.service.Preview(c.Request.Context(), req)
	if err != nil {
		handleServiceError(c, err, "Failed to preview operations catalog")
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) ListNovelOptions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	filter := NovelOptionsFilter{
		Search: c.Query("search"),
		Author: c.Query("author"),
		Genre:  c.Query("genre"),
		Status: c.Query("status"),
		Sort:   c.Query("sort"),
	}
	res, err := h.service.ListNovelOptions(c.Request.Context(), page, pageSize, filter)
	if err != nil {
		if errors.Is(err, ErrInvalidNovelOptionsFilter) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch published novel options"})
		return
	}
	c.JSON(http.StatusOK, res)
}

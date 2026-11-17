package operations

import (
	"errors"
	"net/http"

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
	opsRoles := auth.RoleMiddleware("SuperAdmin", "Admin", "Editor")
	ops := r.Group("/operations", auth.AuthMiddleware())
	{
		ops.GET("", h.GetConfig)
		ops.PUT("/ranking-config", opsRoles, h.SaveRankingConfig)
		ops.PUT("/groups/:group", opsRoles, h.SaveGroup)
	}
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
		if errors.Is(err, ErrInvalidRankingSort) || errors.Is(err, ErrInvalidRankingLimit) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save ranking configuration"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ranking_config": cfg})
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
		if errors.Is(err, ErrInvalidGroup) ||
			errors.Is(err, ErrInvalidNovelID) ||
			errors.Is(err, ErrDuplicateNovelInGroup) ||
			errors.Is(err, ErrInvalidScheduleRange) ||
			errors.Is(err, ErrInvalidRankingLimit) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save recommendation group"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": groupName, "items": items})
}

package domain

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/auth"
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
	domainsGroup := r.Group("/domains", auth.AuthMiddleware())
	{
		domainsGroup.GET("", h.ListDomains)
		domainsGroup.POST("", h.CreateDomain)
		domainsGroup.PUT("/:id/status", h.UpdateDomainStatus)
		domainsGroup.POST("/:id/set-default", h.SetDefaultDomain)
		domainsGroup.DELETE("/:id", h.DeleteDomain)
	}
}

func (h *Handler) ListDomains(c *gin.Context) {
	ctx := c.Request.Context()
	domains, err := h.service.ListDomains(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list system domains: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, domains)
}

func (h *Handler) CreateDomain(c *gin.Context) {
	var req CreateDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	domain, err := h.service.CreateDomain(ctx, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create domain: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, domain)
}

func (h *Handler) UpdateDomainStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	var req UpdateDomainStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err = h.service.UpdateDomainStatus(ctx, id, req.Status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update domain status: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Domain status updated successfully"})
}

func (h *Handler) SetDefaultDomain(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	ctx := c.Request.Context()
	err = h.service.SetDefaultDomain(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to set default domain: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Set default domain successfully"})
}

func (h *Handler) DeleteDomain(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	ctx := c.Request.Context()
	err = h.service.DeleteDomain(ctx, id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Domain deleted successfully"})
}

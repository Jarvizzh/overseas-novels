package meta

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	metaGroup := r.Group("/meta")
	{
		metaGroup.GET("/hierarchy", h.GetHierarchy)
		metaGroup.GET("/overview", h.GetOverview)
		metaGroup.POST("/sync", h.SyncData)
		metaGroup.POST("/purge", h.PurgeData)
		metaGroup.GET("/config", h.GetConfig)
		metaGroup.POST("/config", h.SaveConfig)
	}
}

func (h *Handler) GetHierarchy(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	data, err := h.service.GetHierarchy(c.Request.Context(), startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (h *Handler) GetOverview(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	data, err := h.service.GetOverview(c.Request.Context(), startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (h *Handler) SyncData(c *gin.Context) {
	var req struct {
		DatePreset string `json:"date_preset"`
	}

	_ = c.ShouldBindJSON(&req)
	if req.DatePreset == "" {
		req.DatePreset = c.Query("date_preset")
	}
	if req.DatePreset == "" {
		req.DatePreset = "last_7d"
	}

	go func() {
		_ = h.service.SyncAllData(req.DatePreset)
	}()

	c.JSON(http.StatusOK, gin.H{
		"message":     "Data sync task triggered successfully!",
		"date_preset": req.DatePreset,
	})
}

func (h *Handler) PurgeData(c *gin.Context) {
	if err := h.service.PurgeAllData(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "All Meta advertising data purged successfully!",
	})
}

func (h *Handler) GetConfig(c *gin.Context) {
	token := h.service.GetConfig(c.Request.Context(), "META_ACCESS_TOKEN")
	bmID := h.service.GetConfig(c.Request.Context(), "META_BUSINESS_ID")
	apiVersion := h.service.GetConfig(c.Request.Context(), "META_API_VERSION")
	if apiVersion == "" {
		apiVersion = "v25.0"
	}
	c.JSON(http.StatusOK, gin.H{
		"meta_access_token": token,
		"meta_business_id":  bmID,
		"meta_api_version":  apiVersion,
	})
}

func (h *Handler) SaveConfig(c *gin.Context) {
	var req struct {
		MetaAccessToken string `json:"meta_access_token"`
		MetaBusinessID  string `json:"meta_business_id"`
		MetaAPIVersion  string `json:"meta_api_version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.MetaAPIVersion == "" {
		req.MetaAPIVersion = "v25.0"
	}

	_ = h.service.SetConfig(c.Request.Context(), "META_ACCESS_TOKEN", req.MetaAccessToken)
	_ = h.service.SetConfig(c.Request.Context(), "META_BUSINESS_ID", req.MetaBusinessID)
	_ = h.service.SetConfig(c.Request.Context(), "META_API_VERSION", req.MetaAPIVersion)
	_ = h.service.SetConfig(c.Request.Context(), "meta_api_version", req.MetaAPIVersion)

	go func() {
		_ = h.service.SyncAllData("last_7d")
	}()

	c.JSON(http.StatusOK, gin.H{
		"message":     "Configuration saved & sync started successfully!",
		"date_preset": "last_7d",
	})
}

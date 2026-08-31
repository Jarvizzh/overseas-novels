package novel

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/auth"
)

type Handler struct {
	service NovelService
}

func NewHandler(service NovelService) *Handler {
	return &Handler{
		service: service,
	}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	novels := rg.Group("/novels", auth.AuthMiddleware())
	{
		novels.GET("", h.ListNovels)
		novels.GET("/:id", h.GetNovel)
		novels.POST("", h.CreateNovel)
		novels.PUT("/:id", h.UpdateNovel)
		novels.DELETE("/:id", h.DeleteNovel)

		// Chapters sub-routes
		novels.GET("/:id/chapters", h.ListChapters)
		novels.GET("/:id/chapters/:index", h.GetChapterDetail)
		novels.POST("/:id/chapters", h.CreateChapter)
		novels.PUT("/:id/chapters/:index", h.UpdateChapter)
		novels.POST("/:id/chapters/bulk-import", h.BulkImportChapters)
	}

	settings := rg.Group("/settings", auth.AuthMiddleware())
	{
		settings.GET("", h.GetSettings)
		settings.POST("", h.UpdateSettings)
	}

	promo := rg.Group("/promotion-links", auth.AuthMiddleware())
	{
		promo.GET("", h.ListPromotionLinks)
		promo.POST("", h.CreatePromotionLink)
		promo.PUT("/:id", h.UpdatePromotionLink)
		promo.DELETE("/:id", h.DeletePromotionLink)
	}

	pixels := rg.Group("/fb-pixels", auth.AuthMiddleware())
	{
		pixels.GET("", h.ListFBPixels)
		pixels.POST("", h.CreateFBPixel)
		pixels.PUT("/:id", h.UpdateFBPixel)
		pixels.DELETE("/:id", h.DeleteFBPixel)
	}
}

func (h *Handler) ListNovels(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	status := c.Query("status")
	genre := c.Query("genre")
	search := c.Query("search")

	ctx := c.Request.Context()
	novels, total, err := h.service.ListNovels(ctx, page, pageSize, status, genre, search)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch novels: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"novels":    novels,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *Handler) GetNovel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	ctx := c.Request.Context()
	n, err := h.service.GetNovel(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, n)
}

type CreateNovelReq struct {
	Title                string   `json:"title" binding:"required"`
	Author               string   `json:"author" binding:"required"`
	CoverURL             string   `json:"cover_url"`
	Status               string   `json:"status" binding:"required,oneof=Ongoing Completed"`
	Synopsis             string   `json:"synopsis"`
	Genres               []string `json:"genres"`
	Rating               float64  `json:"rating"`
	CoinCostPerThousand  *int     `json:"coin_cost_per_thousand"`
	StartPayChapterIndex int      `json:"start_pay_chapter_index"`
}

func (h *Handler) CreateNovel(c *gin.Context) {
	var req CreateNovelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	n := &Novel{
		Title:                req.Title,
		Author:               req.Author,
		CoverURL:             req.CoverURL,
		Status:               req.Status,
		Synopsis:             req.Synopsis,
		Genres:               req.Genres,
		Rating:               req.Rating,
		CoinCostPerThousand:  req.CoinCostPerThousand,
		StartPayChapterIndex: req.StartPayChapterIndex,
	}

	created, err := h.service.CreateNovel(ctx, n)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, created)
}

type UpdateNovelReq struct {
	Title                string   `json:"title" binding:"required"`
	Author               string   `json:"author" binding:"required"`
	CoverURL             string   `json:"cover_url"`
	Status               string   `json:"status" binding:"required,oneof=Ongoing Completed"`
	Synopsis             string   `json:"synopsis"`
	Genres               []string `json:"genres"`
	Rating               float64  `json:"rating"`
	CoinCostPerThousand  *int     `json:"coin_cost_per_thousand"`
	StartPayChapterIndex int      `json:"start_pay_chapter_index"`
}

func (h *Handler) UpdateNovel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	var req UpdateNovelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	n, err := h.service.UpdateNovel(ctx, id, req.Title, req.Author, req.CoverURL, req.Status, req.Synopsis, req.Genres, req.Rating, req.CoinCostPerThousand, req.StartPayChapterIndex)
	if err != nil {
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, n)
}

func (h *Handler) DeleteNovel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	ctx := c.Request.Context()
	err = h.service.DeleteNovel(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Novel and all associated chapters deleted successfully"})
}

func (h *Handler) ListChapters(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	ctx := c.Request.Context()
	chapters, err := h.service.ListChapters(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, chapters)
}

func (h *Handler) GetChapterDetail(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	indexStr := c.Param("index")
	idx, err := strconv.Atoi(indexStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter index"})
		return
	}

	ctx := c.Request.Context()
	chapter, err := h.service.GetChapterDetail(ctx, id, idx)
	if err != nil {
		if errors.Is(err, ErrChapterNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, chapter)
}

type CreateChapterReq struct {
	ChapterIndex int    `json:"chapter_index" binding:"gte=0"`
	Title        string `json:"title" binding:"required"`
	Content      string `json:"content" binding:"required"`
	IsPaid       bool   `json:"is_paid"`
	Price        int    `json:"price"`
}

func (h *Handler) CreateChapter(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	var req CreateChapterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	chapter, err := h.service.CreateChapter(ctx, id, req.ChapterIndex, req.Title, req.Content, req.IsPaid, req.Price)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, chapter)
}

type UpdateChapterReq struct {
	Title   string `json:"title" binding:"required"`
	Content string `json:"content" binding:"required"`
	IsPaid  bool   `json:"is_paid"`
	Price   int    `json:"price"`
}

func (h *Handler) UpdateChapter(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	indexStr := c.Param("index")
	idx, err := strconv.Atoi(indexStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter index"})
		return
	}

	var req UpdateChapterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err = h.service.UpdateChapter(ctx, id, idx, req.Title, req.Content, req.IsPaid, req.Price)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Chapter updated successfully"})
}

func (h *Handler) BulkImportChapters(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	importType := c.DefaultPostForm("import_type", "single_txt")

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ZIP/TXT file is required via form-data 'file'"})
		return
	}

	f, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open uploaded file"})
		return
	}
	defer f.Close()

	zipData, err := io.ReadAll(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read uploaded file"})
		return
	}

	ctx := c.Request.Context()
	err = h.service.BulkImportChapters(ctx, id, zipData, importType)
	if err != nil {
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Chapters imported successfully"})
}

func (h *Handler) GetSettings(c *gin.Context) {
	ctx := c.Request.Context()
	settings, err := h.service.GetSettings(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch system configs: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, settings)
}

func (h *Handler) UpdateSettings(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err := h.service.UpdateSettings(ctx, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Global settings updated successfully"})
}

func (h *Handler) ListPromotionLinks(c *gin.Context) {
	ctx := c.Request.Context()
	links, err := h.service.ListPromotionLinks(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, links)
}

type CreatePromotionLinkReq struct {
	Name                 string `json:"name"`
	NovelID              int64  `json:"novel_id" binding:"required"`
	NovelTitle           string `json:"novel_title" binding:"required"`
	ChapterIndex         int    `json:"chapter_index"`
	UtmSource            string `json:"utm_source"`
	UtmCampaign          string `json:"utm_campaign"`
	GeneratedURL         string `json:"generated_url" binding:"required"`
	FBPixelID            *int   `json:"fb_pixel_id"`
	RechargeTemplateID   *int   `json:"recharge_template_id"`
	DomainID             *int   `json:"domain_id"`
	Domain               string `json:"domain"`
	CoinCostPerThousand  *int   `json:"coin_cost_per_thousand"`
	StartPayChapterIndex *int   `json:"start_pay_chapter_index"`
}

func (h *Handler) CreatePromotionLink(c *gin.Context) {
	var req CreatePromotionLinkReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.FBPixelID == nil || *req.FBPixelID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "推广链接必须显式绑定 Facebook 像素"})
		return
	}

	ctx := c.Request.Context()
	link := &PromotionLink{
		Name:                 req.Name,
		NovelID:              req.NovelID,
		NovelTitle:           req.NovelTitle,
		ChapterIndex:         req.ChapterIndex,
		UtmSource:            req.UtmSource,
		UtmCampaign:          req.UtmCampaign,
		GeneratedURL:         req.GeneratedURL,
		FBPixelID:            req.FBPixelID,
		RechargeTemplateID:   req.RechargeTemplateID,
		DomainID:             req.DomainID,
		Domain:               req.Domain,
		CoinCostPerThousand:  req.CoinCostPerThousand,
		StartPayChapterIndex: req.StartPayChapterIndex,
	}

	created, err := h.service.CreatePromotionLink(ctx, link)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, created)
}

type UpdatePromotionLinkReq struct {
	Name                 string `json:"name"`
	ChapterIndex         int    `json:"chapter_index"`
	UtmSource            string `json:"utm_source"`
	UtmCampaign          string `json:"utm_campaign"`
	GeneratedURL         string `json:"generated_url" binding:"required"`
	FBPixelID            *int   `json:"fb_pixel_id"`
	RechargeTemplateID   *int   `json:"recharge_template_id"`
	DomainID             *int   `json:"domain_id"`
	Domain               string `json:"domain"`
	CoinCostPerThousand  *int   `json:"coin_cost_per_thousand"`
	StartPayChapterIndex *int   `json:"start_pay_chapter_index"`
}

func (h *Handler) UpdatePromotionLink(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	var req UpdatePromotionLinkReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.FBPixelID == nil || *req.FBPixelID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "推广链接必须显式绑定 Facebook 像素"})
		return
	}

	ctx := c.Request.Context()
	err = h.service.UpdatePromotionLink(ctx, id, req.Name, req.ChapterIndex, req.UtmSource, req.UtmCampaign, req.GeneratedURL, req.FBPixelID, req.RechargeTemplateID, req.DomainID, req.CoinCostPerThousand, req.StartPayChapterIndex, req.Domain)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Promotion link updated successfully"})
}

func (h *Handler) DeletePromotionLink(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	ctx := c.Request.Context()
	err = h.service.DeletePromotionLink(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Promotion link deleted successfully"})
}

func (h *Handler) ListFBPixels(c *gin.Context) {
	ctx := c.Request.Context()
	pixels, err := h.service.ListFBPixels(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, pixels)
}

type CreateFBPixelRequest struct {
	Name        string `json:"name" binding:"required"`
	PixelID     string `json:"pixel_id" binding:"required"`
	AccessToken string `json:"access_token" binding:"required"`
}

func (h *Handler) CreateFBPixel(c *gin.Context) {
	var req CreateFBPixelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	pixel := &FBPixel{
		Name:        req.Name,
		PixelID:     req.PixelID,
		AccessToken: req.AccessToken,
	}

	created, err := h.service.CreateFBPixel(ctx, pixel)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, created)
}

type UpdateFBPixelRequest struct {
	Name        string `json:"name" binding:"required"`
	PixelID     string `json:"pixel_id" binding:"required"`
	AccessToken string `json:"access_token" binding:"required"`
}

func (h *Handler) UpdateFBPixel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	var req UpdateFBPixelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err = h.service.UpdateFBPixel(ctx, id, req.Name, req.PixelID, req.AccessToken)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Pixel updated successfully"})
}

func (h *Handler) DeleteFBPixel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	ctx := c.Request.Context()
	err = h.service.DeleteFBPixel(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Pixel deleted successfully"})
}

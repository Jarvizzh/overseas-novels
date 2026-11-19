package novel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/storage"
)

type Handler struct {
	service NovelService
}

func NewHandler(service NovelService) *Handler {
	return &Handler{
		service: service,
	}
}

const maxBulkImportBytes int64 = 50 << 20

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	editorRoles := auth.RoleMiddleware("SuperAdmin", "Admin", "Editor")
	settingsWriteRoles := auth.RoleMiddleware("SuperAdmin", "Admin")
	promoWriteRoles := auth.RoleMiddleware("SuperAdmin", "Admin", "Editor", "MediaBuyer")

	novels := rg.Group("/novels", auth.AuthMiddleware())
	{
		novels.GET("", h.ListNovels)
		novels.POST("/cover-upload", editorRoles, h.UploadCover)
		novels.POST("/batch-import", editorRoles, h.BatchImportNovels)
		novels.PATCH("/batch-publish-status", editorRoles, h.BatchUpdatePublishStatus)
		novels.GET("/:id", h.GetNovel)
		novels.POST("", editorRoles, h.CreateNovel)
		novels.PUT("/:id", editorRoles, h.UpdateNovel)
		novels.PATCH("/:id/publish-status", editorRoles, h.UpdatePublishStatus)
		novels.DELETE("/:id", editorRoles, h.DeleteNovel)

		// Chapters sub-routes
		novels.GET("/:id/chapters", h.ListChapters)
		novels.GET("/:id/chapters/:index", h.GetChapterDetail)
		novels.GET("/:id/chapters/identities", editorRoles, h.ListChapterIdentities)
		novels.POST("/:id/chapters", editorRoles, h.CreateChapter)
		novels.PUT("/:id/chapters/:index", editorRoles, h.UpdateChapter)
		novels.POST("/:id/chapters/bulk-import", editorRoles, h.BulkImportChapters)
	}

	settings := rg.Group("/settings", auth.AuthMiddleware())
	{
		settings.GET("", h.GetSettings)
		settings.POST("", settingsWriteRoles, h.UpdateSettings)
	}

	promo := rg.Group("/promotion-links", auth.AuthMiddleware())
	{
		promo.GET("", h.ListPromotionLinks)
		promo.POST("", promoWriteRoles, h.CreatePromotionLink)
		promo.PUT("/:id", promoWriteRoles, h.UpdatePromotionLink)
		promo.DELETE("/:id", promoWriteRoles, h.DeletePromotionLink)
	}

	pixels := rg.Group("/fb-pixels", auth.AuthMiddleware())
	{
		pixels.GET("", h.ListFBPixels)
	}
}

func (h *Handler) ListNovels(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if pageSize < 1 {
		pageSize = 10
	} else if pageSize > 200 {
		pageSize = 200
	}
	filter := NovelListFilter{
		Status:        c.Query("status"),
		PublishStatus: c.Query("publish_status"),
		Genre:         c.Query("genre"),
		Search:        c.Query("search"),
		TitleOrID:     c.Query("title_or_id"),
		Author:        c.Query("author"),
		CreatedStart:  c.Query("created_start"),
		CreatedEnd:    c.Query("created_end"),
		Sort:          c.Query("sort"),
	}

	ctx := c.Request.Context()
	novels, total, err := h.service.ListNovels(ctx, page, pageSize, filter)
	if err != nil {
		if errors.Is(err, ErrInvalidFilterParam) || errors.Is(err, ErrInvalidPublishStatus) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
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
	PublishStatus        string   `json:"publish_status" binding:"omitempty,oneof=draft published offline"`
	Synopsis             string   `json:"synopsis"`
	Genres               []string `json:"genres"`
	Rating               float64  `json:"rating"`
	CoinCostPerThousand  *int     `json:"coin_cost_per_thousand"`
	StartPayChapterIndex int      `json:"start_pay_chapter_index"`
}

type flexibleGenres []string

func (g *flexibleGenres) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*g = nil
		return nil
	}
	if trimmed[0] == '[' {
		var list []string
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return err
		}
		*g = list
		return nil
	}
	var raw string
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return err
	}
	if strings.TrimSpace(raw) == "" {
		*g = nil
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	*g = out
	return nil
}

type batchImportManifestItem struct {
	Title                string         `json:"title"`
	Author               string         `json:"author"`
	CoverURL             string         `json:"cover_url"`
	CoverField           string         `json:"cover_field"`
	ChapterField         string         `json:"chapter_field"`
	ImportType           string         `json:"import_type"`
	DelimiterRegex       string         `json:"delimiter_regex"`
	Status               string         `json:"status"`
	PublishStatus        string         `json:"publish_status"`
	Synopsis             string         `json:"synopsis"`
	Genres               flexibleGenres `json:"genres"`
	Rating               *float64       `json:"rating"`
	CoinCostPerThousand  *int           `json:"coin_cost_per_thousand"`
	StartPayChapterIndex int            `json:"start_pay_chapter_index"`
}

func readMultipartFileField(form *multipart.Form, fieldName string, maxBytes int64, tooLargeMsg string) ([]byte, bool, error) {
	files := form.File[fieldName]
	if len(files) == 0 {
		return nil, false, nil
	}
	fh := files[0]
	if fh.Size > maxBytes {
		return nil, true, errors.New(tooLargeMsg)
	}
	f, err := fh.Open()
	if err != nil {
		return nil, true, fmt.Errorf("打开上传文件 %q 失败", fieldName)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, true, fmt.Errorf("读取上传文件 %q 失败", fieldName)
	}
	if int64(len(data)) > maxBytes {
		return nil, true, errors.New(tooLargeMsg)
	}
	return data, true, nil
}

func parseBatchImportManifest(form *multipart.Form) ([]batchImportManifestItem, error) {
	if raw := strings.TrimSpace(cFirstFormValue(form, "manifest")); raw != "" {
		var items []batchImportManifestItem
		if strings.HasPrefix(raw, "{") {
			var single batchImportManifestItem
			if err := json.Unmarshal([]byte(raw), &single); err != nil {
				return nil, fmt.Errorf("manifest JSON 格式无效: %w", err)
			}
			items = []batchImportManifestItem{single}
		} else {
			if err := json.Unmarshal([]byte(raw), &items); err != nil {
				return nil, fmt.Errorf("manifest JSON 数组格式无效: %w", err)
			}
		}
		return items, nil
	}

	title := strings.TrimSpace(cFirstFormValue(form, "title"))
	author := strings.TrimSpace(cFirstFormValue(form, "author"))
	if title == "" && author == "" {
		return nil, errors.New("请提供 manifest JSON 数组（form-data 字段名: manifest）")
	}

	single := batchImportManifestItem{
		Title:          title,
		Author:         author,
		CoverURL:       strings.TrimSpace(cFirstFormValue(form, "cover_url")),
		CoverField:     strings.TrimSpace(cFirstFormValue(form, "cover_field")),
		ChapterField:   strings.TrimSpace(cFirstFormValue(form, "chapter_field")),
		ImportType:     strings.TrimSpace(cFirstFormValue(form, "import_type")),
		DelimiterRegex: cFirstFormValue(form, "delimiter_regex"),
		Status:         strings.TrimSpace(cFirstFormValue(form, "status")),
		PublishStatus:  strings.TrimSpace(cFirstFormValue(form, "publish_status")),
		Synopsis:       cFirstFormValue(form, "synopsis"),
	}
	if single.CoverField == "" {
		if len(form.File["cover"]) > 0 {
			single.CoverField = "cover"
		} else if len(form.File["cover_file"]) > 0 {
			single.CoverField = "cover_file"
		}
	}
	if single.ChapterField == "" {
		if len(form.File["file"]) > 0 {
			single.ChapterField = "file"
		} else if len(form.File["chapter_file"]) > 0 {
			single.ChapterField = "chapter_file"
		}
	}
	if rawGenres := strings.TrimSpace(cFirstFormValue(form, "genres")); rawGenres != "" {
		if err := json.Unmarshal([]byte(rawGenres), &single.Genres); err != nil {
			parts := strings.Split(rawGenres, ",")
			for _, p := range parts {
				if t := strings.TrimSpace(p); t != "" {
					single.Genres = append(single.Genres, t)
				}
			}
		}
	}
	if rawRating := strings.TrimSpace(cFirstFormValue(form, "rating")); rawRating != "" {
		val, err := strconv.ParseFloat(rawRating, 64)
		if err != nil {
			return nil, errors.New("评分 rating 必须为数字")
		}
		single.Rating = &val
	}
	if rawCost := strings.TrimSpace(cFirstFormValue(form, "coin_cost_per_thousand")); rawCost != "" {
		val, err := strconv.Atoi(rawCost)
		if err != nil {
			return nil, errors.New("千字金币单价 coin_cost_per_thousand 必须为整数")
		}
		single.CoinCostPerThousand = &val
	}
	if rawStartPay := strings.TrimSpace(cFirstFormValue(form, "start_pay_chapter_index")); rawStartPay != "" {
		val, err := strconv.Atoi(rawStartPay)
		if err != nil {
			return nil, errors.New("起始收费章节 start_pay_chapter_index 必须为整数")
		}
		single.StartPayChapterIndex = val
	}
	return []batchImportManifestItem{single}, nil
}

func cFirstFormValue(form *multipart.Form, key string) string {
	if form == nil {
		return ""
	}
	vals := form.Value[key]
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func (h *Handler) BatchImportNovels(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBulkImportBytes+(2<<20))

	form, err := c.MultipartForm()
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) || strings.Contains(err.Error(), "http: request body too large") {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "批量导入单次请求体大小不能超过 50MB，请拆分批次提交"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "请使用 multipart/form-data 上传 manifest 与对应封面/章节文件: " + err.Error()})
		return
	}

	manifestItems, err := parseBatchImportManifest(form)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(manifestItems) == 0 || len(manifestItems) > maxBatchImportNovels {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("manifest 书籍数量必须在 1 到 %d 本之间", maxBatchImportNovels)})
		return
	}

	maxCoverBytes := h.service.MaxCoverUploadBytes()
	inputs := make([]BatchImportNovelInput, 0, len(manifestItems))
	for _, item := range manifestItems {
		input := BatchImportNovelInput{
			Title:                item.Title,
			Author:               item.Author,
			CoverURL:             item.CoverURL,
			Status:               item.Status,
			PublishStatus:        item.PublishStatus,
			Synopsis:             item.Synopsis,
			Genres:               item.Genres,
			Rating:               item.Rating,
			CoinCostPerThousand:  item.CoinCostPerThousand,
			StartPayChapterIndex: item.StartPayChapterIndex,
			ImportType:           item.ImportType,
			DelimiterRegex:       item.DelimiterRegex,
		}

		coverField := strings.TrimSpace(item.CoverField)
		if coverField != "" && strings.TrimSpace(item.CoverURL) != "" {
			input.PreError = "不能同时填写 cover_field 与 cover_url，请二选一"
		} else if coverField != "" {
			coverBytes, found, readErr := readMultipartFileField(form, coverField, maxCoverBytes, storage.ErrCoverTooLarge.Error())
			if readErr != nil {
				input.PreError = readErr.Error()
			} else if !found {
				input.PreError = fmt.Sprintf("未找到封面文件字段 %q", coverField)
			} else {
				input.CoverBytes = coverBytes
			}
		}

		chapterField := strings.TrimSpace(item.ChapterField)
		if chapterField == "" && len(manifestItems) == 1 && len(form.File["file"]) > 0 {
			chapterField = "file"
		}
		if input.PreError == "" {
			if chapterField == "" {
				input.PreError = "请提供章节文件字段 chapter_field"
			} else {
				chapterBytes, found, readErr := readMultipartFileField(form, chapterField, maxBulkImportBytes, "章节文件大小不能超过 50MB")
				if readErr != nil {
					input.PreError = readErr.Error()
				} else if !found {
					input.PreError = fmt.Sprintf("未找到章节文件字段 %q", chapterField)
				} else {
					input.ChapterBytes = chapterBytes
				}
			}
		}

		inputs = append(inputs, input)
	}

	result, err := h.service.BatchImportNovels(c.Request.Context(), inputs)
	if err != nil {
		if errors.Is(err, ErrInvalidBatchImport) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if result.CreatedCount == 0 {
		c.JSON(http.StatusBadRequest, result)
		return
	}
	c.JSON(http.StatusOK, result)
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
		PublishStatus:        req.PublishStatus,
		Synopsis:             req.Synopsis,
		Genres:               req.Genres,
		Rating:               req.Rating,
		CoinCostPerThousand:  req.CoinCostPerThousand,
		StartPayChapterIndex: req.StartPayChapterIndex,
	}

	created, err := h.service.CreateNovel(ctx, n)
	if err != nil {
		if errors.Is(err, ErrInvalidCover) || errors.Is(err, ErrInvalidPublishStatus) || errors.Is(err, ErrCannotPublishEmptyNovel) || errors.Is(err, ErrInvalidGenres) || errors.Is(err, ErrInvalidNovelPricing) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, created)
}

func (h *Handler) UploadCover(c *gin.Context) {
	maxBytes := h.service.MaxCoverUploadBytes()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+(1<<20))

	fileHeader, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": storage.ErrCoverTooLarge.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传封面图片文件（form-data 字段名: file）"})
		return
	}
	if fileHeader.Size > maxBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": storage.ErrCoverTooLarge.Error()})
		return
	}

	f, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取上传文件失败"})
		return
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": storage.ErrCoverTooLarge.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取封面图片内容失败"})
		return
	}

	coverKey, previewURL, err := h.service.UploadCover(c.Request.Context(), data)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrCoverOSSDisabled):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		case errors.Is(err, storage.ErrCoverEmpty),
			errors.Is(err, storage.ErrCoverTooLarge),
			errors.Is(err, storage.ErrCoverUnsupportedType),
			errors.Is(err, storage.ErrCoverInvalidImage):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"cover_key":   coverKey,
		"preview_url": previewURL,
	})
}

type UpdateNovelReq struct {
	Title                string   `json:"title" binding:"required"`
	Author               string   `json:"author" binding:"required"`
	CoverURL             string   `json:"cover_url"`
	Status               string   `json:"status" binding:"required,oneof=Ongoing Completed"`
	PublishStatus        string   `json:"publish_status" binding:"omitempty,oneof=draft published offline"`
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
	n, err := h.service.UpdateNovel(ctx, id, req.Title, req.Author, req.CoverURL, req.Status, req.PublishStatus, req.Synopsis, req.Genres, req.Rating, req.CoinCostPerThousand, req.StartPayChapterIndex)
	if err != nil {
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrInvalidCover) || errors.Is(err, ErrInvalidPublishStatus) || errors.Is(err, ErrCannotPublishEmptyNovel) || errors.Is(err, ErrInvalidGenres) || errors.Is(err, ErrInvalidNovelPricing) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, n)
}

type UpdatePublishStatusReq struct {
	PublishStatus string `json:"publish_status" binding:"required,oneof=draft published offline"`
}

func (h *Handler) UpdatePublishStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	var req UpdatePublishStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := h.service.UpdatePublishStatus(c.Request.Context(), id, req.PublishStatus)
	if err != nil {
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrInvalidPublishStatus) || errors.Is(err, ErrCannotPublishEmptyNovel) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, updated)
}

type BatchUpdatePublishStatusReq struct {
	IDs           []int64 `json:"ids" binding:"required,min=1"`
	PublishStatus string  `json:"publish_status" binding:"required,oneof=draft published offline"`
}

func (h *Handler) BatchUpdatePublishStatus(c *gin.Context) {
	var req BatchUpdatePublishStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.service.BatchUpdatePublishStatus(c.Request.Context(), req.IDs, req.PublishStatus)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidBatchNovelIDs),
			errors.Is(err, ErrBatchLimitExceeded),
			errors.Is(err, ErrInvalidPublishStatus),
			errors.Is(err, ErrCannotPublishEmptyNovel):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, ErrNovelNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, result)
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
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, chapters)
}

func (h *Handler) ListChapterIdentities(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
		return
	}
	items, err := h.service.GetChapterIdentities(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		log.Printf("list chapter identities for novel %d: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载章节标识失败"})
		return
	}
	c.JSON(http.StatusOK, items)
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
	if err != nil || idx < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter index"})
		return
	}

	ctx := c.Request.Context()
	chapter, err := h.service.GetChapterDetail(ctx, id, idx)
	if err != nil {
		if errors.Is(err, ErrNovelNotFound) || errors.Is(err, ErrChapterNotFound) {
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
	Title        string `json:"title" binding:"required,max=150"`
	Content      string `json:"content" binding:"required"`
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
	chapter, err := h.service.CreateChapter(ctx, id, req.ChapterIndex, req.Title, req.Content)
	if err != nil {
		if errors.Is(err, ErrPublicationChanged) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "该章节序号已存在，请勿重复创建"})
			return
		}
		if errors.Is(err, ErrInvalidNovelPricing) || errors.Is(err, ErrInvalidImportFile) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		log.Printf("publish chapter for novel %d: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "章节发布失败，请稍后重试"})
		return
	}

	c.JSON(http.StatusCreated, chapter)
}

type UpdateChapterReq struct {
	ID      string `json:"chapter_id" binding:"required,max=64"`
	Title   string `json:"title" binding:"required,max=150"`
	Content string `json:"content" binding:"required"`
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
	if err != nil || idx < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter index"})
		return
	}

	var req UpdateChapterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err = h.service.UpdateChapter(ctx, id, idx, req.Title, req.Content, req.ID)
	if err != nil {
		if errors.Is(err, ErrPublicationChanged) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrNovelNotFound) || errors.Is(err, ErrChapterNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrInvalidNovelPricing) || errors.Is(err, ErrInvalidImportFile) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		log.Printf("publish chapter for novel %d: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "章节发布失败，请稍后重试"})
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

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBulkImportBytes+(2<<20))

	fileHeader, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "导入文件大小不能超过 50MB"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "ZIP/TXT file is required via form-data 'file'"})
		return
	}
	if fileHeader.Size > maxBulkImportBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "导入文件大小不能超过 50MB"})
		return
	}

	importType := c.DefaultPostForm("import_type", "single_txt")
	delimiterRegex := c.PostForm("delimiter_regex")

	f, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open uploaded file"})
		return
	}
	defer f.Close()

	zipData, err := io.ReadAll(io.LimitReader(f, maxBulkImportBytes+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "导入文件大小不能超过 50MB"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read uploaded file"})
		return
	}
	if int64(len(zipData)) > maxBulkImportBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "导入文件大小不能超过 50MB"})
		return
	}

	var mappings []ChapterImportMapping
	if raw := c.PostForm("chapter_mapping"); raw != "" {
		var mapping ChapterImportMapping
		if err := json.Unmarshal([]byte(raw), &mapping); err != nil || mapping.Version <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "章节映射格式无效"})
			return
		}
		mappings = append(mappings, mapping)
	}
	ctx := c.Request.Context()
	result, err := h.service.BulkImportChapters(ctx, id, zipData, importType, delimiterRegex, mappings...)
	if err != nil {
		switch {
		case errors.Is(err, ErrNovelNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, ErrChapterMapping), errors.Is(err, ErrPublicationChanged):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case errors.Is(err, ErrInvalidImportFile), errors.Is(err, ErrInvalidDelimiterRegex), errors.Is(err, ErrInvalidNovelPricing):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			log.Printf("import chapters for novel %d: %v", id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "导入章节失败，请稍后重试"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":          "Chapters imported successfully",
		"chapters_count":   result.ChaptersCount,
		"total_word_count": result.TotalWordCount,
	})
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
		if errors.Is(err, ErrInvalidSettingValue) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
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
		if errors.Is(err, ErrInvalidPromoPricing) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrNovelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
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
		if errors.Is(err, ErrInvalidPromoPricing) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
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

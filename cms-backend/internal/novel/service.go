package novel

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	redisclient "star-novel-cms/internal/redis"
	"star-novel-cms/internal/storage"
)

const maxGenresTotalChars = 2048

var (
	ErrNovelNotFound           = errors.New("Novel not found")
	ErrInvalidImportFile       = errors.New("无效的章节文件")
	ErrChapterNotFound         = errors.New("Chapter not found")
	ErrInvalidCover            = errors.New("请先上传书籍封面图片，不允许直接填写外部 URL")
	ErrInvalidDelimiterRegex   = errors.New("无效的章节分割正则表达式")
	ErrInvalidPublishStatus    = errors.New("无效的上架状态，仅支持 draft、published 或 offline")
	ErrCannotPublishEmptyNovel = errors.New("书籍暂无章节内容，请先导入章节后再上架发布")
	ErrInvalidSettingValue     = errors.New("无效的全局配置参数")
	ErrInvalidNovelPricing     = errors.New("小说千字单价必须为 0 到 2147483647 的整数")
	ErrInvalidPromoPricing     = errors.New("无效的推广链接计费参数")
	ErrInvalidGenres           = errors.New("分类标签总长度不能超过 2048 个字符")
	ErrInvalidFilterParam      = errors.New("无效的查询筛选参数")
	ErrInvalidBatchNovelIDs    = errors.New("请至少选择一本有效书籍")
	ErrBatchLimitExceeded      = errors.New("单次批量操作最多支持 200 本书籍")
	ErrInvalidBatchImport      = errors.New("无效的批量新建书籍请求")
)

const (
	maxBatchPublishIDs   = 200
	maxBatchImportNovels = 50
)

type BatchImportNovelInput struct {
	Title                string
	Author               string
	CoverURL             string
	CoverBytes           []byte
	Status               string
	PublishStatus        string
	Synopsis             string
	Genres               []string
	Rating               *float64
	CoinCostPerThousand  *int
	StartPayChapterIndex int
	ImportType           string
	DelimiterRegex       string
	ChapterBytes         []byte
	PreError             string
}

type BatchImportNovelItemResult struct {
	Index          int    `json:"index"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	Novel          *Novel `json:"novel,omitempty"`
	ChaptersCount  int    `json:"chapters_count,omitempty"`
	TotalWordCount int    `json:"total_word_count,omitempty"`
	Error          string `json:"error,omitempty"`
}

type BatchImportNovelsResult struct {
	Total        int                          `json:"total"`
	CreatedCount int                          `json:"created_count"`
	FailedCount  int                          `json:"failed_count"`
	Results      []BatchImportNovelItemResult `json:"results"`
}

type NovelListFilter struct {
	Status        string
	PublishStatus string
	Genre         string
	Search        string
	TitleOrID     string
	Author        string
	CreatedStart  string
	CreatedEnd    string
}

type BatchPublishSkippedItem struct {
	ID     int64  `json:"id"`
	Title  string `json:"title,omitempty"`
	Reason string `json:"reason"`
}

type BatchPublishResult struct {
	PublishStatus string                    `json:"publish_status"`
	UpdatedCount  int                       `json:"updated_count"`
	UpdatedIDs    []int64                   `json:"updated_ids"`
	Skipped       []BatchPublishSkippedItem `json:"skipped,omitempty"`
}

type BulkImportResult struct {
	ChaptersCount  int `json:"chapters_count"`
	TotalWordCount int `json:"total_word_count"`
}

type NovelService interface {
	ListNovels(ctx context.Context, page, pageSize int, filter NovelListFilter) ([]Novel, int, error)
	GetNovel(ctx context.Context, id int64) (*Novel, error)
	CreateNovel(ctx context.Context, n *Novel) (*Novel, error)
	BatchImportNovels(ctx context.Context, items []BatchImportNovelInput) (*BatchImportNovelsResult, error)
	UpdateNovel(ctx context.Context, id int64, title, author, coverURL, status, publishStatus, synopsis string, genres []string, rating float64, coinCost *int, startPayIndex int) (*Novel, error)
	UpdatePublishStatus(ctx context.Context, id int64, publishStatus string) (*Novel, error)
	BatchUpdatePublishStatus(ctx context.Context, ids []int64, publishStatus string) (*BatchPublishResult, error)
	DeleteNovel(ctx context.Context, id int64) error
	UploadCover(ctx context.Context, data []byte) (string, string, error)
	MaxCoverUploadBytes() int64

	ListChapters(ctx context.Context, novelID int64) ([]Chapter, error)
	GetChapterIdentities(ctx context.Context, novelID int64) ([]ChapterIdentity, error)
	GetChapterDetail(ctx context.Context, novelID int64, chapterIndex int) (*Chapter, error)
	CreateChapter(ctx context.Context, novelID int64, chapterIndex int, title, content string) (*Chapter, error)
	UpdateChapter(ctx context.Context, novelID int64, chapterIndex int, title, content string, expectedID string) error
	BulkImportChapters(ctx context.Context, novelID int64, zipData []byte, importType string, delimiterRegex string, mappings ...ChapterImportMapping) (*BulkImportResult, error)

	GetSettings(ctx context.Context) (map[string]string, error)
	UpdateSettings(ctx context.Context, configs map[string]string) error

	ListPromotionLinks(ctx context.Context) ([]PromotionLink, error)
	CreatePromotionLink(ctx context.Context, link *PromotionLink) (*PromotionLink, error)
	UpdatePromotionLink(ctx context.Context, id int, name string, chapterIndex int, source, campaign, url string, pixelID, templateID, domainID, coinCost, startPayIndex *int, domain string) error
	DeletePromotionLink(ctx context.Context, id int) error

	ListFBPixels(ctx context.Context) ([]FBPixel, error)
	CreateFBPixel(ctx context.Context, pixel *FBPixel) (*FBPixel, error)
	UpdateFBPixel(ctx context.Context, id int, name, pixelID, token string) error
	DeleteFBPixel(ctx context.Context, id int) error
}

type novelService struct {
	repo       NovelRepository
	storage    storage.ContentStorage
	coverStore storage.CoverStore
}

func NewNovelService(repo NovelRepository, storage storage.ContentStorage, coverStore storage.CoverStore) NovelService {
	return &novelService{
		repo:       repo,
		storage:    storage,
		coverStore: coverStore,
	}
}

func (s *novelService) applyCoverDisplay(n *Novel) {
	if n == nil {
		return
	}
	n.CoverKey = n.CoverURL
	if s.coverStore != nil {
		n.CoverURL = s.coverStore.SignCoverURL(n.CoverURL)
	}
}

func (s *novelService) isValidNewCoverRef(ref string) bool {
	if s.coverStore == nil {
		return false
	}
	return s.coverStore.IsValidCoverRef(ref)
}

func (s *novelService) MaxCoverUploadBytes() int64 {
	if s.coverStore != nil {
		return s.coverStore.MaxUploadBytes()
	}
	return 5 * 1024 * 1024
}

func (s *novelService) UploadCover(ctx context.Context, data []byte) (string, string, error) {
	if s.coverStore == nil {
		return "", "", storage.ErrCoverOSSDisabled
	}
	return s.coverStore.UploadCover(ctx, data)
}

// Cache invalidation helpers
func (s *novelService) invalidateNovelCache(ctx context.Context, novelID int64) {
	if redisclient.RDB == nil {
		return
	}
	keyDetail := fmt.Sprintf("novel:detail:%d", novelID)
	keyChapters := fmt.Sprintf("novel:chapters:%d", novelID)
	redisclient.RDB.Del(ctx, keyDetail, keyChapters)
}

func (s *novelService) invalidateChapterCache(ctx context.Context, novelID int64, chapterIndex int) {
	if redisclient.RDB == nil {
		return
	}
	keyChapter := fmt.Sprintf("novel:chapter:%d:%d", novelID, chapterIndex)
	redisclient.RDB.Del(ctx, keyChapter)
}

func (s *novelService) invalidateAllChaptersCache(ctx context.Context, novelID int64) {
	if redisclient.RDB == nil {
		return
	}
	pattern := fmt.Sprintf("novel:chapter:%d:*", novelID)
	iter := redisclient.RDB.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		redisclient.RDB.Del(ctx, iter.Val())
	}
}

func isValidPublishStatus(s string) bool {
	return s == "draft" || s == "published" || s == "offline"
}

func (s *novelService) resolveDefaultStartPay(ctx context.Context) (int, error) {
	configs, err := s.repo.GetSettings(ctx)
	if err != nil {
		return 0, err
	}
	return pricingSetting(configs, "global_start_pay_chapter_index", 1, 3)
}

func pricingSetting(configs map[string]string, key string, minimum, fallback int) (int, error) {
	if value, ok := configs[key]; ok {
		return validateSettingInt(value, minimum, key)
	}
	return fallback, nil
}

func parseNovelTimeBound(raw string, endOfDay bool) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return &t, nil
	}
	if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return &t, nil
	}
	if d, err := time.Parse("2006-01-02", trimmed); err == nil {
		if endOfDay {
			d = d.Add(24*time.Hour - time.Nanosecond)
		}
		return &d, nil
	}
	return nil, fmt.Errorf("%w: invalid timestamp %q", ErrInvalidFilterParam, trimmed)
}

func (s *novelService) ListNovels(ctx context.Context, page, pageSize int, filter NovelListFilter) ([]Novel, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize

	var whereClauses []string
	var args []interface{}
	argCount := 1

	status := strings.TrimSpace(filter.Status)
	publishStatus := strings.TrimSpace(filter.PublishStatus)
	genre := strings.TrimSpace(filter.Genre)
	search := strings.TrimSpace(filter.Search)
	titleOrID := strings.TrimSpace(filter.TitleOrID)
	author := strings.TrimSpace(filter.Author)

	if status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", argCount))
		args = append(args, status)
		argCount++
	} else {
		whereClauses = append(whereClauses, "status != 'archived'")
	}

	if publishStatus != "" {
		if !isValidPublishStatus(publishStatus) {
			return nil, 0, ErrInvalidPublishStatus
		}
		whereClauses = append(whereClauses, fmt.Sprintf("publish_status = $%d", argCount))
		args = append(args, publishStatus)
		argCount++
	}

	if genre != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("$%d = ANY(genres)", argCount))
		args = append(args, genre)
		argCount++
	}

	if titleOrID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("(title ILIKE $%d OR CAST(id AS TEXT) = $%d)", argCount, argCount+1))
		args = append(args, "%"+titleOrID+"%", titleOrID)
		argCount += 2
	}

	if author != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("author ILIKE $%d", argCount))
		args = append(args, "%"+author+"%")
		argCount++
	}

	if search != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("(title ILIKE $%d OR author ILIKE $%d OR CAST(id AS TEXT) = $%d)", argCount, argCount, argCount+1))
		args = append(args, "%"+search+"%", search)
		argCount += 2
	}

	startTime, err := parseNovelTimeBound(filter.CreatedStart, false)
	if err != nil {
		return nil, 0, err
	}
	endTime, err := parseNovelTimeBound(filter.CreatedEnd, true)
	if err != nil {
		return nil, 0, err
	}
	if startTime != nil && endTime != nil && startTime.After(*endTime) {
		return nil, 0, fmt.Errorf("%w: created_start cannot be after created_end", ErrInvalidFilterParam)
	}
	if startTime != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("created_at >= $%d", argCount))
		args = append(args, *startTime)
		argCount++
	}
	if endTime != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("created_at <= $%d", argCount))
		args = append(args, *endTime)
		argCount++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM novels %s", whereSQL)
	total, err := s.repo.CountNovels(ctx, countQuery, args)
	if err != nil {
		return nil, 0, err
	}

	selectQuery := fmt.Sprintf(`
		SELECT %s
		FROM novels
		%s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d OFFSET $%d`, novelSelectColumns, whereSQL, argCount, argCount+1)

	args = append(args, pageSize, offset)
	novels, err := s.repo.ListNovels(ctx, selectQuery, args)
	if err != nil {
		return nil, 0, err
	}
	if novels == nil {
		novels = []Novel{}
	}
	for i := range novels {
		s.applyCoverDisplay(&novels[i])
	}

	return novels, total, nil
}

func (s *novelService) GetNovel(ctx context.Context, id int64) (*Novel, error) {
	n, err := s.repo.GetNovel(ctx, id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, ErrNovelNotFound
	}
	s.applyCoverDisplay(n)
	return n, nil
}

func normalizeAndValidateGenres(genres []string) ([]string, error) {
	cleaned := make([]string, 0, len(genres))
	for _, g := range genres {
		trimmed := strings.TrimSpace(g)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if utf8.RuneCountInString(strings.Join(cleaned, ",")) > maxGenresTotalChars {
		return nil, ErrInvalidGenres
	}
	return cleaned, nil
}

func (s *novelService) CreateNovel(ctx context.Context, n *Novel) (*Novel, error) {
	n.CoverURL = strings.TrimSpace(n.CoverURL)
	if !s.isValidNewCoverRef(n.CoverURL) {
		return nil, ErrInvalidCover
	}
	genres, err := normalizeAndValidateGenres(n.Genres)
	if err != nil {
		return nil, err
	}
	n.Genres = genres
	n.PublishStatus = strings.TrimSpace(n.PublishStatus)
	if n.PublishStatus == "" {
		n.PublishStatus = "draft"
	}
	if !isValidPublishStatus(n.PublishStatus) {
		return nil, ErrInvalidPublishStatus
	}
	if n.PublishStatus == "published" {
		return nil, ErrCannotPublishEmptyNovel
	}

	if n.CoinCostPerThousand != nil && (*n.CoinCostPerThousand < 0 || *n.CoinCostPerThousand > math.MaxInt32) {
		return nil, ErrInvalidNovelPricing
	}
	if n.StartPayChapterIndex <= 0 {
		n.StartPayChapterIndex, err = s.resolveDefaultStartPay(ctx)
		if err != nil {
			return nil, err
		}
	}

	err = s.repo.CreateNovel(ctx, n)
	if err != nil {
		return nil, err
	}
	s.applyCoverDisplay(n)
	return n, nil
}

func defaultBatchNovelGenres() []string {
	return []string{"Contemporary", "Romance", "Cheating"}
}

func validateCleanUTF8Text(val, fieldName string) error {
	if !utf8.ValidString(val) || strings.ContainsRune(val, '\x00') {
		return fmt.Errorf("%s 包含无效编码或非法控制字符（仅支持 UTF-8 文本）", fieldName)
	}
	return nil
}

func (s *novelService) cleanupUnreferencedCover(ctx context.Context, coverRef string, excludeNovelID int64) {
	if s.coverStore == nil || !s.coverStore.IsValidCoverRef(coverRef) {
		return
	}
	if refCount, countErr := s.repo.CountNovelsByCoverURL(ctx, coverRef, excludeNovelID); countErr == nil && refCount == 0 {
		if delErr := s.coverStore.DeleteCover(ctx, coverRef); delErr != nil {
			log.Printf("[WARN] failed to delete unreferenced OSS cover %q: %v", coverRef, delErr)
		}
	}
}

func (s *novelService) importNovelAtomically(ctx context.Context, item BatchImportNovelInput) (*Novel, int, int, error) {
	if item.PreError != "" {
		return nil, 0, 0, errors.New(item.PreError)
	}

	title := strings.TrimSpace(item.Title)
	author := strings.TrimSpace(item.Author)
	if title == "" {
		return nil, 0, 0, errors.New("书名 title 不能为空")
	}
	if err := validateCleanUTF8Text(title, "书名 title"); err != nil {
		return nil, 0, 0, err
	}
	if utf8.RuneCountInString(title) > 150 {
		return nil, 0, 0, errors.New("书名 title 不能超过 150 个字符")
	}
	if author == "" {
		return nil, 0, 0, errors.New("作者 author 不能为空")
	}
	if err := validateCleanUTF8Text(author, "作者 author"); err != nil {
		return nil, 0, 0, err
	}
	if utf8.RuneCountInString(author) > 100 {
		return nil, 0, 0, errors.New("作者 author 不能超过 100 个字符")
	}
	if err := validateCleanUTF8Text(item.Synopsis, "简介 synopsis"); err != nil {
		return nil, 0, 0, err
	}

	status := strings.TrimSpace(item.Status)
	if status == "" {
		status = "Ongoing"
	}
	if status != "Ongoing" && status != "Completed" {
		return nil, 0, 0, errors.New("连载状态 status 仅支持 Ongoing 或 Completed")
	}

	publishStatus := strings.TrimSpace(item.PublishStatus)
	if publishStatus == "" {
		publishStatus = "draft"
	}
	if !isValidPublishStatus(publishStatus) {
		return nil, 0, 0, ErrInvalidPublishStatus
	}

	genres, err := normalizeAndValidateGenres(item.Genres)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(genres) == 0 {
		genres = defaultBatchNovelGenres()
	}

	rating := 4.5
	if item.Rating != nil {
		rating = *item.Rating
		if math.IsNaN(rating) || rating < 0 || rating > 5 {
			return nil, 0, 0, errors.New("评分 rating 必须在 0 到 5 之间")
		}
	}

	if item.CoinCostPerThousand != nil && (*item.CoinCostPerThousand < 0 || *item.CoinCostPerThousand > math.MaxInt32) {
		return nil, 0, 0, ErrInvalidNovelPricing
	}
	if item.StartPayChapterIndex < 0 || item.StartPayChapterIndex > math.MaxInt32 {
		return nil, 0, 0, errors.New("起始收费章节 start_pay_chapter_index 无效")
	}

	importType := strings.TrimSpace(item.ImportType)
	if importType == "" {
		importType = "single_txt"
	}
	if importType != "single_txt" && importType != "zip" {
		return nil, 0, 0, errors.New("import_type 仅支持 single_txt 或 zip")
	}
	if len(item.ChapterBytes) == 0 {
		return nil, 0, 0, fmt.Errorf("%w: 章节文件内容不能为空", ErrInvalidImportFile)
	}
	if importType == "single_txt" && (!utf8.Valid(item.ChapterBytes) || bytes.IndexByte(item.ChapterBytes, 0) >= 0) {
		return nil, 0, 0, fmt.Errorf("%w: 单个 TXT 文件必须使用无 NUL 字符的 UTF-8 编码", ErrInvalidImportFile)
	}

	var parsedChapters []zipFileItem
	if importType == "zip" {
		parsedChapters, err = parseChaptersFromZIP(item.ChapterBytes)
	} else {
		parsedChapters, err = parseChaptersFromSingleTXT(item.ChapterBytes, item.DelimiterRegex)
	}
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%w: %w", ErrInvalidImportFile, err)
	}
	for _, parsed := range parsedChapters {
		if err := validateCleanUTF8Text(parsed.Title, fmt.Sprintf("第 %d 章标题", parsed.Index+1)); err != nil {
			return nil, 0, 0, fmt.Errorf("%w: %w", ErrInvalidImportFile, err)
		}
		if err := validateCleanUTF8Text(parsed.Content, fmt.Sprintf("第 %d 章正文", parsed.Index+1)); err != nil {
			return nil, 0, 0, fmt.Errorf("%w: %w", ErrInvalidImportFile, err)
		}
	}

	n := &Novel{
		Title:                title,
		Author:               author,
		Rating:               rating,
		Status:               status,
		PublishStatus:        publishStatus,
		Synopsis:             item.Synopsis,
		Genres:               genres,
		CoinCostPerThousand:  item.CoinCostPerThousand,
		StartPayChapterIndex: item.StartPayChapterIndex,
	}

	if n.CoinCostPerThousand == nil || n.StartPayChapterIndex <= 0 {
		settings, err := s.repo.GetSettings(ctx)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("read chapter pricing settings: %w", err)
		}
		if _, _, err := importPricing(n, settings); err != nil {
			return nil, 0, 0, err
		}
	}

	coverRef := strings.TrimSpace(item.CoverURL)
	uploadedNewCover := false
	if len(item.CoverBytes) > 0 {
		if coverRef != "" {
			return nil, 0, 0, errors.New("不能同时提供封面图片字节流与 cover_url，请二选一")
		}
		coverKey, _, uploadErr := s.UploadCover(ctx, item.CoverBytes)
		if uploadErr != nil {
			return nil, 0, 0, uploadErr
		}
		coverRef = coverKey
		uploadedNewCover = true
	} else if !s.isValidNewCoverRef(coverRef) {
		return nil, 0, 0, ErrInvalidCover
	}

	committed := false
	defer func() {
		if !committed && uploadedNewCover {
			s.cleanupUnreferencedCover(ctx, coverRef, 0)
		}
	}()

	chapters := make([]Chapter, 0, len(parsedChapters))
	totalWords := 0
	for _, parsed := range parsedChapters {
		words := countCharacters(parsed.Content)
		totalWords += words
		chapters = append(chapters, Chapter{
			ID:           uuid.NewString(),
			ChapterIndex: parsed.Index,
			Title:        parsed.Title,
			Content:      parsed.Content,
			ContentHash:  chapterDigest(parsed.Content),
			WordCount:    words,
		})
	}
	if err := s.stageChapters(ctx, chapters); err != nil {
		return nil, 0, 0, err
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	defer tx.Rollback(ctx)

	if err := s.repo.LockSettings(ctx, tx); err != nil {
		return nil, 0, 0, err
	}

	n.CoverURL = coverRef

	startPay, costPerThousand, err := s.publicationPricing(ctx, tx, n)
	if err != nil {
		return nil, 0, 0, err
	}
	n.StartPayChapterIndex = startPay

	if err := s.repo.CreateNovelTx(ctx, tx, n); err != nil {
		return nil, 0, 0, err
	}

	for i := range chapters {
		ch := &chapters[i]
		ch.NovelID = n.ID
		if err := setChapterPrice(ch, startPay, costPerThousand); err != nil {
			return nil, 0, 0, err
		}
	}
	if err := s.repo.ReplaceChaptersTx(ctx, tx, n.ID, chapters, nil, nil); err != nil {
		return nil, 0, 0, err
	}
	if err := s.repo.SyncNovelWordCountTx(ctx, tx, n.ID); err != nil {
		return nil, 0, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, 0, err
	}
	committed = true

	n.WordCount = totalWords
	s.applyCoverDisplay(n)
	return n, len(chapters), totalWords, nil
}

func (s *novelService) BatchImportNovels(ctx context.Context, items []BatchImportNovelInput) (*BatchImportNovelsResult, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: manifest 不能为空", ErrInvalidBatchImport)
	}
	if len(items) > maxBatchImportNovels {
		return nil, fmt.Errorf("%w: 单次最多批量导入 %d 本书籍", ErrInvalidBatchImport, maxBatchImportNovels)
	}

	result := &BatchImportNovelsResult{
		Total:   len(items),
		Results: make([]BatchImportNovelItemResult, 0, len(items)),
	}
	for i, item := range items {
		created, chaptersCount, totalWords, err := s.importNovelAtomically(ctx, item)
		if err != nil {
			result.FailedCount++
			result.Results = append(result.Results, BatchImportNovelItemResult{
				Index:  i,
				Title:  strings.TrimSpace(item.Title),
				Status: "failed",
				Error:  err.Error(),
			})
			continue
		}
		result.CreatedCount++
		result.Results = append(result.Results, BatchImportNovelItemResult{
			Index:          i,
			Title:          created.Title,
			Status:         "created",
			Novel:          created,
			ChaptersCount:  chaptersCount,
			TotalWordCount: totalWords,
		})
	}
	return result, nil
}

func (s *novelService) UpdateNovel(ctx context.Context, id int64, title, author, coverURL, status, publishStatus, synopsis string, genres []string, rating float64, coinCost *int, startPayIndex int) (*Novel, error) {
	existing, err := s.repo.GetNovel(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrNovelNotFound
	}

	coverURL = strings.TrimSpace(coverURL)
	if coverURL == "" || (coverURL != existing.CoverURL && !s.isValidNewCoverRef(coverURL)) {
		return nil, ErrInvalidCover
	}

	normalizedGenres, err := normalizeAndValidateGenres(genres)
	if err != nil {
		return nil, err
	}
	genres = normalizedGenres

	publishStatus = strings.TrimSpace(publishStatus)
	if publishStatus == "" {
		publishStatus = strings.TrimSpace(existing.PublishStatus)
	}
	if publishStatus == "" {
		publishStatus = "draft"
	}
	if !isValidPublishStatus(publishStatus) {
		return nil, ErrInvalidPublishStatus
	}
	if publishStatus == "published" {
		chCount, countErr := s.repo.CountChapters(ctx, id)
		if countErr != nil {
			return nil, countErr
		}
		if chCount <= 0 {
			return nil, ErrCannotPublishEmptyNovel
		}
	}

	if coinCost != nil && (*coinCost < 0 || *coinCost > math.MaxInt32) {
		return nil, ErrInvalidNovelPricing
	}
	if startPayIndex <= 0 {
		startPayIndex, err = s.resolveDefaultStartPay(ctx)
		if err != nil {
			return nil, err
		}
	}

	n := &Novel{
		ID:                   id,
		Title:                title,
		Author:               author,
		CoverURL:             coverURL,
		Status:               status,
		PublishStatus:        publishStatus,
		Synopsis:             synopsis,
		Genres:               genres,
		Rating:               rating,
		CoinCostPerThousand:  coinCost,
		StartPayChapterIndex: startPayIndex,
	}

	err = s.repo.UpdateNovel(ctx, n)
	if err != nil {
		return nil, err
	}

	// Invalidate reader page cache
	s.invalidateNovelCache(ctx, id)
	s.invalidateAllChaptersCache(ctx, id)

	// Best-effort cleanup of replaced OSS cover when no other active novel shares the hash key.
	if existing.CoverURL != coverURL {
		s.cleanupUnreferencedCover(ctx, existing.CoverURL, id)
	}

	s.applyCoverDisplay(n)
	return n, nil
}

func (s *novelService) UpdatePublishStatus(ctx context.Context, id int64, publishStatus string) (*Novel, error) {
	publishStatus = strings.TrimSpace(publishStatus)
	if !isValidPublishStatus(publishStatus) {
		return nil, ErrInvalidPublishStatus
	}

	existing, err := s.repo.GetNovel(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrNovelNotFound
	}

	if publishStatus == "published" {
		chCount, countErr := s.repo.CountChapters(ctx, id)
		if countErr != nil {
			return nil, countErr
		}
		if chCount <= 0 {
			return nil, ErrCannotPublishEmptyNovel
		}
	}

	updated, err := s.repo.UpdatePublishStatus(ctx, id, publishStatus)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, ErrNovelNotFound
	}

	s.invalidateNovelCache(ctx, id)
	s.invalidateAllChaptersCache(ctx, id)
	s.applyCoverDisplay(updated)
	return updated, nil
}

func (s *novelService) BatchUpdatePublishStatus(ctx context.Context, ids []int64, publishStatus string) (*BatchPublishResult, error) {
	publishStatus = strings.TrimSpace(publishStatus)
	if !isValidPublishStatus(publishStatus) {
		return nil, ErrInvalidPublishStatus
	}

	seen := make(map[int64]struct{}, len(ids))
	dedupIDs := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		dedupIDs = append(dedupIDs, id)
	}
	if len(dedupIDs) == 0 {
		return nil, ErrInvalidBatchNovelIDs
	}
	if len(dedupIDs) > maxBatchPublishIDs {
		return nil, ErrBatchLimitExceeded
	}

	candidates, err := s.repo.GetNovelsWithChapterCounts(ctx, dedupIDs)
	if err != nil {
		return nil, err
	}
	candidateMap := make(map[int64]NovelPublishCandidate, len(candidates))
	for _, item := range candidates {
		candidateMap[item.ID] = item
	}

	eligibleIDs := make([]int64, 0, len(dedupIDs))
	var skipped []BatchPublishSkippedItem
	emptyNovelSkipped := false
	for _, id := range dedupIDs {
		item, ok := candidateMap[id]
		if !ok {
			skipped = append(skipped, BatchPublishSkippedItem{ID: id, Reason: "书籍不存在或已归档"})
			continue
		}
		if publishStatus == "published" && item.ChapterCount <= 0 {
			emptyNovelSkipped = true
			skipped = append(skipped, BatchPublishSkippedItem{ID: id, Title: item.Title, Reason: "暂无章节内容，无法上架"})
			continue
		}
		eligibleIDs = append(eligibleIDs, id)
	}

	if len(eligibleIDs) == 0 {
		if emptyNovelSkipped {
			return nil, ErrCannotPublishEmptyNovel
		}
		return nil, ErrNovelNotFound
	}

	updatedIDs, err := s.repo.BatchUpdatePublishStatus(ctx, eligibleIDs, publishStatus)
	if err != nil {
		return nil, err
	}
	if len(updatedIDs) == 0 {
		return nil, ErrNovelNotFound
	}
	for _, id := range updatedIDs {
		s.invalidateNovelCache(ctx, id)
		s.invalidateAllChaptersCache(ctx, id)
	}

	return &BatchPublishResult{
		PublishStatus: publishStatus,
		UpdatedCount:  len(updatedIDs),
		UpdatedIDs:    updatedIDs,
		Skipped:       skipped,
	}, nil
}

func (s *novelService) DeleteNovel(ctx context.Context, id int64) error {
	affected, err := s.repo.DeleteNovel(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNovelNotFound
	}

	s.invalidateNovelCache(ctx, id)
	s.invalidateAllChaptersCache(ctx, id)

	return nil
}

func (s *novelService) GetChapterIdentities(ctx context.Context, novelID int64) ([]ChapterIdentity, error) {
	n, err := s.repo.GetNovel(ctx, novelID)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, ErrNovelNotFound
	}
	return s.repo.ListChapterIdentities(ctx, novelID)
}

func (s *novelService) ListChapters(ctx context.Context, novelID int64) ([]Chapter, error) {
	n, err := s.repo.GetNovel(ctx, novelID)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, ErrNovelNotFound
	}
	chapters, err := s.repo.ListChapters(ctx, novelID)
	if err != nil {
		return nil, err
	}
	if chapters == nil {
		chapters = []Chapter{}
	}
	return chapters, nil
}

func (s *novelService) GetChapterDetail(ctx context.Context, novelID int64, chapterIndex int) (*Chapter, error) {
	n, err := s.repo.GetNovel(ctx, novelID)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, ErrNovelNotFound
	}

	c, err := s.repo.GetChapter(ctx, novelID, chapterIndex)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrChapterNotFound
	}

	c.Content, err = s.readChapterBody(ctx, c)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (s *novelService) CreateChapter(ctx context.Context, novelID int64, chapterIndex int, title, content string) (*Chapter, error) {
	ch := &Chapter{
		ID: uuid.NewString(), NovelID: novelID, ChapterIndex: chapterIndex,
		Title: title, Content: content,
	}
	if err := s.publishChapter(ctx, ch, false, ""); err != nil {
		return nil, err
	}
	ch.Content = content
	return ch, nil
}

func (s *novelService) UpdateChapter(ctx context.Context, novelID int64, chapterIndex int, title, content string, expectedID string) error {
	ch := &Chapter{
		NovelID: novelID, ChapterIndex: chapterIndex, Title: title, Content: content,
	}
	return s.publishChapter(ctx, ch, true, expectedID)
}

func (s *novelService) publishChapter(ctx context.Context, ch *Chapter, update bool, expectedID string) error {
	n, err := s.repo.GetNovel(ctx, ch.NovelID)
	if err != nil {
		return err
	}
	if n == nil {
		return ErrNovelNotFound
	}
	if update {
		current, err := s.repo.GetChapter(ctx, ch.NovelID, ch.ChapterIndex)
		if err != nil {
			return err
		}
		if current == nil {
			return ErrChapterNotFound
		}
		if expectedID != "" && current.ID != expectedID {
			return ErrPublicationChanged
		}
		ch.ID = current.ID
	}
	ch.ContentHash = chapterDigest(ch.Content)
	ch.WordCount = countCharacters(ch.Content)
	staged := []Chapter{*ch}
	if err := s.stageChapters(ctx, staged); err != nil {
		return err
	}
	*ch = staged[0]

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := s.repo.LockPublication(ctx, tx, n.ID, n.ContentVersion); err != nil {
		return err
	}
	startPay, cost, err := s.publicationPricing(ctx, tx, n)
	if err != nil {
		return err
	}
	if err := setChapterPrice(ch, startPay, cost); err != nil {
		return err
	}
	if update {
		affected, err := s.repo.UpdateChapterTx(ctx, tx, ch)
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrChapterNotFound
		}
	} else if err := s.repo.CreateChapterTx(ctx, tx, ch); err != nil {
		return err
	}
	if err := s.repo.SyncNovelWordCountTx(ctx, tx, n.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type zipFileItem struct {
	Index   int
	Title   string
	Content string
}

const defaultChapterDelimiterRegex = `(?i)(chapter\s+\d+|第[一二三四五六七八九十百千万零两\d]+章)`

func truncateRunes(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}

const maxZIPUncompressedBytes int64 = 200 << 20

func parseChaptersFromZIP(zipData []byte) ([]zipFileItem, error) {
	archive, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, errors.New("Invalid zip archive: " + err.Error())
	}

	// Look for standard naming: "001_Title.txt", "1_Title.txt", or just "001.txt"
	reName := regexp.MustCompile(`(?i)^(\d+)[_\s-]*([^\.]+)?\.txt$`)

	type zipParsedEntry struct {
		sortOrder int
		title     string
		content   string
	}
	var entries []zipParsedEntry
	var totalUncompressed int64

	for _, f := range archive.File {
		if f.FileInfo().IsDir() {
			continue
		}
		baseName := filepath.Base(f.Name)
		matches := reName.FindStringSubmatch(baseName)
		if len(matches) < 2 {
			continue
		}

		var num int
		_, _ = fmt.Sscanf(matches[1], "%d", &num)

		title := strings.TrimSpace(matches[2])
		if title == "" {
			title = fmt.Sprintf("Chapter %d", num)
		}
		title = truncateRunes(title, 150)

		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		contentBytes, err := io.ReadAll(io.LimitReader(rc, (10<<20)+1)) // 10MB limit per chapter TXT
		rc.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(contentBytes)) > 10<<20 {
			return nil, fmt.Errorf("章节文件 %s 超过单章 10MB 上限", baseName)
		}
		totalUncompressed += int64(len(contentBytes))
		if totalUncompressed > maxZIPUncompressedBytes {
			return nil, errors.New("ZIP 解压后章节文本总大小不能超过 200MB")
		}

		contentStr := string(contentBytes)
		contentStr = strings.TrimPrefix(contentStr, "\ufeff")
		contentStr = strings.ReplaceAll(contentStr, "\r\n", "\n")
		contentStr = strings.ReplaceAll(contentStr, "\r", "\n")

		entries = append(entries, zipParsedEntry{
			sortOrder: num,
			title:     title,
			content:   strings.TrimSpace(contentStr),
		})
	}

	if len(entries) == 0 {
		return nil, errors.New("ZIP 压缩包内未找到有效的章节 TXT 文件（示例命名: 001_Chapter.txt）")
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].sortOrder < entries[j].sortOrder
	})

	parsed := make([]zipFileItem, len(entries))
	for i, e := range entries {
		parsed[i] = zipFileItem{
			Index:   i, // 0-based sequential index matching schema/reader conventions
			Title:   e.title,
			Content: e.content,
		}
	}
	return parsed, nil
}

func parseChaptersFromSingleTXT(rawData []byte, delimiterRegex string) ([]zipFileItem, error) {
	pattern := strings.TrimSpace(delimiterRegex)
	if pattern == "" {
		pattern = defaultChapterDelimiterRegex
	}
	reHeader, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDelimiterRegex, err)
	}

	fullContent := string(rawData)
	fullContent = strings.TrimPrefix(fullContent, "\ufeff")
	fullContent = strings.ReplaceAll(fullContent, "\r\n", "\n")
	fullContent = strings.ReplaceAll(fullContent, "\r", "\n")

	lines := strings.Split(fullContent, "\n")
	var parsed []zipFileItem
	var currentTitle string
	var bodyLines []string
	hasStarted := false

	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed != "" {
			loc := reHeader.FindStringIndex(trimmed)
			if loc != nil && loc[0] == 0 && loc[1] > 0 {
				if hasStarted {
					parsed = append(parsed, zipFileItem{
						Index:   len(parsed),
						Title:   currentTitle,
						Content: strings.TrimSpace(strings.Join(bodyLines, "\n")),
					})
				}
				hasStarted = true
				currentTitle = truncateRunes(trimmed, 150)
				bodyLines = bodyLines[:0]
				continue
			}
		}
		if hasStarted {
			bodyLines = append(bodyLines, rawLine)
		}
	}

	if hasStarted {
		parsed = append(parsed, zipFileItem{
			Index:   len(parsed),
			Title:   currentTitle,
			Content: strings.TrimSpace(strings.Join(bodyLines, "\n")),
		})
	}

	if len(parsed) == 0 {
		return nil, errors.New("未匹配到任何章节标题（例如 'Chapter 1' 或 '第一章'），请检查文件内容或分割正则")
	}
	return parsed, nil
}

func (s *novelService) BulkImportChapters(ctx context.Context, novelID int64, zipData []byte, importType string, delimiterRegex string, mappings ...ChapterImportMapping) (*BulkImportResult, error) {
	novel, err := s.repo.GetNovel(ctx, novelID)
	if err != nil {
		return nil, err
	}
	if novel == nil {
		return nil, ErrNovelNotFound
	}

	inheritsPricing := novel.CoinCostPerThousand == nil || novel.StartPayChapterIndex <= 0
	if inheritsPricing {
		configs, err := s.repo.GetSettings(ctx)
		if err != nil {
			return nil, fmt.Errorf("read import pricing settings: %w", err)
		}
		if _, _, err := importPricing(novel, configs); err != nil {
			return nil, err
		}
	}

	var parsedChapters []zipFileItem
	if importType == "zip" {
		parsedChapters, err = parseChaptersFromZIP(zipData)
	} else {
		parsedChapters, err = parseChaptersFromSingleTXT(zipData, delimiterRegex)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidImportFile, err)
	}

	var mapping ChapterImportMapping
	if len(mappings) > 0 {
		mapping = mappings[0]
		if mapping.Version != novel.ContentVersion {
			return nil, ErrPublicationChanged
		}
	}
	identities, err := s.importIdentities(ctx, novelID)
	if err != nil {
		return nil, err
	}
	chapters := make([]Chapter, 0, len(parsedChapters))
	totalWords := 0
	for _, item := range parsedChapters {
		words := countCharacters(item.Content)
		totalWords += words
		chapters = append(chapters, Chapter{
			NovelID: novelID, ChapterIndex: item.Index, Title: item.Title,
			Content: item.Content, ContentHash: chapterDigest(item.Content), WordCount: words,
		})
	}
	retired, splits, err := assignChapterIdentities(chapters, identities, mapping)
	if err != nil {
		return nil, err
	}
	if err := s.stageChapters(ctx, chapters); err != nil {
		return nil, err
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := s.repo.LockPublication(ctx, tx, novelID, novel.ContentVersion); err != nil {
		return nil, err
	}
	startPay, costPerThousand, err := s.publicationPricing(ctx, tx, novel)
	if err != nil {
		return nil, err
	}
	for i := range chapters {
		ch := &chapters[i]
		if err := setChapterPrice(ch, startPay, costPerThousand); err != nil {
			return nil, err
		}
	}
	if err := s.repo.ReplaceChaptersTx(ctx, tx, novelID, chapters, retired, splits); err != nil {
		return nil, err
	}
	if err := s.repo.SyncNovelWordCountTx(ctx, tx, novelID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &BulkImportResult{
		ChaptersCount:  len(parsedChapters),
		TotalWordCount: totalWords,
	}, nil
}

func (s *novelService) GetSettings(ctx context.Context) (map[string]string, error) {
	return s.repo.GetSettings(ctx)
}

func validateSettingInt(val string, minVal int, fieldName string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil || parsed < minVal || parsed > math.MaxInt32 {
		return 0, fmt.Errorf("%w：%s 必须为 %d 到 %d 的整数", ErrInvalidSettingValue, fieldName, minVal, math.MaxInt32)
	}
	return parsed, nil
}

func (s *novelService) UpdateSettings(ctx context.Context, configs map[string]string) error {
	coin, coinUpdated := configs["global_coin_cost_per_thousand"]
	cost := 0
	var err error
	if coinUpdated {
		cost, err = validateSettingInt(coin, 0, "global_coin_cost_per_thousand")
		if err != nil {
			return err
		}
		configs["global_coin_cost_per_thousand"] = strconv.Itoa(cost)
	}
	start, startUpdated := configs["global_start_pay_chapter_index"]
	startPay := 0
	if startUpdated {
		startPay, err = validateSettingInt(start, 1, "global_start_pay_chapter_index")
		if err != nil {
			return err
		}
		configs["global_start_pay_chapter_index"] = strconv.Itoa(startPay)
	}
	scope := func(key string) bool {
		if flag, ok := configs[key]; ok {
			return strings.TrimSpace(flag) == "true"
		}
		return strings.TrimSpace(configs["apply_to_all_novels"]) == "true"
	}
	applyStart := startUpdated && scope("apply_start_pay_to_all_novels")
	resetCost := configs["reset_novel_coin_cost"] == "true"
	if resetCost && coinUpdated && scope("apply_coin_cost_to_all_novels") {
		return ErrInvalidSettingValue
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := s.repo.LockSettings(ctx, tx); err != nil {
		return err
	}
	if (applyStart || resetCost) && !coinUpdated {
		all, err := s.repo.GetSettingsTx(ctx, tx)
		if err != nil {
			return fmt.Errorf("read pricing settings: %w", err)
		}
		cost, err = pricingSetting(all, "global_coin_cost_per_thousand", 0, 500)
		if err != nil {
			return err
		}
	}
	for k, v := range configs {
		switch k {
		case "apply_to_all_novels", "apply_coin_cost_to_all_novels", "apply_start_pay_to_all_novels", "reset_novel_coin_cost":
			continue
		}
		if err := s.repo.UpdateSettingTx(ctx, tx, k, v); err != nil {
			return err
		}
	}
	if coinUpdated || applyStart || resetCost {
		var explicitCost *int
		if coinUpdated && scope("apply_coin_cost_to_all_novels") {
			explicitCost = &cost
		}
		if !applyStart {
			startPay = 0
		}
		if err := s.repo.ApplyPricingSettingsTx(ctx, tx, cost, explicitCost, resetCost, startPay); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func validatePromoPricing(coinCost, startPayIndex *int) error {
	if coinCost != nil && *coinCost < 0 {
		return fmt.Errorf("%w: 千字单价不能为负数", ErrInvalidPromoPricing)
	}
	if startPayIndex != nil && *startPayIndex <= 0 {
		return fmt.Errorf("%w: 起付章节必须大于等于 1", ErrInvalidPromoPricing)
	}
	return nil
}

func appendLinkIDToURL(rawURL string, linkID int) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return rawURL
	}
	q := u.Query()
	q.Set("link_id", strconv.Itoa(linkID))
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *novelService) ListPromotionLinks(ctx context.Context) ([]PromotionLink, error) {
	return s.repo.ListPromotionLinks(ctx)
}

func (s *novelService) CreatePromotionLink(ctx context.Context, link *PromotionLink) (*PromotionLink, error) {
	if err := validatePromoPricing(link.CoinCostPerThousand, link.StartPayChapterIndex); err != nil {
		return nil, err
	}
	if link.NovelID < 0 {
		return nil, ErrNovelNotFound
	}
	if link.NovelID > 0 {
		n, err := s.repo.GetNovel(ctx, link.NovelID)
		if err != nil {
			return nil, err
		}
		if n == nil {
			return nil, ErrNovelNotFound
		}
	}

	id, err := s.repo.CreatePromotionLink(ctx, link)
	if err != nil {
		return nil, err
	}
	link.ID = id
	resolvedURL := appendLinkIDToURL(link.GeneratedURL, id)
	if resolvedURL != link.GeneratedURL {
		if err := s.repo.UpdatePromotionLinkURL(ctx, id, resolvedURL); err != nil {
			return nil, err
		}
		link.GeneratedURL = resolvedURL
	}
	return link, nil
}

func (s *novelService) UpdatePromotionLink(ctx context.Context, id int, name string, chapterIndex int, source, campaign, url string, pixelID, templateID, domainID, coinCost, startPayIndex *int, domain string) error {
	if err := validatePromoPricing(coinCost, startPayIndex); err != nil {
		return err
	}
	return s.repo.UpdatePromotionLink(ctx, id, name, chapterIndex, source, campaign, url, pixelID, templateID, domainID, coinCost, startPayIndex, domain)
}

func (s *novelService) DeletePromotionLink(ctx context.Context, id int) error {
	return s.repo.DeletePromotionLink(ctx, id)
}

func (s *novelService) ListFBPixels(ctx context.Context) ([]FBPixel, error) {
	return s.repo.ListFBPixels(ctx)
}

func (s *novelService) CreateFBPixel(ctx context.Context, pixel *FBPixel) (*FBPixel, error) {
	id, err := s.repo.CreateFBPixel(ctx, pixel)
	if err != nil {
		return nil, err
	}
	pixel.ID = id
	return pixel, nil
}

func (s *novelService) UpdateFBPixel(ctx context.Context, id int, name, pixelID, token string) error {
	return s.repo.UpdateFBPixel(ctx, id, name, pixelID, token)
}

func (s *novelService) DeleteFBPixel(ctx context.Context, id int) error {
	return s.repo.DeleteFBPixel(ctx, id)
}

func countCharacters(s string) int {
	count := 0
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '\u3000' {
			continue
		}
		count++
	}
	return count
}

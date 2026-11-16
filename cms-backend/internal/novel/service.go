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
	"unicode/utf8"

	"github.com/google/uuid"
	redisclient "star-novel-cms/internal/redis"
	"star-novel-cms/internal/storage"
)

const maxGenresTotalChars = 2048

var (
	ErrNovelNotFound           = errors.New("Novel not found")
	ErrChapterNotFound         = errors.New("Chapter not found")
	ErrInvalidCover            = errors.New("请先上传书籍封面图片，不允许直接填写外部 URL")
	ErrInvalidDelimiterRegex   = errors.New("无效的章节分割正则表达式")
	ErrInvalidPublishStatus    = errors.New("无效的上架状态，仅支持 draft、published 或 offline")
	ErrCannotPublishEmptyNovel = errors.New("书籍暂无章节内容，请先导入章节后再上架发布")
	ErrInvalidSettingValue     = errors.New("无效的全局配置参数")
	ErrInvalidPromoPricing     = errors.New("无效的推广链接计费参数")
	ErrInvalidGenres           = errors.New("分类标签总长度不能超过 2048 个字符")
)

type BulkImportResult struct {
	ChaptersCount  int `json:"chapters_count"`
	TotalWordCount int `json:"total_word_count"`
}

type NovelService interface {
	ListNovels(ctx context.Context, page, pageSize int, status, publishStatus, genre, search string) ([]Novel, int, error)
	GetNovel(ctx context.Context, id int64) (*Novel, error)
	CreateNovel(ctx context.Context, n *Novel) (*Novel, error)
	UpdateNovel(ctx context.Context, id int64, title, author, coverURL, status, publishStatus, synopsis string, genres []string, rating float64, coinCost *int, startPayIndex int) (*Novel, error)
	UpdatePublishStatus(ctx context.Context, id int64, publishStatus string) (*Novel, error)
	DeleteNovel(ctx context.Context, id int64) error
	UploadCover(ctx context.Context, data []byte) (string, string, error)
	MaxCoverUploadBytes() int64

	ListChapters(ctx context.Context, novelID int64) ([]Chapter, error)
	GetChapterDetail(ctx context.Context, novelID int64, chapterIndex int) (*Chapter, error)
	CreateChapter(ctx context.Context, novelID int64, chapterIndex int, title, content string, isPaid bool, price int) (*Chapter, error)
	UpdateChapter(ctx context.Context, novelID int64, chapterIndex int, title, content string, isPaid bool, price int) error
	BulkImportChapters(ctx context.Context, novelID int64, zipData []byte, importType string, delimiterRegex string) (*BulkImportResult, error)

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

func (s *novelService) resolveDefaultStartPay(ctx context.Context) int {
	if configs, err := s.repo.GetSettings(ctx); err == nil {
		if val, ok := configs["global_start_pay_chapter_index"]; ok {
			var parsed int
			if _, scanErr := fmt.Sscanf(val, "%d", &parsed); scanErr == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 3
}

func (s *novelService) ListNovels(ctx context.Context, page, pageSize int, status, publishStatus, genre, search string) ([]Novel, int, error) {
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

	if status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", argCount))
		args = append(args, status)
		argCount++
	} else {
		whereClauses = append(whereClauses, "status != 'archived'")
	}

	if publishStatus != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("publish_status = $%d", argCount))
		args = append(args, publishStatus)
		argCount++
	}

	if genre != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("$%d = ANY(genres)", argCount))
		args = append(args, genre)
		argCount++
	}

	if search != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("(title ILIKE $%d OR author ILIKE $%d OR CAST(id AS TEXT) = $%d)", argCount, argCount, argCount+1))
		args = append(args, "%"+search+"%", strings.TrimSpace(search))
		argCount += 2
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

	if n.CoinCostPerThousand != nil && *n.CoinCostPerThousand <= 0 {
		n.CoinCostPerThousand = nil
	}
	if n.StartPayChapterIndex <= 0 {
		n.StartPayChapterIndex = s.resolveDefaultStartPay(ctx)
	}

	err = s.repo.CreateNovel(ctx, n)
	if err != nil {
		return nil, err
	}
	s.applyCoverDisplay(n)
	return n, nil
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

	if coinCost != nil && *coinCost <= 0 {
		coinCost = nil
	}
	if startPayIndex <= 0 {
		startPayIndex = s.resolveDefaultStartPay(ctx)
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

	// Update all chapters of this novel to reflect new pay rules
	if err := s.repo.UpdateChapterPrices(ctx, startPayIndex, coinCost, id); err != nil {
		return nil, err
	}

	// Invalidate reader page cache
	s.invalidateNovelCache(ctx, id)
	s.invalidateAllChaptersCache(ctx, id)

	// Best-effort cleanup of replaced OSS cover when no other active novel shares the hash key.
	if existing.CoverURL != coverURL && s.coverStore != nil && s.coverStore.IsValidCoverRef(existing.CoverURL) {
		if refCount, countErr := s.repo.CountNovelsByCoverURL(ctx, existing.CoverURL, id); countErr == nil && refCount == 0 {
			if delErr := s.coverStore.DeleteCover(ctx, existing.CoverURL); delErr != nil {
				log.Printf("[WARN] failed to delete replaced OSS cover %q for novel %d: %v", existing.CoverURL, id, delErr)
			}
		}
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

	if s.storage != nil {
		if err := s.storage.DeleteNovelContent(ctx, id); err != nil {
			log.Printf("[WARN] failed to clean chapter content in storage for deleted novel %d: %v", id, err)
		}
	}

	return nil
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

	if s.storage != nil && (c.Content == "" || s.storage.StorageType() == "oss") {
		content, err := s.storage.GetContent(ctx, novelID, chapterIndex)
		if err == nil && content != "" {
			c.Content = content
		}
	}

	return c, nil
}

func (s *novelService) CreateChapter(ctx context.Context, novelID int64, chapterIndex int, title, content string, isPaid bool, price int) (*Chapter, error) {
	n, err := s.repo.GetNovel(ctx, novelID)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, ErrNovelNotFound
	}

	dbContent := content
	if s.storage != nil && s.storage.StorageType() == "oss" {
		dbContent = ""
	}

	c := &Chapter{
		ID:           strings.ReplaceAll(uuid.New().String(), "-", ""),
		NovelID:      novelID,
		ChapterIndex: chapterIndex,
		Title:        title,
		Content:      dbContent,
		WordCount:    countCharacters(content),
		IsPaid:       isPaid,
		Price:        price,
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := s.repo.CreateChapterTx(ctx, tx, c); err != nil {
		return nil, err
	}
	if err := s.repo.SyncNovelWordCountTx(ctx, tx, novelID); err != nil {
		return nil, err
	}

	if s.storage != nil && s.storage.StorageType() == "oss" {
		if err := s.storage.PutContent(ctx, novelID, chapterIndex, content); err != nil {
			return nil, fmt.Errorf("failed to store chapter content to OSS: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	c.Content = content
	s.invalidateNovelCache(ctx, novelID)
	s.invalidateChapterCache(ctx, novelID, chapterIndex)
	return c, nil
}

func (s *novelService) UpdateChapter(ctx context.Context, novelID int64, chapterIndex int, title, content string, isPaid bool, price int) error {
	n, err := s.repo.GetNovel(ctx, novelID)
	if err != nil {
		return err
	}
	if n == nil {
		return ErrNovelNotFound
	}

	dbContent := content
	if s.storage != nil && s.storage.StorageType() == "oss" {
		dbContent = ""
	}

	c := &Chapter{
		NovelID:      novelID,
		ChapterIndex: chapterIndex,
		Title:        title,
		Content:      dbContent,
		WordCount:    countCharacters(content),
		IsPaid:       isPaid,
		Price:        price,
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	affected, err := s.repo.UpdateChapterTx(ctx, tx, c)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrChapterNotFound
	}
	if err := s.repo.SyncNovelWordCountTx(ctx, tx, novelID); err != nil {
		return err
	}

	if s.storage != nil && s.storage.StorageType() == "oss" {
		if err := s.storage.PutContent(ctx, novelID, chapterIndex, content); err != nil {
			return fmt.Errorf("failed to update chapter content in OSS: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.invalidateNovelCache(ctx, novelID)
	s.invalidateChapterCache(ctx, novelID, chapterIndex)
	return nil
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

func (s *novelService) BulkImportChapters(ctx context.Context, novelID int64, zipData []byte, importType string, delimiterRegex string) (*BulkImportResult, error) {
	novel, err := s.repo.GetNovel(ctx, novelID)
	if err != nil || novel == nil {
		return nil, ErrNovelNotFound
	}

	var configs map[string]string
	if novel.CoinCostPerThousand == nil || novel.StartPayChapterIndex <= 0 {
		configs, _ = s.repo.GetSettings(ctx)
	}

	costPerThousand := 500
	if novel.CoinCostPerThousand != nil {
		costPerThousand = *novel.CoinCostPerThousand
	} else if val, ok := configs["global_coin_cost_per_thousand"]; ok {
		_, _ = fmt.Sscanf(val, "%d", &costPerThousand)
	}

	startPay := novel.StartPayChapterIndex
	if startPay <= 0 {
		if val, ok := configs["global_start_pay_chapter_index"]; ok {
			_, _ = fmt.Sscanf(val, "%d", &startPay)
		}
		if startPay <= 0 {
			startPay = 3
		}
	}

	var parsedChapters []zipFileItem
	if importType == "zip" {
		parsedChapters, err = parseChaptersFromZIP(zipData)
	} else {
		parsedChapters, err = parseChaptersFromSingleTXT(zipData, delimiterRegex)
	}
	if err != nil {
		return nil, err
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Clean existing chapters first
	err = s.repo.DeleteChaptersByNovelIDTx(ctx, tx, novelID)
	if err != nil {
		return nil, err
	}

	// Insert parsed chapters
	var totalWords int = 0
	var storageItems []storage.ChapterContentItem

	for _, item := range parsedChapters {
		words := countCharacters(item.Content)
		totalWords += words
		isPaid := item.Index >= (startPay - 1)
		price := 0
		if isPaid {
			price = int(math.Round((float64(words) / 1000.0) * float64(costPerThousand)))
		}

		dbContent := item.Content
		if s.storage != nil && s.storage.StorageType() == "oss" {
			dbContent = ""
		}

		ch := &Chapter{
			ID:           strings.ReplaceAll(uuid.New().String(), "-", ""),
			NovelID:      novelID,
			ChapterIndex: item.Index,
			Title:        item.Title,
			Content:      dbContent,
			WordCount:    words,
			IsPaid:       isPaid,
			Price:        price,
		}

		err = s.repo.CreateChapterTx(ctx, tx, ch)
		if err != nil {
			return nil, err
		}

		storageItems = append(storageItems, storage.ChapterContentItem{
			ChapterIndex: item.Index,
			Content:      item.Content,
		})
	}

	// Update word count in novel
	err = s.repo.SyncNovelWordCountTx(ctx, tx, novelID)
	if err != nil {
		return nil, err
	}

	// Upload chapter contents to OSS before committing DB transaction so any OSS failure rolls back DB cleanly.
	if s.storage != nil && s.storage.StorageType() == "oss" {
		if err := s.storage.BatchPutContent(ctx, novelID, storageItems); err != nil {
			return nil, fmt.Errorf("failed to upload chapter contents to storage: %w", err)
		}
	}

	err = tx.Commit(ctx)
	if err != nil {
		return nil, err
	}

	// Remove stale chapter objects from a previously larger import (non-fatal cleanup)
	if s.storage != nil && s.storage.StorageType() == "oss" {
		if err := s.storage.DeleteNovelContentFrom(ctx, novelID, len(parsedChapters)); err != nil {
			log.Printf("[WARN] failed to delete stale OSS chapters for novel %d from index %d: %v", novelID, len(parsedChapters), err)
		}
	}

	// Invalidate reader page cache
	s.invalidateNovelCache(ctx, novelID)
	s.invalidateAllChaptersCache(ctx, novelID)

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
	if err != nil || parsed < minVal {
		return 0, fmt.Errorf("%w: %s 必须为 >= %d 的整数", ErrInvalidSettingValue, fieldName, minVal)
	}
	return parsed, nil
}

func (s *novelService) UpdateSettings(ctx context.Context, configs map[string]string) error {
	if val, ok := configs["global_coin_cost_per_thousand"]; ok {
		normalized, err := validateSettingInt(val, 0, "global_coin_cost_per_thousand")
		if err != nil {
			return err
		}
		configs["global_coin_cost_per_thousand"] = strconv.Itoa(normalized)
	}
	if val, ok := configs["global_start_pay_chapter_index"]; ok {
		normalized, err := validateSettingInt(val, 1, "global_start_pay_chapter_index")
		if err != nil {
			return err
		}
		configs["global_start_pay_chapter_index"] = strconv.Itoa(normalized)
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for k, v := range configs {
		if k == "apply_to_all_novels" {
			continue
		}
		err := s.repo.UpdateSettingTx(ctx, tx, k, v)
		if err != nil {
			return err
		}
	}

	err = tx.Commit(ctx)
	if err != nil {
		return err
	}

	startPay := 3
	if val, ok := configs["global_start_pay_chapter_index"]; ok {
		startPay, _ = strconv.Atoi(val)
	} else {
		allSettings, _ := s.repo.GetSettings(ctx)
		if dbVal, ok := allSettings["global_start_pay_chapter_index"]; ok {
			if parsed, convErr := strconv.Atoi(strings.TrimSpace(dbVal)); convErr == nil && parsed > 0 {
				startPay = parsed
			}
		}
	}

	costPerThousand := 500
	if val, ok := configs["global_coin_cost_per_thousand"]; ok {
		costPerThousand, _ = strconv.Atoi(val)
	} else {
		allSettings, _ := s.repo.GetSettings(ctx)
		if dbVal, ok := allSettings["global_coin_cost_per_thousand"]; ok {
			if parsed, convErr := strconv.Atoi(strings.TrimSpace(dbVal)); convErr == nil && parsed >= 0 {
				costPerThousand = parsed
			}
		}
	}

	if configs["apply_to_all_novels"] != "false" {
		if startPay > 0 && costPerThousand >= 0 {
			if err := s.repo.BatchUpdateAllNovelChapterPrices(ctx, startPay, costPerThousand); err != nil {
				return err
			}
		}
	} else if costPerThousand >= 0 {
		if err := s.repo.BatchUpdateInheritedNovelChapterPrices(ctx, costPerThousand); err != nil {
			return err
		}
	}

	// Invalidate Redis Cache safely using SCAN iterator
	if redisclient.RDB != nil {
		iter := redisclient.RDB.Scan(ctx, 0, "novel:*", 100).Iterator()
		for iter.Next(ctx) {
			redisclient.RDB.Del(ctx, iter.Val())
		}
	}

	return nil
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

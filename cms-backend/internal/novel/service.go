package novel

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	redisclient "star-novel-cms/internal/redis"
	"star-novel-cms/internal/storage"
)

var (
	ErrNovelNotFound   = errors.New("Novel not found")
	ErrChapterNotFound = errors.New("Chapter not found")
)

type NovelService interface {
	ListNovels(ctx context.Context, page, pageSize int, status, genre, search string) ([]Novel, int, error)
	GetNovel(ctx context.Context, id int64) (*Novel, error)
	CreateNovel(ctx context.Context, n *Novel) (*Novel, error)
	UpdateNovel(ctx context.Context, id int64, title, author, coverURL, status, synopsis string, genres []string, rating float64, coinCost *int, startPayIndex int) (*Novel, error)
	DeleteNovel(ctx context.Context, id int64) error

	ListChapters(ctx context.Context, novelID int64) ([]Chapter, error)
	GetChapterDetail(ctx context.Context, novelID int64, chapterIndex int) (*Chapter, error)
	CreateChapter(ctx context.Context, novelID int64, chapterIndex int, title, content string, isPaid bool, price int) (*Chapter, error)
	UpdateChapter(ctx context.Context, novelID int64, chapterIndex int, title, content string, isPaid bool, price int) error
	BulkImportChapters(ctx context.Context, novelID int64, zipData []byte, importType string) error

	GetSettings(ctx context.Context) (map[string]string, error)
	UpdateSettings(ctx context.Context, configs map[string]string) error

	ListPromotionLinks(ctx context.Context) ([]PromotionLink, error)
	CreatePromotionLink(ctx context.Context, link *PromotionLink) (*PromotionLink, error)
	UpdatePromotionLink(ctx context.Context, id int, name string, chapterIndex int, source, campaign, url string, pixelID, templateID, coinCost, startPayIndex *int) error
	DeletePromotionLink(ctx context.Context, id int) error

	ListFBPixels(ctx context.Context) ([]FBPixel, error)
	CreateFBPixel(ctx context.Context, pixel *FBPixel) (*FBPixel, error)
	UpdateFBPixel(ctx context.Context, id int, name, pixelID, token string) error
	DeleteFBPixel(ctx context.Context, id int) error
}

type novelService struct {
	repo    NovelRepository
	storage storage.ContentStorage
}

func NewNovelService(repo NovelRepository, storage storage.ContentStorage) NovelService {
	return &novelService{
		repo:    repo,
		storage: storage,
	}
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

func (s *novelService) ListNovels(ctx context.Context, page, pageSize int, status, genre, search string) ([]Novel, int, error) {
	offset := (page - 1) * pageSize

	var whereClauses []string
	var args []interface{}
	argCount := 1

	if status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", argCount))
		args = append(args, status)
		argCount++
	}

	if genre != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("$%d = ANY(genres)", argCount))
		args = append(args, genre)
		argCount++
	}

	if search != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("(title ILIKE $%d OR author ILIKE $%d)", argCount, argCount))
		args = append(args, "%"+search+"%")
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
		SELECT id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, COALESCE(coin_cost_per_thousand, (SELECT value::integer FROM system_configs WHERE key = 'global_coin_cost_per_thousand'), 5) AS coin_cost_per_thousand, start_pay_chapter_index, created_at
		FROM novels
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, whereSQL, argCount, argCount+1)

	args = append(args, pageSize, offset)
	novels, err := s.repo.ListNovels(ctx, selectQuery, args)
	if err != nil {
		return nil, 0, err
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
	return n, nil
}

func (s *novelService) CreateNovel(ctx context.Context, n *Novel) (*Novel, error) {
	if n.CoinCostPerThousand != nil && *n.CoinCostPerThousand <= 0 {
		n.CoinCostPerThousand = nil
	}
	if n.StartPayChapterIndex < 0 {
		n.StartPayChapterIndex = 10
	}

	err := s.repo.CreateNovel(ctx, n)
	if err != nil {
		return nil, err
	}
	return n, nil
}

func (s *novelService) UpdateNovel(ctx context.Context, id int64, title, author, coverURL, status, synopsis string, genres []string, rating float64, coinCost *int, startPayIndex int) (*Novel, error) {
	if coinCost != nil && *coinCost <= 0 {
		coinCost = nil
	}
	if startPayIndex < 0 {
		startPayIndex = 10
	}

	n := &Novel{
		ID:                   id,
		Title:                title,
		Author:               author,
		CoverURL:             coverURL,
		Status:               status,
		Synopsis:             synopsis,
		Genres:               genres,
		Rating:               rating,
		CoinCostPerThousand:  coinCost,
		StartPayChapterIndex: startPayIndex,
	}

	err := s.repo.UpdateNovel(ctx, n)
	if err != nil {
		return nil, err
	}

	// Update all chapters of this novel to reflect new pay rules
	_ = s.repo.UpdateChapterPrices(ctx, startPayIndex, coinCost, id)

	// Invalidate reader page cache
	s.invalidateNovelCache(ctx, id)
	s.invalidateAllChaptersCache(ctx, id)

	return n, nil
}

func (s *novelService) DeleteNovel(ctx context.Context, id int64) error {
	affected, err := s.repo.DeleteNovel(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNovelNotFound
	}

	if s.storage != nil {
		_ = s.storage.DeleteNovelContent(ctx, id)
	}

	s.invalidateNovelCache(ctx, id)
	s.invalidateAllChaptersCache(ctx, id)
	return nil
}

func (s *novelService) ListChapters(ctx context.Context, novelID int64) ([]Chapter, error) {
	return s.repo.ListChapters(ctx, novelID)
}

func (s *novelService) GetChapterDetail(ctx context.Context, novelID int64, chapterIndex int) (*Chapter, error) {
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

	err := s.repo.CreateChapter(ctx, c)
	if err != nil {
		return nil, err
	}

	if s.storage != nil && s.storage.StorageType() == "oss" {
		if err := s.storage.PutContent(ctx, novelID, chapterIndex, content); err != nil {
			return nil, fmt.Errorf("failed to store chapter content to OSS: %w", err)
		}
	}

	c.Content = content
	s.invalidateNovelCache(ctx, novelID)
	return c, nil
}

func (s *novelService) UpdateChapter(ctx context.Context, novelID int64, chapterIndex int, title, content string, isPaid bool, price int) error {
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

	err := s.repo.UpdateChapter(ctx, c)
	if err != nil {
		return err
	}

	if s.storage != nil && s.storage.StorageType() == "oss" {
		if err := s.storage.PutContent(ctx, novelID, chapterIndex, content); err != nil {
			return fmt.Errorf("failed to update chapter content in OSS: %w", err)
		}
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

func (s *novelService) BulkImportChapters(ctx context.Context, novelID int64, zipData []byte, importType string) error {
	novel, err := s.repo.GetNovel(ctx, novelID)
	if err != nil || novel == nil {
		return ErrNovelNotFound
	}

	var costPerThousand int = 5
	if novel.CoinCostPerThousand != nil {
		costPerThousand = *novel.CoinCostPerThousand
	} else {
		// Fallback to system configs
		configs, _ := s.repo.GetSettings(ctx)
		if val, ok := configs["global_coin_cost_per_thousand"]; ok {
			if valInt, err := fmt.Sscanf(val, "%d", &costPerThousand); err == nil && valInt > 0 {
				// OK
			}
		}
	}

	var parsedChapters []zipFileItem

	if importType == "zip" {
		archive, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
		if err != nil {
			return errors.New("Invalid zip archive: " + err.Error())
		}

		// Look for standard naming: "001_Title.txt", "1_Title.txt", or just "001.txt"
		reName := regexp.MustCompile(`^(\d+)[_\s-]*([^\.]+)?\.txt$`)

		for _, f := range archive.File {
			if f.FileInfo().IsDir() {
				continue
			}
			baseName := filepath.Base(f.Name)
			matches := reName.FindStringSubmatch(baseName)
			if len(matches) < 2 {
				continue
			}

			var idx int
			_, _ = fmt.Sscanf(matches[1], "%d", &idx)

			title := strings.TrimSpace(matches[2])
			if title == "" {
				title = fmt.Sprintf("Chapter %d", idx)
			}

			rc, err := f.Open()
			if err != nil {
				return err
			}
			contentBytes, err := io.ReadAll(io.LimitReader(rc, 10<<20)) // 10MB limit per chapter TXT
			rc.Close()
			if err != nil {
				return err
			}

			contentStr := string(contentBytes)
			// clean encoding BOM, fix returns
			contentStr = strings.TrimPrefix(contentStr, "\ufeff")
			contentStr = strings.ReplaceAll(contentStr, "\r\n", "\n")

			parsedChapters = append(parsedChapters, zipFileItem{
				Index:   idx,
				Title:   title,
				Content: contentStr,
			})
		}

		if len(parsedChapters) == 0 {
			return errors.New("No valid chapter TXT files found in ZIP (name format like '001_Chapter.txt')")
		}

		// Sort chapters by index
		sort.Slice(parsedChapters, func(i, j int) bool {
			return parsedChapters[i].Index < parsedChapters[j].Index
		})

	} else {
		// single_txt format
		fullContent := string(zipData)
		fullContent = strings.TrimPrefix(fullContent, "\ufeff")
		fullContent = strings.ReplaceAll(fullContent, "\r\n", "\n")

		// Regex to find chapter headers
		reHeader := regexp.MustCompile(`(?m)^[ \t]*(Chapter\s+\d+|第[一二三四五六七八九十百千万\d]+章)[ \t]*(.*)$`)
		matches := reHeader.FindAllStringSubmatchIndex(fullContent, -1)

		if len(matches) == 0 {
			return errors.New("Could not find any chapter headings (e.g. 'Chapter 1' or '第一章')")
		}

		for i := 0; i < len(matches); i++ {
			currMatch := matches[i]
			startContent := currMatch[1]
			endContent := len(fullContent)
			if i+1 < len(matches) {
				endContent = matches[i+1][0]
			}

			heading := fullContent[currMatch[0]:currMatch[1]]
			headingParts := strings.Fields(heading)
			var idx int
			if len(headingParts) >= 2 {
				// Parse first numeric token
				reNum := regexp.MustCompile(`\d+`)
				numStr := reNum.FindString(heading)
				if numStr != "" {
					_, _ = fmt.Sscanf(numStr, "%d", &idx)
				} else {
					idx = i + 1
				}
			} else {
				idx = i + 1
			}

			title := strings.TrimSpace(fullContent[currMatch[2]:currMatch[3]])
			content := strings.TrimSpace(fullContent[startContent:endContent])

			parsedChapters = append(parsedChapters, zipFileItem{
				Index:   idx,
				Title:   title,
				Content: content,
			})
		}
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Clean existing chapters first
	err = s.repo.DeleteChaptersByNovelIDTx(ctx, tx, novelID)
	if err != nil {
		return err
	}

	// Insert parsed chapters
	var totalWords int = 0
	var storageItems []storage.ChapterContentItem

	for _, item := range parsedChapters {
		words := countCharacters(item.Content)
		totalWords += words
		isPaid := item.Index >= novel.StartPayChapterIndex
		price := 0
		if isPaid {
			price = int(float64(words) / 1000.0 * float64(costPerThousand))
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
			return err
		}

		storageItems = append(storageItems, storage.ChapterContentItem{
			ChapterIndex: item.Index,
			Content:      item.Content,
		})
	}

	// Update word count in novel
	_, err = tx.Exec(ctx, "UPDATE novels SET word_count = $1 WHERE id = $2", totalWords, novelID)
	if err != nil {
		return err
	}

	err = tx.Commit(ctx)
	if err != nil {
		return err
	}

	// Write chapter contents to storage (in OSS mode, this cleans previous objects and executes concurrent uploads)
	if s.storage != nil && s.storage.StorageType() == "oss" {
		_ = s.storage.DeleteNovelContent(ctx, novelID)
		if err := s.storage.BatchPutContent(ctx, novelID, storageItems); err != nil {
			return fmt.Errorf("failed to upload chapter contents to storage: %w", err)
		}
	}

	// Invalidate reader page cache
	s.invalidateNovelCache(ctx, novelID)
	s.invalidateAllChaptersCache(ctx, novelID)

	return nil
}

func (s *novelService) GetSettings(ctx context.Context) (map[string]string, error) {
	return s.repo.GetSettings(ctx)
}

func (s *novelService) UpdateSettings(ctx context.Context, configs map[string]string) error {
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

	// Always update all novels and chapter prices unless explicitly requested not to
	if configs["apply_to_all_novels"] != "false" {
		startPay := 3
		if val, ok := configs["global_start_pay_chapter_index"]; ok {
			_, _ = fmt.Sscanf(val, "%d", &startPay)
		} else {
			// Read from db if not in request
			allSettings, _ := s.repo.GetSettings(ctx)
			if dbVal, ok := allSettings["global_start_pay_chapter_index"]; ok {
				_, _ = fmt.Sscanf(dbVal, "%d", &startPay)
			}
		}

		costPerThousand := 5
		if val, ok := configs["global_coin_cost_per_thousand"]; ok {
			_, _ = fmt.Sscanf(val, "%d", &costPerThousand)
		} else {
			allSettings, _ := s.repo.GetSettings(ctx)
			if dbVal, ok := allSettings["global_coin_cost_per_thousand"]; ok {
				_, _ = fmt.Sscanf(dbVal, "%d", &costPerThousand)
			}
		}

		if startPay > 0 && costPerThousand >= 0 {
			_ = s.repo.BatchUpdateAllNovelChapterPrices(ctx, startPay, costPerThousand)
		}
	}

	// Invalidate Redis Cache safely using SCAN iterator
	if redisclient.RDB != nil {
		iter := redisclient.RDB.Scan(ctx, 0, "novel:*", 100).Iterator()
		for iter.Next(ctx) {
			redisclient.RDB.Del(ctx, iter.Val())
		}
		iterCh := redisclient.RDB.Scan(ctx, 0, "chapter:*", 100).Iterator()
		for iterCh.Next(ctx) {
			redisclient.RDB.Del(ctx, iterCh.Val())
		}
		iterSf := redisclient.RDB.Scan(ctx, 0, "sf:*", 100).Iterator()
		for iterSf.Next(ctx) {
			redisclient.RDB.Del(ctx, iterSf.Val())
		}
	}

	return nil
}

func (s *novelService) ListPromotionLinks(ctx context.Context) ([]PromotionLink, error) {
	return s.repo.ListPromotionLinks(ctx)
}

func (s *novelService) CreatePromotionLink(ctx context.Context, link *PromotionLink) (*PromotionLink, error) {
	id, err := s.repo.CreatePromotionLink(ctx, link)
	if err != nil {
		return nil, err
	}
	link.ID = id
	return link, nil
}

func (s *novelService) UpdatePromotionLink(ctx context.Context, id int, name string, chapterIndex int, source, campaign, url string, pixelID, templateID, coinCost, startPayIndex *int) error {
	return s.repo.UpdatePromotionLink(ctx, id, name, chapterIndex, source, campaign, url, pixelID, templateID, coinCost, startPayIndex)
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

package novel

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"
	"star-novel-cms/internal/storage"
)

var (
	ErrPublicationChanged = errors.New("书籍已更新，请刷新后重试")
	ErrChapterMapping     = errors.New("请确认章节映射")
)

type ChapterImportMapping struct {
	Version   int64          `json:"version"`
	Keep      map[int]string `json:"keep"` // New 1-based chapter number -> existing ID.
	Retire    []string       `json:"retire"`
	SplitFrom map[int]string `json:"split_from"` // New 1-based number -> prior purchased chapter.
}

type revisionStorage interface {
	ReadChapterRevision(context.Context, string, string) (string, error)
	ReadLegacyChapter(context.Context, int64, int) (string, error)
	WriteChapterRevision(context.Context, string, string, string) error
}

func chapterDigest(content string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
}

func (s *novelService) stageChapters(ctx context.Context, chapters []Chapter) error {
	for _, ch := range chapters {
		if !utf8.ValidString(ch.Content) || strings.ContainsRune(ch.Content, 0) {
			return fmt.Errorf("%w：第 %d 章正文必须为有效 UTF-8 文本", ErrInvalidImportFile, ch.ChapterIndex+1)
		}
	}
	if s.storage == nil || s.storage.StorageType() != "oss" {
		return nil
	}
	store, ok := s.storage.(revisionStorage)
	if !ok {
		return errors.New("chapter storage does not support immutable objects")
	}
	for i := range chapters {
		ch := &chapters[i]
		ch.ObjectKey = fmt.Sprintf("revisions/%s/%s-%s.txt", ch.ID, ch.ContentHash, uuid.NewString())
	}
	if err := s.repo.StageChapterObjects(ctx, chapters); err != nil {
		return fmt.Errorf("stage chapter objects: %w", err)
	}
	uploadCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	group, uploadCtx := errgroup.WithContext(uploadCtx)
	group.SetLimit(10)
	for i := range chapters {
		ch := &chapters[i]
		group.Go(func() error {
			if err := uploadCtx.Err(); err != nil {
				return err
			}
			return store.WriteChapterRevision(uploadCtx, ch.ObjectKey, ch.ContentHash, ch.Content)
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	for i := range chapters {
		chapters[i].Content = ""
	}
	return nil
}

func (s *novelService) readChapterBody(ctx context.Context, ch *Chapter) (string, error) {
	if ch.ObjectKey != "" {
		store, ok := s.storage.(revisionStorage)
		if !ok {
			return "", errors.New("chapter storage does not support immutable objects")
		}
		return store.ReadChapterRevision(ctx, ch.ObjectKey, ch.ContentHash)
	}
	if ch.Content != "" || s.storage == nil || s.storage.StorageType() != "oss" {
		return ch.Content, nil
	}
	store, ok := s.storage.(revisionStorage)
	if !ok {
		return "", errors.New("chapter storage does not support legacy reads")
	}
	return store.ReadLegacyChapter(ctx, ch.NovelID, ch.ChapterIndex)
}

func (s *novelService) importIdentities(ctx context.Context, novelID int64) ([]ChapterIdentity, error) {
	old, err := s.repo.ListChapterIdentities(ctx, novelID)
	if err != nil {
		return nil, err
	}
	for i := range old {
		identity := &old[i]
		if identity.Index == nil || identity.Hash != "" {
			continue
		}
		ch, err := s.repo.GetChapter(ctx, novelID, *identity.Index)
		if err != nil {
			return nil, err
		}
		if ch == nil || ch.ID != identity.ID {
			return nil, ErrPublicationChanged
		}
		content, err := s.readChapterBody(ctx, ch)
		if err != nil && !errors.Is(err, storage.ErrContentNotFound) {
			return nil, err
		}
		if content != "" {
			identity.Hash = chapterDigest(content)
		}
	}
	return old, nil
}

func assignChapterIdentities(chapters []Chapter, previous []ChapterIdentity, mapping ChapterImportMapping) ([]ChapterIdentity, map[string]string, error) {
	byID := make(map[string]ChapterIdentity, len(previous))
	available := make(map[string][]string)
	newHashCount := make(map[string]int)
	used := make(map[string]bool)
	retired := make(map[string]bool)
	for _, old := range previous {
		byID[old.ID] = old
	}
	for _, id := range mapping.Retire {
		if old, ok := byID[id]; !ok || old.Index == nil || retired[id] {
			return nil, nil, fmt.Errorf("%w：无效的下架 ID %q", ErrChapterMapping, id)
		}
		retired[id] = true
	}
	chapterByNumber := make(map[int]*Chapter, len(chapters))
	for i := range chapters {
		ch := &chapters[i]
		chapterByNumber[ch.ChapterIndex+1] = ch
	}
	for number, id := range mapping.Keep {
		ch := chapterByNumber[number]
		if _, ok := byID[id]; ch == nil || !ok || used[id] || retired[id] {
			return nil, nil, fmt.Errorf("%w：无效或重复的保留映射（第 %d 章，%q）", ErrChapterMapping, number, id)
		}
		ch.ID, used[id] = id, true
	}
	splits := make(map[string]string, len(mapping.SplitFrom))
	for number, source := range mapping.SplitFrom {
		ch := chapterByNumber[number]
		if old, ok := byID[source]; ch == nil || !ok || old.Index == nil || ch.ID != "" {
			return nil, nil, fmt.Errorf("%w：无效的拆分来源（第 %d 章，%q）", ErrChapterMapping, number, source)
		}
		ch.ID = uuid.NewString()
		splits[ch.ID] = source
	}
	for _, ch := range chapters {
		if ch.ID == "" {
			newHashCount[ch.ContentHash]++
		}
	}
	for _, old := range previous {
		if old.Hash != "" && !used[old.ID] && !retired[old.ID] {
			available[old.Hash] = append(available[old.Hash], old.ID)
		}
	}
	for i := range chapters {
		ch := &chapters[i]
		if ch.ID != "" {
			continue
		}
		candidates := available[ch.ContentHash]
		if len(candidates) == 1 && newHashCount[ch.ContentHash] == 1 {
			ch.ID = candidates[0]
			used[ch.ID] = true
		} else {
			ch.ID = uuid.NewString()
		}
	}
	var removals []ChapterIdentity
	var unmapped []string
	for _, old := range previous {
		if old.Index == nil || used[old.ID] {
			continue
		}
		if retired[old.ID] {
			removals = append(removals, old)
		} else if len(unmapped) < 10 {
			unmapped = append(unmapped, strconv.Itoa(*old.Index+1)+" "+old.Title+" ("+old.ID+")")
		}
	}
	if len(unmapped) > 0 {
		return nil, nil, fmt.Errorf("%w：以下现有章节需要指定保留或下架：%s", ErrChapterMapping, strings.Join(unmapped, "、"))
	}
	return removals, splits, nil
}

func importPricing(n *Novel, configs map[string]string) (int, int, error) {
	start, cost := n.StartPayChapterIndex, n.EffectiveCoinCostPerThousand
	var err error
	if start <= 0 {
		start, err = pricingSetting(configs, "global_start_pay_chapter_index", 1, 3)
		if err != nil {
			return 0, 0, err
		}
	}
	if n.CoinCostPerThousand == nil {
		cost, err = pricingSetting(configs, "global_coin_cost_per_thousand", 0, 500)
	} else {
		cost = *n.CoinCostPerThousand
	}
	if err == nil && (cost < 0 || cost > math.MaxInt32) {
		err = ErrInvalidNovelPricing
	}
	return start, cost, err
}

func (s *novelService) publicationPricing(ctx context.Context, tx pgx.Tx, n *Novel) (int, int, error) {
	var settings map[string]string
	if n.CoinCostPerThousand == nil || n.StartPayChapterIndex <= 0 {
		var err error
		settings, err = s.repo.GetSettingsTx(ctx, tx)
		if err != nil {
			return 0, 0, fmt.Errorf("read chapter pricing settings: %w", err)
		}
	}
	return importPricing(n, settings)
}

func setChapterPrice(ch *Chapter, startPay, cost int) error {
	ch.IsPaid = cost > 0 && ch.ChapterIndex >= startPay-1
	ch.Price = 0
	if ch.IsPaid {
		price := (int64(ch.WordCount)*int64(cost) + 500) / 1000
		if price > math.MaxInt32 {
			return fmt.Errorf("%w：第 %d 章计价超出可存储范围", ErrInvalidNovelPricing, ch.ChapterIndex+1)
		}
		ch.Price = int(price)
	}
	return nil
}

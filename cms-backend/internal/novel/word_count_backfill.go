package novel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
	redisclient "star-novel-cms/internal/redis"
	"star-novel-cms/internal/storage"
)

type WordCountBackfillOptions struct {
	DryRun      bool
	NovelID     int64 // Optional: when > 0, only backfill this novel.
	Concurrency int   // Concurrent chapter body reads per novel (defaults to 8).
}

type WordCountBackfillReport struct {
	DryRun          bool
	NovelsScanned   int
	NovelsUpdated   int
	ChaptersScanned int
	ChaptersUpdated int
	ChaptersSkipped int
	ChaptersFailed  int
}

type backfillNovelRow struct {
	ID                   int64
	Title                string
	WordCount            int
	CoinCostPerThousand  *int
	StartPayChapterIndex int
}

type backfillChapterRow struct {
	ID           string
	NovelID      int64
	ChapterIndex int
	WordCount    int
	IsPaid       bool
	Price        int
	Content      string
	ObjectKey    *string
	ContentHash  *string
}

type chapterBackfillComputation struct {
	row          backfillChapterRow
	newWordCount int
	newIsPaid    bool
	newPrice     int
	changed      bool
	err          error
}

// BackfillWordCounts recalculates chapters.word_count, chapters.is_paid,
// chapters.price, and novels.word_count using word-based counting (countWords).
// It is idempotent and fences chapter updates against concurrent publication.
func BackfillWordCounts(
	ctx context.Context,
	pool *pgxpool.Pool,
	store storage.ContentStorage,
	opts WordCountBackfillOptions,
) (*WordCountBackfillReport, error) {
	if pool == nil {
		return nil, errors.New("database pool is required")
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}

	settings, err := loadBackfillSettings(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("load system_configs: %w", err)
	}

	novels, err := listBackfillNovels(ctx, pool, opts.NovelID)
	if err != nil {
		return nil, fmt.Errorf("list novels: %w", err)
	}

	report := &WordCountBackfillReport{DryRun: opts.DryRun}
	for _, n := range novels {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.NovelsScanned++
		if err := backfillNovelWordCounts(ctx, pool, store, settings, n, opts, report); err != nil {
			return report, err
		}
	}

	log.Printf(
		"word-count backfill finished: dry_run=%t novels_scanned=%d novels_updated=%d chapters_scanned=%d chapters_updated=%d chapters_skipped=%d chapters_failed=%d",
		report.DryRun,
		report.NovelsScanned,
		report.NovelsUpdated,
		report.ChaptersScanned,
		report.ChaptersUpdated,
		report.ChaptersSkipped,
		report.ChaptersFailed,
	)
	if report.ChaptersFailed > 0 {
		return report, fmt.Errorf("%d chapters failed word-count backfill and need a retry", report.ChaptersFailed)
	}
	return report, nil
}

func loadBackfillSettings(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	rows, err := pool.Query(ctx, `SELECT key, value FROM system_configs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		settings[k] = v
	}
	return settings, rows.Err()
}

func listBackfillNovels(ctx context.Context, pool *pgxpool.Pool, novelID int64) ([]backfillNovelRow, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if novelID > 0 {
		rows, err = pool.Query(ctx, `
			SELECT id, COALESCE(title, ''), COALESCE(word_count, 0), coin_cost_per_thousand, COALESCE(start_pay_chapter_index, 0)
			FROM novels
			WHERE id = $1
			ORDER BY id ASC`, novelID)
	} else {
		rows, err = pool.Query(ctx, `
			SELECT id, COALESCE(title, ''), COALESCE(word_count, 0), coin_cost_per_thousand, COALESCE(start_pay_chapter_index, 0)
			FROM novels
			ORDER BY id ASC`)
	}
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (backfillNovelRow, error) {
		var n backfillNovelRow
		err := row.Scan(&n.ID, &n.Title, &n.WordCount, &n.CoinCostPerThousand, &n.StartPayChapterIndex)
		return n, err
	})
}

func listBackfillChapters(ctx context.Context, pool *pgxpool.Pool, novelID int64) ([]backfillChapterRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, novel_id, chapter_index, COALESCE(word_count, 0), COALESCE(is_paid, false), COALESCE(price, 0),
		       COALESCE(content, ''), content_object_key, content_sha256
		FROM chapters
		WHERE novel_id = $1 AND chapter_index IS NOT NULL
		ORDER BY chapter_index ASC, id ASC`, novelID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (backfillChapterRow, error) {
		var c backfillChapterRow
		err := row.Scan(&c.ID, &c.NovelID, &c.ChapterIndex, &c.WordCount, &c.IsPaid, &c.Price, &c.Content, &c.ObjectKey, &c.ContentHash)
		return c, err
	})
}

func readBackfillChapterBody(ctx context.Context, store storage.ContentStorage, ch backfillChapterRow) (string, error) {
	if ch.Content != "" {
		return ch.Content, nil
	}
	if store == nil || store.StorageType() != "oss" {
		return "", nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if revStore, ok := store.(revisionStorage); ok {
		if ch.ObjectKey != nil && *ch.ObjectKey != "" {
			hash := ""
			if ch.ContentHash != nil {
				hash = *ch.ContentHash
			}
			return revStore.ReadChapterRevision(readCtx, *ch.ObjectKey, hash)
		}
		return revStore.ReadLegacyChapter(readCtx, ch.NovelID, ch.ChapterIndex)
	}
	return store.GetContent(readCtx, ch.NovelID, ch.ChapterIndex)
}

func computeChapterWordCountBackfill(
	ctx context.Context,
	store storage.ContentStorage,
	ch backfillChapterRow,
	startPay, cost int,
) chapterBackfillComputation {
	res := chapterBackfillComputation{row: ch}
	body, err := readBackfillChapterBody(ctx, store, ch)
	if err != nil {
		res.err = err
		return res
	}
	if !utf8.ValidString(body) {
		res.err = errors.New("chapter body is not valid UTF-8")
		return res
	}
	target := Chapter{
		ChapterIndex: ch.ChapterIndex,
		WordCount:    countWords(body),
	}
	if err := setChapterPrice(&target, startPay, cost); err != nil {
		res.err = err
		return res
	}
	res.newWordCount = target.WordCount
	res.newIsPaid = target.IsPaid
	res.newPrice = target.Price
	res.changed = ch.WordCount != target.WordCount || ch.IsPaid != target.IsPaid || ch.Price != target.Price
	return res
}

func backfillNovelWordCounts(
	ctx context.Context,
	pool *pgxpool.Pool,
	store storage.ContentStorage,
	settings map[string]string,
	n backfillNovelRow,
	opts WordCountBackfillOptions,
	report *WordCountBackfillReport,
) error {
	startPay, cost, err := importPricing(&Novel{
		CoinCostPerThousand:  n.CoinCostPerThousand,
		StartPayChapterIndex: n.StartPayChapterIndex,
	}, settings)
	if err != nil {
		return fmt.Errorf("resolve pricing for novel %d: %w", n.ID, err)
	}

	chapters, err := listBackfillChapters(ctx, pool, n.ID)
	if err != nil {
		return fmt.Errorf("list chapters for novel %d: %w", n.ID, err)
	}
	if len(chapters) == 0 {
		return nil
	}

	results := make([]chapterBackfillComputation, len(chapters))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(opts.Concurrency)
	for i := range chapters {
		idx := i
		ch := chapters[i]
		group.Go(func() error {
			if err := groupCtx.Err(); err != nil {
				return err
			}
			results[idx] = computeChapterWordCountBackfill(groupCtx, store, ch, startPay, cost)
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}

	novelChaptersUpdated := 0
	novelChaptersFailed := 0
	expectedTotalWords := 0

	for _, comp := range results {
		report.ChaptersScanned++
		if comp.err != nil {
			report.ChaptersFailed++
			novelChaptersFailed++
			expectedTotalWords += comp.row.WordCount
			log.Printf("word-count backfill chapter failed: novel_id=%d chapter_index=%d chapter_id=%q err=%v",
				n.ID, comp.row.ChapterIndex, comp.row.ID, comp.err)
			continue
		}
		expectedTotalWords += comp.newWordCount
		if !comp.changed {
			report.ChaptersSkipped++
			continue
		}
		if opts.DryRun {
			novelChaptersUpdated++
			report.ChaptersUpdated++
			continue
		}

		tag, err := pool.Exec(ctx, `
			UPDATE chapters
			SET word_count = $1, is_paid = $2, price = $3
			WHERE id = $4
			  AND novel_id = $5
			  AND chapter_index = $6
			  AND content_object_key IS NOT DISTINCT FROM $7
			  AND content_sha256 IS NOT DISTINCT FROM $8
			  AND COALESCE(content, '') = $9
			  AND (word_count, is_paid, price) IS DISTINCT FROM ($1, $2, $3)`,
			comp.newWordCount, comp.newIsPaid, comp.newPrice,
			comp.row.ID, comp.row.NovelID, comp.row.ChapterIndex,
			comp.row.ObjectKey, comp.row.ContentHash, comp.row.Content,
		)
		if err != nil {
			report.ChaptersFailed++
			novelChaptersFailed++
			log.Printf("word-count backfill update failed: novel_id=%d chapter_index=%d chapter_id=%q err=%v",
				n.ID, comp.row.ChapterIndex, comp.row.ID, err)
			continue
		}
		if tag.RowsAffected() > 0 {
			novelChaptersUpdated++
			report.ChaptersUpdated++
		} else {
			report.ChaptersSkipped++
		}
	}

	novelWordCountChanged := n.WordCount != expectedTotalWords
	if novelChaptersUpdated == 0 && !novelWordCountChanged {
		return nil
	}

	if opts.DryRun {
		report.NovelsUpdated++
		log.Printf("[DRY-RUN] novel_id=%d title=%q chapters_to_update=%d failed=%d total_word_count: %d -> %d (cost=%d/1000w, start_pay=%d)",
			n.ID, n.Title, novelChaptersUpdated, novelChaptersFailed, n.WordCount, expectedTotalWords, cost, startPay)
		return nil
	}

	tag, err := pool.Exec(ctx, `
		UPDATE novels
		SET word_count = COALESCE((SELECT SUM(word_count) FROM chapters WHERE novel_id = $1 AND chapter_index IS NOT NULL), 0),
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
		  AND word_count IS DISTINCT FROM COALESCE((SELECT SUM(word_count) FROM chapters WHERE novel_id = $1 AND chapter_index IS NOT NULL), 0)`,
		n.ID,
	)
	if err != nil {
		return fmt.Errorf("sync novel %d word_count: %w", n.ID, err)
	}
	if novelChaptersUpdated > 0 || tag.RowsAffected() > 0 {
		report.NovelsUpdated++
		invalidateBackfillNovelCache(ctx, n.ID)
		log.Printf("backfilled novel_id=%d title=%q chapters_updated=%d failed=%d total_word_count: %d -> %d",
			n.ID, n.Title, novelChaptersUpdated, novelChaptersFailed, n.WordCount, expectedTotalWords)
	}
	return nil
}

func invalidateBackfillNovelCache(ctx context.Context, novelID int64) {
	if redisclient.RDB == nil {
		return
	}
	keyDetail := fmt.Sprintf("novel:detail:%d", novelID)
	keyChapters := fmt.Sprintf("novel:chapters:%d", novelID)
	if err := redisclient.RDB.Del(ctx, keyDetail, keyChapters).Err(); err != nil {
		log.Printf("word-count backfill redis invalidate warning: novel_id=%d err=%v", novelID, err)
	}
}

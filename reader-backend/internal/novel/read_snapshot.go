package novel

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"reader-backend/internal/auth"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
)

const chapterPricingColumns = `
    COALESCE(NULLIF(pl.start_pay_chapter_index,0), NULLIF(n.start_pay_chapter_index,0),
        (SELECT value::int FROM system_configs WHERE key='global_start_pay_chapter_index'),3),
    COALESCE(pl.coin_cost_per_thousand, n.coin_cost_per_thousand,
        (SELECT value::int FROM system_configs WHERE key='global_coin_cost_per_thousand'),500)`

func chapterPricingJoinSQL(envParam string) string {
	return `LEFT JOIN LATERAL (
    SELECT p.start_pay_chapter_index, p.coin_cost_per_thousand
    FROM promotion_links p WHERE p.novel_id=n.id AND p.id=CASE WHEN reader.user_id>0
    THEN (SELECT id FROM reader_promotion_for_user_env(reader.user_id,` + envParam + `))
    ELSE resolve_reader_promotion_for_env(reader.promo_id,'','',` + envParam + `) END
) pl ON TRUE`
}

var paidAccessSQL = `(
    EXISTS(SELECT 1 FROM user_subscriptions WHERE user_id=reader.user_id AND payment_subscription_environment(user_subscriptions)=$6 AND ` + PaidSubscriptionPredicate + `)
    OR EXISTS(SELECT 1 FROM recharge_orders WHERE recharge_orders.user_id=reader.user_id AND recharge_orders.novel_id=book.id AND ` + WholeBookEntitlementPredicate + ` AND ` + WholeBookEnvironmentPredicate("$6") + `)
)`

// ReadSnapshot keeps pricing, publication and access in one database snapshot.
type ReadSnapshot struct {
	userID, version        int64
	start, cost            int
	pass, unlocked, review bool
	chapter                *model.Chapter
	preview                string
}

type readIdentity struct {
	sessionHash string
	expected    *string
}

type readIdentityKey struct{}

var (
	errReadAccountChanged     = errors.New("the signed-in account changed")
	errReadSessionExpired     = errors.New("reader session expired")
	errReadAccountUnavailable = errors.New("reader account unavailable")
)

func verifyReadIdentity(ctx context.Context, userID int64) error {
	identity, _ := ctx.Value(readIdentityKey{}).(readIdentity)
	if identity.expected != nil && *identity.expected != strconv.FormatInt(userID, 10) {
		return errReadAccountChanged
	}
	return nil
}

func (r *dbRepository) ReadSnapshot(ctx context.Context, userID, novelID int64, promoID int, index *int) (ReadSnapshot, error) {
	var snapshot ReadSnapshot
	var chapter model.Chapter
	var id *string
	var created *time.Time
	var accountActive bool
	identity, _ := ctx.Value(readIdentityKey{}).(readIdentity)
	err := db.DB.QueryRow(ctx, `
		WITH reader AS MATERIALIZED (
		    SELECT CASE WHEN $1::bigint > 0 AND `+auth.ActiveSessionSQL+` THEN $1 ELSE 0 END user_id, $4::int promo_id,
		        EXISTS(SELECT 1 FROM users u WHERE u.id=$1 AND u.status=1) account_active
		), book(id, content_version, start_pay, cost) AS MATERIALIZED (
		    SELECT n.id, n.content_version, `+chapterPricingColumns+`
		    FROM reader JOIN novels n ON n.id=$3 AND n.status<>'archived' AND n.publish_status='published'
		    `+chapterPricingJoinSQL("$7")+`
		), reading AS MATERIALIZED (
		    SELECT reader.user_id, reader.account_active, book.id AS novel_id, book.content_version, book.start_pay, book.cost,
		        CASE WHEN reader.user_id>0 AND book.id IS NOT NULL AND
		            ($5::int IS NULL OR (c.id IS NOT NULL AND $5>=book.start_pay-1 AND book.cost>0))
		        THEN `+paidAccessSQL+` ELSE false END pass,
		        c.id, c.title, c.word_count, c.created_at, c.content_object_key, c.content_sha256, c.content_preview, c.body_revision
		    FROM reader LEFT JOIN book ON true
		    LEFT JOIN chapters c ON c.novel_id=book.id AND c.chapter_index=$5
		), access AS (
		    SELECT reading.*, CASE WHEN user_id>0 AND NOT pass AND id IS NOT NULL AND $5>=start_pay-1 AND cost>0
		         THEN reader_has_chapter_unlock(user_id,id) ELSE false END unlocked
		    FROM reading
		)
		SELECT user_id, account_active, COALESCE(content_version,0), COALESCE(start_pay,0), COALESCE(cost,0), pass, unlocked,
		    CASE WHEN user_id>0 AND NOT pass AND NOT unlocked AND id IS NOT NULL AND $5>=start_pay-1 AND cost>0
		         THEN EXISTS(SELECT 1 FROM unlock_records u WHERE u.user_id=access.user_id
		                         AND u.novel_id=access.novel_id AND u.chapter_id IS NULL) ELSE false END,
		    id, COALESCE(title,''), COALESCE(word_count,0), created_at,
		    COALESCE(content_object_key,''), COALESCE(content_sha256,''), COALESCE(body_revision,0),
		    CASE WHEN id IS NOT NULL AND NOT pass AND NOT unlocked AND $5>=start_pay-1 AND cost>0
		        THEN COALESCE(content_preview, (SELECT reader_chapter_preview(c.content) FROM chapters c WHERE c.id=access.id), '')
		        ELSE '' END
		FROM access`, userID, identity.sessionHash, novelID, promoID, index, conversion.Environment(), conversion.PromotionEnvironment()).Scan(
		&snapshot.userID, &accountActive, &snapshot.version, &snapshot.start, &snapshot.cost, &snapshot.pass,
		&snapshot.unlocked, &snapshot.review, &id, &chapter.Title, &chapter.WordCount, &created,
		&chapter.ObjectKey, &chapter.ContentHash, &chapter.BodyRevision, &snapshot.preview)
	if err != nil {
		return ReadSnapshot{}, err
	}
	if userID > 0 && snapshot.userID == 0 {
		if !accountActive {
			return ReadSnapshot{}, errReadAccountUnavailable
		}
		return ReadSnapshot{}, errReadSessionExpired
	}
	if snapshot.version != 0 && (snapshot.start <= 0 || snapshot.cost < 0) {
		return ReadSnapshot{}, fmt.Errorf("invalid chapter pricing configuration")
	}
	if id != nil {
		chapter.ID, chapter.NovelID, chapter.ChapterIndex = *id, novelID, *index
		if created != nil {
			chapter.CreatedAt = *created
		}
		snapshot.chapter = &chapter
	}
	return snapshot, nil
}

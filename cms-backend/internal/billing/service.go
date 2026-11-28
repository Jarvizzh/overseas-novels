package billing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrOrderNotFound                  = errors.New("Order not found")
	ErrOrderAlreadyRefunded           = errors.New("Order has already been refunded")
	ErrOrderNotRefundable             = errors.New("Only successfully completed orders can be refunded")
	ErrTemplateSlotCountInvalid       = errors.New("Each recharge template slot count must be between 4 and 8")
	ErrTemplateSlotBonusTooHigh       = errors.New("Bonus coins cannot exceed 1.5 times of standard coins for single slots")
	ErrTemplateSlotInvalid            = errors.New("Each template slot must reference a distinct published product with slot_index 1..8")
	ErrTemplateProductNotPublished    = errors.New("All template slots must reference products that are published and dual-channel ready")
	ErrTemplateNotFound               = errors.New("Recharge template not found")
	ErrDefaultTemplateNotDelete       = errors.New("Default recharge template cannot be deleted, please set another template as default first")
	ErrCannotUnsetLastDefaultTemplate = errors.New("Cannot unset the only default recharge template; set another template as default first")
	ErrTemplateReferenced             = errors.New("Recharge template is referenced by promotion links and cannot be deleted")
	ErrTemplateEnvPolicy              = errors.New("Only live recharge templates can be default or switched while active/referenced")
)

type BillingService interface {
	ListLedgerMovements(context.Context, movementFilter) (movementPage, error)
	ListSettlementPayouts(context.Context, payoutFilter) (payoutPage, error)
	ListSettlementReportIssues(context.Context, reconciliationPage) (reportIssuePage, error)
	GetPaymentActivity(context.Context, reconciliationScope) (paymentActivity, error)

	ListOrders(ctx context.Context, page, pageSize int, status, userID, orderType, promotionLinkID, paidStart, paidEnd, orderID string, scope reconciliationScope) ([]Order, int, error)
	GetThirdPartyPaymentDetails(ctx context.Context, orderID int64) (*ThirdPartyPaymentOrder, error)

	ListRechargeTemplates(ctx context.Context) ([]RechargeTemplate, error)
	ListVIPOfferPublications(ctx context.Context) ([]VIPOfferPublication, error)
	CreateRechargeTemplate(ctx context.Context, adminID string, name string, environment string, isDefault bool, slots []RechargeSlot) (int, error)
	UpdateRechargeTemplate(ctx context.Context, adminID string, id int, name string, environment string, isDefault bool, slots []RechargeSlot) error
	DeleteRechargeTemplate(ctx context.Context, adminID string, id int) error
	SetDefaultRechargeTemplate(ctx context.Context, adminID string, id int) error
}

type billingService struct {
	repo BillingRepository
}

func NewBillingService(repo BillingRepository) BillingService {
	return &billingService{
		repo: repo,
	}
}

func (s *billingService) ListVIPOfferPublications(ctx context.Context) ([]VIPOfferPublication, error) {
	return s.repo.ListVIPOfferPublications(ctx)
}

func (s *billingService) ListOrders(ctx context.Context, page, pageSize int, status, userID, orderType, promotionLinkID, paidStart, paidEnd, orderID string, scope reconciliationScope) ([]Order, int, error) {
	offset := (page - 1) * pageSize

	var whereClauses []string
	var args []interface{}
	argCount := 1
	if orderID != "" {
		id, err := strconv.ParseInt(orderID, 10, 64)
		if err != nil {
			return nil, 0, err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("id = $%d", argCount))
		args = append(args, id)
		argCount++
	}

	if status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", argCount))
		args = append(args, status)
		argCount++
	}

	if userID != "" {
		if uid, err := strconv.ParseInt(userID, 10, 64); err == nil {
			whereClauses = append(whereClauses, fmt.Sprintf("user_id = $%d", argCount))
			args = append(args, uid)
			argCount++
		} else {
			whereClauses = append(whereClauses, fmt.Sprintf("user_id = $%d", argCount))
			args = append(args, userID)
			argCount++
		}
	}

	if orderType != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("order_type = $%d", argCount))
		args = append(args, orderType)
		argCount++
	}

	if promotionLinkID != "" {
		if pid, err := strconv.Atoi(promotionLinkID); err == nil {
			whereClauses = append(whereClauses, fmt.Sprintf("promotion_link_id = $%d", argCount))
			args = append(args, pid)
			argCount++
		}
	}

	if paidStart != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("paid_at >= $%d", argCount))
		args = append(args, paidStart)
		argCount++
	}

	if paidEnd != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("paid_at <= $%d", argCount))
		args = append(args, paidEnd)
		argCount++
	}

	if scope.Environment != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("payment_order_environment(recharge_orders)=$%d", argCount))
		args = append(args, scope.Environment)
		argCount++
	}
	if scope.Provider != "" || scope.AccountID != 0 {
		whereClauses = append(whereClauses, fmt.Sprintf(`(
			EXISTS (SELECT 1 FROM third_party_payment_orders p
				LEFT JOIN payment_channel_accounts a ON a.id=p.account_id
				WHERE p.order_id=recharge_orders.id AND p.status IN ('VERIFIED','COMPLETED')
					AND ($%[1]d='' OR p.payment_provider=$%[1]d)
					AND ($%[2]d::bigint=0 OR (a.id=$%[2]d AND a.provider=p.payment_provider)))
			OR ($%[2]d::bigint=0 AND payment_method=$%[1]d AND NOT EXISTS
				(SELECT 1 FROM third_party_payment_orders p WHERE p.order_id=recharge_orders.id AND p.status IN ('VERIFIED','COMPLETED')))
		)`, argCount, argCount+1))
		args = append(args, scope.Provider, scope.AccountID)
		argCount += 2
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM recharge_orders %s", whereSQL)
	total, err := s.repo.CountOrders(ctx, countQuery, args)
	if err != nil {
		return nil, 0, err
	}

	selectQuery := fmt.Sprintf(`
		SELECT id, user_id, COALESCE(external_ref_id, ''), amount_cents, currency, coins, bonus_coins_credited, payment_method, status, COALESCE(utm_source, ''), COALESCE(utm_campaign, ''), paid_at, order_type, promotion_link_id, novel_id, created_at, updated_at, COALESCE(reconciliation_note, ''),
			COALESCE((SELECT r.status FROM payment_refund_commands r WHERE r.order_id=recharge_orders.id ORDER BY r.created_at DESC LIMIT 1),''),
			COALESCE(payment_order_environment(recharge_orders),''),
			COALESCE(CASE WHEN receipt_scope.receipts=0 THEN payment_method ELSE receipt_scope.provider END,''),
			COALESCE(receipt_scope.account_id,''),
			CASE WHEN recharge_orders.order_type='subscription' THEN COALESCE(
				NULLIF(recharge_orders.product_snapshot->>'vip_cycle', ''),
				NULLIF(recharge_orders.product_snapshot->>'cycle', ''),
				(SELECT NULLIF(p.vip_cycle, '') FROM products p WHERE p.id = recharge_orders.product_id),
				(SELECT NULLIF(us.cycle, '') FROM user_subscriptions us WHERE us.subscription_id = NULLIF(recharge_orders.subscription_id, '') LIMIT 1),
				(SELECT COALESCE(NULLIF(rs.subscription_cycle, ''), NULLIF(rs.vip_duration, '')) FROM recharge_slots rs WHERE rs.id = recharge_orders.recharge_slot_id LIMIT 1),
				''
			) ELSE '' END
		FROM recharge_orders
		CROSS JOIN LATERAL (
			SELECT count(*) AS receipts,
				CASE WHEN count(DISTINCT p.payment_provider)=1 THEN min(p.payment_provider) END AS provider,
				CASE WHEN count(DISTINCT a.id)=1 AND count(a.id)=count(*) THEN min(a.id)::text END AS account_id
			FROM third_party_payment_orders p LEFT JOIN payment_channel_accounts a ON a.id=p.account_id AND a.provider=p.payment_provider
			WHERE p.order_id=recharge_orders.id AND p.status IN ('VERIFIED','COMPLETED')
		) receipt_scope
		%s
		ORDER BY created_at DESC,id DESC
		LIMIT $%d OFFSET $%d`, whereSQL, argCount, argCount+1)

	args = append(args, pageSize, offset)
	orders, err := s.repo.ListOrders(ctx, selectQuery, args)
	if err != nil {
		return nil, 0, err
	}

	return orders, total, nil
}

func (s *billingService) ListRechargeTemplates(ctx context.Context) ([]RechargeTemplate, error) {
	templates, err := s.repo.ListRechargeTemplates(ctx)
	if err != nil {
		return nil, err
	}

	for i := range templates {
		slots, err := s.repo.GetRechargeSlots(ctx, templates[i].ID)
		if err != nil {
			return nil, err
		}
		templates[i].Slots = slots
	}

	return templates, nil
}

func normalizeTemplateEnvironment(env string) (string, error) {
	env = strings.TrimSpace(env)
	if env == "" {
		return "live", nil
	}
	if env != "sandbox" && env != "live" {
		return "", ErrTemplateSlotInvalid
	}
	return env, nil
}

func validateTemplateSlotsTx(ctx context.Context, tx pgx.Tx, env string, slots []RechargeSlot) error {
	if len(slots) < 4 || len(slots) > 8 {
		return ErrTemplateSlotCountInvalid
	}
	seenIdx := make(map[int]bool, len(slots))
	seenProd := make(map[int64]bool, len(slots))
	productIDs := make([]int64, 0, len(slots))
	for i := range slots {
		s := &slots[i]
		s.Badge = strings.TrimSpace(s.Badge)
		s.DisplayAlias = strings.TrimSpace(s.DisplayAlias)
		if s.SlotIndex < 1 || s.SlotIndex > 8 || s.ProductID <= 0 || len(s.Badge) > 32 || len(s.DisplayAlias) > 64 || s.ListPriceCents < 0 || s.ListPriceCents > 100000000 {
			return ErrTemplateSlotInvalid
		}
		if seenIdx[s.SlotIndex] || seenProd[s.ProductID] {
			return ErrTemplateSlotInvalid
		}
		seenIdx[s.SlotIndex] = true
		seenProd[s.ProductID] = true
		productIDs = append(productIDs, s.ProductID)
	}

	rows, err := tx.Query(ctx, `
		SELECT p.id, p.code, p.price_cents,
		       COALESCE(pub.status, '') = 'published' AS env_published,
		       EXISTS (
		           SELECT 1 FROM product_channel_bindings bp
		           JOIN payment_channel_accounts ap ON ap.id = bp.account_id
		           WHERE bp.product_id = p.id AND bp.provider = 'paypal' AND bp.environment = $2
		             AND ap.provider = 'paypal' AND ap.environment = $2 AND ap.status = 'published' AND ap.is_primary = true
		             AND bp.enabled = true AND bp.validation_status = 'verified'
		             AND ((p.product_type = 'vip' AND ap.new_subscriptions_enabled AND bp.external_price_ref <> '')
		               OR (p.product_type <> 'vip' AND ap.new_orders_enabled))
		       ) AS pp_ready,
		       EXISTS (
		           SELECT 1 FROM product_channel_bindings bs
		           JOIN payment_channel_accounts ast ON ast.id = bs.account_id
		           WHERE bs.product_id = p.id AND bs.provider = 'stripe' AND bs.environment = $2
		             AND ast.provider = 'stripe' AND ast.environment = $2 AND ast.status = 'published' AND ast.is_primary = true
		             AND bs.enabled = true AND bs.validation_status = 'verified'
		             AND bs.external_product_ref <> '' AND bs.external_price_ref <> ''
		             AND ($2 = 'sandbox' OR (bs.validated_catalog IS NOT NULL AND bs.release_evidence IS NOT NULL AND bs.market_policy IS NOT NULL))
		             AND ((p.product_type = 'vip' AND ast.new_subscriptions_enabled)
		               OR (p.product_type <> 'vip' AND ast.new_orders_enabled))
		       ) AS st_ready
		FROM products p
		LEFT JOIN product_publications pub ON pub.product_id = p.id AND pub.environment = $2
		WHERE p.id = ANY($1::bigint[])`, productIDs, env)
	if err != nil {
		return err
	}
	defer rows.Close()

	found := 0
	productPrices := make(map[int64]int, len(productIDs))
	var problems []string
	for rows.Next() {
		var pid int64
		var code string
		var priceCents int
		var published, ppReady, stReady bool
		if err := rows.Scan(&pid, &code, &priceCents, &published, &ppReady, &stReady); err != nil {
			return err
		}
		found++
		productPrices[pid] = priceCents
		if !published {
			problems = append(problems, fmt.Sprintf("%s(#%d) is not published in %s", code, pid, env))
		} else if !ppReady || !stReady {
			missing := []string{}
			if !ppReady {
				missing = append(missing, "PayPal")
			}
			if !stReady {
				missing = append(missing, "Stripe")
			}
			problems = append(problems, fmt.Sprintf("%s(#%d) is not ready on %s (%s)", code, pid, strings.Join(missing, "/"), env))
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if found != len(productIDs) {
		return fmt.Errorf("%w: one or more selected products do not exist", ErrTemplateProductNotPublished)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrTemplateProductNotPublished, strings.Join(problems, "; "))
	}
	for _, s := range slots {
		if s.ListPriceCents > 0 && s.ListPriceCents <= productPrices[s.ProductID] {
			return fmt.Errorf("%w: slot %d list_price_cents (%d) must be greater than product price_cents (%d)", ErrTemplateSlotInvalid, s.SlotIndex, s.ListPriceCents, productPrices[s.ProductID])
		}
	}
	return nil
}

func (s *billingService) CreateRechargeTemplate(ctx context.Context, adminID string, name string, environment string, isDefault bool, slots []RechargeSlot) (int, error) {
	if len(slots) < 4 || len(slots) > 8 {
		return 0, ErrTemplateSlotCountInvalid
	}
	env, err := normalizeTemplateEnvironment(environment)
	if err != nil {
		return 0, err
	}
	if isDefault && env != "live" {
		return 0, fmt.Errorf("%w: sandbox templates cannot be set as the site default", ErrTemplateEnvPolicy)
	}

	tx, err := s.beginTemplateChange(ctx, adminID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if err := validateTemplateSlotsTx(ctx, tx, env, slots); err != nil {
		return 0, err
	}

	before, err := templateSnapshot(ctx, tx, 0)
	if err != nil {
		return 0, err
	}

	if isDefault {
		err = s.repo.ClearDefaultTemplatesTx(ctx, tx)
		if err != nil {
			return 0, err
		}
	}

	templateID, err := s.repo.CreateRechargeTemplateTx(ctx, tx, name, env, isDefault)
	if err != nil {
		return 0, err
	}

	for _, slot := range slots {
		err = s.repo.CreateRechargeSlotTx(ctx, tx, templateID, slot.SlotIndex, slot.ProductID, slot.Badge, slot.IsHighlighted, slot.DisplayAlias, slot.ListPriceCents)
		if err != nil {
			return 0, err
		}
	}

	if err := s.auditTemplateChange(ctx, tx, adminID, "create_recharge_template", templateID, before); err != nil {
		return 0, err
	}
	err = tx.Commit(ctx)
	if err != nil {
		return 0, err
	}

	return templateID, nil
}

func (s *billingService) UpdateRechargeTemplate(ctx context.Context, adminID string, id int, name string, environment string, isDefault bool, slots []RechargeSlot) error {
	if len(slots) < 4 || len(slots) > 8 {
		return ErrTemplateSlotCountInvalid
	}

	tx, err := s.beginTemplateChange(ctx, adminID)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	var wasDefault bool
	var existingEnv string
	err = tx.QueryRow(ctx, `SELECT is_default, environment FROM recharge_templates WHERE id = $1 FOR UPDATE`, id).Scan(&wasDefault, &existingEnv)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTemplateNotFound
	}
	if err != nil {
		return err
	}

	env := strings.TrimSpace(environment)
	if env == "" {
		env = existingEnv
	}
	if env != "sandbox" && env != "live" {
		return ErrTemplateSlotInvalid
	}
	if isDefault && env != "live" {
		return fmt.Errorf("%w: sandbox templates cannot be set as the site default", ErrTemplateEnvPolicy)
	}
	if env != "live" && wasDefault {
		return fmt.Errorf("%w: default templates must remain in live environment", ErrTemplateEnvPolicy)
	}
	if env != existingEnv {
		var hasPromoRef bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM promotion_links WHERE recharge_template_id = $1 FOR SHARE)`, id).Scan(&hasPromoRef); err != nil {
			return err
		}
		if hasPromoRef {
			return fmt.Errorf("%w: promotion-linked templates cannot change environment while referenced", ErrTemplateEnvPolicy)
		}
	}

	if wasDefault && !isDefault {
		var otherDefault bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM recharge_templates WHERE is_default = true AND id <> $1)`, id).Scan(&otherDefault); err != nil {
			return err
		}
		if !otherDefault {
			return ErrCannotUnsetLastDefaultTemplate
		}
	}

	if err := validateTemplateSlotsTx(ctx, tx, env, slots); err != nil {
		return err
	}

	before, err := templateSnapshot(ctx, tx, id)
	if err != nil {
		return err
	}

	if isDefault {
		err = s.repo.ClearDefaultTemplatesTx(ctx, tx)
		if err != nil {
			return err
		}
	}

	affected, err := s.repo.UpdateRechargeTemplateTx(ctx, tx, id, name, env, isDefault)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrTemplateNotFound
	}

	if err := s.repo.SyncRechargeSlotsTx(ctx, tx, id, slots); err != nil {
		return err
	}

	if err := s.auditTemplateChange(ctx, tx, adminID, "update_recharge_template", id, before); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *billingService) DeleteRechargeTemplate(ctx context.Context, adminID string, id int) error {
	tx, err := s.beginTemplateChange(ctx, adminID)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var isDefault bool
	err = tx.QueryRow(ctx, "SELECT is_default FROM recharge_templates WHERE id=$1 FOR UPDATE", id).Scan(&isDefault)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTemplateNotFound
	}
	if err != nil {
		return err
	}
	if isDefault {
		return ErrDefaultTemplateNotDelete
	}
	rows, err := tx.Query(ctx, `
		SELECT COALESCE(NULLIF(name, ''), '#' || id::text)
		FROM promotion_links
		WHERE recharge_template_id = $1
		ORDER BY id FOR SHARE`, id)
	if err != nil {
		return err
	}
	var promoNames []string
	for rows.Next() {
		var pName string
		if err := rows.Scan(&pName); err != nil {
			rows.Close()
			return err
		}
		promoNames = append(promoNames, pName)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(promoNames) > 0 {
		return fmt.Errorf("%w (%d promotion link(s): %s)", ErrTemplateReferenced, len(promoNames), strings.Join(promoNames, ", "))
	}

	before, err := templateSnapshot(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM recharge_templates WHERE id=$1", id); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrTemplateReferenced
		}
		return err
	}
	if err := s.auditTemplateChange(ctx, tx, adminID, "delete_recharge_template", id, before); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *billingService) SetDefaultRechargeTemplate(ctx context.Context, adminID string, id int) error {
	tx, err := s.beginTemplateChange(ctx, adminID)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	var targetEnv string
	err = tx.QueryRow(ctx, `SELECT environment FROM recharge_templates WHERE id = $1 FOR UPDATE`, id).Scan(&targetEnv)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTemplateNotFound
	}
	if err != nil {
		return err
	}
	if targetEnv != "live" {
		return fmt.Errorf("%w: sandbox templates cannot be set as the site default", ErrTemplateEnvPolicy)
	}

	before, err := templateSnapshot(ctx, tx, id)
	if err != nil {
		return err
	}
	err = s.repo.ClearDefaultTemplatesTx(ctx, tx)
	if err != nil {
		return err
	}

	affected, err := s.repo.SetDefaultTemplateOnlyTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrTemplateNotFound
	}

	if err := s.auditTemplateChange(ctx, tx, adminID, "set_default_recharge_template", id, before); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *billingService) GetThirdPartyPaymentDetails(ctx context.Context, orderID int64) (*ThirdPartyPaymentOrder, error) {
	return s.repo.GetThirdPartyPaymentDetails(ctx, orderID)
}

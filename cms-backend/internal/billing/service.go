package billing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

var (
	ErrOrderNotFound               = errors.New("Order not found")
	ErrOrderAlreadyRefunded        = errors.New("Order has already been refunded")
	ErrOrderNotRefundable          = errors.New("Only successfully completed orders can be refunded")
	ErrTemplateSlotCountInvalid    = errors.New("Each recharge template slot count must be between 4 and 8")
	ErrTemplateSlotBonusTooHigh    = errors.New("Bonus coins cannot exceed 1.5 times of standard coins for single slots")
	ErrTemplateSlotInvalid         = errors.New("Each template slot must reference a distinct published product with slot_index 1..8")
	ErrTemplateProductNotPublished = errors.New("All template slots must reference products that are published and dual-channel ready")
	ErrTemplateNotFound            = errors.New("Recharge template not found")
	ErrDefaultTemplateNotDelete    = errors.New("Default recharge template cannot be deleted, please set another template as default first")
)

type BillingService interface {
	ListLedgerMovements(context.Context, movementFilter) (movementPage, error)
	ListSettlementPayouts(context.Context, payoutFilter) (payoutPage, error)
	ListSettlementReportIssues(context.Context, reconciliationPage) (reportIssuePage, error)
	GetPaymentActivity(context.Context, reconciliationScope) (paymentActivity, error)

	ListOrders(ctx context.Context, page, pageSize int, status, userID, orderType, promotionLinkID, paidStart, paidEnd, orderID string) ([]Order, int, error)
	GetThirdPartyPaymentDetails(ctx context.Context, orderID int64) (*ThirdPartyPaymentOrder, error)

	ListRechargeTemplates(ctx context.Context) ([]RechargeTemplate, error)
	ListVIPOfferPublications(ctx context.Context) ([]VIPOfferPublication, error)
	CreateRechargeTemplate(ctx context.Context, adminID string, name string, isDefault bool, slots []RechargeSlot) (int, error)
	UpdateRechargeTemplate(ctx context.Context, adminID string, id int, name string, isDefault bool, slots []RechargeSlot) error
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

func (s *billingService) ListOrders(ctx context.Context, page, pageSize int, status, userID, orderType, promotionLinkID, paidStart, paidEnd, orderID string) ([]Order, int, error) {
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
			COALESCE(payment_order_environment(recharge_orders),'')
		FROM recharge_orders
		%s
		ORDER BY created_at DESC
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

func validateTemplateSlotsTx(ctx context.Context, tx pgx.Tx, slots []RechargeSlot) error {
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
		if s.SlotIndex < 1 || s.SlotIndex > 8 || s.ProductID <= 0 || len(s.Badge) > 32 || len(s.DisplayAlias) > 64 {
			return ErrTemplateSlotInvalid
		}
		if seenIdx[s.SlotIndex] || seenProd[s.ProductID] {
			return ErrTemplateSlotInvalid
		}
		seenIdx[s.SlotIndex] = true
		seenProd[s.ProductID] = true
		productIDs = append(productIDs, s.ProductID)
	}
	var publishedCount int
	err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT p.id)
		FROM products p
		JOIN product_publications pub ON pub.product_id = p.id
		WHERE p.id = ANY($1::bigint[]) AND pub.status = 'published'`, productIDs).Scan(&publishedCount)
	if err != nil {
		return err
	}
	if publishedCount != len(productIDs) {
		return ErrTemplateProductNotPublished
	}
	return nil
}

func (s *billingService) CreateRechargeTemplate(ctx context.Context, adminID string, name string, isDefault bool, slots []RechargeSlot) (int, error) {
	if len(slots) < 4 || len(slots) > 8 {
		return 0, ErrTemplateSlotCountInvalid
	}

	tx, err := s.beginTemplateChange(ctx, adminID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if err := validateTemplateSlotsTx(ctx, tx, slots); err != nil {
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

	templateID, err := s.repo.CreateRechargeTemplateTx(ctx, tx, name, isDefault)
	if err != nil {
		return 0, err
	}

	for _, slot := range slots {
		err = s.repo.CreateRechargeSlotTx(ctx, tx, templateID, slot.SlotIndex, slot.ProductID, slot.Badge, slot.IsHighlighted, slot.DisplayAlias)
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

func (s *billingService) UpdateRechargeTemplate(ctx context.Context, adminID string, id int, name string, isDefault bool, slots []RechargeSlot) error {
	if len(slots) < 4 || len(slots) > 8 {
		return ErrTemplateSlotCountInvalid
	}

	tx, err := s.beginTemplateChange(ctx, adminID)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if err := validateTemplateSlotsTx(ctx, tx, slots); err != nil {
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

	affected, err := s.repo.UpdateRechargeTemplateTx(ctx, tx, id, name, isDefault)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrTemplateNotFound
	}

	err = s.repo.DeleteRechargeSlotsTx(ctx, tx, id)
	if err != nil {
		return err
	}

	for _, slot := range slots {
		err = s.repo.CreateRechargeSlotTx(ctx, tx, id, slot.SlotIndex, slot.ProductID, slot.Badge, slot.IsHighlighted, slot.DisplayAlias)
		if err != nil {
			return err
		}
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
	before, err := templateSnapshot(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM recharge_templates WHERE id=$1", id); err != nil {
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

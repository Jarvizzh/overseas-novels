package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

const settlementUploadLimit = 8 << 20

type settlementCommandInput struct {
	Kind            string `form:"kind" json:"kind" binding:"required,oneof=paypal_report stripe_payout stripe_report bank_statement bank_match"`
	Environment     string `form:"environment" json:"environment" binding:"required,oneof=sandbox live"`
	AccountID       int64  `form:"account_id" json:"account_id" binding:"min=0"`
	Reference       string `form:"reference" json:"reference" binding:"max=160"`
	BankAccount     string `form:"bank_account" json:"bank_account" binding:"max=160"`
	BankTransaction string `form:"bank_transaction" json:"bank_transaction" binding:"max=160"`
	BankEvidence    string `form:"bank_evidence" json:"bank_evidence" binding:"max=500"`
	Reason          string `form:"reason" json:"reason" binding:"required,max=500"`
	RequestKey      string `form:"request_key" json:"-" binding:"required,uuid"`
}

type settlementUpload struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

func (h *Handler) submitSettlementCommand(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, settlementUploadLimit+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Upload a valid form with at most 8 MB of report files"})
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	var input settlementCommandInput
	if c.ShouldBind(&input) != nil || strings.TrimSpace(input.Reason) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid settlement command"})
		return
	}
	files := c.Request.MultipartForm.File["files"]
	switch input.Kind {
	case "paypal_report":
		if input.AccountID == 0 || len(files) == 0 || len(files) > 20 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Select a PayPal account and its ordered report files"})
			return
		}
	case "bank_statement":
		if input.AccountID != 0 || input.Reference == "" || input.BankAccount == "" || len(files) != 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Provide the statement reference, bank account and one CSV"})
			return
		}
	case "bank_match":
		if input.AccountID == 0 || input.Reference == "" || input.BankAccount == "" || input.BankTransaction == "" || len(files) != 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Provide the payout, bank account and transaction identifier"})
			return
		}
	case "stripe_report", "stripe_payout":
		prefix := "po_"
		if input.Kind == "stripe_report" {
			prefix = "frr_"
		}
		if input.AccountID == 0 || !strings.HasPrefix(input.Reference, prefix) || len(files) != 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Provide the Stripe account and matching channel reference"})
			return
		}
	}
	var uploads []settlementUpload
	remaining := int64(settlementUploadLimit)
	for _, file := range files {
		if file.Size <= 0 || file.Size > remaining || len(filepath.Base(file.Filename)) > 255 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Report files exceed the allowed size"})
			return
		}
		source, err := file.Open()
		if err != nil {
			writeCaseResult(c, nil, err)
			return
		}
		content, err := io.ReadAll(io.LimitReader(source, remaining+1))
		source.Close()
		if err != nil || len(content) == 0 || int64(len(content)) > remaining {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Report file is empty, too large or unreadable"})
			return
		}
		remaining -= int64(len(content))
		uploads = append(uploads, settlementUpload{Name: filepath.Base(file.Filename), Data: content})
	}
	result, err := saveSettlementCommand(c.Request.Context(), c.GetString("admin_id"), input, uploads)
	writeCaseResult(c, result, err)
}

func saveSettlementCommand(ctx context.Context, actor string, input settlementCommandInput, uploads []settlementUpload) (gin.H, error) {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireFinancialAdmin(ctx, tx, actor); err != nil {
		return nil, errCaseForbidden
	}
	if input.AccountID != 0 {
		var provider string
		err := tx.QueryRow(ctx, `SELECT provider FROM payment_channel_accounts WHERE id=$1 AND environment=$2`, input.AccountID, input.Environment).Scan(&provider)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errCaseConflict
		}
		if err != nil {
			return nil, err
		}
		if (input.Kind == "paypal_report" && provider != "paypal") || (strings.HasPrefix(input.Kind, "stripe_") && provider != "stripe") {
			return nil, errCaseConflict
		}
	}
	params, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var uploadData []byte
	if len(uploads) > 0 {
		uploadData, err = json.Marshal(uploads)
		if err != nil {
			return nil, err
		}
	}
	digest := sha256.New()
	digest.Write(params)
	digest.Write(uploadData)
	var id, status string
	err = tx.QueryRow(ctx, `INSERT INTO payment_settlement_commands(id,request_key,actor_id,kind,account_id,environment,reason,parameters,upload,request_digest)
		VALUES($1,$2,$3,$4,NULLIF($5::bigint,0),$6,$7,$8::jsonb,$9,$10)
		ON CONFLICT(actor_id,request_key) DO UPDATE SET request_key=EXCLUDED.request_key
		WHERE payment_settlement_commands.request_digest=EXCLUDED.request_digest RETURNING id::text,status`,
		uuid.New(), input.RequestKey, actor, input.Kind, input.AccountID, input.Environment, input.Reason, params, uploadData, hex.EncodeToString(digest.Sum(nil))).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errCaseConflict
	}
	if err != nil {
		return nil, err
	}
	return gin.H{"id": id, "status": status}, tx.Commit(ctx)
}

func (h *Handler) listSettlementCommands(c *gin.Context) {
	environment := c.Query("environment")
	if environment != "live" && environment != "sandbox" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Select a payment environment"})
		return
	}
	var result json.RawMessage
	err := db.DB.QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(op) ORDER BY op.created_at DESC),'[]') FROM (
		SELECT id::text,kind,environment,status,parameters->>'reference' AS reference,result,error,actor_id,reason,created_at,updated_at
		FROM payment_settlement_commands WHERE environment=$1 ORDER BY created_at DESC,id LIMIT 30) op`, environment).Scan(&result)
	writeCaseResult(c, gin.H{"commands": result}, err)
}

func (h *Handler) listSettlementAccounts(c *gin.Context) {
	var result json.RawMessage
	err := db.DB.QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'id',a.id,'provider',a.provider,'environment',a.environment,'label','••••'||right(a.account_key,4),
		'auto_enabled',COALESCE(s.enabled,false),'auto_error',s.last_error,'auto_completed_at',s.last_complete_at)
		ORDER BY a.provider,a.environment,a.id),'[]')
		FROM payment_channel_accounts a LEFT JOIN payment_ledger_sync_settings s ON s.account_id=a.id`).Scan(&result)
	writeCaseResult(c, gin.H{"accounts": result}, err)
}

func (h *Handler) updateAutomaticLedger(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var input struct {
		Enabled *bool  `json:"enabled" binding:"required"`
		Reason  string `json:"reason" binding:"required,max=500"`
	}
	if err != nil || id <= 0 || c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Reason) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provide the payment account, setting and audit reason"})
		return
	}
	err = saveAutomaticLedger(c.Request.Context(), id, c.GetString("admin_id"), *input.Enabled, strings.TrimSpace(input.Reason))
	writeCaseResult(c, gin.H{"updated": true}, err)
}

func saveAutomaticLedger(ctx context.Context, id int64, actor string, enabled bool, reason string) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireFinancialAdmin(ctx, tx, actor); err != nil {
		return errCaseForbidden
	}
	var account int64
	if err := tx.QueryRow(ctx, `SELECT id FROM payment_channel_accounts WHERE id=$1 FOR UPDATE`, id).Scan(&account); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH prior AS MATERIALIZED (
		SELECT enabled FROM payment_ledger_sync_settings WHERE account_id=$1
	), saved AS (
		INSERT INTO payment_ledger_sync_settings(account_id,enabled,actor_id,reason,window_start,window_end)
		VALUES($1,$2,$3,$4,now()-interval '7 days',now())
		ON CONFLICT(account_id) DO UPDATE SET enabled=EXCLUDED.enabled,actor_id=EXCLUDED.actor_id,reason=EXCLUDED.reason,
			next_attempt_at=CASE WHEN EXCLUDED.enabled AND NOT payment_ledger_sync_settings.enabled THEN now() ELSE payment_ledger_sync_settings.next_attempt_at END
		RETURNING enabled
	) INSERT INTO admin_audit_logs(admin_id,action,target_id,before_data,after_data)
	SELECT $3,'configure_automatic_payment_ledger',$1::bigint::text,(SELECT to_jsonb(prior) FROM prior),
		jsonb_build_object('enabled',saved.enabled,'reason',$4::text) FROM saved`, account, enabled, actor, reason)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

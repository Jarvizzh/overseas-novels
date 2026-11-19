package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
)

type MockPartnerSettings struct {
	Config       json.RawMessage
	InboundToken string
	SigningSeed  string
}

func (s Service) ConfigureSinanMock(ctx context.Context, actor Operator, id int64, reason string,
	input MockPartnerSettings, encrypt EncryptCredential,
) (OperationResult, error) {
	if s.Environment != "test" || id <= 0 || !json.Valid(input.Config) {
		return OperationResult{}, ErrInvalidOperation
	}
	return s.operate(ctx, actor, reason, func(tx pgx.Tx, operator Operator, reason string) (OperationResult, error) {
		var inbound, report *int64
		var before json.RawMessage
		err := tx.QueryRow(ctx, `
			SELECT inbound_credential_id,report_credential_id,report_config FROM conversion.integrations
			WHERE id=$1 AND source='sinan' AND environment=$2 AND contract_version='sinan-mock-v1' FOR UPDATE`,
			id, s.Environment).Scan(&inbound, &report, &before)
		if err != nil {
			return OperationResult{}, err
		}
		oldInbound, oldReport := inbound != nil, report != nil
		inbound, err = replacePartnerCredential(ctx, tx, inbound, input.InboundToken, encrypt)
		if err != nil {
			return OperationResult{}, err
		}
		report, err = replacePartnerCredential(ctx, tx, report, input.SigningSeed, encrypt)
		if err != nil {
			return OperationResult{}, err
		}
		if inbound == nil || report == nil {
			return OperationResult{}, ErrInvalidOperation
		}
		if _, err = tx.Exec(ctx, `UPDATE conversion.integrations
			SET report_config=$2,inbound_credential_id=$3,report_credential_id=$4 WHERE id=$1`,
			id, input.Config, inbound, report); err != nil {
			return OperationResult{}, err
		}
		out := OperationResult{ID: strconv.FormatInt(id, 10), Action: "configure_sinan_mock"}
		err = s.audit(ctx, tx, operator, out.Action, out.ID, reason, "",
			map[string]any{"config": before, "inbound_configured": oldInbound, "report_configured": oldReport},
			map[string]any{"config": input.Config, "inbound_configured": true, "report_configured": true,
				"inbound_replaced": input.InboundToken != "", "report_replaced": input.SigningSeed != ""})
		return out, err
	})
}

func replacePartnerCredential(ctx context.Context, tx pgx.Tx, current *int64, value string, encrypt EncryptCredential) (*int64, error) {
	if value == "" {
		return current, nil
	}
	if encrypt == nil {
		return nil, errors.New("credential key version unavailable")
	}
	cipher, version, err := encrypt(value)
	if err != nil {
		return nil, err
	}
	id, err := insertCredential(ctx, tx, cipher, version)
	return &id, err
}

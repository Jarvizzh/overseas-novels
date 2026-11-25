package settlement_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"reader-backend/internal/payment/settlement"
	"reader-backend/internal/testdb"
)

func TestBankStatementCommandExecutesOnlyInItsEnvironment(t *testing.T) {
	pool := testdb.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO admins(id,username,password_hash,role,role_id,status)
		VALUES('fixture-finance','fixture-finance','none','FinanceManager',(SELECT id FROM roles WHERE code='FinanceManager'),1)`); err != nil {
		t.Fatal(err)
	}
	files, err := json.Marshal([]settlement.Upload{{Name: "b.csv", Data: []byte("transaction_id,booked_at,currency,amount_minor,bank_reference\nb1,2026-11-21T00:00:00Z,USD,900,TRACE\n")}})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_settlement_commands(id,request_key,actor_id,kind,environment,reason,parameters,upload,request_digest)
		VALUES($1,$1,'fixture-finance','bank_statement','sandbox','bank fixture','{"reference":"qa","bank_account":"operating"}',$2,'digest')`, id, files); err != nil {
		t.Fatal(err)
	}
	repo := settlement.NewRepository(pool)
	for _, scenario := range []struct {
		environment string
		work        bool
	}{{"live", false}, {"sandbox", true}, {"sandbox", false}} {
		ok, err := repo.ExecuteNextCommand(ctx, scenario.environment)
		if err != nil || ok != scenario.work {
			t.Fatalf("environment %s work=%v error=%v", scenario.environment, ok, err)
		}
	}
	var status string
	var rows int
	var cleared bool
	if err := pool.QueryRow(ctx, `SELECT status,COALESCE((result->>'rows')::int,0),upload IS NULL FROM payment_settlement_commands WHERE id=$1`, id).Scan(&status, &rows, &cleared); err != nil ||
		status != "succeeded" || rows != 1 || !cleared {
		t.Fatalf("status=%q rows=%d cleared=%v error=%v", status, rows, cleared, err)
	}
}

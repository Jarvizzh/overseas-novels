package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"conversion-backend/internal/paymentpolicy"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPaymentPolicyAPIAndConcurrentDecisions(t *testing.T) {
	ctx, owner := operationsTestPool(t)
	mustExec(t, ctx, owner, `
		INSERT INTO admins(id,username,password_hash,role,role_id) VALUES ('ops','ops','fixture','Admin',(SELECT id FROM roles WHERE code='Admin'));
		INSERT INTO conversion.integrations(environment,source,account_key,contract_version,allowed_apps)
		VALUES ('test','starnovel','reader','v1','{}'),('test','sinan','partner','v1','{9}'),
		       ('other','starnovel','reader','v1','{}');
		INSERT INTO conversion.destinations(environment,media,pixel_id) VALUES ('test','meta','123'),('test','meta','456');
		INSERT INTO novels(id,title,author) VALUES (1,'fixture','fixture');
		INSERT INTO promotion_links(id,novel_id,novel_title,generated_url,created_at)
		VALUES (13,1,'fixture','https://reader.example/13',now()-interval '1 day'),(14,1,'fixture','https://reader.example/14',now()-interval '1 day');
	`)
	worker, api := restrictedPolicyPools(t, ctx, owner)
	handler := NewHandler(Repository{Pool: api, Environment: "test"}, "service-fixture")
	call := func(method, path, role, body string, status int, want string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/internal/conversion"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer service-fixture")
		req.Header.Set("X-Operator-ID", "ops")
		req.Header.Set("X-Operator-Role", role)
		if role == "FinanceManager" {
			req.Header.Set("X-Operator-Access", "read")
		} else {
			req.Header.Set("X-Operator-Access", "write")
			if method != http.MethodGet {
				req.Header.Set("X-Operator-Permission", "ads.conversions:write")
			}
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != status || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s %s = %d %s, want %d %q", method, path, rec.Code, rec.Body.String(), status, want)
		}
		return rec
	}
	const policyPath = "/integrations/1/payment-policy"
	const firstPaid = `{"app_id":"reader","expected_version":null,"payment_range":"first_paid_on_promotion","reason":"fixture"}`
	call(http.MethodGet, policyPath+"?app_id=reader", "FinanceManager", "", 200, "implicit_all")
	call(http.MethodGet, "/integrations/3/payment-policy?app_id=reader", "FinanceManager", "", 404, "resource_not_found")
	call(http.MethodPost, policyPath+"/preview", "FinanceManager", firstPaid, 403, "operator_forbidden")
	call(http.MethodPost, policyPath+"/publish", "Admin", firstPaid, 400, "payment_policy_first_requires_promotion")
	call(http.MethodPost, policyPath+"/preview", "Admin",
		strings.Replace(firstPaid, `"app_id":"reader"`, `"app_id":"reader","promotion_id":"13"`, 1), 400, "payment_policy_first_coverage_unverified")
	call(http.MethodPost, "/integrations/2/payment-policy/publish", "Admin",
		strings.Replace(firstPaid, `"reader"`, `"9"`, 1), 400, "payment_policy_first_source_unsupported")
	call(http.MethodGet, "/integrations/2/payment-policy?app_id=8", "FinanceManager", "", 400, "invalid_payment_policy_app")

	link := paymentpolicy.Scope{Environment: "test", IntegrationID: 1, App: paymentpolicy.ReaderApp, Promotion: "13"}
	makeFacts := func(scope paymentpolicy.Scope, key string) paymentpolicy.Facts {
		amount := int64(5000)
		return paymentpolicy.Facts{Scope: scope, PaymentKey: key, Destination: 1, Kind: "single", AmountMinor: &amount, Currency: "USD"}
	}
	decide := func(f paymentpolicy.Facts) (ret paymentpolicy.Decision, err error) {
		err = pgx.BeginFunc(ctx, worker, func(tx pgx.Tx) error {
			ret, err = paymentpolicy.DecideTx(ctx, tx, f)
			return err
		})
		return ret, err
	}
	mustDecide := func(f paymentpolicy.Facts) paymentpolicy.Decision {
		t.Helper()
		result, err := decide(f)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	prior := mustDecide(makeFacts(link, "previous"))
	if prior.VersionID != nil || prior.Outcome != paymentpolicy.Selected || prior.Reason != "rule_no_match_send" {
		t.Fatalf("implicit decision = %+v", prior)
	}

	const initial = `{"app_id":"reader","expected_version":null,"publication_id":"00000000000000000000000000000001","operations":[
		{"op":"add_band","currency":"USD","bands":[{"min_minor":"3000","max_minor":"9000","mode":"cycle","send_count":3,"skip_count":2}]}]}`
	assertPolicyCount(t, ctx, owner, "payment_policy_versions", 0)
	var base paymentpolicy.View
	decodePolicyView(t, call(http.MethodPost, policyPath+"/publish", "Admin", initial, 200, "own"), &base)
	call(http.MethodPost, policyPath+"/publish", "Admin", initial, 409, "payment_policy_version_conflict")
	assertPolicyCount(t, ctx, owner, "operation_audit", 1)
	var initialAuditReason, publicationID string
	if err := owner.QueryRow(ctx, `SELECT reason,after_state->>'publication_id' FROM conversion.operation_audit`).
		Scan(&initialAuditReason, &publicationID); err != nil ||
		initialAuditReason != "新建回传规则" || publicationID != "00000000000000000000000000000001" {
		t.Fatalf("initial audit reason=%q publication=%q err=%v", initialAuditReason, publicationID, err)
	}
	if got := mustDecide(makeFacts(link, "previous")); got.ID != prior.ID || got.VersionID != nil {
		t.Fatalf("previous result changed = %+v", got)
	}

	rollback := errors.New("injected downstream failure")
	if err := pgx.BeginFunc(ctx, worker, func(tx pgx.Tx) error {
		result, err := paymentpolicy.DecideTx(ctx, tx, makeFacts(link, "rolled-back"))
		if err != nil || result.CycleSequence == nil || *result.CycleSequence != 1 {
			t.Fatalf("rollback fixture: %+v, %v", result, err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	assertPolicyCount(t, ctx, owner, "payment_policy_counters", 0)

	var wg sync.WaitGroup
	results := make(chan paymentpolicy.Decision, 20)
	for i := range 20 {
		wg.Go(func() {
			result, err := decide(makeFacts(link, fmt.Sprintf("payment-%d", i%10)))
			if err != nil {
				t.Error(err)
				return
			}
			results <- result
		})
	}
	wg.Wait()
	close(results)
	sequences := make(map[int64]int)
	selected := make(map[int64]bool)
	for result := range results {
		if result.CycleSequence == nil || result.VersionID == nil || *result.VersionID != base.OwnVersion.ID {
			t.Fatalf("concurrent decision = %+v", result)
		}
		sequences[*result.CycleSequence]++
		if result.Outcome == paymentpolicy.Selected {
			selected[*result.CycleSequence] = true
		}
	}
	if len(sequences) != 10 || len(selected) != 6 {
		t.Fatalf("sequences=%v, selected=%v", sequences, selected)
	}
	for seq := int64(1); seq <= 10; seq++ {
		if sequences[seq] != 2 {
			t.Fatalf("missing decision reuse at sequence %d", seq)
		}
	}
	nonMatching := makeFacts(link, "other-currency")
	nonMatching.Currency = "EUR"
	if got := mustDecide(nonMatching); got.Outcome != paymentpolicy.Selected || got.Reason != "rule_no_match_send" ||
		got.VersionID == nil || *got.VersionID != base.OwnVersion.ID || got.CycleSequence != nil {
		t.Fatalf("unmatched decision = %+v", got)
	}
	changed := makeFacts(link, "payment-0")
	changed.Destination = 2
	if _, err := decide(changed); !errors.Is(err, paymentpolicy.ErrPaymentConflict) {
		t.Fatalf("payment conflict = %v", err)
	}
	for _, promotion := range []string{"", "14"} {
		scope := link
		scope.Promotion = promotion
		if got := mustDecide(makeFacts(scope, "scope-"+promotion)); got.CycleSequence == nil || *got.CycleSequence != 1 {
			t.Fatalf("scope %q: %+v", promotion, got)
		}
	}

	branchID := base.Config.Groups[0].Bands[0].ID
	const split = `{"app_id":"reader","promotion_id":"13","expected_version":null,"expected_inherited_version":"%d","reason":"split",
		"operations":[{"op":"split_band","branch_id":"%d","bands":[
			{"min_minor":"3000","max_minor":"6000","mode":"cycle","send_count":1,"skip_count":1},
			{"min_minor":"6000","max_minor":"9000","mode":"cycle","send_count":1,"skip_count":1}]}]}`
	body := fmt.Sprintf(split, base.OwnVersion.ID, branchID)
	call(http.MethodPost, policyPath+"/preview", "Admin", body, 200, `"next_sequence":"11"`)
	var dedicated paymentpolicy.View
	decodePolicyView(t, call(http.MethodPost, policyPath+"/publish", "Admin", body, 200, `"next_sequence":"11"`), &dedicated)
	bands := dedicated.Config.Groups[0].Bands
	if bands[0].ID != branchID || bands[1].ID == branchID || bands[1].ID == 0 {
		t.Fatalf("split inheritance = %+v", bands)
	}
	for i, amount := range []int64{3000, 6000} {
		f := makeFacts(link, fmt.Sprintf("split-%d", i))
		f.AmountMinor = &amount
		got := mustDecide(f)
		if got.CycleSequence == nil || *got.CycleSequence != []int64{11, 1}[i] || got.BranchID == nil || *got.BranchID != bands[i].ID {
			t.Fatalf("split decision = %+v", got)
		}
	}
	const merge = `{"app_id":"reader","promotion_id":"13","expected_version":"%d","reason":"merge",
		"operations":[{"op":"merge_bands","branch_ids":["%d","%d"],"bands":[
			{"min_minor":"3000","max_minor":"9000","mode":"cycle","send_count":1,"skip_count":1}]}]}`
	var merged paymentpolicy.View
	decodePolicyView(t, call(http.MethodPost, policyPath+"/publish", "Admin",
		fmt.Sprintf(merge, dedicated.OwnVersion.ID, bands[1].ID, bands[0].ID), 200, `"next_sequence":"12"`), &merged)
	if got := mustDecide(makeFacts(link, "merged")); got.CycleSequence == nil || *got.CycleSequence != 12 || got.Outcome != paymentpolicy.Skipped {
		t.Fatalf("merge decision = %+v", got)
	}

	mustExec(t, ctx, owner, `INSERT INTO reader_payment_promotion_coverage VALUES ('test',13,now(),'fixture')`)
	const publishMode = `{"app_id":"reader","promotion_id":"13","expected_version":"%d",%s
		"operations":[{"op":"update_band","branch_id":"%d","bands":[{"min_minor":"3000","max_minor":"9000",%s}]}]}`
	var ratio paymentpolicy.View
	decodePolicyView(t, call(http.MethodPost, policyPath+"/publish", "Admin",
		fmt.Sprintf(publishMode, merged.OwnVersion.ID, `"payment_range":"first_paid_on_promotion",`, branchID, `"mode":"ratio","rate_bps":0`), 200, "first_paid_on_promotion"), &ratio)
	missing := makeFacts(link, "first-evidence")
	if err := pgx.BeginFunc(ctx, worker, func(tx pgx.Tx) error {
		_, err := paymentpolicy.DecideTx(ctx, tx, missing)
		if !errors.Is(err, paymentpolicy.ErrFactMissing) {
			t.Fatalf("missing first-payment evidence: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	truth := true
	missing.First = &truth
	if got := mustDecide(missing); got.Outcome != paymentpolicy.Skipped || got.SampleVersion == nil || got.SampleBucket == nil {
		t.Fatalf("ratio decision = %+v", got)
	}
	var restored paymentpolicy.View
	decodePolicyView(t, call(http.MethodPost, policyPath+"/publish", "Admin",
		fmt.Sprintf(publishMode, ratio.OwnVersion.ID, `"payment_range":"all_paid",`, branchID, `"mode":"cycle","send_count":1,"skip_count":1`), 200, `"next_sequence":"13"`), &restored)
	call(http.MethodPost, policyPath+"/publish", "Admin",
		fmt.Sprintf(`{"app_id":"reader","promotion_id":"13","expected_version":"%d","expected_inherited_version":"%d","reason":"inherit","deactivate":true}`, restored.OwnVersion.ID, base.OwnVersion.ID), 200, `"config_source":"inherited"`)
	if got := mustDecide(makeFacts(link, "re-inherited")); got.CycleSequence == nil || *got.CycleSequence != 13 {
		t.Fatalf("inheritance decision = %+v", got)
	}

	rows, err := owner.Query(ctx, `SELECT action FROM conversion.operation_audit`)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := pgx.CollectRows(rows, pgx.RowTo[string])
	sort.Strings(actions)
	if err != nil || len(actions) != 6 || actions[0] != "inherit_payment_policy" || actions[5] != "publish_payment_policy" {
		t.Fatalf("audit actions = %v, %v", actions, err)
	}
	var created, changedRules int
	if err := owner.QueryRow(ctx, `SELECT count(*) FILTER (WHERE reason='新建回传规则'),
		count(*) FILTER (WHERE reason='变更回传规则') FROM conversion.operation_audit`).Scan(&created, &changedRules); err != nil || created != 2 || changedRules != 4 {
		t.Fatalf("audit reasons: created=%d changed=%d err=%v", created, changedRules, err)
	}
	// Hold the worker's initial decision transaction open and prove the new
	// version cannot publish until that decision has committed.
	workerTx, err := worker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer workerTx.Rollback(ctx)
	lockedFacts := makeFacts(link, "publishing-during-decision")
	oldDecision, err := paymentpolicy.DecideTx(ctx, workerTx, lockedFacts)
	if err != nil || oldDecision.VersionID == nil || *oldDecision.VersionID != base.OwnVersion.ID {
		t.Fatalf("decision holding publication lock = %+v, %v", oldDecision, err)
	}
	defaultScope := link
	defaultScope.Promotion = ""
	err = pgx.BeginFunc(ctx, api, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '100ms'"); err != nil {
			return err
		}
		_, _, err := paymentpolicy.PublishTx(ctx, tx, defaultScope, paymentpolicy.Draft{
			ExpectedVersion: &base.OwnVersion.ID,
		}, "ops")
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("publish should wait for the decision transaction, got %v", err)
	}
	if err := workerTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var latest paymentpolicy.View
	decodePolicyView(t, call(http.MethodPost, policyPath+"/publish", "Admin",
		fmt.Sprintf(`{"app_id":"reader","expected_version":"%d","reason":"set all",
			"operations":[{"op":"update_band","branch_id":"%d","bands":[{"min_minor":"3000","max_minor":"9000","mode":"all"}]}]}`,
			base.OwnVersion.ID, branchID), 200, `"mode":"all"`), &latest)
	if got := mustDecide(lockedFacts); got.ID != oldDecision.ID || got.VersionID == nil || *got.VersionID != base.OwnVersion.ID {
		t.Fatalf("concurrent publication altered the committed decision: %+v", got)
	}
	if got := mustDecide(makeFacts(link, "after-publish")); got.VersionID == nil ||
		*got.VersionID != latest.OwnVersion.ID || got.CycleSequence != nil || got.Outcome != paymentpolicy.Selected {
		t.Fatalf("next payment did not see the complete new version: %+v", got)
	}
}

func restrictedPolicyPools(t *testing.T, ctx context.Context, owner *pgxpool.Pool) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	raw, err := os.ReadFile("../../../cms-backend/ops/conversion_role_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.TrimPrefix(string(raw), "\\set ON_ERROR_STOP on\n")
	roles := make(map[string]string)
	for _, kind := range []string{"reader", "cms", "worker", "api", "collector", "retention"} {
		name := pgx.Identifier{owner.Config().ConnConfig.Database + "_" + kind}.Sanitize()
		roles[kind] = name
		mustExec(t, ctx, owner, "CREATE ROLE "+name+" NOLOGIN")
		t.Cleanup(func() { mustExec(t, context.Background(), owner, "DROP OWNED BY "+name+"; DROP ROLE "+name) })
		sql = strings.ReplaceAll(sql, `:"`+kind+`_role"`, name)
	}
	mustExec(t, ctx, owner, sql)
	newPool := func(kind string) *pgxpool.Pool {
		cfg := owner.Config()
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET ROLE "+roles[kind])
			return err
		}
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	return newPool("worker"), newPool("api")
}

func decodePolicyView(t *testing.T, r *httptest.ResponseRecorder, out *paymentpolicy.View) {
	t.Helper()
	if err := json.Unmarshal(r.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
}

func assertPolicyCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{"conversion", table}.Sanitize()).Scan(&got); err != nil || got != want {
		t.Fatalf("%s count=%d, want %d (%v)", table, got, want, err)
	}
}

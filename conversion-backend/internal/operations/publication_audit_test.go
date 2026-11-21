package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"conversion-backend/internal/paymentpolicy"
)

func TestPublicationAuditMatchesCommittedScope(t *testing.T) {
	ctx, owner := operationsTestPool(t)
	mustExec(t, ctx, owner, `
		INSERT INTO admins(id,username,password_hash,role) VALUES ('ops','ops','fixture','Admin');
		INSERT INTO conversion.integrations(environment,source,account_key,contract_version)
		VALUES ('test','starnovel','reader','v1'),('other','starnovel','reader','v1');
		INSERT INTO conversion.integrations(environment,source,account_key,contract_version,allowed_apps)
		VALUES ('test','sinan','partner','v1','{123,456}');
		INSERT INTO novels(id,title,author) VALUES (1,'fixture','fixture');
		INSERT INTO promotion_links(id,novel_id,novel_title,generated_url)
		VALUES (13,1,'fixture','https://reader.example/13'),(14,1,'fixture','https://reader.example/14');
	`)
	indexes, err := os.ReadFile("../../../cms-backend/db/conversion_query_indexes.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range strings.Split(string(indexes), ";") {
		if strings.TrimSpace(statement) != "" {
			mustExec(t, ctx, owner, statement)
		}
	}
	_, api := restrictedPolicyPools(t, ctx, owner)
	handler := NewHandler(Repository{Pool: api, Environment: "test"}, "service-fixture")
	call := func(method, path, actor string, body []byte, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/internal/conversion"+path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer service-fixture")
		req.Header.Set("X-Operator-ID", actor)
		req.Header.Set("X-Operator-Role", "Admin")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d %s, want %d", method, path, rec.Code, rec.Body.String(), want)
		}
		return rec
	}
	const publishPath = "/integrations/1/payment-policy/publish"
	publish := func(draft paymentpolicy.Draft) paymentpolicy.View {
		t.Helper()
		body, err := json.Marshal(draft)
		if err != nil {
			t.Fatal(err)
		}
		var view paymentpolicy.View
		decodePolicyView(t, call(http.MethodPost, publishPath, "ops", body, http.StatusOK), &view)
		return view
	}
	fallback := []paymentpolicy.Operation{{Type: "set_fallback", Action: &paymentpolicy.Action{Mode: paymentpolicy.All}}}
	base := publish(paymentpolicy.Draft{App: "reader", Operations: fallback})
	link := publish(paymentpolicy.Draft{App: "reader", Promotion: "13", ExpectedInherited: &base.OwnVersion.ID, Operations: fallback})
	newBase := publish(paymentpolicy.Draft{App: "reader", ExpectedVersion: &base.OwnVersion.ID, Operations: fallback})
	const publicationID = "0000000000000000000000000000a016"
	published := publish(paymentpolicy.Draft{
		App: "reader", Promotion: "13", ExpectedVersion: &link.OwnVersion.ID,
		ExpectedInherited: &base.OwnVersion.ID, PublicationID: publicationID, Operations: fallback,
	})
	if published.InheritedVersion.ID != newBase.OwnVersion.ID {
		t.Fatal("fixture did not publish over the changed parent version")
	}
	auditPath := func(integration, app, promotion, id string) string {
		query := url.Values{"subject_type": {"integration"}, "subject_id": {integration},
			"publication_id": {id}, "app_id": {app}, "promotion_id": {promotion}}
		return "/audit?" + query.Encode()
	}
	get := func(actor, integration, app, promotion, id string) publicationAuditResult {
		t.Helper()
		res := call(http.MethodGet, auditPath(integration, app, promotion, id), actor, nil, http.StatusOK)
		var receipt publicationAuditResult
		if err := json.Unmarshal(res.Body.Bytes(), &receipt); err != nil || receipt.PublicationID != id {
			t.Fatalf("unexpected receipt: %s %v", res.Body, err)
		}
		return receipt
	}
	assertReceipt := func(receipt publicationAuditResult, current int64) {
		t.Helper()
		if receipt.CurrentStatus != "available" || receipt.Current == nil ||
			receipt.Current.OwnVersion == nil || receipt.Current.OwnVersion.ID != current ||
			receipt.Record == nil || receipt.Record.Action != "publish_payment_policy" ||
			receipt.Record.VersionID == nil || *receipt.Record.VersionID != fmt.Sprint(published.OwnVersion.ID) ||
			receipt.Record.VersionNumber == nil || *receipt.Record.VersionNumber != fmt.Sprint(published.OwnVersion.Number) {
			t.Fatalf("wrong committed or current version: %+v %+v", receipt.Record, receipt.Current)
		}
	}
	assertReceipt(get("ops", "1", "reader", "13", publicationID), published.OwnVersion.ID)

	latest := publish(paymentpolicy.Draft{App: "reader", Promotion: "13", ExpectedVersion: &published.OwnVersion.ID, Operations: fallback})
	mustExec(t, ctx, owner, `
		INSERT INTO conversion.operation_audit(environment,action,subject_id,operator_id,operator_role,reason,before_state,after_state)
		SELECT 'test','configure_integration','1','ops','Admin','later audit','{}','{}' FROM generate_series(1,25);
		INSERT INTO conversion.operation_audit(environment,action,subject_id,operator_id,operator_role,reason,before_state,after_state)
		SELECT 'other','publish_payment_policy','1','ops','Admin','different environment',before_state,after_state
		FROM conversion.operation_audit WHERE after_state->>'publication_id'='0000000000000000000000000000a016';
	`)
	assertReceipt(get("ops", "1", "reader", "13", publicationID), latest.OwnVersion.ID)
	const missingID = "0000000000000000000000000000a017"
	missing := get("ops", "1", "reader", "13", missingID)
	if missing.Record != nil || missing.Current.OwnVersion.ID != latest.OwnVersion.ID {
		t.Fatalf("missing publication must return current config: %+v", missing)
	}
	if get("other-operator", "1", "reader", "13", publicationID).Record != nil ||
		get("ops", "1", "reader", "14", publicationID).Record != nil ||
		get("ops", "1", "reader", "", publicationID).Record != nil {
		t.Fatal("publication lookup crossed the operator or promotion boundary")
	}
	call(http.MethodGet, auditPath("2", "reader", "13", publicationID), "ops", nil, http.StatusNotFound)
	call(http.MethodGet, auditPath("1", "reader", "13", "bad-token"), "ops", nil, http.StatusBadRequest)

	t.Run("waits for in-flight publication audit", func(t *testing.T) {
		tx, err := owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err := tx.Exec(ctx, "SELECT id FROM conversion.integrations WHERE id=1 FOR UPDATE"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversion.operation_audit(environment,action,subject_id,operator_id,operator_role,reason,before_state,after_state)
			SELECT 'test','publish_payment_policy','1','ops','Admin','in-flight',before_state,
			       jsonb_set(after_state,'{publication_id}',to_jsonb($1::text))
			FROM conversion.operation_audit WHERE environment='test' AND after_state->>'publication_id'=$2`, missingID, publicationID); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/internal/conversion"+auditPath("1", "reader", "13", missingID), nil)
		req.Header.Set("Authorization", "Bearer service-fixture")
		req.Header.Set("X-Operator-ID", "ops")
		req.Header.Set("X-Operator-Role", "Admin")
		response := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			response <- rec
		}()
		limit := time.Now().Add(2 * time.Second)
		for {
			var waiting bool
			if err := owner.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			    WHERE wait_event_type='Lock' AND query LIKE 'SELECT source FROM conversion.integrations%FOR SHARE')`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			if time.Now().After(limit) {
				t.Fatal("receipt query never waited for the publishing transaction")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		res := <-response
		var receipt publicationAuditResult
		if err := json.Unmarshal(res.Body.Bytes(), &receipt); err != nil || res.Code != http.StatusOK {
			t.Fatalf("in-flight receipt: %d %s %v", res.Code, res.Body, err)
		}
		assertReceipt(receipt, latest.OwnVersion.ID)
	})

	t.Run("confirms publication after app authorization is revoked", func(t *testing.T) {
		const id = "0000000000000000000000000000a018"
		const path = "/integrations/3/payment-policy"
		body, err := json.Marshal(paymentpolicy.Draft{
			App: "123", Promotion: "13", PublicationID: id, Operations: fallback,
		})
		if err != nil {
			t.Fatal(err)
		}
		var published paymentpolicy.View
		decodePolicyView(t, call(http.MethodPost, path+"/publish", "ops", body, http.StatusOK), &published)
		mustExec(t, ctx, owner, `UPDATE conversion.integrations SET allowed_apps='{456}' WHERE id=3`)

		receipt := get("ops", "3", "123", "13", id)
		if receipt.CurrentStatus != "app_unauthorized" || receipt.Current != nil ||
			receipt.Record == nil || receipt.Record.Action != "publish_payment_policy" ||
			receipt.Record.VersionID == nil || *receipt.Record.VersionID != fmt.Sprint(published.OwnVersion.ID) ||
			receipt.Record.VersionNumber == nil || *receipt.Record.VersionNumber != fmt.Sprint(published.OwnVersion.Number) {
			t.Fatalf("wrong receipt for revoked app: %+v, record: %+v", receipt, receipt.Record)
		}
		for _, other := range []publicationAuditResult{
			get("another-operator", "3", "123", "13", id),
			get("ops", "3", "123", "14", id),
			get("ops", "3", "456", "13", id),
			get("ops", "3", "123", "13", missingID),
		} {
			if other.Record != nil {
				t.Fatalf("publication lookup crossed operator, app, promotion or publication ID: %+v", other)
			}
		}
		call(http.MethodGet, path+"?app_id=123&promotion_id=13", "ops", nil, http.StatusBadRequest)
		call(http.MethodPost, path+"/publish", "ops", body, http.StatusBadRequest)
	})

	t.Run("publishes a full batch without reason", func(t *testing.T) {
		draft := paymentpolicy.Draft{App: "reader", ExpectedVersion: &newBase.OwnVersion.ID}
		for i := range 100 {
			min, max := int64(i)*1000, int64(i+1)*1000
			draft.Operations = append(draft.Operations, paymentpolicy.Operation{
				Type: "add_band", Currency: "USD", Bands: []paymentpolicy.BandInput{{
					Min: &min, Max: &max, Action: paymentpolicy.Action{Mode: paymentpolicy.All},
				}},
			})
		}
		body, err := json.Marshal(draft)
		if err != nil || len(body) <= 8<<10 {
			t.Fatalf("large fixture: %d bytes %v", len(body), err)
		}
		call(http.MethodPost, publishPath, "ops", body, http.StatusOK)
		call(http.MethodPost, publishPath, "ops", []byte(`{"app_id":"reader","unexpected":"value"}`),
			http.StatusBadRequest)
	})

	t.Run("restore inheritance rejects stale default versions", func(t *testing.T) {
		for _, scope := range []struct{ integration, app, promotion string }{
			{"1", "reader", "14"}, {"3", "456", "14"},
		} {
			path := "/integrations/" + scope.integration + "/payment-policy"
			var base paymentpolicy.View
			decodePolicyView(t, call(http.MethodGet, path+"?app_id="+scope.app, "ops", nil, http.StatusOK), &base)
			var inherited *int64
			if base.OwnVersion != nil {
				inherited = &base.OwnVersion.ID
			}
			submit := func(draft paymentpolicy.Draft, status int) *httptest.ResponseRecorder {
				t.Helper()
				body, err := json.Marshal(draft)
				if err != nil {
					t.Fatal(err)
				}
				return call(http.MethodPost, path+"/publish", "ops", body, status)
			}
			var link paymentpolicy.View
			decodePolicyView(t, submit(paymentpolicy.Draft{App: scope.app, Promotion: scope.promotion,
				ExpectedInherited: inherited, Operations: fallback}, http.StatusOK), &link)
			zero := 0
			var latest paymentpolicy.View
			decodePolicyView(t, submit(paymentpolicy.Draft{
				App: scope.app, ExpectedVersion: inherited,
				Operations: []paymentpolicy.Operation{{Type: "set_fallback", Action: &paymentpolicy.Action{Mode: paymentpolicy.Ratio, RateBPS: &zero}}},
			}, http.StatusOK), &latest)
			restore := paymentpolicy.Draft{App: scope.app, Promotion: scope.promotion, Deactivate: true,
				ExpectedVersion: &link.OwnVersion.ID, ExpectedInherited: inherited}
			submit(restore, http.StatusConflict)
			restore.ExpectedInherited = &latest.OwnVersion.ID
			var restored paymentpolicy.View
			decodePolicyView(t, submit(restore, http.StatusOK), &restored)
			if restored.OwnVersion != nil || restored.InheritedVersion == nil ||
				restored.InheritedVersion.ID != latest.OwnVersion.ID || len(restored.Config.Groups) == 0 {
				t.Fatalf("restored unexpected policy for %s: %+v", scope.app, restored)
			}
			action := restored.Config.Groups[0].Fallback
			if action == nil || action.Mode != paymentpolicy.Ratio || action.RateBPS == nil || *action.RateBPS != 0 {
				t.Fatalf("restored unexpected fallback for %s: %+v", scope.app, action)
			}
		}
	})

	t.Run("confirms publication after the Reader promotion is deleted", func(t *testing.T) {
		mustExec(t, ctx, owner, `DELETE FROM promotion_links WHERE id=13`)
		receipt := get("ops", "1", "reader", "13", publicationID)
		if receipt.CurrentStatus != "promotion_deleted" || receipt.Current != nil ||
			receipt.Record == nil || receipt.Record.Action != "publish_payment_policy" ||
			receipt.Record.VersionID == nil || *receipt.Record.VersionID != fmt.Sprint(published.OwnVersion.ID) ||
			receipt.Record.VersionNumber == nil || *receipt.Record.VersionNumber != fmt.Sprint(published.OwnVersion.Number) {
			t.Fatalf("wrong receipt for deleted promotion: %+v, record: %+v", receipt, receipt.Record)
		}
		for _, other := range []publicationAuditResult{
			get("another-operator", "1", "reader", "13", publicationID),
			get("ops", "1", "reader", "13", "0000000000000000000000000000a020"),
			get("ops", "1", "reader", "14", publicationID),
		} {
			if other.Record != nil {
				t.Fatalf("publication lookup crossed operator, promotion or publication ID: %+v", other)
			}
		}
		call(http.MethodGet, auditPath("2", "reader", "13", publicationID), "ops", nil, http.StatusNotFound)
		call(http.MethodGet, auditPath("1", "reader", "2147483648", publicationID), "ops", nil, http.StatusBadRequest)
		call(http.MethodGet, "/integrations/1/payment-policy?app_id=reader&promotion_id=13", "ops", nil, http.StatusBadRequest)
		call(http.MethodPost, publishPath, "ops", []byte(`{"app_id":"reader","promotion_id":"13"}`), http.StatusBadRequest)
	})
}

package operations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"conversion-backend/internal/credentials"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSinanMockConfiguration(t *testing.T) {
	ctx, pool := operationsTestPool(t)
	mustExec(t, ctx, pool, `
		INSERT INTO admins(id,username,password_hash,role,role_id,status) VALUES('ops','ops','fixture','Admin',(SELECT id FROM roles WHERE code='Admin'),1);
		INSERT INTO conversion.integrations(source,account_key,environment,contract_version)
		VALUES('sinan','fixture','test','sinan-mock-v1'),('sinan','fixture','production','sinan-mock-v1');
	`)
	seed := base64.StdEncoding.EncodeToString(make([]byte, 32))
	keys, err := credentials.Parse(fmt.Sprintf(`{"key":%q}`, seed))
	if err != nil {
		t.Fatal(err)
	}
	repo := Repository{Pool: pool, Environment: "test", EncryptCredential: func(secret string) ([]byte, string, error) {
		cipher, err := keys.Encrypt("key", secret)
		return cipher, "key", err
	}}
	const token = "fixture-inbound-credential"
	config := fmt.Sprintf(`{"config":{"app_pixels":{"1":["123"]},"endpoint":"http://127.0.0.1:8080/final",
		"max_age_seconds":3600},"inbound_token":%q,"signing_seed":%q,"reason":"integration fixture"}`, token, seed)
	request := func(handler http.Handler, id, auth, role, access, perm, input string, status int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/internal/conversion/integrations/"+id+"/sinan-mock", strings.NewReader(input))
		req.Header.Set("Authorization", "Bearer "+auth)
		req.Header.Set("X-Operator-ID", "ops")
		req.Header.Set("X-Operator-Role", role)
		req.Header.Set("X-Operator-Access", access)
		if perm != "" {
			req.Header.Set("X-Operator-Permission", perm)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != status || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), seed) {
			t.Fatalf("status=%d response=%s", w.Code, w.Body.String())
		}
	}
	handler := NewHandler(repo, "private-fixture")
	request(handler, "1", "wrong", "Admin", "write", "ads.pixels:write", config, 401)
	request(handler, "1", "private-fixture", "FinanceManager", "read", "", config, 403)
	request(handler, "1", "private-fixture", "Admin", "write", "ads.conversions:write", config, 403)
	request(handler, "1", "private-fixture", "Admin", "write", "ads.pixels:write", strings.Replace(config, "127.0.0.1", "example.com", 1), 400)
	request(handler, "2", "private-fixture", "Admin", "write", "ads.pixels:write", config, 404)
	request(handler, "1", "private-fixture", "Admin", "write", "ads.pixels:write", config, 200)
	repo.Environment = "production"
	request(NewHandler(repo, "private-fixture"), "2", "private-fixture", "Admin", "write", "ads.pixels:write", config, 404)

	var ciphertext []byte
	var version string
	var audit string
	err = pool.QueryRow(ctx, `
		SELECT c.ciphertext,c.key_version,(SELECT string_agg(a::text,'') FROM conversion.operation_audit a)
		FROM conversion.integrations i JOIN conversion.credentials c ON c.id=i.inbound_credential_id WHERE i.id=1`).
		Scan(&ciphertext, &version, &audit)
	if err != nil {
		t.Fatal(err)
	}
	value, err := keys.Decrypt(version, ciphertext)
	if value != token || err != nil || !strings.Contains(audit, "configure_sinan_mock") ||
		strings.Contains(audit, token) || strings.Contains(audit, seed) {
		t.Fatal("unexpected credential or audit storage")
	}
}

func TestConfigurationAuditQueryValidation(t *testing.T) {
	handler := NewHandler(Repository{Environment: "test"}, "private-fixture")
	cases := []struct {
		query string
		code  string
	}{
		{"?subject_type=other&subject_id=1", "invalid_filter"},
		{"?subject_type=destination&subject_id=abc", "invalid_id"},
		{"?subject_type=destination&subject_id=1&limit=0", "invalid_limit"},
		{"?subject_type=destination&subject_id=1&limit=51", "invalid_limit"},
		{"?subject_type=integration&subject_id=1&before_id=0", "invalid_id"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/internal/conversion/audit"+tc.query, nil)
		req.Header.Set("Authorization", "Bearer private-fixture")
		req.Header.Set("X-Operator-ID", "ops")
		req.Header.Set("X-Operator-Role", "FinanceManager")
		req.Header.Set("X-Operator-Access", "read")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), tc.code) {
			t.Fatalf("query=%s status=%d body=%s", tc.query, res.Code, res.Body.String())
		}
	}
}

func TestConfigurationAuditQueryAndRestrictedRole(t *testing.T) {
	ctx, pool := operationsTestPool(t)
	mustExec(t, ctx, pool, `
		INSERT INTO admins(id,username,password_hash,role,role_id,status) VALUES('ops','ops','fixture','Admin',(SELECT id FROM roles WHERE code='Admin'),1);
		INSERT INTO conversion.integrations(source,account_key,environment,contract_version,enabled)
		VALUES('starnovel','reader','test','v1',true),('sinan','partner-a','test','sinan-mock-v1',false);
	`)

	rawGrants, err := os.ReadFile("../../../cms-backend/ops/conversion_role_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{}
	for _, kind := range []string{"reader", "cms", "worker", "api", "collector", "retention"} {
		name := pgx.Identifier{pool.Config().ConnConfig.Database + "_audit_" + kind}.Sanitize()
		roles[kind] = name
		mustExec(t, ctx, pool, "CREATE ROLE "+name+" NOLOGIN")
		t.Cleanup(func() {
			mustExec(t, context.Background(), pool, "DROP OWNED BY "+name+"; DROP ROLE "+name)
		})
	}
	grantSQL := strings.TrimPrefix(string(rawGrants), "\\set ON_ERROR_STOP on\n")
	for kind, role := range roles {
		grantSQL = strings.ReplaceAll(grantSQL, ":\""+kind+"_role\"", role)
	}
	mustExec(t, ctx, pool, grantSQL)

	apiCfg := pool.Config()
	apiCfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+roles["api"])
		return err
	}
	apiPool, err := pgxpool.NewWithConfig(ctx, apiCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(apiPool.Close)

	seed := base64.StdEncoding.EncodeToString(make([]byte, 32))
	keys, err := credentials.Parse(fmt.Sprintf(`{"key":%q}`, seed))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Repository{
		Pool:        apiPool,
		Environment: "test",
		EncryptCredential: func(secret string) ([]byte, string, error) {
			cipher, err := keys.Encrypt("key", secret)
			return cipher, "key", err
		},
	}, "private-fixture")

	doReq := func(method, target, role, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/internal/conversion"+target, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer private-fixture")
		req.Header.Set("X-Operator-ID", "ops")
		req.Header.Set("X-Operator-Role", role)
		if method == http.MethodGet {
			req.Header.Set("X-Operator-Access", "read")
		} else {
			req.Header.Set("X-Operator-Access", "write")
			req.Header.Set("X-Operator-Permission", "ads.pixels:write")
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	// Create and update a destination
	const secretToken = "meta-secret-token-fixture"
	if res := doReq(http.MethodPost, "/destinations", "Admin",
		fmt.Sprintf(`{"pixel_id":"123456","display_name":"Main Pixel","access_token":%q,"reason":"init pixel"}`, secretToken)); res.Code != http.StatusCreated {
		t.Fatalf("create destination: %d %s", res.Code, res.Body.String())
	}
	if res := doReq(http.MethodPatch, "/destinations/1", "Admin",
		`{"display_name":"Main Pixel v2","reason":"rename pixel"}`); res.Code != http.StatusOK {
		t.Fatalf("patch destination: %d %s", res.Code, res.Body.String())
	}
	if res := doReq(http.MethodGet, "/destinations", "AdOptimizer", ""); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"Main Pixel v2"`) {
		t.Fatalf("get destinations: %d %s", res.Code, res.Body.String())
	}

	for _, fixture := range []struct{ body, code string }{
		{`{"enabled":true,"reason":"no apps"}`, "sinan_apps_required"},
		{`{"enabled":true,"allowed_apps":["10"],"reason":"no events"}`, "sinan_events_required"},
	} {
		if res := doReq(http.MethodPatch, "/integrations/2", "Admin", fixture.body); res.Code != http.StatusBadRequest ||
			!strings.Contains(res.Body.String(), fixture.code) {
			t.Fatalf("accepts incomplete enabled configuration: %d %s", res.Code, res.Body.String())
		}
	}
	if res := doReq(http.MethodGet, "/throughput?minutes=1", "FinanceManager", ""); res.Code != http.StatusOK {
		t.Fatalf("restricted API role cannot read throughput: %d %s", res.Code, res.Body.String())
	}

	// Update integration + sinan-mock on integration #2
	if res := doReq(http.MethodPatch, "/integrations/2", "Admin",
		`{"enabled":true,"allowed_apps":["10"],"allowed_events":["recharge-paid"],"bindings":[{"destination_id":"1","additional":true}],"reason":"enable sinan"}`); res.Code != http.StatusOK {
		t.Fatalf("patch integration: %d %s", res.Code, res.Body.String())
	}
	if res := doReq(http.MethodPut, "/integrations/2/sinan-mock", "Admin",
		fmt.Sprintf(`{"config":{"app_pixels":{"10":["123456"]},"endpoint":"http://127.0.0.1:8080/final","max_age_seconds":3600},"inbound_token":"inbound-secret-fixture-token-01","signing_seed":%q,"reason":"setup mock"}`, seed)); res.Code != http.StatusOK {
		t.Fatalf("sinan-mock: %d %s", res.Code, res.Body.String())
	}

	if res := doReq(http.MethodPatch, "/integrations/2", "Admin",
		`{"allowed_apps":[],"reason":"clear enabled application list"}`); res.Code != http.StatusBadRequest ||
		!strings.Contains(res.Body.String(), "sinan_apps_required") {
		t.Fatalf("enabled integration accepts empty app list: %d %s", res.Code, res.Body.String())
	}

	// Finance (read-only role) queries destination audit page 1 (limit=1) and page 2 (before_id)
	type auditPage struct {
		Records []struct {
			ID          string         `json:"id"`
			Action      string         `json:"action"`
			Reason      string         `json:"reason"`
			BeforeState map[string]any `json:"before_state"`
			AfterState  map[string]any `json:"after_state"`
		} `json:"records"`
		HasMore bool `json:"has_more"`
	}

	res := doReq(http.MethodGet, "/audit?subject_type=destination&subject_id=1&limit=1", "FinanceManager", "")
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), secretToken) {
		t.Fatalf("audit dest page 1: %d %s", res.Code, res.Body.String())
	}
	var p1 auditPage
	if err := json.Unmarshal(res.Body.Bytes(), &p1); err != nil {
		t.Fatal(err)
	}
	if len(p1.Records) != 1 || !p1.HasMore || p1.Records[0].Reason != "rename pixel" ||
		p1.Records[0].AfterState["display_name"] != "Main Pixel v2" {
		t.Fatalf("unexpected dest page 1: %+v", p1)
	}

	res = doReq(http.MethodGet, "/audit?subject_type=destination&subject_id=1&limit=1&before_id="+p1.Records[0].ID, "FinanceManager", "")
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), secretToken) {
		t.Fatalf("audit dest page 2: %d %s", res.Code, res.Body.String())
	}
	var p2 auditPage
	if err := json.Unmarshal(res.Body.Bytes(), &p2); err != nil {
		t.Fatal(err)
	}
	if len(p2.Records) != 1 || p2.HasMore || p2.Records[0].Reason != "init pixel" {
		t.Fatalf("unexpected dest page 2: %+v", p2)
	}

	// Query integration audit (returns both configure_sinan_mock and configure_integration)
	res = doReq(http.MethodGet, "/audit?subject_type=integration&subject_id=2", "FinanceManager", "")
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "inbound-secret-fixture-token-01") || strings.Contains(res.Body.String(), seed) {
		t.Fatalf("audit integration: %d %s", res.Code, res.Body.String())
	}
	var integAudit auditPage
	if err := json.Unmarshal(res.Body.Bytes(), &integAudit); err != nil {
		t.Fatal(err)
	}
	if len(integAudit.Records) != 2 || integAudit.HasMore ||
		integAudit.Records[0].Action != "configure_sinan_mock" ||
		integAudit.Records[1].Action != "configure_integration" {
		t.Fatalf("unexpected integration audit: %+v", integAudit)
	}

	// Verify api_role remains blocked from reading secret ciphertexts or admins directly
	var ciphertext []byte
	if err := apiPool.QueryRow(ctx, "SELECT ciphertext FROM conversion.credentials LIMIT 1").Scan(&ciphertext); err == nil {
		t.Fatal("expected api_role SELECT ciphertext on conversion.credentials to be denied")
	}
	var adminCount int
	if err := apiPool.QueryRow(ctx, "SELECT count(*) FROM admins").Scan(&adminCount); err == nil {
		t.Fatal("expected api_role SELECT on admins to be denied")
	}
}

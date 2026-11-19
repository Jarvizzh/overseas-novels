package operations

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"conversion-backend/internal/credentials"
)

func TestSinanMockConfiguration(t *testing.T) {
	ctx, pool := operationsTestPool(t)
	mustExec(t, ctx, pool, `
		INSERT INTO admins(id,username,password_hash,role,status) VALUES('ops','ops','fixture','Admin',1);
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
	request := func(handler http.Handler, id, auth, role, input string, status int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/internal/conversion/integrations/"+id+"/sinan-mock", strings.NewReader(input))
		req.Header.Set("Authorization", "Bearer "+auth)
		req.Header.Set("X-Operator-ID", "ops")
		req.Header.Set("X-Operator-Role", role)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != status || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), seed) {
			t.Fatalf("status=%d response=%s", w.Code, w.Body.String())
		}
	}
	handler := NewHandler(repo, "private-fixture")
	request(handler, "1", "wrong", "Admin", config, 401)
	request(handler, "1", "private-fixture", "MediaBuyer", config, 403)
	request(handler, "1", "private-fixture", "Admin", strings.Replace(config, "127.0.0.1", "example.com", 1), 400)
	request(handler, "2", "private-fixture", "Admin", config, 404)
	request(handler, "1", "private-fixture", "Admin", config, 200)
	repo.Environment = "production"
	request(NewHandler(repo, "private-fixture"), "2", "private-fixture", "Admin", config, 404)

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

package credentials

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, aes256KeyBytes)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestKeyringDecryptAndAAD(t *testing.T) {
	ring, err := ParseKeyring("v1:" + testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	aad := CredentialAAD(7, 2, "webhook_secret")
	ct, err := ring.Encrypt("v1", "whsec_secret_1234", aad)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := ring.Decrypt("v1", ct, aad)
	if err != nil || pt != "whsec_secret_1234" {
		t.Fatalf("decrypt = %q, err = %v", pt, err)
	}
	if _, err := ring.Decrypt("v1", ct, CredentialAAD(7, 1, "webhook_secret")); err == nil {
		t.Fatal("expected AAD version mismatch rejection")
	}
}

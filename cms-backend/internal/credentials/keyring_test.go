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

func TestKeyringEncryptDecryptRotationAndAAD(t *testing.T) {
	k1 := testKey(t)
	k2 := testKey(t)
	ring, err := ParseKeyring("v1:" + k1 + ",v2:" + k2)
	if err != nil {
		t.Fatal(err)
	}
	ver, err := ring.ActiveVersion("")
	if err != nil || ver != "v2" {
		t.Fatalf("active version = %q, err = %v", ver, err)
	}
	aad := CredentialAAD(42, 3, "api_secret")
	ct, err := ring.Encrypt("v1", "sk_test_supersecret", aad)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := ring.Decrypt("v1", ct, aad)
	if err != nil || pt != "sk_test_supersecret" {
		t.Fatalf("decrypt = %q, err = %v", pt, err)
	}
	if _, err := ring.Decrypt("v1", ct, CredentialAAD(43, 3, "api_secret")); err == nil {
		t.Fatal("expected AAD mismatch rejection")
	}
	if _, err := ring.Decrypt("v2", ct, aad); err == nil {
		t.Fatal("expected key version mismatch rejection")
	}
	if got := MaskSecret("whsec_abc12345"); got != "••••2345" {
		t.Fatalf("mask = %q", got)
	}
}

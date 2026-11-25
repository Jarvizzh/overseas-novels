package config

import (
	"strings"
	"testing"
)

func TestAdminJWTSecretValidation(t *testing.T) {
	secure := "9a8d6f41c3e150b7d29c0e54f162a88380d714cbcd2725e680f392b30d75906a"
	for _, secret := range []string{"", "short", "CHANGE_ME_TO_A_UNIQUE_RANDOM_SECRET", "STAR_NOVEL_CMS_SUPER_KEY_PROD_2026_RANDOM_STRING", "star-novel-cms-local-development-only", secure} {
		if validAdminJWTSecret(secret, secure) {
			t.Fatalf("invalid or shared CMS secret accepted: %q", secret)
		}
	}
	if !validAdminJWTSecret(secure, "different-reader-secret") {
		t.Fatal("independent random CMS secret rejected")
	}
}

func TestBootstrapAdminConfigValidation(t *testing.T) {
	validPass := "StarNovel!2026#Guard"

	if err := ValidateBootstrapAdminConfig("admin", validPass, "管理员"); err != nil {
		t.Fatalf("expected valid bootstrap config to pass, got: %v", err)
	}

	if err := ValidateBootstrapAdminConfig("adm", validPass, "管理员"); err == nil {
		t.Fatal("expected short username (<4) to fail")
	}

	badPasswords := []string{
		"",
		"   ",
		"Short!1a",
		"admin123",
		"Admin123!ExtraLong",
		"MyPassword2026!",
		"ChangeMe2026!Secure",
		"<CHANGE_ME_STRONG_PASS>",
		"alllowercasepassword",
		"1234567890123456",
		"Abcdefghijkl",
		" StarNovel!2026#Guard ",
	}
	for _, pw := range badPasswords {
		err := ValidateBootstrapAdminConfig("admin", pw, "管理员")
		if err == nil {
			t.Fatalf("expected weak/invalid bootstrap password %q to be rejected", pw)
		}
		if pw != "" && strings.TrimSpace(pw) != "" && strings.Contains(err.Error(), pw) {
			t.Fatalf("error message leaked plaintext password: %v", err)
		}
	}
}

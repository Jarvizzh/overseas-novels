package config

import "testing"

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

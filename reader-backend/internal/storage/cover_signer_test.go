package storage

import (
	"net/url"
	"strings"
	"testing"

	"reader-backend/internal/config"
)

func TestCoverOSSSigner_CnameV4Signing(t *testing.T) {
	signer, err := NewCoverOSSSigner(&config.Config{
		CoverOSSEnabled:         true,
		CoverOSSRegion:          "us-west-1",
		CoverOSSBucket:          "star-novels",
		CoverOSSPrefix:          "covers",
		CoverOSSPublicBaseURL:   "https://oss.starsfic.com",
		CoverOSSAccessKeyID:     "LTAI5tTestAccessKeyId",
		CoverOSSAccessKeySecret: "TestAccessKeySecret1234567890",
		CoverOSSSignedURLTTL:    3600,
	})
	if err != nil {
		t.Fatalf("NewCoverOSSSigner failed: %v", err)
	}

	signed := signer.SignCoverURL("oss://covers/2026/11/sample.webp")
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("signed URL invalid: %v", err)
	}
	if u.Scheme != "https" || u.Host != "oss.starsfic.com" || u.Path != "/covers/2026/11/sample.webp" {
		t.Fatalf("unexpected signed URL parts: %s", signed)
	}
	if !strings.Contains(u.RawQuery, "OSS4-HMAC-SHA256") {
		t.Fatalf("expected AuthV4 signature, got %q", u.RawQuery)
	}
	if got := signer.SignCoverURL("/assets/sample.png"); got != "/assets/sample.png" {
		t.Fatalf("expected legacy cover untouched, got %q", got)
	}
}

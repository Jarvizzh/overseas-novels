package storage

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/url"
	"strings"
	"testing"
	"time"

	"star-novel-cms/internal/config"
)

func makeTestPNG(w, h int) []byte {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func makeTestJPEG(w, h int) []byte {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	_ = jpeg.Encode(&buf, img, nil)
	return buf.Bytes()
}

func TestValidateCoverImage(t *testing.T) {
	t.Run("accepts png and jpeg", func(t *testing.T) {
		ct, ext, err := ValidateCoverImage(makeTestPNG(16, 24), 5<<20)
		if err != nil || ct != "image/png" || ext != ".png" {
			t.Fatalf("unexpected png result: ct=%q ext=%q err=%v", ct, ext, err)
		}
		ct, ext, err = ValidateCoverImage(makeTestJPEG(16, 24), 5<<20)
		if err != nil || ct != "image/jpeg" || ext != ".jpg" {
			t.Fatalf("unexpected jpeg result: ct=%q ext=%q err=%v", ct, ext, err)
		}
	})

	t.Run("rejects svg and non-images", func(t *testing.T) {
		svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
		if _, _, err := ValidateCoverImage(svg, 5<<20); !errors.Is(err, ErrCoverUnsupportedType) {
			t.Fatalf("expected ErrCoverUnsupportedType for svg, got %v", err)
		}
		if _, _, err := ValidateCoverImage([]byte("plain text"), 5<<20); !errors.Is(err, ErrCoverUnsupportedType) {
			t.Fatalf("expected ErrCoverUnsupportedType for text, got %v", err)
		}
	})

	t.Run("rejects empty and oversized", func(t *testing.T) {
		if _, _, err := ValidateCoverImage(nil, 5<<20); !errors.Is(err, ErrCoverEmpty) {
			t.Fatalf("expected ErrCoverEmpty, got %v", err)
		}
		if _, _, err := ValidateCoverImage(makeTestPNG(16, 24), 10); !errors.Is(err, ErrCoverTooLarge) {
			t.Fatalf("expected ErrCoverTooLarge, got %v", err)
		}
	})
}

func TestCoverOSSStore_CnameV4Signing(t *testing.T) {
	store, err := NewCoverOSSStore(&config.Config{
		CoverOSSEnabled:         true,
		CoverOSSRegion:          "us-west-1",
		CoverOSSEndpoint:        "https://oss-us-west-1-internal.aliyuncs.com",
		CoverOSSBucket:          "star-novels",
		CoverOSSPrefix:          "covers",
		CoverOSSPublicBaseURL:   "https://oss.starsfic.com",
		CoverOSSAccessKeyID:     "LTAI5tTestAccessKeyId",
		CoverOSSAccessKeySecret: "TestAccessKeySecret1234567890",
		CoverOSSSignedURLTTL:    3600,
		CoverMaxUploadBytes:     5 << 20,
	})
	if err != nil {
		t.Fatalf("NewCoverOSSStore failed: %v", err)
	}

	signed := store.SignCoverURL("oss://covers/2026/11/sample.png")
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("signed URL invalid: %v", err)
	}
	if u.Scheme != "https" || u.Host != "oss.starsfic.com" {
		t.Fatalf("expected https://oss.starsfic.com host, got %s://%s", u.Scheme, u.Host)
	}
	if u.Path != "/covers/2026/11/sample.png" {
		t.Fatalf("expected CNAME path /covers/2026/11/sample.png without bucket prefix, got %q", u.Path)
	}
	if !strings.Contains(u.RawQuery, "OSS4-HMAC-SHA256") {
		t.Fatalf("expected AuthV4 signature in query, got %q", u.RawQuery)
	}

	if got := store.SignCoverURL("/assets/legacy.png"); got != "/assets/legacy.png" {
		t.Fatalf("legacy asset path should pass through, got %q", got)
	}
}

func TestCoverOSSStore_UploadAndRefValidation(t *testing.T) {
	store, err := NewCoverOSSStore(&config.Config{
		CoverOSSEnabled:         true,
		CoverOSSRegion:          "us-west-1",
		CoverOSSEndpoint:        "https://oss-us-west-1-internal.aliyuncs.com",
		CoverOSSBucket:          "star-novels",
		CoverOSSPrefix:          "covers",
		CoverOSSPublicBaseURL:   "https://oss.starsfic.com",
		CoverOSSAccessKeyID:     "LTAI5tTestAccessKeyId",
		CoverOSSAccessKeySecret: "TestAccessKeySecret1234567890",
		CoverOSSSignedURLTTL:    3600,
		CoverMaxUploadBytes:     5 << 20,
	})
	if err != nil {
		t.Fatalf("NewCoverOSSStore failed: %v", err)
	}

	var uploadedKey, uploadedCT string
	var uploadedBytes []byte
	store.nowFunc = func() time.Time { return time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC) }
	store.putObjectFunc = func(ctx context.Context, objectKey string, data []byte, contentType string) error {
		uploadedKey = objectKey
		uploadedCT = contentType
		uploadedBytes = append([]byte(nil), data...)
		return nil
	}

	raw := makeTestPNG(20, 30)
	coverKey, previewURL, err := store.UploadCover(context.Background(), raw)
	if err != nil {
		t.Fatalf("UploadCover failed: %v", err)
	}
	if !strings.HasPrefix(coverKey, "oss://covers/2026/11/") || !strings.HasSuffix(coverKey, ".png") {
		t.Fatalf("unexpected coverKey: %q", coverKey)
	}
	if !bytes.Equal(uploadedBytes, raw) {
		t.Fatalf("expected original bytes uploaded without recompression")
	}
	if uploadedCT != "image/png" || uploadedKey != strings.TrimPrefix(coverKey, "oss://") {
		t.Fatalf("unexpected upload metadata: key=%q ct=%q", uploadedKey, uploadedCT)
	}
	parsedPreview, err := url.Parse(previewURL)
	if err != nil {
		t.Fatalf("invalid previewURL %q: %v", previewURL, err)
	}
	if parsedPreview.Scheme != "https" || parsedPreview.Host != "oss.starsfic.com" || parsedPreview.Path != "/"+uploadedKey {
		t.Fatalf("unexpected previewURL: %q", previewURL)
	}

	if !store.IsValidCoverRef(coverKey) {
		t.Fatalf("expected %q to be valid cover ref", coverKey)
	}
	for _, invalid := range []string{
		"https://example.com/a.png",
		"oss://other/2026/11/a.png",
		"oss://covers/../secret.png",
		"oss://covers/2026/11/a.svg",
		"oss://covers/2026/11/a.png?foo=bar",
	} {
		if store.IsValidCoverRef(invalid) {
			t.Fatalf("expected %q to be rejected as invalid cover ref", invalid)
		}
	}

	var deletedKey string
	store.deleteObjectFunc = func(_ context.Context, key string) error {
		deletedKey = key
		return nil
	}
	if err := store.DeleteCover(context.Background(), coverKey); err != nil {
		t.Fatalf("DeleteCover failed: %v", err)
	}
	if deletedKey != uploadedKey {
		t.Fatalf("expected DeleteCover to delete %q, got %q", uploadedKey, deletedKey)
	}
	deletedKey = ""
	if err := store.DeleteCover(context.Background(), "/assets/legacy.png"); err != nil || deletedKey != "" {
		t.Fatalf("expected non-OSS ref skipped on DeleteCover, got deletedKey=%q err=%v", deletedKey, err)
	}
}

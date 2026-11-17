package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gen2brain/webp"
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

	t.Run("rejects animated webp", func(t *testing.T) {
		animData := makeAnimatedWebP(16, 24)
		if _, _, err := ValidateCoverImage(animData, 5<<20); !errors.Is(err, ErrCoverUnsupportedType) {
			t.Fatalf("expected ErrCoverUnsupportedType for animated webp, got %v", err)
		}
	})
}

func makeAnimatedWebP(w, h int) []byte {
	f1 := image.NewRGBA(image.Rect(0, 0, w, h))
	f2 := image.NewRGBA(image.Rect(0, 0, w, h))
	f2.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	_ = webp.EncodeAll(&buf, &webp.WEBP{
		Image: []image.Image{f1, f2},
		Delay: []int{100, 100},
	}, webp.Options{Quality: 75})
	return buf.Bytes()
}

func injectJPEGOrientation(jpegBytes []byte, orientation uint16) []byte {
	if len(jpegBytes) < 2 || jpegBytes[0] != 0xFF || jpegBytes[1] != 0xD8 {
		return jpegBytes
	}
	// APP1 marker + length + "Exif\x00\x00" + TIFF("MM", 0x002A, offset=8, 1 entry tag=0x0112 SHORT(3) count=1 val)
	var app1 bytes.Buffer
	app1.Write([]byte{0xFF, 0xE1})
	payloadLen := 2 + 6 + 8 + 2 + 12 + 4
	_ = binary.Write(&app1, binary.BigEndian, uint16(payloadLen))
	app1.WriteString("Exif\x00\x00")
	app1.WriteString("MM")
	_ = binary.Write(&app1, binary.BigEndian, uint16(0x002A))
	_ = binary.Write(&app1, binary.BigEndian, uint32(8))
	_ = binary.Write(&app1, binary.BigEndian, uint16(1))
	_ = binary.Write(&app1, binary.BigEndian, uint16(0x0112))
	_ = binary.Write(&app1, binary.BigEndian, uint16(3))
	_ = binary.Write(&app1, binary.BigEndian, uint32(1))
	_ = binary.Write(&app1, binary.BigEndian, orientation)
	_ = binary.Write(&app1, binary.BigEndian, uint16(0))
	_ = binary.Write(&app1, binary.BigEndian, uint32(0))

	out := make([]byte, 0, len(jpegBytes)+app1.Len())
	out = append(out, jpegBytes[:2]...)
	out = append(out, app1.Bytes()...)
	out = append(out, jpegBytes[2:]...)
	return out
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
	if !strings.HasPrefix(coverKey, "oss://covers/2026/11/") || !strings.HasSuffix(coverKey, ".webp") {
		t.Fatalf("unexpected coverKey: %q", coverKey)
	}
	sum := sha256.Sum256(uploadedBytes)
	expectedKey := "covers/2026/11/" + hex.EncodeToString(sum[:12]) + ".webp"
	if uploadedCT != "image/webp" || uploadedKey != expectedKey || uploadedKey != strings.TrimPrefix(coverKey, "oss://") {
		t.Fatalf("unexpected upload metadata: key=%q expected=%q ct=%q", uploadedKey, expectedKey, uploadedCT)
	}
	if gotCT := http.DetectContentType(uploadedBytes); gotCT != "image/webp" {
		t.Fatalf("expected uploaded bytes to be WebP, got %q", gotCT)
	}
	cfg, err := webp.DecodeConfig(bytes.NewReader(uploadedBytes))
	if err != nil || cfg.Width != 20 || cfg.Height != 30 {
		t.Fatalf("unexpected encoded WebP config: cfg=%+v err=%v", cfg, err)
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

func makeTexturedPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	state := uint32(0x12345678)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Deterministic pseudo-random texture + smooth gradient (closer to photographic cover art than repeating stripes)
			state = state*1664525 + 1013904223
			noise := uint8(state >> 24)
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x*200)/max(1, w)) ^ (noise & 0x3f),
				G: uint8((y*200)/max(1, h)) ^ ((noise >> 2) & 0x3f),
				B: uint8(((x+y)*120)/max(1, w+h)) ^ ((noise >> 4) & 0x3f),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestProcessCoverImage(t *testing.T) {
	t.Run("downscales oversized 3:4 png to 600x800 webp and shrinks size", func(t *testing.T) {
		raw := makeTexturedPNG(1200, 1600)
		out, ct, ext, err := ProcessCoverImage(raw, 5<<20)
		if err != nil {
			t.Fatalf("ProcessCoverImage failed: %v", err)
		}
		if ct != "image/webp" || ext != ".webp" {
			t.Fatalf("unexpected metadata: ct=%q ext=%q", ct, ext)
		}
		cfg, err := webp.DecodeConfig(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("DecodeConfig failed: %v", err)
		}
		if cfg.Width != 600 || cfg.Height != 800 {
			t.Fatalf("expected 600x800, got %dx%d", cfg.Width, cfg.Height)
		}
		if len(out) >= len(raw) {
			t.Fatalf("expected compressed WebP (%d bytes) to be smaller than raw PNG (%d bytes)", len(out), len(raw))
		}
	})

	t.Run("constrains tall image by max height 900", func(t *testing.T) {
		raw := makeTestJPEG(1000, 3000)
		out, ct, ext, err := ProcessCoverImage(raw, 5<<20)
		if err != nil {
			t.Fatalf("ProcessCoverImage failed: %v", err)
		}
		if ct != "image/webp" || ext != ".webp" {
			t.Fatalf("unexpected metadata: ct=%q ext=%q", ct, ext)
		}
		cfg, err := webp.DecodeConfig(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("DecodeConfig failed: %v", err)
		}
		if cfg.Width != 300 || cfg.Height != 900 {
			t.Fatalf("expected 300x900, got %dx%d", cfg.Width, cfg.Height)
		}
	})

	t.Run("does not upscale small image and accepts webp input", func(t *testing.T) {
		var webpBuf bytes.Buffer
		_ = webp.Encode(&webpBuf, image.NewRGBA(image.Rect(0, 0, 300, 400)), webp.Options{Quality: 90})

		out, ct, ext, err := ProcessCoverImage(webpBuf.Bytes(), 5<<20)
		if err != nil {
			t.Fatalf("ProcessCoverImage webp failed: %v", err)
		}
		if ct != "image/webp" || ext != ".webp" {
			t.Fatalf("unexpected metadata: ct=%q ext=%q", ct, ext)
		}
		cfg, err := webp.DecodeConfig(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("DecodeConfig failed: %v", err)
		}
		if cfg.Width != 300 || cfg.Height != 400 {
			t.Fatalf("expected 300x400, got %dx%d", cfg.Width, cfg.Height)
		}
	})

	t.Run("rejects animated webp before decoding", func(t *testing.T) {
		if _, _, _, err := ProcessCoverImage(makeAnimatedWebP(16, 24), 5<<20); !errors.Is(err, ErrCoverUnsupportedType) {
			t.Fatalf("expected ErrCoverUnsupportedType for animated webp, got %v", err)
		}
	})

	t.Run("applies JPEG EXIF orientation 6 before resizing", func(t *testing.T) {
		// Raw 1600x1200 image: left half red, right half blue.
		img := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
		for y := 0; y < 1200; y++ {
			for x := 0; x < 1600; x++ {
				if x < 800 {
					img.SetRGBA(x, y, color.RGBA{R: 230, G: 20, B: 20, A: 255})
				} else {
					img.SetRGBA(x, y, color.RGBA{R: 20, G: 20, B: 230, A: 255})
				}
			}
		}
		var jpegBuf bytes.Buffer
		_ = jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 90})
		raw := injectJPEGOrientation(jpegBuf.Bytes(), 6) // 90 CW -> portrait 1200x1600, top red, bottom blue

		out, _, _, err := ProcessCoverImage(raw, 5<<20)
		if err != nil {
			t.Fatalf("ProcessCoverImage oriented jpeg failed: %v", err)
		}
		decoded, err := webp.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("decode output webp failed: %v", err)
		}
		b := decoded.Bounds()
		if b.Dx() != 600 || b.Dy() != 800 {
			t.Fatalf("expected oriented & scaled 600x800, got %dx%d", b.Dx(), b.Dy())
		}
		topR, _, topB, _ := decoded.At(300, 100).RGBA()
		botR, _, botB, _ := decoded.At(300, 700).RGBA()
		if topR <= topB || botB <= botR {
			t.Fatalf("unexpected oriented pixel colors: top(R=%d,B=%d) bot(R=%d,B=%d)", topR, topB, botR, botB)
		}
	})

	t.Run("applies JPEG EXIF orientation 3 (180 degrees)", func(t *testing.T) {
		// Raw 200x300 image: top half red, bottom half blue.
		img := image.NewRGBA(image.Rect(0, 0, 200, 300))
		for y := 0; y < 300; y++ {
			c := color.RGBA{R: 230, G: 20, B: 20, A: 255}
			if y >= 150 {
				c = color.RGBA{R: 20, G: 20, B: 230, A: 255}
			}
			for x := 0; x < 200; x++ {
				img.SetRGBA(x, y, c)
			}
		}
		var jpegBuf bytes.Buffer
		_ = jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 90})
		raw := injectJPEGOrientation(jpegBuf.Bytes(), 3)

		out, _, _, err := ProcessCoverImage(raw, 5<<20)
		if err != nil {
			t.Fatalf("ProcessCoverImage 180 jpeg failed: %v", err)
		}
		decoded, err := webp.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("decode output webp failed: %v", err)
		}
		topR, _, topB, _ := decoded.At(100, 40).RGBA()
		botR, _, botB, _ := decoded.At(100, 260).RGBA()
		if topB <= topR || botR <= botB {
			t.Fatalf("expected top blue & bottom red after 180deg flip: top(R=%d,B=%d) bot(R=%d,B=%d)", topR, topB, botR, botB)
		}
	})
}

func TestCoverOSSStore_PublicRead(t *testing.T) {
	store, err := NewCoverOSSStore(&config.Config{
		CoverOSSEnabled:         true,
		CoverOSSPublicRead:      true,
		CoverOSSRegion:          "us-west-1",
		CoverOSSEndpoint:        "https://oss-us-west-1-internal.aliyuncs.com",
		CoverOSSBucket:          "star-novels",
		CoverOSSPrefix:          "covers",
		CoverOSSPublicBaseURL:   "https://oss.starsfic.com/",
		CoverOSSAccessKeyID:     "LTAI5tTestAccessKeyId",
		CoverOSSAccessKeySecret: "TestAccessKeySecret1234567890",
		CoverMaxUploadBytes:     5 << 20,
	})
	if err != nil {
		t.Fatalf("NewCoverOSSStore public read failed: %v", err)
	}

	if got := store.SignCoverURL("oss://covers/2026/11/sample.webp"); got != "https://oss.starsfic.com/covers/2026/11/sample.webp" {
		t.Fatalf("unexpected public SignCoverURL result: %q", got)
	}
	if got := store.SignCoverURL("/assets/legacy.png"); got != "/assets/legacy.png" {
		t.Fatalf("expected legacy path untouched, got %q", got)
	}

	var uploadedKey string
	store.nowFunc = func() time.Time { return time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC) }
	store.putObjectFunc = func(_ context.Context, objectKey string, _ []byte, _ string) error {
		uploadedKey = objectKey
		return nil
	}

	coverKey, previewURL, err := store.UploadCover(context.Background(), makeTestPNG(20, 30))
	if err != nil {
		t.Fatalf("UploadCover failed: %v", err)
	}
	expectedPreview := "https://oss.starsfic.com/" + uploadedKey
	if previewURL != expectedPreview || strings.TrimPrefix(coverKey, "oss://") != uploadedKey {
		t.Fatalf("unexpected upload preview: coverKey=%q previewURL=%q expected=%q", coverKey, previewURL, expectedPreview)
	}

	for _, badBase := range []string{"", "https://", "ftp://oss.starsfic.com"} {
		if _, err := NewCoverOSSStore(&config.Config{
			CoverOSSEnabled:         true,
			CoverOSSPublicRead:      true,
			CoverOSSRegion:          "us-west-1",
			CoverOSSEndpoint:        "https://oss-us-west-1-internal.aliyuncs.com",
			CoverOSSBucket:          "star-novels",
			CoverOSSPublicBaseURL:   badBase,
			CoverOSSAccessKeyID:     "LTAI5tTestAccessKeyId",
			CoverOSSAccessKeySecret: "TestAccessKeySecret1234567890",
		}); err == nil {
			t.Fatalf("expected invalid CoverOSSPublicBaseURL %q rejected", badBase)
		}
	}
}

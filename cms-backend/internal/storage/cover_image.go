package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"strings"

	"github.com/gen2brain/webp"
	xdraw "golang.org/x/image/draw"
)

const (
	defaultCoverMaxUploadBytes = 5 * 1024 * 1024
	maxCoverImageDimension     = 4096
	maxCoverImagePixels        = 4096 * 4096
	coverTargetMaxWidth        = 600
	coverTargetMaxHeight       = 900
	coverWebPQuality           = 80
	coverWebPMethod            = 4
)

var (
	ErrCoverEmpty           = errors.New("请选择要上传的封面图片")
	ErrCoverTooLarge        = errors.New("封面图片大小不能超过 5MB")
	ErrCoverUnsupportedType = errors.New("仅支持 JPG、PNG、WebP 格式的封面图片")
	ErrCoverInvalidImage    = errors.New("无效或损坏的图片文件，或尺寸超过 4096x4096 限制")
)

// ValidateCoverImage validates raw bytes are a static JPEG, PNG, or WebP image within size/dimension limits.
func ValidateCoverImage(data []byte, maxBytes int64) (contentType string, ext string, err error) {
	if len(data) == 0 {
		return "", "", ErrCoverEmpty
	}
	if maxBytes <= 0 {
		maxBytes = defaultCoverMaxUploadBytes
	}
	if int64(len(data)) > maxBytes {
		return "", "", ErrCoverTooLarge
	}

	ct := http.DetectContentType(data)
	switch ct {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	default:
		return "", "", ErrCoverUnsupportedType
	}

	if ct == "image/webp" && isAnimatedWebP(data) {
		return "", "", fmt.Errorf("%w（不支持动态 WebP 动画封面）", ErrCoverUnsupportedType)
	}

	cfg, format, decodeErr := image.DecodeConfig(bytes.NewReader(data))
	if decodeErr != nil || cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > maxCoverImageDimension || cfg.Height > maxCoverImageDimension ||
		int64(cfg.Width)*int64(cfg.Height) > maxCoverImagePixels {
		return "", "", ErrCoverInvalidImage
	}

	expectedFormat := strings.TrimPrefix(ct, "image/")
	if format != expectedFormat {
		return "", "", ErrCoverUnsupportedType
	}
	// gen2brain/webp reports color.RGBAModel only when the WebP container carries animation.
	if ct == "image/webp" && cfg.ColorModel == color.RGBAModel {
		return "", "", fmt.Errorf("%w（不支持动态 WebP 动画封面）", ErrCoverUnsupportedType)
	}

	return ct, ext, nil
}

// ProcessCoverImage validates, normalizes orientation, downscales (max 600x900), and encodes cover images to WebP.
func ProcessCoverImage(data []byte, maxBytes int64) ([]byte, string, string, error) {
	ct, _, err := ValidateCoverImage(data, maxBytes)
	if err != nil {
		return nil, "", "", err
	}

	var img image.Image
	if ct == "image/webp" {
		img, err = webp.Decode(bytes.NewReader(data), webp.Options{AutoRotate: true})
	} else {
		img, _, err = image.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, "", "", ErrCoverInvalidImage
	}

	if ct == "image/jpeg" {
		img = applyExifOrientation(img, readJPEGOrientation(data))
	}

	srcBounds := img.Bounds()
	srcW, srcH := srcBounds.Dx(), srcBounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return nil, "", "", ErrCoverInvalidImage
	}

	targetW, targetH := fitCoverDimensions(srcW, srcH, coverTargetMaxWidth, coverTargetMaxHeight)
	targetImg := img
	if targetW != srcW || targetH != srcH {
		dst := image.NewNRGBA(image.Rect(0, 0, targetW, targetH))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, srcBounds, xdraw.Src, nil)
		targetImg = dst
	}

	var out bytes.Buffer
	if err := webp.Encode(&out, targetImg, webp.Options{
		Quality: coverWebPQuality,
		Method:  coverWebPMethod,
	}); err != nil {
		return nil, "", "", fmt.Errorf("压缩封面图片为 WebP 失败: %w", err)
	}

	return out.Bytes(), "image/webp", ".webp", nil
}

func isAnimatedWebP(data []byte) bool {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return false
	}
	off := 12
	for off+8 <= len(data) {
		fourcc := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		payloadOff := off + 8
		if fourcc == "VP8X" && payloadOff+1 <= len(data) && (data[payloadOff]&0x02) != 0 {
			return true
		}
		if fourcc == "ANIM" || fourcc == "ANMF" {
			return true
		}
		if size < 0 {
			break
		}
		padded := size + (size & 1)
		if payloadOff+padded <= off || payloadOff+padded > len(data) {
			break
		}
		off = payloadOff + padded
	}
	return false
}

func readJPEGOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	pos := 2
	for pos+2 <= len(data) {
		for pos < len(data) && data[pos] != 0xFF {
			pos++
		}
		for pos < len(data) && data[pos] == 0xFF {
			pos++
		}
		if pos >= len(data) {
			break
		}
		marker := data[pos]
		pos++
		// Stop at Start of Scan or End of Image; skip standalone markers without payload length.
		if marker == 0xD9 || marker == 0xDA {
			break
		}
		if marker == 0x00 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD8) {
			continue
		}
		if pos+2 > len(data) {
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if segLen < 2 || pos+segLen > len(data) {
			break
		}
		if marker == 0xE1 && segLen >= 8 {
			payload := data[pos+2 : pos+segLen]
			if bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
				if o := parseTIFFOrientation(payload[6:]); o != 0 {
					return o
				}
			}
		}
		pos += segLen
	}
	return 1
}

func parseTIFFOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(tiff[0:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(tiff[2:4]) != 0x002A {
		return 0
	}
	ifdOff := int(order.Uint32(tiff[4:8]))
	if ifdOff < 8 || ifdOff+2 > len(tiff) {
		return 0
	}
	entries := min(int(order.Uint16(tiff[ifdOff:ifdOff+2])), 256)
	base := ifdOff + 2
	for i := 0; i < entries; i++ {
		entryOff := base + i*12
		if entryOff+12 > len(tiff) {
			break
		}
		if order.Uint16(tiff[entryOff:entryOff+2]) == 0x0112 {
			typ := order.Uint16(tiff[entryOff+2 : entryOff+4])
			count := order.Uint32(tiff[entryOff+4 : entryOff+8])
			if typ == 3 && count == 1 {
				val := int(order.Uint16(tiff[entryOff+8 : entryOff+10]))
				if val >= 1 && val <= 8 {
					return val
				}
			}
			return 0
		}
	}
	return 0
}

func applyExifOrientation(img image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return img
	}
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return img
	}

	src := image.NewNRGBA(image.Rect(0, 0, sw, sh))
	draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)

	dw, dh := sw, sh
	if orientation >= 5 {
		dw, dh = sh, sw
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))

	for sy := 0; sy < sh; sy++ {
		srow := src.Pix[sy*src.Stride:]
		for sx := 0; sx < sw; sx++ {
			dx, dy := orientTarget(orientation, sx, sy, sw, sh)
			si := sx * 4
			di := dy*dst.Stride + dx*4
			copy(dst.Pix[di:di+4], srow[si:si+4])
		}
	}
	return dst
}

func orientTarget(orientation, sx, sy, sw, sh int) (int, int) {
	switch orientation {
	case 2:
		return sw - 1 - sx, sy
	case 3:
		return sw - 1 - sx, sh - 1 - sy
	case 4:
		return sx, sh - 1 - sy
	case 5:
		return sy, sx
	case 6:
		return sh - 1 - sy, sx
	case 7:
		return sh - 1 - sy, sw - 1 - sx
	case 8:
		return sy, sw - 1 - sx
	default:
		return sx, sy
	}
}

func fitCoverDimensions(width, height, maxWidth, maxHeight int) (int, int) {
	if width <= maxWidth && height <= maxHeight {
		return width, height
	}
	scaleW := float64(maxWidth) / float64(width)
	scaleH := float64(maxHeight) / float64(height)
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	targetW := max(1, int(math.Round(float64(width)*scale)))
	targetH := max(1, int(math.Round(float64(height)*scale)))
	if targetW > maxWidth {
		targetW = maxWidth
	}
	if targetH > maxHeight {
		targetH = maxHeight
	}
	return targetW, targetH
}

package engine

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
)

// Photo limits for one turn. Photos travel with the turn to the engine and on
// to the person's model; training-mcp never stores them. The browser already
// re-encodes them (no EXIF, at most 1600 px), so these limits are generous for
// a real photo and tight for anything else.
//
// MaxImagePixels caps width x height, read from the header alone: a small file
// can declare a huge image (a decompression bomb) that the person's engine
// would otherwise decode in full to resize it. 9 MP is about 3000 x 3000, and
// decodes to some 36 MB.
const (
	MaxImages      = 4
	MaxImageBytes  = 2 << 20
	MaxImagesBytes = 6 << 20
	MaxImagePixels = 9_000_000
)

// acceptedImageTypes are the formats the model takes; each is checked against
// the bytes themselves, not only against what the browser declared.
var acceptedImageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

// Image is one photo of a turn: its declared type and its bytes.
type Image struct {
	MIME string
	Data []byte
}

// promptImage is an image as it travels to the engine; Data is base64 in JSON.
type promptImage struct {
	MIMEType string `json:"mimeType"`
	Data     []byte `json:"data"`
}

// ValidImages reports whether a turn's photos are within the limits: at most
// MaxImages, none empty or over MaxImageBytes, MaxImagesBytes in all, each of
// an accepted type that its bytes confirm, with a readable header declaring at
// most MaxImagePixels. Nothing past the header is decoded.
func ValidImages(images []Image) bool {
	if len(images) > MaxImages {
		return false
	}
	total := 0
	for _, im := range images {
		n := len(im.Data)
		if n == 0 || n > MaxImageBytes || !acceptedImageTypes[im.MIME] || http.DetectContentType(im.Data) != im.MIME {
			return false
		}
		w, h, ok := imageSize(im.MIME, im.Data)
		if !ok || w <= 0 || h <= 0 || w > MaxImagePixels/h {
			return false
		}
		total += n
	}
	return total <= MaxImagesBytes
}

func toPromptImages(images []Image) []promptImage {
	if len(images) == 0 {
		return nil
	}
	out := make([]promptImage, len(images))
	for i, im := range images {
		out[i] = promptImage{MIMEType: im.MIME, Data: im.Data}
	}
	return out
}

// imageSize reads the dimensions an image's header declares.
func imageSize(mime string, data []byte) (int, int, bool) {
	var cfg image.Config
	var err error
	switch mime {
	case "image/jpeg":
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(data))
	case "image/png":
		cfg, err = png.DecodeConfig(bytes.NewReader(data))
	case "image/webp":
		return webpSize(data)
	default:
		return 0, 0, false
	}
	if err != nil {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// webpSize reads the first chunk of a WebP file: VP8 (lossy), VP8L (lossless)
// or VP8X (extended), each of which carries the canvas size.
func webpSize(b []byte) (int, int, bool) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, false
	}
	d := b[20:]
	switch string(b[12:16]) {
	case "VP8 ":
		if d[3] != 0x9d || d[4] != 0x01 || d[5] != 0x2a {
			return 0, 0, false
		}
		return int(binary.LittleEndian.Uint16(d[6:]) & 0x3fff), int(binary.LittleEndian.Uint16(d[8:]) & 0x3fff), true
	case "VP8L":
		if d[0] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(d[1:])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, true
	case "VP8X":
		w := int(d[4]) | int(d[5])<<8 | int(d[6])<<16
		h := int(d[7]) | int(d[8])<<8 | int(d[9])<<16
		return w + 1, h + 1, true
	}
	return 0, 0, false
}

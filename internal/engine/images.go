package engine

import "net/http"

// Photo limits for one turn. Photos travel with the turn to the engine and on
// to the person's model; training-mcp never stores them. The browser already
// re-encodes them (no EXIF, at most 1600 px), so these limits are generous for
// a real photo and tight for anything else.
const (
	MaxImages      = 4
	MaxImageBytes  = 2 << 20
	MaxImagesBytes = 6 << 20
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
// MaxImages, none empty or over MaxImageBytes, MaxImagesBytes in all, and each
// of an accepted type that its bytes confirm.
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

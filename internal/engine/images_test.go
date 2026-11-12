package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// imageOf is a real image header of the given format that declares w x h
// pixels, padded to n bytes (or left as is when shorter). The pixel data is
// never needed: only the header is read.
func imageOf(mime string, w, h, n int) []byte {
	var b []byte
	switch mime {
	case "image/jpeg":
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
			panic(err)
		}
		b = buf.Bytes()
		sof := bytes.Index(b, []byte{0xff, 0xc0})
		binary.BigEndian.PutUint16(b[sof+5:], uint16(h))
		binary.BigEndian.PutUint16(b[sof+7:], uint16(w))
	case "image/png":
		var buf bytes.Buffer
		if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
			panic(err)
		}
		b = buf.Bytes()
		binary.BigEndian.PutUint32(b[16:], uint32(w))
		binary.BigEndian.PutUint32(b[20:], uint32(h))
		binary.BigEndian.PutUint32(b[29:], crc32.ChecksumIEEE(b[12:29]))
	case "image/webp":
		b = webpVP8X(w, h)
	default:
		panic(mime)
	}
	if n > len(b) {
		b = append(b, bytes.Repeat([]byte{0}, n-len(b))...)
	}
	return b
}

// webpVP8X is an extended-format WebP header for w x h.
func webpVP8X(w, h int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00")
	b = append(b, byte(w-1), byte((w-1)>>8), byte((w-1)>>16), byte(h-1), byte((h-1)>>8), byte((h-1)>>16))
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	return b
}

// webpVP8L is a lossless WebP header for w x h.
func webpVP8L(w, h int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBPVP8L\x0a\x00\x00\x00\x2f")
	b = binary.LittleEndian.AppendUint32(b, uint32(w-1)|uint32(h-1)<<14)
	return append(b, 0, 0, 0, 0, 0)
}

// webpVP8 is a lossy WebP header for w x h.
func webpVP8(w, h int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBPVP8 \x0a\x00\x00\x00\x00\x00\x00\x9d\x01\x2a")
	b = binary.LittleEndian.AppendUint16(b, uint16(w))
	return binary.LittleEndian.AppendUint16(b, uint16(h))
}

func jpegBytes(n int) []byte { return imageOf("image/jpeg", 64, 48, n) }
func pngBytes(n int) []byte  { return imageOf("image/png", 64, 48, n) }
func webpBytes() []byte      { return imageOf("image/webp", 64, 48, 0) }

// A decompression bomb is small in bytes and huge in pixels: each format is
// refused from its header, before anything decodes it.
func TestValidImagesRefusesTooManyPixels(t *testing.T) {
	side := 1
	for side*side <= MaxImagePixels {
		side *= 2
	}
	for _, c := range []struct {
		name string
		im   Image
		ok   bool
	}{
		{"jpeg at the cap", Image{"image/jpeg", imageOf("image/jpeg", MaxImagePixels/1000, 1000, 0)}, true},
		{"jpeg over the cap", Image{"image/jpeg", imageOf("image/jpeg", side, side, 0)}, false},
		{"png over the cap", Image{"image/png", imageOf("image/png", side, side, 0)}, false},
		{"png one pixel too wide", Image{"image/png", imageOf("image/png", MaxImagePixels+1, 1, 0)}, false},
		{"webp extended over the cap", Image{"image/webp", webpVP8X(side, side)}, false},
		{"webp lossless within the cap", Image{"image/webp", webpVP8L(3000, 3000)}, true},
		{"webp lossless over the cap", Image{"image/webp", webpVP8L(side, side)}, false},
		{"webp lossy within the cap", Image{"image/webp", webpVP8(3000, 3000)}, true},
		{"webp lossy over the cap", Image{"image/webp", webpVP8(side, side)}, false},
		{"jpeg with a broken header", Image{"image/jpeg", []byte("\xff\xd8\xff\xe0jjjjjjjj")}, false},
		{"png with a broken header", Image{"image/png", []byte("\x89PNG\r\n\x1a\nIHDR")}, false},
		{"webp with a broken header", Image{"image/webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 xxxx")}, false},
	} {
		if got := ValidImages([]Image{c.im}); got != c.ok {
			t.Errorf("%s: ValidImages = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestValidImages(t *testing.T) {
	cases := []struct {
		name   string
		images []Image
		ok     bool
	}{
		{"none", nil, true},
		{"jpeg", []Image{{"image/jpeg", jpegBytes(100)}}, true},
		{"png and webp", []Image{{"image/png", pngBytes(100)}, {"image/webp", webpBytes()}}, true},
		{"four at the size limit", []Image{{"image/jpeg", jpegBytes(MaxImagesBytes / 4)}, {"image/jpeg", jpegBytes(MaxImagesBytes / 4)}, {"image/jpeg", jpegBytes(MaxImagesBytes / 4)}, {"image/jpeg", jpegBytes(MaxImagesBytes / 4)}}, true},
		{"five", []Image{{"image/jpeg", jpegBytes(10)}, {"image/jpeg", jpegBytes(10)}, {"image/jpeg", jpegBytes(10)}, {"image/jpeg", jpegBytes(10)}, {"image/jpeg", jpegBytes(10)}}, false},
		{"empty", []Image{{"image/jpeg", nil}}, false},
		{"one over the image limit", []Image{{"image/jpeg", jpegBytes(MaxImageBytes + 1)}}, false},
		{"over the turn limit", []Image{{"image/jpeg", jpegBytes(MaxImageBytes)}, {"image/jpeg", jpegBytes(MaxImageBytes)}, {"image/jpeg", jpegBytes(MaxImageBytes)}, {"image/jpeg", jpegBytes(10)}}, false},
		{"gif", []Image{{"image/gif", []byte("GIF89a......")}}, false},
		{"svg", []Image{{"image/svg+xml", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")}}, false},
		{"declared jpeg, bytes png", []Image{{"image/jpeg", pngBytes(100)}}, false},
		{"declared jpeg, bytes html", []Image{{"image/jpeg", []byte("<html><script>x</script>")}}, false},
		{"no type", []Image{{"", jpegBytes(100)}}, false},
	}
	for _, c := range cases {
		if got := ValidImages(c.images); got != c.ok {
			t.Errorf("%s: ValidImages = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestPromptMessageEncodesImagesAsBase64(t *testing.T) {
	data := jpegBytes(20)
	line, err := json.Marshal(parentMessage{Type: "prompt", Text: "mira", Images: toPromptImages([]Image{{"image/jpeg", data}})})
	if err != nil {
		t.Fatal(err)
	}
	want := `"images":[{"mimeType":"image/jpeg","data":"` + base64.StdEncoding.EncodeToString(data) + `"}]`
	if !strings.Contains(string(line), want) {
		t.Fatalf("prompt line = %s", line)
	}
	plain, _ := json.Marshal(parentMessage{Type: "prompt", Text: "hola"})
	if strings.Contains(string(plain), "images") {
		t.Fatalf("a prompt without photos carries an images field: %s", plain)
	}
}

func photoDigest(mime string, data []byte) string {
	return fmt.Sprintf("<%s:%d:%x>", mime, len(data), sha256.Sum256(data))
}

func TestPromptCarriesPhotosToTheEngine(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.connect(1)
	ch, closeSub := h.m.Subscribe(1)
	defer closeSub()
	// Two photos as large as a turn allows: the line to the engine is megabytes.
	a, b := jpegBytes(MaxImageBytes), pngBytes(MaxImageBytes)
	if err := prompt(h.m, 1, "¿qué tal la técnica?", Image{"image/jpeg", a}, Image{"image/png", b}); err != nil {
		t.Fatal(err)
	}
	events, text := collect(t, ch)
	if last(events).Name != "done" {
		t.Fatalf("last event = %+v", last(events))
	}
	if !strings.Contains(text, "TEXT<¿qué tal la técnica?>IMAGES"+photoDigest("image/jpeg", a)+photoDigest("image/png", b)) {
		t.Fatalf("answer = %q", text)
	}
	// Photo bytes never reach the log.
	if logs := h.logs.String(); strings.Contains(logs, base64.StdEncoding.EncodeToString(a[:64])) || len(logs) > 64<<10 {
		t.Fatalf("photo bytes in the log (%d bytes)", len(logs))
	}
}

func TestPromptWithOnlyAPhoto(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.connect(1)
	ch, closeSub := h.m.Subscribe(1)
	defer closeSub()
	img := jpegBytes(50)
	if err := prompt(h.m, 1, "", Image{"image/jpeg", img}); err != nil {
		t.Fatal(err)
	}
	_, text := collect(t, ch)
	if !strings.Contains(text, "TEXT<"+photoOnlyText+">IMAGES"+photoDigest("image/jpeg", img)) {
		t.Fatalf("answer = %q", text)
	}
}

func TestPromptRefusesBadPhotosBeforeAnyTurn(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.connect(1)
	for name, images := range map[string][]Image{
		"gif":       {{"image/gif", []byte("GIF89a......")}},
		"too large": {{"image/jpeg", jpegBytes(MaxImageBytes + 1)}},
		"five":      {{"image/jpeg", jpegBytes(9)}, {"image/jpeg", jpegBytes(9)}, {"image/jpeg", jpegBytes(9)}, {"image/jpeg", jpegBytes(9)}, {"image/jpeg", jpegBytes(9)}},
	} {
		if err := prompt(h.m, 1, "mira", images...); codeOf(err) != CodeProviderError {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := prompt(h.m, 1, ""); codeOf(err) != CodeProviderError {
		t.Errorf("no text and no photo: %v", err)
	}
	// Nothing started: a good turn still goes through.
	if err := prompt(h.m, 1, "hola"); err != nil {
		t.Fatalf("turn after refusals: %v", err)
	}
}

// One person's photos reach that person's engine and no one else's.
func TestPhotosStayWithTheirPerson(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.connect(1)
	h.connect(2)
	ch1, close1 := h.m.Subscribe(1)
	defer close1()
	ch2, close2 := h.m.Subscribe(2)
	defer close2()
	img := jpegBytes(80)
	if err := prompt(h.m, 2, "mira", Image{"image/jpeg", img}); err != nil {
		t.Fatal(err)
	}
	if err := prompt(h.m, 1, "hola"); err != nil {
		t.Fatal(err)
	}
	_, text2 := collect(t, ch2)
	_, text1 := collect(t, ch1)
	if !strings.Contains(text2, "IMAGES"+photoDigest("image/jpeg", img)) {
		t.Fatalf("person 2 answer = %q", text2)
	}
	if !strings.Contains(text1, "TEXT<hola>IMAGES") || strings.Contains(text1, "image/") {
		t.Fatalf("person 1 got photos: %q", text1)
	}
}

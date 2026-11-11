package websrv_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/engine"
)

func jpeg(n int) []byte { return append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte{'j'}, n-4)...) }

type photo struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

func photoBody(t *testing.T, text string, photos ...photo) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"text": text, "images": photos})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func TestCoachPromptCarriesPhotos(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	a := jpeg(engine.MaxImageBytes)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{'p'}, 100)...)
	body := photoBody(t, " mira mi sentadilla ", photo{"image/jpeg", b64(a)}, photo{"image/png", b64(png)})
	w := coachRequest(t, world.as(2), "POST", "/coach/prompt", body, true)
	if w.Code != http.StatusAccepted {
		t.Fatalf("prompt with photos: %d %s", w.Code, w.Body)
	}
	if len(coach.images) != 2 || coach.images[0].MIME != "image/jpeg" || !bytes.Equal(coach.images[0].Data, a) || coach.images[1].MIME != "image/png" || !bytes.Equal(coach.images[1].Data, png) {
		t.Fatalf("coach got %d photos", len(coach.images))
	}
	if coach.calls[0] != "prompt:mira mi sentadilla" || len(coach.callers) != 1 || !coach.callers[2] {
		t.Fatalf("calls = %v callers = %v", coach.calls, coach.callers)
	}
}

func TestCoachPromptWithOnlyAPhoto(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	if w := coachRequest(t, world.as(1), "POST", "/coach/prompt", photoBody(t, "", photo{"image/jpeg", b64(jpeg(50))}), true); w.Code != http.StatusAccepted {
		t.Fatalf("photo only: %d %s", w.Code, w.Body)
	}
	if len(coach.images) != 1 {
		t.Fatalf("coach got %d photos", len(coach.images))
	}
}

func TestCoachPromptRejectsBadPhotos(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	small := photo{"image/jpeg", b64(jpeg(50))}
	cases := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"five photos", photoBody(t, "x", small, small, small, small, small), http.StatusBadRequest, "too_many_images"},
		{"gif", photoBody(t, "x", photo{"image/gif", b64([]byte("GIF89a......"))}), http.StatusBadRequest, "bad_image"},
		{"svg", photoBody(t, "x", photo{"image/svg+xml", b64([]byte("<svg/>"))}), http.StatusBadRequest, "bad_image"},
		{"declared jpeg, bytes html", photoBody(t, "x", photo{"image/jpeg", b64([]byte("<html><script>x</script>"))}), http.StatusBadRequest, "bad_image"},
		{"empty photo", photoBody(t, "x", photo{"image/jpeg", ""}), http.StatusBadRequest, "bad_image"},
		{"one photo over its limit", photoBody(t, "x", photo{"image/jpeg", b64(jpeg(engine.MaxImageBytes + 1))}), http.StatusBadRequest, "bad_image"},
		{"photos over the turn limit", photoBody(t, "x", photo{"image/jpeg", b64(jpeg(engine.MaxImageBytes))}, photo{"image/jpeg", b64(jpeg(engine.MaxImageBytes))}, photo{"image/jpeg", b64(jpeg(engine.MaxImageBytes))}, photo{"image/jpeg", b64(jpeg(100 << 10))}), http.StatusRequestEntityTooLarge, "images_too_large"},
		{"body over the request limit", photoBody(t, "x", photo{"image/jpeg", b64(jpeg(9 << 20))}), http.StatusRequestEntityTooLarge, "images_too_large"},
		{"not base64", `{"text":"x","images":[{"type":"image/jpeg","data":"%%%"}]}`, http.StatusBadRequest, "bad_request"},
		{"unknown photo field", `{"text":"x","images":[{"type":"image/jpeg","data":"","url":"http://x"}]}`, http.StatusBadRequest, "bad_request"},
		{"no text, no photo", `{"text":"  ","images":[]}`, http.StatusBadRequest, "bad_request"},
	}
	for _, c := range cases {
		w := coachRequest(t, world.as(1), "POST", "/coach/prompt", c.body, true)
		if w.Code != c.status || !strings.Contains(w.Body.String(), `"error":"`+c.code+`"`) {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
	}
	if len(coach.calls) != 0 {
		t.Fatalf("coach reached: %v", coach.calls)
	}
}

// A cross-site form cannot send photos either: no coach header, no turn.
func TestCoachPhotosNeedTheCoachHeader(t *testing.T) {
	coach := newFakeCoach()
	world := newCoachWorld(t, coach)
	if w := coachRequest(t, world.as(1), "POST", "/coach/prompt", photoBody(t, "x", photo{"image/jpeg", b64(jpeg(50))}), false); w.Code != http.StatusForbidden {
		t.Fatalf("without the header: %d", w.Code)
	}
	if len(coach.calls) != 0 {
		t.Fatalf("coach reached: %v", coach.calls)
	}
}

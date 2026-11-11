package websrv_test

import (
	"bytes"
	"database/sql"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/importer"
)

// hevyExport is a small Hevy workout export: two workouts, a warm-up, an unrated set.
const hevyExport = `"title","start_time","end_time","description","exercise_title","superset_id","exercise_notes","set_index","set_type","weight_kg","reps","distance_km","duration_seconds","rpe"
"Push","22 Dec 2025, 08:00","22 Dec 2025, 08:37","","Bench Press (Barbell)",,"",0,"warmup",40,10,,,
"Push","22 Dec 2025, 08:00","22 Dec 2025, 08:37","","Bench Press (Barbell)",,"",1,"normal",80,8,,,8.5
"Push","22 Dec 2025, 08:00","22 Dec 2025, 08:37","","Bench Press (Barbell)",,"",2,"normal",80,7,,,
"Legs","20 Dec 2025, 18:30","20 Dec 2025, 19:30","","Squat (Barbell)",,"",0,"normal",120,5,,,9
`

func upload(t *testing.T, h http.Handler, path string, fields url.Values, fileName, content string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, vs := range fields {
		for _, v := range vs {
			if err := mw.WriteField(k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	part, err := mw.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err = mw.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, path, &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var payloadField = regexp.MustCompile(`name="payload" value="([^"]+)"`)

// confirmForm is the preview's form as the person would send it back.
func confirmForm(t *testing.T, preview string, mapping map[string]string) url.Values {
	t.Helper()
	m := payloadField.FindStringSubmatch(preview)
	if m == nil {
		t.Fatalf("no payload in preview:\n%s", preview)
	}
	form := url.Values{"payload": {html.UnescapeString(m[1])}, "file_name": {"workouts.csv"}, "unit": {"kg"}}
	for src, dst := range mapping {
		form.Add("src", src)
		form.Add("dst", dst)
	}
	return form
}

func countSQL(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportPreviewShowsWhatWouldBeImportedAndWritesNothing(t *testing.T) {
	world := newWebWorld(t)
	before := dumpDatabase(t, world.db)
	w := upload(t, world.second, base+"/import/preview", nil, "workouts.csv", hevyExport)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{"2 sesiones", "3 series", "1 sin RPE", "Bench Press (Barbell)", "Squat (Barbell)", "Calentamientos: 1", `name="dst"`, "Importar"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview lacks %q:\n%s", want, body)
		}
	}
	if after := dumpDatabase(t, world.db); after != before {
		t.Fatalf("a preview changed rows:\nbefore\n%s\nafter\n%s", before, after)
	}
}

func TestImportApplyRecordsTheHistoryOnceAndRemembersNames(t *testing.T) {
	world := newWebWorld(t)
	preview := upload(t, world.second, base+"/import/preview", nil, "workouts.csv", hevyExport)
	form := confirmForm(t, preview.Body.String(), map[string]string{"Bench Press (Barbell)": "Press banca", "Squat (Barbell)": ""})
	w := post(t, world.second, base+"/import/apply", form, false)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Importadas 3 series en 2 sesiones") {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if n := countSQL(t, world.db, `SELECT COUNT(*) FROM sets st JOIN sessions se ON se.id=st.session_id WHERE se.user_id=2 AND st.exercise='press banca'`); n != 2 {
		t.Fatalf("bench sets = %d", n)
	}
	if n := countSQL(t, world.db, `SELECT COUNT(*) FROM sets st JOIN sessions se ON se.id=st.session_id WHERE se.user_id=2 AND st.rpe_imputed=1`); n != 1 {
		t.Fatalf("imputed sets = %d", n)
	}
	before := dumpDatabase(t, world.db)
	again := post(t, world.second, base+"/import/apply", form, false)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), "No había nada nuevo") {
		t.Fatalf("status %d: %s", again.Code, again.Body)
	}
	if after := dumpDatabase(t, world.db); after != before {
		t.Fatalf("a second import changed rows:\nbefore\n%s\nafter\n%s", before, after)
	}
	page := get(t, world.second, base+"/import")
	if !strings.Contains(page.Body.String(), "workouts.csv") || !strings.Contains(page.Body.String(), "3 series") {
		t.Fatalf("history:\n%s", page.Body)
	}
	// The next preview starts from the name chosen this time.
	next := upload(t, world.second, base+"/import/preview", nil, "workouts.csv", hevyExport)
	if !strings.Contains(next.Body.String(), `value="press banca"`) || !strings.Contains(next.Body.String(), "3 ya importadas") {
		t.Fatalf("next preview:\n%s", next.Body)
	}
}

func TestImportRefusesWhatItCannotRead(t *testing.T) {
	world := newWebWorld(t)
	before := dumpDatabase(t, world.db)
	cases := []struct {
		name    string
		send    func() *httptest.ResponseRecorder
		status  int
		message string
	}{
		{"not an export", func() *httptest.ResponseRecorder {
			return upload(t, world.second, base+"/import/preview", nil, "notes.csv", "a,b\n1,2\n")
		}, http.StatusBadRequest, "no parece una exportación"},
		{"too large", func() *httptest.ResponseRecorder {
			return upload(t, world.second, base+"/import/preview", nil, "big.csv", strings.Repeat("x", importer.MaxBytes+1))
		}, http.StatusRequestEntityTooLarge, "demasiado grande"},
		{"no file", func() *httptest.ResponseRecorder {
			return post(t, world.second, base+"/import/preview", url.Values{}, false)
		}, http.StatusBadRequest, "Elige el archivo"},
		{"tampered payload", func() *httptest.ResponseRecorder {
			return post(t, world.second, base+"/import/apply", url.Values{"payload": {"!!!"}}, false)
		}, http.StatusBadRequest, "Vuelve a subir"},
		{"unknown unit", func() *httptest.ResponseRecorder {
			return upload(t, world.second, base+"/import/preview", url.Values{"unit": {"stone"}}, "workouts.csv", hevyExport)
		}, http.StatusBadRequest, "no parece una exportación"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.send()
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.message) {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
		})
	}
	if after := dumpDatabase(t, world.db); after != before {
		t.Fatalf("a refused import changed rows:\nbefore\n%s\nafter\n%s", before, after)
	}
}

// The history page links the import panel; nothing else on it changes.
func TestHistoryLinksTheImportPanel(t *testing.T) {
	world := newWebWorld(t)
	if body := get(t, world.owner, base+"/history").Body.String(); !strings.Contains(body, base+"/import") {
		t.Fatalf("history:\n%s", body)
	}
}

// A second user previewing the very file the owner imported sees it all as new,
// and importing it records their own rows only.
func testImportingTheSameFileAsAnotherUserIsYourOwn(t *testing.T) {
	world := newWebWorld(t)
	preview := upload(t, world.second, base+"/import/preview", nil, "workouts.csv", ownerExport)
	if !strings.Contains(preview.Body.String(), "1 serie ") || strings.Contains(preview.Body.String(), "ya importada") || strings.Contains(preview.Body.String(), secretExercise) {
		t.Fatalf("second user's preview:\n%s", preview.Body)
	}
}

func testApplyingAnImportTouchesOnlyTheUsersOwnRows(t *testing.T) {
	world := newWebWorld(t)
	owner := dumpOwner(t, world.db)
	preview := upload(t, world.second, base+"/import/preview", nil, "workouts.csv", ownerExport)
	if w := post(t, world.second, base+"/import/apply", confirmForm(t, preview.Body.String(), nil), false); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if n := countSQL(t, world.db, `SELECT COUNT(*) FROM imported_sets k JOIN sets st ON st.id=k.set_id JOIN sessions se ON se.id=st.session_id WHERE k.user_id=2 AND se.user_id=2`); n != 1 {
		t.Fatalf("user 2's imported sets = %d", n)
	}
	if after := dumpOwner(t, world.db); after != owner {
		t.Fatalf("the owner's rows changed:\nbefore\n%s\nafter\n%s", owner, after)
	}
}

// dumpOwner is user 1's imports and sessions with their sets.
func dumpOwner(t *testing.T, db *sql.DB) string {
	t.Helper()
	var out strings.Builder
	for _, q := range []string{
		`SELECT id||'|'||source||'|'||content_hash||'|'||sets FROM imports WHERE user_id=1 ORDER BY id`,
		`SELECT natural_key||'|'||COALESCE(set_id,0) FROM imported_sets WHERE user_id=1 ORDER BY natural_key`,
		`SELECT source_name||'|'||exercise FROM import_exercise_map WHERE user_id=1 ORDER BY source_name`,
		`SELECT se.id||'|'||se.date||'|'||se.notes||'|'||st.exercise||'|'||st.weight_kg FROM sessions se JOIN sets st ON st.session_id=se.id WHERE se.user_id=1 ORDER BY st.id`,
	} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err = rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			out.WriteString(line + "\n")
		}
		rows.Close()
	}
	return out.String()
}

// An imported workout's notes show on its session page.
func TestAnImportedSessionShowsItsNotes(t *testing.T) {
	world := newWebWorld(t)
	var id int64
	if err := world.db.QueryRow(`SELECT id FROM sessions WHERE user_id=1 AND notes<>''`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if body := get(t, world.owner, base+"/s/"+itoa(id)).Body.String(); !strings.Contains(body, "Push") {
		t.Fatalf("session page:\n%s", body)
	}
}

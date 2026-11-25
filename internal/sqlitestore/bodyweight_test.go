package sqlitestore

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// bodyweightClock is a Sunday evening; today is 2026-11-22.
func newBodyweightService(t *testing.T) (*Store, *training.Service, *training.Service) {
	t.Helper()
	store, err := Open(freshDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err := store.db.Exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return time.Date(2026, 11, 22, 21, 0, 0, 0, time.UTC) }
	return store, training.NewService(store.ForUser(1), clock), training.NewService(store.ForUser(2), clock)
}

type storedBodyweight struct {
	date   string
	kg     float64
	note   string
	noteOK bool
}

// bodyweightRows reads the raw rows of a user, oldest first.
func bodyweightRows(t *testing.T, store *Store, userID int64) []storedBodyweight {
	t.Helper()
	rows, err := store.db.Query(`SELECT date,weight_kg,note FROM bodyweight WHERE user_id=? ORDER BY date`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []storedBodyweight
	for rows.Next() {
		var r storedBodyweight
		var note *string
		if err := rows.Scan(&r.date, &r.kg, &note); err != nil {
			t.Fatal(err)
		}
		if note != nil {
			r.note, r.noteOK = *note, true
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLogBodyweightInLbIsStoredAsKGAndReadBackInLb(t *testing.T) {
	ctx := context.Background()
	store, svc, _ := newBodyweightService(t)
	got, err := svc.LogBodyweight(ctx, training.BodyweightInput{Weight: 176.4, Unit: "lb"})
	if err != nil {
		t.Fatal(err)
	}
	wantKG := training.ToKG(176.4, training.UnitLB)
	if got.Date != "2026-11-22" || got.WeightKG != wantKG {
		t.Fatalf("logged %+v, want today at %v kg", got, wantKG)
	}
	rows := bodyweightRows(t, store, 1)
	if len(rows) != 1 || rows[0].date != "2026-11-22" || rows[0].kg != wantKG || rows[0].noteOK {
		t.Fatalf("stored rows = %+v, want one row of %v kg and a NULL note", rows, wantKG)
	}
	if back := training.FromKG(rows[0].kg, training.UnitLB); back != 176.4 {
		t.Fatalf("stored kg shows as %v lb, want 176.4", back)
	}
}

func TestLogBodyweightOnTheSameDateReplacesTheEntry(t *testing.T) {
	ctx := context.Background()
	store, svc, _ := newBodyweightService(t)
	if _, err := svc.LogBodyweight(ctx, training.BodyweightInput{Date: "2026-11-20", Weight: 80, Unit: "kg", Note: "en ayunas"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.LogBodyweight(ctx, training.BodyweightInput{Date: "2026-11-20", Weight: 79.4, Unit: "kg"}); err != nil {
		t.Fatal(err)
	}
	rows := bodyweightRows(t, store, 1)
	if len(rows) != 1 || rows[0].kg != 79.4 || rows[0].noteOK {
		t.Fatalf("stored rows = %+v, want one entry of 79.4 kg with no note", rows)
	}
}

func TestLogBodyweightRejectsInvalidValuesClearly(t *testing.T) {
	ctx := context.Background()
	store, svc, _ := newBodyweightService(t)
	for name, tc := range map[string]struct {
		in   training.BodyweightInput
		want string
	}{
		"zero":          {training.BodyweightInput{Weight: 0, Unit: "kg"}, "weight must be between 20 and 400 kg"},
		"negative":      {training.BodyweightInput{Weight: -70, Unit: "kg"}, "weight must be between 20 and 400 kg"},
		"too light":     {training.BodyweightInput{Weight: 19.9, Unit: "kg"}, "weight must be between 20 and 400 kg"},
		"too heavy lb":  {training.BodyweightInput{Weight: 900, Unit: "lb"}, "weight must be between 20 and 400 kg"},
		"unknown unit":  {training.BodyweightInput{Weight: 80, Unit: "stone"}, "unit must be kg or lb"},
		"missing unit":  {training.BodyweightInput{Weight: 80}, "unit must be kg or lb"},
		"bad date":      {training.BodyweightInput{Date: "2026-13-01", Weight: 80, Unit: "kg"}, "date must be YYYY-MM-DD"},
		"long note":     {training.BodyweightInput{Weight: 80, Unit: "kg", Note: strings.Repeat("x", 501)}, "note is at most 500 characters"},
		"not a number":  {training.BodyweightInput{Weight: math.NaN(), Unit: "kg"}, "weight must be between 20 and 400 kg"},
		"infinite (lb)": {training.BodyweightInput{Weight: math.Inf(1), Unit: "lb"}, "weight must be between 20 and 400 kg"},
	} {
		_, err := svc.LogBodyweight(ctx, tc.in)
		if !errors.Is(err, training.ErrValidation) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want a validation error saying %q", name, err, tc.want)
		}
	}
	if rows := bodyweightRows(t, store, 1); len(rows) != 0 {
		t.Fatalf("a refused entry was stored: %+v", rows)
	}
}

func TestBodyweightHistorySummarizesTheRangeWithPlainNumbers(t *testing.T) {
	ctx := context.Background()
	_, svc, _ := newBodyweightService(t)
	for _, e := range []training.BodyweightInput{
		{Date: "2026-10-01", Weight: 83, Unit: "kg"}, // outside the range asked
		{Date: "2026-11-01", Weight: 82, Unit: "kg"},
		{Date: "2026-11-10", Weight: 81.5, Unit: "kg", Note: "tras viaje"},
		{Date: "2026-11-16", Weight: 80, Unit: "kg"},
		{Date: "2026-11-19", Weight: 80.6, Unit: "kg"},
		{Date: "2026-11-21", Weight: 80.2, Unit: "kg"},
	} {
		if _, err := svc.LogBodyweight(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	h, err := svc.BodyweightHistory(ctx, "2026-11-01", "2026-11-22")
	if err != nil {
		t.Fatal(err)
	}
	if h.From != "2026-11-01" || h.To != "2026-11-22" || len(h.Entries) != 5 {
		t.Fatalf("history = %+v, want 5 entries from 2026-11-01 to 2026-11-22", h)
	}
	if h.Entries[0].Date != "2026-11-01" || h.Entries[4].Date != "2026-11-21" || h.Entries[1].Note != "tras viaje" {
		t.Fatalf("entries are not oldest first with their notes: %+v", h.Entries)
	}
	s := h.Summary
	if s == nil {
		t.Fatal("no summary for a range with entries")
	}
	if s.Count != 5 || s.First.Date != "2026-11-01" || s.First.WeightKG != 82 || s.Last.Date != "2026-11-21" || s.Last.WeightKG != 80.2 {
		t.Fatalf("first/last = %+v / %+v, count %d", s.First, s.Last, s.Count)
	}
	if s.Min.Date != "2026-11-16" || s.Min.WeightKG != 80 || s.Max.Date != "2026-11-01" || s.Max.WeightKG != 82 {
		t.Fatalf("min/max = %+v / %+v", s.Min, s.Max)
	}
	if s.ChangeKG != -1.8 {
		t.Fatalf("change = %v, want -1.8 (last minus first)", s.ChangeKG)
	}
	// The 7 days ending on the last entry: 11-15 to 11-21 hold 80, 80.6 and 80.2.
	if s.Avg7.From != "2026-11-15" || s.Avg7.To != "2026-11-21" || s.Avg7.Entries != 3 || s.Avg7.WeightKG != 80.27 {
		t.Fatalf("7-day average = %+v, want 80.27 kg over 3 entries from 11-15 to 11-21", s.Avg7)
	}

	empty, err := svc.BodyweightHistory(ctx, "2026-01-01", "2026-01-31")
	if err != nil || len(empty.Entries) != 0 || empty.Summary != nil {
		t.Fatalf("empty range = %+v, %v; want no entries and no summary", empty, err)
	}
}

func TestBodyweightHistoryDefaultsAndBoundsItsRange(t *testing.T) {
	ctx := context.Background()
	_, svc, _ := newBodyweightService(t)
	h, err := svc.BodyweightHistory(ctx, "", "")
	if err != nil || h.From != "2026-08-25" || h.To != "2026-11-22" {
		t.Fatalf("default range = %s..%s, %v; want the 90 days ending today", h.From, h.To, err)
	}
	h, err = svc.BodyweightHistory(ctx, "2026-11-01", "")
	if err != nil || h.From != "2026-11-01" || h.To != "2026-11-22" {
		t.Fatalf("open-ended range = %s..%s, %v; want it to end today", h.From, h.To, err)
	}
	h, err = svc.BodyweightHistory(ctx, "", "2026-03-31")
	if err != nil || h.From != "2026-01-01" || h.To != "2026-03-31" {
		t.Fatalf("range ending on a date = %s..%s, %v; want the 90 days ending then", h.From, h.To, err)
	}
	for name, tc := range map[string]struct{ from, to, want string }{
		"reversed":  {"2026-11-10", "2026-11-01", "from must not be after to"},
		"too long":  {"2025-01-01", "2026-11-22", "at most 366 days"},
		"bad from":  {"2026-11-1", "2026-11-22", "from must be YYYY-MM-DD"},
		"bad to":    {"2026-11-01", "tomorrow", "to must be YYYY-MM-DD"},
		"leap span": {"2023-11-22", "2024-11-22", "at most 366 days"},
	} {
		_, err := svc.BodyweightHistory(ctx, tc.from, tc.to)
		if !errors.Is(err, training.ErrValidation) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want a validation error saying %q", name, err, tc.want)
		}
	}
}

func TestDeleteBodyweightRemovesOnlyThatDay(t *testing.T) {
	ctx := context.Background()
	store, svc, _ := newBodyweightService(t)
	for _, d := range []string{"2026-11-20", "2026-11-21"} {
		if _, err := svc.LogBodyweight(ctx, training.BodyweightInput{Date: d, Weight: 80, Unit: "kg"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.DeleteBodyweight(ctx, "2026-11-20"); err != nil {
		t.Fatal(err)
	}
	if rows := bodyweightRows(t, store, 1); len(rows) != 1 || rows[0].date != "2026-11-21" {
		t.Fatalf("rows after delete = %+v", rows)
	}
	if err := svc.DeleteBodyweight(ctx, "2026-11-20"); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("deleting a day with no entry: err = %v, want ErrNotFound", err)
	}
	if err := svc.DeleteBodyweight(ctx, "ayer"); !errors.Is(err, training.ErrValidation) {
		t.Fatalf("deleting a malformed date: err = %v, want ErrValidation", err)
	}
}

func TestBodyweightIsEachPersonsOwn(t *testing.T) {
	ctx := context.Background()
	store, owner, other := newBodyweightService(t)
	if _, err := owner.LogBodyweight(ctx, training.BodyweightInput{Date: "2026-11-20", Weight: 80, Unit: "kg"}); err != nil {
		t.Fatal(err)
	}
	h, err := other.BodyweightHistory(ctx, "2026-11-01", "2026-11-22")
	if err != nil || len(h.Entries) != 0 {
		t.Fatalf("the other person sees %+v, %v", h.Entries, err)
	}
	if err := other.DeleteBodyweight(ctx, "2026-11-20"); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("the other person deleting the owner's day: err = %v, want ErrNotFound", err)
	}
	if _, err := other.LogBodyweight(ctx, training.BodyweightInput{Date: "2026-11-20", Weight: 60, Unit: "kg"}); err != nil {
		t.Fatal(err)
	}
	if rows := bodyweightRows(t, store, 1); len(rows) != 1 || rows[0].kg != 80 {
		t.Fatalf("the owner's entry changed: %+v", rows)
	}
	if rows := bodyweightRows(t, store, 2); len(rows) != 1 || rows[0].kg != 60 {
		t.Fatalf("the other person's entry = %+v", rows)
	}
}

func TestSetSessionNotesWritesClearsAndStaysWithinTheOwner(t *testing.T) {
	ctx := context.Background()
	_, owner, other := newBodyweightService(t)
	session, err := owner.StartSession(ctx, "2026-11-22")
	if err != nil {
		t.Fatal(err)
	}
	got, err := owner.SetSessionNotes(ctx, session.ID, "  Dormí 5 horas; hombro molesto.  ")
	if err != nil || got.Notes != "Dormí 5 horas; hombro molesto." {
		t.Fatalf("SetSessionNotes = %q, %v", got.Notes, err)
	}
	read, err := owner.GetSession(ctx, session.ID)
	if err != nil || read.Notes != "Dormí 5 horas; hombro molesto." {
		t.Fatalf("GetSession notes = %q, %v", read.Notes, err)
	}
	if _, err := other.SetSessionNotes(ctx, session.ID, "suya"); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("another person's session: err = %v, want ErrNotFound", err)
	}
	if _, err := owner.SetSessionNotes(ctx, session.ID, strings.Repeat("x", 2001)); !errors.Is(err, training.ErrValidation) || !strings.Contains(err.Error(), "at most 2000 characters") {
		t.Fatalf("long notes: err = %v", err)
	}
	if _, err := owner.SetSessionNotes(ctx, session.ID+100, "x"); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("missing session: err = %v, want ErrNotFound", err)
	}
	if got, err = owner.SetSessionNotes(ctx, session.ID, "   "); err != nil || got.Notes != "" {
		t.Fatalf("clearing notes = %q, %v", got.Notes, err)
	}
	if read, _ = owner.GetSession(ctx, session.ID); read.Notes != "" {
		t.Fatalf("notes after clearing = %q", read.Notes)
	}
}

// Migration 032 only adds the bodyweight table: every row already stored stays
// as it was, and the table starts empty.
func TestMigration032AddsAnEmptyBodyweightTableAndChangesNothingElse(t *testing.T) {
	path := t.TempDir() + "/training.db"
	db := migrateTo(t, path, 31)
	if _, err := db.Exec(`INSERT INTO sessions(id,user_id,date,started_at,notes) VALUES (1,1,'2026-11-12',CURRENT_TIMESTAMP,'importada')`); err != nil {
		t.Fatal(err)
	}
	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='bodyweight'`).Scan(&exists); err != nil || exists != 0 {
		t.Fatalf("bodyweight exists before 032: %d, %v", exists, err)
	}
	db.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM bodyweight`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("bodyweight rows after 032 = %d, %v", n, err)
	}
	var notes string
	if err := store.db.QueryRow(`SELECT notes FROM sessions WHERE id=1`).Scan(&notes); err != nil || notes != "importada" {
		t.Fatalf("existing session notes = %q, %v", notes, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=32`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("032 recorded %d times, %v", n, err)
	}
	// The table refuses what the service would never write.
	for _, bad := range []string{
		`INSERT INTO bodyweight(user_id,date,weight_kg,created_at,updated_at) VALUES (1,'2026-11-12',0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO bodyweight(user_id,date,weight_kg,note,created_at,updated_at) VALUES (1,'2026-11-12',80,'',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO bodyweight(user_id,date,weight_kg,created_at,updated_at) VALUES (99,'2026-11-12',80,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
	} {
		if _, err := store.db.Exec(bad); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
}

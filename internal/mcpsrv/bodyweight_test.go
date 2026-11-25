package mcpsrv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// bodyweightRow is the owner's raw row of a date, timestamps included, or "".
func bodyweightRow(t *testing.T, w isolationWorld, date string) string {
	t.Helper()
	rows, err := w.db.Query(`SELECT date,weight_kg,COALESCE(note,'<null>'),created_at,updated_at FROM bodyweight WHERE user_id=1 AND date=?`, date)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ""
	}
	var d, note, created, updated string
	var kg float64
	if err := rows.Scan(&d, &kg, &note, &created, &updated); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal([]any{d, kg, note, created, updated})
	return string(b)
}

func TestLogBodyweightTakesLbStoresKGAndAnswersInThePersonsUnit(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	if err := w.ownerService.SetWeightUnit(ctx, "lb"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Status string `json:"status"`
		Entry  struct {
			Date       string  `json:"date"`
			WeightKG   float64 `json:"weight_kg"`
			Weight     float64 `json:"weight"`
			WeightUnit string  `json:"weight_unit"`
		} `json:"entry"`
		Replaced any `json:"replaced"`
	}
	decode(t, call(t, w.owner, "log_bodyweight", map[string]any{"date": "2026-08-06", "weight": 176.4, "unit": "lb"}), &out)
	wantKG := training.ToKG(176.4, training.UnitLB)
	if out.Status != "accepted" || out.Entry.Date != "2026-08-06" || out.Entry.WeightKG != wantKG || out.Entry.Weight != 176.4 || out.Entry.WeightUnit != "lb" || out.Replaced != nil {
		t.Fatalf("log_bodyweight = %+v, want 176.4 lb stored as %v kg on 2026-08-06 replacing nothing", out, wantKG)
	}
	var kg float64
	if err := w.db.QueryRow(`SELECT weight_kg FROM bodyweight WHERE user_id=1 AND date='2026-08-06'`).Scan(&kg); err != nil || kg != wantKG {
		t.Fatalf("stored %v kg, %v; want %v", kg, err, wantKG)
	}

	var hist struct {
		WeightUnit string `json:"weight_unit"`
		Entries    []struct {
			Date   string  `json:"date"`
			Weight float64 `json:"weight"`
		} `json:"entries"`
	}
	decode(t, call(t, w.owner, "bodyweight_history", map[string]any{"from": "2026-08-06", "to": "2026-08-06"}), &hist)
	if hist.WeightUnit != "lb" || len(hist.Entries) != 1 || hist.Entries[0].Weight != 176.4 {
		t.Fatalf("history = %+v, want the entry back as 176.4 lb", hist)
	}
}

func TestLogBodyweightOnTheSameDayReplacesItAndUndoBringsItBack(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	before := bodyweightRow(t, w, isolationDay)
	if !strings.Contains(before, "81.5") {
		t.Fatalf("seeded row = %s", before)
	}
	// No date: today, by the server's clock.
	result := structured(t, call(t, w.owner, "log_bodyweight", map[string]any{"weight": 80.2, "unit": "kg", "note": "en ayunas"}))
	if result["status"] != "accepted" {
		t.Fatalf("result = %v", result)
	}
	entry, _ := result["entry"].(map[string]any)
	replaced, _ := result["replaced"].(map[string]any)
	if entry["date"] != isolationDay || entry["weight_kg"] != 80.2 || entry["note"] != "en ayunas" || replaced["weight_kg"] != 81.5 {
		t.Fatalf("result = %v, want 80.2 replacing 81.5 on %s", result, isolationDay)
	}
	var n int
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM bodyweight WHERE user_id=1 AND date=?`, isolationDay).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d rows for the day, %v; want 1", n, err)
	}
	if after := bodyweightRow(t, w, isolationDay); !strings.Contains(after, "80.2") || !strings.Contains(after, "en ayunas") {
		t.Fatalf("row after = %s", after)
	}
	if _, err := w.ownerService.UndoProposal(ctx, proposalOf(t, result)); err != nil {
		t.Fatal(err)
	}
	if got := bodyweightRow(t, w, isolationDay); got != before {
		t.Fatalf("after undo = %s, want %s", got, before)
	}
}

// A proposal left waiting keeps the day it was made on, not the day it is
// accepted on.
func TestPendingBodyweightKeepsTheDayItWasProposed(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	if err := w.ownerService.SetApprovalMode(ctx, training.ApprovalReviewAll); err != nil {
		t.Fatal(err)
	}
	before := bodyweightRow(t, w, isolationDay)
	result := structured(t, call(t, w.owner, "log_bodyweight", map[string]any{"weight": 79, "unit": "kg"}))
	if result["status"] != "pending" || result["entry"] != nil {
		t.Fatalf("result = %v, want pending with no entry", result)
	}
	if got := bodyweightRow(t, w, isolationDay); got != before {
		t.Fatalf("a pending proposal changed the row: %s", got)
	}
	p, err := w.ownerService.GetProposal(ctx, proposalOf(t, result))
	if err != nil || p.ActionType != "bodyweight.log" || !strings.Contains(string(p.Payload), `"date":"`+isolationDay+`"`) {
		t.Fatalf("proposal = %+v (%s), %v; want the date fixed in its payload", p, p.Payload, err)
	}
	if _, err := w.ownerService.AcceptProposal(ctx, p.ID, training.AnswerAllowOnce, w.ownerServer.Apply); err != nil {
		t.Fatal(err)
	}
	if got := bodyweightRow(t, w, isolationDay); !strings.Contains(got, `79,`) {
		t.Fatalf("row after accepting = %s", got)
	}
}

func TestLogBodyweightRefusesImplausibleValuesWithAClearMessage(t *testing.T) {
	w := newIsolationWorld(t)
	before := dumpTables(t, w.db, nil)
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"kg typed as grams": {map[string]any{"weight": 8000, "unit": "kg"}, "weight must be between 20 and 400 kg"},
		"too light":         {map[string]any{"weight": 10, "unit": "kg"}, "weight must be between 20 and 400 kg"},
		"unknown unit":      {map[string]any{"weight": 80, "unit": "st"}, "unit"},
		"no unit":           {map[string]any{"weight": 80}, "unit"},
		"bad date":          {map[string]any{"weight": 80, "unit": "kg", "date": "7/8/2026"}, "date"},
	} {
		result := call(t, w.owner, "log_bodyweight", tc.args)
		if !result.IsError || !strings.Contains(stringContent(result), tc.want) {
			t.Errorf("%s: %s, want an error saying %q", name, answer(t, result), tc.want)
		}
	}
	if after := dumpTables(t, w.db, nil); after != before {
		t.Fatalf("a refused call changed data:\nbefore\n%s\nafter\n%s", before, after)
	}
}

func TestBodyweightHistoryReturnsEntriesAndPlainSummaries(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	for date, kg := range map[string]float64{"2026-07-20": 83, "2026-08-01": 82.4, "2026-08-04": 81.9} {
		if _, err := w.ownerService.LogBodyweight(ctx, training.BodyweightInput{Date: date, Weight: kg, Unit: "kg"}); err != nil {
			t.Fatal(err)
		}
	}
	var out struct {
		From, To   string
		WeightUnit string `json:"weight_unit"`
		Entries    []map[string]any
		Summary    struct {
			Count    int
			First    struct{ Date string }
			Last     struct{ Date string }
			Min      struct{ Date string }
			Max      struct{ Date string }
			ChangeKG float64 `json:"change_kg"`
			Change   float64 `json:"change"`
			Avg7     struct {
				From, To string
				Entries  int
				WeightKG float64 `json:"weight_kg"`
			} `json:"avg_7d"`
		}
	}
	decode(t, call(t, w.owner, "bodyweight_history", map[string]any{"from": "2026-07-25"}), &out)
	if out.From != "2026-07-25" || out.To != isolationDay || out.WeightUnit != "kg" || len(out.Entries) != 3 {
		t.Fatalf("history = %+v", out)
	}
	s := out.Summary
	if s.Count != 3 || s.First.Date != "2026-08-01" || s.Last.Date != isolationDay || s.Min.Date != isolationDay || s.Max.Date != "2026-08-01" ||
		s.ChangeKG != -0.9 || s.Change != -0.9 || s.Avg7.From != "2026-08-01" || s.Avg7.Entries != 3 || s.Avg7.WeightKG != 81.93 {
		t.Fatalf("summary = %+v", s)
	}
	result := call(t, w.owner, "bodyweight_history", map[string]any{"from": "2025-01-01"})
	if !result.IsError || !strings.Contains(stringContent(result), "at most 366 days") {
		t.Fatalf("an unbounded range = %s", answer(t, result))
	}
}

func TestDeleteBodyweightRemovesTheDayAndUndoRestoresIt(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	if err := w.ownerService.SetApprovalMode(ctx, training.ApprovalAcceptAll); err != nil {
		t.Fatal(err)
	}
	before := bodyweightRow(t, w, isolationDay)
	result := structured(t, call(t, w.owner, "delete_bodyweight", map[string]any{"date": isolationDay}))
	if result["status"] != "accepted" || result["date"] != isolationDay {
		t.Fatalf("result = %v", result)
	}
	if got := bodyweightRow(t, w, isolationDay); got != "" {
		t.Fatalf("row still there: %s", got)
	}
	again := call(t, w.owner, "delete_bodyweight", map[string]any{"date": isolationDay})
	if !again.IsError || !strings.Contains(stringContent(again), "not found") {
		t.Fatalf("deleting again = %s", answer(t, again))
	}
	if _, err := w.ownerService.UndoProposal(ctx, proposalOf(t, result)); err != nil {
		t.Fatal(err)
	}
	if got := bodyweightRow(t, w, isolationDay); got != before {
		t.Fatalf("after undo = %s, want %s", got, before)
	}
}

// Deleting waits for the person in the default mode, like every other delete.
func TestDeleteBodyweightWaitsForThePersonByDefault(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	if err := w.ownerService.SetApprovalMode(ctx, training.ApprovalAcceptExceptDelete); err != nil {
		t.Fatal(err)
	}
	if r := structured(t, call(t, w.owner, "delete_bodyweight", map[string]any{"date": isolationDay})); r["status"] != "pending" {
		t.Fatalf("delete_bodyweight = %v, want pending", r)
	}
	if r := structured(t, call(t, w.owner, "log_bodyweight", map[string]any{"weight": 80, "unit": "kg", "date": "2026-08-01"})); r["status"] != "accepted" {
		t.Fatalf("log_bodyweight = %v, want accepted", r)
	}
	if bodyweightRow(t, w, isolationDay) == "" {
		t.Fatal("a pending delete removed the row")
	}
}

func TestSetSessionNotesWritesThroughAProposalAndUndoes(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	if err := w.ownerService.SetApprovalMode(ctx, training.ApprovalAcceptAll); err != nil {
		t.Fatal(err)
	}
	result := structured(t, call(t, w.owner, "set_session_notes", map[string]any{"session_id": w.ids.session, "notes": "Dormí mal; gimnasio lleno."}))
	if result["status"] != "accepted" || result["notes"] != "Dormí mal; gimnasio lleno." {
		t.Fatalf("result = %v", result)
	}
	var got notedSession
	decode(t, call(t, w.owner, "get_session", map[string]any{"session_id": w.ids.session}), &got)
	if got.Session.Notes != "Dormí mal; gimnasio lleno." {
		t.Fatalf("get_session notes = %q", got.Session.Notes)
	}
	if _, err := w.ownerService.UndoProposal(ctx, proposalOf(t, result)); err != nil {
		t.Fatal(err)
	}
	got = notedSession{}
	decode(t, call(t, w.owner, "get_session", map[string]any{"session_id": w.ids.session}), &got)
	if got.Session.Notes != "" {
		t.Fatalf("notes after undo = %q", got.Session.Notes)
	}

	cleared := structured(t, call(t, w.owner, "set_session_notes", map[string]any{"session_id": w.ids.session, "notes": "  "}))
	p, err := w.ownerService.GetProposal(ctx, proposalOf(t, cleared))
	if err != nil || p.ActionType != "session.notes.remove" {
		t.Fatalf("clearing proposal = %+v, %v; want session.notes.remove", p, err)
	}
}

type notedSession struct {
	Session struct {
		Notes string `json:"notes"`
	} `json:"session"`
}

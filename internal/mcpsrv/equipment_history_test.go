package mcpsrv

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/sqlitestore/sqlitestoretest"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// equipmentWorld is one store shared by several people, each reached through
// their own MCP connection, so per-equipment answers can be checked for
// isolation as well as for content.
type equipmentWorld struct {
	store *sqlitestore.Store
	clock func() time.Time
}

func newEquipmentWorld(t *testing.T) equipmentWorld {
	t.Helper()
	store, err := sqlitestore.Open(sqlitestoretest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return equipmentWorld{store: store, clock: func() time.Time { return time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC) }}
}

func (w equipmentWorld) service(userID int64) *training.Service {
	return training.NewService(w.store.ForUser(userID), w.clock)
}

func (w equipmentWorld) connect(t *testing.T, userID int64) *mcp.ClientSession {
	t.Helper()
	return mustConnect(t, New(only(w.service(userID))).Handler())
}

// seedTwoMachines records chest flys on two machines that share the exercise
// name, with the equipment typed in different case and spacing, plus sets with
// no equipment stated, a warm-up and a drop set. "aperturas" is prescribed at
// 8-12 reps @8 by a plan.
//
// Estimated 1RMs: technogym 90x10=120, 95x8=120.3, 95x12=133; matrix 70x10=93.3;
// not stated 60x12=84, 62x12=86.8. The matrix drop set (120x5=140) and warm-up
// never count.
func seedTwoMachines(t *testing.T, svc *training.Service) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.CreatePlan(ctx, training.Plan{Name: "Pecho", Items: []training.PlanItem{{Exercise: "aperturas", TargetSets: 3, RepMin: 8, RepMax: 12, TargetRPE: 8}}}); err != nil {
		t.Fatal(err)
	}
	days := []struct {
		date string
		sets []training.AddSetInput
	}{
		{"2026-11-10", []training.AddSetInput{
			{Exercise: "aperturas", WeightKG: 90, Reps: 10, RPE: 8, Equipment: "Technogym Pec Fly"},
			{Exercise: "aperturas", WeightKG: 95, Reps: 8, RPE: 9, Equipment: "technogym pec fly"},
			{Exercise: "aperturas", WeightKG: 60, Reps: 12, RPE: 7},
		}},
		{"2026-11-17", []training.AddSetInput{
			{Exercise: "aperturas", WeightKG: 95, Reps: 12, RPE: 7, Equipment: "  TECHNOGYM   pec fly "},
			{Exercise: "aperturas", WeightKG: 30, Reps: 10, RPE: 5, Category: training.SetWarmup, Equipment: "Matrix Pec Fly"},
			{Exercise: "aperturas", WeightKG: 70, Reps: 10, RPE: 8, Equipment: "Matrix Pec Fly"},
			{Exercise: "aperturas", WeightKG: 120, Reps: 5, RPE: 10, Technique: "drop set", Equipment: "Matrix Pec Fly"},
		}},
		{"2026-11-20", []training.AddSetInput{
			{Exercise: "aperturas", WeightKG: 62, Reps: 12, RPE: 6},
		}},
	}
	for _, d := range days {
		session, err := svc.StartSession(ctx, d.date)
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range d.sets {
			in.SessionID = session.ID
			if _, _, err := svc.AddSet(ctx, in); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func parseJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Golden outputs captured from main before per-equipment answers existed
// (55d6fe91), on the seedTwoMachines fixture. Without equipment, every field
// that existed then must keep its exact value; only the new fields are added.
const (
	goldenHistoryBefore = `{"history":{"best":{"category":"working","date":"2026-11-17","equipment":"technogym pec fly","est_1rm":133,"reps":12,"rpe":7,"session_id":2,"set_id":4,"si":0.8,"weight_kg":95},"exercise":"aperturas","sets":[{"category":"working","date":"2026-11-20","est_1rm":86.8,"reps":12,"rpe":6,"session_id":3,"set_id":8,"si":0.6,"weight_kg":62},{"category":"working","date":"2026-11-17","equipment":"matrix pec fly","est_1rm":140,"reps":5,"rpe":10,"session_id":2,"set_id":7,"si":1.4,"technique":"drop set","weight_kg":120},{"category":"working","date":"2026-11-17","equipment":"matrix pec fly","est_1rm":93.3,"reps":10,"rpe":8,"session_id":2,"set_id":6,"si":1,"weight_kg":70},{"category":"warmup","date":"2026-11-17","equipment":"matrix pec fly","est_1rm":0,"reps":10,"rpe":5,"session_id":2,"set_id":5,"si":0,"weight_kg":30},{"category":"working","date":"2026-11-17","equipment":"technogym pec fly","est_1rm":133,"reps":12,"rpe":7,"session_id":2,"set_id":4,"si":0.8,"weight_kg":95},{"category":"working","date":"2026-11-10","est_1rm":84,"reps":12,"rpe":7,"session_id":1,"set_id":3,"si":0.8,"weight_kg":60},{"category":"working","date":"2026-11-10","equipment":"technogym pec fly","est_1rm":120.3,"reps":8,"rpe":9,"session_id":1,"set_id":2,"si":1.2,"weight_kg":95},{"category":"working","date":"2026-11-10","equipment":"technogym pec fly","est_1rm":120,"reps":10,"rpe":8,"session_id":1,"set_id":1,"si":1,"weight_kg":90}]}}`
	goldenSuggestBefore = `{"suggestion":{"delta_kg":2.5,"exercise":"aperturas","last_reps":12,"last_rpe":6,"last_weight_kg":62,"reason":"Llegaste al tope del rango sobrado de RPE: sube el peso.","rep_max":12,"rep_min":8,"suggested_kg":64.5,"target_rpe":8}}`
)

// Omitting equipment changes no field that existed before: the new fields are
// the only difference from the golden output of main.
func TestOmittedEquipmentKeepsEveryExistingOutput(t *testing.T) {
	w := newEquipmentWorld(t)
	seedTwoMachines(t, w.service(1))
	session := w.connect(t, 1)

	history := structured(t, call(t, session, "exercise_history", map[string]any{"exercise": "aperturas"}))
	delete(history["history"].(map[string]any), "best_by_equipment")
	if got, want := mustJSON(t, history), mustJSON(t, parseJSON(t, goldenHistoryBefore)); got != want {
		t.Fatalf("exercise_history changed without equipment:\n got %s\nwant %s", got, want)
	}

	suggestion := structured(t, call(t, session, "suggest_load", map[string]any{"exercise": "aperturas"}))
	delete(suggestion["suggestion"].(map[string]any), "equipment_seen")
	if got, want := mustJSON(t, suggestion), mustJSON(t, parseJSON(t, goldenSuggestBefore)); got != want {
		t.Fatalf("suggest_load changed without equipment:\n got %s\nwant %s", got, want)
	}
}

// Two machines sharing an exercise name each keep their own best; the sets
// with no equipment stated are a group of their own; the overall best is the
// one it always was.
func TestExerciseHistoryReportsTheBestSetPerEquipment(t *testing.T) {
	w := newEquipmentWorld(t)
	seedTwoMachines(t, w.service(1))
	session := w.connect(t, 1)

	var out HistoryOut
	decode(t, call(t, session, "exercise_history", map[string]any{"exercise": "aperturas", "limit": 1}), &out)
	h := out.History
	if h.Best == nil || h.Best.Equipment != "technogym pec fly" || h.Best.Est1RM != 133 {
		t.Fatalf("overall best = %#v", h.Best)
	}
	type row struct {
		equipment string
		weight    float64
		reps      int
		est       float64
	}
	want := []row{
		{"", 62, 12, 86.8},
		{"matrix pec fly", 70, 10, 93.3},
		{"technogym pec fly", 95, 12, 133},
	}
	if len(h.BestByEquipment) != len(want) {
		t.Fatalf("best_by_equipment = %#v, want %d groups", h.BestByEquipment, len(want))
	}
	for i, wr := range want {
		g := h.BestByEquipment[i]
		if g.Equipment != wr.equipment || g.Best.Equipment != wr.equipment || g.Best.WeightKG != wr.weight || g.Best.Reps != wr.reps || g.Best.Est1RM != wr.est {
			t.Fatalf("group %d = %#v, want %+v", i, g, wr)
		}
	}
	// The "not stated" group is named explicitly, not dropped by omitempty.
	raw := structured(t, call(t, session, "exercise_history", map[string]any{"exercise": "aperturas"}))
	first := raw["history"].(map[string]any)["best_by_equipment"].([]any)[0].(map[string]any)
	if v, ok := first["equipment"]; !ok || v != "" {
		t.Fatalf("not-stated group = %#v, want an explicit empty equipment", first)
	}
}

func TestExerciseHistoryWithoutComparableSetsHasNoEquipmentBests(t *testing.T) {
	w := newEquipmentWorld(t)
	session := w.connect(t, 1)
	raw := structured(t, call(t, session, "exercise_history", map[string]any{"exercise": "nunca hecho"}))
	if _, ok := raw["history"].(map[string]any)["best_by_equipment"]; ok {
		t.Fatalf("empty history carries best_by_equipment: %#v", raw)
	}
}

func TestSuggestLoadOnOneEquipment(t *testing.T) {
	w := newEquipmentWorld(t)
	seedTwoMachines(t, w.service(1))
	session := w.connect(t, 1)

	tests := []struct {
		name      string
		equipment string
		want      training.LoadSuggestion
	}{
		{
			// Case and spacing are normalized as on write; the latest set on this
			// machine is 95x12 @7, top of the range with RPE to spare.
			name: "named machine", equipment: "  Technogym  PEC fly ",
			want: training.LoadSuggestion{Exercise: "aperturas", Equipment: "technogym pec fly", LastWeightKG: 95, SuggestedKG: 97.5, DeltaKG: 2.5, Reason: "Llegaste al tope del rango sobrado de RPE: sube el peso.", RepMin: 8, RepMax: 12, TargetRPE: 8, LastReps: 12, LastRPE: 7},
		},
		{
			// The warm-up and the drop set on this machine are not comparable.
			name: "other machine", equipment: "matrix pec fly",
			want: training.LoadSuggestion{Exercise: "aperturas", Equipment: "matrix pec fly", LastWeightKG: 70, SuggestedKG: 70, Reason: "Peso correcto: mantenlo y busca una repetición más.", RepMin: 8, RepMax: 12, TargetRPE: 8, LastReps: 10, LastRPE: 8},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out LoadSuggestionOut
			decode(t, call(t, session, "suggest_load", map[string]any{"exercise": "aperturas", "equipment": tt.equipment}), &out)
			if got, want := mustJSON(t, out.Suggestion), mustJSON(t, tt.want); got != want {
				t.Fatalf("suggestion:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// An equipment with no sets says so and lists what there is; it never falls
// back to sets on other equipment.
func TestSuggestLoadOnUnknownEquipmentSaysSo(t *testing.T) {
	w := newEquipmentWorld(t)
	seedTwoMachines(t, w.service(1))
	session := w.connect(t, 1)

	var out LoadSuggestionOut
	decode(t, call(t, session, "suggest_load", map[string]any{"exercise": "aperturas", "equipment": "Hammer Strength"}), &out)
	s := out.Suggestion
	if !s.NoSetsOnEquipment || s.Equipment != "hammer strength" || s.LastWeightKG != 0 || s.SuggestedKG != 0 || s.DeltaKG != 0 {
		t.Fatalf("suggestion = %#v", s)
	}
	if s.Reason != `Sin series previas de este ejercicio en el equipo "hammer strength"; no uso las de otros equipos. Elige un peso que te deje en el rango objetivo.` {
		t.Fatalf("reason = %q", s.Reason)
	}
	if got := mustJSON(t, s.EquipmentSeen); got != `["","matrix pec fly","technogym pec fly"]` {
		t.Fatalf("equipment_seen = %s", got)
	}
}

func TestSuggestLoadListsEquipmentSeenOnlyWhenThereIsAChoice(t *testing.T) {
	w := newEquipmentWorld(t)
	seedTwoMachines(t, w.service(1))
	session := w.connect(t, 1)

	var several LoadSuggestionOut
	decode(t, call(t, session, "suggest_load", map[string]any{"exercise": "aperturas"}), &several)
	if got := mustJSON(t, several.Suggestion.EquipmentSeen); got != `["","matrix pec fly","technogym pec fly"]` {
		t.Fatalf("equipment_seen = %s", got)
	}
	if several.Suggestion.Equipment != "" || several.Suggestion.NoSetsOnEquipment {
		t.Fatalf("suggestion without equipment = %#v", several.Suggestion)
	}

	// Only one equipment value (here: none stated): no list to choose from.
	svc := w.service(2)
	session2 := w.connect(t, 2)
	s, err := svc.StartSession(context.Background(), "2026-11-20")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AddSet(context.Background(), training.AddSetInput{SessionID: s.ID, Exercise: "aperturas", WeightKG: 50, Reps: 10, RPE: 8}); err != nil {
		t.Fatal(err)
	}
	raw := structured(t, call(t, session2, "suggest_load", map[string]any{"exercise": "aperturas"}))
	if _, ok := raw["suggestion"].(map[string]any)["equipment_seen"]; ok {
		t.Fatalf("single equipment lists equipment_seen: %#v", raw)
	}
}

// Sets with no equipment are a value of their own: asking for a machine never
// uses them, and they never leak into a machine's best.
func TestSetsWithoutEquipmentCompareOnlyWithEachOther(t *testing.T) {
	w := newEquipmentWorld(t)
	svc := w.service(1)
	ctx := context.Background()
	s, err := svc.StartSession(ctx, "2026-11-20")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AddSet(ctx, training.AddSetInput{SessionID: s.ID, Exercise: "remo", WeightKG: 80, Reps: 10, RPE: 8}); err != nil {
		t.Fatal(err)
	}
	session := w.connect(t, 1)

	var out LoadSuggestionOut
	decode(t, call(t, session, "suggest_load", map[string]any{"exercise": "remo", "equipment": "hammer row"}), &out)
	if !out.Suggestion.NoSetsOnEquipment || out.Suggestion.LastWeightKG != 0 {
		t.Fatalf("machine borrowed unstated sets: %#v", out.Suggestion)
	}
	if got := mustJSON(t, out.Suggestion.EquipmentSeen); got != `[""]` {
		t.Fatalf("equipment_seen = %s", got)
	}
	var h HistoryOut
	decode(t, call(t, session, "exercise_history", map[string]any{"exercise": "remo"}), &h)
	if len(h.History.BestByEquipment) != 1 || h.History.BestByEquipment[0].Equipment != "" || h.History.BestByEquipment[0].Best.WeightKG != 80 {
		t.Fatalf("best_by_equipment = %#v", h.History.BestByEquipment)
	}
}

// Another person's machines are neither seen nor ranked, and a person who
// works in lb gets the same kilogram answers: weights are stored in kg and the
// unit only changes how the app shows them.
func TestEquipmentAnswersAreIsolatedAndUnitIndependent(t *testing.T) {
	w := newEquipmentWorld(t)
	seedTwoMachines(t, w.service(1))
	other := w.service(2)
	seedTwoMachines(t, other)
	if err := other.SetWeightUnit(context.Background(), training.UnitLB); err != nil {
		t.Fatal(err)
	}
	s, err := other.StartSession(context.Background(), "2026-11-21")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := other.AddSet(context.Background(), training.AddSetInput{SessionID: s.ID, Exercise: "aperturas", WeightKG: 200, Reps: 5, RPE: 9, Equipment: "Hammer Strength"}); err != nil {
		t.Fatal(err)
	}

	one, two := w.connect(t, 1), w.connect(t, 2)
	var h1, h2 HistoryOut
	decode(t, call(t, one, "exercise_history", map[string]any{"exercise": "aperturas"}), &h1)
	decode(t, call(t, two, "exercise_history", map[string]any{"exercise": "aperturas"}), &h2)
	if len(h1.History.BestByEquipment) != 3 {
		t.Fatalf("user 1 sees another person's machine: %#v", h1.History.BestByEquipment)
	}
	if len(h2.History.BestByEquipment) != 4 || h2.History.BestByEquipment[1].Equipment != "hammer strength" {
		t.Fatalf("user 2 groups = %#v", h2.History.BestByEquipment)
	}

	var foreign LoadSuggestionOut
	decode(t, call(t, one, "suggest_load", map[string]any{"exercise": "aperturas", "equipment": "hammer strength"}), &foreign)
	if !foreign.Suggestion.NoSetsOnEquipment {
		t.Fatalf("user 1 got a suggestion from user 2's machine: %#v", foreign.Suggestion)
	}
	var s1, s2 LoadSuggestionOut
	decode(t, call(t, one, "suggest_load", map[string]any{"exercise": "aperturas", "equipment": "technogym pec fly"}), &s1)
	decode(t, call(t, two, "suggest_load", map[string]any{"exercise": "aperturas", "equipment": "technogym pec fly"}), &s2)
	if s1.Suggestion.SuggestedKG != 97.5 || mustJSON(t, s1.Suggestion) != mustJSON(t, s2.Suggestion) {
		t.Fatalf("kg and lb users differ on identical sets:\n kg %#v\n lb %#v", s1.Suggestion, s2.Suggestion)
	}
}

func TestSuggestLoadRejectsOverlongEquipment(t *testing.T) {
	w := newEquipmentWorld(t)
	session := w.connect(t, 1)
	long := make([]byte, 81)
	for i := range long {
		long[i] = 'a'
	}
	result := call(t, session, "suggest_load", map[string]any{"exercise": "aperturas", "equipment": string(long)})
	if !result.IsError {
		t.Fatalf("81-character equipment accepted: %#v", result.StructuredContent)
	}
}

package mcpsrv

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rawField is one top-level field of a tool's structured result, as JSON.
func rawField(t *testing.T, result *mcp.CallToolResult, field string) string {
	t.Helper()
	var fields map[string]json.RawMessage
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	return string(fields[field])
}

func keysOf(t *testing.T, result *mcp.CallToolResult) []string {
	t.Helper()
	var fields map[string]json.RawMessage
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	var out []string
	for k := range fields {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The library through its tools, from a set logged under a name it does not
// know to an alias and a correction, with the volume reads beside it.
func TestExerciseLibraryToolsFromAMissToACorrection(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	owner := w.ownerService

	var curated ExerciseLookupOut
	decode(t, call(t, w.owner, "lookup_exercise", map[string]any{"name": "Bench Press - Powerlifting"}), &curated)
	if !curated.Found || curated.Layer != training.LayerCurated || curated.Match != training.MatchExact || curated.Entry.Muscles[0].MuscleGroup != "pecho" {
		t.Fatalf("curated lookup = %+v", curated)
	}
	var public ExerciseLookupOut
	decode(t, call(t, w.owner, "lookup_exercise", map[string]any{"name": "barbell bench press medium grip"}), &public)
	if public.Layer != training.LayerPublic || public.Match != training.MatchNormalized || !public.Entry.Uncertain {
		t.Fatalf("public lookup = %+v", public)
	}

	// The world's owner logged banca, grouped as pecho, with no library entry.
	var needs MappingNeedsOut
	decode(t, call(t, w.owner, "list_exercises_needing_mapping", map[string]any{}), &needs)
	if len(needs.Exercises) != 1 || needs.Exercises[0].Exercise != isolationExercise || needs.Exercises[0].Reason != training.NeedNotFound || needs.Exercises[0].MuscleGroup != "pecho" {
		t.Fatalf("needs mapping = %+v", needs)
	}

	// Existing reads keep every existing field and number; fractional is beside them.
	weeklyBefore, err := owner.WeeklyVolume(ctx, training.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	volumeBefore, err := owner.VolumeByGroup(ctx, training.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	weekly := call(t, w.owner, "weekly_volume", map[string]any{})
	volume := call(t, w.owner, "volume_by_muscle", map[string]any{})
	var weeks []training.WeeklyVolume
	var groups []training.GroupVolume
	if err := json.Unmarshal([]byte(rawField(t, weekly, "weeks")), &weeks); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(rawField(t, volume, "volume")), &groups); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(weeks, weeklyBefore) || !reflect.DeepEqual(groups, volumeBefore) || len(groups) == 0 {
		t.Fatalf("existing fields changed:\n%+v\n%+v", weeks, groups)
	}
	if got := keysOf(t, weekly); !reflect.DeepEqual(got, []string{"fractional", "weeks"}) {
		t.Fatalf("weekly_volume fields = %v", got)
	}
	if got := keysOf(t, volume); !reflect.DeepEqual(got, []string{"fractional", "volume"}) {
		t.Fatalf("volume_by_muscle fields = %v", got)
	}
	var total VolumeOut
	decode(t, volume, &total)
	// No entry: the logged group gets the whole set, as in the existing totals.
	if total.Fractional == nil || total.Fractional.Method != training.FractionalMethod || !strings.Contains(total.Fractional.Source, "10.1007/s40279-025-02344-w") ||
		len(total.Fractional.Groups) != 1 || total.Fractional.Groups[0].MuscleGroup != "pecho" || total.Fractional.Groups[0].Sets != float64(volumeBefore[0].Sets) {
		t.Fatalf("fractional before mapping = %+v", total.Fractional)
	}

	// An alias moves banca onto the curated entry's muscles.
	var aliased ExerciseAliasOut
	decode(t, call(t, w.owner, "set_exercise_alias", map[string]any{"alias": "Banca", "target": "bench press - POWERLIFTING"}), &aliased)
	if aliased.Status != "accepted" || aliased.Alias != "banca" || aliased.Entry.Layer != training.LayerCurated || aliased.Match != training.MatchExact {
		t.Fatalf("alias = %+v", aliased)
	}
	decode(t, call(t, w.owner, "volume_by_muscle", map[string]any{}), &total)
	sets := float64(volumeBefore[0].Sets)
	want := []training.FractionalGroup{{MuscleGroup: "pecho", Sets: sets, TotalSI: volumeBefore[0].TotalSI},
		{MuscleGroup: "hombro anterior", Sets: sets / 2, TotalSI: total.Fractional.Groups[1].TotalSI},
		{MuscleGroup: "triceps", Sets: sets / 2, TotalSI: total.Fractional.Groups[2].TotalSI}}
	if !reflect.DeepEqual(total.Fractional.Groups, want) || mustJSON(t, total.Volume) != mustJSON(t, volumeBefore) {
		t.Fatalf("after the alias = %+v, volume %+v", total.Fractional.Groups, total.Volume)
	}

	// Correcting the curated entry copies it to the owner's own layer.
	var saved ExerciseEntryOut
	decode(t, call(t, w.owner, "save_exercise_entry", map[string]any{"name": "Bench Press - Powerlifting", "primary": []string{"pecho"}, "secondary": []string{"triceps"}}), &saved)
	if saved.Status != "accepted" || saved.Entry.Layer != training.LayerOwn || saved.Entry.BasedOn != "curated:bench press - powerlifting" || len(saved.Entry.Muscles) != 2 {
		t.Fatalf("saved = %+v", saved)
	}
	var viaAlias ExerciseLookupOut
	decode(t, call(t, w.owner, "lookup_exercise", map[string]any{"name": "BANCA"}), &viaAlias)
	if viaAlias.Layer != training.LayerOwn || viaAlias.Match != training.MatchAlias || viaAlias.Alias != "banca" {
		t.Fatalf("lookup through the alias = %+v", viaAlias)
	}
	decode(t, call(t, w.owner, "list_exercises_needing_mapping", map[string]any{}), &needs)
	if len(needs.Exercises) != 0 {
		t.Fatalf("still needing mapping = %+v", needs)
	}
	// The second person sees none of it.
	var theirs ExerciseLookupOut
	decode(t, call(t, w.second, "lookup_exercise", map[string]any{"name": "banca"}), &theirs)
	if theirs.Found {
		t.Fatalf("second person = %+v", theirs)
	}
}

func TestExerciseLibraryWritesRefuseWhatTheyCannotStore(t *testing.T) {
	w := newIsolationWorld(t)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"save_exercise_entry", map[string]any{"name": "x", "primary": []string{}}},
		{"save_exercise_entry", map[string]any{"name": "x", "primary": []string{"chest"}}},
		{"save_exercise_entry", map[string]any{"name": "x", "primary": []string{"pecho"}, "secondary": []string{"pecho"}}},
		{"set_exercise_alias", map[string]any{"alias": "x", "target": "nothing like it"}},
		{"set_exercise_alias", map[string]any{"alias": "press militar", "target": "press militar"}},
		{"lookup_exercise", map[string]any{"name": " "}},
	} {
		before := dumpTables(t, w.db, nil)
		if result := call(t, w.owner, tc.tool, tc.args); !result.IsError || strings.Contains(stringContent(result), "internal") {
			t.Errorf("%s %v = %s", tc.tool, tc.args, answer(t, result))
		}
		if after := dumpTables(t, w.db, nil); after != before {
			t.Errorf("%s %v changed rows", tc.tool, tc.args)
		}
	}
}

func TestLookupWithoutALibraryStoreStillReadsTheSharedLayers(t *testing.T) {
	session := mustConnect(t, New(only(training.NewService(testStore{}, nil))).Handler())
	var got ExerciseLookupOut
	decode(t, call(t, session, "lookup_exercise", map[string]any{"name": "press de banca"}), &got)
	if got.Found || len(got.Candidates) == 0 {
		t.Fatalf("lookup = %+v", got)
	}
	weekly := call(t, session, "weekly_volume", map[string]any{})
	if got := keysOf(t, weekly); !reflect.DeepEqual(got, []string{"weeks"}) {
		t.Fatalf("a store without a library adds no fractional view: %v", got)
	}
}

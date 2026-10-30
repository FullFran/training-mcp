package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func logSetsSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	store, err := sqlitestore.Open(t.TempDir() + "/training.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return mustConnect(t, New(training.NewService(store, time.Now)).Handler())
}

func repsArgs(key string, reps ...int) map[string]any {
	sets := make([]map[string]any, 0, len(reps))
	for _, r := range reps {
		sets = append(sets, map[string]any{"weight_kg": 6.8, "reps": r, "rpe": 8})
	}
	return map[string]any{"idempotency_key": key, "exercise": " Laterales Polea ", "sets": sets}
}

func listedSets(t *testing.T, session *mcp.ClientSession) int {
	t.Helper()
	var listed ListOut
	decode(t, call(t, session, "list_sessions", map[string]any{}), &listed)
	total := 0
	for _, s := range listed.Sessions {
		total += s.SetCount
	}
	return total
}

func TestLogSetsRecordsDifferentSetsInOneCall(t *testing.T) {
	session := logSetsSession(t)
	var out LogSetsOut
	decode(t, call(t, session, "log_sets", repsArgs("key-1", 12, 12, 10, 8)), &out)
	if out.Replayed {
		t.Fatal("first call reported as a replay")
	}
	if len(out.Sets) != 4 {
		t.Fatalf("sets = %d", len(out.Sets))
	}
	for i, want := range []int{12, 12, 10, 8} {
		got := out.Sets[i]
		if got.Reps != want || got.Position != i+1 || got.Exercise != "laterales polea" || got.SI != 1 {
			t.Fatalf("set %d = %#v", i, got)
		}
	}
	if out.TotalSI != 4 {
		t.Fatalf("total SI = %v", out.TotalSI)
	}
	var stored SessionOut
	decode(t, call(t, session, "get_session", map[string]any{"session_id": out.Session.ID}), &stored)
	if len(stored.Session.Sets) != 4 || stored.Session.TotalSI != 4 {
		t.Fatalf("stored session = %#v", stored.Session)
	}
	today := time.Now().Format("2006-01-02")
	if stored.Session.Date != today {
		t.Fatalf("session date = %s, want %s", stored.Session.Date, today)
	}
}

func TestLogSetsRetryWithTheSameKeyStoresNothingNew(t *testing.T) {
	session := logSetsSession(t)
	var first, retry LogSetsOut
	decode(t, call(t, session, "log_sets", repsArgs("key-1", 12, 10)), &first)
	decode(t, call(t, session, "log_sets", repsArgs("key-1", 12, 10)), &retry)
	if !retry.Replayed {
		t.Fatal("retry was not reported as a replay")
	}
	if retry.Sets[0].ID != first.Sets[0].ID || retry.Sets[1].ID != first.Sets[1].ID || retry.TotalSI != first.TotalSI {
		t.Fatalf("retry = %#v, first = %#v", retry, first)
	}
	if got := listedSets(t, session); got != 2 {
		t.Fatalf("stored sets = %d, want 2", got)
	}
}

func TestLogSetsAddsToTodaysSessionUnderANewKey(t *testing.T) {
	session := logSetsSession(t)
	var first, second LogSetsOut
	decode(t, call(t, session, "log_sets", repsArgs("key-1", 12)), &first)
	decode(t, call(t, session, "log_sets", repsArgs("key-2", 12)), &second)
	if second.Replayed || second.Session.ID != first.Session.ID || second.Sets[0].Position != 2 {
		t.Fatalf("second = %#v", second)
	}
	if second.TotalSI != 2 {
		t.Fatalf("total SI = %v", second.TotalSI)
	}
}

func TestLogSetsRefusesAKeyReusedForDifferentSets(t *testing.T) {
	session := logSetsSession(t)
	call(t, session, "log_sets", repsArgs("key-1", 12))
	result := call(t, session, "log_sets", repsArgs("key-1", 8))
	if !result.IsError || !strings.Contains(stringContent(result), "idempotency_key") {
		t.Fatalf("result = %#v", result)
	}
	if got := listedSets(t, session); got != 1 {
		t.Fatalf("stored sets = %d, want 1", got)
	}
}

func TestLogSetsStoresNothingWhenAnySetIsInvalid(t *testing.T) {
	session := logSetsSession(t)
	args := repsArgs("key-1", 12, 10)
	args["sets"].([]map[string]any)[1]["rpe"] = 99
	if result := call(t, session, "log_sets", args); !result.IsError {
		t.Fatal("accepted a set with RPE 99")
	}
	var listed ListOut
	decode(t, call(t, session, "list_sessions", map[string]any{}), &listed)
	if len(listed.Sessions) != 0 {
		t.Fatalf("a rejected call left sessions behind: %#v", listed.Sessions)
	}
}

func TestLogSetsRequiresAKeyAndAtLeastOneSet(t *testing.T) {
	session := logSetsSession(t)
	noKey := repsArgs("", 12)
	delete(noKey, "idempotency_key")
	blankKey := repsArgs("   ", 12)
	noSets := map[string]any{"idempotency_key": "k", "exercise": "remo", "sets": []map[string]any{}}
	for name, args := range map[string]map[string]any{"missing key": noKey, "blank key": blankKey, "no sets": noSets} {
		if result := call(t, session, "log_sets", args); !result.IsError {
			t.Errorf("%s was accepted", name)
		}
	}
	if got := listedSets(t, session); got != 0 {
		t.Fatalf("stored sets = %d", got)
	}
}

func unknownRPEArgs(key string) map[string]any {
	return map[string]any{"idempotency_key": key, "exercise": "remo", "sets": []map[string]any{
		{"weight_kg": 70, "reps": 10},
		{"weight_kg": 70, "reps": 8, "rpe": 9},
	}}
}

func TestLogSetsMarksAnUnstatedRPEAsImputedAndCountsOneUnit(t *testing.T) {
	session := logSetsSession(t)
	var out LogSetsOut
	decode(t, call(t, session, "log_sets", unknownRPEArgs("k")), &out)
	unknown, measured := out.Sets[0], out.Sets[1]
	if !unknown.RPEImputed || unknown.SI != 1 {
		t.Fatalf("unknown set = %#v", unknown)
	}
	if measured.RPEImputed || measured.RPE != 9 || measured.SI != 1.2 {
		t.Fatalf("measured set = %#v", measured)
	}
	if out.TotalSI != 2.2 {
		t.Fatalf("total SI = %v", out.TotalSI)
	}
	var stored SessionOut
	decode(t, call(t, session, "get_session", map[string]any{"session_id": out.Session.ID}), &stored)
	if !stored.Session.Sets[0].RPEImputed || stored.Session.Sets[1].RPEImputed {
		t.Fatalf("stored = %#v", stored.Session.Sets)
	}
	var history HistoryOut
	decode(t, call(t, session, "exercise_history", map[string]any{"exercise": "remo"}), &history)
	flags := map[int]bool{}
	for _, set := range history.History.Sets {
		flags[set.Reps] = set.RPEImputed
	}
	if !flags[10] || flags[8] {
		t.Fatalf("history flags = %#v", flags)
	}
}

func TestStatingTheRPELaterClearsTheMarkAndOtherEditsKeepIt(t *testing.T) {
	session := logSetsSession(t)
	var out LogSetsOut
	decode(t, call(t, session, "log_sets", unknownRPEArgs("k")), &out)
	id := out.Sets[0].ID
	var edited SetOut
	decode(t, call(t, session, "update_set", map[string]any{"set_id": id, "reps": 11}), &edited)
	if !edited.Set.RPEImputed || edited.Set.SI != 1 {
		t.Fatalf("editing reps changed the mark: %#v", edited.Set)
	}
	var stated SetOut
	decode(t, call(t, session, "update_set", map[string]any{"set_id": id, "rpe": 8}), &stated)
	if stated.Set.RPEImputed {
		t.Fatalf("stating the RPE kept the mark: %#v", stated.Set)
	}
}

func TestMeasuredSetsDoNotGainTheImputedField(t *testing.T) {
	session := logSetsSession(t)
	result := call(t, session, "log_sets", repsArgs("k", 12))
	if strings.Contains(stringContent(result), "rpe_imputed") {
		t.Fatalf("output = %s", stringContent(result))
	}
}

func TestLogSetsStillRejectsAnOutOfRangeRPE(t *testing.T) {
	session := logSetsSession(t)
	args := unknownRPEArgs("k")
	args["sets"].([]map[string]any)[0]["rpe"] = 0
	if result := call(t, session, "log_sets", args); !result.IsError {
		t.Fatal("accepted an explicit RPE of 0")
	}
}

func TestSuggestLoadDoesNotJudgeEffortFromAnImputedRPE(t *testing.T) {
	session := logSetsSession(t)
	var plan PlanOut
	decode(t, call(t, session, "create_plan", map[string]any{"name": "A", "items": []map[string]any{
		{"exercise": "remo", "target_sets": 3, "rep_min": 8, "rep_max": 10, "target_rpe": 9.5},
	}}), &plan)
	call(t, session, "log_sets", map[string]any{"idempotency_key": "k", "exercise": "remo", "sets": []map[string]any{{"weight_kg": 70, "reps": 10}}})
	var out LoadSuggestionOut
	decode(t, call(t, session, "suggest_load", map[string]any{"exercise": "remo"}), &out)
	s := out.Suggestion
	if s.DeltaKG != 0 || s.LastRPE != 0 || !strings.Contains(s.Reason, "RPE") {
		t.Fatalf("suggestion = %#v", s)
	}
}

package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// richPlan is a routine with a target RPE on some exercises and none on
// others, the shapes a modifier treats differently.
func (w *periodWorld) richPlan(name string) int64 {
	w.t.Helper()
	p, err := w.svc.CreatePlan(context.Background(), training.Plan{Name: name, Items: []training.PlanItem{
		{Exercise: "press banca", TargetSets: 3, RepMin: 6, RepMax: 8, TargetRPE: 8},
		{Exercise: "remo", TargetSets: 2},
		{Exercise: "curl", TargetSets: 1, TargetRPE: 9.5},
	}})
	if err != nil {
		w.t.Fatal(err)
	}
	w.plans[name] = p.ID
	return p.ID
}

func (w *periodWorld) progress(session int64) []training.PlanProgress {
	w.t.Helper()
	p, err := w.svc.SessionProgress(context.Background(), session)
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

// targets reads a session's copy as exercise:sets:rpe.
func targets(p []training.PlanProgress) []string {
	out := []string{}
	for _, it := range p {
		if it.TargetSets > 0 {
			out = append(out, it.Exercise+":"+itoa(it.TargetSets)+":"+ftoa(it.TargetRPE))
		}
	}
	return out
}

func itoa(v int) string     { b, _ := json.Marshal(v); return string(b) }
func ftoa(v float64) string { b, _ := json.Marshal(v); return string(b) }

// Starting a routine inside an active block applies the microcycle's
// modifiers to the session's copy, never below one set or outside RPE 1-10,
// only to exercises that have a target RPE, and says where they came from.
// The routine is not changed.
func TestStartInABlockAppliesTheMicrocycleModifiersToTheSessionCopy(t *testing.T) {
	ctx := context.Background()
	w := newPeriodWorld(t)
	plan := w.richPlan("empuje")
	routine, err := w.svc.GetPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	b := w.block(training.BlockInput{Intent: strPtr("acumular"), MicrocycleCount: intPtr(2), Rotation: w.rotation("empuje", "empuje"),
		Microcycles: []training.MicrocycleInput{
			{Position: 1, Label: strPtr("descarga"), SetsDelta: intPtr(-2), EffortDelta: f64Ptr(1)},
			{Position: 2, Label: strPtr("choque"), SetsDelta: intPtr(training.MaxSetsDelta), EffortDelta: f64Ptr(-training.MaxEffortDelta)},
		}})
	w.advance(b.ID, training.BlockActivate)

	session, applied, err := w.svc.StartPrescribedSession(ctx, "", plan)
	if err != nil {
		t.Fatal(err)
	}
	if applied == nil {
		t.Fatal("no prescription reported")
	}
	want := &training.Prescription{
		BlockID: b.ID, MicrocycleID: b.Microcycles[0].ID, MicrocyclePosition: 1, MicrocycleLabel: "descarga",
		PlanID: plan, PlanName: "empuje", SetsDelta: -2, EffortDelta: 1, Rule: training.PrescriptionRule,
		Items: []training.PrescribedItem{
			{Exercise: "press banca", BaseSets: 3, Sets: 1, BaseTargetRPE: 8, TargetRPE: 9},
			{Exercise: "remo", BaseSets: 2, Sets: 1, SetsClamped: true},
			{Exercise: "curl", BaseSets: 1, Sets: 1, SetsClamped: true, BaseTargetRPE: 9.5, TargetRPE: 10, RPEClamped: true},
		},
	}
	if !reflect.DeepEqual(applied, want) {
		t.Fatalf("prescription =\n%+v\nwant\n%+v", applied, want)
	}
	if got := targets(w.progress(session.ID)); !reflect.DeepEqual(got, []string{"press banca:1:9", "remo:1:0", "curl:1:10"}) {
		t.Fatalf("session copy = %v", got)
	}
	// The record of what was applied is kept with the session.
	stored, err := w.svc.SessionPrescription(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(stored, want) {
		t.Fatalf("stored prescription = %+v, %v", stored, err)
	}
	// The routine is unchanged.
	if after, err := w.svc.GetPlan(ctx, plan); err != nil || !reflect.DeepEqual(after, routine) {
		t.Fatalf("routine changed: %+v, %v", after, err)
	}

	// A past session is never rewritten when the microcycle it counted in is
	// edited, and the next session reads the edit.
	w.block(training.BlockInput{ID: b.ID, Microcycles: []training.MicrocycleInput{{Position: 1, SetsDelta: intPtr(0)}}})
	if got := targets(w.progress(session.ID)); !reflect.DeepEqual(got, []string{"press banca:1:9", "remo:1:0", "curl:1:10"}) {
		t.Fatalf("first copy after an edit = %v", got)
	}
	if stored, err := w.svc.SessionPrescription(ctx, session.ID); err != nil || !reflect.DeepEqual(stored, want) {
		t.Fatalf("stored prescription after an edit = %+v, %v", stored, err)
	}
	second, applied, err := w.svc.StartPrescribedSession(ctx, "2026-11-18", plan)
	if err != nil || applied == nil || applied.MicrocyclePosition != 1 || applied.SetsDelta != 0 {
		t.Fatalf("second start = %+v, %v", applied, err)
	}
	if got := targets(w.progress(second.ID)); !reflect.DeepEqual(got, []string{"press banca:3:9", "remo:2:0", "curl:1:10"}) {
		t.Fatalf("second copy = %v", got)
	}

	// The next microcycle clamps the other way: at most 20 sets, RPE at least 1.
	third, applied, err := w.svc.StartPrescribedSession(ctx, "2026-11-23", plan)
	if err != nil || applied == nil || applied.MicrocyclePosition != 2 {
		t.Fatalf("third start = %+v, %v", applied, err)
	}
	if got := targets(w.progress(third.ID)); !reflect.DeepEqual(got, []string{"press banca:20:3", "remo:20:0", "curl:20:4.5"}) {
		t.Fatalf("third copy = %v", got)
	}
	if it := applied.Items[0]; !it.SetsClamped || it.RPEClamped {
		t.Fatalf("third item = %+v", it)
	}
}

// With no active block, an off-plan start, a free-form start, or a microcycle
// without modifiers, starting a session behaves exactly as before slice 2:
// the copy equals the routine and nothing is recorded.
func TestStartWithoutModifiersBehavesExactlyAsBefore(t *testing.T) {
	ctx := context.Background()
	run := func(setup func(w *periodWorld)) (string, string) {
		w := newPeriodWorld(t)
		plan := w.richPlan("empuje")
		if setup != nil {
			setup(w)
		}
		session, applied, err := w.svc.StartPrescribedSession(ctx, "", plan)
		if err != nil {
			t.Fatal(err)
		}
		if applied != nil {
			t.Fatalf("prescription without modifiers: %+v", applied)
		}
		if p, err := w.svc.SessionPrescription(ctx, session.ID); err != nil || p != nil {
			t.Fatalf("stored prescription = %+v, %v", p, err)
		}
		out, err := json.Marshal(session)
		if err != nil {
			t.Fatal(err)
		}
		return periodTimestamps.ReplaceAllString(string(out), "T"),
			dumpQuery(t, w.store.db, `SELECT position,exercise,target_sets,rep_min,rep_max,target_rpe,skipped,superset,notes FROM session_items WHERE session_id=? ORDER BY position`, session.ID) +
				dumpQuery(t, w.store.db, `SELECT id,date,plan_id,plan_name,block_modifiers FROM sessions WHERE id=?`, session.ID)
	}
	refOut, refRows := run(nil)
	for name, setup := range map[string]func(w *periodWorld){
		"active block, no modifiers": func(w *periodWorld) {
			b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(2), Rotation: w.rotation("empuje"),
				Microcycles: []training.MicrocycleInput{{Position: 2, SetsDelta: intPtr(3)}}})
			w.advance(b.ID, training.BlockActivate)
		},
		"planned block with modifiers": func(w *periodWorld) {
			w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(1), Rotation: w.rotation("empuje"), Status: strPtr(training.BlockPlanned),
				Microcycles: []training.MicrocycleInput{{Position: 1, SetsDelta: intPtr(3), EffortDelta: f64Ptr(1)}}})
		},
		"off-plan in an active block": func(w *periodWorld) {
			b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(1), Rotation: w.rotation("empuje"),
				Microcycles: []training.MicrocycleInput{{Position: 1, SetsDelta: intPtr(3), EffortDelta: f64Ptr(1)}}})
			if _, err := w.svc.AdvanceBlock(ctx, training.AdvanceInput{BlockID: b.ID, Action: training.BlockActivate, StartDate: "2026-11-20"}); err != nil {
				t.Fatal(err)
			}
		},
	} {
		out, rows := run(setup)
		if out != refOut {
			t.Errorf("%s: result %s, want %s", name, out, refOut)
		}
		if rows != refRows {
			t.Errorf("%s: rows\n%s\nwant\n%s", name, rows, refRows)
		}
	}
	// A free-form session inside a microcycle with modifiers has no copy to
	// change, and records nothing.
	w := newPeriodWorld(t)
	b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(1), Rotation: w.rotation("torso"),
		Microcycles: []training.MicrocycleInput{{Position: 1, SetsDelta: intPtr(3)}}})
	w.advance(b.ID, training.BlockActivate)
	free, applied, err := w.svc.StartPrescribedSession(ctx, "", 0)
	if err != nil || applied != nil || !w.link(free.ID).Placed {
		t.Fatalf("free-form start = %+v, %+v, %v", free, applied, err)
	}
}

// failModifiers makes every rewrite of a session's targets fail.
func failModifiers(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TRIGGER fail_modifiers BEFORE UPDATE OF target_sets, target_rpe ON session_items
		BEGIN SELECT RAISE(ABORT, 'modifiers unavailable'); END`); err != nil {
		t.Fatal(err)
	}
}

// Applying modifiers is best effort: when it fails, the session starts, stays
// placed, keeps the routine's copy unchanged and records nothing, and logging
// into it works.
func TestAModifierFailureNeverFailsTheStart(t *testing.T) {
	ctx := context.Background()
	w := newPeriodWorld(t)
	plan := w.richPlan("empuje")
	b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(2), Rotation: w.rotation("empuje"),
		Microcycles: []training.MicrocycleInput{{Position: 1, SetsDelta: intPtr(1), EffortDelta: f64Ptr(-1)}}})
	w.advance(b.ID, training.BlockActivate)
	failModifiers(t, w.store.db)
	session, applied, err := w.svc.StartPrescribedSession(ctx, "", plan)
	if err != nil || session.ID == 0 {
		t.Fatalf("start = %+v, %v", session, err)
	}
	if applied != nil {
		t.Fatalf("a failed application was reported: %+v", applied)
	}
	if got := targets(w.progress(session.ID)); !reflect.DeepEqual(got, []string{"press banca:3:8", "remo:2:0", "curl:1:9.5"}) {
		t.Fatalf("copy = %v", got)
	}
	if l := w.link(session.ID); !l.Placed || l.MicrocycleID != b.Microcycles[0].ID {
		t.Fatalf("link = %+v", l)
	}
	if p, err := w.svc.SessionPrescription(ctx, session.ID); err != nil || p != nil {
		t.Fatalf("stored prescription = %+v, %v", p, err)
	}
	if _, sets, err := w.svc.LogSet(ctx, "press banca", 80, 6, 8, 2, "", "", ""); err != nil || len(sets) != 2 {
		t.Fatalf("log_set = %d, %v", len(sets), err)
	}
}

// training_status shows what is due next with the current microcycle's
// modifiers applied, base and result side by side.
func TestTrainingStatusShowsTheNextPrescriptionWithModifiers(t *testing.T) {
	ctx := context.Background()
	w := newPeriodWorld(t)
	plan := w.richPlan("empuje")
	b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(2), Rotation: w.rotation("torso", "empuje"),
		Microcycles: []training.MicrocycleInput{{Position: 1, Label: strPtr("carga"), SetsDelta: intPtr(1), EffortDelta: f64Ptr(0.5)}}})
	w.advance(b.ID, training.BlockActivate)
	st, err := w.svc.TrainingStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Next == nil || st.Next.PlanName != "torso" || st.Next.MicrocyclePosition != 1 || st.Next.SetsDelta != 1 || len(st.Next.Items) != 1 ||
		st.Next.Items[0] != (training.PrescribedItem{Exercise: "press banca", BaseSets: 3, Sets: 4}) {
		t.Fatalf("next = %+v", st.Next)
	}
	w.start("", "torso")
	st, err = w.svc.TrainingStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []training.PrescribedItem{
		{Exercise: "press banca", BaseSets: 3, Sets: 4, BaseTargetRPE: 8, TargetRPE: 8.5},
		{Exercise: "remo", BaseSets: 2, Sets: 3},
		{Exercise: "curl", BaseSets: 1, Sets: 2, BaseTargetRPE: 9.5, TargetRPE: 10},
	}
	if st.Next == nil || st.Next.PlanID != plan || st.Next.MicrocycleLabel != "carga" || !reflect.DeepEqual(st.Next.Items, want) {
		t.Fatalf("next = %+v", st.Next)
	}
	// Once the microcycle is done the next one, with no modifiers, shows the
	// routine as it is.
	w.start("", "empuje")
	st, err = w.svc.TrainingStatus(ctx)
	if err != nil || st.Next == nil || st.Next.MicrocyclePosition != 2 || st.Next.Items[0].Sets != st.Next.Items[0].BaseSets {
		t.Fatalf("next after the first microcycle = %+v, %v", st.Next, err)
	}
}

// The prescription port reads another account's session as missing and
// changes nothing of theirs.
func TestEveryPrescriptionMethodIsolatesAccounts(t *testing.T) {
	store := openTwoUsers(t)
	ctx := context.Background()
	clock := func() time.Time { return time.Date(2026, 11, 16, 18, 0, 0, 0, time.UTC) }
	other := training.NewService(store.ForUser(2), clock)
	plan, err := other.CreatePlan(ctx, training.Plan{Name: "torso", Items: []training.PlanItem{{Exercise: "remo", TargetSets: 3, TargetRPE: 8}}})
	if err != nil {
		t.Fatal(err)
	}
	block, err := other.SaveBlock(ctx, training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(1), Rotation: &[]int64{plan.ID},
		Microcycles: []training.MicrocycleInput{{Position: 1, SetsDelta: intPtr(1)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.AdvanceBlock(ctx, training.AdvanceInput{BlockID: block.ID, Action: training.BlockActivate}); err != nil {
		t.Fatal(err)
	}
	session, applied, err := other.StartPrescribedSession(ctx, "", plan.ID)
	if err != nil || applied == nil {
		t.Fatalf("other start = %+v, %v", applied, err)
	}
	before := dumpUser(t, store, 2)
	port := store.ForUser(1).(training.PrescriptionStore)
	cases := map[string]func(){
		"ApplyPrescription": func() {
			err := port.ApplyPrescription(ctx, session.ID, training.Prescription{Items: []training.PrescribedItem{{Exercise: "remo", BaseSets: 3, Sets: 1}}})
			if !errors.Is(err, training.ErrNotFound) {
				t.Errorf("ApplyPrescription = %v, want not found", err)
			}
		},
		"SessionPrescription": func() {
			if _, _, err := port.SessionPrescription(ctx, session.ID); !errors.Is(err, training.ErrNotFound) {
				t.Errorf("SessionPrescription = %v, want not found", err)
			}
		},
	}
	iface := reflect.TypeFor[training.PrescriptionStore]()
	for i := range iface.NumMethod() {
		name := iface.Method(i).Name
		c, ok := cases[name]
		if !ok {
			t.Errorf("%s has no two-user case", name)
			continue
		}
		c()
	}
	if after := dumpUser(t, store, 2); after != before {
		t.Fatalf("the other account's rows changed:\nbefore %s\nafter  %s", before, after)
	}
}

// Migration 034 only adds columns: every existing row keeps its values, and
// nothing reads as applied or closed by anyone.
func TestMigration034KeepsEveryRowAndRecordsNothing(t *testing.T) {
	path := t.TempDir() + "/training.db"
	db := migrateTo(t, path, 33)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO plans(id,user_id,name,notes,created_at) VALUES (7,1,'Torso','',CURRENT_TIMESTAMP)`)
	exec(`INSERT INTO blocks(id,user_id,intent,status,start_date) VALUES (3,1,'fuerza','active','2026-11-09')`)
	exec(`INSERT INTO block_microcycles(id,user_id,block_id,position,sets_delta,effort_delta,status,closed_on) VALUES (4,1,3,1,-1,0.5,'done','2026-11-10'),(5,1,3,2,NULL,NULL,'pending',NULL)`)
	exec(`INSERT INTO sessions(id,user_id,date,started_at,plan_id,plan_name,block_id,block_microcycle_id,block_off_plan) VALUES (1,1,'2026-11-10','2026-11-10 18:00:00',7,'Torso',3,4,0)`)
	exec(`INSERT INTO session_items(session_id,position,exercise,target_sets,target_rpe) VALUES (1,1,'press banca',2,8.5)`)
	snapshot := func(db *sql.DB) string {
		t.Helper()
		return dumpQuery(t, db, `SELECT id,user_id,date,started_at,plan_id,plan_name,block_id,block_microcycle_id,block_off_plan FROM sessions ORDER BY id`) +
			dumpQuery(t, db, `SELECT * FROM session_items ORDER BY id`) +
			dumpQuery(t, db, `SELECT id,user_id,block_id,position,label,sets_delta,effort_delta,own_rotation,status,closed_on FROM block_microcycles ORDER BY id`)
	}
	before := snapshot(db)
	db.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if after := snapshot(store.db); after != before {
		t.Fatalf("rows changed:\nbefore %s\nafter  %s", before, after)
	}
	if got := dumpQuery(t, store.db, `SELECT id FROM sessions WHERE block_modifiers IS NOT NULL UNION ALL SELECT id FROM block_microcycles WHERE closed_by IS NOT NULL`); got != "" {
		t.Fatalf("rows recorded by the migration: %s", got)
	}
	if _, err := store.db.Exec(`UPDATE block_microcycles SET closed_by='otro' WHERE id=4`); err == nil {
		t.Fatal("closed_by accepted an unknown value")
	}
}

// Every way a microcycle closes is recorded: the last routine of its rotation,
// by hand, or its dates passing when a calendar block goes back to sequence.
func TestAClosedMicrocycleSaysHowItClosed(t *testing.T) {
	ctx := context.Background()
	w := newPeriodWorld(t)
	b := w.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(4), Rotation: w.rotation("torso")})
	w.advance(b.ID, training.BlockActivate)
	w.start("", "torso")
	w.advance(b.ID, training.BlockSkipCurrent)
	v := w.advance(b.ID, training.BlockCompleteCurrent)
	if got := []string{v.Microcycles[0].ClosedBy, v.Microcycles[1].ClosedBy, v.Microcycles[2].ClosedBy, v.Microcycles[3].ClosedBy}; !reflect.DeepEqual(got, []string{"placement", "hand", "hand", ""}) {
		t.Fatalf("closed_by = %v", got)
	}

	c := newPeriodWorld(t)
	cb := c.block(training.BlockInput{Intent: strPtr("x"), MicrocycleCount: intPtr(3), Rotation: c.rotation("torso"), Advance: strPtr(training.AdvanceCalendar), MicrocycleDays: intPtr(2)})
	if _, err := c.svc.AdvanceBlock(ctx, training.AdvanceInput{BlockID: cb.ID, Action: training.BlockActivate, StartDate: "2026-11-13"}); err != nil {
		t.Fatal(err)
	}
	cv := c.block(training.BlockInput{ID: cb.ID, Advance: strPtr(training.AdvanceSequence)})
	if m := cv.Microcycles[0]; m.Status != training.MicrocycleDone || m.ClosedBy != training.ClosedByModeChange || cv.Microcycles[2].ClosedBy != "" {
		t.Fatalf("after the mode change = %+v", cv.Microcycles)
	}
}

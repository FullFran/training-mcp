package mcpsrv

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

// Tools keep kg whatever the person's unit: weight_kg is stored as sent and
// read back as stored, and live_workout adds the person's unit for the agent.
func TestToolsStayInKgWhenThePersonUsesLb(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/training.db"
	store, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := training.NewService(store.ForUser(1), liveClock)
	if err := service.SetAcceptAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	session := mustConnect(t, New(only(service)).Handler())

	var idle LiveWorkoutOut
	decode(t, call(t, session, "live_workout", map[string]any{}), &idle)
	if idle.Live.WeightUnit != "kg" {
		t.Fatalf("default unit = %q", idle.Live.WeightUnit)
	}
	if err := service.SetWeightUnit(ctx, "lb"); err != nil {
		t.Fatal(err)
	}
	call(t, session, "log_set", map[string]any{"exercise": "press banca", "weight_kg": 60, "reps": 8, "rpe": 8})
	var kg float64
	if err := db.QueryRow(`SELECT weight_kg FROM sets`).Scan(&kg); err != nil || kg != 60 {
		t.Fatalf("stored %v, %v", kg, err)
	}
	var live LiveWorkoutOut
	decode(t, call(t, session, "live_workout", map[string]any{}), &live)
	if live.Live.WeightUnit != "lb" || live.Live.LastSet == nil || live.Live.LastSet.Set.WeightKG != 60 {
		raw, _ := json.Marshal(live)
		t.Fatalf("live_workout = %s", raw)
	}
}

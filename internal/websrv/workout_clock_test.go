package websrv_test

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/sqlitestore/sqlitestoretest"
	"github.com/fullfran/training-mcp/internal/training"
)

// The workout clock and the finish summary time the workout from when the
// session started, as the server stored it, so every device shows the same
// duration; the other user's page carries no clock from it.
func TestWorkoutClockStartsWhenTheSessionStarted(t *testing.T) {
	ctx := context.Background()
	path := sqlitestoretest.Path(t)
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
	if _, err = db.Exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	services := training.NewServices(store, webClock)
	session, err := services.ForUser(1).StartSession(ctx, "2026-08-07")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE sessions SET started_at='2026-08-07 09:35:00' WHERE id=?`, session.ID); err != nil {
		t.Fatal(err)
	}

	phone, laptop := webHandler(t, services, 1), webHandler(t, services, 1)
	for name, h := range map[string]http.Handler{"phone": phone, "laptop": laptop} {
		body := get(t, h, base+"/?modo=entrenar").Body.String()
		panel := section(t, body, `id="panel"`, ">")
		if !strings.Contains(panel, `data-workout-elapsed="1500"`) {
			t.Fatalf("%s: panel should time the workout from its start (25 min ago): %s", name, panel)
		}
		finish := section(t, body, `class="finish-totals"`, "</p>")
		if !strings.Contains(finish, `data-clock>25:00<`) {
			t.Fatalf("%s: finish summary should show the duration so far: %s", name, finish)
		}
	}

	other := get(t, webHandler(t, services, 2), base+"/?modo=entrenar").Body.String()
	if strings.Contains(section(t, other, `id="panel"`, ">"), "data-workout-elapsed") {
		t.Fatal("the second user's page carries a clock from the owner's session")
	}
}

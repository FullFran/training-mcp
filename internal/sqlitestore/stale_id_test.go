package sqlitestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// A write transaction deletes user 1's row and gives its id to user 2, uncommitted.
// User 1's statement starts meanwhile: it still sees its own row, then waits for the
// write lock. No test hook or production change is needed to hold it in the window.
func TestStaleIDWriteCannotReachAnotherUser(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(u training.UserStore, id int64) error{
		"SetSessionItem": func(u training.UserStore, id int64) error {
			return u.SetSessionItem(ctx, id, training.PlanItem{Exercise: "inyectado", TargetSets: 9})
		},
		"SetFeedback": func(u training.UserStore, id int64) error {
			return u.SetFeedback(ctx, id, training.Feedback{MuscleGroup: "inyectado"})
		},
		"AddSet": func(u training.UserStore, id int64) error {
			_, _, err := u.AddSet(ctx, training.AddSetInput{SessionID: id, Exercise: "inyectado", WeightKG: 1, Reps: 1, RPE: 8})
			return err
		},
		"PatchSessionItem": func(u training.UserStore, id int64) error {
			n := 9
			return u.PatchSessionItem(ctx, id, "banca", training.SessionItemPatch{TargetSets: &n})
		},
		"SwapSessionItem":   func(u training.UserStore, id int64) error { return u.SwapSessionItem(ctx, id, "banca", "inyectado") },
		"RemoveSessionItem": func(u training.UserStore, id int64) error { return u.RemoveSessionItem(ctx, id, "banca") },
		"DeleteSession":     func(u training.UserStore, id int64) error { _, err := u.DeleteSession(ctx, id); return err },
		"SetPlanItem": func(u training.UserStore, id int64) error {
			return u.SetPlanItem(ctx, id, training.PlanItem{Exercise: "inyectado", TargetSets: 9})
		},
		"RemovePlanItem": func(u training.UserStore, id int64) error { return u.RemovePlanItem(ctx, id, "banca") },
		"MovePlanItem":   func(u training.UserStore, id int64) error { return u.MovePlanItem(ctx, id, "banca", 1) },
		"UpdatePlan": func(u training.UserStore, id int64) error {
			n := "robado"
			return u.UpdatePlan(ctx, id, training.PlanPatch{Name: &n})
		},
		"DeletePlan": func(u training.UserStore, id int64) error { return u.DeletePlan(ctx, id) },
		"UpdateSet": func(u training.UserStore, id int64) error {
			n := 99
			_, _, err := u.UpdateSet(ctx, id, training.SetPatch{Reps: &n})
			return err
		},
		"DeleteSet": func(u training.UserStore, id int64) error { _, _, _, err := u.DeleteSet(ctx, id); return err },
	}
	for name, attack := range cases {
		t.Run(name, func(t *testing.T) {
			path := t.TempDir() + "/w.db"
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			raw, _ := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(8000)")
			defer raw.Close()
			raw.Exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`)
			u1 := store.ForUser(1)
			plan, _ := u1.CreatePlan(ctx, training.Plan{Name: "uno", Items: []training.PlanItem{{Exercise: "x", TargetSets: 1}}})
			sess, _ := u1.Start(ctx, training.Session{Date: "2026-01-01"})
			set, _, _ := u1.AddSet(ctx, training.AddSetInput{SessionID: sess.ID, Exercise: "x", WeightKG: 1, Reps: 1, RPE: 8})
			id := sess.ID
			switch name {
			case "SetPlanItem", "RemovePlanItem", "MovePlanItem", "UpdatePlan", "DeletePlan":
				id = plan.ID
			case "UpdateSet", "DeleteSet":
				id = set.ID
			}

			conn, _ := raw.Conn(ctx)
			defer conn.Close()
			if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{
				fmt.Sprintf(`DELETE FROM sessions WHERE id=%d`, sess.ID),
				fmt.Sprintf(`DELETE FROM plans WHERE id=%d`, plan.ID),
				fmt.Sprintf(`INSERT INTO sessions(id,user_id,date,started_at,plan_name) VALUES (%d,2,'2026-02-02',CURRENT_TIMESTAMP,'')`, sess.ID),
				fmt.Sprintf(`INSERT INTO session_items(session_id,position,exercise,target_sets) VALUES (%d,1,'banca',3)`, sess.ID),
				fmt.Sprintf(`INSERT INTO sets(id,session_id,position,exercise,weight_kg,reps,rpe,si,created_at,updated_at) VALUES (%d,%d,1,'banca',50,5,8,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, set.ID, sess.ID),
				fmt.Sprintf(`INSERT INTO plans(id,user_id,name,notes,created_at) VALUES (%d,2,'dos','',CURRENT_TIMESTAMP)`, plan.ID),
				fmt.Sprintf(`INSERT INTO plan_items(plan_id,position,exercise,target_sets) VALUES (%d,1,'banca',3),(%d,2,'remo',3)`, plan.ID, plan.ID),
			} {
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			done := make(chan error, 1)
			go func() { done <- attack(u1, id) }()
			time.Sleep(300 * time.Millisecond)
			var commitErr error
			for i := 0; i < 3; i++ {
				if _, commitErr = conn.ExecContext(ctx, `COMMIT`); commitErr == nil {
					break
				}
				time.Sleep(time.Second)
			}
			if commitErr != nil {
				t.Fatal(commitErr)
			}
			attackErr := <-done

			dump := func() string {
				var out string
				for _, q := range []string{`SELECT 'sess',COUNT(*) FROM sessions WHERE user_id=2`, `SELECT i.exercise,i.target_sets FROM session_items i JOIN sessions s ON s.id=i.session_id WHERE s.user_id=2 ORDER BY 1`, `SELECT st.exercise,st.reps FROM sets st JOIN sessions s ON s.id=st.session_id WHERE s.user_id=2 ORDER BY 1`, `SELECT f.muscle_group,0 FROM session_feedback f JOIN sessions s ON s.id=f.session_id WHERE s.user_id=2`, `SELECT p.name,0 FROM plans p WHERE p.user_id=2`, `SELECT i.exercise,i.position*100+i.target_sets FROM plan_items i JOIN plans p ON p.id=i.plan_id WHERE p.user_id=2 ORDER BY 1`} {
					rows, err := raw.Query(q)
					if err != nil {
						t.Fatal(err)
					}
					for rows.Next() {
						var a string
						var b int
						rows.Scan(&a, &b)
						out += fmt.Sprintf("%s=%d ", a, b)
					}
					rows.Close()
				}
				return out
			}
			got := dump()
			want := "sess=1 banca=3 banca=5 dos=0 banca=103 remo=203 "
			t.Logf("attackErr=%v", attackErr)
			if got != want {
				t.Errorf("user 1's stale call changed user 2's data:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// Start holds no id of a session, but it does name a plan, whose id can be given to
// someone else between looking it up and storing it on the new session.
func TestStaleIDStartCannotAdoptAnotherUsersPlan(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/w.db"
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	raw, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(8000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.Exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	u1 := store.ForUser(1)
	plan, err := u1.CreatePlan(ctx, training.Plan{Name: "uno", Items: []training.PlanItem{{Exercise: "x", TargetSets: 1}}})
	if err != nil {
		t.Fatal(err)
	}

	conn, err := raw.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{
		`BEGIN IMMEDIATE`,
		fmt.Sprintf(`DELETE FROM plans WHERE id=%d`, plan.ID),
		fmt.Sprintf(`INSERT INTO plans(id,user_id,name,notes,created_at) VALUES (%d,2,'dos','',CURRENT_TIMESTAMP)`, plan.ID),
		fmt.Sprintf(`INSERT INTO plan_items(plan_id,position,exercise,target_sets) VALUES (%d,1,'banca',3)`, plan.ID),
	} {
		if _, err = conn.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := u1.Start(ctx, training.Session{Date: "2026-01-01", PlanID: plan.ID})
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("Start = %v, want not found", err)
	}
	var sessions, items int
	if err = raw.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err = raw.QueryRow(`SELECT COUNT(*) FROM session_items`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || items != 0 {
		t.Fatalf("a refused Start left %d sessions and %d items", sessions, items)
	}
}

// statementHook runs a callback just before each statement a database prepares, so a
// test can act between two statements of one store method with no production hook.
type statementHook struct {
	inner  driver.Driver
	dsn    string
	before func(query string)
}

func (h statementHook) Connect(context.Context) (driver.Conn, error) {
	conn, err := h.inner.Open(h.dsn)
	if err != nil {
		return nil, err
	}
	return hookedConn{Conn: conn, before: h.before}, nil
}

func (h statementHook) Driver() driver.Driver { return h.inner }

type hookedConn struct {
	driver.Conn
	before func(query string)
}

func (c hookedConn) Prepare(query string) (driver.Stmt, error) {
	c.before(query)
	return c.Conn.Prepare(query)
}

// A read that checks ownership and then fetches children must not hand back the
// children of whoever holds the id by the time the second statement runs.
func TestStaleIDReadCannotReturnAnotherUsersRows(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		after int
		read  func(training.UserStore, seeded) (any, error)
	}{
		{"GetSession", 1, func(u training.UserStore, id seeded) (any, error) { return u.GetSession(ctx, id.session) }},
		{"GetPlan", 1, func(u training.UserStore, id seeded) (any, error) { return u.GetPlan(ctx, id.plan) }},
		{"SessionProgress/items", 1, func(u training.UserStore, id seeded) (any, error) { return u.SessionProgress(ctx, id.session) }},
		{"SessionProgress/extra", 2, func(u training.UserStore, id seeded) (any, error) { return u.SessionProgress(ctx, id.session) }},
		{"ExerciseHistory/sets", 1, func(u training.UserStore, _ seeded) (any, error) { return u.ExerciseHistory(ctx, "banca", 50) }},
		{"ExerciseHistory/best", 2, func(u training.UserStore, _ seeded) (any, error) { return u.ExerciseHistory(ctx, "banca", 50) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := t.TempDir() + "/w.db"
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err = store.db.Exec(`INSERT INTO users(id,created_at) VALUES (2,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			ids := seedUser(t, store.ForUser(1), "pecho")

			statements := 0
			hooked := sql.OpenDB(statementHook{inner: store.db.Driver(), dsn: path + "?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", before: func(string) {
				if statements++; statements != tc.after+1 {
					return
				}
				for _, q := range []string{
					`DELETE FROM sessions WHERE id=?`,
					`INSERT INTO sessions(id,user_id,date,started_at,plan_name) VALUES (?,2,'2026-02-02',CURRENT_TIMESTAMP,'secreto')`,
					`INSERT INTO session_items(session_id,position,exercise,target_sets) VALUES (?,1,'secreto',77)`,
					`INSERT INTO sets(session_id,position,exercise,weight_kg,reps,rpe,si,created_at,updated_at) VALUES (?,1,'banca',77.7,5,8,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),(?,2,'secreto',77.7,5,8,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
				} {
					args := []any{ids.session}
					if strings.Count(q, "(?,") == 2 {
						args = append(args, ids.session)
					}
					if _, err := store.db.Exec(q, args...); err != nil {
						t.Error(q, err)
					}
				}
				for _, q := range []string{
					`DELETE FROM plans WHERE id=?`,
					`INSERT INTO plans(id,user_id,name,notes,created_at) VALUES (?,2,'secreto','',CURRENT_TIMESTAMP)`,
					`INSERT INTO plan_items(plan_id,position,exercise,target_sets) VALUES (?,1,'secreto',77)`,
				} {
					if _, err := store.db.Exec(q, ids.plan); err != nil {
						t.Error(q, err)
					}
				}
			}})
			defer hooked.Close()

			out, err := tc.read(&userStore{db: hooked, userID: 1}, ids)
			if statements <= tc.after {
				t.Fatalf("the read ran %d statements, so the id never changed hands after statement %d", statements, tc.after)
			}
			if leaked := userTwoMarkers(t, out, err); leaked != "" {
				t.Fatalf("user 1's stale read returned user 2's rows (%s): %s", leaked, mustJSON(t, out))
			}
		})
	}
}

// userTwoMarkerRE matches the values injected for user 2 as whole JSON numbers:
// a 77.7 kg set, or an item with target_sets 77.
var userTwoMarkerRE = regexp.MustCompile(`[:,\[]77\.7[,}\]]|"target_sets":77[,}]`)

// userTwoMarkers reports which of user 2's injected markers a read returned.
// It inspects the JSON encoding, so pointers are dereferenced and their
// addresses (which can contain any digits) never reach the match.
func userTwoMarkers(t *testing.T, out any, err error) string {
	t.Helper()
	text := mustJSON(t, out)
	if err != nil {
		text += " " + err.Error()
	}
	if strings.Contains(text, "secreto") {
		return "secreto"
	}
	return userTwoMarkerRE.FindString(text)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode read result: %v", err)
	}
	return string(b)
}

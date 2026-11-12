package sqlitestore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

const undoMigration = 24

// acceptedWithUndo applies a change as an accepted proposal of user, with the
// undo image the store captures.
func acceptedWithUndo(t *testing.T, user training.UserStore, scope training.UndoScope, change func(context.Context) error) int64 {
	t.Helper()
	ctx := context.Background()
	p, err := user.CreateProposal(ctx, proposal("add_set"))
	if err != nil {
		t.Fatal(err)
	}
	if err := user.ClaimProposal(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	result, image, err := user.(training.UndoStore).CaptureUndo(ctx, scope, func(ctx context.Context) (json.RawMessage, error) {
		return json.RawMessage(`{}`), change(ctx)
	})
	if err != nil || image == nil {
		t.Fatalf("capture = %s, %v", image, err)
	}
	if _, err := user.FinishProposal(ctx, p.ID, training.ProposalOutcome{Status: training.ProposalAccepted, DecidedBy: training.DecidedByPerson, Result: result, At: time.Now(), UndoImage: image}); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func undoEntry() (training.Proposal, training.DecisionRecord) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	return training.Proposal{Tool: training.UndoTool, ActionType: training.UndoAction, Payload: json.RawMessage(`{}`), DecidedBy: training.DecidedByPerson, CreatedAt: now, DecidedAt: now},
		training.DecisionRecord{Time: now, Permission: training.UndoAction, Outcome: training.OutcomeAllow, DeciderKind: training.DeciderSubject, DeciderID: training.AccountHolder, Channel: training.ChannelWeb}
}

func undo(user training.UserStore, id int64) (training.Proposal, error) {
	entry, record := undoEntry()
	return user.(training.UndoStore).UndoProposal(context.Background(), id, entry, record)
}

func startWithSet(t *testing.T, user training.UserStore) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	session, err := user.Start(ctx, training.Session{Date: "2026-11-12"})
	if err != nil {
		t.Fatal(err)
	}
	set, _, err := user.AddSet(ctx, training.AddSetInput{SessionID: session.ID, Exercise: "banca", WeightKG: 80, Reps: 8, RPE: 8})
	if err != nil {
		t.Fatal(err)
	}
	return session.ID, set.ID
}

// Each account captures and undoes only its own rows: another account's undo of
// a proposal answers as for a missing one and changes nothing, and a scope naming
// another account's session captures none of its rows.
func TestUndoIsIsolatedBetweenUsers(t *testing.T) {
	store := openTwoUsers(t)
	ctx := context.Background()
	owner, second := store.ForUser(1), store.ForUser(2)
	ownSession, ownSet := startWithSet(t, owner)
	otherSession, otherSet := startWithSet(t, second)
	before := dumpData(t, store)

	id := acceptedWithUndo(t, owner, training.UndoScope{SessionIDs: []int64{ownSession, otherSession}, SetIDs: []int64{ownSet, otherSet}}, func(ctx context.Context) error {
		_, _, _, err := owner.DeleteSet(ctx, ownSet)
		return err
	})
	var raw string
	if err := store.db.QueryRow(`SELECT undo_image FROM proposals WHERE id=?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var image undoImage
	if err := json.Unmarshal([]byte(raw), &image); err != nil {
		t.Fatal(err)
	}
	for _, c := range image.Rows {
		if c.Table == "sets" && c.Key["id"].v == otherSet || c.Table == "sessions" && c.Key["id"].v == otherSession {
			t.Fatalf("the image holds another account's row: %+v", c)
		}
	}

	afterChange := dumpAll(t, store)
	if _, err := undo(second, id); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("another account's undo = %v, want ErrNotFound", err)
	}
	if dumpAll(t, store) != afterChange {
		t.Fatal("another account's undo changed data")
	}
	if _, err := undo(owner, id); err != nil {
		t.Fatal(err)
	}
	if got := dumpData(t, store); got != before {
		t.Fatalf("undo did not restore:\n%s\nwant\n%s", got, before)
	}
	if _, err := second.GetProposal(ctx, id); !errors.Is(err, training.ErrNotFound) {
		t.Fatal("the undo made the proposal visible to another account")
	}
}

// dumpData is every account's data rows, leaving out the proposals and
// decision records a change and its undo add.
func dumpData(t *testing.T, store *Store) string {
	t.Helper()
	var out string
	for _, table := range userTables {
		if table == "proposals" || table == "decision_records" {
			continue
		}
		out += table + ":\n" + dumpQuery(t, store.db, `SELECT * FROM `+table+` ORDER BY rowid`)
	}
	return out
}

// An image is only ever written by the store for the account that captured it;
// even so, one naming another account's rows cannot reach them.
func TestUndoImageCannotReachAnotherAccount(t *testing.T) {
	store := openTwoUsers(t)
	owner, second := store.ForUser(1), store.ForUser(2)
	ownSession, _ := startWithSet(t, owner)
	otherSession, otherSet := startWithSet(t, second)
	id := acceptedWithUndo(t, owner, training.UndoScope{SessionIDs: []int64{ownSession}}, func(context.Context) error { return nil })

	var row undoRow
	{
		rows, err := store.db.Query(`SELECT * FROM sets WHERE id=?`, otherSet)
		if err != nil {
			t.Fatal(err)
		}
		list, err := scanRows(rows)
		if err != nil || len(list) != 1 {
			t.Fatal(err)
		}
		row = list[0]
	}
	moved := undoRow{}
	for k, v := range row {
		moved[k] = v
	}
	moved["id"] = cell{int64(9999)}
	moved["position"] = cell{int64(50)}
	for name, rows := range map[string][]undoChange{
		// Undoing an "added" row of the other account would delete it.
		"delete": {{Table: "sets", Key: undoRow{"id": cell{otherSet}}, After: row}},
		// Restoring a "removed" row into the other account's session.
		"insert": {{Table: "sets", Key: undoRow{"id": cell{int64(9999)}}, Before: moved}},
		// Rewriting the other account's session.
		"update": {{Table: "sessions", Key: undoRow{"id": cell{otherSession}}, Before: undoRow{"id": cell{otherSession}, "notes": cell{"robado"}}, After: undoRow{"id": cell{otherSession}, "notes": cell{""}}}},
	} {
		t.Run(name, func(t *testing.T) {
			image, err := json.Marshal(undoImage{Version: 1, Rows: rows})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(`UPDATE proposals SET undo_image=?, undone_by=0 WHERE id=?`, string(image), id); err != nil {
				t.Fatal(err)
			}
			before := dumpAll(t, store)
			if _, err := undo(owner, id); err == nil {
				t.Fatal("an image naming another account's rows was undone")
			}
			if dumpAll(t, store) != before {
				t.Fatal("a refused undo changed data")
			}
		})
	}
}

// Proposals answered before undo existed hold no image and cannot be undone.
func TestUndoMigrationLeavesEarlierProposalsNotUndoable(t *testing.T) {
	path := databaseBefore(t, undoMigration)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	user := store.ForUser(2)
	ctx := context.Background()
	p, err := user.CreateProposal(ctx, proposal("add_set"))
	if err != nil {
		t.Fatal(err)
	}
	if err := user.ClaimProposal(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	done, err := user.FinishProposal(ctx, p.ID, training.ProposalOutcome{Status: training.ProposalAccepted, DecidedBy: training.DecidedByPerson, At: time.Now()})
	if err != nil || done.Undoable || done.UndoneBy != 0 {
		t.Fatalf("finished without image = %+v, %v", done, err)
	}
	if _, err := undo(user, p.ID); !errors.Is(err, training.ErrNotUndoable) {
		t.Fatalf("undo = %v, want ErrNotUndoable", err)
	}
}

package mcpsrv

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

// fullDump is every user's undoable rows, timestamps included, plus the import
// links a deleted set clears.
func fullDump(t *testing.T, w isolationWorld) string {
	t.Helper()
	var out strings.Builder
	out.WriteString(dumpTables(t, w.db, nil))
	rows, err := w.db.Query(`SELECT user_id,natural_key,import_id,set_id FROM imported_sets ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var user, imp int64
		var key string
		var set *int64
		if err := rows.Scan(&user, &key, &imp, &set); err != nil {
			t.Fatal(err)
		}
		out.WriteString("imported_sets " + strconv.FormatInt(user, 10) + " " + key + " " + strconv.FormatInt(imp, 10))
		if set != nil {
			out.WriteString(" " + strconv.FormatInt(*set, 10))
		}
		out.WriteString("\n")
	}
	return out.String()
}

// linkImport marks the world's set as imported, so deleting it clears a link.
func linkImport(t *testing.T, w isolationWorld) {
	t.Helper()
	if _, err := w.db.Exec(`INSERT INTO imports(id,user_id,source,file_name,content_hash,sessions,sets,imputed,created_at) VALUES (1,1,'hevy','a.csv','h',1,1,0,'2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.Exec(`INSERT INTO imported_sets(user_id,natural_key,import_id,set_id) VALUES (1,'k',1,?)`, w.ids.set); err != nil {
		t.Fatal(err)
	}
}

func ftsIntact(t *testing.T, w isolationWorld) {
	t.Helper()
	if _, err := w.db.Exec(`INSERT INTO coach_memory_fts(coach_memory_fts) VALUES('integrity-check')`); err != nil {
		t.Fatalf("memory search index out of step: %v", err)
	}
}

func proposalOf(t *testing.T, result map[string]any) int64 {
	t.Helper()
	id, ok := result["proposal_id"].(float64)
	if !ok || id == 0 {
		t.Fatalf("no proposal in %v", result)
	}
	return int64(id)
}

// Undoing an accepted proposal of every write tool puts back exactly the rows
// it changed, timestamps and ids included, deletions too, and touches no other
// user's rows. The undo is its own history entry with a decision record, and a
// proposal is undone once.
func TestUndoRestoresTheExactPriorStateForEveryWriteTool(t *testing.T) {
	ctx := context.Background()
	for name := range wantActionType {
		t.Run(name, func(t *testing.T) {
			w := newIsolationWorld(t)
			linkImport(t, w)
			args := isolationCases[name].args(w.ids)
			if name == "log_sets" {
				args["idempotency_key"] = "nueva"
			}
			before := fullDump(t, w)
			result := structured(t, call(t, w.owner, name, args))
			if result["status"] != "accepted" {
				t.Fatalf("result = %v", result)
			}
			id := proposalOf(t, result)
			// The world's clock is fixed, so confirming the fact it saved at that
			// instant restarts its review at the same time: nothing changes.
			if fullDump(t, w) == before && name != "memory_confirm" {
				t.Fatal("the tool changed nothing, so its undo proves nothing")
			}
			p, err := w.ownerService.GetProposal(ctx, id)
			if err != nil || !p.Undoable {
				t.Fatalf("accepted proposal = %+v, %v; want undoable", p, err)
			}

			entry, err := w.ownerService.UndoProposal(ctx, id)
			if err != nil {
				t.Fatalf("undo: %v", err)
			}
			if got := fullDump(t, w); got != before {
				t.Fatalf("undo did not restore the prior state:\ngot\n%s\nwant\n%s", got, before)
			}
			ftsIntact(t, w)
			if entry.Tool != training.UndoTool || entry.ActionType != training.UndoAction || entry.Status != training.ProposalAccepted ||
				entry.DecidedBy != training.DecidedByPerson || entry.Undoable || string(entry.Payload) != `{"proposal_id":`+strconv.FormatInt(id, 10)+`}` {
				t.Fatalf("undo entry = %+v", entry)
			}
			undone, err := w.ownerService.GetProposal(ctx, id)
			if err != nil || undone.UndoneBy != entry.ID || undone.Undoable || undone.Status != training.ProposalAccepted {
				t.Fatalf("undone proposal = %+v, %v", undone, err)
			}
			records, err := w.ownerService.Decisions(ctx, 1)
			if err != nil || len(records) != 1 {
				t.Fatalf("records = %+v, %v", records, err)
			}
			if r := records[0]; r.Permission != training.UndoAction || r.Outcome != training.OutcomeAllow || r.Operation != training.OperationID(entry.ID) ||
				r.DeciderKind != training.DeciderSubject || r.DeciderID != training.AccountHolder || r.Channel != training.ChannelWeb {
				t.Fatalf("undo record = %+v", r)
			}
			if _, err := w.ownerService.UndoProposal(ctx, id); !errors.Is(err, training.ErrUndone) {
				t.Fatalf("second undo = %v, want ErrUndone", err)
			}
			if _, err := w.ownerService.UndoProposal(ctx, entry.ID); !errors.Is(err, training.ErrNotUndoable) {
				t.Fatalf("undoing the undo = %v, want ErrNotUndoable", err)
			}
		})
	}
}

// A proposal the person accepted from the review page is undone the same way.
func TestUndoAfterThePersonAccepted(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	if err := w.ownerService.SetAcceptAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	before := fullDump(t, w)
	id := proposalOf(t, structured(t, call(t, w.owner, "delete_session", map[string]any{"session_id": w.ids.session})))
	if _, err := w.ownerService.AcceptProposal(ctx, id, training.AnswerAllowOnce, w.ownerServer.Apply); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ownerService.UndoProposal(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := fullDump(t, w); got != before {
		t.Fatalf("got\n%s\nwant\n%s", got, before)
	}
}

func wantConflict(t *testing.T, err error, reason, table string) {
	t.Helper()
	var conflict *training.UndoConflict
	if !errors.As(err, &conflict) || conflict.Reason != reason || conflict.Table != table {
		t.Fatalf("undo = %v, want %s on %s", err, reason, table)
	}
}

// When later changes depend on the undone one, the undo is refused with the
// reason and changes nothing.
func TestUndoIsRefusedWhenLaterChangesDependOnIt(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		tool   string
		args   func(toolIDs) map[string]any
		later  func(t *testing.T, w isolationWorld, result map[string]any)
		reason string
		table  string
	}{
		{"a set edited after it", "update_set", func(id toolIDs) map[string]any { return map[string]any{"set_id": id.set, "reps": 5} },
			func(t *testing.T, w isolationWorld, _ map[string]any) {
				reps := 6
				if _, _, err := w.ownerService.UpdateSet(ctx, w.ids.set, training.SetPatch{Reps: &reps}); err != nil {
					t.Fatal(err)
				}
			}, training.UndoChanged, "sets"},
		{"a set deleted after it", "update_set", func(id toolIDs) map[string]any { return map[string]any{"set_id": id.set, "reps": 5} },
			func(t *testing.T, w isolationWorld, _ map[string]any) {
				if _, _, _, err := w.ownerService.DeleteSet(ctx, w.ids.set); err != nil {
					t.Fatal(err)
				}
			}, training.UndoChanged, "sets"},
		{"a set logged in the session it started", "start_session", func(toolIDs) map[string]any { return map[string]any{"date": "2026-03-01"} },
			func(t *testing.T, w isolationWorld, result map[string]any) {
				session := int64(result["session"].(map[string]any)["id"].(float64))
				if _, _, err := w.ownerService.AddSet(ctx, training.AddSetInput{SessionID: session, Exercise: "remo", WeightKG: 50, Reps: 10, RPE: 8}); err != nil {
					t.Fatal(err)
				}
			}, training.UndoDepended, "sessions"},
		{"a session started from the plan it created", "create_plan", func(toolIDs) map[string]any {
			return map[string]any{"name": "pierna", "items": []map[string]any{{"exercise": "sentadilla", "target_sets": 3}}}
		},
			func(t *testing.T, w isolationWorld, result map[string]any) {
				plan := int64(result["plan"].(map[string]any)["id"].(float64))
				if _, err := w.ownerService.StartPlannedSession(ctx, "2026-03-02", plan); err != nil {
					t.Fatal(err)
				}
			}, training.UndoDepended, "plans"},
		{"a set logged where a deleted one stood", "delete_set", func(id toolIDs) map[string]any { return map[string]any{"set_id": id.set} },
			func(t *testing.T, w isolationWorld, _ map[string]any) {
				if _, _, err := w.ownerService.AddSet(ctx, training.AddSetInput{SessionID: w.ids.session, Exercise: "remo", WeightKG: 50, Reps: 10, RPE: 8}); err != nil {
					t.Fatal(err)
				}
			}, training.UndoOccupied, "sets"},
		{"a memory rewritten after it", "memory_forget", func(id toolIDs) map[string]any { return map[string]any{"memory_id": id.memory} },
			func(t *testing.T, w isolationWorld, _ map[string]any) {
				if _, err := w.db.Exec(`UPDATE coach_memory SET body='otra' WHERE id=?`, w.ids.memory); err != nil {
					t.Fatal(err)
				}
			}, training.UndoChanged, "coach_memory"},
		{"a rest target set again after it", "set_rest_target", func(toolIDs) map[string]any { return map[string]any{"rest_seconds": 90} },
			func(t *testing.T, w isolationWorld, _ map[string]any) {
				if err := w.ownerService.SetRestTarget(ctx, 150); err != nil {
					t.Fatal(err)
				}
			}, training.UndoChanged, "users"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newIsolationWorld(t)
			result := structured(t, call(t, w.owner, tc.tool, tc.args(w.ids)))
			id := proposalOf(t, result)
			tc.later(t, w, result)
			before := fullDump(t, w)
			_, err := w.ownerService.UndoProposal(ctx, id)
			wantConflict(t, err, tc.reason, tc.table)
			if got := fullDump(t, w); got != before {
				t.Fatalf("a refused undo changed data:\ngot\n%s\nwant\n%s", got, before)
			}
			if p, err := w.ownerService.GetProposal(ctx, id); err != nil || p.UndoneBy != 0 || !p.Undoable {
				t.Fatalf("refused proposal = %+v, %v", p, err)
			}
		})
	}
}

// Later changes elsewhere do not stand in the way: a set logged in another
// session, or the same session's other sets.
func TestUndoIgnoresUnrelatedLaterChanges(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	id := proposalOf(t, structured(t, call(t, w.owner, "add_set", map[string]any{"session_id": w.ids.session, "exercise": "remo", "weight_kg": 60, "reps": 8, "rpe": 8})))
	other, err := w.ownerService.StartPlannedSession(ctx, "2026-03-03", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.ownerService.AddSet(ctx, training.AddSetInput{SessionID: other.ID, Exercise: "remo", WeightKG: 50, Reps: 10, RPE: 8}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ownerService.UndoProposal(ctx, id); err != nil {
		t.Fatalf("undo = %v", err)
	}
	var n int
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM sets WHERE session_id=?`, other.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the other session's set = %d, %v", n, err)
	}
}

// Only the account that accepted a proposal can undo it: to anyone else it is
// missing, and their undo changes nothing.
func TestOnlyTheOwnerCanUndo(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	id := proposalOf(t, structured(t, call(t, w.owner, "delete_session", map[string]any{"session_id": w.ids.session})))
	before := fullDump(t, w)
	if _, err := w.services.ForUser(2).UndoProposal(ctx, id); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("another user's undo = %v, want ErrNotFound", err)
	}
	if got := fullDump(t, w); got != before {
		t.Fatal("another user's undo changed data")
	}
	if p, _ := w.ownerService.GetProposal(ctx, id); p.UndoneBy != 0 {
		t.Fatal("another user's undo marked it undone")
	}
	// The second user's own proposal undoes only their rows.
	second := proposalOf(t, structured(t, call(t, w.second, "set_rest_target", map[string]any{"rest_seconds": 75})))
	ownerRows := dumpTables(t, w.db, ownersRows)
	if _, err := w.services.ForUser(2).UndoProposal(ctx, second); err != nil {
		t.Fatal(err)
	}
	if got := dumpTables(t, w.db, ownersRows); got != ownerRows {
		t.Fatal("the second user's undo changed the owner's rows")
	}
}

// Rejected, pending and failed proposals hold nothing to undo.
func TestOnlyAcceptedProposalsCanBeUndone(t *testing.T) {
	ctx := context.Background()
	w := newIsolationWorld(t)
	if err := w.ownerService.SetAcceptAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	pending := proposalOf(t, structured(t, call(t, w.owner, "set_rest_target", map[string]any{"rest_seconds": 90})))
	if _, err := w.ownerService.UndoProposal(ctx, pending); !errors.Is(err, training.ErrNotUndoable) {
		t.Fatalf("pending = %v", err)
	}
	if _, err := w.ownerService.RejectProposal(ctx, pending, training.AnswerRejectOnce); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ownerService.UndoProposal(ctx, pending); !errors.Is(err, training.ErrNotUndoable) {
		t.Fatalf("rejected = %v", err)
	}
	failing := proposalOf(t, structured(t, call(t, w.owner, "update_set", map[string]any{"set_id": 999999, "reps": 5})))
	if _, err := w.ownerService.AcceptProposal(ctx, failing, training.AnswerAllowOnce, w.ownerServer.Apply); err == nil {
		t.Fatal("accepting a proposal for a missing set succeeded")
	}
	if _, err := w.ownerService.UndoProposal(ctx, failing); !errors.Is(err, training.ErrNotUndoable) {
		t.Fatalf("failed = %v", err)
	}
}

// No tool lets an agent undo: undo is the person's own action in the app.
func TestNoToolUndoes(t *testing.T) {
	w := newIsolationWorld(t)
	for _, name := range w.ownerServer.WriteTools() {
		if strings.Contains(name, "undo") {
			t.Fatalf("write tool %q undoes", name)
		}
	}
	tools, err := w.owner.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if strings.Contains(tool.Name, "undo") || strings.Contains(tool.Name, "revert") {
			t.Fatalf("tool %q undoes", tool.Name)
		}
	}
}

// The person logging sets while an agent's proposal is accepted keeps every
// set: the capture holds their writes back until it ends, so undoing the
// proposal removes only what the proposal did. Neither side's write fails:
// transactions take the write lock first and wait for each other.
func TestUndoKeepsWritesThePersonMadeWhileAProposalApplied(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 15; i++ {
		w := newIsolationWorld(t)
		other, err := w.ownerService.StartPlannedSession(ctx, "2026-03-04", 0)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan int, 1)
		go func() {
			stored := 0
			for j := 0; j < 10; j++ {
				if _, _, err := w.ownerService.AddSet(ctx, training.AddSetInput{SessionID: other.ID, Exercise: "remo", WeightKG: 50, Reps: 10, RPE: 8}); err == nil {
					stored++
				}
			}
			done <- stored
		}()
		result := structured(t, call(t, w.owner, "add_set", map[string]any{"session_id": w.ids.session, "exercise": "remo", "weight_kg": 60, "reps": 8, "rpe": 8}))
		if stored := <-done; stored != 10 || result["status"] != "accepted" {
			t.Fatalf("run %d: person stored %d of 10, proposal %v", i, stored, result["status"])
		}
		if _, err := w.ownerService.UndoProposal(ctx, proposalOf(t, result)); err != nil {
			t.Fatalf("undo: %v", err)
		}
		var n int
		if err := w.db.QueryRow(`SELECT COUNT(*) FROM sets WHERE session_id=?`, other.ID).Scan(&n); err != nil || n != 10 {
			t.Fatalf("run %d: the person's sets after undo = %d, %v; want 10", i, n, err)
		}
	}
}

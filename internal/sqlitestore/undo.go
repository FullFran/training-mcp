package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/fullfran/training-mcp/internal/training"
)

// Undo images. Accepting a proposal captures the user's undoable rows before and
// after its change and keeps only the rows that differ, each with its values
// before (none when the change added it) and after (none when it removed it).
// Undoing checks every row is still as the change left it, then writes the
// before values back in one transaction. The rules are in training/undo.go.

// undoTable is how one table's rows are captured and restored.
type undoTable struct {
	name string
	key  []string
	// cols, when set, are the only columns captured: of users, only the rest
	// target is an agent's to change.
	cols []string
	// owned selects the user's rows; its single parameter is the user id.
	owned string
	// bySession tables hang off sessions and are too large to capture whole:
	// only the rows of the sessions in scope are, plus every row added during
	// the change, found by an id above the largest before it.
	bySession bool
	// bySet: imported_sets, whose link to a set is cleared when the set is
	// deleted; the links into scope sessions and the cleared ones are captured.
	bySet bool
	// insertOnly tables capture only the rows a change adds: idempotency keys
	// expire on their own, and undo leaves that expiry alone.
	insertOnly bool
	// temp gives a unique column a placeholder, set while rows are rewritten so
	// two rows can trade values (positions, names) without a collision.
	temp map[string]string
	// refs are the columns elsewhere that point at this table's id.
	refs []undoRef
}

type undoRef struct{ table, col string }

const (
	sessionOwned = `session_id IN (SELECT id FROM sessions WHERE user_id=?)`
	positionTemp = `-1000000000-id`
	textTemp     = `char(0)||'undo:'||id`
)

// undoTables are in the order rows are added back: a parent before its rows.
var undoTables = []undoTable{
	{name: "users", key: []string{"id"}, cols: []string{"id", "rest_seconds"}, owned: `id=?`},
	{name: "plans", key: []string{"id"}, owned: `user_id=?`, refs: []undoRef{{"plan_items", "plan_id"}, {"sessions", "plan_id"}}},
	{name: "plan_items", key: []string{"id"}, owned: `plan_id IN (SELECT id FROM plans WHERE user_id=?)`, temp: map[string]string{"position": positionTemp}},
	{name: "sessions", key: []string{"id"}, owned: `user_id=?`, refs: []undoRef{{"sets", "session_id"}, {"session_items", "session_id"}, {"session_feedback", "session_id"}}},
	{name: "sets", key: []string{"id"}, owned: sessionOwned, bySession: true, temp: map[string]string{"position": positionTemp}, refs: []undoRef{{"imported_sets", "set_id"}}},
	{name: "session_items", key: []string{"id"}, owned: sessionOwned, bySession: true, temp: map[string]string{"exercise": textTemp}},
	{name: "session_feedback", key: []string{"id"}, owned: sessionOwned, bySession: true, temp: map[string]string{"muscle_group": textTemp}},
	{name: "imported_sets", key: []string{"user_id", "natural_key"}, owned: `user_id=?`, bySet: true},
	{name: "exercise_notes", key: []string{"user_id", "exercise"}, owned: `user_id=?`},
	{name: "exercise_groups", key: []string{"user_id", "exercise"}, owned: `user_id=?`},
	{name: "volume_landmarks", key: []string{"user_id", "muscle_group"}, owned: `user_id=?`},
	{name: "idempotency_keys", key: []string{"user_id", "key"}, owned: `user_id=?`, insertOnly: true},
	{name: "coach_memory", key: []string{"id"}, owned: `user_id=?`, temp: map[string]string{"topic_key": textTemp},
		refs: []undoRef{{"coach_memory_revisions", "memory_id"}, {"coach_memory_relations", "memory_id"}, {"coach_memory_relations", "candidate_id"}, {"coach_memory", "superseded_by"}, {"coach_memory", "consolidated_into"}}},
	{name: "coach_memory_revisions", key: []string{"id"}, owned: `memory_id IN (SELECT id FROM coach_memory WHERE user_id=?)`},
	{name: "coach_memory_relations", key: []string{"id"}, owned: `user_id=?`},
}

func undoTableNamed(name string) (undoTable, int, bool) {
	for i, t := range undoTables {
		if t.name == name {
			return t, i, true
		}
	}
	return undoTable{}, 0, false
}

// cell is one stored value with its SQLite type, so a value written back is
// stored exactly as it was: an integer stays an integer, a real a real.
type cell struct{ v any }

func (c cell) MarshalJSON() ([]byte, error) {
	switch v := c.v.(type) {
	case nil:
		return []byte("null"), nil
	case int64:
		return json.Marshal(map[string]int64{"i": v})
	case float64:
		return json.Marshal(map[string]float64{"r": v})
	case string:
		return json.Marshal(map[string]string{"s": v})
	case []byte:
		return json.Marshal(map[string]string{"b": base64.StdEncoding.EncodeToString(v)})
	}
	return nil, fmt.Errorf("undo: unsupported value %T", c.v)
}

func (c *cell) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		c.v = nil
		return nil
	}
	var tagged struct {
		I *int64   `json:"i"`
		R *float64 `json:"r"`
		S *string  `json:"s"`
		B *string  `json:"b"`
	}
	if err := json.Unmarshal(data, &tagged); err != nil {
		return err
	}
	switch {
	case tagged.I != nil:
		c.v = *tagged.I
	case tagged.R != nil:
		c.v = *tagged.R
	case tagged.S != nil:
		c.v = *tagged.S
	case tagged.B != nil:
		b, err := base64.StdEncoding.DecodeString(*tagged.B)
		if err != nil {
			return err
		}
		c.v = b
	default:
		return fmt.Errorf("undo: untyped value %s", data)
	}
	return nil
}

type undoRow map[string]cell

// sig is a row's canonical form, for comparing two rows.
func (r undoRow) sig() string {
	b, _ := json.Marshal(r)
	return string(b)
}

// only is the row limited to cols, the columns another row holds.
func (r undoRow) only(cols []string) undoRow {
	out := undoRow{}
	for _, c := range cols {
		if v, ok := r[c]; ok {
			out[c] = v
		}
	}
	return out
}

func (r undoRow) columns() []string {
	cols := make([]string, 0, len(r))
	for c := range r {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	return cols
}

// undoChange is one row a proposal changed. Before is nil when it added the
// row, After nil when it removed it.
type undoChange struct {
	Table  string  `json:"t"`
	Key    undoRow `json:"k"`
	Before undoRow `json:"b"`
	After  undoRow `json:"a"`
}

type undoImage struct {
	Version int          `json:"v"`
	Rows    []undoChange `json:"rows"`
}

// undoLocks serialises the captures of one user, so rows another of their
// proposals adds at the same moment are not taken for this one's.
type undoLocks struct{ m sync.Map }

func (l *undoLocks) lock(userID int64) func() {
	if l == nil {
		return func() {}
	}
	v, _ := l.m.LoadOrStore(userID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// snapshot is the rows captured of each table, by key.
type snapshot map[string]map[string]undoRow

func keyOf(t undoTable, r undoRow) undoRow { return r.only(t.key) }

func scanRows(rows *sql.Rows) ([]undoRow, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []undoRow
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := undoRow{}
		for i, c := range cols {
			switch v := values[i].(type) {
			case nil, int64, float64, string:
				row[c] = cell{v}
			case []byte:
				row[c] = cell{append([]byte(nil), v...)}
			case bool:
				if v {
					row[c] = cell{int64(1)}
				} else {
					row[c] = cell{int64(0)}
				}
			default:
				return nil, fmt.Errorf("undo: unsupported value %T in column %s", v, c)
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func selectCols(t undoTable) string {
	if t.cols != nil {
		return strings.Join(t.cols, ",")
	}
	return "*"
}

// capture reads the undoable rows of the user. Before the change (maxIDs nil)
// it records the largest id of each bySession table; after it, maxIDs adds the
// rows above them.
func (s *userStore) capture(ctx context.Context, q queryer, sessions []int64, maxIDs map[string]int64) (snapshot, error) {
	snap := snapshot{}
	for _, t := range undoTables {
		query := `SELECT ` + selectCols(t) + ` FROM ` + t.name + ` WHERE ` + t.owned
		args := []any{s.userID}
		switch {
		case t.bySession:
			var parts []string
			if len(sessions) > 0 {
				parts = append(parts, `session_id IN (`+placeholders(len(sessions))+`)`)
				for _, id := range sessions {
					args = append(args, id)
				}
			}
			if maxIDs != nil {
				parts = append(parts, `id > ?`)
				args = append(args, maxIDs[t.name])
			}
			if len(parts) == 0 {
				snap[t.name] = map[string]undoRow{}
				continue
			}
			query += ` AND (` + strings.Join(parts, " OR ") + `)`
		case t.bySet:
			query += ` AND (set_id IS NULL`
			if len(sessions) > 0 {
				query += ` OR set_id IN (SELECT id FROM sets WHERE session_id IN (` + placeholders(len(sessions)) + `))`
				for _, id := range sessions {
					args = append(args, id)
				}
			}
			query += `)`
		case t.insertOnly && maxIDs == nil:
			// Before the change only which keys exist matters.
			query = `SELECT ` + strings.Join(t.key, ",") + ` FROM ` + t.name + ` WHERE ` + t.owned
		}
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		list, err := scanRows(rows)
		if err != nil {
			return nil, err
		}
		byKey := make(map[string]undoRow, len(list))
		for _, r := range list {
			byKey[keyOf(t, r).sig()] = r
		}
		snap[t.name] = byKey
	}
	return snap, nil
}

// scopeSessions are the user's sessions a change may touch beyond adding rows.
// Another user's ids drop out.
func (s *userStore) scopeSessions(ctx context.Context, q queryer, scope training.UndoScope) ([]int64, error) {
	seen := map[int64]bool{}
	var out []int64
	add := func(query string, ids []int64) error {
		if len(ids) == 0 {
			return nil
		}
		args := []any{s.userID}
		for _, id := range ids {
			args = append(args, id)
		}
		rows, err := q.QueryContext(ctx, fmt.Sprintf(query, placeholders(len(ids))), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		return rows.Err()
	}
	if err := add(`SELECT id FROM sessions WHERE user_id=? AND id IN (%s)`, scope.SessionIDs); err != nil {
		return nil, err
	}
	if err := add(`SELECT st.session_id FROM sets st JOIN sessions se ON se.id=st.session_id WHERE se.user_id=? AND st.id IN (%s)`, scope.SetIDs); err != nil {
		return nil, err
	}
	return out, nil
}

// before reads the state a change starts from, in one read transaction.
func (s *userStore) before(ctx context.Context, scope training.UndoScope) ([]int64, map[string]int64, snapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	defer tx.Rollback()
	sessions, err := s.scopeSessions(ctx, tx, scope)
	if err != nil {
		return nil, nil, nil, err
	}
	maxIDs := map[string]int64{}
	for _, t := range undoTables {
		if !t.bySession {
			continue
		}
		var max int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM `+t.name).Scan(&max); err != nil {
			return nil, nil, nil, err
		}
		maxIDs[t.name] = max
	}
	snap, err := s.capture(ctx, tx, sessions, nil)
	return sessions, maxIDs, snap, err
}

func (s *userStore) after(ctx context.Context, sessions []int64, maxIDs map[string]int64) (snapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return s.capture(ctx, tx, sessions, maxIDs)
}

// diff lists the rows that differ, in table order.
func diff(before, after snapshot) []undoChange {
	var out []undoChange
	for _, t := range undoTables {
		b, a := before[t.name], after[t.name]
		keys := map[string]bool{}
		for k := range a {
			keys[k] = true
		}
		if !t.insertOnly {
			for k := range b {
				keys[k] = true
			}
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			br, inB := b[k]
			ar, inA := a[k]
			if t.insertOnly && inB {
				continue
			}
			if inB && inA && br.sig() == ar.sig() {
				continue
			}
			c := undoChange{Table: t.name}
			if inB {
				c.Before, c.Key = br, keyOf(t, br)
			}
			if inA {
				c.After, c.Key = ar, keyOf(t, ar)
			}
			out = append(out, c)
		}
	}
	return out
}

// CaptureUndo applies a change and returns what undoing it needs.
func (s *userStore) CaptureUndo(ctx context.Context, scope training.UndoScope, apply training.ApplyFunc) (json.RawMessage, json.RawMessage, error) {
	defer s.locks.lock(s.userID)()
	sessions, maxIDs, before, err := s.before(ctx, scope)
	if err != nil {
		return nil, nil, err
	}
	result, err := apply(ctx)
	if err != nil {
		return result, nil, err
	}
	after, err := s.after(ctx, sessions, maxIDs)
	if err != nil {
		// The change stands; it just cannot be undone.
		return result, nil, nil
	}
	image, err := json.Marshal(undoImage{Version: 1, Rows: diff(before, after)})
	if err != nil {
		return result, nil, nil
	}
	return result, image, nil
}

func where(t undoTable, key undoRow) (string, []any) {
	cols := key.columns()
	parts := make([]string, 0, len(cols)+1)
	args := make([]any, 0, len(cols)+1)
	for _, c := range cols {
		parts = append(parts, c+`=?`)
		args = append(args, key[c].v)
	}
	parts = append(parts, `(`+t.owned+`)`)
	return strings.Join(parts, " AND "), args
}

func (s *userStore) currentRow(ctx context.Context, tx *sql.Tx, t undoTable, key undoRow, cols []string) (undoRow, bool, error) {
	cond, args := where(t, key)
	args = append(args, s.userID)
	rows, err := tx.QueryContext(ctx, `SELECT `+strings.Join(cols, ",")+` FROM `+t.name+` WHERE `+cond, args...)
	if err != nil {
		return nil, false, err
	}
	list, err := scanRows(rows)
	if err != nil || len(list) == 0 {
		return nil, false, err
	}
	return list[0], true, nil
}

// restoreError turns a constraint the restored rows break into the refusal it
// means: a later row holds the place of one coming back, or a row coming back
// belongs to one removed since.
func restoreError(table string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed"), strings.Contains(msg, "PRIMARY KEY constraint failed"):
		return &training.UndoConflict{Reason: training.UndoOccupied, Table: table}
	case strings.Contains(msg, "FOREIGN KEY constraint failed"):
		return &training.UndoConflict{Reason: training.UndoChanged, Table: table}
	}
	return err
}

// restore writes an image's before rows back inside tx, or refuses.
func (s *userStore) restore(ctx context.Context, tx *sql.Tx, changes []undoChange) error {
	removing := map[string]map[string]bool{}
	updating := map[string]map[string]undoRow{}
	for _, c := range changes {
		t, _, ok := undoTableNamed(c.Table)
		if !ok || len(c.Key) != len(t.key) || (c.Before == nil && c.After == nil) {
			return fmt.Errorf("undo: malformed image row for %q", c.Table)
		}
		image := c.After
		if image == nil {
			image = c.Before
		}
		current, found, err := s.currentRow(ctx, tx, t, c.Key, image.columns())
		if err != nil {
			return err
		}
		if t.insertOnly {
			// An idempotency key may have expired since; it is removed if present.
			if found && c.Before == nil {
				addTo(removing, t.name, c.Key.sig())
			}
			continue
		}
		if (c.After == nil) == found || (found && current.sig() != c.After.sig()) {
			return &training.UndoConflict{Reason: training.UndoChanged, Table: t.name}
		}
		switch {
		case c.Before == nil:
			addTo(removing, t.name, c.Key.sig())
		case c.After != nil:
			if updating[t.name] == nil {
				updating[t.name] = map[string]undoRow{}
			}
			updating[t.name][c.Key.sig()] = c.Before
		}
	}
	// Nothing added later may point at a row about to be removed, unless it is
	// removed too or the undo points it elsewhere.
	for _, c := range changes {
		t, _, _ := undoTableNamed(c.Table)
		if c.Before != nil || !removing[t.name][c.Key.sig()] || len(t.refs) == 0 {
			continue
		}
		id := c.Key["id"].v
		for _, ref := range t.refs {
			rt, _, _ := undoTableNamed(ref.table)
			rows, err := tx.QueryContext(ctx, `SELECT `+strings.Join(rt.key, ",")+`,`+ref.col+` FROM `+rt.name+` WHERE `+ref.col+`=? AND (`+rt.owned+`)`, id, s.userID)
			if err != nil {
				return err
			}
			list, err := scanRows(rows)
			if err != nil {
				return err
			}
			for _, r := range list {
				k := keyOf(rt, r).sig()
				if removing[rt.name][k] {
					continue
				}
				if before, ok := updating[rt.name][k]; ok && !sameCell(before[ref.col], cell{id}) {
					continue
				}
				return &training.UndoConflict{Reason: training.UndoDepended, Table: t.name}
			}
		}
	}
	// Remove added rows, rows of a table after the rows hanging off it.
	for i := len(changes) - 1; i >= 0; i-- {
		c := changes[i]
		t, _, _ := undoTableNamed(c.Table)
		if !removing[t.name][c.Key.sig()] {
			continue
		}
		cond, args := where(t, c.Key)
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t.name+` WHERE `+cond, append(args, s.userID)...); err != nil {
			return restoreError(t.name, err)
		}
	}
	// Set unique values aside, so rewritten rows can trade them.
	for _, c := range changes {
		t, _, _ := undoTableNamed(c.Table)
		if c.Before == nil || c.After == nil || len(t.temp) == 0 {
			continue
		}
		sets := make([]string, 0, len(t.temp))
		for col, expr := range t.temp {
			sets = append(sets, col+`=`+expr)
		}
		sort.Strings(sets)
		cond, args := where(t, c.Key)
		if _, err := tx.ExecContext(ctx, `UPDATE `+t.name+` SET `+strings.Join(sets, ",")+` WHERE `+cond, append(args, s.userID)...); err != nil {
			return restoreError(t.name, err)
		}
	}
	// Bring removed rows back, parents first, then rewrite changed ones.
	for _, c := range changes {
		if c.Before == nil || c.After != nil {
			continue
		}
		cols := c.Before.columns()
		args := make([]any, len(cols))
		for i, col := range cols {
			args[i] = c.Before[col].v
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+c.Table+`(`+strings.Join(cols, ",")+`) VALUES (`+placeholders(len(cols))+`)`, args...); err != nil {
			return restoreError(c.Table, err)
		}
	}
	for _, c := range changes {
		if c.Before == nil || c.After == nil {
			continue
		}
		t, _, _ := undoTableNamed(c.Table)
		var sets []string
		var args []any
		for _, col := range c.Before.columns() {
			if _, isKey := c.Key[col]; isKey {
				continue
			}
			sets = append(sets, col+`=?`)
			args = append(args, c.Before[col].v)
		}
		if len(sets) == 0 {
			continue
		}
		cond, keyArgs := where(t, c.Key)
		args = append(append(args, keyArgs...), s.userID)
		if _, err := tx.ExecContext(ctx, `UPDATE `+t.name+` SET `+strings.Join(sets, ",")+` WHERE `+cond, args...); err != nil {
			return restoreError(t.name, err)
		}
	}
	// Every row written back must be this user's: an image is only ever read
	// back for the account that captured it, and this proves it.
	for _, c := range changes {
		if c.Before == nil {
			continue
		}
		t, _, _ := undoTableNamed(c.Table)
		if t.insertOnly {
			continue
		}
		if _, found, err := s.currentRow(ctx, tx, t, c.Key, t.key); err != nil {
			return err
		} else if !found {
			return fmt.Errorf("undo: restored row of %s is not this account's", t.name)
		}
	}
	return nil
}

func sameCell(a, b cell) bool { return undoRow{"v": a}.sig() == undoRow{"v": b}.sig() }

func addTo(m map[string]map[string]bool, table, key string) {
	if m[table] == nil {
		m[table] = map[string]bool{}
	}
	m[table][key] = true
}

// UndoProposal undoes an accepted proposal of this user, all or nothing.
func (s *userStore) UndoProposal(ctx context.Context, id int64, entry training.Proposal, record training.DecisionRecord) (training.Proposal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.Proposal{}, err
	}
	defer tx.Rollback()
	// A write first takes SQLite's write lock before anything is read, as LogSets
	// does, so two undos of one proposal queue and the second sees the first.
	res, err := tx.ExecContext(ctx, `UPDATE proposals SET undone_by=undone_by WHERE id=? AND user_id=?`, id, s.userID)
	if err != nil {
		return training.Proposal{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return training.Proposal{}, err
	} else if n == 0 {
		return training.Proposal{}, training.ErrNotFound
	}
	var status, raw string
	var undoneBy int64
	if err := tx.QueryRowContext(ctx, `SELECT status,undo_image,undone_by FROM proposals WHERE id=? AND user_id=?`, id, s.userID).Scan(&status, &raw, &undoneBy); err != nil {
		return training.Proposal{}, err
	}
	if undoneBy != 0 {
		return training.Proposal{}, training.ErrUndone
	}
	if status != training.ProposalAccepted || raw == "" {
		return training.Proposal{}, training.ErrNotUndoable
	}
	var image undoImage
	if err := json.Unmarshal([]byte(raw), &image); err != nil || image.Version != 1 {
		return training.Proposal{}, errors.Join(errors.New("undo: unreadable image"), err)
	}
	if err := s.restore(ctx, tx, image.Rows); err != nil {
		return training.Proposal{}, err
	}
	created, err := scanProposal(tx.QueryRowContext(ctx, `INSERT INTO proposals(user_id,tool,action_type,payload,session,status,decided_by,created_at,decided_at)
		VALUES (?,?,?,?,'',?,?,?,?) RETURNING `+proposalColumns,
		s.userID, entry.Tool, entry.ActionType, string(entry.Payload), training.ProposalAccepted, entry.DecidedBy, formatTime(entry.CreatedAt), formatTime(entry.DecidedAt)))
	if err != nil {
		return training.Proposal{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE proposals SET undone_by=? WHERE id=? AND user_id=?`, created.ID, id, s.userID); err != nil {
		return training.Proposal{}, err
	}
	record.Operation = training.OperationID(created.ID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_records(user_id,time,permission,outcome,operation,session,decider_kind,decider_id,channel,answer)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		s.userID, formatTime(record.Time), record.Permission, record.Outcome, record.Operation, record.Session, record.DeciderKind, record.DeciderID, record.Channel, record.Answer); err != nil {
		return training.Proposal{}, err
	}
	if err := tx.Commit(); err != nil {
		return training.Proposal{}, err
	}
	return created, nil
}

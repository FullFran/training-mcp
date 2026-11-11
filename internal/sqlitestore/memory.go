package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fullfran/training-mcp/internal/training"
)

// memoryReturning lists a fact's columns for RETURNING, which takes no table
// qualifier; memoryColumnsOf qualifies them for joins with other tables.
const memoryReturning = `id,topic_key,type,title,body,source_chat_id,pinned,created_at,updated_at,revision_count,review_after,superseded_by,deleted_at,forget_reason,consolidated_into`

func memoryColumnsOf(alias string) string {
	fields := strings.Split(memoryReturning, ",")
	for i, f := range fields {
		fields[i] = alias + "." + f
	}
	return strings.Join(fields, ",")
}

var memoryColumns = memoryColumnsOf("m")

// memoryTime has a fixed width, so text order is time order and the recency
// index sorts correctly.
const memoryTime = "2006-01-02T15:04:05.000000000Z"

func formatMemoryTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(memoryTime)
}

func parseMemoryTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, v)
}

// memoryScan holds one fact's raw columns while scanning.
type memoryScan struct {
	m                                        training.Memory
	source                                   sql.NullString
	superseded, consolidated                 sql.NullInt64
	created, updated, reviewAfter, deletedAt string
}

func (r *memoryScan) targets() []any {
	return []any{&r.m.ID, &r.m.TopicKey, &r.m.Type, &r.m.Title, &r.m.Body, &r.source, &r.m.Pinned, &r.created, &r.updated,
		&r.m.Revision, &r.reviewAfter, &r.superseded, &r.deletedAt, &r.m.ForgetReason, &r.consolidated}
}

func (r *memoryScan) memory() (training.Memory, error) {
	m := r.m
	m.SourceChatID = r.source.String
	m.SupersededBy = r.superseded.Int64
	m.ConsolidatedInto = r.consolidated.Int64
	var err error
	for _, f := range []struct {
		dst *time.Time
		v   string
	}{{&m.CreatedAt, r.created}, {&m.UpdatedAt, r.updated}, {&m.ReviewAfter, r.reviewAfter}, {&m.DeletedAt, r.deletedAt}} {
		if *f.dst, err = parseMemoryTime(f.v); err != nil {
			return training.Memory{}, err
		}
	}
	return m, nil
}

func scanMemory(row rowScanner) (training.Memory, error) {
	var r memoryScan
	if err := row.Scan(r.targets()...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return training.Memory{}, training.ErrNotFound
		}
		return training.Memory{}, err
	}
	return r.memory()
}

func collectMemories(rows *sql.Rows) ([]training.Memory, error) {
	defer rows.Close()
	out := []training.Memory{}
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func optional[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}

// querier is what both *sql.DB and *sql.Tx offer.
type querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// withTx runs fn in a transaction that commits only if fn succeeds.
func (s *userStore) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// saveMemoryTx inserts the fact or updates the user's fact with the same topic
// key. Changed text goes to the history first and bumps the revision; saving
// brings back a forgotten or superseded fact. The first statement writes, so
// the transaction holds the write lock before it reads anything.
func (s *userStore) saveMemoryTx(ctx context.Context, q querier, m training.Memory, pinned *bool) (training.Memory, error) {
	if _, err := q.ExecContext(ctx, `INSERT INTO coach_memory_revisions(memory_id,revision,type,title,body,replaced_at)
		SELECT id,revision_count,type,title,body,? FROM coach_memory
		WHERE user_id=? AND topic_key=? AND (type<>? OR title<>? OR body<>?)`,
		formatMemoryTime(m.UpdatedAt), s.userID, m.TopicKey, m.Type, m.Title, m.Body); err != nil {
		return training.Memory{}, err
	}
	keep := pinned == nil
	value := pinned != nil && *pinned
	return scanMemory(q.QueryRowContext(ctx, `INSERT INTO coach_memory AS m (user_id,topic_key,type,title,body,source_chat_id,pinned,created_at,updated_at,review_after)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (user_id,topic_key) DO UPDATE SET
		 revision_count=m.revision_count + (m.type<>excluded.type OR m.title<>excluded.title OR m.body<>excluded.body),
		 type=excluded.type, title=excluded.title, body=excluded.body,
		 source_chat_id=COALESCE(excluded.source_chat_id, m.source_chat_id),
		 pinned=CASE WHEN ? THEN m.pinned ELSE excluded.pinned END,
		 updated_at=excluded.updated_at, review_after=excluded.review_after,
		 superseded_by=NULL, deleted_at='', forget_reason='', consolidated_into=NULL
		RETURNING `+memoryReturning,
		s.userID, m.TopicKey, m.Type, m.Title, m.Body, nullable(m.SourceChatID), value,
		formatMemoryTime(m.CreatedAt), formatMemoryTime(m.UpdatedAt), formatMemoryTime(m.ReviewAfter), keep))
}

const maxMemoryCandidates = 5

// SaveMemory saves the fact and returns the similar active facts whose relation
// to it is still to be judged, recording a pending relation for each.
func (s *userStore) SaveMemory(ctx context.Context, m training.Memory, pinned *bool) (training.MemorySaved, error) {
	var out training.MemorySaved
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		saved, err := s.saveMemoryTx(ctx, tx, m, pinned)
		if err != nil {
			return err
		}
		out.Memory = saved
		out.Candidates, err = s.memoryCandidates(ctx, tx, saved, formatMemoryTime(m.UpdatedAt))
		return err
	})
	if err != nil {
		return training.MemorySaved{}, err
	}
	return out, nil
}

// ftsQuery turns free text into a query of words only: every run of letters,
// digits and combining marks becomes a quoted prefix term, and all of them must
// match. Nothing the caller types can be read as FTS syntax (operators, columns, quotes).
func ftsQuery(text string) string {
	return strings.Join(ftsWords(text), " ")
}

func ftsWords(text string) []string {
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.Is(unicode.Mn, r) })
	for i, w := range words {
		words[i] = `"` + w + `"*`
	}
	return words
}

// ftsAnyQuery matches any word of three or more characters instead of all of
// them: the deterministic "is this about the same thing?".
func ftsAnyQuery(text string) string {
	var words []string
	for _, w := range ftsWords(text) {
		if utf8.RuneCountInString(w) >= len(`""*`)+3 {
			words = append(words, w)
		}
	}
	return strings.Join(words, " OR ")
}

// memoryCandidates finds active facts sharing words with the saved one's title,
// leaving out pairs already judged and pairs recorded the other way round.
func (s *userStore) memoryCandidates(ctx context.Context, tx *sql.Tx, saved training.Memory, at string) ([]training.MemoryCandidate, error) {
	out := []training.MemoryCandidate{}
	match := ftsAnyQuery(saved.Title)
	if match == "" {
		return out, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+memoryColumns+` FROM coach_memory_fts f JOIN coach_memory m ON m.id=f.rowid
		WHERE coach_memory_fts MATCH ? AND m.user_id=? AND m.id<>? AND m.deleted_at='' AND m.superseded_by IS NULL
		 AND NOT EXISTS (SELECT 1 FROM coach_memory_relations r WHERE r.user_id=m.user_id
		  AND ((r.memory_id=? AND r.candidate_id=m.id AND r.status<>'pending') OR (r.memory_id=m.id AND r.candidate_id=?)))
		ORDER BY bm25(coach_memory_fts), m.id LIMIT ?`, match, s.userID, saved.ID, saved.ID, saved.ID, maxMemoryCandidates)
	if err != nil {
		return nil, err
	}
	found, err := collectMemories(rows)
	if err != nil {
		return nil, err
	}
	for _, c := range found {
		var id int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO coach_memory_relations(user_id,memory_id,candidate_id,status,created_at)
			VALUES (?,?,?,'pending',?)
			ON CONFLICT (memory_id,candidate_id) DO UPDATE SET status=status RETURNING id`,
			s.userID, saved.ID, c.ID, at).Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, training.MemoryCandidate{RelationID: id, Memory: c})
	}
	return out, nil
}

// GetMemory returns the user's fact unless it is forgotten.
func (s *userStore) GetMemory(ctx context.Context, id int64) (training.Memory, error) {
	return scanMemory(s.db.QueryRowContext(ctx, `SELECT `+memoryColumns+` FROM coach_memory m WHERE m.id=? AND m.user_id=? AND m.deleted_at=''`, id, s.userID))
}

const activeMemory = `m.deleted_at='' AND m.superseded_by IS NULL`

// memoryView is each view's condition, its arguments and its order. The
// conditions are fixed text; only values travel as arguments.
func memoryView(view string, now time.Time) (cond string, args []any, order string) {
	switch view {
	case training.MemoryViewDue:
		return activeMemory + ` AND m.review_after<>'' AND m.review_after<=?`, []any{formatMemoryTime(now)}, `m.review_after, m.id`
	case training.MemoryViewSuperseded:
		return `m.deleted_at='' AND m.superseded_by IS NOT NULL`, nil, `m.updated_at DESC, m.id DESC`
	case training.MemoryViewForgotten:
		return `m.deleted_at<>''`, nil, `m.deleted_at DESC, m.id DESC`
	default:
		return activeMemory, nil, `m.pinned DESC, m.updated_at DESC, m.id DESC`
	}
}

// ListMemory lists the user's facts in a view, or, with text, the ones in that
// view matching it ranked by relevance.
func (s *userStore) ListMemory(ctx context.Context, q training.MemoryQuery) ([]training.Memory, error) {
	from := `coach_memory m`
	var conds []string
	var args []any
	viewCond, viewArgs, order := memoryView(q.View, q.Now)
	if match := ftsQuery(q.Text); match != "" {
		from = `coach_memory_fts f JOIN coach_memory m ON m.id=f.rowid`
		conds = append(conds, `coach_memory_fts MATCH ?`)
		args = append(args, match)
		order = `bm25(coach_memory_fts), ` + order
	}
	conds = append(conds, `m.user_id=?`, `(?='' OR m.type=?)`, viewCond)
	args = append(append(args, s.userID, q.Type, q.Type), viewArgs...)
	rows, err := s.db.QueryContext(ctx, `SELECT `+memoryColumns+` FROM `+from+` WHERE `+strings.Join(conds, ` AND `)+
		` ORDER BY `+order+` LIMIT ?`, append(args, q.Limit)...)
	if err != nil {
		return nil, err
	}
	return collectMemories(rows)
}

// MemoryHistory lists the earlier texts of the user's fact, forgotten or not.
func (s *userStore) MemoryHistory(ctx context.Context, id int64) ([]training.MemoryRevision, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM coach_memory WHERE id=? AND user_id=?`, id, s.userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, training.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.revision,r.type,r.title,r.body,r.replaced_at FROM coach_memory_revisions r
		JOIN coach_memory m ON m.id=r.memory_id WHERE m.id=? AND m.user_id=? ORDER BY r.revision`, id, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []training.MemoryRevision{}
	for rows.Next() {
		var r training.MemoryRevision
		var at string
		if err := rows.Scan(&r.Revision, &r.Type, &r.Title, &r.Body, &at); err != nil {
			return nil, err
		}
		if r.ReplacedAt, err = parseMemoryTime(at); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateMemory is the person's edit: changed text goes to the history first.
func (s *userStore) UpdateMemory(ctx context.Context, id int64, p training.MemoryPatch) (training.Memory, error) {
	var out training.Memory
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		kind, title, body := optional(p.Type), optional(p.Title), optional(p.Body)
		if _, err := tx.ExecContext(ctx, `INSERT INTO coach_memory_revisions(memory_id,revision,type,title,body,replaced_at)
			SELECT id,revision_count,type,title,body,? FROM coach_memory
			WHERE id=? AND user_id=? AND deleted_at='' AND (type<>COALESCE(?,type) OR title<>COALESCE(?,title) OR body<>COALESCE(?,body))`,
			formatMemoryTime(p.UpdatedAt), id, s.userID, kind, title, body); err != nil {
			return err
		}
		review := formatMemoryTime(p.ReviewAfter)
		var err error
		out, err = scanMemory(tx.QueryRowContext(ctx, `UPDATE coach_memory AS m SET
			 revision_count=m.revision_count + (m.type<>COALESCE(?,m.type) OR m.title<>COALESCE(?,m.title) OR m.body<>COALESCE(?,m.body)),
			 type=COALESCE(?,m.type), title=COALESCE(?,m.title), body=COALESCE(?,m.body), pinned=COALESCE(?,m.pinned),
			 review_after=CASE WHEN ?='' THEN m.review_after ELSE ? END, updated_at=?
			WHERE m.id=? AND m.user_id=? AND m.deleted_at='' RETURNING `+memoryReturning,
			kind, title, body, kind, title, body, optional(p.Pinned), review, review, formatMemoryTime(p.UpdatedAt), id, s.userID))
		return err
	})
	return out, err
}

// ConfirmMemory moves the review date of the user's active fact; the text and
// revision stay as they are.
func (s *userStore) ConfirmMemory(ctx context.Context, id int64, reviewAfter time.Time) (training.Memory, error) {
	return scanMemory(s.db.QueryRowContext(ctx, `UPDATE coach_memory SET review_after=?
		WHERE id=? AND user_id=? AND deleted_at='' RETURNING `+memoryReturning, formatMemoryTime(reviewAfter), id, s.userID))
}

// releaseSuperseded gives back the facts the given fact had replaced, once it
// is forgotten or deleted: nobody meant to retire them for good.
func (s *userStore) releaseSuperseded(ctx context.Context, q querier, id int64) error {
	_, err := q.ExecContext(ctx, `UPDATE coach_memory SET superseded_by=NULL WHERE user_id=? AND superseded_by=?`, s.userID, id)
	return err
}

func (s *userStore) ForgetMemory(ctx context.Context, id int64, reason string, at time.Time) (training.Memory, error) {
	var out training.Memory
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		if out, err = scanMemory(tx.QueryRowContext(ctx, `UPDATE coach_memory SET deleted_at=?, forget_reason=?
			WHERE id=? AND user_id=? AND deleted_at='' RETURNING `+memoryReturning, formatMemoryTime(at), reason, id, s.userID)); err != nil {
			return err
		}
		return s.releaseSuperseded(ctx, tx, id)
	})
	return out, err
}

// DeleteMemory removes the fact for good; its history and relations go with it,
// the facts it replaced come back, and facts merged into it keep no pointer to it.
func (s *userStore) DeleteMemory(ctx context.Context, id int64) (training.Memory, error) {
	var out training.Memory
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		if out, err = scanMemory(tx.QueryRowContext(ctx, `DELETE FROM coach_memory WHERE id=? AND user_id=? RETURNING `+memoryReturning, id, s.userID)); err != nil {
			return err
		}
		if err = s.releaseSuperseded(ctx, tx, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE coach_memory SET consolidated_into=NULL WHERE user_id=? AND consolidated_into=?`, s.userID, id)
		return err
	})
	return out, err
}

func (s *userStore) DeleteAllMemory(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM coach_memory WHERE user_id=?`, s.userID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

const relationColumns = `id,memory_id,candidate_id,relation,reason,status,created_at,judged_at`

type relationScan struct {
	r               training.MemoryRelation
	created, judged string
}

func (r *relationScan) targets() []any {
	return []any{&r.r.ID, &r.r.MemoryID, &r.r.CandidateID, &r.r.Relation, &r.r.Reason, &r.r.Status, &r.created, &r.judged}
}

func (r *relationScan) relation() (training.MemoryRelation, error) {
	out := r.r
	var err error
	if out.CreatedAt, err = parseMemoryTime(r.created); err != nil {
		return training.MemoryRelation{}, err
	}
	out.JudgedAt, err = parseMemoryTime(r.judged)
	return out, err
}

// relationRefused tells a relation that is not the user's (not found) from one
// that is but is no longer open (not pending).
func (s *userStore) relationRefused(ctx context.Context, q querier, id int64) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM coach_memory_relations WHERE id=? AND user_id=?`, id, s.userID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return training.ErrNotFound
	case err != nil:
		return err
	}
	return training.ErrNotPending
}

// JudgeMemory records the coach's verdict on a pending relation. Supersedes
// retires the older fact at once.
func (s *userStore) JudgeMemory(ctx context.Context, id int64, relation, reason string, at time.Time) (training.MemoryRelation, error) {
	var out training.MemoryRelation
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var r relationScan
		err := tx.QueryRowContext(ctx, `UPDATE coach_memory_relations SET relation=?, reason=?, status='judged', judged_at=?
			WHERE id=? AND user_id=? AND status='pending' RETURNING `+relationColumns,
			relation, reason, formatMemoryTime(at), id, s.userID).Scan(r.targets()...)
		if errors.Is(err, sql.ErrNoRows) {
			return s.relationRefused(ctx, tx, id)
		}
		if err != nil {
			return err
		}
		if out, err = r.relation(); err != nil || relation != training.RelationSupersedes {
			return err
		}
		// Only a fact that is not forgotten can retire another.
		_, err = tx.ExecContext(ctx, `UPDATE coach_memory SET superseded_by=? WHERE id=? AND user_id=? AND deleted_at=''
			AND EXISTS (SELECT 1 FROM coach_memory WHERE id=? AND user_id=? AND deleted_at='')`,
			out.MemoryID, out.CandidateID, s.userID, out.MemoryID, s.userID)
		return err
	})
	return out, err
}

// ListMemoryConflicts lists judged conflicts between two facts that are both
// still active, newest first.
func (s *userStore) ListMemoryConflicts(ctx context.Context) ([]training.MemoryConflict, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.memory_id,r.candidate_id,r.relation,r.reason,r.status,r.created_at,r.judged_at,`+
		memoryColumnsOf("n")+`,`+memoryColumnsOf("o")+`
		FROM coach_memory_relations r
		JOIN coach_memory n ON n.id=r.memory_id AND n.user_id=r.user_id
		JOIN coach_memory o ON o.id=r.candidate_id AND o.user_id=r.user_id
		WHERE r.user_id=? AND r.relation='conflicts_with' AND r.status='judged'
		 AND n.deleted_at='' AND n.superseded_by IS NULL AND o.deleted_at='' AND o.superseded_by IS NULL
		ORDER BY r.id DESC`, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []training.MemoryConflict{}
	for rows.Next() {
		var r relationScan
		var n, o memoryScan
		if err := rows.Scan(append(append(r.targets(), n.targets()...), o.targets()...)...); err != nil {
			return nil, err
		}
		var c training.MemoryConflict
		if c.Relation, err = r.relation(); err != nil {
			return nil, err
		}
		if c.New, err = n.memory(); err != nil {
			return nil, err
		}
		if c.Old, err = o.memory(); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResolveMemoryConflict closes a judged conflict as the person decides: the
// fact not kept is superseded by the one kept; keeping both changes neither.
func (s *userStore) ResolveMemoryConflict(ctx context.Context, id int64, keep string, at time.Time) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var newID, oldID int64
		err := tx.QueryRowContext(ctx, `UPDATE coach_memory_relations SET status='resolved', judged_at=?
			WHERE id=? AND user_id=? AND relation='conflicts_with' AND status='judged' RETURNING memory_id,candidate_id`,
			formatMemoryTime(at), id, s.userID).Scan(&newID, &oldID)
		if errors.Is(err, sql.ErrNoRows) {
			return s.relationRefused(ctx, tx, id)
		}
		if err != nil {
			return err
		}
		switch keep {
		case training.KeepNew:
		case training.KeepOld:
			newID, oldID = oldID, newID
		default:
			return nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE coach_memory SET superseded_by=? WHERE id=? AND user_id=? AND deleted_at=''`, newID, oldID, s.userID)
		return err
	})
}

// ConsolidateMemory merges the sources into the survivor, all or nothing. Each
// source must still be the user's, not forgotten and at the revision the plan
// saw. A survivor whose key is a source's updates that fact, which then stays.
func (s *userStore) ConsolidateMemory(ctx context.Context, c training.MemoryConsolidation) (training.Memory, error) {
	var out training.Memory
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		// A write first takes the lock, so no save slips in between the checks
		// and the merge.
		if _, err := tx.ExecContext(ctx, `UPDATE coach_memory SET id=id WHERE 0`); err != nil {
			return err
		}
		for id, revision := range c.Sources {
			var current int
			err := tx.QueryRowContext(ctx, `SELECT revision_count FROM coach_memory WHERE id=? AND user_id=? AND deleted_at=''`, id, s.userID).Scan(&current)
			if errors.Is(err, sql.ErrNoRows) || err == nil && current != revision {
				return training.ErrStale
			}
			if err != nil {
				return err
			}
		}
		// The survivor may take a source's key or a forgotten fact's, never the
		// key of another active fact, which it would silently overwrite.
		var taken int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM coach_memory WHERE user_id=? AND topic_key=? AND deleted_at=''`, s.userID, c.Survivor.TopicKey).Scan(&taken)
		if _, isSource := c.Sources[taken]; err == nil && !isSource {
			return training.ErrValidation
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		pinned := c.Survivor.Pinned
		survivor, err := s.saveMemoryTx(ctx, tx, c.Survivor, &pinned)
		if err != nil {
			return err
		}
		for id := range c.Sources {
			if id == survivor.ID {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE coach_memory SET deleted_at=?, forget_reason=?, consolidated_into=? WHERE id=? AND user_id=?`,
				formatMemoryTime(c.At), training.MemoryConsolidated, survivor.ID, id, s.userID); err != nil {
				return err
			}
			// What a source replaced stays replaced, now by the survivor.
			if _, err := tx.ExecContext(ctx, `UPDATE coach_memory SET superseded_by=? WHERE user_id=? AND superseded_by=? AND id<>?`,
				survivor.ID, s.userID, id, survivor.ID); err != nil {
				return err
			}
		}
		out = survivor
		return nil
	})
	return out, err
}

package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/fullfran/training-mcp/internal/training"
)

// memoryReturning lists a fact's columns for RETURNING, which takes no table
// qualifier; memoryColumns qualifies them for joins with the full-text table.
const (
	memoryReturning = `id,topic_key,type,title,body,source_chat_id,pinned,created_at,updated_at`
	memoryColumns   = `m.id,m.topic_key,m.type,m.title,m.body,m.source_chat_id,m.pinned,m.created_at,m.updated_at`
)

// memoryTime has a fixed width, so text order is time order and the recency
// index sorts correctly.
const memoryTime = "2006-01-02T15:04:05.000000000Z"

func formatMemoryTime(t time.Time) string { return t.UTC().Format(memoryTime) }

func scanMemory(row rowScanner) (training.Memory, error) {
	var m training.Memory
	var source sql.NullString
	var created, updated string
	if err := row.Scan(&m.ID, &m.TopicKey, &m.Type, &m.Title, &m.Body, &source, &m.Pinned, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return training.Memory{}, training.ErrNotFound
		}
		return training.Memory{}, err
	}
	m.SourceChatID = source.String
	var err error
	if m.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return training.Memory{}, err
	}
	if m.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return training.Memory{}, err
	}
	return m, nil
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// SaveMemory inserts a fact or updates the user's fact with the same topic key.
// The conflict target includes user_id, so a key can only ever match the user's own row.
func (s *userStore) SaveMemory(ctx context.Context, m training.Memory, pinned *bool) (training.Memory, error) {
	keep := pinned == nil
	value := pinned != nil && *pinned
	return scanMemory(s.db.QueryRowContext(ctx, `INSERT INTO coach_memory AS m (user_id,topic_key,type,title,body,source_chat_id,pinned,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (user_id,topic_key) DO UPDATE SET
		 type=excluded.type, title=excluded.title, body=excluded.body,
		 source_chat_id=COALESCE(excluded.source_chat_id, m.source_chat_id),
		 pinned=CASE WHEN ? THEN m.pinned ELSE excluded.pinned END,
		 updated_at=excluded.updated_at
		RETURNING `+memoryReturning,
		s.userID, m.TopicKey, m.Type, m.Title, m.Body, nullable(m.SourceChatID), value,
		formatMemoryTime(m.CreatedAt), formatMemoryTime(m.UpdatedAt), keep))
}

func (s *userStore) GetMemory(ctx context.Context, id int64) (training.Memory, error) {
	return scanMemory(s.db.QueryRowContext(ctx, `SELECT `+memoryColumns+` FROM coach_memory m WHERE m.id=? AND m.user_id=?`, id, s.userID))
}

// ftsQuery turns free text into a query of words only: every run of letters,
// digits and combining marks becomes a quoted prefix term, and all of them must match. Nothing the
// caller types can be read as FTS syntax (operators, columns, quotes).
func ftsQuery(text string) string {
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.Is(unicode.Mn, r) })
	for i, w := range words {
		words[i] = `"` + w + `"*`
	}
	return strings.Join(words, " ")
}

// ListMemory lists the user's facts, pinned first and then the most recent, or,
// with text, the ones matching it ranked by relevance.
func (s *userStore) ListMemory(ctx context.Context, q training.MemoryQuery) ([]training.Memory, error) {
	var rows *sql.Rows
	var err error
	if match := ftsQuery(q.Text); match != "" {
		rows, err = s.db.QueryContext(ctx, `SELECT `+memoryColumns+` FROM coach_memory_fts f JOIN coach_memory m ON m.id=f.rowid
			WHERE coach_memory_fts MATCH ? AND m.user_id=? AND (?='' OR m.type=?)
			ORDER BY bm25(coach_memory_fts), m.updated_at DESC, m.id DESC LIMIT ?`, match, s.userID, q.Type, q.Type, q.Limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT `+memoryColumns+` FROM coach_memory m
			WHERE m.user_id=? AND (?='' OR m.type=?)
			ORDER BY m.pinned DESC, m.updated_at DESC, m.id DESC LIMIT ?`, s.userID, q.Type, q.Type, q.Limit)
	}
	if err != nil {
		return nil, err
	}
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

func optional[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}

func (s *userStore) UpdateMemory(ctx context.Context, id int64, p training.MemoryPatch) (training.Memory, error) {
	return scanMemory(s.db.QueryRowContext(ctx, `UPDATE coach_memory AS m SET
		 type=COALESCE(?,m.type), title=COALESCE(?,m.title), body=COALESCE(?,m.body), pinned=COALESCE(?,m.pinned), updated_at=?
		WHERE m.id=? AND m.user_id=? RETURNING `+memoryReturning,
		optional(p.Type), optional(p.Title), optional(p.Body), optional(p.Pinned), formatMemoryTime(p.UpdatedAt), id, s.userID))
}

func (s *userStore) DeleteMemory(ctx context.Context, id int64) (training.Memory, error) {
	return scanMemory(s.db.QueryRowContext(ctx, `DELETE FROM coach_memory AS m WHERE m.id=? AND m.user_id=? RETURNING `+memoryReturning, id, s.userID))
}

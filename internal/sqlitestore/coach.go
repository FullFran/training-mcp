package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/fullfran/training-mcp/internal/engine"
)

// The coach's conversations. Times are Unix milliseconds; every statement
// names the person, so one person's conversation is invisible to another.

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

func (s *Store) CreateCoachConversation(ctx context.Context, userID int64, title string, at time.Time) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO coach_conversations(user_id,title,created_at,updated_at) VALUES (?,?,?,?) RETURNING id`,
		userID, title, ms(at), ms(at)).Scan(&id)
	return id, err
}

// AppendCoachMessage adds a message and moves the conversation's last activity.
func (s *Store) AppendCoachMessage(ctx context.Context, userID, convID int64, m engine.ConversationMessage) error {
	steps := m.Steps
	if steps == nil {
		steps = []string{}
	}
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE coach_conversations SET updated_at=MAX(updated_at,?) WHERE id=? AND user_id=?`, ms(m.At), convID, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return engine.ErrNoConversation
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO coach_messages(conversation_id,role,origin,body,steps,photos,created_at) VALUES (?,?,?,?,?,?,?)`,
		convID, m.Role, m.Origin, m.Text, string(stepsJSON), m.Photos, ms(m.At)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CoachConversations(ctx context.Context, userID int64, since time.Time) ([]engine.Conversation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,title,updated_at FROM coach_conversations
		WHERE user_id=? AND updated_at>=? ORDER BY updated_at DESC, id DESC`, userID, ms(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []engine.Conversation{}
	for rows.Next() {
		var c engine.Conversation
		var at int64
		if err := rows.Scan(&c.ID, &c.Title, &at); err != nil {
			return nil, err
		}
		c.UpdatedAt = fromMS(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CoachConversation(ctx context.Context, userID, convID int64, since time.Time) (engine.Conversation, []engine.ConversationMessage, error) {
	c := engine.Conversation{ID: convID}
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT title,updated_at FROM coach_conversations WHERE id=? AND user_id=? AND updated_at>=?`,
		convID, userID, ms(since)).Scan(&c.Title, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil, engine.ErrNoConversation
	}
	if err != nil {
		return c, nil, err
	}
	c.UpdatedAt = fromMS(at)
	rows, err := s.db.QueryContext(ctx, `SELECT role,origin,body,steps,photos,created_at FROM coach_messages
		WHERE conversation_id=? ORDER BY created_at, id`, convID)
	if err != nil {
		return c, nil, err
	}
	defer rows.Close()
	msgs := []engine.ConversationMessage{}
	for rows.Next() {
		var m engine.ConversationMessage
		var steps string
		var created int64
		if err := rows.Scan(&m.Role, &m.Origin, &m.Text, &steps, &m.Photos, &created); err != nil {
			return c, nil, err
		}
		_ = json.Unmarshal([]byte(steps), &m.Steps)
		m.At = fromMS(created)
		msgs = append(msgs, m)
	}
	return c, msgs, rows.Err()
}

func (s *Store) RenameCoachConversation(ctx context.Context, userID, convID int64, title string) error {
	return oneRow(s.db.ExecContext(ctx, `UPDATE coach_conversations SET title=? WHERE id=? AND user_id=?`, title, convID, userID))
}

func (s *Store) DeleteCoachConversation(ctx context.Context, userID, convID int64) error {
	return oneRow(s.db.ExecContext(ctx, `DELETE FROM coach_conversations WHERE id=? AND user_id=?`, convID, userID))
}

func (s *Store) DeleteCoachConversations(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM coach_conversations WHERE user_id=?`, userID)
	return err
}

func (s *Store) PurgeCoachConversations(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM coach_conversations WHERE updated_at<?`, ms(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func oneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return engine.ErrNoConversation
	}
	return nil
}

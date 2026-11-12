package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

const proposalColumns = `id,tool,action_type,payload,session,status,decided_by,result,error,created_at,decided_at,
	(status='accepted' AND undo_image<>'' AND undone_by=0),undone_by`

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, v)
}

type rowScanner interface{ Scan(...any) error }

func scanProposal(row rowScanner) (training.Proposal, error) {
	var p training.Proposal
	var payload, result, created, decided string
	if err := row.Scan(&p.ID, &p.Tool, &p.ActionType, &payload, &p.Session, &p.Status, &p.DecidedBy, &result, &p.Error, &created, &decided, &p.Undoable, &p.UndoneBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return training.Proposal{}, training.ErrNotFound
		}
		return training.Proposal{}, err
	}
	p.Payload = json.RawMessage(payload)
	if result != "" {
		p.Result = json.RawMessage(result)
	}
	var err error
	if p.CreatedAt, err = parseTime(created); err != nil {
		return training.Proposal{}, err
	}
	if p.DecidedAt, err = parseTime(decided); err != nil {
		return training.Proposal{}, err
	}
	return p, nil
}

func (s *userStore) CreateProposal(ctx context.Context, p training.Proposal) (training.Proposal, error) {
	row := s.db.QueryRowContext(ctx, `INSERT INTO proposals(user_id,tool,action_type,payload,session,status,created_at)
		VALUES (?,?,?,?,?,?,?) RETURNING `+proposalColumns,
		s.userID, p.Tool, p.ActionType, string(p.Payload), p.Session, training.ProposalPending, formatTime(p.CreatedAt))
	return scanProposal(row)
}

func (s *userStore) GetProposal(ctx context.Context, id int64) (training.Proposal, error) {
	return scanProposal(s.db.QueryRowContext(ctx, `SELECT `+proposalColumns+` FROM proposals WHERE id=? AND user_id=?`, id, s.userID))
}

// ListProposals returns pending proposals oldest first, the order they were
// asked in, or answered ones newest first.
func (s *userStore) ListProposals(ctx context.Context, f training.ProposalFilter) ([]training.Proposal, error) {
	query := `SELECT ` + proposalColumns + ` FROM proposals WHERE user_id=? AND status<>'pending' ORDER BY id DESC LIMIT ?`
	if f.Pending {
		query = `SELECT ` + proposalColumns + ` FROM proposals WHERE user_id=? AND status='pending' ORDER BY id LIMIT ?`
	}
	rows, err := s.db.QueryContext(ctx, query, s.userID, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []training.Proposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// notPendingOrMissing tells apart a proposal already answered from one that is
// not this user's, which answers exactly as a missing one.
func (s *userStore) notPendingOrMissing(ctx context.Context, id int64) error {
	if _, err := s.GetProposal(ctx, id); err != nil {
		return err
	}
	return training.ErrNotPending
}

func (s *userStore) ClaimProposal(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE proposals SET status='applying' WHERE id=? AND user_id=? AND status='pending'`, id, s.userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return err
	}
	return s.notPendingOrMissing(ctx, id)
}

func (s *userStore) FinishProposal(ctx context.Context, id int64, out training.ProposalOutcome) (training.Proposal, error) {
	if out.Status != training.ProposalAccepted && out.Status != training.ProposalFailed {
		return training.Proposal{}, training.ErrValidation
	}
	image := ""
	if out.Status == training.ProposalAccepted {
		image = string(out.UndoImage)
	}
	row := s.db.QueryRowContext(ctx, `UPDATE proposals SET status=?, decided_by=?, result=?, error=?, decided_at=?, undo_image=?
		WHERE id=? AND user_id=? AND status='applying' RETURNING `+proposalColumns,
		out.Status, out.DecidedBy, string(out.Result), out.Error, formatTime(out.At), image, id, s.userID)
	p, err := scanProposal(row)
	if errors.Is(err, training.ErrNotFound) {
		return training.Proposal{}, s.notPendingOrMissing(ctx, id)
	}
	return p, err
}

func (s *userStore) RejectProposal(ctx context.Context, id int64, decidedBy string, at time.Time) (training.Proposal, error) {
	row := s.db.QueryRowContext(ctx, `UPDATE proposals SET status='rejected', decided_by=?, decided_at=?
		WHERE id=? AND user_id=? AND status='pending' RETURNING `+proposalColumns,
		decidedBy, formatTime(at), id, s.userID)
	p, err := scanProposal(row)
	if errors.Is(err, training.ErrNotFound) {
		return training.Proposal{}, s.notPendingOrMissing(ctx, id)
	}
	return p, err
}

func (s *userStore) Permissions(ctx context.Context) (training.Permissions, error) {
	var perms training.Permissions
	err := s.db.QueryRowContext(ctx, `SELECT accept_all FROM agent_permissions WHERE user_id=?`, s.userID).Scan(&perms.AcceptAll)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return training.Permissions{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+ruleColumns+` FROM answer_rules WHERE user_id=? ORDER BY action_type, session, effect`, s.userID)
	if err != nil {
		return training.Permissions{}, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return training.Permissions{}, err
		}
		perms.Rules = append(perms.Rules, r)
	}
	return perms, rows.Err()
}

const ruleColumns = `id,action_type,effect,session,expires_at,origin`

func scanRule(row rowScanner) (training.AnswerRule, error) {
	var r training.AnswerRule
	var expires string
	if err := row.Scan(&r.ID, &r.ActionType, &r.Effect, &r.Session, &expires, &r.Origin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return training.AnswerRule{}, training.ErrNotFound
		}
		return training.AnswerRule{}, err
	}
	var err error
	r.ExpiresAt, err = parseTime(expires)
	return r, err
}

func (s *userStore) SetAcceptAll(ctx context.Context, on bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_permissions(user_id,accept_all,updated_at) VALUES (?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET accept_all=excluded.accept_all, updated_at=excluded.updated_at`,
		s.userID, on, formatTime(time.Now()))
	return err
}

// AddRule stores a grant or denial. A newer answer of the same effect in the
// same session replaces the older one, so its window and origin are the latest.
func (s *userStore) AddRule(ctx context.Context, r training.AnswerRule) (int64, error) {
	if (r.Effect != training.EffectAllow && r.Effect != training.EffectDeny) || r.Session == "" || r.ActionType == "" || r.ExpiresAt.IsZero() || r.Origin <= 0 {
		return 0, training.ErrValidation
	}
	// The origin must be one of this account's own records.
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO answer_rules(user_id,action_type,effect,session,expires_at,origin,created_at)
		SELECT ?,?,?,?,?,id,? FROM decision_records WHERE id=? AND user_id=?
		ON CONFLICT(user_id,action_type,effect,session) DO UPDATE SET expires_at=excluded.expires_at, origin=excluded.origin, created_at=excluded.created_at
		RETURNING id`,
		s.userID, r.ActionType, r.Effect, r.Session, formatTime(r.ExpiresAt), formatTime(time.Now()), r.Origin, s.userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, training.ErrValidation
	}
	return id, err
}

func (s *userStore) ForgetRule(ctx context.Context, id int64) (training.AnswerRule, error) {
	return scanRule(s.db.QueryRowContext(ctx, `DELETE FROM answer_rules WHERE id=? AND user_id=? RETURNING `+ruleColumns, id, s.userID))
}

func (s *userStore) RecordDecision(ctx context.Context, d training.DecisionRecord) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO decision_records(user_id,time,permission,outcome,operation,session,decider_kind,decider_id,channel,answer)
		VALUES (?,?,?,?,?,?,?,?,?,?) RETURNING id`,
		s.userID, formatTime(d.Time), d.Permission, d.Outcome, d.Operation, d.Session, d.DeciderKind, d.DeciderID, d.Channel, d.Answer).Scan(&id)
	return id, err
}

func (s *userStore) Decisions(ctx context.Context, limit int) ([]training.DecisionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,time,permission,outcome,operation,session,decider_kind,decider_id,channel,answer
		FROM decision_records WHERE user_id=? ORDER BY id DESC LIMIT ?`, s.userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []training.DecisionRecord{}
	for rows.Next() {
		var d training.DecisionRecord
		var at string
		if err := rows.Scan(&d.ID, &at, &d.Permission, &d.Outcome, &d.Operation, &d.Session, &d.DeciderKind, &d.DeciderID, &d.Channel, &d.Answer); err != nil {
			return nil, err
		}
		if d.Time, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

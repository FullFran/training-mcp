package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

const proposalColumns = `id,tool,action_type,payload,status,decided_by,result,error,created_at,decided_at`

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
	if err := row.Scan(&p.ID, &p.Tool, &p.ActionType, &payload, &p.Status, &p.DecidedBy, &result, &p.Error, &created, &decided); err != nil {
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
	row := s.db.QueryRowContext(ctx, `INSERT INTO proposals(user_id,tool,action_type,payload,status,created_at)
		VALUES (?,?,?,?,?,?) RETURNING `+proposalColumns,
		s.userID, p.Tool, p.ActionType, string(p.Payload), training.ProposalPending, formatTime(p.CreatedAt))
	return scanProposal(row)
}

func (s *userStore) GetProposal(ctx context.Context, id int64) (training.Proposal, error) {
	return scanProposal(s.db.QueryRowContext(ctx, `SELECT `+proposalColumns+` FROM proposals WHERE id=? AND user_id=?`, id, s.userID))
}

// ListProposals returns pending proposals oldest first, the order they were
// asked in, or answered ones newest first.
func (s *userStore) ListProposals(ctx context.Context, f training.ProposalFilter) ([]training.Proposal, error) {
	query := `SELECT ` + proposalColumns + ` FROM proposals WHERE user_id=? AND status NOT IN ('pending','applying') ORDER BY id DESC LIMIT ?`
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
	row := s.db.QueryRowContext(ctx, `UPDATE proposals SET status=?, decided_by=?, result=?, error=?, decided_at=?
		WHERE id=? AND user_id=? AND status='applying' RETURNING `+proposalColumns,
		out.Status, out.DecidedBy, string(out.Result), out.Error, formatTime(out.At), id, s.userID)
	p, err := scanProposal(row)
	if errors.Is(err, training.ErrNotFound) {
		return training.Proposal{}, s.notPendingOrMissing(ctx, id)
	}
	return p, err
}

func (s *userStore) RejectProposal(ctx context.Context, id int64, at time.Time) (training.Proposal, error) {
	row := s.db.QueryRowContext(ctx, `UPDATE proposals SET status='rejected', decided_by=?, decided_at=?
		WHERE id=? AND user_id=? AND status='pending' RETURNING `+proposalColumns,
		training.DecidedByPerson, formatTime(at), id, s.userID)
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
	rows, err := s.db.QueryContext(ctx, `SELECT action_type,remember,expires_at FROM approval_rules WHERE user_id=? ORDER BY action_type`, s.userID)
	if err != nil {
		return training.Permissions{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var r training.ApprovalRule
		var expires string
		if err := rows.Scan(&r.ActionType, &r.Remember, &expires); err != nil {
			return training.Permissions{}, err
		}
		if r.ExpiresAt, err = parseTime(expires); err != nil {
			return training.Permissions{}, err
		}
		perms.Rules = append(perms.Rules, r)
	}
	return perms, rows.Err()
}

func (s *userStore) SetAcceptAll(ctx context.Context, on bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_permissions(user_id,accept_all,updated_at) VALUES (?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET accept_all=excluded.accept_all, updated_at=excluded.updated_at`,
		s.userID, on, formatTime(time.Now()))
	return err
}

func (s *userStore) SetRule(ctx context.Context, r training.ApprovalRule) error {
	if r.Remember != training.RememberSession && r.Remember != training.RememberAlways {
		return training.ErrValidation
	}
	if r.Remember == training.RememberAlways {
		r.ExpiresAt = time.Time{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO approval_rules(user_id,action_type,remember,expires_at,created_at) VALUES (?,?,?,?,?)
		ON CONFLICT(user_id,action_type) DO UPDATE SET remember=excluded.remember, expires_at=excluded.expires_at, created_at=excluded.created_at`,
		s.userID, r.ActionType, r.Remember, formatTime(r.ExpiresAt), formatTime(time.Now()))
	return err
}

func (s *userStore) ForgetRule(ctx context.Context, actionType string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM approval_rules WHERE user_id=? AND action_type=?`, s.userID, actionType)
	return err
}

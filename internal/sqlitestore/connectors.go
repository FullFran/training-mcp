package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// Every connector query names the person: a connector of someone else is
// ErrNotFound, exactly like one that does not exist.

const connectorColumns = `id,slug,label,url,auth,state,created_at`

func (s *Store) scanConnector(row rowScanner) (training.Connector, error) {
	var c training.Connector
	var created string
	if err := row.Scan(&c.ID, &c.Slug, &c.Label, &c.URL, &c.Auth, &c.State, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return training.Connector{}, training.ErrNotFound
		}
		return training.Connector{}, err
	}
	t, err := parseTime(created)
	if err != nil {
		return training.Connector{}, err
	}
	c.CreatedAt = t
	return c, nil
}

// ListConnectors returns the person's connectors, oldest first.
func (s *Store) ListConnectors(ctx context.Context, userID int64) ([]training.Connector, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+connectorColumns+` FROM connectors WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	var out []training.Connector
	for rows.Next() {
		c, err := s.scanConnector(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	return out, rows.Err()
}

func (s *Store) Connector(ctx context.Context, userID, id int64) (training.Connector, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+connectorColumns+` FROM connectors WHERE user_id=? AND id=?`, userID, id)
	return s.scanConnector(row)
}

func (s *Store) ConnectorBySlug(ctx context.Context, userID int64, slug string) (training.Connector, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+connectorColumns+` FROM connectors WHERE user_id=? AND slug=?`, userID, slug)
	return s.scanConnector(row)
}

// CreateConnector adds a connector for the person, at most MaxConnectors each.
func (s *Store) CreateConnector(ctx context.Context, userID int64, c training.Connector) (training.Connector, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.Connector{}, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM connectors WHERE user_id=?`, userID).Scan(&n); err != nil {
		return training.Connector{}, err
	}
	if n >= training.MaxConnectors {
		return training.Connector{}, training.ErrTooManyConnectors
	}
	created := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `INSERT INTO connectors(user_id,slug,label,url,auth,state,created_at) VALUES (?,?,?,?,?,?,?)`,
		userID, c.Slug, c.Label, c.URL, c.Auth, c.State, formatTime(created))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return training.Connector{}, training.ErrConnectorExists
		}
		return training.Connector{}, err
	}
	if err := tx.Commit(); err != nil {
		return training.Connector{}, err
	}
	c.ID, _ = res.LastInsertId()
	c.CreatedAt = created
	return c, nil
}

func (s *Store) exec(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return training.ErrNotFound
	}
	return nil
}

func (s *Store) SetConnectorState(ctx context.Context, userID, id int64, auth, state string) error {
	return s.exec(ctx, `UPDATE connectors SET auth=?, state=? WHERE user_id=? AND id=?`, auth, state, userID, id)
}

func (s *Store) DeleteConnector(ctx context.Context, userID, id int64) error {
	return s.exec(ctx, `DELETE FROM connectors WHERE user_id=? AND id=?`, userID, id)
}

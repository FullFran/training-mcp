package sqlitestore

import (
	"context"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

// keyBatch keeps each IN list well under SQLite's bound-parameter limit.
const keyBatch = 500

func (s *userStore) ImportedSetKeys(ctx context.Context, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	for start := 0; start < len(keys); start += keyBatch {
		batch := keys[start:min(start+keyBatch, len(keys))]
		args := make([]any, 0, len(batch)+1)
		args = append(args, s.userID)
		for _, k := range batch {
			args = append(args, k)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT natural_key FROM imported_sets WHERE user_id=? AND natural_key IN (?`+strings.Repeat(",?", len(batch)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k string
			if err = rows.Scan(&k); err != nil {
				rows.Close()
				return nil, err
			}
			out[k] = true
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *userStore) DaySets(ctx context.Context, from, to string) ([]training.DaySet, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT se.date,st.exercise,st.weight_kg,st.reps FROM sets st JOIN sessions se ON se.id=st.session_id
WHERE se.user_id=? AND se.date BETWEEN ? AND ? ORDER BY se.date,se.id,st.position`, s.userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.DaySet
	for rows.Next() {
		var d training.DaySet
		if err = rows.Scan(&d.Date, &d.Exercise, &d.WeightKG, &d.Reps); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ExerciseNames gathers every name the person has given an exercise: in sets,
// plans, muscle groups and setup notes.
func (s *userStore) ExerciseNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT st.exercise FROM sets st JOIN sessions se ON se.id=st.session_id WHERE se.user_id=?1
UNION SELECT i.exercise FROM plan_items i JOIN plans p ON p.id=i.plan_id WHERE p.user_id=?1
UNION SELECT exercise FROM exercise_groups WHERE user_id=?1
UNION SELECT exercise FROM exercise_notes WHERE user_id=?1
ORDER BY 1`, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *userStore) ImportMappings(ctx context.Context, source string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source_name,exercise FROM import_exercise_map WHERE user_id=? AND source=?`, s.userID, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var from, to string
		if err = rows.Scan(&from, &to); err != nil {
			return nil, err
		}
		out[from] = to
	}
	return out, rows.Err()
}

// ApplyImport records an import in one transaction. Its first statement is a
// write, as in LogSets, so two confirmations of the same file queue rather than
// both reading the keys as new. A set is recorded only when its key is claimed
// here; a workout gets a session only once one of its sets is.
func (s *userStore) ApplyImport(ctx context.Context, in training.ImportRecord) (training.ImportSummary, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return training.ImportSummary{}, err
	}
	defer tx.Rollback()
	created := in.CreatedAt.UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, `INSERT INTO imports(user_id,source,file_name,content_hash,sessions,sets,imputed,created_at) VALUES (?,?,?,?,0,0,0,?)`,
		s.userID, in.Source, in.FileName, in.ContentHash, created)
	if err != nil {
		return training.ImportSummary{}, err
	}
	out := training.ImportSummary{Source: in.Source, FileName: in.FileName, ContentHash: in.ContentHash}
	if out.ID, err = res.LastInsertId(); err != nil {
		return training.ImportSummary{}, err
	}
	if out.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
		return training.ImportSummary{}, err
	}
	for _, w := range in.Workouts {
		var sessionID int64
		for _, set := range w.Sets {
			claimed, err := tx.ExecContext(ctx, `INSERT INTO imported_sets(user_id,natural_key,import_id) VALUES (?,?,?) ON CONFLICT(user_id,natural_key) DO NOTHING`,
				s.userID, set.Key, out.ID)
			if err != nil {
				return training.ImportSummary{}, err
			}
			if n, err := claimed.RowsAffected(); err != nil {
				return training.ImportSummary{}, err
			} else if n == 0 {
				continue
			}
			if sessionID == 0 {
				started, err := tx.ExecContext(ctx, `INSERT INTO sessions(user_id,date,started_at,plan_id,plan_name,notes) VALUES (?,?,?,NULL,'',?)`,
					s.userID, w.Date, w.StartedAt, w.Notes)
				if err != nil {
					return training.ImportSummary{}, err
				}
				if sessionID, err = started.LastInsertId(); err != nil {
					return training.ImportSummary{}, err
				}
				out.Sessions++
			}
			stored, _, err := insertSet(ctx, tx, s.userID, training.AddSetInput{SessionID: sessionID, Exercise: set.Exercise,
				WeightKG: set.WeightKG, Reps: set.Reps, RPE: set.RPE, RPEImputed: set.RPEImputed, Technique: set.Technique})
			if err != nil {
				return training.ImportSummary{}, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE imported_sets SET set_id=? WHERE user_id=? AND natural_key=?`, stored.ID, s.userID, set.Key); err != nil {
				return training.ImportSummary{}, err
			}
			out.Sets++
			if set.RPEImputed {
				out.Imputed++
			}
		}
	}
	if out.Sets == 0 {
		return training.ImportSummary{}, nil
	}
	if s.failpoint != nil {
		if err = s.failpoint("import-inserted"); err != nil {
			return training.ImportSummary{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE imports SET sessions=?,sets=?,imputed=? WHERE id=?`, out.Sessions, out.Sets, out.Imputed, out.ID); err != nil {
		return training.ImportSummary{}, err
	}
	for from, to := range in.Mappings {
		if _, err = tx.ExecContext(ctx, `INSERT INTO import_exercise_map(user_id,source,source_name,exercise,updated_at) VALUES (?,?,?,?,?)
ON CONFLICT(user_id,source,source_name) DO UPDATE SET exercise=excluded.exercise, updated_at=excluded.updated_at`,
			s.userID, in.Source, from, to, created); err != nil {
			return training.ImportSummary{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return training.ImportSummary{}, err
	}
	return out, nil
}

func (s *userStore) ListImports(ctx context.Context, limit int) ([]training.ImportSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,source,file_name,content_hash,sessions,sets,imputed,created_at FROM imports
WHERE user_id=? ORDER BY id DESC LIMIT ?`, s.userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.ImportSummary
	for rows.Next() {
		var v training.ImportSummary
		var created string
		if err = rows.Scan(&v.ID, &v.Source, &v.FileName, &v.ContentHash, &v.Sessions, &v.Sets, &v.Imputed, &created); err != nil {
			return nil, err
		}
		if v.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

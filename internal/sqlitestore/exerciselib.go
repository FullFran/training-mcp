package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fullfran/training-mcp/internal/training"
)

// The person's own layer of the exercise library (migration 031). The shared
// layers are embedded in the training package; only this layer is stored.

var _ training.ExerciseLibraryStore = (*userStore)(nil)

func (s *userStore) OwnExerciseEntries(ctx context.Context) ([]training.OwnExerciseEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT match_key,name,primary_muscles,secondary_muscles,based_on
FROM exercise_library_entries WHERE user_id=? ORDER BY match_key`, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.OwnExerciseEntry
	for rows.Next() {
		var e training.OwnExerciseEntry
		var primary, secondary string
		if err = rows.Scan(&e.Key, &e.Name, &primary, &secondary, &e.BasedOn); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(primary), &e.Primary); err != nil {
			return nil, fmt.Errorf("exercise library entry %q: %w", e.Key, err)
		}
		if err = json.Unmarshal([]byte(secondary), &e.Secondary); err != nil {
			return nil, fmt.Errorf("exercise library entry %q: %w", e.Key, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SaveOwnExerciseEntry adds the entry or replaces the one under the same key.
func (s *userStore) SaveOwnExerciseEntry(ctx context.Context, e training.OwnExerciseEntry) error {
	primary, err := json.Marshal(nonNil(e.Primary))
	if err != nil {
		return err
	}
	secondary, err := json.Marshal(nonNil(e.Secondary))
	if err != nil {
		return err
	}
	defer s.writing(ctx)()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO exercise_library_entries(user_id,match_key,name,primary_muscles,secondary_muscles,based_on,updated_at)
VALUES (?,?,?,?,?,?,CURRENT_TIMESTAMP)
ON CONFLICT(user_id,match_key) DO UPDATE SET name=excluded.name, primary_muscles=excluded.primary_muscles,
 secondary_muscles=excluded.secondary_muscles, based_on=excluded.based_on, updated_at=CURRENT_TIMESTAMP`,
		s.userID, e.Key, e.Name, string(primary), string(secondary), e.BasedOn)
	return err
}

func (s *userStore) ExerciseAliases(ctx context.Context) ([]training.ExerciseAlias, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT match_key,alias,target FROM exercise_aliases WHERE user_id=? ORDER BY match_key`, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.ExerciseAlias
	for rows.Next() {
		var a training.ExerciseAlias
		if err = rows.Scan(&a.Key, &a.Alias, &a.Target); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetExerciseAlias adds the alias or points the one under the same key elsewhere.
func (s *userStore) SetExerciseAlias(ctx context.Context, a training.ExerciseAlias) error {
	defer s.writing(ctx)()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO exercise_aliases(user_id,match_key,alias,target,updated_at) VALUES (?,?,?,?,CURRENT_TIMESTAMP)
ON CONFLICT(user_id,match_key) DO UPDATE SET alias=excluded.alias, target=excluded.target, updated_at=CURRENT_TIMESTAMP`,
		s.userID, a.Key, a.Alias, a.Target)
	return err
}

// ExerciseVolume is WeeklyVolume split by exercise: the same working sets,
// weeks and exercise groups, so the fractional view counts what the per-group
// totals count.
func (s *userStore) ExerciseVolume(ctx context.Context, f training.ListFilter) ([]training.ExerciseVolume, error) {
	q := `SELECT date(se.date, '-' || ((CAST(strftime('%w', se.date) AS INTEGER) + 6) % 7) || ' days') AS wk,
st.exercise,COALESCE(g.muscle_group,''),COALESCE(SUM(st.si),0),COUNT(st.id)
FROM sets st
JOIN sessions se ON se.id=st.session_id
LEFT JOIN exercise_groups g ON g.user_id=se.user_id AND g.exercise=st.exercise
WHERE se.user_id=? AND st.category='working'`
	args := []any{s.userID}
	if f.From != "" {
		q += ` AND se.date>=?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND se.date<=?`
		args = append(args, f.To)
	}
	q += ` GROUP BY wk,st.exercise ORDER BY wk DESC,st.exercise`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []training.ExerciseVolume
	for rows.Next() {
		var v training.ExerciseVolume
		if err = rows.Scan(&v.WeekStart, &v.Exercise, &v.MuscleGroup, &v.TotalSI, &v.Sets); err != nil {
			return nil, err
		}
		v.TotalSI = training.NormalizeSI(v.TotalSI)
		out = append(out, v)
	}
	return out, rows.Err()
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

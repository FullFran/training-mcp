package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fullfran/training-mcp/internal/evidence"
)

// Column weights for ranking evidence: source_id (not searched), title,
// authors, topics, keywords, findings, context. A word in the title says most
// about what a source is about; one in its methods or limitations says least.
const evidenceRank = `bm25(evidence_fts, 0, 10, 3, 4, 4, 2, 1)`

// SyncEvidence makes the evidence index hold corpus c. It does nothing when
// the latest load has the same version and digest; otherwise it replaces every
// source in one transaction, so a search sees the old corpus or the new one,
// never a mix. An invalid corpus is refused and the index keeps what it had.
func (s *Store) SyncEvidence(ctx context.Context, c evidence.Corpus) error {
	if err := c.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version, digest string
	err = tx.QueryRowContext(ctx, `SELECT version, digest FROM evidence_corpus_loads ORDER BY id DESC LIMIT 1`).Scan(&version, &digest)
	if err == nil && version == c.Version && digest == c.Digest {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, stmt := range []string{`DELETE FROM evidence_fts`, `DELETE FROM evidence_source_topics`, `DELETE FROM evidence_sources`} {
		if _, err = tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	for _, src := range c.Sources {
		record, err := json.Marshal(src)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO evidence_sources(id, year, study_type, record) VALUES (?,?,?,?)`,
			src.ID, src.Year, src.StudyType, string(record)); err != nil {
			return err
		}
		for _, topic := range src.Topics {
			if _, err = tx.ExecContext(ctx, `INSERT INTO evidence_source_topics(source_id, topic) VALUES (?,?)`, src.ID, topic); err != nil {
				return err
			}
		}
		contextText := strings.Join([]string{src.Population, src.Methods, src.Limitations, src.Publication,
			strings.ReplaceAll(src.StudyType, "_", " "), fmt.Sprint(src.Year)}, " ")
		if _, err = tx.ExecContext(ctx, `INSERT INTO evidence_fts(source_id, title, authors, topics, keywords, findings, context) VALUES (?,?,?,?,?,?,?)`,
			src.ID, src.Title, strings.Join(src.Authors, ", "), evidence.TopicTerms(src.Topics),
			strings.Join(src.Keywords, "; "), strings.Join(src.KeyFindings, " "), contextText); err != nil {
			return err
		}
	}
	debates, err := json.Marshal(c.DebateRefs())
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO evidence_corpus_loads(version, digest, source_count, debates, loaded_at) VALUES (?,?,?,?,?)`,
		c.Version, c.Digest, len(c.Sources), string(debates), time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// syncEmbeddedEvidence loads the corpus compiled into this binary; Open calls
// it, so a deploy with a new corpus rebuilds the index on start.
func (s *Store) syncEmbeddedEvidence() error {
	c, err := evidence.Embedded()
	if err != nil {
		return fmt.Errorf("evidence corpus: %w", err)
	}
	return s.SyncEvidence(context.Background(), c)
}

// Evidence is the evidence index. It is the same for every user.
func (s *Store) Evidence() evidence.Index { return evidenceIndex{db: s.db} }

type evidenceIndex struct{ db *sql.DB }

// Search matches every term as a quoted FTS5 string. The library hands over only
// runs of letters and digits; quoting them as well means no term can ever be
// read as an operator, a column filter or a prefix.
func (x evidenceIndex) Search(ctx context.Context, q evidence.IndexQuery) ([]evidence.Hit, error) {
	if len(q.Terms) == 0 {
		return nil, nil
	}
	quoted := make([]string, len(q.Terms))
	for i, term := range q.Terms {
		quoted[i] = `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
	}
	where := []string{`evidence_fts MATCH ?`}
	args := []any{strings.Join(quoted, " ")}
	if q.Topic != "" {
		where = append(where, `EXISTS (SELECT 1 FROM evidence_source_topics t WHERE t.source_id=s.id AND t.topic=?)`)
		args = append(args, q.Topic)
	}
	if q.StudyType != "" {
		where = append(where, `s.study_type=?`)
		args = append(args, q.StudyType)
	}
	if q.YearFrom != 0 {
		where = append(where, `s.year>=?`)
		args = append(args, q.YearFrom)
	}
	if q.YearTo != 0 {
		where = append(where, `s.year<=?`)
		args = append(args, q.YearTo)
	}
	args = append(args, q.Limit)
	rows, err := x.db.QueryContext(ctx, `SELECT s.record, snippet(evidence_fts, 5, '[', ']', '...', 24)
		FROM evidence_fts JOIN evidence_sources s ON s.id=evidence_fts.source_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY `+evidenceRank+`, s.year DESC, s.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []evidence.Hit
	for rows.Next() {
		var record string
		var h evidence.Hit
		if err = rows.Scan(&record, &h.Snippet); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(record), &h.Source); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

func (x evidenceIndex) Source(ctx context.Context, id string) (evidence.Source, error) {
	var record string
	err := x.db.QueryRowContext(ctx, `SELECT record FROM evidence_sources WHERE id=?`, id).Scan(&record)
	if errors.Is(err, sql.ErrNoRows) {
		return evidence.Source{}, evidence.ErrUnknownSource
	}
	if err != nil {
		return evidence.Source{}, err
	}
	var src evidence.Source
	return src, json.Unmarshal([]byte(record), &src)
}

func (x evidenceIndex) Loaded(ctx context.Context) (evidence.LoadedCorpus, error) {
	var l evidence.LoadedCorpus
	var debates string
	err := x.db.QueryRowContext(ctx, `SELECT version, digest, source_count, debates, loaded_at FROM evidence_corpus_loads ORDER BY id DESC LIMIT 1`).
		Scan(&l.Version, &l.Digest, &l.SourceCount, &debates, &l.LoadedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return l, errors.New("the evidence library is not loaded")
	}
	if err != nil {
		return l, err
	}
	return l, json.Unmarshal([]byte(debates), &l.Debates)
}

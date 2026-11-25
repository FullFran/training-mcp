-- Evidence library (#116): the training-science corpus embedded in the binary,
-- indexed for lexical search. It is global and read-only for every user: no
-- table here has a user id, and only the corpus loader writes to them. The
-- loader rebuilds every table below when the embedded corpus version or
-- digest differs from the latest load, inside one transaction.

-- One row per corpus load; the latest row is the corpus the index holds.
CREATE TABLE evidence_corpus_loads (
 id INTEGER PRIMARY KEY,
 version TEXT NOT NULL,
 digest TEXT NOT NULL,
 source_count INTEGER NOT NULL,
 debates TEXT NOT NULL,
 loaded_at TEXT NOT NULL
);

-- One row per source: the filterable fields and the whole record as JSON.
CREATE TABLE evidence_sources (
 id TEXT PRIMARY KEY,
 year INTEGER NOT NULL,
 study_type TEXT NOT NULL,
 record TEXT NOT NULL
);

CREATE TABLE evidence_source_topics (
 source_id TEXT NOT NULL REFERENCES evidence_sources(id) ON DELETE CASCADE,
 topic TEXT NOT NULL,
 PRIMARY KEY (source_id, topic)
);
CREATE INDEX idx_evidence_source_topics_topic ON evidence_source_topics(topic, source_id);

-- Full-text index. Porter stemming folds English plurals and suffixes
-- ("deloads" finds "deload"); diacritics are folded so "periodizacion" finds
-- "periodización". context holds population, methods, limitations and the
-- publication.
CREATE VIRTUAL TABLE evidence_fts USING fts5(
 source_id UNINDEXED, title, authors, topics, keywords, findings, context,
 tokenize='porter unicode61 remove_diacritics 2'
);

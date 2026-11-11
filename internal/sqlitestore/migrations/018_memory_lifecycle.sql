-- Coach memory lifecycle: revisions, review dates, soft delete, supersession,
-- consolidation and the coach's judgements between similar facts.
ALTER TABLE coach_memory ADD COLUMN revision_count INTEGER NOT NULL DEFAULT 1;
-- When the coach should check the fact still holds; '' means never.
ALTER TABLE coach_memory ADD COLUMN review_after TEXT NOT NULL DEFAULT '';
-- The fact that replaced this one; a superseded fact leaves context and search.
ALTER TABLE coach_memory ADD COLUMN superseded_by INTEGER;
-- A forgotten fact is kept for history: '' while it is not forgotten.
ALTER TABLE coach_memory ADD COLUMN deleted_at TEXT NOT NULL DEFAULT '';
ALTER TABLE coach_memory ADD COLUMN forget_reason TEXT NOT NULL DEFAULT '' CHECK (forget_reason IN ('','forgotten','consolidated'));
ALTER TABLE coach_memory ADD COLUMN consolidated_into INTEGER;

-- Facts saved before this migration start their review cycle from their last update.
UPDATE coach_memory SET review_after = strftime('%Y-%m-%dT%H:%M:%S.000000000Z', substr(updated_at, 1, 19),
 CASE type WHEN 'limitation' THEN '+14 days' WHEN 'goal' THEN '+30 days' WHEN 'worked' THEN '+60 days'
  WHEN 'note' THEN '+90 days' ELSE '+180 days' END);

-- A fact's earlier texts, oldest first. They go with the fact when it is deleted
-- for good; whose they are comes from the fact.
CREATE TABLE coach_memory_revisions (
 id INTEGER PRIMARY KEY,
 memory_id INTEGER NOT NULL REFERENCES coach_memory(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL,
 type TEXT NOT NULL,
 title TEXT NOT NULL,
 body TEXT NOT NULL,
 replaced_at TEXT NOT NULL,
 UNIQUE (memory_id, revision)
);

-- How a saved fact relates to a similar one found when it was saved. Pending
-- until the coach judges it; a conflict stays judged until the person resolves it.
CREATE TABLE coach_memory_relations (
 id INTEGER PRIMARY KEY,
 user_id INTEGER NOT NULL,
 memory_id INTEGER NOT NULL REFERENCES coach_memory(id) ON DELETE CASCADE,
 candidate_id INTEGER NOT NULL REFERENCES coach_memory(id) ON DELETE CASCADE,
 relation TEXT NOT NULL DEFAULT '' CHECK (relation IN ('','supersedes','conflicts_with','related','compatible','scoped','not_conflict')),
 reason TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK (status IN ('pending','judged','resolved')),
 created_at TEXT NOT NULL,
 judged_at TEXT NOT NULL DEFAULT '',
 UNIQUE (memory_id, candidate_id)
);
CREATE INDEX idx_coach_memory_relations_user ON coach_memory_relations(user_id, status);

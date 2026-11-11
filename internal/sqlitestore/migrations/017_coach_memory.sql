-- Coach memory: durable facts about a person that outlive chats. Chats expire
-- after 90 days; memory stays until the person deletes it. Like proposals,
-- user_id does not reference users. The topic key names a fact, so saving the
-- same key again updates it instead of adding a duplicate.
CREATE TABLE coach_memory (
 id INTEGER PRIMARY KEY,
 user_id INTEGER NOT NULL,
 topic_key TEXT NOT NULL,
 type TEXT NOT NULL CHECK (type IN ('preference','limitation','goal','equipment','worked','note')),
 title TEXT NOT NULL,
 body TEXT NOT NULL,
 source_chat_id TEXT,
 pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0,1)),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE (user_id, topic_key)
);
CREATE INDEX idx_coach_memory_user_recent ON coach_memory(user_id, pinned DESC, updated_at DESC, id DESC);

-- Full-text index over the facts. It holds no copy of the text (external
-- content) and no user id: every search joins back to coach_memory and filters
-- by user there. Diacritics are folded so "lesion" finds "lesión".
CREATE VIRTUAL TABLE coach_memory_fts USING fts5(
 title, body, topic_key,
 content='coach_memory', content_rowid='id',
 tokenize='unicode61 remove_diacritics 2'
);
CREATE TRIGGER coach_memory_ai AFTER INSERT ON coach_memory BEGIN
 INSERT INTO coach_memory_fts(rowid, title, body, topic_key) VALUES (new.id, new.title, new.body, new.topic_key);
END;
CREATE TRIGGER coach_memory_ad AFTER DELETE ON coach_memory BEGIN
 INSERT INTO coach_memory_fts(coach_memory_fts, rowid, title, body, topic_key) VALUES ('delete', old.id, old.title, old.body, old.topic_key);
END;
CREATE TRIGGER coach_memory_au AFTER UPDATE ON coach_memory BEGIN
 INSERT INTO coach_memory_fts(coach_memory_fts, rowid, title, body, topic_key) VALUES ('delete', old.id, old.title, old.body, old.topic_key);
 INSERT INTO coach_memory_fts(rowid, title, body, topic_key) VALUES (new.id, new.title, new.body, new.topic_key);
END;

-- The coach's conversations, per person. Times are Unix milliseconds. A
-- conversation is deleted 90 days after its last message (updated_at), and with
-- its person. Photos are never stored: a message keeps only how many it had.
CREATE TABLE coach_conversations (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX coach_conversations_by_user ON coach_conversations(user_id, updated_at);
CREATE INDEX coach_conversations_by_age ON coach_conversations(updated_at);

CREATE TABLE coach_messages (
  id INTEGER PRIMARY KEY,
  conversation_id INTEGER NOT NULL REFERENCES coach_conversations(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
  origin TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL,
  steps TEXT NOT NULL DEFAULT '[]',
  photos INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX coach_messages_by_conversation ON coach_messages(conversation_id, created_at, id);

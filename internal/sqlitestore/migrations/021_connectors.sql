-- Remote MCP servers a person connected to their coach. No secret is stored here:
-- tokens and OAuth client credentials live in the coach's encrypted vault. Answers to
-- tool permission requests are never stored here either: they are host-held and
-- limited to one coach session (HCS-0005-R006, R007).
CREATE TABLE connectors (
 id INTEGER PRIMARY KEY,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 slug TEXT NOT NULL,
 label TEXT NOT NULL,
 url TEXT NOT NULL,
 auth TEXT NOT NULL CHECK (auth IN ('oauth','bearer','none')),
 state TEXT NOT NULL CHECK (state IN ('disconnected','connected')),
 created_at TEXT NOT NULL,
 UNIQUE (user_id, slug)
);


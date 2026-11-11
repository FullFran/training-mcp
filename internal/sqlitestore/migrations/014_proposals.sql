-- Agent writes become proposals. A proposal keeps what the agent asked for and,
-- once answered, what applying it returned. Like sessions, user_id does not
-- reference users: a request with no identity acts as user 0, apart from everyone.
CREATE TABLE proposals (
 id INTEGER PRIMARY KEY,
 user_id INTEGER NOT NULL,
 tool TEXT NOT NULL,
 action_type TEXT NOT NULL,
 payload TEXT NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('pending','applying','accepted','rejected','failed')),
 decided_by TEXT NOT NULL DEFAULT '',
 result TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 decided_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_proposals_user_status ON proposals(user_id, status, id DESC);

-- A remembered answer per action type, never per tool. A session answer expires.
CREATE TABLE approval_rules (
 user_id INTEGER NOT NULL,
 action_type TEXT NOT NULL,
 remember TEXT NOT NULL CHECK (remember IN ('session','always')),
 expires_at TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 PRIMARY KEY (user_id, action_type)
);

-- Accept-all per account. An account with no row asks for everything; every
-- account that exists now keeps today's behavior, where agent writes apply at once.
CREATE TABLE agent_permissions (
 user_id INTEGER PRIMARY KEY,
 accept_all INTEGER NOT NULL CHECK (accept_all IN (0,1)),
 updated_at TEXT NOT NULL
);
INSERT INTO agent_permissions(user_id,accept_all,updated_at) SELECT id,1,CURRENT_TIMESTAMP FROM users;

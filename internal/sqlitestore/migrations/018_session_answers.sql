-- HCS-0005 section 3.2: an "always" answer is stored only limited to the session
-- it was given in, with the decision record of that answer as its origin.
-- approval_rules held answers with neither (a permanent "always" and a 12-hour
-- "session" shared by every client), so none can be kept: each is dropped, a
-- revoked decision record says so, and the person is asked again. Accept-all
-- (agent_permissions) is an account-level grant and is left exactly as it was.

-- A decision record per HCS-0005 section 3.3. The operation is the proposal's id,
-- never its payload or result. The entry is always this server.
CREATE TABLE decision_records (
 id INTEGER PRIMARY KEY,
 user_id INTEGER NOT NULL,
 time TEXT NOT NULL,
 permission TEXT NOT NULL,
 outcome TEXT NOT NULL CHECK (outcome IN ('allow','deny','timeout','error','revoked')),
 operation TEXT NOT NULL DEFAULT '',
 session TEXT NOT NULL DEFAULT '',
 decider_kind TEXT NOT NULL CHECK (decider_kind IN ('subject','grant','denial','policy')),
 decider_id TEXT NOT NULL,
 channel TEXT NOT NULL,
 answer TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_decision_records_user ON decision_records(user_id, id DESC);

-- A stored allow_always (grant) or reject_always (denial) for one action type,
-- limited to one agent session and to a window. A rule without a session cannot
-- exist: it would match checks from every session.
CREATE TABLE answer_rules (
 id INTEGER PRIMARY KEY,
 user_id INTEGER NOT NULL,
 action_type TEXT NOT NULL,
 effect TEXT NOT NULL CHECK (effect IN ('allow','deny')),
 session TEXT NOT NULL CHECK (session <> ''),
 expires_at TEXT NOT NULL,
 origin INTEGER NOT NULL REFERENCES decision_records(id),
 created_at TEXT NOT NULL,
 UNIQUE (user_id, action_type, effect, session)
);

INSERT INTO decision_records(user_id,time,permission,outcome,decider_kind,decider_id,channel)
 SELECT user_id, strftime('%Y-%m-%dT%H:%M:%SZ','now'), action_type, 'revoked', 'policy', 'HCS-0005-R008', 'migration:018'
 FROM approval_rules ORDER BY user_id, action_type;
DROP TABLE approval_rules;

-- The agent session a proposal was asked in; '' for none, which no rule matches.
ALTER TABLE proposals ADD COLUMN session TEXT NOT NULL DEFAULT '';

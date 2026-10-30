-- Remembers the result of a keyed write, so a client that lost the response can
-- repeat the call without recording anything twice. Values cannot detect a retry:
-- identical straight sets are normal training. The fingerprint exposes a key
-- reused for different content, and rows only matter for as long as a retry
-- could plausibly arrive, so old ones are pruned.
CREATE TABLE IF NOT EXISTS idempotency_keys (
 key TEXT PRIMARY KEY,
 fingerprint TEXT NOT NULL,
 response TEXT NOT NULL,
 created_at TEXT NOT NULL
);

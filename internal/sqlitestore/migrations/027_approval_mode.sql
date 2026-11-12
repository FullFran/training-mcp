-- Approval modes per account: review_all (every agent proposal waits for the
-- person), accept_except_delete (everything applies at once except deleting
-- action types) and accept_all. An account with no row, or a row with no mode,
-- is in accept_except_delete, the mode of every account created from now on.
-- accept_all stays in step with the mode (1 only for accept_all).
ALTER TABLE agent_permissions ADD COLUMN mode TEXT CHECK (mode IS NULL OR mode IN ('review_all','accept_except_delete','accept_all'));

-- Every account that exists now keeps exactly its behavior: accept-all on stays
-- accept_all, everything else becomes an explicit review_all. Only accounts
-- created later start in the new default.
UPDATE agent_permissions SET mode = CASE WHEN accept_all = 1 THEN 'accept_all' ELSE 'review_all' END;
INSERT INTO agent_permissions(user_id,accept_all,mode,updated_at)
 SELECT id,0,'review_all',CURRENT_TIMESTAMP FROM users WHERE id NOT IN (SELECT user_id FROM agent_permissions);

-- The host proposes one memory note per finished workout (issue #69): when the
-- person taps Terminar, or when the app is opened after the session went idle.
-- This column records that the summary of a session was proposed, so it is
-- proposed at most once whatever the person answers. NULL: never proposed.
ALTER TABLE sessions ADD COLUMN summary_proposed_at TEXT;

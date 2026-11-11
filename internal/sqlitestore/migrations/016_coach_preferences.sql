-- The person's choice of coach model and thinking level. NULL means never
-- chosen, and the engine's default applies, so existing users see no change.
ALTER TABLE users ADD COLUMN coach_model TEXT;
ALTER TABLE users ADD COLUMN coach_thinking TEXT;

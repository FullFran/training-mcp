-- The person's target rest between sets, in seconds. NULL means never chosen, and
-- the app's long-standing 120 s default applies, so existing users see no change.
ALTER TABLE users ADD COLUMN rest_seconds INTEGER;

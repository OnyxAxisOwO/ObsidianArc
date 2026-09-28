-- Optional reason for disabling or banning an account, displayed to the user
-- when they attempt to sign in or access the application.

ALTER TABLE users ADD COLUMN ban_reason TEXT NOT NULL DEFAULT '';

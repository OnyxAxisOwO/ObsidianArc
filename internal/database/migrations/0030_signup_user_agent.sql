-- The client an account was created with, shown back to its owner in account
-- settings. Existing accounts stay empty because a later login session cannot
-- tell us which user agent made the original registration.
ALTER TABLE users ADD COLUMN signup_user_agent TEXT NOT NULL DEFAULT '';

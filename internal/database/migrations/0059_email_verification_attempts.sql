-- A resend or profile email change replaces the code row, so guesses must
-- survive independently of any one credential and follow the account.
CREATE TABLE email_verification_attempts (
    user_id          TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    attempts         INTEGER NOT NULL,
    last_attempt_at   BIGINT NOT NULL
);

-- Preserve the strongest per-code limit reached before this migration.
-- code_expires_at bounds the last possible guess, so an upgrade cannot shorten
-- an existing account's 24-hour window.
INSERT INTO email_verification_attempts (user_id, attempts, last_attempt_at)
SELECT user_id, MAX(code_attempts), MAX(code_expires_at)
FROM email_verifications
WHERE code_attempts > 0
GROUP BY user_id;

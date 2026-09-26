-- A numeric code is short enough to guess, so only its purpose-keyed HMAC
-- is stored; attempts live beside it so account-row locks serialize guesses.
ALTER TABLE email_verifications ADD COLUMN code_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE email_verifications ADD COLUMN code_expires_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE email_verifications ADD COLUMN code_attempts INTEGER NOT NULL DEFAULT 0;

-- Confirming that an address belongs to whoever typed it.
--
-- The domain allowlist added with the registration controls only checks the
-- string a visitor typed; it stops nobody who is willing to type
-- "someone@qq.com". Verification is what turns that from a speed bump into a
-- gate, because it costs an attacker a mailbox per account.

-- Existing accounts are verified. They predate the feature, and marking them
-- unverified would lock out everyone on an instance the moment an operator
-- switched it on — including, on a small instance, the only administrator.
ALTER TABLE users ADD COLUMN email_verified BOOLEAN NOT NULL DEFAULT TRUE;

-- Outstanding verification links.
--
-- The token is stored as a SHA-256 digest, the same way a session is: a
-- database that leaks must not hand out working links. The address is carried
-- alongside because a user may change it before following the link, and the
-- link should then verify what it was sent to rather than whatever the row
-- says now.
CREATE TABLE email_verifications (
    -- The digest, not the token.
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email      TEXT NOT NULL,
    expires_at BIGINT NOT NULL,
    created_at BIGINT NOT NULL
);

CREATE INDEX ix_email_verifications_user ON email_verifications (user_id);
CREATE INDEX ix_email_verifications_expiry ON email_verifications (expires_at);

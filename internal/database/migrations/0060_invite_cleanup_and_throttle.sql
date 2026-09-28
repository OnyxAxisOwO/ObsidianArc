-- Clean up orphaned personal invite codes whose owner no longer exists.
DELETE FROM invite_codes WHERE owner_id <> '' AND owner_id NOT IN (SELECT id FROM users);

-- Rate-limit regenerating personal invite codes: 10 per hour per account,
-- backed by a row lock on the user's row so concurrent requests serialize.
CREATE TABLE invite_regenerate_throttle (
    user_id      TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    window_start BIGINT NOT NULL DEFAULT 0,
    count        INTEGER NOT NULL DEFAULT 0
);

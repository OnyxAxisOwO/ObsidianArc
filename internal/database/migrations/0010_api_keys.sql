-- Programmatic access: API keys, and which groups may use them.
--
-- The token is stored the way a session token is — as its SHA-256 digest,
-- never the value — so a database dump tells you which keys exist and when
-- they expire but does not let you present one. A digest is enough because
-- the token is 256 bits of randomness rather than a password: there is
-- nothing to brute-force offline, and a slow KDF on every API request would
-- be paid for nothing.
CREATE TABLE api_keys (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL,
    -- The opening characters of the token, kept in clear so the owner can
    -- tell two keys apart in a list. Not secret and not sufficient to
    -- authenticate: it is a label, the way a card's last four digits are.
    prefix       TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    -- Epoch millis. Zero means the key does not expire, which is the shape
    -- every other optional timestamp in this schema already uses.
    expires_at   BIGINT NOT NULL DEFAULT 0,
    last_used_at BIGINT NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL,
    updated_at   BIGINT NOT NULL
);

-- The authentication path is a single lookup on this index, so an instance
-- with many keys costs the same per request as one with a handful.
CREATE UNIQUE INDEX ux_api_keys_token ON api_keys (token_hash);
CREATE INDEX ix_api_keys_user ON api_keys (user_id, id);

-- Whether a group's members may use the API at all.
--
-- Defaulting to TRUE means the master switch in settings is the deliberate
-- act: an operator who turns the API on gets what they asked for, and
-- narrows it afterwards by unticking groups. A default of FALSE would make
-- enabling the feature appear to do nothing.
ALTER TABLE user_groups ADD COLUMN api_access BOOLEAN NOT NULL DEFAULT TRUE;

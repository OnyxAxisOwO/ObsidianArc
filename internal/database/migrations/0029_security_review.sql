-- A review may let an account exist while withholding programmatic access.
-- The restriction belongs to the account rather than its individual keys:
-- it applies before the first key exists, covers every existing key at once,
-- and can expire without rewriting a credential list.
ALTER TABLE users ADD COLUMN api_restricted BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN api_restricted_until BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN api_restriction_source TEXT NOT NULL DEFAULT '';

-- Security decisions are not ordinary HTTP traffic. Keeping them apart from
-- request_log makes "why was this account restricted" a direct query rather
-- than an inference from status codes, and lets the evidence survive deletion
-- of the account it described.
CREATE TABLE security_events (
    id        TEXT PRIMARY KEY,
    at        BIGINT NOT NULL,
    event     TEXT NOT NULL,
    severity  TEXT NOT NULL,
    user_id   TEXT NOT NULL DEFAULT '',
    username  TEXT NOT NULL DEFAULT '',
    actor_id  TEXT NOT NULL DEFAULT '',
    actor_username TEXT NOT NULL DEFAULT '',
    ip        TEXT NOT NULL DEFAULT '',
    source    TEXT NOT NULL DEFAULT '',
    decision  TEXT NOT NULL DEFAULT '',
    reason    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX ix_security_events_at ON security_events (at);
CREATE INDEX ix_security_events_user ON security_events (user_id, at);
CREATE INDEX ix_security_events_event ON security_events (event, at);
CREATE INDEX ix_security_events_severity ON security_events (severity, at);

-- One row per account is enough for the burst detector. The owner row is
-- locked before this row is read and changed, so two server processes cannot
-- both observe the last free attempt and let it through.
CREATE TABLE chat_security_state (
    user_id        TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    window_start   BIGINT NOT NULL DEFAULT 0,
    attempts       INTEGER NOT NULL DEFAULT 0,
    verified_until BIGINT NOT NULL DEFAULT 0
);

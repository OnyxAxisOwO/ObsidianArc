-- Identity: groups, users, sessions, and the two key/value stores every
-- later module leans on.
--
-- Conventions used by every migration in this directory:
--   * ids are TEXT (ULIDs, see internal/id) — no dialect-specific sequences,
--     and nothing enumerable in a URL
--   * timestamps are BIGINT Unix epoch milliseconds — identical semantics in
--     SQLite and Postgres, and window queries become integer comparisons
--   * %BLOB% expands to BLOB or BYTEA (see migrate.go)

CREATE TABLE user_groups (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    -- The group new registrations land in. Exactly one row carries it; the
    -- group service enforces that, not a constraint, because swapping which
    -- group is default has to be a two-row update.
    is_default       BOOLEAN NOT NULL DEFAULT FALSE,
    -- A shortcut for "every enabled model", so a Free/Pro split does not have
    -- to re-list every model each time one is added.
    allow_all_models BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order       INTEGER NOT NULL DEFAULT 0,
    created_at       BIGINT NOT NULL,
    updated_at       BIGINT NOT NULL
);

CREATE UNIQUE INDEX ux_user_groups_name ON user_groups (name);

CREATE TABLE users (
    id             TEXT PRIMARY KEY,
    -- Stored as typed, matched as folded: Postgres CITEXT has no SQLite
    -- equivalent, so the folded form is a real column with its own index.
    username       TEXT NOT NULL,
    username_lower TEXT NOT NULL,
    email          TEXT NOT NULL DEFAULT '',
    email_lower    TEXT NOT NULL DEFAULT '',
    password_hash  TEXT NOT NULL,
    nickname       TEXT NOT NULL DEFAULT '',
    avatar         TEXT NOT NULL DEFAULT '',
    bio            TEXT NOT NULL DEFAULT '',
    role           TEXT NOT NULL DEFAULT 'user',   -- 'user' | 'admin'
    group_id       TEXT REFERENCES user_groups (id) ON DELETE SET NULL,
    status         TEXT NOT NULL DEFAULT 'active', -- 'active' | 'disabled'
    created_at     BIGINT NOT NULL,
    updated_at     BIGINT NOT NULL,
    last_login_at  BIGINT NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX ux_users_username_lower ON users (username_lower);
-- Partial, because an address is optional and several users may have none.
CREATE UNIQUE INDEX ux_users_email_lower ON users (email_lower) WHERE email_lower <> '';
CREATE INDEX ix_users_group ON users (group_id);
CREATE INDEX ix_users_created ON users (created_at);

CREATE TABLE sessions (
    -- The SHA-256 of the cookie value, never the value itself: a dump of this
    -- table does not let anyone log in as anybody.
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   BIGINT NOT NULL,
    expires_at   BIGINT NOT NULL,
    last_seen_at BIGINT NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX ix_sessions_user ON sessions (user_id);
CREATE INDEX ix_sessions_expires ON sessions (expires_at);

-- Instance-wide settings an administrator can change without a restart:
-- whether registration is open, which group new accounts join, the site name.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at BIGINT NOT NULL
);

-- Per-user interface state that should follow the account between devices:
-- theme, accent, wallpaper, default model, default reasoning. Kept out of
-- `users` on purpose — it is a growing bag of presentation choices, and the
-- user row is read on every authenticated request.
CREATE TABLE user_preferences (
    user_id    TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    data       TEXT NOT NULL DEFAULT '{}',
    updated_at BIGINT NOT NULL
);

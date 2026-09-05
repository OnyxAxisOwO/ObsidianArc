-- Usage accounting and the limits that constrain it.
--
-- Two tables with different jobs. usage_records is the audit trail: one
-- immutable row per AI request, which is what makes "per user", "per model",
-- "per provider", "last week" and "why did that fail" all the same GROUP BY.
-- usage_counters is a fast index over it, so enforcing a limit is one atomic
-- upsert instead of a scan; it can be rebuilt from the ledger at any time and
-- is never the source of truth.
--
-- There is deliberately no used_tokens column on users. A denormalised total
-- with no ledger behind it drifts, and cannot answer any question except the
-- one it was written for.

CREATE TABLE usage_records (
    id      TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Snapshots, not foreign keys: which group someone was in and what the
    -- model was called at the time is part of the record, and must survive
    -- the group being renamed or the model deleted.
    group_id     TEXT NOT NULL DEFAULT '',
    provider_id  TEXT NOT NULL DEFAULT '',
    provider_name TEXT NOT NULL DEFAULT '',
    model_id     TEXT NOT NULL DEFAULT '',
    model_name   TEXT NOT NULL DEFAULT '',
    model_ref    TEXT NOT NULL DEFAULT '',
    conversation_id TEXT NOT NULL DEFAULT '',
    message_id      TEXT NOT NULL DEFAULT '',
    request_id      TEXT NOT NULL,

    input_tokens     INTEGER NOT NULL DEFAULT 0,
    output_tokens    INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens     INTEGER NOT NULL DEFAULT 0,
    credits          DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- 'ok' | 'error' | 'aborted' | 'rejected'. An aborted turn keeps the
    -- tokens it actually spent; a rejection is recorded with zeros, which is
    -- what makes "why did my request fail" answerable later.
    status     TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',

    started_at  BIGINT NOT NULL,
    finished_at BIGINT NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX ux_usage_request ON usage_records (request_id);
CREATE INDEX ix_usage_user_time ON usage_records (user_id, started_at);
CREATE INDEX ix_usage_model_time ON usage_records (model_id, started_at);
CREATE INDEX ix_usage_provider_time ON usage_records (provider_id, started_at);
CREATE INDEX ix_usage_time ON usage_records (started_at);

-- One row per (who, which window, which bucket). The primary key is what
-- makes the increment a single atomic upsert: the check happens after the
-- write, on the value the write returned, so two concurrent requests cannot
-- both see room that only one of them has.
CREATE TABLE usage_counters (
    scope_key    TEXT NOT NULL,   -- 'u:<user id>'
    window_kind  TEXT NOT NULL,   -- 'rpm' | 'tpm' | '5h' | '1w' | '1m'
    window_start BIGINT NOT NULL,
    requests     BIGINT NOT NULL DEFAULT 0,
    tokens       BIGINT NOT NULL DEFAULT 0,
    credits      DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (scope_key, window_kind, window_start)
);

-- The janitor drops buckets that have rolled over.
CREATE INDEX ix_usage_counters_window ON usage_counters (window_start);

-- Limits, at three levels. Every column is nullable, and NULL means "inherit
-- from the level above" — global, then the user's group, then an override on
-- the user, last non-null winning. An explicit `enabled = FALSE` at any level
-- turns that window off rather than inheriting it, which is how one account
-- is exempted without editing the group everyone else shares.
CREATE TABLE quota_policies (
    id       TEXT PRIMARY KEY,
    scope    TEXT NOT NULL,             -- 'global' | 'group' | 'user'
    scope_id TEXT NOT NULL DEFAULT '',  -- '' for global

    -- Rate limits, per minute.
    rpm INTEGER,
    tpm INTEGER,

    window_5h_enabled  BOOLEAN,
    window_5h_requests INTEGER,
    window_5h_tokens   BIGINT,
    window_5h_credits  DOUBLE PRECISION,

    window_1w_enabled  BOOLEAN,
    window_1w_requests INTEGER,
    window_1w_tokens   BIGINT,
    window_1w_credits  DOUBLE PRECISION,

    window_1m_enabled  BOOLEAN,
    window_1m_requests INTEGER,
    window_1m_tokens   BIGINT,
    window_1m_credits  DOUBLE PRECISION,

    updated_at BIGINT NOT NULL
);

CREATE UNIQUE INDEX ux_quota_scope ON quota_policies (scope, scope_id);

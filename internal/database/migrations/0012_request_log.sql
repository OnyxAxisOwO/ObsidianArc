-- Every request the server answered.
--
-- Distinct from usage_records, which is the money: one row per AI turn, with
-- tokens and credits. This is the traffic — who called what, when, and what
-- came back — including the calls that never reached a model at all: a failed
-- sign-in, a refused upload, a key presented after it expired.
--
-- The two overlap on purpose. A request that did reach a model carries its
-- name and id here too, so "show me everything that account did, and which
-- model each attempt was for" is one query against one table rather than a
-- join an operator has to know to write.
--
-- The application keeps the newest 200,000 rows. Without a hard ceiling this
-- public-facing audit feature would let an anonymous caller fill the database
-- simply by generating distinct failed requests forever.
CREATE TABLE request_log (
    id          TEXT PRIMARY KEY,
    at          BIGINT NOT NULL,
    method      TEXT NOT NULL,
    path        TEXT NOT NULL,
    status      INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    bytes       BIGINT NOT NULL DEFAULT 0,

    -- Snapshots, not foreign keys: who made a request is part of the record
    -- and has to survive the account being deleted.
    user_id  TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    -- 'web' when a session cookie carried it, 'api' when a bearer key did,
    -- '' when nobody was signed in.
    channel  TEXT NOT NULL DEFAULT '',

    ip         TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',

    -- Set by the handlers that talk to a model, so the log can be filtered by
    -- model without joining the ledger.
    model_id   TEXT NOT NULL DEFAULT '',
    model_name TEXT NOT NULL DEFAULT '',
    -- The application-level code, where there was one. A 400 that says
    -- "email_domain_rejected" answers a support question that "400" does not.
    error_code TEXT NOT NULL DEFAULT ''
);

-- Every listing is newest-first over a window, optionally narrowed by one of
-- these columns. Each index is one filter plus the ordering.
CREATE INDEX ix_request_log_at ON request_log (at);
CREATE INDEX ix_request_log_user ON request_log (user_id, at);
CREATE INDEX ix_request_log_model ON request_log (model_id, at);
CREATE INDEX ix_request_log_status ON request_log (status, at);

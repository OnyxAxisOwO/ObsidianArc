-- The administrators' audit trail: who did what in the backoffice.
--
-- Not the request log, which records traffic and is pruned by hand, and not
-- the security log, which records the decisions the system made about
-- accounts. This one answers "which administrator changed that", so it is
-- written for every backoffice write (and a few reads that expose somebody
-- else's data) and kept for a retention an administrator cannot shorten from
-- a delegated grant.
--
-- Names are snapshots for the same reason as in security_events: the row has
-- to stay readable after the actor or the target is renamed or deleted.

CREATE TABLE admin_audit (
    id             TEXT PRIMARY KEY,
    at             BIGINT NOT NULL,
    actor_id       TEXT NOT NULL DEFAULT '',
    actor_username TEXT NOT NULL DEFAULT '',
    actor_role     TEXT NOT NULL DEFAULT '',
    method         TEXT NOT NULL,
    route          TEXT NOT NULL,
    path           TEXT NOT NULL,
    target_id      TEXT NOT NULL DEFAULT '',
    target_label   TEXT NOT NULL DEFAULT '',
    status         INTEGER NOT NULL DEFAULT 0,
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    via            TEXT NOT NULL DEFAULT '',
    ip             TEXT NOT NULL DEFAULT '',
    user_agent     TEXT NOT NULL DEFAULT '',
    request        TEXT NOT NULL DEFAULT '',
    changes        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX ix_admin_audit_at ON admin_audit (at);
CREATE INDEX ix_admin_audit_actor ON admin_audit (actor_id, at);
CREATE INDEX ix_admin_audit_route ON admin_audit (route, at);
CREATE INDEX ix_admin_audit_target ON admin_audit (target_id, at);

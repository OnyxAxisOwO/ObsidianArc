-- Instance backups have their own row because their scheduler lease and
-- write-only storage credentials must survive cache refreshes and restarts.
CREATE TABLE system_backups (
    id                    TEXT PRIMARY KEY,
    instance_id           TEXT NOT NULL,
    enabled               BOOLEAN NOT NULL DEFAULT FALSE,
    endpoint              TEXT NOT NULL DEFAULT '',
    bucket                TEXT NOT NULL DEFAULT '',
    region                TEXT NOT NULL DEFAULT 'us-east-1',
    prefix                TEXT NOT NULL DEFAULT 'obsidian-arc-backups',
    access_key_id_enc     %BLOB%,
    secret_access_key_enc %BLOB%,
    interval_hours        BIGINT NOT NULL DEFAULT 24,
    retention_days        BIGINT NOT NULL DEFAULT 7,
    lease_token           TEXT NOT NULL DEFAULT '',
    lease_until           BIGINT NOT NULL DEFAULT 0,
    last_started_at       BIGINT NOT NULL DEFAULT 0,
    last_finished_at      BIGINT NOT NULL DEFAULT 0,
    last_success_at       BIGINT NOT NULL DEFAULT 0,
    last_status           TEXT NOT NULL DEFAULT '',
    last_error            TEXT NOT NULL DEFAULT ''
);

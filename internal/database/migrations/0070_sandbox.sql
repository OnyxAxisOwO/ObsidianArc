-- Code sandboxes: somewhere the model can run what it wrote, in work mode,
-- for the groups an administrator chose.
--
-- A profile is the unit an administrator configures: which backend, which
-- languages, and the limits. A group points at one profile or at none, so
-- "which groups may run code" and "under what limits" are one choice rather
-- than two that can disagree.

-- Interpreters for the in-process backend: WASI command modules (a QuickJS,
-- a CPython built for wasip1) that an administrator uploaded. Kept as bytes
-- here rather than embedded in the binary or written to disk, for the
-- reasons plugin_packages are: the binary does not grow by the size of every
-- language somebody might want, a second instance finds them without being
-- told, a backup carries them, and removing one is a DELETE.
CREATE TABLE sandbox_interpreters (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    -- The language the model asks for, e.g. 'javascript' or 'python'.
    language    TEXT NOT NULL,
    version     TEXT NOT NULL DEFAULT '',
    -- Of the module, so "is this the file I uploaded" has an answer.
    sha256      TEXT NOT NULL,
    -- JSON array of argv the module is started with; the code is passed on
    -- stdin or as a file in a per-run in-memory directory, as the interpreter
    -- needs, which the argv says ('{file}' is replaced by its path).
    args        TEXT NOT NULL DEFAULT '',
    module      %BLOB% NOT NULL,
    size_bytes  BIGINT NOT NULL DEFAULT 0,
    created_at  BIGINT NOT NULL,
    created_by  TEXT NOT NULL DEFAULT '',
    updated_at  BIGINT NOT NULL
);

CREATE UNIQUE INDEX ux_sandbox_interpreters_name ON sandbox_interpreters (name);

CREATE TABLE sandbox_profiles (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    -- 'wasm' runs inside the server on the plugin runtime it already has;
    -- 'runner' hands the job to a Docker runner that connected out to us.
    kind             TEXT NOT NULL,
    -- Comma-separated language names. Stored as text rather than a join
    -- table because the list is short, read whole and never queried into.
    languages        TEXT NOT NULL DEFAULT '',
    -- A JSON object of language to what runs it: an image for a runner
    -- profile, a sandbox_interpreters id for a wasm one. One column for both
    -- because a profile is one kind or the other, never both; an entry that
    -- names nothing that exists makes that language unavailable, not an error
    -- at save time, so deleting an interpreter fails closed.
    images           TEXT NOT NULL DEFAULT '',
    timeout_ms       BIGINT NOT NULL DEFAULT 10000,
    memory_mb        BIGINT NOT NULL DEFAULT 64,
    max_output_bytes BIGINT NOT NULL DEFAULT 65536,
    -- Jobs running at once under this profile, across every instance. Zero
    -- means the instance-wide setting decides.
    max_concurrent   BIGINT NOT NULL DEFAULT 0,
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       BIGINT NOT NULL,
    updated_at       BIGINT NOT NULL
);

CREATE UNIQUE INDEX ux_sandbox_profiles_name ON sandbox_profiles (name);

-- Unlike the other group capabilities this defaults to off: running code is
-- a new thing a group has to be given, not something existing groups had and
-- may lose. Empty rather than NULL with a foreign key, because SQLite only
-- takes a REFERENCES clause on ADD COLUMN under conditions the two engines
-- read differently; the profile store clears it when a profile is deleted,
-- in the same transaction, and a dangling id is read as "none".
ALTER TABLE user_groups ADD COLUMN sandbox_profile_id TEXT NOT NULL DEFAULT '';

-- A runner is a machine with Docker that connects out to this server and
-- asks for work, so the server never holds a Docker socket. Its token is
-- stored as a digest, the way an API key's is, for the same reason.
CREATE TABLE sandbox_runners (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL DEFAULT '',
    profile_id   TEXT NOT NULL REFERENCES sandbox_profiles (id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    last_seen_at BIGINT NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL,
    updated_at   BIGINT NOT NULL
);

CREATE UNIQUE INDEX ux_sandbox_runners_token ON sandbox_runners (token_hash);
CREATE INDEX ix_sandbox_runners_profile ON sandbox_runners (profile_id);

-- Jobs for runners. In the database rather than in memory because two
-- instances may share it: the runner can be connected to one while the chat
-- request that wants the answer is being served by the other.
CREATE TABLE sandbox_jobs (
    id               TEXT PRIMARY KEY,
    profile_id       TEXT NOT NULL REFERENCES sandbox_profiles (id) ON DELETE CASCADE,
    user_id          TEXT NOT NULL DEFAULT '',
    -- queued, running, done, failed, cancelled.
    status           TEXT NOT NULL,
    language         TEXT NOT NULL,
    code             TEXT NOT NULL,
    stdin            TEXT NOT NULL DEFAULT '',
    -- JSON: stdout, stderr, exit code, whether output was cut, duration.
    result           TEXT NOT NULL DEFAULT '',
    lease_owner      TEXT NOT NULL DEFAULT '',
    -- Epoch millis. A running job whose lease has passed is queued again, so
    -- a runner that died mid-job does not strand the request waiting on it.
    lease_until      BIGINT NOT NULL DEFAULT 0,
    -- Set when the request that wanted the answer went away; the runner sees
    -- it on its next heartbeat and kills the container.
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       BIGINT NOT NULL,
    finished_at      BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX ix_sandbox_jobs_claim ON sandbox_jobs (profile_id, status, created_at);
CREATE INDEX ix_sandbox_jobs_finished ON sandbox_jobs (status, finished_at);

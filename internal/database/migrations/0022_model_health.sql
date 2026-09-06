-- Whether a model is answering, and why it stopped.
--
-- Most of the evidence is already in usage_records: every turn a reader takes
-- lands there with a status and, when it failed, a code. That is the better
-- signal — it is real traffic, on the real prompt sizes, and it costs nothing
-- to collect. This table is only for the gap: a model nobody has used for a
-- while, where the alternative to asking it directly is reporting "unknown"
-- forever and finding out from a user that it has been dead since Tuesday.
CREATE TABLE model_probes (
    id         TEXT PRIMARY KEY,
    model_id   TEXT NOT NULL,
    at         BIGINT NOT NULL,
    ok         BOOLEAN NOT NULL,
    -- Empty when it answered. Otherwise the same code a reader's failed turn
    -- would have carried, so the two sources read alike in one list.
    code       TEXT NOT NULL DEFAULT '',
    message    TEXT NOT NULL DEFAULT '',
    latency_ms INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX ix_model_probes_model_time ON model_probes (model_id, at);

-- Set when the system turned a model off because it had stopped answering.
--
-- It exists so that switching one back on is only ever undoing the system's
-- own decision. An administrator who disabled a model on purpose has said
-- something, and a model that starts answering again is not an argument
-- against it.
ALTER TABLE models ADD COLUMN auto_disabled BOOLEAN NOT NULL DEFAULT FALSE;

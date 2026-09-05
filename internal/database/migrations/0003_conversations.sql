-- Conversations, messages and attachments.
--
-- The transcript moves server-side here. In the standalone build the browser
-- held it and re-sent it on every turn, which is what made "edit an earlier
-- message and run again" a matter of truncating an array. The same model
-- survives, one level down: the rows are the conversation, a turn is
-- "truncate at this sequence number and append", and the client sends an
-- intent rather than a transcript.

CREATE TABLE conversations (
    id      TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title   TEXT NOT NULL DEFAULT '',
    -- The model last used here, so reopening a conversation resumes with it
    -- rather than with whatever the picker happened to be showing.
    model_id      TEXT REFERENCES models (id) ON DELETE SET NULL,
    pinned        BOOLEAN NOT NULL DEFAULT FALSE,
    message_count INTEGER NOT NULL DEFAULT 0,
    created_at    BIGINT NOT NULL,
    updated_at    BIGINT NOT NULL
);

-- The rail's own query: this user's conversations, newest first.
CREATE INDEX ix_conversations_user ON conversations (user_id, updated_at);

CREATE TABLE messages (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    -- Denormalised from the conversation so every read can be scoped by the
    -- owner in its WHERE clause instead of by a check on the result.
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Position in the conversation. Truncating a transcript is a delete by
    -- sequence number, which is what makes edit-and-resend one statement.
    seq  INTEGER NOT NULL,
    role TEXT NOT NULL,               -- 'user' | 'assistant'
    content TEXT NOT NULL DEFAULT '',
    -- Kept apart from the answer, not merged into it: the interface shows it
    -- collapsed, and a future export should be able to leave it out.
    reasoning TEXT NOT NULL DEFAULT '',
    -- A failed turn is a row, so "try again" has something to replace, but it
    -- is never replayed to the model as though the assistant had said it.
    error       TEXT NOT NULL DEFAULT '',
    model_id    TEXT,
    provider_id TEXT,
    -- Duration, first-token latency, token counts, tokens per second. JSON
    -- because it is displayed and never queried.
    stats_json TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL
);

CREATE UNIQUE INDEX ux_messages_seq ON messages (conversation_id, seq);
CREATE INDEX ix_messages_user ON messages (user_id, created_at);

-- Images, stored in the database.
--
-- One database is the whole deployment story, and an object store would be a
-- second thing to run. The client downscales before uploading, the row is
-- capped, and the read path is a single ownership-checked fetch. The store is
-- narrow enough that a disk or S3 backend later is one implementation rather
-- than a migration.
CREATE TABLE attachments (
    id      TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Null until the message it belongs to exists: a file is uploaded while
    -- the user is still typing. The janitor removes ones that never get
    -- attached.
    message_id TEXT REFERENCES messages (id) ON DELETE CASCADE,
    mime       TEXT NOT NULL,
    width      INTEGER NOT NULL DEFAULT 0,
    height     INTEGER NOT NULL DEFAULT 0,
    size       INTEGER NOT NULL DEFAULT 0,
    data       %BLOB% NOT NULL,
    created_at BIGINT NOT NULL
);

CREATE INDEX ix_attachments_message ON attachments (message_id);
CREATE INDEX ix_attachments_orphans ON attachments (message_id, created_at);

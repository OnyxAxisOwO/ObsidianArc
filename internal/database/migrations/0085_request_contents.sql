CREATE TABLE request_contents (
    log_id TEXT PRIMARY KEY REFERENCES request_log(id) ON DELETE CASCADE,
    body TEXT NOT NULL,
    truncated BOOLEAN NOT NULL DEFAULT FALSE
);

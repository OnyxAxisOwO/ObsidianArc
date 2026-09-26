CREATE TABLE usercheck_config (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    enabled         BOOLEAN NOT NULL,
    exempt_domains  TEXT NOT NULL,
    failure_mode    TEXT NOT NULL,
    api_key         %BLOB%,
    updated_at      BIGINT NOT NULL
);

-- A shared row keeps paid test requests behind one cooldown across instances.
CREATE TABLE usercheck_test_limiter (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    last_sent_at  BIGINT NOT NULL
);

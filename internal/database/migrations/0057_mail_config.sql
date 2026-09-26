CREATE TABLE mail_config (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    host          TEXT NOT NULL,
    port          INTEGER NOT NULL,
    username      TEXT NOT NULL,
    from_address  TEXT NOT NULL,
    implicit_tls  BOOLEAN NOT NULL,
    public_url    TEXT NOT NULL,
    password      %BLOB%,
    updated_at    BIGINT NOT NULL
);

-- The one-row database gate makes the test-send cooldown shared by instances
-- that use the same database, so an operator cannot bypass it by alternating
-- between replicas.
CREATE TABLE mail_test_limiter (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    last_sent_at  BIGINT NOT NULL
);

CREATE TABLE demo_things (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at BIGINT NOT NULL
);
CREATE INDEX ix_demo_things_created ON demo_things (created_at);

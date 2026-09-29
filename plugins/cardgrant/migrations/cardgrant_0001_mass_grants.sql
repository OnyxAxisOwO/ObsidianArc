-- Mass card grants history table.
CREATE TABLE mass_card_grants (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    windows         TEXT NOT NULL DEFAULT '',
    expires_at      BIGINT NOT NULL,
    recipient_count INTEGER NOT NULL,
    actor_id        TEXT NOT NULL,
    actor_username  TEXT NOT NULL DEFAULT '',
    created_at      BIGINT NOT NULL
);

CREATE INDEX ix_mass_card_grants_created ON mass_card_grants (created_at);

-- Proof-of-work salts this server has accepted.
--
-- A solved challenge stays valid for five minutes, and whoever solved it can
-- submit it again. The salt is spent here rather than in one process's memory,
-- so a copy cannot be used a second time on another instance sharing this
-- database. A row past its expiry can be dropped: the challenge it records is
-- refused by its own expiry check, so the row can no longer change an answer.
CREATE TABLE pow_salts (
    salt       TEXT PRIMARY KEY,
    expires_at BIGINT NOT NULL
);
CREATE INDEX ix_pow_salts_expires ON pow_salts (expires_at);

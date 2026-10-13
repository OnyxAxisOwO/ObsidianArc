-- Proof-of-work salts this server has accepted.
--
-- A solved challenge stays valid for five minutes, and whoever solved it can
-- submit it again. The salt is spent here rather than in one process's memory,
-- so a copy cannot be used a second time on another instance sharing this
-- database. A row is dropped a minute past its expiry, not at it: the challenge
-- it records is refused by its own expiry check on any instance whose clock is
-- within that minute, so the row can no longer change an answer there.
CREATE TABLE pow_salts (
    salt       TEXT PRIMARY KEY,
    expires_at BIGINT NOT NULL
);
CREATE INDEX ix_pow_salts_expires ON pow_salts (expires_at);

-- Sign-in states this server has already accepted at a callback.
--
-- A state is a ten-minute bearer ticket: the signed cookie carries its nonce
-- and PKCE verifier, and a copy of that cookie is as good as the original
-- until it expires. The callback clears the browser's copy, but nothing
-- reaches a copy somebody else kept, so the nonce is spent here instead.
--
-- The row is written before the provider is asked anything, so two callbacks
-- racing with the same state cannot both reach an account. A row past its
-- expiry can be dropped: the state it records is refused by its own expiry
-- check, so the row can no longer change an answer.
CREATE TABLE oauth_states (
    nonce      TEXT PRIMARY KEY,
    expires_at BIGINT NOT NULL
);
CREATE INDEX ix_oauth_states_expires ON oauth_states (expires_at);

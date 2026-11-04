-- The key an account's sign-up address is counted under for the per-address limit.
--
-- signup_ip keeps the address exactly as it was seen. An administrator reads it,
-- and a session records the same exact address, which is how an invite notices
-- an inviter signed in from the invitee's address. The limit groups an IPv6
-- address with its /64, as every other rate limit in the server does, so that
-- grouping lives in this column instead of in the address.
--
-- Accounts from before this column count under the address they stored. An IPv6
-- account from before the upgrade therefore counts alone until it leaves the
-- window, which is the same as it did before the grouping existed.
ALTER TABLE users ADD COLUMN signup_rate_key TEXT NOT NULL DEFAULT '';
UPDATE users SET signup_rate_key = signup_ip;
CREATE INDEX ix_users_signup_rate_key ON users (signup_rate_key, created_at);

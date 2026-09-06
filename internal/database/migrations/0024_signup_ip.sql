-- Where an account was created from.
--
-- The existing signup throttle is one counter for the whole instance, held in
-- memory. That stops a flood, but it stops it for everybody: one script
-- registering as fast as it can also locks out every real person for the
-- length of the window, and the counter is gone on restart and not shared
-- between two instances against one database.
--
-- Counting per address needs the address, and it has to survive both of
-- those. It lives on the account rather than in a table of its own because
-- the question is "how many accounts did this address make", and the accounts
-- are the answer — a separate log would have to be pruned, and could disagree
-- with the accounts it claims to count.
ALTER TABLE users ADD COLUMN signup_ip TEXT NOT NULL DEFAULT '';

-- The lookup is always an address and a time, and it runs on the registration
-- path, where a table scan per attempt is exactly what an attacker would
-- rather it did.
CREATE INDEX ix_users_signup_ip ON users (signup_ip, created_at);

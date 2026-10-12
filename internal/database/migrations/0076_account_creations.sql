-- When each account was opened through sign-up or a provider.
--
-- The instance-wide sign-up limit is counted from here, inside the transaction
-- that opens the account and under the instance lock it holds. It used to be
-- counted in each process's memory, which let every process behind one database
-- grant its own allowance. Rows older than an hour are dropped as the next one
-- is written, so the table holds about an hour of sign-ups.
CREATE TABLE account_creations (
    created_at BIGINT NOT NULL
);
CREATE INDEX ix_account_creations_created ON account_creations (created_at);

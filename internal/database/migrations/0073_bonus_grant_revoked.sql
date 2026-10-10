-- When an administrator took a grant back. Zero means it was never revoked.
--
-- A revoke cuts the grant's amount to what it had been used for, so nothing
-- is left to spend. Holds that were still in flight count as used, and their
-- turns give them back when they finish. Without a marker that refund raised
-- the amount-used gap again, and the revoked credit became spendable. The
-- marker is what the refund and the spend look at to tell a revoked grant
-- from one that merely has room.
ALTER TABLE bonus_grants ADD COLUMN revoked_at BIGINT NOT NULL DEFAULT 0;

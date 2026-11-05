-- A rewind invalidates replies still running on the previous branch.
ALTER TABLE conversations ADD COLUMN branch_revision BIGINT NOT NULL DEFAULT 0;

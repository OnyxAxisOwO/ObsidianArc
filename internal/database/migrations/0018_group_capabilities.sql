-- What a group's members may do with the interface, as opposed to which
-- models they may reach.
--
-- Both default to true, so every group that exists keeps behaving exactly as
-- it did: this adds a way to take something away, not a new thing to grant.
--
-- allow_stats gates the switch, not the figures. A member of a group without
-- it cannot turn the per-answer timing line on; the numbers are still
-- measured, still stored on the message, and still visible to an
-- administrator, because they are what the usage ledger is built from.

ALTER TABLE user_groups ADD COLUMN allow_stats BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_groups ADD COLUMN allow_delete_conversations BOOLEAN NOT NULL DEFAULT TRUE;

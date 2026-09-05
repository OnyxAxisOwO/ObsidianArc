-- What the model was called when the message was written.
--
-- Until now the transcript joined `models` for the name, which meant deleting
-- a model quietly unattributed every answer it had ever given. The usage
-- ledger already snapshots names for exactly this reason; the transcript
-- should too, because a conversation is the thing people keep.
--
-- Existing rows are backfilled from the join that used to supply it, so
-- history written before this migration keeps its attribution.

ALTER TABLE messages ADD COLUMN model_name TEXT NOT NULL DEFAULT '';

UPDATE messages
SET model_name = COALESCE((SELECT display_name FROM models WHERE models.id = messages.model_id), '')
WHERE model_id IS NOT NULL AND model_id <> '';
